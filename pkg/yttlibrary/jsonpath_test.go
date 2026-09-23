// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package yttlibrary_test

import (
	"testing"

	"carvel.dev/ytt/pkg/yttlibrary"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
)

func jsonpathBuiltin(t *testing.T, name string) starlark.Value {
	t.Helper()
	mod, ok := yttlibrary.JSONPathAPI["jsonpath"].(*starlarkstruct.Module)
	if !ok {
		t.Fatalf("JSONPathAPI[jsonpath] = %T", yttlibrary.JSONPathAPI["jsonpath"])
	}
	fn := mod.Members[name]
	if fn == nil {
		t.Fatalf("missing %s", name)
	}
	return fn
}

func callJSONPath(t *testing.T, name string, args ...starlark.Value) (starlark.Value, error) {
	t.Helper()
	return starlark.Call(&starlark.Thread{Name: "test"}, jsonpathBuiltin(t, name), args, nil)
}

func TestJSONPathStarlarkQuery(t *testing.T) {
	book := &starlark.Dict{}
	if err := book.SetKey(starlark.String("title"), starlark.String("cheap")); err != nil {
		t.Fatal(err)
	}
	if err := book.SetKey(starlark.String("price"), starlark.MakeInt(5)); err != nil {
		t.Fatal(err)
	}
	other := &starlark.Dict{}
	if err := other.SetKey(starlark.String("title"), starlark.String("dear")); err != nil {
		t.Fatal(err)
	}
	if err := other.SetKey(starlark.String("price"), starlark.MakeInt(15)); err != nil {
		t.Fatal(err)
	}
	books := starlark.NewList([]starlark.Value{book, other})
	doc := &starlark.Dict{}
	if err := doc.SetKey(starlark.String("books"), books); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetKey(starlark.String("my-key"), starlark.String("hyphen")); err != nil {
		t.Fatal(err)
	}

	res, err := callJSONPath(t, "query", doc, starlark.String("$.books[?(@.price < 10)].title"))
	if err != nil {
		t.Fatal(err)
	}
	list, ok := res.(*starlark.List)
	if !ok {
		t.Fatalf("query result %T", res)
	}
	if list.Len() != 1 || list.Index(0) != starlark.String("cheap") {
		t.Fatalf("titles = %s", list.String())
	}

	res, err = callJSONPath(t, "query", doc, starlark.String("$.missing"))
	if err != nil {
		t.Fatal(err)
	}
	list, ok = res.(*starlark.List)
	if !ok || list.Len() != 0 {
		t.Fatalf("empty = %#v", res)
	}

	res, err = callJSONPath(t, "query_one", doc, starlark.String("$.my-key"))
	if err != nil || res != starlark.String("hyphen") {
		t.Fatalf("query_one hyphen = %v, %v", res, err)
	}
	res, err = callJSONPath(t, "query_one", doc, starlark.String("$.nope"))
	if err != nil || res != starlark.None {
		t.Fatalf("query_one miss = %v, %v", res, err)
	}

	res, err = callJSONPath(t, "query", books, starlark.String("$[-1].title"))
	if err != nil {
		t.Fatal(err)
	}
	list = res.(*starlark.List)
	if list.Len() != 1 || list.Index(0) != starlark.String("dear") {
		t.Fatalf("last title = %s", list.String())
	}

	res, err = callJSONPath(t, "query", books, starlark.String("$.length()"))
	if err != nil {
		t.Fatal(err)
	}
	list = res.(*starlark.List)
	n, ok := list.Index(0).(starlark.Int)
	if !ok {
		t.Fatalf("length type %T", list.Index(0))
	}
	if i, ok := n.Int64(); !ok || i != 2 {
		t.Fatalf("length = %s", n.String())
	}

	_, err = callJSONPath(t, "query", doc, starlark.String("not-jsonpath"))
	if err == nil {
		t.Fatal("expected syntax error")
	}

	if err := doc.SetKey(starlark.String("n"), starlark.None); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetKey(starlark.String("zero"), starlark.MakeInt(0)); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetKey(starlark.String("flag"), starlark.Bool(false)); err != nil {
		t.Fatal(err)
	}

	res, err = callJSONPath(t, "query", doc, starlark.String("$.n"))
	if err != nil {
		t.Fatal(err)
	}
	list = res.(*starlark.List)
	if list.Len() != 1 || list.Index(0) != starlark.None {
		t.Fatalf("explicit none = %s", list.String())
	}
	res, err = callJSONPath(t, "query_one", doc, starlark.String("$.zero"))
	if err != nil || res != starlark.MakeInt(0) {
		t.Fatalf("zero = %v, %v", res, err)
	}
	res, err = callJSONPath(t, "query_one", doc, starlark.String("$.flag"))
	if err != nil || res != starlark.Bool(false) {
		t.Fatalf("false = %v, %v", res, err)
	}

	res, err = callJSONPath(t, "query_one", doc, starlark.String("$.books[0]"))
	if err != nil {
		t.Fatal(err)
	}
	gotBook, ok := res.(*starlark.Dict)
	if !ok {
		t.Fatalf("book type %T", res)
	}
	title, found, err := gotBook.Get(starlark.String("title"))
	if err != nil || !found || title != starlark.String("cheap") {
		t.Fatalf("book title = %v, %v, %v", title, found, err)
	}
}
