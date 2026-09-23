package tengo

import (
	"errors"
	"fmt"

	"github.com/d5/tengo/v2/parser"
)

// vmRuntime is the execution environment a compiled function is bound to.
type vmRuntime struct {
	constants []Object
	globals   []Object
	fileSet   *parser.SourceFileSet
	maxAllocs int64
}

func newVMRuntime(
	constants []Object,
	globals []Object,
	fileSet *parser.SourceFileSet,
	maxAllocs int64,
) *vmRuntime {
	rt := &vmRuntime{
		globals:   globals,
		fileSet:   fileSet,
		maxAllocs: maxAllocs,
	}
	rt.constants = make([]Object, len(constants))
	for i, c := range constants {
		if fn, ok := c.(*CompiledFunction); ok {
			bound := *fn
			bound.rt = rt
			c = &bound
		}
		rt.constants[i] = c
	}
	return rt
}

func (rt *vmRuntime) sharesGlobals(o *vmRuntime) bool {
	if rt == o {
		return true
	}
	if len(rt.globals) == 0 || len(o.globals) == 0 {
		return false
	}
	return &rt.globals[0] == &o.globals[0]
}

// call executes fn with args in a fresh VM bound to rt.
func (rt *vmRuntime) call(fn *CompiledFunction, args []Object) (Object, error) {
	main := &CompiledFunction{
		Instructions: []byte{parser.OpCall, 1, 1, parser.OpSuspend},
	}
	v := newVMFromRuntime(rt, main)
	v.stack[0] = fn
	v.stack[1] = &Array{Value: append([]Object{}, args...)}
	v.sp = 2
	v.allocs = v.maxAllocs + 1
	v.run()
	if v.err != nil {
		return nil, v.formatError(v.err, 1)
	}
	ret := v.stack[v.sp-1]
	if ret == nil {
		ret = UndefinedValue
	}
	return ret, nil
}

var errUnboundFunction = errors.New(
	"not callable: compiled-function is not bound to a runtime")

// Call executes the compiled function (or closure) outside of the VM using
// the globals, constants, and captured variables it is bound to.
func (o *CompiledFunction) Call(args ...Object) (Object, error) {
	if o.rt == nil {
		return nil, errUnboundFunction
	}
	return o.rt.call(o, args)
}

// transferer rebinds callables reachable from objects onto a destination
// runtime so the destination never shares state with the source.
type transferer struct {
	dest *vmRuntime
	rts  map[*vmRuntime]*vmRuntime
	memo map[interface{}]interface{}
}

func newTransferer(dest *vmRuntime) *transferer {
	return &transferer{
		dest: dest,
		rts:  make(map[*vmRuntime]*vmRuntime),
		memo: make(map[interface{}]interface{}),
	}
}

func (t *transferer) owns(rt *vmRuntime) bool {
	return rt == nil || t.dest.sharesGlobals(rt)
}

func (t *transferer) runtimeFor(rt *vmRuntime) *vmRuntime {
	if rt == nil {
		return nil
	}
	if r, ok := t.rts[rt]; ok {
		return r
	}
	if t.owns(rt) {
		return rt
	}
	r := newVMRuntime(rt.constants, t.dest.globals, rt.fileSet,
		t.dest.maxAllocs)
	t.rts[rt] = r
	return r
}

func (t *transferer) foreign(o Object, seen map[Object]bool) bool {
	if o == nil || seen[o] {
		return false
	}
	switch o := o.(type) {
	case *CompiledFunction:
		seen[o] = true
		if _, remapped := t.rts[o.rt]; remapped || !t.owns(o.rt) {
			return true
		}
		for _, p := range o.Free {
			if p != nil && p.Value != nil && t.foreign(*p.Value, seen) {
				return true
			}
		}
	case *Array:
		seen[o] = true
		for _, e := range o.Value {
			if t.foreign(e, seen) {
				return true
			}
		}
	case *ImmutableArray:
		seen[o] = true
		for _, e := range o.Value {
			if t.foreign(e, seen) {
				return true
			}
		}
	case *Map:
		seen[o] = true
		for _, e := range o.Value {
			if t.foreign(e, seen) {
				return true
			}
		}
	case *ImmutableMap:
		seen[o] = true
		for _, e := range o.Value {
			if t.foreign(e, seen) {
				return true
			}
		}
	case *Error:
		seen[o] = true
		return t.foreign(o.Value, seen)
	}
	return false
}

// transfer returns o rebound to the destination runtime. With force set,
// every container and callable is copied; otherwise only the ones that
// (transitively) reference a foreign runtime are.
func (t *transferer) transfer(o Object, force bool) Object {
	if o == nil {
		return nil
	}
	if r, ok := t.memo[o]; ok {
		return r.(Object)
	}
	if !force && !t.foreign(o, make(map[Object]bool)) {
		return o
	}
	switch o := o.(type) {
	case *CompiledFunction:
		cp := *o
		cp.rt = t.runtimeFor(o.rt)
		t.memo[o] = &cp
		// captures of a function leaving its runtime are snapshotted
		snapshot := force || cp.rt != o.rt
		cp.Free = make([]*ObjectPtr, len(o.Free))
		for i, p := range o.Free {
			cp.Free[i] = t.transferPtr(p, snapshot)
		}
		return &cp
	case *Array:
		cp := &Array{Value: make([]Object, len(o.Value))}
		t.memo[o] = cp
		for i, e := range o.Value {
			cp.Value[i] = t.transfer(e, force)
		}
		return cp
	case *ImmutableArray:
		cp := &ImmutableArray{Value: make([]Object, len(o.Value))}
		t.memo[o] = cp
		for i, e := range o.Value {
			cp.Value[i] = t.transfer(e, force)
		}
		return cp
	case *Map:
		cp := &Map{Value: make(map[string]Object, len(o.Value))}
		t.memo[o] = cp
		for k, e := range o.Value {
			cp.Value[k] = t.transfer(e, force)
		}
		return cp
	case *ImmutableMap:
		cp := &ImmutableMap{Value: make(map[string]Object, len(o.Value))}
		t.memo[o] = cp
		for k, e := range o.Value {
			cp.Value[k] = t.transfer(e, force)
		}
		return cp
	case *Error:
		cp := &Error{}
		t.memo[o] = cp
		cp.Value = t.transfer(o.Value, force)
		return cp
	default:
		return o.Copy()
	}
}

func (t *transferer) transferPtr(p *ObjectPtr, force bool) *ObjectPtr {
	if p == nil {
		return nil
	}
	if r, ok := t.memo[p]; ok {
		return r.(*ObjectPtr)
	}
	if !force && (p.Value == nil ||
		!t.foreign(*p.Value, make(map[Object]bool))) {
		return p
	}
	np := &ObjectPtr{Value: new(Object)}
	t.memo[p] = np
	if p.Value != nil {
		*np.Value = t.transfer(*p.Value, force)
	}
	return np
}

func (v *VM) formatError(err error, minFrames int) error {
	if v.framesIndex <= minFrames {
		return fmt.Errorf("Runtime Error: %w", err)
	}
	filePos := v.fileSet.Position(v.curFrame.fn.SourcePos(v.ip - 1))
	err = fmt.Errorf("Runtime Error: %w\n\tat %s", err, filePos)
	for v.framesIndex > minFrames+1 {
		v.framesIndex--
		v.curFrame = &v.frames[v.framesIndex-1]
		filePos = v.fileSet.Position(
			v.curFrame.fn.SourcePos(v.curFrame.ip - 1))
		err = fmt.Errorf("%w\n\tat %s", err, filePos)
	}
	return err
}
