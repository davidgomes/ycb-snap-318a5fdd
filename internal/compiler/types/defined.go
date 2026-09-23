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

	// methods is shared by every copy of this defined type. It holds the
	// declaring package path and every method declared on the type.
	methods *methodTable
}

// ScriggoMethod is a method declared on a Scriggo defined type.
type ScriggoMethod struct {
	Name     string
	Pointer  bool         // declared with a pointer receiver.
	Promote  bool         // value-receiver method seen through a pointer type.
	FuncType reflect.Type // function type including the declared receiver.
	BoundGo  reflect.Type // Go function type without the receiver.
	Fn       *runtime.Function
	Node     interface{} // declaring *ast.Func, when known.
}

type scriggoMethod struct {
	name    string
	ptr     bool
	typ     reflect.Type
	boundGo reflect.Type
	fn      *runtime.Function
	node    interface{}
}

type methodTable struct {
	pkg    string
	byName map[string]*scriggoMethod
	order  []*scriggoMethod // all methods, sorted by name.
}

func (t *methodTable) add(m *scriggoMethod) bool {
	if t.byName == nil {
		t.byName = map[string]*scriggoMethod{}
	}
	if _, ok := t.byName[m.name]; ok {
		return false
	}
	t.byName[m.name] = m
	t.order = append(t.order, m)
	// Keep order sorted by name so Method(i) is deterministic.
	for i := len(t.order) - 1; i > 0 && t.order[i].name < t.order[i-1].name; i-- {
		t.order[i], t.order[i-1] = t.order[i-1], t.order[i]
	}
	return true
}

func (t *methodTable) get(name string) *scriggoMethod {
	if t == nil {
		return nil
	}
	return t.byName[name]
}

func (m *scriggoMethod) export(promote bool) ScriggoMethod {
	return ScriggoMethod{
		Name:     m.name,
		Pointer:  m.ptr,
		Promote:  promote,
		FuncType: m.typ,
		BoundGo:  m.boundGo,
		Fn:       m.fn,
		Node:     m.node,
	}
}

// DefinedOf returns the defined type with the given name and underlying type.
// For example, if n is "Int" and k represents int, DefinedOf(n, k) represents
// the type Int declared with 'type Int int'.
func (types *Types) DefinedOf(name string, underlyingType reflect.Type) reflect.Type {
	if name == "" {
		panic(internalError("name cannot be empty"))
	}
	return definedType{Type: underlyingType, name: name, sign: new(byte), methods: &methodTable{}}
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

func (x definedType) MethodByName(name string) (reflect.Method, bool) {
	m := x.methods.get(name)
	if m == nil || m.ptr {
		return reflect.Method{}, false
	}
	return reflect.Method{Name: name, Type: m.typ, Index: x.methodIndex(name, false)}, true
}

func (x definedType) NumMethod() int {
	if x.methods == nil {
		return 0
	}
	n := 0
	for _, m := range x.methods.order {
		if !m.ptr {
			n++
		}
	}
	return n
}

func (x definedType) Method(i int) reflect.Method {
	n := 0
	for _, m := range x.methods.order {
		if m.ptr {
			continue
		}
		if n == i {
			return reflect.Method{Name: m.name, Type: m.typ, Index: n}
		}
		n++
	}
	panic("scriggo: method index out of range")
}

func (x definedType) methodIndex(name string, pointerSet bool) int {
	n := 0
	for _, m := range x.methods.order {
		if !pointerSet && m.ptr {
			continue
		}
		if m.name == name {
			return n
		}
		n++
	}
	return 0
}

// PackagePath returns the package path stored for this defined type.
func (x definedType) PackagePath() string {
	if x.methods == nil {
		return ""
	}
	return x.methods.pkg
}

// SetPackagePath records the package in which a defined type was declared.
func SetPackagePath(t reflect.Type, pkg string) {
	if dt, ok := t.(definedType); ok && dt.methods != nil {
		dt.methods.pkg = pkg
	}
}

// AddMethod adds a method to the defined type base. The receiver is either
// base or a pointer to base.
func AddMethod(base reflect.Type, name string, pointer bool, funcType, boundGo reflect.Type, node interface{}) bool {
	dt := base.(definedType)
	return dt.methods.add(&scriggoMethod{
		name:    name,
		ptr:     pointer,
		typ:     funcType,
		boundGo: boundGo,
		node:    node,
	})
}

// BindMethodFunc attaches the compiled function to a method declaration.
func BindMethodFunc(recv reflect.Type, name string, fn *runtime.Function) {
	if recv != nil && recv.Kind() == reflect.Ptr {
		recv = recv.Elem()
	}
	dt, ok := recv.(definedType)
	if !ok {
		return
	}
	if m := dt.methods.get(name); m != nil {
		m.fn = fn
	}
}

// LookupMethod reports the method name in the method set of t when t is a
// Scriggo defined type or a pointer to one.
func LookupMethod(t reflect.Type, name string) (ScriggoMethod, bool) {
	switch t := t.(type) {
	case definedType:
		m := t.methods.get(name)
		if m == nil || m.ptr {
			return ScriggoMethod{}, false
		}
		sm := m.export(false)
		sm.Fn = m.fn
		return sm, true
	case ptrType:
		dt, ok := t.elem.(definedType)
		if !ok {
			return ScriggoMethod{}, false
		}
		m := dt.methods.get(name)
		if m == nil {
			return ScriggoMethod{}, false
		}
		sm := m.export(!m.ptr)
		sm.Fn = m.fn
		return sm, true
	default:
		return ScriggoMethod{}, false
	}
}

// RuntimeMethod returns the compiled method bound to a wrapped Scriggo value
// of type sign. deref reports whether the stored receiver must be dereferenced
// before the call (value-receiver method invoked on a pointer).
func RuntimeMethod(sign reflect.Type, name string) (fn *runtime.Function, bound reflect.Type, deref bool, ok bool) {
	switch s := sign.(type) {
	case definedType:
		m := s.methods.get(name)
		if m == nil || m.ptr || m.fn == nil {
			return nil, nil, false, false
		}
		return m.fn, m.boundGo, false, true
	case ptrType:
		dt, isDef := s.elem.(definedType)
		if !isDef {
			return nil, nil, false, false
		}
		m := dt.methods.get(name)
		if m == nil || m.fn == nil {
			return nil, nil, false, false
		}
		return m.fn, m.boundGo, !m.ptr, true
	default:
		return nil, nil, false, false
	}
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
