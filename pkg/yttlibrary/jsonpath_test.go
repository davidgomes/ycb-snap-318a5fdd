// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package yttlibrary_test

import (
	"testing"

	"carvel.dev/ytt/pkg/yttlibrary"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
)

func TestJSONPathStarlark(t *testing.T) {
	mod := yttlibrary.JSONPathAPI["jsonpath"].(*starlarkstruct.Module)
	query := mod.Members["query"].(*starlark.Builtin)
	queryOne := mod.Members["query_one"].(*starlark.Builtin)
	thread := &starlark.Thread{}

	book := starlark.NewDict(2)
	book.SetKey(starlark.String("author"), starlark.String("Nigel"))
	book.SetKey(starlark.String("price"), starlark.MakeInt(8))
	books := starlark.NewList([]starlark.Value{book})

	got, err := query.CallInternal(thread, starlark.Tuple{books, starlark.String("$[0].author")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*starlark.List)
	if list.Len() != 1 || list.Index(0) != starlark.String("Nigel") {
		t.Fatalf("%s", got.String())
	}

	got, err = query.CallInternal(thread, starlark.Tuple{books, starlark.String("$[?(@.price < 10)]")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.(*starlark.List).Len() != 1 {
		t.Fatalf("%s", got.String())
	}

	got, err = queryOne.CallInternal(thread, starlark.Tuple{books, starlark.String("$[0].missing")}, nil)
	if err != nil || got != starlark.None {
		t.Fatalf("%v %v", got, err)
	}

	got, err = query.CallInternal(thread, starlark.Tuple{books, starlark.String("$.length()")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.(*starlark.List).Index(0) != starlark.MakeInt(1) {
		t.Fatalf("%s", got.String())
	}

	empty, err := query.CallInternal(thread, starlark.Tuple{books, starlark.String("$.nope")}, nil)
	if err != nil || empty.(*starlark.List).Len() != 0 {
		t.Fatalf("%v %v", empty, err)
	}
}
