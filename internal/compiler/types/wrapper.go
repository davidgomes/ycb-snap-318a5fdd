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

// unwrap unwraps v if it is a proxy, created by wrap or by the virtual
// machine, of a value with type x.
func unwrap(x runtime.ScriggoType, v reflect.Value) (reflect.Value, bool) {
	p, ok := v.Interface().(runtime.Proxy)
	// Not a proxy.
	if !ok {
		return reflect.Value{}, false
	}
	value, sign := p.ScriggoValue()
	// v is a proxy but it has a different Scriggo type.
	if sign != x {
		return reflect.Value{}, false
	}
	return value, true
}

// emptyInterfaceProxy is a proxy for values of types whose methods, if any,
// are not implemented in Go by the proxy.
type emptyInterfaceProxy struct {
	value reflect.Value
	sign  runtime.ScriggoType
}

// ScriggoValue implements the runtime.Proxy interface.
func (p emptyInterfaceProxy) ScriggoValue() (reflect.Value, runtime.ScriggoType) {
	return p.value, p.sign
}
