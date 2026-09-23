// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package compiler

import (
	"reflect"
	"strconv"

	"github.com/open2b/scriggo/ast"
	"github.com/open2b/scriggo/internal/compiler/types"
)

// methodBaseType returns the element type of t, and true, if t is a
// non-defined pointer type. Otherwise it returns t and false.
func methodBaseType(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() == reflect.Ptr && t.Name() == "" {
		return t.Elem(), true
	}
	return t, false
}

// checkMethodDeclaration checks the receiver and the type of the method
// declaration f and adds the method to the method set of the receiver type.
func (tc *typechecker) checkMethodDeclaration(f *ast.Func) {

	if f.Body == nil {
		panic(tc.errorf(f.Ident.Pos(), "missing function body"))
	}

	// Check the receiver type.
	recv := tc.checkType(f.Recv.Type).Type
	base, isPtr := methodBaseType(recv)
	baseExpr := f.Recv.Type
	if isPtr {
		if op, ok := baseExpr.(*ast.UnaryOperator); ok && op.Op == ast.OperatorPointer {
			baseExpr = op.Expr
		}
	}
	if k := base.Kind(); k == reflect.Ptr || k == reflect.Interface {
		panic(tc.errorf(f.Recv.Type, "invalid receiver type %s (pointer or interface type)", recv))
	}
	local := false
	if ident, ok := baseExpr.(*ast.Identifier); ok && types.IsDefined(base) {
		if ti, ok := tc.scopes.FilePackage(ident.Name); ok && ti.Type == base {
			_, imported := tc.scopes.LookupImport(ident.Name)
			local = !imported
		}
	}
	if !local {
		panic(tc.errorf(f.Recv.Type, "cannot define new methods on non-local type %s", base))
	}

	// Check the function type, with the receiver as first parameter.
	funcType := tc.checkType(f.Type).Type

	name := f.Ident.Name
	if isBlankIdentifier(f.Ident) {
		return
	}
	if _, ok := types.DeclaredMethod(base, name); ok {
		panic(tc.errorf(f.Ident, "method %s.%s already declared", base, name))
	}
	if base.Kind() == reflect.Struct {
		for i := 0; i < base.NumField(); i++ {
			if decodeFieldName(base.Field(i).Name) == name {
				panic(tc.errorf(f.Ident, "field and method with the same name %s", name))
			}
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
		Func:    funcType,
		Pointer: isPtr,
	}
	if !isExported(name) {
		m.PkgPath = tc.path
	}
	types.AddMethod(base, m)

}

// checkDeclaredMethodAccess panics if the method m, selected by expr, is not
// exported and it has been declared in another package.
func (tc *typechecker) checkDeclaredMethodAccess(m *types.Method, expr *ast.Selector) {
	if m.PkgPath != "" && m.PkgPath != tc.path {
		panic(tc.errorf(expr, "%s undefined (cannot refer to unexported field or method %s)", expr, expr.Ident))
	}
}

// checkDeclaredMethodValue checks a method value, or the function of a method
// call, where the method has been declared in Scriggo. If the type of the
// receiver has not such method, it returns nil and false.
//
// If the method has a pointer receiver and the receiver x is not a pointer,
// the receiver is replaced with &x. If the method has a value receiver and
// the receiver x is a pointer, the receiver is replaced with *x.
func (tc *typechecker) checkDeclaredMethodValue(t *typeInfo, expr *ast.Selector) (*typeInfo, bool) {
	base, isPtr := methodBaseType(t.Type)
	m, ok := types.DeclaredMethod(base, expr.Ident)
	if !ok {
		return nil, false
	}
	tc.checkDeclaredMethodAccess(m, expr)
	switch {
	case m.Pointer && !isPtr:
		if !t.Addressable() {
			panic(tc.errorf(expr, "cannot call pointer method %s on %s", expr.Ident, t.Type))
		}
		if ident, ok := expr.Expr.(*ast.Identifier); ok {
			if _, decl, ok := tc.scopes.LookupInFunc(ident.Name); ok {
				tc.compilation.indirectVars[decl] = true
			}
		}
		expr.Expr = ast.NewUnaryOperator(expr.Pos(), ast.OperatorAddress, expr.Expr)
		tc.compilation.typeInfos[expr.Expr] = &typeInfo{Type: tc.types.PointerTo(t.Type)}
	case !m.Pointer && isPtr:
		expr.Expr = ast.NewUnaryOperator(expr.Pos(), ast.OperatorPointer, expr.Expr)
		tc.compilation.typeInfos[expr.Expr] = &typeInfo{Type: base, Properties: propertyAddressable}
	}
	return &typeInfo{Type: m.Type, value: m, MethodType: methodValueDeclared}, true
}

// declaredMethodExpression returns the type info of the method expression
// expr, where recv is the receiver type and m is a method declared in Scriggo.
//
// The method expression T.M is replaced with the function literal
//
//	func(r T, a0 A0, ..., an An) (R0, ..., Rm) { return r.M(a0, ..., an) }
func (tc *typechecker) declaredMethodExpression(recv reflect.Type, m *types.Method, expr *ast.Selector) *typeInfo {
	pos := expr.Pos()
	typeExpr := func(t reflect.Type) ast.Expression {
		ident := ast.NewIdentifier(pos, t.String())
		tc.compilation.typeInfos[ident] = &typeInfo{Type: t, Properties: propertyIsType}
		return ident
	}
	mt := m.Type
	variadic := mt.IsVariadic()
	numIn := mt.NumIn()
	params := make([]*ast.Parameter, numIn+1)
	params[0] = ast.NewParameter(ast.NewIdentifier(pos, "$recv"), typeExpr(recv))
	args := make([]ast.Expression, numIn)
	for i := 0; i < numIn; i++ {
		t := mt.In(i)
		if variadic && i == numIn-1 {
			t = t.Elem()
		}
		name := "$arg" + strconv.Itoa(i)
		params[i+1] = ast.NewParameter(ast.NewIdentifier(pos, name), typeExpr(t))
		args[i] = ast.NewIdentifier(pos, name)
	}
	result := make([]*ast.Parameter, mt.NumOut())
	for i := range result {
		result[i] = ast.NewParameter(nil, typeExpr(mt.Out(i)))
	}
	call := ast.NewCall(pos, ast.NewSelector(pos, ast.NewIdentifier(pos, "$recv"), m.Name), args, variadic)
	var body ast.Node = call
	if len(result) > 0 {
		body = ast.NewReturn(pos, []ast.Expression{call})
	}
	typ := ast.NewFuncType(pos, false, params, result, variadic)
	lit := ast.NewFunc(pos, nil, typ, ast.NewBlock(pos, []ast.Node{body}), false, ast.FormatText)
	ti := tc.checkExpr(lit)
	return &typeInfo{Type: ti.Type, replacement: lit}
}
