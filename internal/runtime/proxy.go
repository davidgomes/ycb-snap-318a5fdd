// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// A value with a Scriggo type is stored in an interface as a proxy that
// implements MethodProxy. As the Go type of such proxy has no methods, it
// cannot be stored in a value with a non-empty interface type, such as an
// element of a []error slice, and gc compiled code cannot call its methods.
//
// For this reason, when a proxy is stored into a value with an interface
// type, it is wrapped into an interface proxy whose Go type implements that
// interface, and it is unwrapped when it is read back. The interface proxies
// for the error, fmt.Stringer and sort.Interface interfaces call the methods
// declared in Scriggo; the interface proxies for the other interfaces embed
// the interface, so gc compiled code cannot call their methods.

var (
	errorType         = reflect.TypeOf((*error)(nil)).Elem()
	stringerType      = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
	sortInterfaceType = reflect.TypeOf((*sort.Interface)(nil)).Elem()
	scriggoValueType  = reflect.TypeOf((*scriggoValue)(nil))
)

// scriggoValue is a value with a Scriggo type wrapped by an interface proxy.
type scriggoValue struct {
	value reflect.Value // proxy that implements MethodProxy.
	env   *env
}

// call calls the method with the given name and arguments.
func (sv *scriggoValue) call(name string, args ...reflect.Value) []reflect.Value {
	fn, rcvr, ok := sv.value.Interface().(MethodProxy).ScriggoMethod(name)
	if !ok {
		panic("scriggo: internal error: missing method " + name)
	}
	if !rcvr.IsValid() {
		panic(errNilPointer)
	}
	c := &callable{fn: fn, vars: []reflect.Value{rcvr}}
	return c.Value(sv.env).Call(args)
}

// errorProxy is the interface proxy of a value that implements error.
type errorProxy struct{ P *scriggoValue }

func (p errorProxy) Error() string { return p.P.call("Error")[0].String() }

// stringerProxy is the interface proxy of a value that implements
// fmt.Stringer.
type stringerProxy struct{ P *scriggoValue }

func (p stringerProxy) String() string { return p.P.call("String")[0].String() }

// sortProxy is the interface proxy of a value that implements sort.Interface.
type sortProxy struct{ P *scriggoValue }

func (p sortProxy) Len() int { return int(p.P.call("Len")[0].Int()) }

func (p sortProxy) Less(i, j int) bool {
	return p.P.call("Less", reflect.ValueOf(i), reflect.ValueOf(j))[0].Bool()
}

func (p sortProxy) Swap(i, j int) { p.P.call("Swap", reflect.ValueOf(i), reflect.ValueOf(j)) }

// interfaceProxyTypes maps an interface type to the type of its interface
// proxies. It contains the interface proxy types for the error, fmt.Stringer
// and sort.Interface interfaces and the types created by interfaceProxyType.
var interfaceProxyTypes sync.Map

// isInterfaceProxyType maps the interface proxy types to true.
var isInterfaceProxyType sync.Map

func init() {
	for iface, proxy := range map[reflect.Type]reflect.Type{
		errorType:         reflect.TypeOf(errorProxy{}),
		stringerType:      reflect.TypeOf(stringerProxy{}),
		sortInterfaceType: reflect.TypeOf(sortProxy{}),
	} {
		interfaceProxyTypes.Store(iface, proxy)
		isInterfaceProxyType.Store(proxy, true)
	}
}

// interfaceProxyType returns the type of the interface proxies for the
// non-empty interface type iface.
func interfaceProxyType(iface reflect.Type) reflect.Type {
	if t, ok := interfaceProxyTypes.Load(iface); ok {
		return t.(reflect.Type)
	}
	t := reflect.StructOf([]reflect.StructField{
		{Name: "Interface", Type: iface, Anonymous: true},
		{Name: "P", Type: scriggoValueType},
	})
	if t2, loaded := interfaceProxyTypes.LoadOrStore(iface, t); loaded {
		return t2.(reflect.Type)
	}
	isInterfaceProxyType.Store(t, true)
	return t
}

// exportValue returns the value to store into a value with the interface
// type t in place of v. If v is a proxy of a value with a Scriggo type, it
// returns its interface proxy for t, otherwise it returns v.
//
// If t is the empty interface, v is wrapped only if it implements error or
// fmt.Stringer, so that gc compiled code can call these methods.
func (vm *VM) exportValue(v reflect.Value, t reflect.Type) reflect.Value {
	if v.Kind() != reflect.Struct || !v.CanInterface() {
		return v
	}
	if _, ok := v.Interface().(MethodProxy); !ok {
		return v
	}
	iface := t
	if t.NumMethod() == 0 {
		st := vm.env.typeof(v)
		switch {
		case st.Implements(errorType):
			iface = errorType
		case st.Implements(stringerType):
			iface = stringerType
		default:
			return v
		}
	}
	p := reflect.New(interfaceProxyType(iface)).Elem()
	p.Field(p.NumField() - 1).Set(reflect.ValueOf(&scriggoValue{value: v, env: vm.env}))
	return p
}

// importValue returns the value to read in place of v, where v has been read
// from a value with an interface type. If v is an interface proxy, it returns
// the proxy it wraps, otherwise it returns v.
func importValue(v reflect.Value) reflect.Value {
	if v.Kind() != reflect.Struct {
		return v
	}
	if _, ok := isInterfaceProxyType.Load(v.Type()); !ok {
		return v
	}
	return v.Field(v.NumField() - 1).Interface().(*scriggoValue).value
}
