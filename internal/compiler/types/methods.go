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

// method is a method declared on a Scriggo defined type.
type method struct {
	name    string
	typ     reflect.Type // includes the declared receiver.
	ptrTyp  reflect.Type // includes a pointer receiver; same as typ if declared on *T.
	ptrRecv bool
	impl    *runtime.Function
	wrap    *runtime.Function // (*T).ValueMethod wrapper; nil until emission.
}

// methodSet is the set of methods of a defined type. It is shared by all
// copies of that definedType value.
type methodSet struct {
	list []method
}

func (ms *methodSet) byName(name string) (method, bool) {
	if ms == nil {
		return method{}, false
	}
	for _, m := range ms.list {
		if m.name == name {
			return m, true
		}
	}
	return method{}, false
}

func (ms *methodSet) valueMethods(exportedOnly bool) []method {
	if ms == nil {
		return nil
	}
	var out []method
	for _, m := range ms.list {
		if m.ptrRecv {
			continue
		}
		if exportedOnly && !exportedName(m.name) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (ms *methodSet) allMethods(exportedOnly bool) []method {
	if ms == nil {
		return nil
	}
	var out []method
	for _, m := range ms.list {
		if exportedOnly && !exportedName(m.name) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func exportedName(name string) bool {
	if name == "" {
		return false
	}
	if c := name[0]; c < utf8.RuneSelf {
		return 'A' <= c && c <= 'Z'
	}
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.Is(unicode.Lu, r)
}

func toReflectMethod(m method, typ reflect.Type, index int) reflect.Method {
	pkgPath := ""
	if !exportedName(m.name) {
		pkgPath = "main"
	}
	return reflect.Method{
		Name:    m.name,
		PkgPath: pkgPath,
		Type:    typ,
		Index:   index,
	}
}

// AddMethod adds a method named name to the defined type base. recv is the
// receiver type (T or *T) and fnType is the method type without the receiver.
// It reports whether the method was added and an error message otherwise.
func (types *Types) AddMethod(base reflect.Type, name string, recv reflect.Type, fnType reflect.Type) string {
	dt, ok := base.(definedType)
	if !ok {
		return "invalid receiver type " + base.String()
	}
	if _, exists := dt.methods.byName(name); exists {
		return "method " + dt.name + "." + name + " already declared"
	}

	in := make([]reflect.Type, 0, fnType.NumIn()+1)
	in = append(in, recv)
	for i := 0; i < fnType.NumIn(); i++ {
		in = append(in, fnType.In(i))
	}
	out := make([]reflect.Type, fnType.NumOut())
	for i := range out {
		out[i] = fnType.Out(i)
	}
	full := types.FuncOf(in, out, fnType.IsVariadic())

	ptrRecv := isPointerType(recv)
	ptrFull := full
	if !ptrRecv {
		ptrIn := make([]reflect.Type, len(in))
		copy(ptrIn, in)
		ptrIn[0] = types.PointerTo(base)
		ptrFull = types.FuncOf(ptrIn, out, fnType.IsVariadic())
	}

	dt.methods.list = append(dt.methods.list, method{
		name:    name,
		typ:     full,
		ptrTyp:  ptrFull,
		ptrRecv: ptrRecv,
	})
	sort.Slice(dt.methods.list, func(i, j int) bool {
		return dt.methods.list[i].name < dt.methods.list[j].name
	})
	return ""
}

// SetMethodImpl stores the compiled implementation of a method.
func SetMethodImpl(base reflect.Type, name string, fn *runtime.Function) {
	dt, ok := base.(definedType)
	if !ok {
		return
	}
	for i, m := range dt.methods.list {
		if m.name == name {
			dt.methods.list[i].impl = fn
			return
		}
	}
}

// SetMethodWrapper stores the (*T).ValueMethod wrapper for a value-receiver method.
func SetMethodWrapper(base reflect.Type, name string, fn *runtime.Function) {
	dt, ok := base.(definedType)
	if !ok {
		return
	}
	for i, m := range dt.methods.list {
		if m.name == name {
			dt.methods.list[i].wrap = fn
			return
		}
	}
}

// LookupMethod returns the compiled function for the method name on t,
// which may be a defined type T or a pointer type *T.
func LookupMethod(t reflect.Type, name string) *runtime.Function {
	if dt, ok := t.(definedType); ok {
		m, found := dt.methods.byName(name)
		if !found || m.ptrRecv {
			return nil
		}
		return m.impl
	}
	if pt, ok := t.(ptrType); ok {
		dt, ok := pt.elem.(definedType)
		if !ok {
			return nil
		}
		m, found := dt.methods.byName(name)
		if !found {
			return nil
		}
		if m.ptrRecv {
			return m.impl
		}
		if m.wrap != nil {
			return m.wrap
		}
		return m.impl
	}
	return nil
}

// MethodMustDeref reports whether calling the named method on t requires
// dereferencing a pointer receiver value (value method accessed via *T).
func MethodMustDeref(t reflect.Type, name string) bool {
	pt, ok := t.(ptrType)
	if !ok {
		return false
	}
	dt, ok := pt.elem.(definedType)
	if !ok {
		return false
	}
	m, found := dt.methods.byName(name)
	return found && !m.ptrRecv && m.wrap == nil
}

// DefinedBase reports whether t is a Scriggo defined type and returns it.
func DefinedBase(t reflect.Type) (reflect.Type, bool) {
	dt, ok := t.(definedType)
	return dt, ok
}

// ReceiverBaseType reports the defined base type of a method receiver type.
// recv must denote T or *T, where T is a defined type that is not a pointer
// or interface type.
func ReceiverBaseType(recv reflect.Type) (base reflect.Type, ptr bool, err string) {
	t := recv
	if isPointerType(recv) {
		ptr = true
		t = recv.Elem()
	}
	if isPointerType(t) {
		return nil, false, "invalid receiver type " + recv.String() + " (" + recv.String() + " is a pointer type)"
	}
	dt, ok := t.(definedType)
	if !ok {
		if t.Name() == "" {
			return nil, false, "invalid receiver type " + recv.String() + " (" + recv.String() + " is not a defined type)"
		}
		return nil, false, "cannot define new methods on non-local type " + t.String()
	}
	switch dt.Type.Kind() {
	case reflect.Ptr:
		return nil, false, "invalid receiver type " + dt.name + " (" + dt.name + " is a pointer type)"
	case reflect.Interface:
		return nil, false, "invalid receiver type " + dt.name + " (" + dt.name + " is an interface type)"
	}
	return dt, ptr, ""
}

func isPointerType(t reflect.Type) bool {
	if _, ok := t.(ptrType); ok {
		return true
	}
	return t.Kind() == reflect.Ptr && t.Name() == ""
}

func methodsImplement(x, y reflect.Type) bool {
	if y.Kind() != reflect.Interface {
		return false
	}
	n := y.NumMethod()
	for i := 0; i < n; i++ {
		ym := y.Method(i)
		xm, ok := x.MethodByName(ym.Name)
		if !ok {
			return false
		}
		if !methodSatisfies(xm.Type, ym.Type) {
			return false
		}
	}
	return true
}

// methodSatisfies reports whether a method type with a receiver (mt) satisfies
// an interface method type without a receiver (it).
func methodSatisfies(mt, it reflect.Type) bool {
	if mt.NumIn()-1 != it.NumIn() || mt.NumOut() != it.NumOut() || mt.IsVariadic() != it.IsVariadic() {
		return false
	}
	for i := 0; i < it.NumIn(); i++ {
		if !identical(mt.In(i+1), it.In(i), false, false) {
			return false
		}
	}
	for i := 0; i < it.NumOut(); i++ {
		if !identical(mt.Out(i), it.Out(i), false, false) {
			return false
		}
	}
	return true
}
