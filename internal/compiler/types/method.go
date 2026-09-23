// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"
	"sort"

	"github.com/open2b/scriggo/internal/runtime"
)

// A Method is a method declared in Scriggo code on a defined type.
type Method struct {
	Name string

	// PkgPath is the package path of the method if it is not exported,
	// otherwise it is empty.
	PkgPath string

	// Type is the type of the method without the receiver.
	Type reflect.Type

	// Func is the type of the function that implements the method, whose
	// first parameter is the receiver.
	Func reflect.Type

	// Pointer reports whether the method has a pointer receiver.
	Pointer bool

	// Fn is the function that implements the method. It is set by the
	// emitter.
	Fn *runtime.Function
}

// methodSet holds the methods declared on a defined type, sorted by name.
type methodSet struct {
	methods []*Method
}

func (ms *methodSet) lookup(name string) (*Method, bool) {
	if ms == nil {
		return nil, false
	}
	i := sort.Search(len(ms.methods), func(i int) bool { return ms.methods[i].Name >= name })
	if i < len(ms.methods) && ms.methods[i].Name == name {
		return ms.methods[i], true
	}
	return nil, false
}

// exported returns the exported methods, including the pointer receiver
// methods only if pointer is true.
func (ms *methodSet) exported(pointer bool) []*Method {
	if ms == nil {
		return nil
	}
	var methods []*Method
	for _, m := range ms.methods {
		if m.PkgPath == "" && (pointer || !m.Pointer) {
			methods = append(methods, m)
		}
	}
	return methods
}

// AddMethod adds the method m to the defined type t. t must be a type
// returned by DefinedOf and must not already have a method with the same name.
func AddMethod(t reflect.Type, m *Method) {
	dt, ok := t.(definedType)
	if !ok {
		panic(internalError("cannot add method to type %s", t))
	}
	ms := dt.methods
	i := sort.Search(len(ms.methods), func(i int) bool { return ms.methods[i].Name >= m.Name })
	ms.methods = append(ms.methods, nil)
	copy(ms.methods[i+1:], ms.methods[i:])
	ms.methods[i] = m
}

// DeclaredMethod returns the method with the given name declared on the
// defined type t, with either a value or a pointer receiver. It returns
// false if t is not a type returned by DefinedOf or if it does not have
// such method.
func DeclaredMethod(t reflect.Type, name string) (*Method, bool) {
	dt, ok := t.(definedType)
	if !ok {
		return nil, false
	}
	return dt.methods.lookup(name)
}

// HasDeclaredMethods reports whether t is a type returned by DefinedOf, or a
// pointer to such type, with at least one declared method.
func HasDeclaredMethods(t reflect.Type) bool {
	if pt, ok := t.(ptrType); ok {
		t = pt.elem
	}
	dt, ok := t.(definedType)
	return ok && len(dt.methods.methods) > 0
}

// toReflectMethod returns m as a reflect.Method with index i.
func toReflectMethod(m *Method, i int) reflect.Method {
	return reflect.Method{Name: m.Name, PkgPath: m.PkgPath, Type: m.Func, Index: i}
}

// scriggoMethod implements the ScriggoMethod method of the runtime.Methoder
// interface for the defined type t, or for a pointer to t if pointer is true.
func scriggoMethod(t definedType, name string, pointer bool) (*runtime.Function, bool, bool) {
	m, ok := t.methods.lookup(name)
	if !ok || m.Pointer && !pointer {
		return nil, false, false
	}
	return m.Fn, pointer && !m.Pointer, true
}

// implementsWithMethods reports whether the Scriggo type x implements the
// interface type y.
func implementsWithMethods(x, y reflect.Type) bool {
	n := y.NumMethod()
	if n == 0 {
		return true
	}
	pointer := false
	if pt, ok := x.(ptrType); ok {
		x = pt.elem
		pointer = true
	}
	dt, ok := x.(definedType)
	if !ok {
		return false
	}
	for i := 0; i < n; i++ {
		ym := y.Method(i)
		if ym.PkgPath != "" {
			return false
		}
		xm, ok := dt.methods.lookup(ym.Name)
		if !ok || xm.Pointer && !pointer || !sameMethodSignature(xm.Func, ym.Type) {
			return false
		}
	}
	return true
}

// MissingMethod returns the name of a method of the interface type y that
// is not in the method set of the Scriggo type x, and reports whether x has
// such method but with a pointer receiver.
func MissingMethod(x, y reflect.Type) (string, bool) {
	pointer := false
	if pt, ok := x.(ptrType); ok {
		x = pt.elem
		pointer = true
	}
	dt, _ := x.(definedType)
	for i := 0; i < y.NumMethod(); i++ {
		ym := y.Method(i)
		xm, ok := dt.methods.lookup(ym.Name)
		if !ok || ym.PkgPath != "" || !sameMethodSignature(xm.Func, ym.Type) {
			return ym.Name, false
		}
		if xm.Pointer && !pointer {
			return ym.Name, true
		}
	}
	return "", false
}

// sameMethodSignature reports whether the function type fn, whose first
// parameter is the receiver, has the signature of the method type m.
func sameMethodSignature(fn, m reflect.Type) bool {
	numIn := m.NumIn()
	numOut := m.NumOut()
	if fn.NumIn()-1 != numIn || fn.NumOut() != numOut || fn.IsVariadic() != m.IsVariadic() {
		return false
	}
	for i := 0; i < numIn; i++ {
		if fn.In(i+1) != m.In(i) {
			return false
		}
	}
	for i := 0; i < numOut; i++ {
		if fn.Out(i) != m.Out(i) {
			return false
		}
	}
	return true
}
