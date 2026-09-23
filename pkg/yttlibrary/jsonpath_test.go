// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package yttlibrary_test

import (
	"testing"

	"carvel.dev/ytt/pkg/yttlibrary"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
)

func TestJSONPathModule(t *testing.T) {
	mod, ok := yttlibrary.JSONPathAPI["jsonpath"].(*starlarkstruct.Module)
	if !ok || mod.Name != "jsonpath" {
		t.Fatalf("JSONPathAPI = %#v", yttlibrary.JSONPathAPI)
	}
	query := mod.Members["query"]
	queryOne := mod.Members["query_one"]

	doc := starlark.NewDict(2)
	if err := doc.SetKey(starlark.String("my-key"), starlark.String("v")); err != nil {
		t.Fatal(err)
	}
	items := starlark.NewList([]starlark.Value{
		starlark.MakeInt(1),
		starlark.MakeInt(2),
		starlark.MakeInt(3),
	})
	if err := doc.SetKey(starlark.String("items"), items); err != nil {
		t.Fatal(err)
	}

	thread := &starlark.Thread{}
	res, err := starlark.Call(thread, query, starlark.Tuple{doc, starlark.String("$.my-key")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, ok := res.(*starlark.List)
	if !ok || list.Len() != 1 || list.Index(0) != starlark.String("v") {
		t.Fatalf("query = %s", res.String())
	}

	res, err = starlark.Call(thread, query, starlark.Tuple{doc, starlark.String("$.missing")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, ok = res.(*starlark.List)
	if !ok || list.Len() != 0 {
		t.Fatalf("empty query = %s", res.String())
	}

	res, err = starlark.Call(thread, queryOne, starlark.Tuple{items, starlark.String("$[(@.length-1)]")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.String() != "3" {
		t.Fatalf("query_one = %s", res.String())
	}

	res, err = starlark.Call(thread, queryOne, starlark.Tuple{doc, starlark.String("$.nope")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res != starlark.None {
		t.Fatalf("missing query_one = %s", res.String())
	}

	_, err = starlark.Call(thread, query, starlark.Tuple{doc, starlark.String("nope")}, nil)
	if err == nil {
		t.Fatal("expected syntax error")
	}
}
