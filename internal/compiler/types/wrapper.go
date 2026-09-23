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

// unwrap unwraps v if it is a proxy, created by this package or by the
// runtime, for a value of type x.
func unwrap(x runtime.ScriggoType, v reflect.Value) (reflect.Value, bool) {
	p, ok := v.Interface().(runtime.Proxy)
	// Not a proxy.
	if !ok {
		return reflect.Value{}, false
	}
	// v is a proxy but it has a different Scriggo type.
	if p.ProxiedType() != x {
		return reflect.Value{}, false
	}
	return p.ProxiedValue(), true
}

// emptyInterfaceProxy is a proxy for values of types that have an empty
// method set.
type emptyInterfaceProxy struct {
	value reflect.Value
	sign  runtime.ScriggoType
}

// ProxiedValue implements the runtime.Proxy interface.
func (p emptyInterfaceProxy) ProxiedValue() reflect.Value { return p.value }

// ProxiedType implements the runtime.Proxy interface.
func (p emptyInterfaceProxy) ProxiedType() runtime.ScriggoType { return p.sign }
