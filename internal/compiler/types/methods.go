// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"
	"sort"
	"unicode"
	"unicode/utf8"

	"github.com/open2b/scriggo/internal/runtime"
)

// A Method is a method declared in Scriggo code on a defined type.
type Method struct {
	Name    string       // name.
	Type    reflect.Type // type, without the receiver.
	Pointer bool         // reports whether the receiver is a pointer.

	// Func is the function that implements the method. Its first parameter
	// is the receiver.
	Func *runtime.Function

	// Indirect is the function that implements the method, called through a
	// pointer to the receiver. It is only used for methods with a value
	// receiver and it is nil until it is needed.
	Indirect *runtime.Function

	// Bound is the function that implements the method with the receiver
	// bound as its only non-local variable.
	Bound *runtime.Function
}

// methodSet holds the methods declared on a defined type.
type methodSet struct {
	types   *Types    // types that created the defined type.
	methods []*Method // methods sorted by name.
}

// lookup returns the method with the given name or nil if there is no such
// method.
func (ms *methodSet) lookup(name string) *Method {
	i := ms.search(name)
	if i < len(ms.methods) && ms.methods[i].Name == name {
		return ms.methods[i]
	}
	return nil
}

// search returns the index of the method with the given name, or the index
// where it would be inserted if not present.
func (ms *methodSet) search(name string) int {
	return sort.Search(len(ms.methods), func(i int) bool {
		return ms.methods[i].Name >= name
	})
}

// exported returns the exported methods callable on a receiver, sorted by
// name. If ptr is false only the methods with a value receiver are returned.
func (ms *methodSet) exported(ptr bool) []*Method {
	methods := make([]*Method, 0, len(ms.methods))
	for _, m := range ms.methods {
		if (ptr || !m.Pointer) && isExported(m.Name) {
			methods = append(methods, m)
		}
	}
	return methods
}

// reflectMethod returns m as a reflect.Method of the receiver type recv,
// with the given index. As for the methods of non-interface types returned
// by the reflect package, the method type includes the receiver.
func (ms *methodSet) reflectMethod(m *Method, recv reflect.Type, index int) reflect.Method {
	in := make([]reflect.Type, m.Type.NumIn()+1)
	in[0] = recv
	for i := 1; i < len(in); i++ {
		in[i] = m.Type.In(i - 1)
	}
	out := make([]reflect.Type, m.Type.NumOut())
	for i := range out {
		out[i] = m.Type.Out(i)
	}
	return reflect.Method{
		Name:  m.Name,
		Type:  ms.types.FuncOf(in, out, m.Type.IsVariadic()),
		Index: index,
	}
}

// AddMethod adds to the defined type t, created by types, a method with the
// given name and type, where typ does not include the receiver. ptr reports
// whether the receiver is a pointer.
//
// If t already has a method with the same name, AddMethod returns the
// already declared method and false.
func (types *Types) AddMethod(t reflect.Type, name string, typ reflect.Type, ptr bool) (*Method, bool) {
	dt, ok := t.(definedType)
	if !ok || dt.methods.types != types {
		panic(internalError("cannot add a method to type %s", t))
	}
	ms := dt.methods
	i := ms.search(name)
	if i < len(ms.methods) && ms.methods[i].Name == name {
		return ms.methods[i], false
	}
	m := &Method{Name: name, Type: typ, Pointer: ptr}
	ms.methods = append(ms.methods, nil)
	copy(ms.methods[i+1:], ms.methods[i:])
	ms.methods[i] = m
	return m, true
}

// Defines reports whether t is a defined type created by types.
func (types *Types) Defines(t reflect.Type) bool {
	dt, ok := t.(definedType)
	return ok && dt.methods.types == types
}

// MethodOf returns the method with the given name declared in Scriggo code
// on t, if t is a defined type, or on the element type of t, if t is a
// pointer to a defined type. It returns nil if there is no such method.
//
// Note that the returned method does not necessarily belong to the method
// set of t, as a method with a pointer receiver does not belong to the
// method set of the value type.
func MethodOf(t reflect.Type, name string) *Method {
	switch t := t.(type) {
	case definedType:
		return t.methods.lookup(name)
	case ptrType:
		if dt, ok := t.elem.(definedType); ok {
			return dt.methods.lookup(name)
		}
	}
	return nil
}

// Methods returns the methods declared in Scriggo code on the defined type t,
// sorted by name. It returns nil if t is not a defined type.
func Methods(t reflect.Type) []*Method {
	if dt, ok := t.(definedType); ok {
		return dt.methods.methods
	}
	return nil
}

// implementsScriggo reports whether the Scriggo type x, that is not an
// interface type, implements the interface type y using the methods
// declared in Scriggo code.
func implementsScriggo(x, y reflect.Type) bool {
	n := y.NumMethod()
	for i := 0; i < n; i++ {
		ym := y.Method(i)
		if ym.PkgPath != "" {
			// A method declared in Scriggo code cannot implement an
			// unexported method of an interface declared in another package.
			return false
		}
		m := MethodOf(x, ym.Name)
		if m == nil || m.Pointer && x.Kind() != reflect.Ptr {
			return false
		}
		if !identical(m.Type, ym.Type, false, false) {
			return false
		}
	}
	return true
}

// boundMethod returns the bound function of the method with the given name
// in the method set of ms, where ptr reports whether the receiver is a
// pointer. deref reports whether the receiver must be dereferenced before
// being bound.
func (ms *methodSet) boundMethod(name string, ptr bool) (fn *runtime.Function, deref bool) {
	m := ms.lookup(name)
	if m == nil || m.Pointer && !ptr {
		return nil, false
	}
	return m.Bound, ptr && !m.Pointer
}

// isExported reports whether name is exported.
func isExported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}
