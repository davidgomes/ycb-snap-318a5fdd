// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"

	"github.com/open2b/scriggo/internal/runtime"
)

// PointerTo behaves like reflect.PointerTo except when it is a Scriggo type; in
// such case a new Scriggo pointer type is created and returned as reflect.Type.
func (types *Types) PointerTo(t reflect.Type) reflect.Type {
	if st, ok := t.(runtime.ScriggoType); ok {
		return ptrType{
			Type: reflect.PointerTo(st.GoType()),
			elem: st,
		}
	}
	return reflect.PointerTo(t)
}

// ptrType represents a composite pointer type where the element is a Scriggo
// type.
type ptrType struct {
	reflect.Type
	elem reflect.Type // Cannot be nil.
}

func (x ptrType) AssignableTo(y reflect.Type) bool {
	return AssignableTo(x, y)
}

func (x ptrType) ConvertibleTo(y reflect.Type) bool {
	return ConvertibleTo(x, y)
}

func (x ptrType) Elem() reflect.Type {
	return x.elem
}

func (x ptrType) Implements(y reflect.Type) bool {
	return Implements(x, y)
}

// Method returns the i-th exported method in the method set of x.
func (x ptrType) Method(i int) reflect.Method {
	elem, ok := x.elem.(definedType)
	if !ok {
		return x.Type.Method(i)
	}
	methods := elem.methods.exported(true)
	if i < 0 || i >= len(methods) {
		panic("reflect: Method index out of range")
	}
	return toReflectMethod(methods[i], i)
}

// MethodByName returns the exported method with the given name in the method
// set of x.
func (x ptrType) MethodByName(name string) (reflect.Method, bool) {
	elem, ok := x.elem.(definedType)
	if !ok {
		return x.Type.MethodByName(name)
	}
	for i, m := range elem.methods.exported(true) {
		if m.Name == name {
			return toReflectMethod(m, i), true
		}
	}
	// Methods promoted from the embedded fields of a struct.
	if elem.Kind() == reflect.Struct {
		if _, ok := elem.methods.lookup(name); !ok {
			return x.Type.MethodByName(name)
		}
	}
	return reflect.Method{}, false
}

// NumMethod returns the number of exported methods in the method set of x.
func (x ptrType) NumMethod() int {
	elem, ok := x.elem.(definedType)
	if !ok {
		return x.Type.NumMethod()
	}
	return len(elem.methods.exported(true))
}

// ScriggoMethod implements the interface runtime.Methoder.
func (x ptrType) ScriggoMethod(name string) (*runtime.Function, bool, bool) {
	elem, ok := x.elem.(definedType)
	if !ok {
		return nil, false, false
	}
	return scriggoMethod(elem, name, true)
}

func (x ptrType) Name() string {
	return "" // composite types do not have a name.
}

func (x ptrType) String() string {
	return "*" + x.elem.String()
}

// GoType implements the interface runtime.ScriggoType.
func (x ptrType) GoType() reflect.Type {
	assertNotScriggoType(x.Type)
	return x.Type
}

// Unwrap implements the interface runtime.ScriggoType.
func (x ptrType) Unwrap(v reflect.Value) (reflect.Value, bool) { return unwrap(x, v) }

// Wrap implements the interface runtime.ScriggoType.
func (x ptrType) Wrap(v reflect.Value) reflect.Value { return wrap(x, v) }
