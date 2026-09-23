// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"reflect"
)

var stringType = reflect.TypeOf("")

// methodProxy is a proxy for a value of a Scriggo type with methods.
//
// Go code can only call the methods of a proxy that are implemented by its
// Go type, so there is a proxy type for each supported combination of the
// methods String() string and Error() string. The other methods can be
// called only by Scriggo code.
type methodProxy struct {
	value reflect.Value
	typ   ScriggoType
	env   *env
}

// ProxiedValue implements the Proxy interface.
func (p methodProxy) ProxiedValue() reflect.Value { return p.value }

// ProxiedType implements the Proxy interface.
func (p methodProxy) ProxiedType() ScriggoType { return p.typ }

func (p methodProxy) callString(name string) string {
	fn, _ := boundMethod(p.env, p.typ, p.value, name)
	return fn.Call(nil)[0].String()
}

type stringerProxy struct{ methodProxy }

func (p stringerProxy) String() string { return p.callString("String") }

type errorProxy struct{ methodProxy }

func (p errorProxy) Error() string { return p.callString("Error") }

type errorStringerProxy struct{ methodProxy }

func (p errorStringerProxy) Error() string  { return p.callString("Error") }
func (p errorStringerProxy) String() string { return p.callString("String") }

// wrap wraps the value v, with Scriggo type t, in a proxy.
func (vm *VM) wrap(t ScriggoType, v reflect.Value) reflect.Value {
	m, ok := t.(Methoder)
	if !ok {
		return t.Wrap(v)
	}
	isStringer := hasStringMethod(m, "String")
	isError := hasStringMethod(m, "Error")
	p := methodProxy{value: v, typ: t, env: vm.env}
	switch {
	case isStringer && isError:
		return reflect.ValueOf(errorStringerProxy{p})
	case isStringer:
		return reflect.ValueOf(stringerProxy{p})
	case isError:
		return reflect.ValueOf(errorProxy{p})
	}
	return t.Wrap(v)
}

// hasStringMethod reports whether m has a method with the given name and
// type func() string.
func hasStringMethod(m Methoder, name string) bool {
	fn, _, ok := m.ScriggoMethod(name)
	if !ok || fn == nil {
		return false
	}
	t := fn.Type
	return t.NumIn() == 1 && t.NumOut() == 1 && t.Out(0) == stringType
}

// boundMethod returns a function that calls the Scriggo method with the given
// name of the type t, with receiver recv. It returns false if t has not a
// method with such name.
func boundMethod(env *env, t ScriggoType, recv reflect.Value, name string) (reflect.Value, bool) {
	m, ok := t.(Methoder)
	if !ok {
		return reflect.Value{}, false
	}
	fn, deref, ok := m.ScriggoMethod(name)
	if !ok {
		return reflect.Value{}, false
	}
	if deref {
		if recv.IsNil() {
			panic(errNilPointer)
		}
		v := reflect.New(recv.Type().Elem()).Elem()
		v.Set(recv.Elem())
		recv = v
	}
	f := (&callable{fn: fn, vars: env.globals}).Value(env)
	ft := f.Type()
	in := make([]reflect.Type, ft.NumIn()-1)
	for i := range in {
		in[i] = ft.In(i + 1)
	}
	out := make([]reflect.Type, ft.NumOut())
	for i := range out {
		out[i] = ft.Out(i)
	}
	variadic := ft.IsVariadic()
	return reflect.MakeFunc(reflect.FuncOf(in, out, variadic), func(args []reflect.Value) []reflect.Value {
		args = append([]reflect.Value{recv}, args...)
		if variadic {
			return f.CallSlice(args)
		}
		return f.Call(args)
	}), true
}
