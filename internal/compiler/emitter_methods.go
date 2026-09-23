// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package compiler

import (
	"reflect"

	"github.com/open2b/scriggo/ast"
	"github.com/open2b/scriggo/internal/runtime"
)

// methodFuncName returns the name of the function that implements the method
// sm, as "T.M" or "(*T).M".
func methodFuncName(sm *scriggoMethod) string {
	if sm.method.PointerReceiver {
		return "(" + sm.recv.String() + ")." + sm.method.Name
	}
	return sm.recv.String() + "." + sm.method.Name
}

// declareMethodFunction creates, if it does not already exist, the function
// that implements the method declared by decl.
func (em *emitter) declareMethodFunction(decl *ast.Func, path string) {
	sm := em.ti(decl).method
	if _, ok := em.methodFuncs[sm]; ok {
		return
	}
	em.methodFuncs[sm] = newFunction("main", methodFuncName(sm), sm.funcType, path, decl.Pos())
}

// methodFunction returns the function that implements the method sm. The
// function takes the receiver as first parameter.
func (em *emitter) methodFunction(sm *scriggoMethod) *runtime.Function {
	fn, ok := em.methodFuncs[sm]
	if !ok {
		panic(internalError("function of method %s not declared", methodFuncName(sm)))
	}
	return fn
}

// emitMethodDeclaration emits the body of the method declared by decl and
// the function used to call it through method values and interfaces.
func (em *emitter) emitMethodDeclaration(decl *ast.Func, path string) {
	sm := em.ti(decl).method
	fn := em.methodFunction(sm)
	em.fb = newBuilder(fn, path)
	em.fb.enterScope()
	em.prepareFunctionBodyParameters(decl)
	em.emitNodes(decl.Body.Nodes)
	em.fb.end()
	em.fb.exitScope()
	em.alreadyEmittedFuncs[decl] = fn
	sm.method.Function = em.newMethodWrapper(sm, sm.method.Type, true)
}

// methodPtrAdapter returns the function, with type typ, that implements the
// method expression (*T).M, where M is the method sm with receiver type T.
func (em *emitter) methodPtrAdapter(sm *scriggoMethod, typ reflect.Type) *runtime.Function {
	if fn, ok := em.methodPtrAdapters[sm]; ok {
		return fn
	}
	fn := em.newMethodWrapper(sm, typ, false)
	em.methodPtrAdapters[sm] = fn
	return fn
}

// newMethodWrapper returns a new function, with type typ, that calls the
// function that implements the method sm.
//
// If bound is true, the returned function has the parameters of the method
// and reads the receiver from its first closure variable. Otherwise its
// first parameter is a pointer to the receiver, that is dereferenced.
func (em *emitter) newMethodWrapper(sm *scriggoMethod, typ reflect.Type, bound bool) *runtime.Function {

	target := em.methodFunction(sm)
	name := methodFuncName(sm)
	if bound {
		name += "-fm"
	} else {
		name = "(*" + sm.recv.String() + ")." + sm.method.Name
	}
	pos := sm.decl.Pos()
	fn := newFunction("main", name, typ, target.File, pos)

	backup := em.fb
	em.fb = newBuilder(fn, target.File)
	defer func() { em.fb = backup }()

	em.fb.enterScope()

	// Reserve the registers of the parameters.
	numOut := typ.NumOut()
	numIn := typ.NumIn()
	outs := make([]int8, numOut)
	for i := 0; i < numOut; i++ {
		outs[i] = em.fb.newRegister(typ.Out(i).Kind())
	}
	ins := make([]int8, numIn)
	for i := 0; i < numIn; i++ {
		ins[i] = em.fb.newRegister(typ.In(i).Kind())
	}

	// Call the function that implements the method.
	shift := em.fb.currentStackShift()
	callOuts := make([]int8, numOut)
	for i := 0; i < numOut; i++ {
		callOuts[i] = em.fb.newRegister(typ.Out(i).Kind())
	}
	recvKind := sm.recv.Kind()
	recv := em.fb.newRegister(recvKind)
	first := 0
	if bound {
		em.fb.emitGetVar(0, recv, recvKind)
	} else {
		em.changeRegister(false, -ins[0], recv, sm.recv, sm.recv)
		first = 1
	}
	for i := first; i < numIn; i++ {
		kind := typ.In(i).Kind()
		arg := em.fb.newRegister(kind)
		em.fb.emitMove(false, ins[i], arg, kind)
	}
	em.fb.emitCallFunc(em.fnStore.scriggoFnIndex(target), shift, pos)
	for i := 0; i < numOut; i++ {
		em.fb.emitMove(false, callOuts[i], outs[i], typ.Out(i).Kind())
	}

	em.fb.exitScope()
	em.fb.end()

	return fn
}

// emitScriggoMethodSelector emits the selector v, with type info ti, that is
// a method value or a method expression of a method declared in Scriggo. The
// result is put into the register reg with type dstType.
func (em *emitter) emitScriggoMethodSelector(v *ast.Selector, ti *typeInfo, reg int8, dstType reflect.Type) {
	sm := ti.method
	if ti.MethodType == methodValueConcrete {
		// Method value: bind the receiver, copied into an interface, to the
		// method.
		rcvrType := em.typ(v.Expr)
		rcvr := em.emitExpr(v.Expr, rcvrType)
		if reg == 0 {
			return
		}
		em.fb.enterStack()
		iface := em.fb.newRegister(reflect.Interface)
		em.changeRegister(false, rcvr, iface, rcvrType, emptyInterfaceType)
		em.fb.emitMethodValue(em.fb.makeStringValue(sm.method.Name), iface, reg, v.Pos())
		em.fb.exitStack()
		em.changeRegister(false, reg, reg, ti.Type, dstType)
		return
	}
	// Method expression.
	if reg == 0 {
		return
	}
	var fn *runtime.Function
	if ti.Type == sm.funcType {
		fn = em.methodFunction(sm)
	} else {
		fn = em.methodPtrAdapter(sm, ti.Type)
	}
	em.fb.emitLoadFunc(false, em.fnStore.scriggoFnIndex(fn), reg)
	em.changeRegister(false, reg, reg, ti.Type, dstType)
}

// emitScriggoMethodCall emits the call to the method declared in Scriggo that
// is called by call. The call can be a method call, as in x.M(...), or a call
// of a method expression, as in T.M(x, ...). goStmt and deferStmt have the
// same meaning as in emitCallNode.
//
// If the call can not be emitted as a direct call, it returns false.
func (em *emitter) emitScriggoMethodCall(call *ast.Call, goStmt, deferStmt bool) ([]int8, []reflect.Type, bool) {
	funTi := em.ti(call.Func)
	sm := funTi.method
	var args []ast.Expression
	var recvAsArg bool
	switch {
	case funTi.MethodType == methodCallConcrete:
		rcvr := call.Func.(*ast.Selector).Expr
		args = append([]ast.Expression{rcvr}, call.Args...)
		recvAsArg = true
	case funTi.MethodType == noMethod && funTi.Type == sm.funcType:
		args = call.Args
	default:
		return nil, nil, false
	}
	fn := em.methodFunction(sm)
	stackShift := em.fb.currentStackShift()
	opts := callOptions{receiverAsArg: recvAsArg, callHasDots: call.IsVariadic}
	regs, types := em.prepareCallParameters(funTi.Type, args, opts)
	index := em.fnStore.scriggoFnIndex(fn)
	if goStmt {
		em.fb.emitGo()
	}
	if deferStmt {
		argsShift := stackDifference(em.fb.currentStackShift(), stackShift)
		reg := em.fb.newRegister(reflect.Func)
		em.fb.emitLoadFunc(false, index, reg)
		em.fb.emitDefer(reg, runtime.NoVariadicArgs, stackShift, argsShift, fn.Type)
		return regs, types, true
	}
	em.fb.emitCallFunc(index, stackShift, call.Pos())
	return regs, types, true
}
