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

// emptyInterfaceProxy is a proxy for values of types that have an empty
// method set.
type emptyInterfaceProxy struct {
	value reflect.Value
	sign  runtime.ScriggoType
}

// ScriggoMethod implements the runtime.ScriggoMethodProxy interface.
func (p emptyInterfaceProxy) ScriggoMethod(name string) (*runtime.Function, reflect.Value, bool) {
	m := scriggoMethod(p.sign, name)
	if m == nil || m.Fn == nil {
		return nil, reflect.Value{}, false
	}
	_, isPtr := p.sign.(ptrType)
	if m.Pointer && !isPtr {
		return nil, reflect.Value{}, false
	}
	rcv := p.value
	if isPtr && !m.Pointer {
		if rcv.IsNil() {
			return m.Fn, reflect.Value{}, true
		}
		rcv = rcv.Elem()
	}
	return m.Fn, rcv, true
}

// ScriggoImplements implements the runtime.ScriggoMethodProxy interface.
func (p emptyInterfaceProxy) ScriggoImplements(t reflect.Type) bool {
	return Implements(p.sign, t)
}
