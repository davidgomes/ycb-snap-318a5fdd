// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"

	"github.com/open2b/scriggo/internal/runtime"
)

// wrap and unwrap are called by the methods Wrap and Unwrap of the types
// defined in this package. These two methods and the GoType method
// implement the runtime.ScriggoType interface.

func wrap(t runtime.ScriggoType, v reflect.Value) reflect.Value {
	return reflect.ValueOf(emptyInterfaceProxy{
		value: v,
		sign:  t,
	})
}

func unwrap(x runtime.ScriggoType, v reflect.Value) (reflect.Value, bool) {
	p, ok := v.Interface().(emptyInterfaceProxy)
	// Not a proxy.
	if !ok {
		return reflect.Value{}, false
	}
	// v is a proxy but it has a different Scriggo type.
	if p.sign != x {
		return reflect.Value{}, false
	}
	return p.value, true
}

// emptyInterfaceProxy is a proxy for a Scriggo value stored in an interface.
// methods, when non-nil, is the method set used to dispatch calls that reach
// the value through an interface.
type emptyInterfaceProxy struct {
	value   reflect.Value
	sign    runtime.ScriggoType
	methods map[string]runtime.ScriggoMethod
}

// LookupScriggoMethod implements runtime.ScriggoMethodSet.
func (p emptyInterfaceProxy) LookupScriggoMethod(name string) (runtime.ScriggoMethod, reflect.Value, bool) {
	if p.methods == nil {
		return runtime.ScriggoMethod{}, reflect.Value{}, false
	}
	m, ok := p.methods[name]
	if !ok || m.Fn == nil {
		return runtime.ScriggoMethod{}, reflect.Value{}, false
	}
	return m, p.value, true
}

// ImplementsInterface implements runtime.ScriggoMethodSet.
func (p emptyInterfaceProxy) ImplementsInterface(iface reflect.Type) bool {
	if iface == nil || iface.Kind() != reflect.Interface {
		return false
	}
	for i := 0; i < iface.NumMethod(); i++ {
		im := iface.Method(i)
		if im.PkgPath != "" {
			return false
		}
		sm, ok := p.methods[im.Name]
		if !ok || !signaturesMatch(sm.Type, im.Type) {
			return false
		}
	}
	return true
}
