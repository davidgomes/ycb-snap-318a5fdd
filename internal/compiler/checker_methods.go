// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package compiler

import (
	"reflect"
	"strconv"

	"github.com/open2b/scriggo/ast"
	"github.com/open2b/scriggo/internal/compiler/types"
)

// scriggoMethodRef identifies a method declared in Scriggo.
type scriggoMethodRef struct {
	node    *ast.Func
	ptrRecv bool
	expr    bool // method expression (T.M / (*T).M), not a method value.
}

// checkReceiver validates a method receiver and returns the receiver base
// type and whether the declared receiver is a pointer.
func (tc *typechecker) checkReceiver(recv *ast.Parameter) (reflect.Type, bool) {
	recvType := tc.checkType(recv.Type).Type
	ptr := false
	if recvType.Kind() == reflect.Ptr {
		ptr = true
		elem := recvType.Elem()
		if elem.Kind() == reflect.Ptr {
			panic(tc.errorf(recv.Type, "invalid receiver type %s (pointer to pointer)", recv.Type))
		}
		recvType = elem
	}
	if !types.IsDefined(recvType) {
		if recvType.Name() != "" && recvType.PkgPath() != "" {
			panic(tc.errorf(recv.Type, "cannot define new methods on non-local type %s", recvType))
		}
		panic(tc.errorf(recv.Type, "invalid receiver type %s (%s is not a defined type)", recv.Type, recvType))
	}
	if ti, ok := tc.scopes.FilePackage(recvType.Name()); !ok || ti.Type != recvType {
		panic(tc.errorf(recv.Type, "cannot define new methods on non-local type %s", recvType))
	}
	u := types.Underlying(recvType)
	if k := u.Kind(); k == reflect.Ptr || k == reflect.Interface {
		panic(tc.errorf(recv.Type, "invalid receiver type %s (%s is a %s type)", recv.Type, recvType, k))
	}
	return recvType, ptr
}

// attachMethod records a method declaration on its receiver type.
func (tc *typechecker) attachMethod(f *ast.Func) {
	if f.Ident == nil {
		panic(tc.errorf(f, "invalid method declaration"))
	}
	base, ptr := tc.checkReceiver(f.Receiver)
	if types.HasMethod(base, f.Ident.Name) {
		panic(tc.errorf(f.Ident, "method %s.%s already declared", base.Name(), f.Ident.Name))
	}
	declaredRecv := base
	if ptr {
		declaredRecv = tc.types.PointerTo(base)
	}
	ft := f.Type.Reflect
	in := make([]reflect.Type, 0, ft.NumIn()+1)
	in = append(in, declaredRecv)
	for i := 0; i < ft.NumIn(); i++ {
		in = append(in, ft.In(i))
	}
	out := make([]reflect.Type, ft.NumOut())
	for i := range out {
		out[i] = ft.Out(i)
	}
	tc.types.AddMethod(base, &types.Method{
		Name:      f.Ident.Name,
		PtrRecv:   ptr,
		Node:      f,
		Type:      tc.types.FuncOf(in, out, ft.IsVariadic()),
		ValueType: ft,
	})
}

func synthesizeMethodExpression(node *ast.Func, recvTypeExpr ast.Expression, pos *ast.Position) *ast.Func {
	recvIdent := ast.NewIdentifier(pos, "$recv")
	params := []*ast.Parameter{ast.NewParameter(recvIdent, recvTypeExpr)}
	args := make([]ast.Expression, 0, len(node.Type.Parameters))
	for i, p := range node.Type.Parameters {
		name := "$a" + strconv.Itoa(i)
		ident := ast.NewIdentifier(pos, name)
		params = append(params, ast.NewParameter(ident, p.Type))
		args = append(args, ident)
	}
	results := make([]*ast.Parameter, len(node.Type.Result))
	for i, r := range node.Type.Result {
		results[i] = ast.NewParameter(nil, r.Type)
	}
	sel := ast.NewSelector(pos, recvIdent, node.Ident.Name)
	call := ast.NewCall(pos, sel, args, node.Type.IsVariadic)
	var body []ast.Node
	if len(results) > 0 {
		body = []ast.Node{ast.NewReturn(pos, []ast.Expression{call})}
	} else {
		body = []ast.Node{call}
	}
	typ := ast.NewFuncType(pos, false, params, results, node.Type.IsVariadic)
	return ast.NewFunc(pos, nil, typ, ast.NewBlock(pos, body), false, ast.FormatText)
}
