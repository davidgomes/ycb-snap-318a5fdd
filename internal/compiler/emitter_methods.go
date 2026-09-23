// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package compiler

import (
	"reflect"

	"github.com/open2b/scriggo/ast"
	"github.com/open2b/scriggo/internal/compiler/types"
	"github.com/open2b/scriggo/internal/runtime"
)

// methodOf returns the method declared by the method declaration fun, or nil
// if the method has the blank identifier as name.
func (em *emitter) methodOf(fun *ast.Func) *types.Method {
	if isBlankIdentifier(fun.Ident) {
		return nil
	}
	return types.MethodOf(em.typ(fun.Recv.Type), fun.Ident.Name)
}

// declareMethod creates the functions that implement the method declared by
// fun, without emitting its body, and emits the function that implements the
// method with a bound receiver.
func (em *emitter) declareMethod(fun *ast.Func, path string) {
	m := em.methodOf(fun)
	if m == nil || m.Func != nil {
		return
	}
	recv := em.typ(fun.Recv.Type)
	m.Func = newFunction("main", methodFuncName(recv, m.Name), em.methodFuncType(recv, m), path, fun.Pos())
	m.Bound = em.emitMethodWrapper(m, recv, false, path, fun.Pos())
}

// methodExprFunc returns the function of the method expression that selects
// the method m on the receiver type recv.
func (em *emitter) methodExprFunc(m *types.Method, recv reflect.Type) *runtime.Function {
	if m.Pointer || recv.Kind() != reflect.Ptr {
		return m.Func
	}
	if m.Indirect == nil {
		m.Indirect = em.emitMethodWrapper(m, recv, true, m.Func.File, nil)
	}
	return m.Indirect
}

// methodFuncType returns the type of the function that implements the method
// m with the receiver of type recv as first parameter.
func (em *emitter) methodFuncType(recv reflect.Type, m *types.Method) reflect.Type {
	in := make([]reflect.Type, m.Type.NumIn()+1)
	in[0] = recv
	for i := 1; i < len(in); i++ {
		in[i] = m.Type.In(i - 1)
	}
	out := make([]reflect.Type, m.Type.NumOut())
	for i := range out {
		out[i] = m.Type.Out(i)
	}
	return em.types.FuncOf(in, out, m.Type.IsVariadic())
}

// methodFuncName returns the name of the function that implements the method
// with the given name on the receiver type recv.
func methodFuncName(recv reflect.Type, name string) string {
	if recv.Kind() == reflect.Ptr && recv.Name() == "" {
		return "(" + recv.String() + ")." + name
	}
	return recv.String() + "." + name
}

// emitMethodWrapper emits a function that calls the function that implements
// the method m.
//
// If indirect is false, the wrapper has the same type as m and it reads the
// receiver, of type recv, from its first and only non-local variable.
//
// If indirect is true, the wrapper has the receiver recv, a pointer to the
// receiver type of m, as first parameter, and it calls m with the value
// pointed by recv.
func (em *emitter) emitMethodWrapper(m *types.Method, recv reflect.Type, indirect bool, path string, pos *ast.Position) *runtime.Function {

	var fn *runtime.Function
	if indirect {
		fn = newFunction("main", methodFuncName(recv, m.Name), em.methodFuncType(recv, m), path, pos)
	} else {
		fn = newFunction("main", methodFuncName(recv, m.Name)+"-fm", m.Type, path, pos)
	}

	backupFb := em.fb
	em.fb = newBuilder(fn, path)
	em.fb.enterScope()

	typ := m.Type

	// Reserve the registers of the wrapper parameters.
	outs := make([]int8, typ.NumOut())
	for i := range outs {
		outs[i] = em.fb.newRegister(typ.Out(i).Kind())
	}
	var ptr int8
	if indirect {
		ptr = em.fb.newRegister(reflect.Ptr)
	}
	ins := make([]int8, typ.NumIn())
	for i := range ins {
		ins[i] = em.fb.newRegister(typ.In(i).Kind())
	}

	// Emit the call to the method.
	shift := em.fb.currentStackShift()
	callOuts := make([]int8, len(outs))
	for i := range callOuts {
		callOuts[i] = em.fb.newRegister(typ.Out(i).Kind())
	}
	if indirect {
		elem := recv.Elem()
		r := em.fb.newRegister(elem.Kind())
		em.changeRegister(false, -ptr, r, elem, elem)
	} else {
		r := em.fb.newRegister(recv.Kind())
		em.fb.emitGetVar(0, r, recv.Kind())
	}
	for i, in := range ins {
		t := typ.In(i)
		em.changeRegister(false, in, em.fb.newRegister(t.Kind()), t, t)
	}
	em.fb.emitCallFunc(em.fnStore.scriggoFnIndex(m.Func), shift, pos)
	for i, out := range outs {
		t := typ.Out(i)
		em.changeRegister(false, callOuts[i], out, t, t)
	}

	em.fb.exitScope()
	em.fb.end()
	em.fb = backupFb

	return fn
}

// emitScriggoFuncCall emits a call to the Scriggo function fn, with type
// fnType, and the arguments args. It returns the registers and the types of
// the returned values.
func (em *emitter) emitScriggoFuncCall(call *ast.Call, fn *runtime.Function, fnType reflect.Type, args []ast.Expression, opts callOptions, goStmt, deferStmt bool) ([]int8, []reflect.Type) {
	stackShift := em.fb.currentStackShift()
	regs, types := em.prepareCallParameters(fnType, args, opts)
	index := em.fnStore.scriggoFnIndex(fn)
	if goStmt {
		em.fb.emitGo()
	}
	if deferStmt {
		argsShift := stackDifference(em.fb.currentStackShift(), stackShift)
		reg := em.fb.newRegister(reflect.Func)
		em.fb.emitLoadFunc(false, index, reg)
		em.fb.emitDefer(reg, runtime.NoVariadicArgs, stackShift, argsShift, fn.Type)
		return regs, types
	}
	em.fb.emitCallFunc(index, stackShift, call.Pos())
	return regs, types
}
