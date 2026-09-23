// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"reflect"
)

var (
	boolType   = reflect.TypeOf(false)
	intType    = reflect.TypeOf(0)
	stringType = reflect.TypeOf("")
)

// methodProxy is a proxy for a value of a Scriggo type with methods.
//
// Go code can only call the methods of a proxy that are implemented by its
// Go type, so there is a proxy type for each supported combination of the
// methods of the fmt.Stringer, error and sort.Interface interfaces. The other
// methods can be called only by Scriggo code.
type methodProxy struct {
	value reflect.Value
	typ   ScriggoType
	env   *env
}

// ProxiedValue implements the Proxy interface.
func (p methodProxy) ProxiedValue() reflect.Value { return p.value }

// ProxiedType implements the Proxy interface.
func (p methodProxy) ProxiedType() ScriggoType { return p.typ }

func (p methodProxy) call(name string, args ...reflect.Value) []reflect.Value {
	fn, _ := boundMethod(p.env, p.typ, p.value, name)
	return fn.Call(args)
}

type stringMethod struct{ p methodProxy }

func (m stringMethod) String() string { return m.p.call("String")[0].String() }

type errorMethod struct{ p methodProxy }

func (m errorMethod) Error() string { return m.p.call("Error")[0].String() }

type sortMethods struct{ p methodProxy }

func (m sortMethods) Len() int { return int(m.p.call("Len")[0].Int()) }
func (m sortMethods) Less(i, j int) bool {
	return m.p.call("Less", reflect.ValueOf(i), reflect.ValueOf(j))[0].Bool()
}
func (m sortMethods) Swap(i, j int) { m.p.call("Swap", reflect.ValueOf(i), reflect.ValueOf(j)) }

type stringerProxy struct {
	methodProxy
	stringMethod
}

type errorProxy struct {
	methodProxy
	errorMethod
}

type errorStringerProxy struct {
	methodProxy
	stringMethod
	errorMethod
}

type sortProxy struct {
	methodProxy
	sortMethods
}

type sortStringerProxy struct {
	methodProxy
	sortMethods
	stringMethod
}

type sortErrorProxy struct {
	methodProxy
	sortMethods
	errorMethod
}

type sortErrorStringerProxy struct {
	methodProxy
	sortMethods
	stringMethod
	errorMethod
}

// wrap wraps the value v, with Scriggo type t, in a proxy.
func (vm *VM) wrap(t ScriggoType, v reflect.Value) reflect.Value {
	m, ok := t.(Methoder)
	if !ok {
		return t.Wrap(v)
	}
	p := methodProxy{value: v, typ: t, env: vm.env}
	s := stringMethod{p}
	e := errorMethod{p}
	o := sortMethods{p}
	isStringer := hasMethod(m, "String", nil, []reflect.Type{stringType})
	isError := hasMethod(m, "Error", nil, []reflect.Type{stringType})
	isSort := hasMethod(m, "Len", nil, []reflect.Type{intType}) &&
		hasMethod(m, "Less", []reflect.Type{intType, intType}, []reflect.Type{boolType}) &&
		hasMethod(m, "Swap", []reflect.Type{intType, intType}, nil)
	var proxy interface{}
	switch {
	case isSort && isStringer && isError:
		proxy = sortErrorStringerProxy{p, o, s, e}
	case isSort && isStringer:
		proxy = sortStringerProxy{p, o, s}
	case isSort && isError:
		proxy = sortErrorProxy{p, o, e}
	case isSort:
		proxy = sortProxy{p, o}
	case isStringer && isError:
		proxy = errorStringerProxy{p, s, e}
	case isStringer:
		proxy = stringerProxy{p, s}
	case isError:
		proxy = errorProxy{p, e}
	default:
		return t.Wrap(v)
	}
	return reflect.ValueOf(proxy)
}

// hasMethod reports whether m has a method with the given name, input
// parameters (receiver excluded) and results.
func hasMethod(m Methoder, name string, in, out []reflect.Type) bool {
	fn, _, ok := m.ScriggoMethod(name)
	if !ok || fn == nil {
		return false
	}
	t := fn.Type
	if t.IsVariadic() || t.NumIn() != len(in)+1 || t.NumOut() != len(out) {
		return false
	}
	for i, typ := range in {
		if t.In(i+1) != typ {
			return false
		}
	}
	for i, typ := range out {
		if t.Out(i) != typ {
			return false
		}
	}
	return true
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
