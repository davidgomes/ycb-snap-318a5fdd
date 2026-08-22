package vm

import (
	"github.com/expr-lang/expr/vm/runtime"
)

const MaxRetries = 3

// TryHandler is stored in program constants and filled in by the compiler.
type TryHandler struct {
	Catch   int // bytecode IP of the first catch; -1 if none
	Finally int // bytecode IP of finally; -1 if none
	End     int // bytecode IP after the whole try/catch/finally
}

type tryFrame struct {
	tryIP        int
	catchIP      int
	finallyIP    int
	endIP        int
	stackLen     int
	scopesLen    int
	scopePoolIdx int
	retries      int
	inCatch      bool
	inFinally    bool
	handled      bool
	pending      any
	result       any
}

func (vm *VM) pushTry(h *TryHandler) {
	vm.tryStack = append(vm.tryStack, &tryFrame{
		tryIP:        vm.ip,
		catchIP:      h.Catch,
		finallyIP:    h.Finally,
		endIP:        h.End,
		stackLen:     len(vm.Stack),
		scopesLen:    len(vm.Scopes),
		scopePoolIdx: vm.scopePoolIdx,
	})
}

func (vm *VM) popTry() *tryFrame {
	if len(vm.tryStack) == 0 {
		return nil
	}
	f := vm.tryStack[len(vm.tryStack)-1]
	vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
	return f
}

func (vm *VM) currentTry() *tryFrame {
	if len(vm.tryStack) == 0 {
		return nil
	}
	return vm.tryStack[len(vm.tryStack)-1]
}

func (vm *VM) unwindTry(frame *tryFrame) {
	if len(vm.Stack) > frame.stackLen {
		clearSlice(vm.Stack[frame.stackLen:])
		vm.Stack = vm.Stack[:frame.stackLen]
	}
	if vm.Scopes != nil && len(vm.Scopes) > frame.scopesLen {
		clearSlice(vm.Scopes[frame.scopesLen:])
		vm.Scopes = vm.Scopes[:frame.scopesLen]
	}
	if len(vm.Scopes) > 0 {
		vm.currScope = vm.Scopes[len(vm.Scopes)-1]
	} else {
		vm.currScope = nil
	}
	vm.scopePoolIdx = frame.scopePoolIdx
}

func (vm *VM) handlePanic(r any) bool {
	wrapped := runtime.Wrap(r)
	for len(vm.tryStack) > 0 {
		frame := vm.tryStack[len(vm.tryStack)-1]
		if frame.inFinally {
			vm.popTry()
			continue
		}
		vm.unwindTry(frame)
		if frame.inCatch {
			frame.pending = wrapped
			frame.handled = false
			if frame.finallyIP >= 0 {
				frame.inFinally = true
				vm.ip = frame.finallyIP
				return true
			}
			vm.popTry()
			continue
		}
		// Error originated in the try body.
		if frame.catchIP >= 0 {
			frame.inCatch = true
			vm.push(wrapped)
			vm.ip = frame.catchIP
			return true
		}
		frame.pending = wrapped
		frame.handled = false
		if frame.finallyIP >= 0 {
			frame.inFinally = true
			vm.ip = frame.finallyIP
			return true
		}
		vm.popTry()
	}
	return false
}

func (vm *VM) tryOk() {
	frame := vm.currentTry()
	if frame == nil {
		return
	}
	frame.handled = true
	frame.pending = nil
	if len(vm.Stack) > frame.stackLen {
		frame.result = vm.pop()
	}
	if frame.finallyIP >= 0 {
		frame.inFinally = true
		frame.inCatch = false
		vm.ip = frame.finallyIP
		return
	}
	end := frame.endIP
	vm.popTry()
	vm.push(frame.result)
	if end >= 0 {
		vm.ip = end
	}
}

func (vm *VM) tryRethrow() {
	frame := vm.currentTry()
	if frame == nil {
		panic("try rethrow without try frame")
	}
	var pending any
	if len(vm.Stack) > frame.stackLen {
		pending = vm.pop()
	}
	frame.pending = pending
	frame.handled = false
	if frame.finallyIP >= 0 {
		frame.inFinally = true
		frame.inCatch = false
		vm.ip = frame.finallyIP
		return
	}
	vm.popTry()
	if pending != nil {
		panic(pending)
	}
}

func (vm *VM) finallyEnd() {
	frame := vm.currentTry()
	if frame == nil {
		return
	}
	pending := frame.pending
	result := frame.result
	end := frame.endIP
	vm.popTry()
	if pending != nil {
		panic(pending)
	}
	vm.push(result)
	if end >= 0 {
		vm.ip = end
	}
}

func (vm *VM) retry() {
	frame := vm.currentTry()
	if frame == nil || !frame.inCatch {
		panic("retry used outside of catch block")
	}
	frame.retries++
	if frame.retries > MaxRetries {
		panic(runtime.RetryLimitError())
	}
	vm.unwindTry(frame)
	frame.inCatch = false
	frame.inFinally = false
	frame.pending = nil
	frame.result = nil
	vm.ip = frame.tryIP
}
