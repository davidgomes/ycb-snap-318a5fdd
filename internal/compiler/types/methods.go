// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"
	"sort"
	"strings"
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

// PromotedMethod returns the method with the given name declared in Scriggo
// code, selected on the type t through its embedded fields, and the indexes
// of the embedded fields that select the receiver. It returns nil if the
// selector does not denote such method, or it denotes a method declared on
// t, or on its element type if t is a pointer. ambiguous reports whether the
// selector is ambiguous because there is a method declared in Scriggo code
// and another method or field with the same name at the same depth.
//
// Note that the returned method does not necessarily belong to the method
// set of t.
func PromotedMethod(t reflect.Type, name string) (m *Method, path []int, ambiguous bool) {
	s, ok, ambiguous := selectMethod(t, name)
	if !ok || len(s.path) == 0 {
		return nil, nil, ambiguous
	}
	return s.method, s.path, false
}

// selection is the selection of a method declared in Scriggo code.
type selection struct {
	method *Method
	path   []int // indexes of the embedded fields that select the receiver.
	ptr    bool  // reports whether the receiver is selected through a pointer.
	isPtr  bool  // reports whether the selected receiver is a pointer.
}

// selectMethod selects, following the Go rules for selectors, the method with
// the given name declared in Scriggo code on t or promoted through its
// embedded fields. It returns false if there is no such method, if the
// selector is ambiguous or if it denotes a field or a method not declared in
// Scriggo code. ambiguous reports whether the selector is ambiguous and at
// least one of the selected methods is declared in Scriggo code.
func selectMethod(t reflect.Type, name string) (s selection, ok, ambiguous bool) {
	isPtr := t.Kind() == reflect.Ptr && t.Name() == ""
	if isPtr {
		t = t.Elem()
	}
	type candidate struct {
		typ   reflect.Type
		path  []int
		ptr   bool
		isPtr bool
	}
	candidates := []candidate{{typ: t, ptr: isPtr, isPtr: isPtr}}
	for depth := 0; len(candidates) > 0; depth++ {
		var found []selection
		var others int
		var next []candidate
		for _, c := range candidates {
			if m := MethodOf(c.typ, name); m != nil {
				found = append(found, selection{method: m, path: c.path, ptr: c.ptr, isPtr: c.isPtr})
				continue
			}
			if depth > 0 && hasGoMethod(c.typ, name) {
				others++
				continue
			}
			if c.typ.Kind() != reflect.Struct {
				continue
			}
			for i := 0; i < c.typ.NumField(); i++ {
				f := c.typ.Field(i)
				if fieldName(f) == name {
					others++
				}
				if !f.Anonymous {
					continue
				}
				typ, ptr, isPtr := f.Type, c.ptr, false
				if typ.Kind() == reflect.Ptr && typ.Name() == "" {
					typ, ptr, isPtr = typ.Elem(), true, true
				}
				path := make([]int, len(c.path)+1)
				copy(path, c.path)
				path[len(c.path)] = i
				next = append(next, candidate{typ: typ, path: path, ptr: ptr, isPtr: isPtr})
			}
		}
		if len(found) > 0 || others > 0 {
			if len(found) == 1 && others == 0 {
				return found[0], true, false
			}
			return selection{}, false, len(found) > 0
		}
		candidates = next
	}
	return selection{}, false, false
}

// methodSetLookup returns the selection of the method, declared in Scriggo
// code, with the given name in the method set of t.
func methodSetLookup(t reflect.Type, name string) (selection, bool) {
	s, ok, _ := selectMethod(t, name)
	if !ok || s.method.Pointer && !s.ptr {
		return selection{}, false
	}
	return s, true
}

// hasGoMethod reports whether the method set of the Go type t, or of the
// pointer to t, has a method with the given name.
func hasGoMethod(t reflect.Type, name string) bool {
	if _, ok := t.(runtime.ScriggoType); ok {
		return false
	}
	if _, ok := t.MethodByName(name); ok {
		return true
	}
	_, ok := reflect.PointerTo(t).MethodByName(name)
	return ok
}

// fieldName returns the name of the struct field f as declared in the
// source code.
//
// Keep in sync with compiler.decodeFieldName.
func fieldName(f reflect.StructField) string {
	name := f.Name
	if !strings.HasPrefix(name, "𝗽") {
		return name
	}
	for i := len("𝗽"); i < len(name); i++ {
		if c := name[i]; c < '0' || c > '9' {
			return name[i:]
		}
	}
	return "_"
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
		s, ok := methodSetLookup(x, ym.Name)
		if !ok || !identical(s.method.Type, ym.Type, false, false) {
			return false
		}
	}
	return true
}

// boundMethod implements the BoundMethod method of the Scriggo types that
// implement the runtime.ScriggoMethodSet interface.
func boundMethod(t reflect.Type, name string) (fn *runtime.Function, path []int, deref, addr bool) {
	s, ok := methodSetLookup(t, name)
	if !ok {
		return nil, nil, false, false
	}
	return s.method.Bound, s.path, s.isPtr && !s.method.Pointer, !s.isPtr && s.method.Pointer
}

// isExported reports whether name is exported.
func isExported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}
