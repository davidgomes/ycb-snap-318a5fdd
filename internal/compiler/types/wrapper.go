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

// TODO: currently unwrap always returns an empty interface wrapper. This will
// change when methods declaration will be implemented in Scriggo.
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

// emptyInterfaceProxy is a proxy for values with a Scriggo type. Its method
// set, for the gc compiled code, is empty; the methods declared in Scriggo
// are available to the VM through the ScriggoMethod method.
type emptyInterfaceProxy struct {
	value reflect.Value
	sign  runtime.ScriggoType
}

// ScriggoMethod implements the runtime.MethodProxy interface.
func (p emptyInterfaceProxy) ScriggoMethod(name string) (*runtime.Function, reflect.Value, bool) {
	m, ok := LookupMethod(p.sign, name)
	if !ok || m.Function == nil {
		return nil, reflect.Value{}, false
	}
	rcvr := p.value
	if _, isPtr := methodsBaseType(p.sign); isPtr && !m.PointerReceiver {
		if rcvr.IsNil() {
			return m.Function, reflect.Value{}, true
		}
		rcvr = rcvr.Elem()
	}
	return m.Function, rcvr, true
}
