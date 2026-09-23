package tengo

import (
	"fmt"

	"github.com/d5/tengo/v2/parser"
)

// call runs a compiled function with the globals, constants, and free
// variables it was bound to.
func (o *CompiledFunction) call(args []Object) (Object, error) {
	if o.globals == nil || o.fileSet == nil {
		return nil, fmt.Errorf(
			"Runtime Error: compiled function is not bound to a runtime")
	}
	prepared, err := o.prepareArgs(args)
	if err != nil {
		filePos := o.fileSet.Position(o.SourcePos(0))
		return nil, fmt.Errorf("Runtime Error: %w\n\tat %s", err, filePos)
	}
	if len(o.Instructions) == 0 {
		return nil, fmt.Errorf(
			"Runtime Error: compiled function has no instructions")
	}

	vm := NewVM(&Bytecode{
		FileSet:      o.fileSet,
		MainFunction: o,
		Constants:    o.constants,
	}, o.globals, o.maxAllocs)
	return vm.invoke(o, prepared)
}

func (o *CompiledFunction) prepareArgs(args []Object) ([]Object, error) {
	n := len(args)
	if o.VarArgs {
		fixed := o.NumParameters - 1
		if fixed < 0 {
			fixed = 0
		}
		if n < fixed {
			return nil, fmt.Errorf(
				"wrong number of arguments: want>=%d, got=%d", fixed, n)
		}
		packed := make([]Object, fixed+1)
		for i := 0; i < fixed; i++ {
			packed[i] = argOrUndefined(args[i])
		}
		rest := make([]Object, n-fixed)
		for i := range rest {
			rest[i] = argOrUndefined(args[fixed+i])
		}
		packed[fixed] = &Array{Value: rest}
		return packed, nil
	}
	if n != o.NumParameters {
		return nil, fmt.Errorf(
			"wrong number of arguments: want=%d, got=%d", o.NumParameters, n)
	}
	if n == 0 {
		return nil, nil
	}
	prepared := make([]Object, n)
	for i, arg := range args {
		prepared[i] = argOrUndefined(arg)
	}
	return prepared, nil
}

func argOrUndefined(arg Object) Object {
	if arg == nil {
		return UndefinedValue
	}
	return arg
}

// invoke executes fn as the root frame. args are already packed to match
// NumParameters, including a trailing array for variadic functions.
func (v *VM) invoke(fn *CompiledFunction, args []Object) (Object, error) {
	localN := fn.NumLocals
	if localN < len(args) {
		localN = len(args)
	}
	for i := 0; i < localN; i++ {
		v.stack[i] = nil
	}
	for i, arg := range args {
		v.stack[i] = arg
	}

	v.frames[0] = frame{
		fn:          fn,
		freeVars:    fn.Free,
		ip:          -1,
		basePointer: 0,
	}
	v.curFrame = &v.frames[0]
	v.curInsts = fn.Instructions
	v.framesIndex = 1
	v.ip = -1
	v.sp = localN
	v.allocs = v.maxAllocs + 1
	v.err = nil
	v.run()

	if err := v.runtimeError(); err != nil {
		return nil, err
	}
	if v.sp == 0 || v.stack[v.sp-1] == nil {
		return UndefinedValue, nil
	}
	return v.stack[v.sp-1], nil
}

// materialize binds constant compiled functions to this VM. Closures created
// by OpClosure are already bound and are returned as-is. Unbound function
// constants are interned so tail-calls keep pointer identity.
func (v *VM) materialize(obj Object) Object {
	fn, ok := obj.(*CompiledFunction)
	if !ok {
		return obj
	}
	if fn.globals != nil && sameGlobals(fn.globals, v.globals) {
		return fn
	}
	if len(fn.Free) > 0 {
		return v.bindFunction(fn, fn.Free)
	}
	if v.funcCache == nil {
		v.funcCache = make(map[*CompiledFunction]*CompiledFunction)
	} else if cached, ok := v.funcCache[fn]; ok {
		return cached
	}
	bound := v.bindFunction(fn, nil)
	v.funcCache[fn] = bound
	return bound
}

func (v *VM) bindFunction(
	fn *CompiledFunction,
	free []*ObjectPtr,
) *CompiledFunction {
	return &CompiledFunction{
		Instructions:  fn.Instructions,
		NumLocals:     fn.NumLocals,
		NumParameters: fn.NumParameters,
		VarArgs:       fn.VarArgs,
		SourceMap:     fn.SourceMap,
		Free:          free,
		globals:       v.globals,
		constants:     v.constants,
		fileSet:       v.fileSet,
		maxAllocs:     v.maxAllocs,
	}
}

func sameGlobals(a, b []Object) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return &a[0] == &b[0]
}

// copyGlobal copies one global for Compiled.Clone. One rebinder walks every
// global so a closure capture and another global that point at the same
// object still point at the same copy. Compiled functions are rebound onto
// the clone.
func (c *Compiled) copyGlobal(obj Object, rb *rebinder) Object {
	if obj == nil {
		return nil
	}
	return rb.isolate(obj)
}

// isolateAssigned copies values that carry compiled functions from another
// runtime. Values already bound to this compiled instance are stored as-is.
func (c *Compiled) isolateAssigned(obj Object) Object {
	if obj == nil || !hasForeignFunction(obj, c.globals, map[Object]bool{}) {
		return obj
	}
	return newRebinder(c).isolate(obj)
}

type rebinder struct {
	globals   []Object
	constants []Object
	fileSet   *parser.SourceFileSet
	maxAllocs int64
	seen      map[Object]Object
	ptrs      map[*ObjectPtr]*ObjectPtr
}

func newRebinder(c *Compiled) *rebinder {
	r := &rebinder{
		globals:   c.globals,
		maxAllocs: c.maxAllocs,
		seen:      make(map[Object]Object),
		ptrs:      make(map[*ObjectPtr]*ObjectPtr),
	}
	if c.bytecode != nil {
		r.constants = c.bytecode.Constants
		r.fileSet = c.bytecode.FileSet
	}
	return r
}

func (r *rebinder) isolate(obj Object) Object {
	if obj == nil {
		return nil
	}
	if prev, ok := r.seen[obj]; ok {
		return prev
	}
	switch o := obj.(type) {
	case *CompiledFunction:
		free := o.Free
		if len(o.Free) > 0 {
			free = make([]*ObjectPtr, len(o.Free))
		}
		constants := o.constants
		if constants == nil {
			constants = r.constants
		}
		fileSet := o.fileSet
		if fileSet == nil {
			fileSet = r.fileSet
		}
		nf := &CompiledFunction{
			Instructions:  o.Instructions,
			NumLocals:     o.NumLocals,
			NumParameters: o.NumParameters,
			VarArgs:       o.VarArgs,
			SourceMap:     o.SourceMap,
			Free:          free,
			globals:       r.globals,
			constants:     constants,
			fileSet:       fileSet,
			maxAllocs:     r.maxAllocs,
		}
		r.seen[obj] = nf
		for i, p := range o.Free {
			nf.Free[i] = r.isolatePtr(p)
		}
		return nf
	case *Array:
		na := &Array{Value: make([]Object, len(o.Value))}
		r.seen[obj] = na
		for i, el := range o.Value {
			na.Value[i] = r.isolate(el)
		}
		return na
	case *ImmutableArray:
		na := &ImmutableArray{Value: make([]Object, len(o.Value))}
		r.seen[obj] = na
		for i, el := range o.Value {
			na.Value[i] = r.isolate(el)
		}
		return na
	case *Map:
		nm := &Map{Value: make(map[string]Object, len(o.Value))}
		r.seen[obj] = nm
		for k, v := range o.Value {
			nm.Value[k] = r.isolate(v)
		}
		return nm
	case *ImmutableMap:
		nm := &ImmutableMap{Value: make(map[string]Object, len(o.Value))}
		r.seen[obj] = nm
		for k, v := range o.Value {
			nm.Value[k] = r.isolate(v)
		}
		return nm
	case *Error:
		ne := &Error{}
		r.seen[obj] = ne
		if o.Value != nil {
			ne.Value = r.isolate(o.Value)
		}
		return ne
	default:
		cpy := o.Copy()
		r.seen[obj] = cpy
		return cpy
	}
}

func (r *rebinder) isolatePtr(p *ObjectPtr) *ObjectPtr {
	if p == nil {
		return nil
	}
	if np, ok := r.ptrs[p]; ok {
		return np
	}
	np := &ObjectPtr{}
	r.ptrs[p] = np
	if p.Value != nil {
		v := r.isolate(*p.Value)
		np.Value = &v
	}
	return np
}

func hasForeignFunction(
	obj Object,
	globals []Object,
	seen map[Object]bool,
) bool {
	if obj == nil || seen[obj] {
		return false
	}
	seen[obj] = true
	switch o := obj.(type) {
	case *CompiledFunction:
		if o.globals == nil || !sameGlobals(o.globals, globals) {
			return true
		}
		for _, p := range o.Free {
			if p != nil && p.Value != nil &&
				hasForeignFunction(*p.Value, globals, seen) {
				return true
			}
		}
		return false
	case *Array:
		for _, v := range o.Value {
			if hasForeignFunction(v, globals, seen) {
				return true
			}
		}
	case *ImmutableArray:
		for _, v := range o.Value {
			if hasForeignFunction(v, globals, seen) {
				return true
			}
		}
	case *Map:
		for _, v := range o.Value {
			if hasForeignFunction(v, globals, seen) {
				return true
			}
		}
	case *ImmutableMap:
		for _, v := range o.Value {
			if hasForeignFunction(v, globals, seen) {
				return true
			}
		}
	case *Error:
		return hasForeignFunction(o.Value, globals, seen)
	}
	return false
}
