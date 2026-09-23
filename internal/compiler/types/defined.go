// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"

	"github.com/open2b/scriggo/internal/runtime"
)

// definedType represents a type defined in the Scriggo compiled code with a
// type definition, where the underlying type can be both a type compiled in
// the Scriggo code or in gc.
type definedType struct {
	// The embedded reflect.Type can be both a reflect.Type implemented by the
	// package "reflect" or a ScriggoType. In the other implementations of
	// ScriggoType the embedded reflect.Type is always a gc compiled type.
	reflect.Type

	name string

	// sign ensures that a definedType returned by DefinedOf is always
	// different from every other instance of definedType.
	// By doing so, two reflect.Types are equal if and only if the type they
	// represents are identical (every defined type, in Go, is different from
	// every other type).
	sign *byte

	// methods holds the methods declared in Scriggo with this type (or a
	// pointer to it) as receiver.
	methods *methodSet
}

// Method represents a method declared in Scriggo.
type Method struct {
	Name    string            // method name.
	Type    reflect.Type      // method type, without the receiver.
	Pointer bool              // reports whether it has a pointer receiver.
	Func    string            // name of the function that implements the method.
	Fn      *runtime.Function // compiled function, with the receiver as first parameter.
}

type methodSet struct {
	methods map[string]*Method
}

// AddMethod adds the method m to the defined type t. It returns false if t is
// not a type defined in Scriggo.
func (types *Types) AddMethod(t reflect.Type, m *Method) bool {
	dt, ok := t.(definedType)
	if !ok {
		return false
	}
	if dt.methods.methods == nil {
		dt.methods.methods = map[string]*Method{}
	}
	dt.methods.methods[m.Name] = m
	return true
}

// Method returns the method with the given name declared in Scriggo for the
// defined type t or, if t is a pointer, for its element type. The returned
// method can have both a value and a pointer receiver. It returns nil if there
// is no such method.
func (types *Types) Method(t reflect.Type, name string) *Method {
	return scriggoMethod(t, name)
}

func scriggoMethod(t reflect.Type, name string) *Method {
	if p, ok := t.(ptrType); ok {
		t = p.elem
	}
	if dt, ok := t.(definedType); ok {
		return dt.methods.methods[name]
	}
	return nil
}

// DefinedOf returns the defined type with the given name and underlying type.
// For example, if n is "Int" and k represents int, DefinedOf(n, k) represents
// the type Int declared with 'type Int int'.
func (types *Types) DefinedOf(name string, underlyingType reflect.Type) reflect.Type {
	if name == "" {
		panic(internalError("name cannot be empty"))
	}
	return definedType{Type: underlyingType, name: name, sign: new(byte), methods: &methodSet{}}
}

func (x definedType) Name() string {
	return x.name
}

func (x definedType) AssignableTo(y reflect.Type) bool {
	return AssignableTo(x, y)
}

func (x definedType) ConvertibleTo(y reflect.Type) bool {
	return ConvertibleTo(x, y)
}

func (x definedType) Implements(y reflect.Type) bool {
	return Implements(x, y)
}

func (x definedType) MethodByName(string) (reflect.Method, bool) {
	// TODO.
	return reflect.Method{}, false
}

func (x definedType) String() string {
	// For defined types the string representation is exactly the name of the
	// type; the internal structure of the type is hidden.
	// TODO: verify that this is correct.
	return x.name
}

// GoType implements the interface runtime.ScriggoType.
func (x definedType) GoType() reflect.Type {
	if st, ok := x.Type.(runtime.ScriggoType); ok {
		return st.GoType()
	}
	assertNotScriggoType(x.Type)
	return x.Type
}

// Unwrap implements the interface runtime.ScriggoType.
func (x definedType) Unwrap(v reflect.Value) (reflect.Value, bool) { return unwrap(x, v) }

// Wrap implements the interface runtime.ScriggoType.
func (x definedType) Wrap(v reflect.Value) reflect.Value { return wrap(x, v) }
