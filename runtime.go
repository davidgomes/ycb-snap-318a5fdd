package tengo

import (
	"github.com/d5/tengo/v2/parser"
)

// program is a compiled bytecode together with the names of its globals.
type program struct {
	bytecode      *Bytecode
	globalIndexes map[string]int
}

// runtime binds a program to the globals it executes against. Every
// CompiledFunction created by the VM carries the runtime it was created in so
// that it can be executed outside of that VM.
type runtime struct {
	prog *program

	// constants are the program constants where compiled functions are
	// replaced with copies bound to this runtime.
	constants []Object
	globals   []Object

	// globalMap translates program global indexes into indexes of globals
	// (non-negative) or of extra (-index-1). nil means program global indexes
	// address globals directly.
	globalMap []int
	extra     []Object
	maxAllocs int64
}

func newRuntime(
	prog *program,
	globals []Object,
	maxAllocs int64,
) *runtime {
	rt := &runtime{
		prog:      prog,
		globals:   globals,
		maxAllocs: maxAllocs,
	}
	rt.bindConstants()
	return rt
}

func (rt *runtime) bindConstants() {
	constants := rt.prog.bytecode.Constants
	rt.constants = constants
	copied := false
	for i, c := range constants {
		fn, ok := c.(*CompiledFunction)
		if !ok {
			continue
		}
		if !copied {
			rt.constants = append([]Object(nil), constants...)
			copied = true
		}
		rt.constants[i] = fn.bind(rt)
	}
}

func (rt *runtime) fileSet() *parser.SourceFileSet {
	return rt.prog.bytecode.FileSet
}

func (rt *runtime) numGlobals() int {
	if rt.globalMap != nil {
		return len(rt.globalMap)
	}
	return len(rt.globals)
}

// globalRef returns the storage of the program global at index idx.
func (rt *runtime) globalRef(idx int) *Object {
	if rt.globalMap == nil {
		return &rt.globals[idx]
	}
	j := rt.globalMap[idx]
	if j >= 0 {
		return &rt.globals[j]
	}
	return &rt.extra[-j-1]
}

// runtimeFor returns the runtime of c that executes functions of src's
// program. Globals are resolved by name against c; program globals that c
// does not define get private storage initialized with src's current values.
func (c *Compiled) runtimeFor(src *runtime, t *transfer) *runtime {
	if src.prog == c.rt.prog {
		return c.rt
	}
	if rt, ok := c.foreign[src.prog]; ok {
		return rt
	}

	names := make(map[int]string, len(src.prog.globalIndexes))
	for name, idx := range src.prog.globalIndexes {
		names[idx] = name
	}
	rt := &runtime{
		prog:      src.prog,
		globals:   c.globals,
		globalMap: make([]int, src.numGlobals()),
		maxAllocs: c.maxAllocs,
	}
	var missing []int
	for idx := range rt.globalMap {
		if name, ok := names[idx]; ok {
			if j, ok := c.globalIndexes[name]; ok {
				rt.globalMap[idx] = j
				continue
			}
		}
		rt.globalMap[idx] = -len(missing) - 1
		missing = append(missing, idx)
	}
	rt.extra = make([]Object, len(missing))
	rt.bindConstants()

	if c.foreign == nil {
		c.foreign = make(map[*program]*runtime)
	}
	c.foreign[src.prog] = rt
	for k, idx := range missing {
		if g := *src.globalRef(idx); g != nil {
			rt.extra[k] = t.value(g)
		}
	}
	return rt
}

func (c *Compiled) owns(rt *runtime) bool {
	return rt == c.rt || c.foreign[rt.prog] == rt
}

// needsTransfer reports whether o reaches a compiled function bound to a
// runtime not owned by c.
func (c *Compiled) needsTransfer(o Object, seen map[Object]bool) bool {
	switch o := o.(type) {
	case *CompiledFunction:
		return o.rt != nil && !c.owns(o.rt)
	case *Array:
		if seen[o] {
			return false
		}
		seen[o] = true
		for _, e := range o.Value {
			if c.needsTransfer(e, seen) {
				return true
			}
		}
	case *ImmutableArray:
		if seen[o] {
			return false
		}
		seen[o] = true
		for _, e := range o.Value {
			if c.needsTransfer(e, seen) {
				return true
			}
		}
	case *Map:
		if seen[o] {
			return false
		}
		seen[o] = true
		for _, e := range o.Value {
			if c.needsTransfer(e, seen) {
				return true
			}
		}
	case *ImmutableMap:
		if seen[o] {
			return false
		}
		seen[o] = true
		for _, e := range o.Value {
			if c.needsTransfer(e, seen) {
				return true
			}
		}
	case *Error:
		if seen[o] {
			return false
		}
		seen[o] = true
		return c.needsTransfer(o.Value, seen)
	}
	return false
}

// transfer copies values into dst so that every reachable compiled function
// is bound to a runtime of dst and holds a snapshot of its captured
// variables. Aliasing between the copied values is preserved.
type transfer struct {
	dst *Compiled

	// clone rebinds every bound function and copies every other value, as
	// opposed to only rebinding functions bound to runtimes dst doesn't own.
	clone bool
	objs  map[Object]Object
	ptrs  map[*ObjectPtr]*ObjectPtr
}

func newTransfer(dst *Compiled, clone bool) *transfer {
	return &transfer{
		dst:   dst,
		clone: clone,
		objs:  make(map[Object]Object),
		ptrs:  make(map[*ObjectPtr]*ObjectPtr),
	}
}

func (t *transfer) value(o Object) Object {
	switch o := o.(type) {
	case nil:
		return nil
	case *CompiledFunction:
		if o.rt == nil || (!t.clone && t.dst.owns(o.rt)) {
			break
		}
		if c, ok := t.objs[o]; ok {
			return c
		}
		fn := &CompiledFunction{
			Instructions:  o.Instructions,
			NumLocals:     o.NumLocals,
			NumParameters: o.NumParameters,
			VarArgs:       o.VarArgs,
			SourceMap:     o.SourceMap,
		}
		t.objs[o] = fn
		fn.rt = t.dst.runtimeFor(o.rt, t)
		if o.Free != nil {
			fn.Free = make([]*ObjectPtr, len(o.Free))
			for i, p := range o.Free {
				fn.Free[i] = t.ptr(p)
			}
		}
		return fn
	case *Array:
		if c, ok := t.objs[o]; ok {
			return c
		}
		arr := &Array{Value: make([]Object, len(o.Value))}
		t.objs[o] = arr
		for i, e := range o.Value {
			arr.Value[i] = t.value(e)
		}
		return arr
	case *ImmutableArray:
		if c, ok := t.objs[o]; ok {
			return c
		}
		arr := &ImmutableArray{Value: make([]Object, len(o.Value))}
		t.objs[o] = arr
		for i, e := range o.Value {
			arr.Value[i] = t.value(e)
		}
		return arr
	case *Map:
		if c, ok := t.objs[o]; ok {
			return c
		}
		m := &Map{Value: make(map[string]Object, len(o.Value))}
		t.objs[o] = m
		for k, e := range o.Value {
			m.Value[k] = t.value(e)
		}
		return m
	case *ImmutableMap:
		if c, ok := t.objs[o]; ok {
			return c
		}
		m := &ImmutableMap{Value: make(map[string]Object, len(o.Value))}
		t.objs[o] = m
		for k, e := range o.Value {
			m.Value[k] = t.value(e)
		}
		return m
	case *Error:
		if c, ok := t.objs[o]; ok {
			return c
		}
		e := &Error{}
		t.objs[o] = e
		e.Value = t.value(o.Value)
		return e
	}
	if t.clone {
		return o.Copy()
	}
	return o
}

func (t *transfer) ptr(p *ObjectPtr) *ObjectPtr {
	if p == nil {
		return nil
	}
	if c, ok := t.ptrs[p]; ok {
		return c
	}
	c := &ObjectPtr{}
	t.ptrs[p] = c
	if p.Value != nil {
		v := t.value(*p.Value)
		c.Value = &v
	}
	return c
}

// adopt returns o, or a copy of o transferred by t (or by a new transfer if t
// is nil) if it reaches compiled functions bound to runtimes c doesn't own.
func (c *Compiled) adopt(o Object, t *transfer) Object {
	if !c.needsTransfer(o, make(map[Object]bool)) {
		return o
	}
	if t == nil {
		t = newTransfer(c, false)
	}
	return t.value(o)
}
