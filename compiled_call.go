package tengo

import (
	"fmt"

	"github.com/d5/tengo/v2/parser"
)

// callCompiledFunction runs fn from Go with the same argument packing,
// globals, imports, and error formatting as an in-script call.
func callCompiledFunction(fn *CompiledFunction, args ...Object) (Object, error) {
	prepared, err := prepareCompiledArgs(fn, args)
	if err != nil {
		return nil, wrapCompiledCallError(fn, err)
	}

	globals := fn.globals
	if globals == nil {
		globals = make([]Object, GlobalsSize)
	}
	fileSet := fn.fileSet
	if fileSet == nil {
		fileSet = parser.NewFileSet()
	}
	v := &VM{
		constants: fn.constants,
		globals:   globals,
		fileSet:   fileSet,
		maxAllocs: fn.maxAllocs,
	}
	return v.runCompiled(fn, prepared)
}

// runCompiled executes fn as the root frame with args already packed into
// local slots (including a variadic array when applicable).
func (v *VM) runCompiled(fn *CompiledFunction, args []Object) (Object, error) {
	v.sp = 0
	v.frames[0].fn = fn
	v.frames[0].freeVars = fn.Free
	v.frames[0].ip = -1
	v.frames[0].basePointer = 0
	v.curFrame = &v.frames[0]
	v.curInsts = fn.Instructions
	v.framesIndex = 1
	v.ip = -1
	v.allocs = v.maxAllocs + 1
	v.err = nil
	v.aborting = 0

	for i := 0; i < len(args); i++ {
		v.stack[i] = args[i]
	}
	for i := len(args); i < fn.NumLocals; i++ {
		v.stack[i] = nil
	}
	if fn.NumLocals > 0 {
		v.sp = fn.NumLocals
	}

	v.run()
	if err := v.runtimeError(); err != nil {
		return nil, err
	}
	if v.sp == 0 {
		return UndefinedValue, nil
	}
	ret := v.stack[v.sp-1]
	if ret == nil {
		return UndefinedValue, nil
	}
	return ret, nil
}

func prepareCompiledArgs(fn *CompiledFunction, args []Object) ([]Object, error) {
	numArgs := len(args)
	if fn.VarArgs {
		realArgs := fn.NumParameters - 1
		varArgs := numArgs - realArgs
		if varArgs >= 0 {
			packed := make([]Object, varArgs)
			copy(packed, args[realArgs:])
			head := make([]Object, realArgs, realArgs+1)
			copy(head, args[:realArgs])
			args = append(head, &Array{Value: packed})
			numArgs = realArgs + 1
		}
	}
	if numArgs != fn.NumParameters {
		if fn.VarArgs {
			return nil, fmt.Errorf(
				"wrong number of arguments: want>=%d, got=%d",
				fn.NumParameters-1, numArgs)
		}
		return nil, fmt.Errorf(
			"wrong number of arguments: want=%d, got=%d",
			fn.NumParameters, numArgs)
	}
	return args, nil
}

func wrapCompiledCallError(fn *CompiledFunction, err error) error {
	var pos parser.SourceFilePos
	if fn.fileSet != nil {
		pos = fn.fileSet.Position(fn.SourcePos(0))
	}
	return fmt.Errorf("Runtime Error: %w\n\tat %s", err, pos)
}

// bindRuntime returns a function object bound to the VM that is executing
// it. Instructions are shared; the result is a distinct object so compiled
// instances that share bytecode do not share callable identity.
func (o *CompiledFunction) bindRuntime(v *VM) *CompiledFunction {
	// Inherit the executing frame's pool and file set so closures built by a
	// transferred function keep that function's bytecode, while globals and
	// the allocation limit come from the VM that is running (the destination
	// instance when the function was moved).
	constants := v.frameConstants()
	fileSet := v.fileSet
	if v.curFrame != nil && v.curFrame.fn != nil && v.curFrame.fn.fileSet != nil {
		fileSet = v.curFrame.fn.fileSet
	}
	return &CompiledFunction{
		Instructions:  o.Instructions,
		NumLocals:     o.NumLocals,
		NumParameters: o.NumParameters,
		VarArgs:       o.VarArgs,
		SourceMap:     o.SourceMap,
		Free:          o.Free,
		globals:       v.globals,
		constants:     constants,
		fileSet:       fileSet,
		maxAllocs:     v.maxAllocs,
	}
}

func sameGlobals(a, b []Object) bool {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return false
	}
	return &a[0] == &b[0]
}

// rebindState copies callable values onto a destination globals slice.
// Captured locals are snapshotted at the moment of transfer. Globals inside
// the copied functions resolve against dest. Shared free-variable cells stay
// shared across the copied graph, and cycles are preserved.
type rebindState struct {
	dest      []Object
	maxAllocs int64
	freeMap   map[*ObjectPtr]*ObjectPtr
	fnMap     map[*CompiledFunction]*CompiledFunction
}

func newRebindState(dest []Object, maxAllocs int64) *rebindState {
	return &rebindState{
		dest:      dest,
		maxAllocs: maxAllocs,
		freeMap:   make(map[*ObjectPtr]*ObjectPtr),
		fnMap:     make(map[*CompiledFunction]*CompiledFunction),
	}
}

// transferToGlobals returns obj unchanged when every compiled function it
// reaches already uses dest. Otherwise it returns a copy whose callables use
// dest and whose captured locals reflect their current values.
func transferToGlobals(obj Object, dest []Object, maxAllocs int64) Object {
	if obj == nil || !containsForeignFunc(obj, dest) {
		return obj
	}
	return newRebindState(dest, maxAllocs).copyValue(obj)
}

func containsForeignFunc(o Object, dest []Object) bool {
	switch o := o.(type) {
	case *CompiledFunction:
		return o.globals == nil || !sameGlobals(o.globals, dest)
	case *Array:
		for _, e := range o.Value {
			if containsForeignFunc(e, dest) {
				return true
			}
		}
	case *ImmutableArray:
		for _, e := range o.Value {
			if containsForeignFunc(e, dest) {
				return true
			}
		}
	case *Map:
		for _, e := range o.Value {
			if containsForeignFunc(e, dest) {
				return true
			}
		}
	case *ImmutableMap:
		for _, e := range o.Value {
			if containsForeignFunc(e, dest) {
				return true
			}
		}
	case *Error:
		return containsForeignFunc(o.Value, dest)
	}
	return false
}

// retargetCopied walks a value that was already produced by Object.Copy and
// rebinds every compiled function onto dest. Free-variable cells are
// snapshotted so the copy does not mutate the source instance.
func retargetCopied(o Object, dest []Object, maxAllocs int64) Object {
	if o == nil {
		return nil
	}
	return newRebindState(dest, maxAllocs).retarget(o)
}

func (st *rebindState) retarget(o Object) Object {
	switch o := o.(type) {
	case *CompiledFunction:
		return st.detach(o)
	case *Array:
		for i, e := range o.Value {
			o.Value[i] = st.retarget(e)
		}
		return o
	case *ImmutableArray:
		for i, e := range o.Value {
			o.Value[i] = st.retarget(e)
		}
		return o
	case *Map:
		for k, e := range o.Value {
			o.Value[k] = st.retarget(e)
		}
		return o
	case *ImmutableMap:
		for k, e := range o.Value {
			o.Value[k] = st.retarget(e)
		}
		return o
	case *Error:
		o.Value = st.retarget(o.Value)
		return o
	default:
		return o
	}
}

func (st *rebindState) copyValue(o Object) Object {
	if o == nil {
		return nil
	}
	switch o := o.(type) {
	case *CompiledFunction:
		if o.globals != nil && sameGlobals(o.globals, st.dest) {
			return o
		}
		return st.detach(o)
	case *Array:
		n := &Array{Value: make([]Object, len(o.Value))}
		for i, e := range o.Value {
			n.Value[i] = st.copyValue(e)
		}
		return n
	case *ImmutableArray:
		n := &ImmutableArray{Value: make([]Object, len(o.Value))}
		for i, e := range o.Value {
			n.Value[i] = st.copyValue(e)
		}
		return n
	case *Map:
		n := &Map{Value: make(map[string]Object, len(o.Value))}
		for k, e := range o.Value {
			n.Value[k] = st.copyValue(e)
		}
		return n
	case *ImmutableMap:
		n := &ImmutableMap{Value: make(map[string]Object, len(o.Value))}
		for k, e := range o.Value {
			n.Value[k] = st.copyValue(e)
		}
		return n
	case *Error:
		return &Error{Value: st.copyValue(o.Value)}
	default:
		return o.Copy()
	}
}

func (st *rebindState) detach(o *CompiledFunction) *CompiledFunction {
	if n, ok := st.fnMap[o]; ok {
		return n
	}
	n := &CompiledFunction{
		Instructions:  o.Instructions,
		NumLocals:     o.NumLocals,
		NumParameters: o.NumParameters,
		VarArgs:       o.VarArgs,
		SourceMap:     o.SourceMap,
		globals:       st.dest,
		constants:     o.constants,
		fileSet:       o.fileSet,
		maxAllocs:     st.maxAllocs,
	}
	st.fnMap[o] = n
	if len(o.Free) == 0 {
		return n
	}
	n.Free = make([]*ObjectPtr, len(o.Free))
	for i, fv := range o.Free {
		n.Free[i] = st.detachFree(fv)
	}
	return n
}

func (st *rebindState) detachFree(fv *ObjectPtr) *ObjectPtr {
	if fv == nil {
		return newObjectPtr(UndefinedValue)
	}
	if n, ok := st.freeMap[fv]; ok {
		return n
	}
	var cur Object
	if fv.Value != nil {
		cur = *fv.Value
	}
	cell := Object(UndefinedValue)
	nptr := &ObjectPtr{Value: &cell}
	st.freeMap[fv] = nptr
	cell = st.copyValue(cur)
	return nptr
}

func newObjectPtr(v Object) *ObjectPtr {
	return &ObjectPtr{Value: &v}
}
