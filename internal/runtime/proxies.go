// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"reflect"
)

// A Proxy is a Go value that holds a value with a Scriggo type, used when
// such value is stored in an interface.
type Proxy interface {
	// ScriggoValue returns the value held by the proxy and its Scriggo type.
	ScriggoValue() (reflect.Value, ScriggoType)
}

var (
	stringMethodType = reflect.TypeOf((func() string)(nil))
	lenMethodType    = reflect.TypeOf((func() int)(nil))
	lessMethodType   = reflect.TypeOf((func(int, int) bool)(nil))
	swapMethodType   = reflect.TypeOf((func(int, int))(nil))
)

// Groups of methods, declared in Scriggo code, that are implemented in Go by
// the method proxies.
const (
	errorMethods    = 1 << iota // Error() string
	stringerMethods             // String() string
	sortMethods                 // Len() int, Less(i, j int) bool and Swap(i, j int)
)

// wrap wraps the value v, with the Scriggo type t, in a proxy.
//
// If the method set of t has the methods of the interfaces error,
// fmt.Stringer or sort.Interface, the proxy implements these interfaces
// calling the methods declared in Scriggo code. This allows such value to be
// stored in values of these interfaces and native code to call its methods.
func (vm *VM) wrap(t ScriggoType, v reflect.Value) reflect.Value {
	ms, ok := t.(ScriggoMethodSet)
	if !ok {
		return t.Wrap(v)
	}
	var methods int
	if hasMethod(ms, "Error", stringMethodType) {
		methods |= errorMethods
	}
	if hasMethod(ms, "String", stringMethodType) {
		methods |= stringerMethods
	}
	if hasMethod(ms, "Len", lenMethodType) && hasMethod(ms, "Less", lessMethodType) && hasMethod(ms, "Swap", swapMethodType) {
		methods |= sortMethods
	}
	p := methodProxy{value: v, sign: t, env: vm.env}
	switch methods {
	case errorMethods:
		return reflect.ValueOf(errorProxy{p})
	case stringerMethods:
		return reflect.ValueOf(stringerProxy{p})
	case errorMethods | stringerMethods:
		return reflect.ValueOf(errorStringerProxy{p})
	case sortMethods:
		return reflect.ValueOf(sortProxy{p})
	case sortMethods | errorMethods:
		return reflect.ValueOf(sortErrorProxy{p})
	case sortMethods | stringerMethods:
		return reflect.ValueOf(sortStringerProxy{p})
	case sortMethods | errorMethods | stringerMethods:
		return reflect.ValueOf(sortErrorStringerProxy{p})
	}
	return t.Wrap(v)
}

// hasMethod reports whether the method set ms has a method, declared in
// Scriggo code, with the given name and type.
func hasMethod(ms ScriggoMethodSet, name string, typ reflect.Type) bool {
	fn, _ := ms.BoundMethod(name)
	return fn != nil && fn.Type == typ
}

// boundMethod returns the function that implements the method, declared in
// Scriggo code, with the given name of the value v with Scriggo type t, and
// the receiver to bind to it as its only non-local variable. If t does not
// have such method, ok is false.
func boundMethod(t ScriggoType, v reflect.Value, name string) (fn *Function, rcvr reflect.Value, ok bool) {
	ms, hasMethods := t.(ScriggoMethodSet)
	if !hasMethods {
		return nil, reflect.Value{}, false
	}
	fn, deref := ms.BoundMethod(name)
	if fn == nil {
		return nil, reflect.Value{}, false
	}
	if deref {
		if v.IsNil() {
			panic(errNilPointer)
		}
		v = v.Elem()
	}
	rcvr = reflect.New(v.Type()).Elem()
	rcvr.Set(v)
	return fn, rcvr, true
}

// methodProxy is a proxy for a value whose Scriggo type has methods declared
// in Scriggo code. It is embedded in the proxies that implement Go
// interfaces.
type methodProxy struct {
	value reflect.Value
	sign  ScriggoType
	env   *env
}

// ScriggoValue implements the Proxy interface.
func (p methodProxy) ScriggoValue() (reflect.Value, ScriggoType) {
	return p.value, p.sign
}

// call calls the method with the given name and arguments.
func (p methodProxy) call(name string, args ...reflect.Value) []reflect.Value {
	fn, rcvr, _ := boundMethod(p.sign, p.value, name)
	c := &callable{fn: fn, vars: []reflect.Value{rcvr}}
	return c.Value(p.env).Call(args)
}

func (p methodProxy) callError() string { return p.call("Error")[0].String() }

func (p methodProxy) callString() string { return p.call("String")[0].String() }

func (p methodProxy) callLen() int { return int(p.call("Len")[0].Int()) }

func (p methodProxy) callLess(i, j int) bool {
	return p.call("Less", reflect.ValueOf(i), reflect.ValueOf(j))[0].Bool()
}

func (p methodProxy) callSwap(i, j int) { p.call("Swap", reflect.ValueOf(i), reflect.ValueOf(j)) }

type errorProxy struct{ methodProxy }

func (p errorProxy) Error() string { return p.callError() }

type stringerProxy struct{ methodProxy }

func (p stringerProxy) String() string { return p.callString() }

type errorStringerProxy struct{ methodProxy }

func (p errorStringerProxy) Error() string  { return p.callError() }
func (p errorStringerProxy) String() string { return p.callString() }

type sortProxy struct{ methodProxy }

func (p sortProxy) Len() int           { return p.callLen() }
func (p sortProxy) Less(i, j int) bool { return p.callLess(i, j) }
func (p sortProxy) Swap(i, j int)      { p.callSwap(i, j) }

type sortErrorProxy struct{ methodProxy }

func (p sortErrorProxy) Error() string      { return p.callError() }
func (p sortErrorProxy) Len() int           { return p.callLen() }
func (p sortErrorProxy) Less(i, j int) bool { return p.callLess(i, j) }
func (p sortErrorProxy) Swap(i, j int)      { p.callSwap(i, j) }

type sortStringerProxy struct{ methodProxy }

func (p sortStringerProxy) String() string     { return p.callString() }
func (p sortStringerProxy) Len() int           { return p.callLen() }
func (p sortStringerProxy) Less(i, j int) bool { return p.callLess(i, j) }
func (p sortStringerProxy) Swap(i, j int)      { p.callSwap(i, j) }

type sortErrorStringerProxy struct{ methodProxy }

func (p sortErrorStringerProxy) Error() string      { return p.callError() }
func (p sortErrorStringerProxy) String() string     { return p.callString() }
func (p sortErrorStringerProxy) Len() int           { return p.callLen() }
func (p sortErrorStringerProxy) Less(i, j int) bool { return p.callLess(i, j) }
func (p sortErrorStringerProxy) Swap(i, j int)      { p.callSwap(i, j) }
