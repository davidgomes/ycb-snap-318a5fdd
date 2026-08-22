// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"reflect"
)

func goFuncType(t reflect.Type) reflect.Type {
	if st, ok := t.(ScriggoType); ok {
		return st.GoType()
	}
	if t.Kind() != reflect.Func {
		return t
	}
	in := make([]reflect.Type, t.NumIn())
	for i := range in {
		in[i] = goTypeOf(t.In(i))
	}
	out := make([]reflect.Type, t.NumOut())
	for i := range out {
		out[i] = goTypeOf(t.Out(i))
	}
	return reflect.FuncOf(in, out, t.IsVariadic())
}

func goTypeOf(t reflect.Type) reflect.Type {
	if st, ok := t.(ScriggoType); ok {
		return st.GoType()
	}
	return t
}

func proxyImplements(sign reflect.Type, iface reflect.Type) bool {
	if iface.Kind() != reflect.Interface {
		return false
	}
	if iface.NumMethod() == 0 {
		return true
	}
	hasRecv := sign.Kind() != reflect.Interface
	for i := 0; i < iface.NumMethod(); i++ {
		um := iface.Method(i)
		m, ok := sign.MethodByName(um.Name)
		if !ok || !funcImplements(m.Type, um.Type, hasRecv) {
			return false
		}
	}
	return true
}

func funcImplements(methodType, ifaceMethodType reflect.Type, hasRecv bool) bool {
	off := 0
	if hasRecv {
		off = 1
	}
	if methodType.Kind() != reflect.Func || ifaceMethodType.Kind() != reflect.Func {
		return false
	}
	if methodType.NumIn()-off != ifaceMethodType.NumIn() {
		return false
	}
	if methodType.NumOut() != ifaceMethodType.NumOut() {
		return false
	}
	if methodType.IsVariadic() != ifaceMethodType.IsVariadic() {
		return false
	}
	for i := 0; i < ifaceMethodType.NumIn(); i++ {
		if goTypeOf(methodType.In(i+off)) != goTypeOf(ifaceMethodType.In(i)) {
			if methodType.In(i+off) != ifaceMethodType.In(i) {
				return false
			}
		}
	}
	for i := 0; i < ifaceMethodType.NumOut(); i++ {
		if goTypeOf(methodType.Out(i)) != goTypeOf(ifaceMethodType.Out(i)) {
			if methodType.Out(i) != ifaceMethodType.Out(i) {
				return false
			}
		}
	}
	return true
}

func bindScriggoMethod(fn *Function, p ValueProxy, env *env) reflect.Value {
	ft := fn.Type
	recvType := ft.In(0)
	recv := p.Value
	if recvType.Kind() == reflect.Ptr && recv.Kind() != reflect.Ptr {
		if recv.CanAddr() {
			recv = recv.Addr()
		} else {
			tmp := reflect.New(recv.Type())
			tmp.Elem().Set(recv)
			recv = tmp
		}
	} else if recvType.Kind() != reflect.Ptr && recv.Kind() == reflect.Ptr {
		if recv.IsNil() {
			panic(errNilPointer)
		}
		recv = recv.Elem()
	}
	goFt := goFuncType(ft)
	in := make([]reflect.Type, goFt.NumIn()-1)
	for i := range in {
		in[i] = goFt.In(i + 1)
	}
	out := make([]reflect.Type, goFt.NumOut())
	for i := range out {
		out[i] = goFt.Out(i)
	}
	boundType := reflect.FuncOf(in, out, goFt.IsVariadic())
	impl := (&callable{fn: fn}).Value(env)
	return reflect.MakeFunc(boundType, func(args []reflect.Value) []reflect.Value {
		all := make([]reflect.Value, 0, len(args)+1)
		all = append(all, recv)
		all = append(all, args...)
		if boundType.IsVariadic() {
			return impl.CallSlice(all)
		}
		return impl.Call(all)
	})
}
