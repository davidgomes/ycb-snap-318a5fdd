// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"

	"github.com/open2b/scriggo/internal/runtime"
)

// Method is a method declared on a Scriggo defined type.
type Method struct {
	Name    string
	Pointer bool // reports whether the method has a pointer receiver.

	// Recv is the receiver type as declared (T or *T).
	Recv reflect.Type
	// RecvGo is the Go type of the receiver value stored in a register.
	RecvGo reflect.Type
	// Sig is the method signature without the receiver.
	Sig reflect.Type
	// ExprType is the function type of the method expression using the
	// declared receiver.
	ExprType reflect.Type
	// PtrExprType is the function type of (*T).M when M has a value receiver.
	PtrExprType reflect.Type

	Fn        *runtime.Function
	PtrExprFn *runtime.Function
	DeclPos   string
}

type methodTable struct {
	list []*Method
}

// AddMethod adds m to the method set of the defined type base.
// If a method with the same name is already present, prev is that method and
// ok is false.
func AddMethod(base reflect.Type, m *Method) (prev *Method, ok bool) {
	d, isDef := base.(definedType)
	if !isDef || d.methods == nil {
		panic("scriggo: AddMethod on a type that is not a defined type")
	}
	for _, existing := range d.methods.list {
		if existing.Name == m.Name {
			return existing, false
		}
	}
	d.methods.list = append(d.methods.list, m)
	return m, true
}

// LookupMethod returns the method name in the method set of t.
// The method set of a defined type contains only value-receiver methods.
// The method set of a pointer to a defined type contains value and pointer
// receiver methods.
func LookupMethod(t reflect.Type, name string) (*Method, bool) {
	if d, ok := t.(definedType); ok {
		return d.lookup(name, false)
	}
	if p, ok := t.(ptrType); ok {
		if d, ok := p.elem.(definedType); ok {
			return d.lookup(name, true)
		}
	}
	return nil, false
}

// IsDefinedType reports whether t is a type defined in Scriggo.
func IsDefinedType(t reflect.Type) bool {
	_, ok := t.(definedType)
	return ok
}

func (d definedType) lookup(name string, fromPointer bool) (*Method, bool) {
	if d.methods == nil {
		return nil, false
	}
	for _, m := range d.methods.list {
		if m.Name == name && (fromPointer || !m.Pointer) {
			return m, true
		}
	}
	return nil, false
}

func (d definedType) valueMethods() []*Method {
	if d.methods == nil {
		return nil
	}
	var list []*Method
	for _, m := range d.methods.list {
		if !m.Pointer {
			list = append(list, m)
		}
	}
	return list
}

func methodAsReflect(m *Method, fromPointer bool) reflect.Method {
	ft := m.ExprType
	if fromPointer && !m.Pointer {
		ft = m.PtrExprType
	}
	return reflect.Method{Name: m.Name, Type: ft}
}

func runtimeMethods(t reflect.Type, pointer bool) map[string]runtime.ScriggoMethod {
	var d definedType
	var ok bool
	if pointer {
		p, pok := t.(ptrType)
		if !pok {
			return nil
		}
		d, ok = p.elem.(definedType)
	} else {
		d, ok = t.(definedType)
	}
	if !ok || d.methods == nil || len(d.methods.list) == 0 {
		return nil
	}
	out := make(map[string]runtime.ScriggoMethod, len(d.methods.list))
	for _, m := range d.methods.list {
		if m.Pointer && !pointer {
			continue
		}
		out[m.Name] = runtime.ScriggoMethod{
			Fn:        m.Fn,
			ValueRecv: !m.Pointer,
			RecvGo:    m.RecvGo,
			Type:      m.Sig,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func scriggoImplements(x, y reflect.Type) bool {
	n := y.NumMethod()
	if n == 0 {
		return true
	}
	for i := 0; i < n; i++ {
		im := y.Method(i)
		if im.PkgPath != "" {
			return false
		}
		m, ok := LookupMethod(x, im.Name)
		if !ok || !signaturesMatch(m.Sig, im.Type) {
			return false
		}
	}
	return true
}

// GoType returns the Go type underlying a Scriggo type, or t itself.
func GoType(t reflect.Type) reflect.Type {
	if st, ok := t.(runtime.ScriggoType); ok {
		return st.GoType()
	}
	return t
}

func signaturesMatch(a, b reflect.Type) bool {
	if a == nil || b == nil || a.Kind() != reflect.Func || b.Kind() != reflect.Func {
		return false
	}
	if a.NumIn() != b.NumIn() || a.NumOut() != b.NumOut() || a.IsVariadic() != b.IsVariadic() {
		return false
	}
	for i := 0; i < a.NumIn(); i++ {
		if a.In(i) != b.In(i) {
			return false
		}
	}
	for i := 0; i < a.NumOut(); i++ {
		if a.Out(i) != b.Out(i) {
			return false
		}
	}
	return true
}
