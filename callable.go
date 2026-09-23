package tengo

import (
	"fmt"
	"sync/atomic"

	"github.com/d5/tengo/v2/parser"
)

// fnRuntime is the execution context a compiled function needs in order to
// run outside the VM: the constant pool its bytecode indexes, the globals of
// one compiled instance, and the source positions used to format runtime errors.
//
// A function moved into another compiled instance keeps the constant pool and
// file set it was compiled with. Only globals change. hybrids caches those
// mixed contexts on the destination instance.
type fnRuntime struct {
	constants []Object
	globals   []Object
	fileSet   *parser.SourceFileSet
	maxAllocs int64
	// bound caches per-constant function values for this instance. Cloning
	// shares bytecode, so each instance must materialize its own objects.
	bound []Object
	// hybrids maps a foreign runtime to one that uses that runtime's constants
	// and file set with this runtime's globals.
	hybrids map[*fnRuntime]*fnRuntime
}

func newFnRuntime(
	constants []Object,
	globals []Object,
	fileSet *parser.SourceFileSet,
	maxAllocs int64,
) *fnRuntime {
	return &fnRuntime{
		constants: constants,
		globals:   globals,
		fileSet:   fileSet,
		maxAllocs: maxAllocs,
	}
}

// boundFunction returns the instance-local copy of a function constant.
// Repeated loads in the same instance return the same object so recursion
// and tail calls keep their identity.
func (rt *fnRuntime) boundFunction(idx int, cf *CompiledFunction) *CompiledFunction {
	if cf == nil {
		return nil
	}
	if rt.bound == nil {
		rt.bound = make([]Object, len(rt.constants))
	}
	if idx >= 0 && idx < len(rt.bound) {
		if existing, ok := rt.bound[idx].(*CompiledFunction); ok && existing != nil {
			return existing
		}
	}
	bound := &CompiledFunction{
		Instructions:  cf.Instructions,
		NumLocals:     cf.NumLocals,
		NumParameters: cf.NumParameters,
		VarArgs:       cf.VarArgs,
		SourceMap:     cf.SourceMap,
		Free:          cf.Free,
		runtime:       rt,
	}
	if idx >= 0 && idx < len(rt.bound) {
		rt.bound[idx] = bound
	}
	return bound
}

// forInstance returns the runtime whose globals are dst's and whose constants
// are the ones fn's bytecode indexes. Functions compiled into dst already use
// dst. A function moved across compiled instances keeps its own constant pool.
func (dst *fnRuntime) forInstance(src *fnRuntime) *fnRuntime {
	if dst == nil {
		return src
	}
	if src == nil || src == dst || sameConstants(src, dst) {
		return dst
	}
	if dst.hybrids != nil {
		if h, ok := dst.hybrids[src]; ok {
			return h
		}
	} else {
		dst.hybrids = make(map[*fnRuntime]*fnRuntime)
	}
	h := &fnRuntime{
		constants: src.constants,
		globals:   dst.globals,
		fileSet:   src.fileSet,
		maxAllocs: dst.maxAllocs,
	}
	dst.hybrids[src] = h
	return h
}

func sameConstants(a, b *fnRuntime) bool {
	if a == nil || b == nil || len(a.constants) != len(b.constants) {
		return false
	}
	if len(a.constants) == 0 {
		return a.fileSet == b.fileSet
	}
	return &a.constants[0] == &b.constants[0]
}

// activeRuntime is the constant pool and globals the instruction pointer is
// executing against. The running frame wins so a function compiled for another
// script does not read this VM's constants.
func (v *VM) activeRuntime() *fnRuntime {
	if v.curFrame != nil && v.curFrame.fn != nil && v.curFrame.fn.runtime != nil {
		return v.curFrame.fn.runtime
	}
	return v.runtime
}

func (v *VM) constantObject(idx int) Object {
	rt := v.activeRuntime()
	constants := v.constants
	if rt != nil {
		constants = rt.constants
	}
	if idx < 0 || idx >= len(constants) {
		return UndefinedValue
	}
	cn := constants[idx]
	cf, ok := cn.(*CompiledFunction)
	if !ok || rt == nil {
		return cn
	}
	return rt.boundFunction(idx, cf)
}

func callCompiledFunction(fn *CompiledFunction, args ...Object) (Object, error) {
	if fn == nil || fn.runtime == nil {
		return nil, fmt.Errorf("Runtime Error: compiled function is not bound to a script\n\tat -")
	}
	prepared, argErr := arrangeCallArgs(fn, args)
	v := fn.runtime.vmForCall(fn)
	if argErr != nil {
		v.err = argErr
		return nil, v.wrapRuntimeError()
	}
	if fn.NumLocals < 0 || 1+fn.NumLocals > StackSize {
		v.err = ErrStackOverflow
		return nil, v.wrapRuntimeError()
	}
	v.stack[0] = fn
	for i, arg := range prepared {
		v.stack[1+i] = arg
	}
	v.sp = 1 + fn.NumLocals
	v.frames[0].fn = fn
	v.frames[0].freeVars = fn.Free
	v.frames[0].basePointer = 1
	v.frames[0].ip = -1
	v.curFrame = &v.frames[0]
	v.curInsts = fn.Instructions
	v.framesIndex = 1
	v.ip = -1
	v.allocs = v.maxAllocs + 1

	v.run()
	atomic.StoreInt64(&v.aborting, 0)
	if v.err != nil {
		return nil, v.wrapRuntimeError()
	}
	var ret Object
	if v.sp > 0 {
		ret = v.stack[0]
	}
	if ret == nil {
		ret = UndefinedValue
	}
	return ret, nil
}

func (rt *fnRuntime) vmForCall(fn *CompiledFunction) *VM {
	globals := rt.globals
	if globals == nil {
		globals = make([]Object, GlobalsSize)
	}
	v := &VM{
		constants:   rt.constants,
		globals:     globals,
		fileSet:     rt.fileSet,
		maxAllocs:   rt.maxAllocs,
		runtime:     rt,
		framesIndex: 1,
		ip:          -1,
	}
	v.frames[0].fn = fn
	v.frames[0].freeVars = fn.Free
	v.frames[0].basePointer = 1
	v.frames[0].ip = -1
	v.curFrame = &v.frames[0]
	if fn != nil {
		v.curInsts = fn.Instructions
	}
	return v
}

// arrangeCallArgs applies the same variadic rolling rules as OpCall.
func arrangeCallArgs(fn *CompiledFunction, args []Object) ([]Object, error) {
	n := len(args)
	if fn.VarArgs && fn.NumParameters > 0 {
		realArgs := fn.NumParameters - 1
		extra := n - realArgs
		if extra >= 0 {
			packed := make([]Object, extra)
			copy(packed, args[realArgs:])
			rolled := make([]Object, realArgs+1)
			copy(rolled, args[:realArgs])
			rolled[realArgs] = &Array{Value: packed}
			args = rolled
		}
	}
	if len(args) != fn.NumParameters {
		if fn.VarArgs {
			return nil, fmt.Errorf(
				"wrong number of arguments: want>=%d, got=%d",
				fn.NumParameters-1, n)
		}
		return nil, fmt.Errorf(
			"wrong number of arguments: want=%d, got=%d",
			fn.NumParameters, n)
	}
	return args, nil
}

// rebindState copies values onto a destination runtime.
// deep is true for Clone, which copies every global.
// Set uses deep=false and copies only containers that hold foreign callables.
type rebindState struct {
	rt    *fnRuntime
	seen  map[Object]Object
	cells map[*ObjectPtr]*ObjectPtr
}

func rebindToRuntime(o Object, rt *fnRuntime, deep bool) Object {
	if o == nil || rt == nil {
		return o
	}
	st := &rebindState{
		rt:    rt,
		seen:  make(map[Object]Object),
		cells: make(map[*ObjectPtr]*ObjectPtr),
	}
	return st.rebind(o, deep, deep)
}

// rebind copies o onto st.rt.
// deep copies every value and matches Clone's immutable-to-mutable Copy.
// force copies this value even when it holds no foreign callable, so a
// container that is copied does not keep aliasing the source's children.
func (st *rebindState) rebind(o Object, deep, force bool) Object {
	if o == nil {
		return nil
	}
	if prev, ok := st.seen[o]; ok {
		return prev
	}
	switch o := o.(type) {
	case *CompiledFunction:
		// Same-instance functions keep their identity unless this value is being
		// copied into a container that must not alias the source.
		if o.runtime == st.rt && !deep && !force {
			return o
		}
		nf := &CompiledFunction{
			Instructions:  o.Instructions,
			NumLocals:     o.NumLocals,
			NumParameters: o.NumParameters,
			VarArgs:       o.VarArgs,
			SourceMap:     o.SourceMap,
			runtime:       st.rt.forInstance(o.runtime),
		}
		st.seen[o] = nf
		nf.Free = st.snapshotFree(o.Free)
		return nf
	case *Array:
		if !force && !deep && !hasForeignCallable(o, st.rt, make(map[Object]bool)) {
			return o
		}
		na := &Array{Value: make([]Object, len(o.Value))}
		st.seen[o] = na
		for i, el := range o.Value {
			na.Value[i] = st.rebind(el, deep, true)
		}
		return na
	case *ImmutableArray:
		if !force && !deep && !hasForeignCallable(o, st.rt, make(map[Object]bool)) {
			return o
		}
		vals := make([]Object, len(o.Value))
		var obj Object
		if deep {
			// Match ImmutableArray.Copy, which returns a mutable array.
			obj = &Array{Value: vals}
		} else {
			obj = &ImmutableArray{Value: vals}
		}
		st.seen[o] = obj
		for i, el := range o.Value {
			vals[i] = st.rebind(el, deep, true)
		}
		return obj
	case *Map:
		if !force && !deep && !hasForeignCallable(o, st.rt, make(map[Object]bool)) {
			return o
		}
		nm := &Map{Value: make(map[string]Object, len(o.Value))}
		st.seen[o] = nm
		for k, el := range o.Value {
			nm.Value[k] = st.rebind(el, deep, true)
		}
		return nm
	case *ImmutableMap:
		if !force && !deep && !hasForeignCallable(o, st.rt, make(map[Object]bool)) {
			return o
		}
		vals := make(map[string]Object, len(o.Value))
		var obj Object
		if deep {
			// Match ImmutableMap.Copy, which returns a mutable map.
			obj = &Map{Value: vals}
		} else {
			obj = &ImmutableMap{Value: vals}
		}
		st.seen[o] = obj
		for k, el := range o.Value {
			vals[k] = st.rebind(el, deep, true)
		}
		return obj
	case *Error:
		if !force && !deep && !hasForeignCallable(o, st.rt, make(map[Object]bool)) {
			return o
		}
		ne := &Error{}
		st.seen[o] = ne
		ne.Value = st.rebind(o.Value, deep, true)
		return ne
	default:
		if !deep && !force {
			return o
		}
		copied := o.Copy()
		if copied == nil {
			return o
		}
		return copied
	}
}

// snapshotFree copies captured cells as they are at transfer time.
// Closures that shared a cell keep sharing the transferred cell.
// Globals are not part of the cell; they resolve through the destination runtime.
func (st *rebindState) snapshotFree(free []*ObjectPtr) []*ObjectPtr {
	if len(free) == 0 {
		return nil
	}
	out := make([]*ObjectPtr, len(free))
	for i, f := range free {
		if f == nil {
			continue
		}
		if existing, ok := st.cells[f]; ok {
			out[i] = existing
			continue
		}
		var cur Object
		if f.Value != nil {
			cur = *f.Value
		}
		copied := st.rebind(cur, true, true)
		cellVal := copied
		cell := &ObjectPtr{Value: &cellVal}
		st.cells[f] = cell
		out[i] = cell
	}
	return out
}

func hasForeignCallable(o Object, rt *fnRuntime, seen map[Object]bool) bool {
	if o == nil || seen[o] {
		return false
	}
	seen[o] = true
	switch o := o.(type) {
	case *CompiledFunction:
		if o.runtime != rt {
			return true
		}
		for _, f := range o.Free {
			if f != nil && f.Value != nil && hasForeignCallable(*f.Value, rt, seen) {
				return true
			}
		}
	case *Array:
		for _, el := range o.Value {
			if hasForeignCallable(el, rt, seen) {
				return true
			}
		}
	case *ImmutableArray:
		for _, el := range o.Value {
			if hasForeignCallable(el, rt, seen) {
				return true
			}
		}
	case *Map:
		for _, el := range o.Value {
			if hasForeignCallable(el, rt, seen) {
				return true
			}
		}
	case *ImmutableMap:
		for _, el := range o.Value {
			if hasForeignCallable(el, rt, seen) {
				return true
			}
		}
	case *Error:
		return hasForeignCallable(o.Value, rt, seen)
	}
	return false
}
