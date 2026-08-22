// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"fmt"
	"reflect"

	"github.com/open2b/scriggo/internal/runtime"
)

// wrap and unwrap are called by the methods Wrap and Unwrap of the types
// defined in this package. These two methods and the GoType method
// implement the runtime.ScriggoType interface.

func wrap(t runtime.ScriggoType, v reflect.Value) reflect.Value {
	p := emptyInterfaceProxy{
		value: v,
		sign:  t,
	}
	hasString := hasStringer(t)
	hasError := hasErrorMethod(t)
	switch {
	case hasString && hasError:
		return reflect.ValueOf(stringerErrorProxy{p})
	case hasString:
		return reflect.ValueOf(stringerProxy{p})
	case hasError:
		return reflect.ValueOf(errorProxy{p})
	default:
		return reflect.ValueOf(p)
	}
}

func unwrap(x runtime.ScriggoType, v reflect.Value) (reflect.Value, bool) {
	p, ok := asProxy(v)
	if !ok {
		return reflect.Value{}, false
	}
	if p.sign != x {
		return reflect.Value{}, false
	}
	return p.value, true
}

// emptyInterfaceProxy is a proxy for values of Scriggo types.
type emptyInterfaceProxy struct {
	value reflect.Value
	sign  runtime.ScriggoType
}

func (p emptyInterfaceProxy) ScriggoReflectType() reflect.Type { return p.sign }
func (p emptyInterfaceProxy) Underlying() reflect.Value        { return p.value }
func (p emptyInterfaceProxy) MethodFunc(name string) *runtime.Function {
	return LookupMethod(p.sign, name)
}

type stringerProxy struct{ emptyInterfaceProxy }

func (p stringerProxy) String() string { return callStringMethod(p.emptyInterfaceProxy, "String") }

type errorProxy struct{ emptyInterfaceProxy }

func (p errorProxy) Error() string { return callStringMethod(p.emptyInterfaceProxy, "Error") }

type stringerErrorProxy struct{ emptyInterfaceProxy }

func (p stringerErrorProxy) String() string {
	return callStringMethod(p.emptyInterfaceProxy, "String")
}
func (p stringerErrorProxy) Error() string { return callStringMethod(p.emptyInterfaceProxy, "Error") }

func asProxy(v reflect.Value) (emptyInterfaceProxy, bool) {
	if !v.IsValid() {
		return emptyInterfaceProxy{}, false
	}
	switch p := v.Interface().(type) {
	case emptyInterfaceProxy:
		return p, true
	case stringerProxy:
		return p.emptyInterfaceProxy, true
	case errorProxy:
		return p.emptyInterfaceProxy, true
	case stringerErrorProxy:
		return p.emptyInterfaceProxy, true
	default:
		return emptyInterfaceProxy{}, false
	}
}

func hasStringer(t reflect.Type) bool {
	m, ok := t.MethodByName("String")
	if !ok {
		return false
	}
	return m.Type.NumIn() == 1 && m.Type.NumOut() == 1 && m.Type.Out(0).Kind() == reflect.String
}

func hasErrorMethod(t reflect.Type) bool {
	m, ok := t.MethodByName("Error")
	if !ok {
		return false
	}
	return m.Type.NumIn() == 1 && m.Type.NumOut() == 1 && m.Type.Out(0).Kind() == reflect.String
}

func callStringMethod(p emptyInterfaceProxy, name string) string {
	fn := LookupMethod(p.sign, name)
	if fn == nil {
		if p.value.IsValid() && p.value.CanInterface() {
			return fmt.Sprint(p.value.Interface())
		}
		return ""
	}
	recv := p.value
	if MethodMustDeref(p.sign, name) && recv.Kind() == reflect.Ptr {
		recv = recv.Elem()
	}
	outs := runtime.CallFunction(fn, []reflect.Value{recv})
	if len(outs) == 1 && outs[0].Kind() == reflect.String {
		return outs[0].String()
	}
	return ""
}
