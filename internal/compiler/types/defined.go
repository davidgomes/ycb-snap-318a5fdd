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

	// methods holds the methods declared in Scriggo with this type as
	// receiver base type. It is shared by all the copies of the type.
	methods *[]*Method
}

// A Method represents a method declared in Scriggo on a defined type.
type Method struct {
	Name            string       // name.
	Type            reflect.Type // type of the method, without the receiver.
	PointerReceiver bool         // reports whether the receiver is a pointer.

	// Function is the function that implements the method when it is called
	// through a method value or an interface. It takes the method parameters
	// and reads the receiver from its first closure variable.
	Function *runtime.Function
}

// DefinedOf returns the defined type with the given name and underlying type.
// For example, if n is "Int" and k represents int, DefinedOf(n, k) represents
// the type Int declared with 'type Int int'.
func (types *Types) DefinedOf(name string, underlyingType reflect.Type) reflect.Type {
	if name == "" {
		panic(internalError("name cannot be empty"))
	}
	return definedType{Type: underlyingType, name: name, sign: new(byte), methods: &[]*Method{}}
}

// AddMethod adds the method m to the defined type t. It panics if t is not a
// type returned by DefinedOf or if t already has a method with the same name.
func AddMethod(t reflect.Type, m *Method) {
	dt, ok := t.(definedType)
	if !ok {
		panic(internalError("%s is not a defined Scriggo type", t))
	}
	methods := *dt.methods
	i := sort.Search(len(methods), func(i int) bool { return methods[i].Name >= m.Name })
	if i < len(methods) && methods[i].Name == m.Name {
		panic(internalError("method %s.%s already declared", t, m.Name))
	}
	methods = append(methods, nil)
	copy(methods[i+1:], methods[i:])
	methods[i] = m
	*dt.methods = methods
}

// LookupMethod returns the method with the given name declared in Scriggo
// that belongs to the method set of t. t can be a defined Scriggo type or a
// pointer to a defined Scriggo type. The method set of a defined type T
// contains the methods with receiver type T and the method set of *T contains
// the methods with receiver type T or *T.
//
// If the method exists but it has a pointer receiver and t is not a pointer
// type, LookupMethod returns the method and false.
func LookupMethod(t reflect.Type, name string) (*Method, bool) {
	dt, isPtr := methodsBaseType(t)
	if dt.methods == nil {
		return nil, false
	}
	methods := *dt.methods
	i := sort.Search(len(methods), func(i int) bool { return methods[i].Name >= name })
	if i == len(methods) || methods[i].Name != name {
		return nil, false
	}
	m := methods[i]
	return m, isPtr || !m.PointerReceiver
}

// HasMethods reports whether t, or its element type if t is a pointer type,
// is a defined Scriggo type with at least one method declared in Scriggo.
func HasMethods(t reflect.Type) bool {
	dt, _ := methodsBaseType(t)
	return dt.methods != nil && len(*dt.methods) > 0
}

// methodsBaseType returns the defined type whose methods are in the method set
// of t, and reports whether t is a pointer to such type. If there is no such
// type, it returns the zero definedType.
func methodsBaseType(t reflect.Type) (definedType, bool) {
	switch t := t.(type) {
	case definedType:
		if t.Type.Kind() != reflect.Ptr && t.Type.Kind() != reflect.Interface {
			return t, false
		}
	case ptrType:
		if dt, ok := t.elem.(definedType); ok && dt.Type.Kind() != reflect.Ptr && dt.Type.Kind() != reflect.Interface {
			return dt, true
		}
	}
	return definedType{}, false
}

// methodSet returns the methods declared in Scriggo in the method set of t.
func methodSet(t reflect.Type) []*Method {
	dt, isPtr := methodsBaseType(t)
	if dt.methods == nil {
		return nil
	}
	if isPtr {
		return *dt.methods
	}
	var ms []*Method
	for _, m := range *dt.methods {
		if !m.PointerReceiver {
			ms = append(ms, m)
		}
	}
	return ms
}

// reflectMethod returns the reflect.Method that represents the method m with
// receiver type recv and index i.
func reflectMethod(recv reflect.Type, m *Method, i int) reflect.Method {
	var types Types
	in := make([]reflect.Type, m.Type.NumIn()+1)
	in[0] = recv
	for j := 1; j < len(in); j++ {
		in[j] = m.Type.In(j - 1)
	}
	out := make([]reflect.Type, m.Type.NumOut())
	for j := range out {
		out[j] = m.Type.Out(j)
	}
	rm := reflect.Method{
		Name:  m.Name,
		Type:  types.FuncOf(in, out, m.Type.IsVariadic()),
		Index: i,
	}
	if !isExported(m.Name) {
		rm.PkgPath = "main"
	}
	return rm
}

// exportedMethodSet returns the exported methods in the method set of t.
func exportedMethodSet(t reflect.Type) []*Method {
	var ms []*Method
	for _, m := range methodSet(t) {
		if isExported(m.Name) {
			ms = append(ms, m)
		}
	}
	return ms
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

func (x definedType) Method(i int) reflect.Method {
	if x.Type.Kind() == reflect.Interface {
		return x.Type.Method(i)
	}
	return reflectMethod(x, exportedMethodSet(x)[i], i)
}

func (x definedType) MethodByName(name string) (reflect.Method, bool) {
	if x.Type.Kind() == reflect.Interface {
		return x.Type.MethodByName(name)
	}
	m, ok := LookupMethod(x, name)
	if !ok {
		return reflect.Method{}, false
	}
	i := 0
	for _, m2 := range exportedMethodSet(x) {
		if m2 == m {
			break
		}
		i++
	}
	return reflectMethod(x, m, i), true
}

func (x definedType) NumMethod() int {
	if x.Type.Kind() == reflect.Interface {
		return x.Type.NumMethod()
	}
	return len(exportedMethodSet(x))
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

// isExported reports whether name is an exported identifier.
func isExported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}
