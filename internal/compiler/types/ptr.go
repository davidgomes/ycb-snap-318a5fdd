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

// methodSet returns the methods declared in Scriggo code on the element
// type, or nil if the element type has no such methods.
func (x ptrType) methodSet() *methodSet {
	if dt, ok := x.elem.(definedType); ok && len(dt.methods.methods) > 0 {
		return dt.methods
	}
	return nil
}

func (x ptrType) Method(i int) reflect.Method {
	ms := x.methodSet()
	if ms == nil {
		return x.Type.Method(i)
	}
	return ms.reflectMethod(ms.exported(true)[i], x, i)
}

func (x ptrType) MethodByName(name string) (reflect.Method, bool) {
	ms := x.methodSet()
	if ms == nil {
		return x.Type.MethodByName(name)
	}
	for i, m := range ms.exported(true) {
		if m.Name == name {
			return ms.reflectMethod(m, x, i), true
		}
	}
	if x.elem.Kind() == reflect.Struct {
		// Methods promoted from embedded fields with a Go type.
		return x.Type.MethodByName(name)
	}
	return reflect.Method{}, false
}

func (x ptrType) NumMethod() int {
	ms := x.methodSet()
	if ms == nil {
		return x.Type.NumMethod()
	}
	return len(ms.exported(true))
}

// BoundMethod implements the interface runtime.ScriggoMethodSet.
func (x ptrType) BoundMethod(name string) (*runtime.Function, []int, bool, bool) {
	return boundMethod(x, name)
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
