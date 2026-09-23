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

func (x ptrType) MethodByName(name string) (reflect.Method, bool) {
	dt, ok := x.elem.(definedType)
	if !ok {
		return x.Type.MethodByName(name)
	}
	m := dt.methods.get(name)
	if m == nil {
		return reflect.Method{}, false
	}
	return reflect.Method{Name: name, Type: m.typ, Index: dt.methodIndex(name, true)}, true
}

func (x ptrType) NumMethod() int {
	dt, ok := x.elem.(definedType)
	if !ok {
		return x.Type.NumMethod()
	}
	if dt.methods == nil {
		return 0
	}
	return len(dt.methods.order)
}

func (x ptrType) Method(i int) reflect.Method {
	dt, ok := x.elem.(definedType)
	if !ok {
		return x.Type.Method(i)
	}
	m := dt.methods.order[i]
	return reflect.Method{Name: m.name, Type: m.typ, Index: i}
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
