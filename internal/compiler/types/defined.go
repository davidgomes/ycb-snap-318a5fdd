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

	// methods holds the methods declared on the type. It is shared by all
	// the copies of the definedType value.
	methods *methodSet
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

// Method returns the i-th exported method, with a value receiver, in the
// method set of x.
func (x definedType) Method(i int) reflect.Method {
	if x.Type.Kind() == reflect.Interface {
		return x.Type.Method(i)
	}
	methods := x.methods.exported(false)
	if i < 0 || i >= len(methods) {
		panic("reflect: Method index out of range")
	}
	return toReflectMethod(methods[i], i)
}

// MethodByName returns the exported method, with a value receiver, with the
// given name in the method set of x.
func (x definedType) MethodByName(name string) (reflect.Method, bool) {
	if x.Type.Kind() == reflect.Interface {
		return x.Type.MethodByName(name)
	}
	for i, m := range x.methods.exported(false) {
		if m.Name == name {
			return toReflectMethod(m, i), true
		}
	}
	return reflect.Method{}, false
}

// NumMethod returns the number of exported methods, with a value receiver,
// in the method set of x.
func (x definedType) NumMethod() int {
	if x.Type.Kind() == reflect.Interface {
		return x.Type.NumMethod()
	}
	return len(x.methods.exported(false))
}

// ScriggoMethod implements the interface runtime.Methoder.
func (x definedType) ScriggoMethod(name string) (*runtime.Function, bool, bool) {
	return scriggoMethod(x, name, false)
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
