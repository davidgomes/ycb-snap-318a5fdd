// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package compiler

import (
	"reflect"
	"strings"

	"github.com/open2b/scriggo/ast"
	"github.com/open2b/scriggo/internal/compiler/types"
)

// declareMethod declares the method implemented by the function declaration
// f, with type funcType, on its receiver base type. A method declaration
// "func (r T) M()" is represented by a function named "T.M" with the receiver
// as first parameter.
func (tc *typechecker) declareMethod(f *ast.Func, funcType reflect.Type) {
	dot := strings.IndexByte(f.Ident.Name, '.')
	typeName, name := f.Ident.Name[:dot], f.Ident.Name[dot+1:]
	ti, ok := tc.scopes.FilePackage(typeName)
	if !ok {
		panic(tc.errorf(f.Recv.Type, "undefined: %s", typeName))
	}
	if !ti.IsType() {
		panic(tc.errorf(f.Recv.Type, "%s is not a type", typeName))
	}
	t := ti.Type
	if k := t.Kind(); k == reflect.Ptr || k == reflect.Interface {
		panic(tc.errorf(f.Recv.Type, "invalid receiver type %s (pointer or interface type)", t))
	}
	if t.Kind() == reflect.Struct {
		if _, ok := t.FieldByName(name); ok {
			panic(tc.errorf(f.Ident, "field and method with the same name %s", name))
		}
	}
	numIn := funcType.NumIn()
	in := make([]reflect.Type, numIn-1)
	for i := 1; i < numIn; i++ {
		in[i-1] = funcType.In(i)
	}
	out := make([]reflect.Type, funcType.NumOut())
	for i := range out {
		out[i] = funcType.Out(i)
	}
	m := &types.Method{
		Name:    name,
		Type:    tc.types.FuncOf(in, out, funcType.IsVariadic()),
		Pointer: funcType.In(0).Kind() == reflect.Ptr,
		Func:    f.Ident.Name,
	}
	if tc.types.Method(t, name) != nil {
		panic(tc.errorf(f.Ident, "method %s.%s already declared", typeName, name))
	}
	if !tc.types.AddMethod(t, m) {
		panic(tc.errorf(f.Recv.Type, "cannot define new methods on non-local type %s", t))
	}
	tc.compilation.typeInfos[f] = &typeInfo{Type: funcType, value: m}
}

// isPtrToScriggoType reports whether t is a pointer type whose method set
// includes the Scriggo methods of its element type.
func isPtrToScriggoType(t reflect.Type) bool {
	return t.Kind() == reflect.Ptr && t.Name() == ""
}

// rewriteMethodCall rewrites the call x.M(args), where M is a method declared
// in Scriggo, as the call T.M(x, args), taking the address of x or
// dereferencing it if necessary.
func (tc *typechecker) rewriteMethodCall(call *ast.Call, sel *ast.Selector) {
	if ident, ok := sel.Expr.(*ast.Identifier); ok {
		if ti, _, ok := tc.scopes.Lookup(ident.Name); ok && ti.IsPackage() {
			return
		}
	}
	t := tc.checkExprOrType(sel.Expr)
	if t.Nil() || t.IsType() || t.Type == nil {
		return
	}
	m := tc.types.Method(t.Type, sel.Ident)
	if m == nil {
		return
	}
	if _, ok := tc.scopes.FilePackage(m.Func); !ok {
		panic(tc.errorf(sel, "cannot call method %s of a type declared in another package", sel))
	}
	rcv := sel.Expr
	isPtr := isPtrToScriggoType(t.Type)
	switch {
	case m.Pointer && !isPtr:
		if !t.Addressable() {
			panic(tc.errorf(sel, "cannot call pointer method %s on %s", sel.Ident, t.Type))
		}
		if ident, ok := rcv.(*ast.Identifier); ok {
			if _, decl, ok := tc.scopes.LookupInFunc(ident.Name); ok {
				tc.compilation.indirectVars[decl] = true
			}
		}
		rcv = ast.NewUnaryOperator(rcv.Pos(), ast.OperatorAddress, rcv)
		tc.compilation.typeInfos[rcv] = &typeInfo{Type: tc.types.PointerTo(t.Type)}
	case !m.Pointer && isPtr:
		rcv = ast.NewUnaryOperator(rcv.Pos(), ast.OperatorPointer, rcv)
		tc.compilation.typeInfos[rcv] = &typeInfo{Type: t.Type.Elem(), Properties: propertyAddressable}
	}
	call.Func = ast.NewIdentifier(sel.Pos(), m.Func)
	call.Args = append([]ast.Expression{rcv}, call.Args...)
}

// checkScriggoMethodExpression checks the method expression expr, where t is
// the type info of the type and m is a method declared in Scriggo.
func (tc *typechecker) checkScriggoMethodExpression(t *typeInfo, expr *ast.Selector, m *types.Method) *typeInfo {
	isPtr := isPtrToScriggoType(t.Type)
	if m.Pointer && !isPtr {
		panic(tc.errorf(expr, "invalid method expression %s (needs pointer receiver: (*%s).%s)", expr, expr.Expr, expr.Ident))
	}
	if !m.Pointer && isPtr {
		panic(tc.errorf(expr, "method expression %s with a value receiver method on a pointer type is not supported", expr))
	}
	if _, ok := tc.scopes.FilePackage(m.Func); !ok {
		panic(tc.errorf(expr, "cannot use method %s of a type declared in another package", expr))
	}
	ident := ast.NewIdentifier(expr.Pos(), m.Func)
	ti := tc.checkExpr(ident)
	return &typeInfo{Type: ti.Type, replacement: ident}
}
