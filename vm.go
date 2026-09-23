package tengo

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/d5/tengo/v2/parser"
	"github.com/d5/tengo/v2/token"
)

// frame represents a function call frame.
type frame struct {
	fn          *CompiledFunction
	freeVars    []*ObjectPtr
	ip          int
	basePointer int
	rt          *vmRuntime
}

// globalEnv holds the global variables of a Compiled instance, or of a VM
// created by NewVM, and the runtimes executing compiled code against them.
type globalEnv struct {
	globals   []Object
	maxAllocs int64
	runtimes  map[*Bytecode]*vmRuntime
	vm        *VM // VM currently running against globals, if any
}

func newGlobalEnv(globals []Object, maxAllocs int64) *globalEnv {
	return &globalEnv{
		globals:   globals,
		maxAllocs: maxAllocs,
		runtimes:  make(map[*Bytecode]*vmRuntime),
	}
}

// runtime returns the runtime executing the code of bytecode against the
// globals of e.
func (e *globalEnv) runtime(bytecode *Bytecode) *vmRuntime {
	if rt, ok := e.runtimes[bytecode]; ok {
		return rt
	}
	rt := &vmRuntime{
		env:       e,
		bytecode:  bytecode,
		constants: make([]Object, len(bytecode.Constants)),
		globals:   e.globals,
	}
	for i, c := range bytecode.Constants {
		if fn, ok := c.(*CompiledFunction); ok && fn.rt == nil {
			c = fn.boundTo(rt)
		}
		rt.constants[i] = c
	}
	e.runtimes[bytecode] = rt
	return rt
}

// vmRuntime binds the code of a Bytecode to a set of globals. Every compiled
// function the VM creates is bound to the runtime of the frame creating it,
// and runs in that runtime wherever it is called from.
type vmRuntime struct {
	env       *globalEnv
	bytecode  *Bytecode
	constants []Object // bytecode constants with functions bound to this runtime
	globals   []Object
}

// binder moves values into a globalEnv. Compiled functions bound to other
// globals are replaced by copies bound to the env, whose captured variables
// hold snapshots of their current values, so the copies share no state with
// the originals. One binder preserves sharing among the values it moves.
type binder struct {
	env  *globalEnv
	objs map[Object]Object
	ptrs map[*ObjectPtr]*ObjectPtr
}

func newBinder(env *globalEnv) *binder {
	return &binder{
		env:  env,
		objs: make(map[Object]Object),
		ptrs: make(map[*ObjectPtr]*ObjectPtr),
	}
}

// bind returns o unchanged if it reaches no compiled function bound to other
// globals. Otherwise it returns a deep copy of o's arrays, maps and errors
// with those functions rebound.
func (b *binder) bind(o Object) Object {
	switch o.(type) {
	case *CompiledFunction, *Array, *ImmutableArray, *Map, *ImmutableMap,
		*Error:
	default:
		return o
	}
	if !b.reachesForeign(o, make(map[Object]bool)) {
		return o
	}
	return b.copy(o)
}

// foreign reports whether fn is bound to globals other than those of b.env.
func (b *binder) foreign(fn *CompiledFunction) bool {
	if fn.rt == nil {
		return false
	}
	g1, g2 := fn.rt.globals, b.env.globals
	if len(g1) == 0 || len(g2) == 0 {
		return len(g1) != len(g2)
	}
	return &g1[0] != &g2[0]
}

func (b *binder) reachesForeign(o Object, seen map[Object]bool) bool {
	switch o := o.(type) {
	case *CompiledFunction:
		return b.foreign(o)
	case *Array:
		return b.elemsReachForeign(o, o.Value, seen)
	case *ImmutableArray:
		return b.elemsReachForeign(o, o.Value, seen)
	case *Map:
		return b.valuesReachForeign(o, o.Value, seen)
	case *ImmutableMap:
		return b.valuesReachForeign(o, o.Value, seen)
	case *Error:
		return b.elemsReachForeign(o, []Object{o.Value}, seen)
	}
	return false
}

func (b *binder) elemsReachForeign(
	o Object,
	elems []Object,
	seen map[Object]bool,
) bool {
	if seen[o] {
		return false
	}
	seen[o] = true
	for _, e := range elems {
		if b.reachesForeign(e, seen) {
			return true
		}
	}
	return false
}

func (b *binder) valuesReachForeign(
	o Object,
	values map[string]Object,
	seen map[Object]bool,
) bool {
	if seen[o] {
		return false
	}
	seen[o] = true
	for _, e := range values {
		if b.reachesForeign(e, seen) {
			return true
		}
	}
	return false
}

func (b *binder) copy(o Object) Object {
	switch o := o.(type) {
	case *CompiledFunction:
		if !b.foreign(o) {
			return o
		}
		if c, ok := b.objs[o]; ok {
			return c
		}
		c := o.boundTo(b.env.runtime(o.rt.bytecode))
		b.objs[o] = c
		if o.Free != nil {
			c.Free = make([]*ObjectPtr, len(o.Free))
			for i, p := range o.Free {
				c.Free[i] = b.snapshot(p)
			}
		}
		return c
	case *Array:
		if c, ok := b.objs[o]; ok {
			return c
		}
		c := &Array{Value: make([]Object, len(o.Value))}
		b.objs[o] = c
		for i, e := range o.Value {
			c.Value[i] = b.copy(e)
		}
		return c
	case *ImmutableArray:
		if c, ok := b.objs[o]; ok {
			return c
		}
		c := &ImmutableArray{Value: make([]Object, len(o.Value))}
		b.objs[o] = c
		for i, e := range o.Value {
			c.Value[i] = b.copy(e)
		}
		return c
	case *Map:
		if c, ok := b.objs[o]; ok {
			return c
		}
		c := &Map{Value: make(map[string]Object, len(o.Value))}
		b.objs[o] = c
		for k, e := range o.Value {
			c.Value[k] = b.copy(e)
		}
		return c
	case *ImmutableMap:
		if c, ok := b.objs[o]; ok {
			return c
		}
		c := &ImmutableMap{Value: make(map[string]Object, len(o.Value))}
		b.objs[o] = c
		for k, e := range o.Value {
			c.Value[k] = b.copy(e)
		}
		return c
	case *Error:
		if c, ok := b.objs[o]; ok {
			return c
		}
		c := &Error{}
		b.objs[o] = c
		c.Value = b.copy(o.Value)
		return c
	}
	return o
}

func (b *binder) snapshot(p *ObjectPtr) *ObjectPtr {
	if c, ok := b.ptrs[p]; ok {
		return c
	}
	c := &ObjectPtr{Value: new(Object)}
	b.ptrs[p] = c
	*c.Value = b.copy(*p.Value)
	return c
}

// VM is a virtual machine that executes the bytecode compiled by Compiler.
type VM struct {
	constants   []Object
	stack       [StackSize]Object
	sp          int
	globals     []Object
	frames      [MaxFrames]frame
	framesIndex int
	curFrame    *frame
	curInsts    []byte
	ip          int
	aborting    int64
	maxAllocs   int64
	allocs      int64
	err         error
}

// NewVM creates a VM.
func NewVM(
	bytecode *Bytecode,
	globals []Object,
	maxAllocs int64,
) *VM {
	if globals == nil {
		globals = make([]Object, GlobalsSize)
	}
	env := newGlobalEnv(globals, maxAllocs)
	b := newBinder(env)
	for idx, g := range globals {
		if g != nil {
			globals[idx] = b.bind(g)
		}
	}
	return newVM(env.runtime(bytecode))
}

func newVM(rt *vmRuntime) *VM {
	v := &VM{
		constants:   rt.constants,
		sp:          0,
		globals:     rt.globals,
		framesIndex: 1,
		ip:          -1,
		maxAllocs:   rt.env.maxAllocs,
	}
	v.frames[0].fn = rt.bytecode.MainFunction
	v.frames[0].ip = -1
	v.frames[0].rt = rt
	v.curFrame = &v.frames[0]
	v.curInsts = v.curFrame.fn.Instructions
	return v
}

// Abort aborts the execution.
func (v *VM) Abort() {
	atomic.StoreInt64(&v.aborting, 1)
}

// Run starts the execution.
func (v *VM) Run() (err error) {
	// reset VM states
	v.sp = 0
	v.curFrame = &(v.frames[0])
	v.curInsts = v.curFrame.fn.Instructions
	v.constants = v.curFrame.rt.constants
	v.globals = v.curFrame.rt.globals
	v.framesIndex = 1
	v.ip = -1
	v.allocs = v.maxAllocs + 1
	v.err = nil

	env := v.curFrame.rt.env
	outer := env.vm
	env.vm = v
	defer func() { env.vm = outer }()

	v.run()
	atomic.StoreInt64(&v.aborting, 0)
	err = v.err
	if err != nil {
		return v.runtimeError(err, 0)
	}
	return nil
}

// callTrampoline is the entry frame of calls made from Go: it calls the
// function below the array of arguments on the stack, and suspends the VM once
// that call returns.
var callTrampoline = &CompiledFunction{
	Instructions: append(MakeInstruction(parser.OpCall, 1, 1),
		parser.OpSuspend),
}

// idleVMs holds VMs for calls from Go made while no VM is running against the
// globals of the called function.
var idleVMs = sync.Pool{New: func() interface{} { return new(VM) }}

// call calls fn, which runs against the globals of e, from Go. Without a VM
// running against them, an idle VM stands in as the running VM for the
// duration of the call, so calls nested through Go share its frames and
// allocation limit.
func (e *globalEnv) call(fn *CompiledFunction, args []Object) (Object, error) {
	if e.vm != nil {
		return e.vm.invoke(fn, args)
	}
	v := idleVMs.Get().(*VM)
	v.maxAllocs = e.maxAllocs
	v.allocs = v.maxAllocs + 1
	e.vm = v
	defer func() {
		e.vm = nil
		idleVMs.Put(v)
	}()
	return v.invoke(fn, args)
}

// invoke calls fn with args on top of the current state of the VM, and returns
// once the call returns. The state of the VM is restored afterwards, even if
// the call panics, so this can run while the VM is calling out to Go code.
func (v *VM) invoke(fn *CompiledFunction, args []Object) (Object, error) {
	if v.framesIndex >= MaxFrames || v.sp+len(args)+2 > StackSize {
		return nil, &runtimeError{err: ErrStackOverflow, call: true}
	}
	sp, ip, framesIndex := v.sp, v.ip, v.framesIndex
	curFrame, curInsts := v.curFrame, v.curInsts
	constants, globals := v.constants, v.globals
	defer func() {
		v.sp, v.ip, v.framesIndex = sp, ip, framesIndex
		v.curFrame, v.curInsts = curFrame, curInsts
		v.constants, v.globals = constants, globals
	}()

	callArgs := make([]Object, len(args))
	for i, arg := range args {
		if arg == nil {
			arg = UndefinedValue
		}
		callArgs[i] = arg
	}
	v.stack[v.sp] = fn
	v.stack[v.sp+1] = &Array{Value: callArgs}
	v.sp += 2

	v.curFrame = &v.frames[v.framesIndex]
	v.curFrame.fn = callTrampoline
	v.curFrame.freeVars = nil
	v.curFrame.basePointer = v.sp
	v.curFrame.rt = fn.rt
	v.curInsts = callTrampoline.Instructions
	v.constants = fn.rt.constants
	v.globals = fn.rt.globals
	v.ip = -1
	v.framesIndex++

	v.run()

	switch {
	case v.err != nil:
		if v.err == ErrObjectAllocLimit {
			// the limit stays exhausted for the code that made the call
			v.allocs = 1
		}
		err := v.runtimeError(v.err, framesIndex+1)
		err.call = true
		v.err = nil
		return nil, err
	case atomic.LoadInt64(&v.aborting) != 0:
		return nil, ErrVMAborted
	}
	return v.stack[sp], nil
}

// runtimeError is an error raised while executing compiled code, with the
// source positions of the call frames it unwound, innermost first.
type runtimeError struct {
	err   error
	trace []parser.SourceFilePos
	call  bool // raised by a call from Go
}

func (e *runtimeError) Error() string {
	var sb strings.Builder
	sb.WriteString("Runtime Error: ")
	sb.WriteString(e.err.Error())
	for _, pos := range e.trace {
		sb.WriteString("\n\tat ")
		sb.WriteString(pos.String())
	}
	return sb.String()
}

func (e *runtimeError) Unwrap() error {
	return e.err
}

// runtimeError attaches to err the source positions of the frames from the
// current one down to the frame at index bottom. The error of a call from Go
// passed on by Go code, such as a callback returning the error of a compiled
// function it called, gets its trace extended instead, so it reads as if the
// call was made from the script.
func (v *VM) runtimeError(err error, bottom int) *runtimeError {
	var trace []parser.SourceFilePos
	for i := v.framesIndex - 1; i >= bottom; i-- {
		f := &v.frames[i]
		ip := f.ip
		if i == v.framesIndex-1 {
			ip = v.ip
		}
		trace = append(trace,
			f.rt.bytecode.FileSet.Position(f.fn.SourcePos(ip-1)))
	}
	if inner, ok := err.(*runtimeError); ok && inner.call {
		n := len(inner.trace)
		return &runtimeError{
			err:   inner.err,
			trace: append(inner.trace[:n:n], trace...),
		}
	}
	return &runtimeError{err: err, trace: trace}
}

func (v *VM) run() {
	for atomic.LoadInt64(&v.aborting) == 0 {
		v.ip++

		switch v.curInsts[v.ip] {
		case parser.OpConstant:
			v.ip += 2
			cidx := int(v.curInsts[v.ip]) | int(v.curInsts[v.ip-1])<<8

			v.stack[v.sp] = v.constants[cidx]
			v.sp++
		case parser.OpNull:
			v.stack[v.sp] = UndefinedValue
			v.sp++
		case parser.OpBinaryOp:
			v.ip++
			right := v.stack[v.sp-1]
			left := v.stack[v.sp-2]
			tok := token.Token(v.curInsts[v.ip])
			res, e := left.BinaryOp(tok, right)
			if e != nil {
				v.sp -= 2
				if e == ErrInvalidOperator {
					v.err = fmt.Errorf("invalid operation: %s %s %s",
						left.TypeName(), tok.String(), right.TypeName())
					return
				}
				v.err = e
				return
			}

			v.allocs--
			if v.allocs == 0 {
				v.err = ErrObjectAllocLimit
				return
			}

			v.stack[v.sp-2] = res
			v.sp--
		case parser.OpEqual:
			right := v.stack[v.sp-1]
			left := v.stack[v.sp-2]
			v.sp -= 2
			if left.Equals(right) {
				v.stack[v.sp] = TrueValue
			} else {
				v.stack[v.sp] = FalseValue
			}
			v.sp++
		case parser.OpNotEqual:
			right := v.stack[v.sp-1]
			left := v.stack[v.sp-2]
			v.sp -= 2
			if left.Equals(right) {
				v.stack[v.sp] = FalseValue
			} else {
				v.stack[v.sp] = TrueValue
			}
			v.sp++
		case parser.OpPop:
			v.sp--
		case parser.OpTrue:
			v.stack[v.sp] = TrueValue
			v.sp++
		case parser.OpFalse:
			v.stack[v.sp] = FalseValue
			v.sp++
		case parser.OpLNot:
			operand := v.stack[v.sp-1]
			v.sp--
			if operand.IsFalsy() {
				v.stack[v.sp] = TrueValue
			} else {
				v.stack[v.sp] = FalseValue
			}
			v.sp++
		case parser.OpBComplement:
			operand := v.stack[v.sp-1]
			v.sp--

			switch x := operand.(type) {
			case *Int:
				var res Object = &Int{Value: ^x.Value}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp] = res
				v.sp++
			default:
				v.err = fmt.Errorf("invalid operation: ^%s",
					operand.TypeName())
				return
			}
		case parser.OpMinus:
			operand := v.stack[v.sp-1]
			v.sp--

			switch x := operand.(type) {
			case *Int:
				var res Object = &Int{Value: -x.Value}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp] = res
				v.sp++
			case *Float:
				var res Object = &Float{Value: -x.Value}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp] = res
				v.sp++
			default:
				v.err = fmt.Errorf("invalid operation: -%s",
					operand.TypeName())
				return
			}
		case parser.OpJumpFalsy:
			v.ip += 4
			v.sp--
			if v.stack[v.sp].IsFalsy() {
				pos := int(v.curInsts[v.ip]) | int(v.curInsts[v.ip-1])<<8 | int(v.curInsts[v.ip-2])<<16 | int(v.curInsts[v.ip-3])<<24
				v.ip = pos - 1
			}
		case parser.OpAndJump:
			v.ip += 4
			if v.stack[v.sp-1].IsFalsy() {
				pos := int(v.curInsts[v.ip]) | int(v.curInsts[v.ip-1])<<8 | int(v.curInsts[v.ip-2])<<16 | int(v.curInsts[v.ip-3])<<24
				v.ip = pos - 1
			} else {
				v.sp--
			}
		case parser.OpOrJump:
			v.ip += 4
			if v.stack[v.sp-1].IsFalsy() {
				v.sp--
			} else {
				pos := int(v.curInsts[v.ip]) | int(v.curInsts[v.ip-1])<<8 | int(v.curInsts[v.ip-2])<<16 | int(v.curInsts[v.ip-3])<<24
				v.ip = pos - 1
			}
		case parser.OpJump:
			pos := int(v.curInsts[v.ip+4]) | int(v.curInsts[v.ip+3])<<8 | int(v.curInsts[v.ip+2])<<16 | int(v.curInsts[v.ip+1])<<24
			v.ip = pos - 1
		case parser.OpSetGlobal:
			v.ip += 2
			v.sp--
			globalIndex := int(v.curInsts[v.ip]) | int(v.curInsts[v.ip-1])<<8
			v.globals[globalIndex] = v.stack[v.sp]
		case parser.OpSetSelGlobal:
			v.ip += 3
			globalIndex := int(v.curInsts[v.ip-1]) | int(v.curInsts[v.ip-2])<<8
			numSelectors := int(v.curInsts[v.ip])

			// selectors and RHS value
			selectors := make([]Object, numSelectors)
			for i := 0; i < numSelectors; i++ {
				selectors[i] = v.stack[v.sp-numSelectors+i]
			}
			val := v.stack[v.sp-numSelectors-1]
			v.sp -= numSelectors + 1
			e := indexAssign(v.globals[globalIndex], val, selectors)
			if e != nil {
				v.err = e
				return
			}
		case parser.OpGetGlobal:
			v.ip += 2
			globalIndex := int(v.curInsts[v.ip]) | int(v.curInsts[v.ip-1])<<8
			val := v.globals[globalIndex]
			v.stack[v.sp] = val
			v.sp++
		case parser.OpArray:
			v.ip += 2
			numElements := int(v.curInsts[v.ip]) | int(v.curInsts[v.ip-1])<<8

			var elements []Object
			for i := v.sp - numElements; i < v.sp; i++ {
				elements = append(elements, v.stack[i])
			}
			v.sp -= numElements

			var arr Object = &Array{Value: elements}
			v.allocs--
			if v.allocs == 0 {
				v.err = ErrObjectAllocLimit
				return
			}

			v.stack[v.sp] = arr
			v.sp++
		case parser.OpMap:
			v.ip += 2
			numElements := int(v.curInsts[v.ip]) | int(v.curInsts[v.ip-1])<<8
			kv := make(map[string]Object, numElements)
			for i := v.sp - numElements; i < v.sp; i += 2 {
				key := v.stack[i]
				value := v.stack[i+1]
				kv[key.(*String).Value] = value
			}
			v.sp -= numElements

			var m Object = &Map{Value: kv}
			v.allocs--
			if v.allocs == 0 {
				v.err = ErrObjectAllocLimit
				return
			}
			v.stack[v.sp] = m
			v.sp++
		case parser.OpError:
			value := v.stack[v.sp-1]
			var e Object = &Error{
				Value: value,
			}
			v.allocs--
			if v.allocs == 0 {
				v.err = ErrObjectAllocLimit
				return
			}
			v.stack[v.sp-1] = e
		case parser.OpImmutable:
			value := v.stack[v.sp-1]
			switch value := value.(type) {
			case *Array:
				var immutableArray Object = &ImmutableArray{
					Value: value.Value,
				}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp-1] = immutableArray
			case *Map:
				var immutableMap Object = &ImmutableMap{
					Value: value.Value,
				}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp-1] = immutableMap
			}
		case parser.OpIndex:
			index := v.stack[v.sp-1]
			left := v.stack[v.sp-2]
			v.sp -= 2

			val, err := left.IndexGet(index)
			if err != nil {
				if err == ErrNotIndexable {
					v.err = fmt.Errorf("not indexable: %s", index.TypeName())
					return
				}
				if err == ErrInvalidIndexType {
					v.err = fmt.Errorf("invalid index type: %s",
						index.TypeName())
					return
				}
				v.err = err
				return
			}
			if val == nil {
				val = UndefinedValue
			}
			v.stack[v.sp] = val
			v.sp++
		case parser.OpSliceIndex:
			high := v.stack[v.sp-1]
			low := v.stack[v.sp-2]
			left := v.stack[v.sp-3]
			v.sp -= 3

			var lowIdx int64
			if low != UndefinedValue {
				if lowInt, ok := low.(*Int); ok {
					lowIdx = lowInt.Value
				} else {
					v.err = fmt.Errorf("invalid slice index type: %s",
						low.TypeName())
					return
				}
			}

			switch left := left.(type) {
			case *Array:
				numElements := int64(len(left.Value))
				var highIdx int64
				if high == UndefinedValue {
					highIdx = numElements
				} else if highInt, ok := high.(*Int); ok {
					highIdx = highInt.Value
				} else {
					v.err = fmt.Errorf("invalid slice index type: %s",
						high.TypeName())
					return
				}
				if lowIdx > highIdx {
					v.err = fmt.Errorf("invalid slice index: %d > %d",
						lowIdx, highIdx)
					return
				}
				if lowIdx < 0 {
					lowIdx = 0
				} else if lowIdx > numElements {
					lowIdx = numElements
				}
				if highIdx < 0 {
					highIdx = 0
				} else if highIdx > numElements {
					highIdx = numElements
				}
				var val Object = &Array{
					Value: left.Value[lowIdx:highIdx],
				}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp] = val
				v.sp++
			case *ImmutableArray:
				numElements := int64(len(left.Value))
				var highIdx int64
				if high == UndefinedValue {
					highIdx = numElements
				} else if highInt, ok := high.(*Int); ok {
					highIdx = highInt.Value
				} else {
					v.err = fmt.Errorf("invalid slice index type: %s",
						high.TypeName())
					return
				}
				if lowIdx > highIdx {
					v.err = fmt.Errorf("invalid slice index: %d > %d",
						lowIdx, highIdx)
					return
				}
				if lowIdx < 0 {
					lowIdx = 0
				} else if lowIdx > numElements {
					lowIdx = numElements
				}
				if highIdx < 0 {
					highIdx = 0
				} else if highIdx > numElements {
					highIdx = numElements
				}
				var val Object = &Array{
					Value: left.Value[lowIdx:highIdx],
				}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp] = val
				v.sp++
			case *String:
				numElements := int64(len(left.Value))
				var highIdx int64
				if high == UndefinedValue {
					highIdx = numElements
				} else if highInt, ok := high.(*Int); ok {
					highIdx = highInt.Value
				} else {
					v.err = fmt.Errorf("invalid slice index type: %s",
						high.TypeName())
					return
				}
				if lowIdx > highIdx {
					v.err = fmt.Errorf("invalid slice index: %d > %d",
						lowIdx, highIdx)
					return
				}
				if lowIdx < 0 {
					lowIdx = 0
				} else if lowIdx > numElements {
					lowIdx = numElements
				}
				if highIdx < 0 {
					highIdx = 0
				} else if highIdx > numElements {
					highIdx = numElements
				}
				var val Object = &String{
					Value: left.Value[lowIdx:highIdx],
				}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp] = val
				v.sp++
			case *Bytes:
				numElements := int64(len(left.Value))
				var highIdx int64
				if high == UndefinedValue {
					highIdx = numElements
				} else if highInt, ok := high.(*Int); ok {
					highIdx = highInt.Value
				} else {
					v.err = fmt.Errorf("invalid slice index type: %s",
						high.TypeName())
					return
				}
				if lowIdx > highIdx {
					v.err = fmt.Errorf("invalid slice index: %d > %d",
						lowIdx, highIdx)
					return
				}
				if lowIdx < 0 {
					lowIdx = 0
				} else if lowIdx > numElements {
					lowIdx = numElements
				}
				if highIdx < 0 {
					highIdx = 0
				} else if highIdx > numElements {
					highIdx = numElements
				}
				var val Object = &Bytes{
					Value: left.Value[lowIdx:highIdx],
				}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp] = val
				v.sp++
			default:
				v.err = fmt.Errorf("not indexable: %s", left.TypeName())
				return
			}
		case parser.OpCall:
			numArgs := int(v.curInsts[v.ip+1])
			spread := int(v.curInsts[v.ip+2])
			v.ip += 2

			value := v.stack[v.sp-1-numArgs]
			if !value.CanCall() {
				v.err = fmt.Errorf("not callable: %s", value.TypeName())
				return
			}

			if spread == 1 {
				v.sp--
				switch arr := v.stack[v.sp].(type) {
				case *Array:
					for _, item := range arr.Value {
						v.stack[v.sp] = item
						v.sp++
					}
					numArgs += len(arr.Value) - 1
				case *ImmutableArray:
					for _, item := range arr.Value {
						v.stack[v.sp] = item
						v.sp++
					}
					numArgs += len(arr.Value) - 1
				default:
					v.err = fmt.Errorf("not an array: %s", arr.TypeName())
					return
				}
			}

			if callee, ok := value.(*CompiledFunction); ok {
				if callee.VarArgs {
					// if the closure is variadic,
					// roll up all variadic parameters into an array
					realArgs := callee.NumParameters - 1
					varArgs := numArgs - realArgs
					if varArgs >= 0 {
						numArgs = realArgs + 1
						args := make([]Object, varArgs)
						spStart := v.sp - varArgs
						for i := spStart; i < v.sp; i++ {
							args[i-spStart] = v.stack[i]
						}
						v.stack[spStart] = &Array{Value: args}
						v.sp = spStart + 1
					}
				}
				if numArgs != callee.NumParameters {
					if callee.VarArgs {
						v.err = fmt.Errorf(
							"wrong number of arguments: want>=%d, got=%d",
							callee.NumParameters-1, numArgs)
					} else {
						v.err = fmt.Errorf(
							"wrong number of arguments: want=%d, got=%d",
							callee.NumParameters, numArgs)
					}
					return
				}

				// test if it's tail-call
				if callee == v.curFrame.fn { // recursion
					nextOp := v.curInsts[v.ip+1]
					if nextOp == parser.OpReturn ||
						(nextOp == parser.OpPop &&
							parser.OpReturn == v.curInsts[v.ip+2]) {
						for p := 0; p < numArgs; p++ {
							v.stack[v.curFrame.basePointer+p] =
								v.stack[v.sp-numArgs+p]
						}
						v.sp -= numArgs + 1
						v.ip = -1 // reset IP to beginning of the frame
						continue
					}
				}
				if v.framesIndex >= MaxFrames {
					v.err = ErrStackOverflow
					return
				}

				rt := callee.rt
				if rt == nil {
					rt = v.curFrame.rt
				} else if rt != v.curFrame.rt {
					v.constants = rt.constants
					v.globals = rt.globals
				}

				// update call frame
				v.curFrame.ip = v.ip // store current ip before call
				v.curFrame = &(v.frames[v.framesIndex])
				v.curFrame.fn = callee
				v.curFrame.freeVars = callee.Free
				v.curFrame.basePointer = v.sp - numArgs
				v.curFrame.rt = rt
				v.curInsts = callee.Instructions
				v.ip = -1
				v.framesIndex++
				v.sp = v.sp - numArgs + callee.NumLocals
			} else {
				var args []Object
				args = append(args, v.stack[v.sp-numArgs:v.sp]...)
				ret, e := value.Call(args...)
				v.sp -= numArgs + 1

				// runtime error
				if e != nil {
					if e == ErrWrongNumArguments {
						v.err = fmt.Errorf(
							"wrong number of arguments in call to '%s'",
							value.TypeName())
						return
					}
					if e, ok := e.(ErrInvalidArgumentType); ok {
						v.err = fmt.Errorf(
							"invalid type for argument '%s' in call to '%s': "+
								"expected %s, found %s",
							e.Name, value.TypeName(), e.Expected, e.Found)
						return
					}
					v.err = e
					return
				}

				// nil return -> undefined
				if ret == nil {
					ret = UndefinedValue
				}
				v.allocs--
				if v.allocs == 0 {
					v.err = ErrObjectAllocLimit
					return
				}
				v.stack[v.sp] = ret
				v.sp++
			}
		case parser.OpReturn:
			v.ip++
			var retVal Object
			if int(v.curInsts[v.ip]) == 1 {
				retVal = v.stack[v.sp-1]
			} else {
				retVal = UndefinedValue
			}
			//v.sp--
			rt := v.curFrame.rt
			v.framesIndex--
			v.curFrame = &v.frames[v.framesIndex-1]
			v.curInsts = v.curFrame.fn.Instructions
			if v.curFrame.rt != rt {
				v.constants = v.curFrame.rt.constants
				v.globals = v.curFrame.rt.globals
			}
			v.ip = v.curFrame.ip
			//v.sp = lastFrame.basePointer - 1
			v.sp = v.frames[v.framesIndex].basePointer
			// skip stack overflow check because (newSP) <= (oldSP)
			v.stack[v.sp-1] = retVal
			//v.sp++
		case parser.OpDefineLocal:
			v.ip++
			localIndex := int(v.curInsts[v.ip])
			sp := v.curFrame.basePointer + localIndex

			// local variables can be mutated by other actions
			// so always store the copy of popped value
			val := v.stack[v.sp-1]
			v.sp--
			v.stack[sp] = val
		case parser.OpSetLocal:
			localIndex := int(v.curInsts[v.ip+1])
			v.ip++
			sp := v.curFrame.basePointer + localIndex

			// update pointee of v.stack[sp] instead of replacing the pointer
			// itself. this is needed because there can be free variables
			// referencing the same local variables.
			val := v.stack[v.sp-1]
			v.sp--
			if obj, ok := v.stack[sp].(*ObjectPtr); ok {
				*obj.Value = val
				val = obj
			}
			v.stack[sp] = val // also use a copy of popped value
		case parser.OpSetSelLocal:
			localIndex := int(v.curInsts[v.ip+1])
			numSelectors := int(v.curInsts[v.ip+2])
			v.ip += 2

			// selectors and RHS value
			selectors := make([]Object, numSelectors)
			for i := 0; i < numSelectors; i++ {
				selectors[i] = v.stack[v.sp-numSelectors+i]
			}
			val := v.stack[v.sp-numSelectors-1]
			v.sp -= numSelectors + 1
			dst := v.stack[v.curFrame.basePointer+localIndex]
			if obj, ok := dst.(*ObjectPtr); ok {
				dst = *obj.Value
			}
			if e := indexAssign(dst, val, selectors); e != nil {
				v.err = e
				return
			}
		case parser.OpGetLocal:
			v.ip++
			localIndex := int(v.curInsts[v.ip])
			val := v.stack[v.curFrame.basePointer+localIndex]
			if obj, ok := val.(*ObjectPtr); ok {
				val = *obj.Value
			}
			v.stack[v.sp] = val
			v.sp++
		case parser.OpGetBuiltin:
			v.ip++
			builtinIndex := int(v.curInsts[v.ip])
			v.stack[v.sp] = builtinFuncs[builtinIndex]
			v.sp++
		case parser.OpClosure:
			v.ip += 3
			constIndex := int(v.curInsts[v.ip-1]) | int(v.curInsts[v.ip-2])<<8
			numFree := int(v.curInsts[v.ip])
			fn, ok := v.constants[constIndex].(*CompiledFunction)
			if !ok {
				v.err = fmt.Errorf("not function: %s", fn.TypeName())
				return
			}
			free := make([]*ObjectPtr, numFree)
			for i := 0; i < numFree; i++ {
				switch freeVar := (v.stack[v.sp-numFree+i]).(type) {
				case *ObjectPtr:
					free[i] = freeVar
				default:
					free[i] = &ObjectPtr{
						Value: &v.stack[v.sp-numFree+i],
					}
				}
			}
			v.sp -= numFree
			cl := &CompiledFunction{
				Instructions:  fn.Instructions,
				NumLocals:     fn.NumLocals,
				NumParameters: fn.NumParameters,
				VarArgs:       fn.VarArgs,
				SourceMap:     fn.SourceMap,
				Free:          free,
				rt:            v.curFrame.rt,
			}
			v.allocs--
			if v.allocs == 0 {
				v.err = ErrObjectAllocLimit
				return
			}
			v.stack[v.sp] = cl
			v.sp++
		case parser.OpGetFreePtr:
			v.ip++
			freeIndex := int(v.curInsts[v.ip])
			val := v.curFrame.freeVars[freeIndex]
			v.stack[v.sp] = val
			v.sp++
		case parser.OpGetFree:
			v.ip++
			freeIndex := int(v.curInsts[v.ip])
			val := *v.curFrame.freeVars[freeIndex].Value
			v.stack[v.sp] = val
			v.sp++
		case parser.OpSetFree:
			v.ip++
			freeIndex := int(v.curInsts[v.ip])
			*v.curFrame.freeVars[freeIndex].Value = v.stack[v.sp-1]
			v.sp--
		case parser.OpGetLocalPtr:
			v.ip++
			localIndex := int(v.curInsts[v.ip])
			sp := v.curFrame.basePointer + localIndex
			val := v.stack[sp]
			var freeVar *ObjectPtr
			if obj, ok := val.(*ObjectPtr); ok {
				freeVar = obj
			} else {
				freeVar = &ObjectPtr{Value: &val}
				v.stack[sp] = freeVar
			}
			v.stack[v.sp] = freeVar
			v.sp++
		case parser.OpSetSelFree:
			v.ip += 2
			freeIndex := int(v.curInsts[v.ip-1])
			numSelectors := int(v.curInsts[v.ip])

			// selectors and RHS value
			selectors := make([]Object, numSelectors)
			for i := 0; i < numSelectors; i++ {
				selectors[i] = v.stack[v.sp-numSelectors+i]
			}
			val := v.stack[v.sp-numSelectors-1]
			v.sp -= numSelectors + 1
			e := indexAssign(*v.curFrame.freeVars[freeIndex].Value,
				val, selectors)
			if e != nil {
				v.err = e
				return
			}
		case parser.OpIteratorInit:
			var iterator Object
			dst := v.stack[v.sp-1]
			v.sp--
			if !dst.CanIterate() {
				v.err = fmt.Errorf("not iterable: %s", dst.TypeName())
				return
			}
			iterator = dst.Iterate()
			v.allocs--
			if v.allocs == 0 {
				v.err = ErrObjectAllocLimit
				return
			}
			v.stack[v.sp] = iterator
			v.sp++
		case parser.OpIteratorNext:
			iterator := v.stack[v.sp-1]
			v.sp--
			hasMore := iterator.(Iterator).Next()
			if hasMore {
				v.stack[v.sp] = TrueValue
			} else {
				v.stack[v.sp] = FalseValue
			}
			v.sp++
		case parser.OpIteratorKey:
			iterator := v.stack[v.sp-1]
			v.sp--
			val := iterator.(Iterator).Key()
			v.stack[v.sp] = val
			v.sp++
		case parser.OpIteratorValue:
			iterator := v.stack[v.sp-1]
			v.sp--
			val := iterator.(Iterator).Value()
			v.stack[v.sp] = val
			v.sp++
		case parser.OpSuspend:
			return
		default:
			v.err = fmt.Errorf("unknown opcode: %d", v.curInsts[v.ip])
			return
		}
	}
}

// IsStackEmpty tests if the stack is empty or not.
func (v *VM) IsStackEmpty() bool {
	return v.sp == 0
}

func indexAssign(dst, src Object, selectors []Object) error {
	numSel := len(selectors)
	for sidx := numSel - 1; sidx > 0; sidx-- {
		next, err := dst.IndexGet(selectors[sidx])
		if err != nil {
			if err == ErrNotIndexable {
				return fmt.Errorf("not indexable: %s", dst.TypeName())
			}
			if err == ErrInvalidIndexType {
				return fmt.Errorf("invalid index type: %s",
					selectors[sidx].TypeName())
			}
			return err
		}
		dst = next
	}

	if err := dst.IndexSet(selectors[0], src); err != nil {
		if err == ErrNotIndexAssignable {
			return fmt.Errorf("not index-assignable: %s", dst.TypeName())
		}
		if err == ErrInvalidIndexValueType {
			return fmt.Errorf("invaid index value type: %s", src.TypeName())
		}
		return err
	}
	return nil
}
