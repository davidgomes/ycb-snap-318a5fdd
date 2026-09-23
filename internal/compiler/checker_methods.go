// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package compiler

import (
	"reflect"

	"github.com/open2b/scriggo/ast"
	"github.com/open2b/scriggo/internal/compiler/types"
)

// scriggoMethod holds the information about a method declared in Scriggo.
type scriggoMethod struct {
	decl     *ast.Func     // method declaration.
	pkg      string        // path of the package that declares the method.
	recv     reflect.Type  // receiver type, T or *T.
	funcType reflect.Type  // type of the function that implements the method; the receiver is its first parameter.
	method   *types.Method // method in the method set of the receiver base type.
}

// declareMethod checks the receiver and the signature of the method
// declaration fn and adds the method to the methods of its receiver base
// type. localTypes contains the types defined in the current package.
func (tc *typechecker) declareMethod(fn *ast.Func, localTypes map[reflect.Type]bool) {

	if fn.Body == nil {
		panic(tc.errorf(fn.Ident.Pos(), "missing function body"))
	}

	// Check the receiver type.
	recvType := tc.checkType(fn.Recv.Type).Type
	base := recvType
	isPtr := false
	if base.Kind() == reflect.Ptr && base.Name() == "" {
		base = base.Elem()
		isPtr = true
	}
	if !localTypes[base] {
		if base.Name() == "" || base.Kind() == reflect.Ptr || base.Kind() == reflect.Interface {
			panic(tc.errorf(fn.Recv.Type, "invalid receiver type %s", recvType))
		}
		panic(tc.errorf(fn.Recv.Type, "cannot define new methods on non-local type %s", base))
	}
	if k := base.Kind(); k == reflect.Ptr || k == reflect.Interface {
		panic(tc.errorf(fn.Recv.Type, "invalid receiver type %s (pointer or interface type)", recvType))
	}

	// Check the method signature.
	funcType := tc.checkType(fn.Type).Type
	if recv := fn.Recv.Ident; recv != nil && !isBlankIdentifier(recv) {
		for _, params := range [][]*ast.Parameter{fn.Type.Parameters, fn.Type.Result} {
			for _, param := range params {
				if param.Ident != nil && param.Ident.Name == recv.Name {
					panic(tc.errorf(param.Ident, "duplicate argument %s", recv.Name))
				}
			}
		}
	}

	name := fn.Ident.Name
	if name != "_" {
		if m, _ := types.LookupMethod(base, name); m != nil {
			panic(tc.errorf(fn.Ident, "method redeclared: %s.%s", base, name))
		}
		if base.Kind() == reflect.Struct {
			for i := 0; i < base.NumField(); i++ {
				if decodeFieldName(base.Field(i).Name) == name {
					panic(tc.errorf(fn.Ident, "type %s has both field and method named %s", base, name))
				}
			}
		}
	}

	// Make the type of the function that implements the method.
	in := make([]reflect.Type, funcType.NumIn()+1)
	in[0] = recvType
	for i := 1; i < len(in); i++ {
		in[i] = funcType.In(i - 1)
	}
	out := make([]reflect.Type, funcType.NumOut())
	for i := range out {
		out[i] = funcType.Out(i)
	}

	sm := &scriggoMethod{
		decl:     fn,
		pkg:      tc.path,
		recv:     recvType,
		funcType: tc.types.FuncOf(in, out, funcType.IsVariadic()),
		method:   &types.Method{Name: name, Type: funcType, PointerReceiver: isPtr},
	}
	if name != "_" {
		types.AddMethod(base, sm.method)
	}
	tc.compilation.methods[sm.method] = sm
	tc.compilation.typeInfos[fn] = &typeInfo{Type: sm.funcType, method: sm}

}

// lookupScriggoMethod returns the Scriggo method with the given name in the
// method set of typ or, if typ is not a pointer type, in the method set of
// *typ. inMethodSet reports whether the method is in the method set of typ.
// If there is no such method, it returns nil.
func (tc *typechecker) lookupScriggoMethod(typ reflect.Type, name string) (sm *scriggoMethod, inMethodSet bool) {
	m, ok := types.LookupMethod(typ, name)
	if m == nil {
		return nil, false
	}
	return tc.compilation.methods[m], ok
}

// checkScriggoMethodSelector checks the selector expr, where t is the type info
// of the selector operand, if it selects a method declared in Scriggo. If it
// does not select a method declared in Scriggo, it returns nil and false.
//
// If the receiver must be addressed or dereferenced, the operand of expr is
// replaced with the expression &x or *x respectively.
func (tc *typechecker) checkScriggoMethodSelector(t *typeInfo, expr *ast.Selector) (*typeInfo, bool) {
	name := expr.Ident
	sm, inMethodSet := tc.lookupScriggoMethod(t.Type, name)
	if sm == nil {
		return nil, false
	}
	if !isExported(name) && sm.pkg != tc.path {
		panic(tc.errorf(expr, "%s undefined (cannot refer to unexported field or method %s)", expr, name))
	}
	switch {
	case !inMethodSet:
		// x.M is shorthand for (&x).M if x is addressable.
		if !t.Addressable() {
			panic(tc.errorf(expr, "cannot call pointer method %s on %s", name, t.Type))
		}
		if ident, ok := expr.Expr.(*ast.Identifier); ok {
			if _, decl, ok := tc.scopes.LookupInFunc(ident.Name); ok {
				tc.compilation.indirectVars[decl] = true
			}
		}
		expr.Expr = ast.NewUnaryOperator(expr.Expr.Pos(), ast.OperatorAddress, expr.Expr)
		tc.compilation.typeInfos[expr.Expr] = &typeInfo{Type: sm.recv}
	case t.Type.Kind() == reflect.Ptr && !sm.method.PointerReceiver:
		// x.M is shorthand for (*x).M if x is a pointer.
		expr.Expr = ast.NewUnaryOperator(expr.Expr.Pos(), ast.OperatorPointer, expr.Expr)
		tc.compilation.typeInfos[expr.Expr] = &typeInfo{Type: sm.recv, Properties: propertyAddressable}
	}
	return &typeInfo{
		Type:       sm.method.Type,
		MethodType: methodValueConcrete,
		method:     sm,
	}, true
}

// checkScriggoMethodExpression checks the method expression expr, where t is
// the type info of the type, if it refers to a method declared in Scriggo. If
// it does not refer to a method declared in Scriggo, it returns nil and false.
func (tc *typechecker) checkScriggoMethodExpression(t *typeInfo, expr *ast.Selector) (*typeInfo, bool) {
	name := expr.Ident
	sm, inMethodSet := tc.lookupScriggoMethod(t.Type, name)
	if sm == nil {
		return nil, false
	}
	if !inMethodSet {
		panic(tc.errorf(expr, "invalid method expression %s (needs pointer receiver: (*%s).%s)", expr, expr.Expr, name))
	}
	if !isExported(name) && sm.pkg != tc.path {
		panic(tc.errorf(expr, "%s undefined (cannot refer to unexported field or method %s)", expr, name))
	}
	typ := sm.funcType
	if t.Type != sm.recv {
		// (*T).M where M has a value receiver.
		ft := sm.funcType
		in := make([]reflect.Type, ft.NumIn())
		in[0] = t.Type
		for i := 1; i < len(in); i++ {
			in[i] = ft.In(i)
		}
		out := make([]reflect.Type, ft.NumOut())
		for i := range out {
			out[i] = ft.Out(i)
		}
		typ = tc.types.FuncOf(in, out, ft.IsVariadic())
	}
	return &typeInfo{Type: typ, method: sm}, true
}
