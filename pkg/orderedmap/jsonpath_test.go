// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap_test

import (
	"testing"

	"carvel.dev/ytt/pkg/orderedmap"
)

func doc() interface{} {
	book := func(cat, author string, price float64, isbn interface{}) *orderedmap.Map {
		m := orderedmap.NewMap()
		m.Set("category", cat)
		m.Set("author", author)
		m.Set("title", author+"-title")
		m.Set("price", price)
		if isbn != nil {
			m.Set("isbn", isbn)
		}
		return m
	}
	store := orderedmap.NewMap()
	store.Set("book", []interface{}{
		book("reference", "Nigel Rees", 8.95, nil),
		book("fiction", "Evelyn Waugh", 12.99, nil),
		book("fiction", "Herman Melville", 8.99, "0-553-21311-3"),
		book("fiction", "J. R. R. Tolkien", 22.99, "0-395-19395-8"),
	})
	store.Set("bicycle", func() *orderedmap.Map {
		m := orderedmap.NewMap()
		m.Set("color", "red")
		m.Set("price", 19.95)
		return m
	}())
	root := orderedmap.NewMap()
	root.Set("store", store)
	root.Set("my-key", "hyphen")
	root.Set("expensive", 10)
	return root
}

func TestQueryDotAndIndex(t *testing.T) {
	d := doc()
	got, err := orderedmap.Query(d, "$.store.bicycle.color")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "red" {
		t.Fatalf("color: %#v", got)
	}
	got, err = orderedmap.Query(d, "$.my-key")
	if err != nil || len(got) != 1 || got[0] != "hyphen" {
		t.Fatalf("hyphen: %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.store.book[0].author")
	if err != nil || got[0] != "Nigel Rees" {
		t.Fatalf("author0: %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.store.book[-1].author")
	if err != nil || got[0] != "J. R. R. Tolkien" {
		t.Fatalf("author-1: %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.store.book[99]")
	if err != nil || len(got) != 0 {
		t.Fatalf("oor: %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.store.book.color")
	if err != nil || len(got) != 0 {
		t.Fatalf("key on array: %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.store[0]")
	if err != nil || len(got) != 0 {
		t.Fatalf("index on map: %#v %v", got, err)
	}
}

func TestQueryBracketUnion(t *testing.T) {
	d := doc()
	got, err := orderedmap.Query(d, `$['my-key']`)
	if err != nil || got[0] != "hyphen" {
		t.Fatalf("%#v %v", got, err)
	}
	got, err = orderedmap.Query(d, `$.store.book[0]["author"]`)
	if err != nil || got[0] != "Nigel Rees" {
		t.Fatalf("%#v %v", got, err)
	}
	got, err = orderedmap.Query(d, `$.store['bicycle','book']`)
	if err != nil || len(got) != 2 {
		t.Fatalf("union keys %#v %v", got, err)
	}
	if _, ok := got[0].(*orderedmap.Map); !ok {
		t.Fatalf("expected bicycle first, got %#v", got[0])
	}
	if _, ok := got[1].([]interface{}); !ok {
		t.Fatalf("expected book second, got %#v", got[1])
	}
	got, err = orderedmap.Query(d, `$.store.book[3,1].author`)
	if err != nil || len(got) != 2 || got[0] != "J. R. R. Tolkien" || got[1] != "Evelyn Waugh" {
		t.Fatalf("union idx %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, `$['quote\'d']`)
	if err != nil || len(got) != 0 {
		t.Fatalf("escape %#v %v", got, err)
	}
	m := orderedmap.NewMap()
	m.Set("a'b", 1)
	got, err = orderedmap.Query(m, `$['a\'b']`)
	if err != nil || len(got) != 1 || got[0] != 1 {
		t.Fatalf("escaped key %#v %v", got, err)
	}
}

func TestQueryRecursive(t *testing.T) {
	d := doc()
	got, err := orderedmap.Query(d, "$..author")
	if err != nil {
		t.Fatal(err)
	}
	want := []interface{}{"Nigel Rees", "Evelyn Waugh", "Herman Melville", "J. R. R. Tolkien"}
	if len(got) != len(want) {
		t.Fatalf("%#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%#v", got)
		}
	}
	got, err = orderedmap.Query(d, "$..*")
	if err != nil || len(got) == 0 {
		t.Fatal(got, err)
	}
	if got[0] != d {
		t.Fatalf("root first, got %#v", got[0])
	}
	got, err = orderedmap.Query(d, `$..['color','price']`)
	if err != nil {
		t.Fatal(err)
	}
	// bicycle color, bicycle price, then each book price (books have no color)
	if len(got) != 6 || got[4] != "red" || got[5] != 19.95 {
		t.Fatalf("%#v", got)
	}
}

func TestQueryFilter(t *testing.T) {
	d := doc()
	got, err := orderedmap.Query(d, "$.store.book[?(@.price < 10)].author")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "Nigel Rees" || got[1] != "Herman Melville" {
		t.Fatalf("%#v", got)
	}
	got, err = orderedmap.Query(d, "$.store.book[?(@.isbn)].author")
	if err != nil || len(got) != 2 || got[0] != "Herman Melville" {
		t.Fatalf("truthy %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, `$.store.book[?(@.category == "fiction" && @.price <= 9)].author`)
	if err != nil || len(got) != 1 || got[0] != "Herman Melville" {
		t.Fatalf("and %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, `$.store.book[?(@.price > 20 || @.category == "reference")].author`)
	if err != nil || len(got) != 2 || got[0] != "Nigel Rees" || got[1] != "J. R. R. Tolkien" {
		t.Fatalf("or %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, `$.store.book[?(@.author != "Evelyn Waugh")].price`)
	if err != nil || len(got) != 3 {
		t.Fatalf("ne %#v %v", got, err)
	}
	arr := []interface{}{
		map[string]interface{}{"a": map[string]interface{}{"b": []interface{}{float64(2)}}},
		map[string]interface{}{"a": map[string]interface{}{"b": []interface{}{float64(0)}}},
	}
	got, err = orderedmap.Query(arr, `$[?(@.a.b[0] >= 2)]`)
	if err != nil || len(got) != 1 {
		t.Fatalf("nested %#v %v", got, err)
	}
	got, err = orderedmap.Query([]interface{}{false, true, 0, 1, "", "x", []interface{}{}, []interface{}{1}, nil}, `$[?(@)]`)
	if err != nil || len(got) != 4 || got[0] != true || got[1] != 1 || got[2] != "x" {
		t.Fatalf("truth %#v %v", got, err)
	}
}

func TestQueryLengthAndScript(t *testing.T) {
	d := doc()
	got, err := orderedmap.Query(d, "$.store.book.length()")
	if err != nil || len(got) != 1 || got[0] != 4 {
		t.Fatalf("len %#v %T %v", got, got[0], err)
	}
	if _, ok := got[0].(int); !ok {
		t.Fatalf("want int, got %T", got[0])
	}
	got, err = orderedmap.Query(d, "$.store.bicycle.length()")
	if err != nil || got[0] != 2 {
		t.Fatalf("map len %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.my-key.length()")
	if err != nil || got[0] != len("hyphen") {
		t.Fatalf("str len %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.store.book[(@.length-1)].author")
	if err != nil || got[0] != "J. R. R. Tolkien" {
		t.Fatalf("script %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.store.book[( @.length - 2 )].author")
	if err != nil || got[0] != "Herman Melville" {
		t.Fatalf("script ws %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.store.book[?(@.author.length() > 15)].author")
	if err != nil || len(got) != 1 || got[0] != "J. R. R. Tolkien" {
		t.Fatalf("len filter %#v %v", got, err)
	}
	got, err = orderedmap.Query(d, "$.store.book[?(length(@.isbn) > 0)].author")
	if err != nil || len(got) != 2 {
		t.Fatalf("len() fn %#v %v", got, err)
	}
}

func TestQueryOneAndSyntax(t *testing.T) {
	d := doc()
	v, ok, err := orderedmap.QueryOne(d, "$.store.book[*].author")
	if err != nil || !ok || v != "Nigel Rees" {
		t.Fatalf("%#v %v %v", v, ok, err)
	}
	v, ok, err = orderedmap.QueryOne(d, "$.nope")
	if err != nil || ok || v != nil {
		t.Fatalf("miss %#v %v %v", v, ok, err)
	}
	_, err = orderedmap.Query(d, "store")
	syn, is := err.(*orderedmap.SyntaxError)
	if !is {
		t.Fatalf("%T %v", err, err)
	}
	if syn.Error() != "syntax error at position 0: path must start with '$'" {
		t.Fatalf("%q", syn.Error())
	}
	_, err = orderedmap.Query(d, "$.store.")
	if _, is = err.(*orderedmap.SyntaxError); !is {
		t.Fatalf("%v", err)
	}
	empty, err := orderedmap.Query(d, "$.missing")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty %#v %v", empty, err)
	}
}
