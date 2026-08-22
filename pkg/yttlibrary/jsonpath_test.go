// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package yttlibrary_test

import (
	"testing"

	"carvel.dev/ytt/pkg/yttlibrary"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
)

func jsonpathMember(t *testing.T, name string) starlark.Value {
	t.Helper()
	mod, ok := yttlibrary.JSONPathAPI["jsonpath"].(*starlarkstruct.Module)
	if !ok {
		t.Fatalf("JSONPathAPI[\"jsonpath\"] is %T", yttlibrary.JSONPathAPI["jsonpath"])
	}
	fn, ok := mod.Members[name]
	if !ok {
		t.Fatalf("missing member %s", name)
	}
	return fn
}

func callJSONPath(t *testing.T, name string, args starlark.Tuple) starlark.Value {
	t.Helper()
	thread := &starlark.Thread{Name: "test"}
	v, err := starlark.Call(thread, jsonpathMember(t, name), args, nil)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestJSONPathAPIQuery(t *testing.T) {
	doc := new(starlark.Dict)
	if err := doc.SetKey(starlark.String("a"), starlark.MakeInt(1)); err != nil {
		t.Fatal(err)
	}
	inner := starlark.NewList([]starlark.Value{starlark.String("x"), starlark.String("y")})
	if err := doc.SetKey(starlark.String("items"), inner); err != nil {
		t.Fatal(err)
	}

	got := callJSONPath(t, "query", starlark.Tuple{doc, starlark.String("$.items[1]")})
	list, ok := got.(*starlark.List)
	if !ok {
		t.Fatalf("query should return *starlark.List, got %T", got)
	}
	if list.Len() != 1 || list.Index(0) != starlark.String("y") {
		t.Fatalf("query results: %v", got)
	}

	empty := callJSONPath(t, "query", starlark.Tuple{doc, starlark.String("$.missing")})
	emptyList, ok := empty.(*starlark.List)
	if !ok || emptyList.Len() != 0 {
		t.Fatalf("expected empty list, got %v", empty)
	}

	one := callJSONPath(t, "query_one", starlark.Tuple{doc, starlark.String("$.a")})
	if i, ok := one.(starlark.Int); !ok {
		t.Fatalf("query_one type %T", one)
	} else if n, _ := i.Int64(); n != 1 {
		t.Fatalf("query_one = %v", one)
	}

	none := callJSONPath(t, "query_one", starlark.Tuple{doc, starlark.String("$.nope")})
	if none != starlark.None {
		t.Fatalf("expected None, got %v", none)
	}

	fromList := callJSONPath(t, "query", starlark.Tuple{inner, starlark.String("$[0]")})
	fromListRes := fromList.(*starlark.List)
	if fromListRes.Len() != 1 || fromListRes.Index(0) != starlark.String("x") {
		t.Fatalf("list doc query: %v", fromList)
	}
}

func TestJSONPathAPISyntaxError(t *testing.T) {
	thread := &starlark.Thread{Name: "test"}
	doc := new(starlark.Dict)
	_, err := starlark.Call(thread, jsonpathMember(t, "query"), starlark.Tuple{doc, starlark.String("nope")}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
