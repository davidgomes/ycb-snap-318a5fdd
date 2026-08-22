// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"

	"github.com/open2b/scriggo/ast"
	"github.com/open2b/scriggo/internal/runtime"
)

// Method is a method declared on a Scriggo-defined type.
type Method struct {
	Name      string
	PtrRecv   bool
	Node      *ast.Func
	Type      reflect.Type // declared signature, including the receiver.
	ValueType reflect.Type // signature without the receiver.
	Func      *runtime.Function
}

type methodSet struct {
	types *Types
	list  []*Method
}

func (ms *methodSet) lookup(name string, ptr bool) *Method {
	if ms == nil {
		return nil
	}
	for _, m := range ms.list {
		if m.Name == name && (!m.PtrRecv || ptr) {
			return m
		}
	}
	return nil
}

func (ms *methodSet) has(name string) bool {
	if ms == nil {
		return false
	}
	for _, m := range ms.list {
		if m.Name == name {
			return true
		}
	}
	return false
}

// IsDefined reports whether t is a type defined in Scriggo.
func IsDefined(t reflect.Type) bool {
	_, ok := t.(definedType)
	return ok
}

// Underlying returns the type literal of a defined type, or t itself.
func Underlying(t reflect.Type) reflect.Type {
	if dt, ok := t.(definedType); ok {
		return dt.Type
	}
	return t
}

// definedBase returns the defined type underlying t, which may be T or *T.
func definedBase(t reflect.Type) (definedType, bool, bool) {
	if dt, ok := t.(definedType); ok {
		return dt, false, true
	}
	if pt, ok := t.(ptrType); ok {
		if dt, ok := pt.elem.(definedType); ok {
			return dt, true, true
		}
	}
	return definedType{}, false, false
}

// LookupMethod returns the declared method name on t, where t may be T or *T.
// Pointer-receiver methods are visible only on *T.
func LookupMethod(t reflect.Type, name string) (*Method, bool) {
	dt, ptr, ok := definedBase(t)
	if !ok {
		return nil, false
	}
	m := dt.methods.lookup(name, ptr)
	if m == nil {
		return nil, false
	}
	return m, true
}

// HasMethod reports whether a method named name is already declared on the
// defined type t (value and pointer receivers share a name).
func HasMethod(t reflect.Type, name string) bool {
	dt, _, ok := definedBase(t)
	if !ok {
		return false
	}
	return dt.methods.has(name)
}

// AddMethod records a method on the defined type t. t must be the receiver
// base type (not a pointer).
func (types *Types) AddMethod(t reflect.Type, m *Method) {
	dt, ok := t.(definedType)
	if !ok {
		panic(internalError("AddMethod: %s is not a defined type", t))
	}
	if dt.methods == nil {
		panic(internalError("AddMethod: missing method set"))
	}
	dt.methods.list = append(dt.methods.list, m)
}

// SetMethodFunc attaches the compiled function to a previously added method.
func (types *Types) SetMethodFunc(t reflect.Type, name string, fn *runtime.Function) {
	dt, _, ok := definedBase(t)
	if !ok {
		panic(internalError("SetMethodFunc: %s is not a defined type", t))
	}
	for _, m := range dt.methods.list {
		if m.Name == name {
			m.Func = fn
			return
		}
	}
	panic(internalError("SetMethodFunc: method %s.%s not found", t, name))
}

func (ms *methodSet) methodType(recv reflect.Type, m *Method, ptr bool) reflect.Type {
	if ms == nil || ms.types == nil {
		return m.Type
	}
	if ptr && !m.PtrRecv {
		in := []reflect.Type{recv}
		for i := 0; i < m.ValueType.NumIn(); i++ {
			in = append(in, m.ValueType.In(i))
		}
		out := make([]reflect.Type, m.ValueType.NumOut())
		for i := range out {
			out[i] = m.ValueType.Out(i)
		}
		return ms.types.FuncOf(in, out, m.ValueType.IsVariadic())
	}
	return m.Type
}

func reflectMethod(name string, typ reflect.Type, fn *runtime.Function) reflect.Method {
	var f reflect.Value
	if fn != nil {
		f = reflect.ValueOf(fn)
	}
	return reflect.Method{Name: name, Type: typ, Func: f}
}

func methodImplements(methodType, ifaceMethodType reflect.Type, hasRecv bool) bool {
	off := 0
	if hasRecv {
		off = 1
	}
	if methodType.Kind() != reflect.Func || ifaceMethodType.Kind() != reflect.Func {
		return false
	}
	if methodType.NumIn()-off != ifaceMethodType.NumIn() {
		return false
	}
	if methodType.NumOut() != ifaceMethodType.NumOut() {
		return false
	}
	if methodType.IsVariadic() != ifaceMethodType.IsVariadic() {
		return false
	}
	for i := 0; i < ifaceMethodType.NumIn(); i++ {
		if !identical(methodType.In(i+off), ifaceMethodType.In(i), false, false) {
			return false
		}
	}
	for i := 0; i < ifaceMethodType.NumOut(); i++ {
		if !identical(methodType.Out(i), ifaceMethodType.Out(i), false, false) {
			return false
		}
	}
	return true
}

func implementsMethodSet(t, u reflect.Type) bool {
	if u.Kind() != reflect.Interface {
		return false
	}
	hasRecv := t.Kind() != reflect.Interface
	for i := 0; i < u.NumMethod(); i++ {
		um := u.Method(i)
		m, ok := t.MethodByName(um.Name)
		if !ok || !methodImplements(m.Type, um.Type, hasRecv) {
			return false
		}
	}
	return true
}
