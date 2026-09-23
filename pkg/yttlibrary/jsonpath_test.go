// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

//revive:disable:add-constant,line-length-limit,cognitive-complexity,cyclomatic,use-any

package yttlibrary_test

import (
	"reflect"
	"testing"

	"carvel.dev/ytt/pkg/template/core"
	"carvel.dev/ytt/pkg/yttlibrary"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
)

func jsonPathMember(t *testing.T, name string) starlark.Value {
	t.Helper()
	modVal, ok := yttlibrary.JSONPathAPI["jsonpath"]
	if !ok {
		t.Fatal("JSONPathAPI missing jsonpath")
	}
	mod, ok := modVal.(*starlarkstruct.Module)
	if !ok {
		t.Fatalf("jsonpath module type %T", modVal)
	}
	fn, ok := mod.Members[name]
	if !ok {
		t.Fatalf("missing member %s", name)
	}
	return fn
}

func callJSONPath(t *testing.T, name string, doc starlark.Value, path string) starlark.Value {
	t.Helper()
	got, err := starlark.Call(
		&starlark.Thread{Name: "test"},
		jsonPathMember(t, name),
		starlark.Tuple{doc, starlark.String(path)},
		nil,
	)
	if err != nil {
		t.Fatalf("%s(%q) error: %v", name, path, err)
	}
	return got
}

func TestJSONPathModuleShape(t *testing.T) {
	if jsonPathMember(t, "query") == nil || jsonPathMember(t, "query_one") == nil {
		t.Fatal("expected query and query_one")
	}
	api := yttlibrary.NewAPI(nil, yttlibrary.DataModule{}, nil, nil)
	mod, err := api.FindModule("jsonpath")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mod["jsonpath"]; !ok {
		t.Fatal("FindModule(jsonpath) missing symbol")
	}
}

func TestJSONPathStarlarkDictAndList(t *testing.T) {
	inner := &starlark.Dict{}
	if err := inner.SetKey(starlark.String("n"), starlark.MakeInt(2)); err != nil {
		t.Fatal(err)
	}
	first := &starlark.Dict{}
	if err := first.SetKey(starlark.String("n"), starlark.MakeInt(1)); err != nil {
		t.Fatal(err)
	}
	items := starlark.NewList([]starlark.Value{first, inner})
	doc := &starlark.Dict{}
	if err := doc.SetKey(starlark.String("my-key"), starlark.String("v")); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetKey(starlark.String("items"), items); err != nil {
		t.Fatal(err)
	}

	got := callJSONPath(t, "query", doc, "$.my-key")
	list, ok := got.(*starlark.List)
	if !ok {
		t.Fatalf("query type %T", got)
	}
	if list.Len() != 1 || list.Index(0) != starlark.String("v") {
		t.Fatalf("query = %v", list)
	}

	one := callJSONPath(t, "query_one", doc, "$.items[?(@.n > 1)].n")
	if one != starlark.MakeInt(2) {
		t.Fatalf("query_one = %v (%T)", one, one)
	}

	missing := callJSONPath(t, "query_one", doc, "$.nope")
	if missing != starlark.None {
		t.Fatalf("missing query_one = %v", missing)
	}

	empty := callJSONPath(t, "query", doc, "$.nope")
	emptyList, ok := empty.(*starlark.List)
	if !ok || emptyList.Len() != 0 {
		t.Fatalf("empty query = %#v", empty)
	}

	length := callJSONPath(t, "query_one", items, "$.length()")
	n, ok := length.(starlark.Int)
	if !ok {
		t.Fatalf("length type %T", length)
	}
	if v, ok := n.Int64(); !ok || v != 2 {
		t.Fatalf("length = %v", n)
	}

	arr := starlark.NewList([]starlark.Value{
		starlark.String("a"),
		starlark.String("b"),
		starlark.String("c"),
	})
	last := callJSONPath(t, "query_one", arr, "$[(@.length - 1)]")
	if last != starlark.String("c") {
		t.Fatalf("script = %v", last)
	}
}

func TestJSONPathStarlarkSyntaxError(t *testing.T) {
	_, err := starlark.Call(
		&starlark.Thread{Name: "test"},
		jsonPathMember(t, "query"),
		starlark.Tuple{starlark.NewList(nil), starlark.String("foo")},
		nil,
	)
	if err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestJSONPathRoundTrip(t *testing.T) {
	doc := &starlark.Dict{}
	child := &starlark.Dict{}
	if err := child.SetKey(starlark.String("b"), starlark.MakeInt(1)); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetKey(starlark.String("a"), child); err != nil {
		t.Fatal(err)
	}
	got := callJSONPath(t, "query_one", doc, "$.a")
	gotGo, err := core.NewStarlarkValue(got).AsGoValue()
	if err != nil {
		t.Fatal(err)
	}
	wantGo, err := core.NewStarlarkValue(child).AsGoValue()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotGo, wantGo) {
		t.Fatalf("round trip = %#v, want %#v", gotGo, wantGo)
	}
}
