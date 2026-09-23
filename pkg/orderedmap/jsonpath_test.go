// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap_test

import (
	"errors"
	"reflect"
	"testing"

	"carvel.dev/ytt/pkg/orderedmap"
)

func om(pairs ...interface{}) *orderedmap.Map {
	m := orderedmap.NewMap()
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func TestQueryRootAndMissing(t *testing.T) {
	doc := om("a", 1)
	got, err := orderedmap.Query(doc, "$")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != doc {
		t.Fatalf("root = %#v", got)
	}

	got, err = orderedmap.Query(doc, "$.missing")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("missing = %#v, want empty slice", got)
	}

	v, ok, err := orderedmap.QueryOne(doc, "$.missing")
	if err != nil || ok || v != nil {
		t.Fatalf("QueryOne missing = %#v %v %v", v, ok, err)
	}
	v, ok, err = orderedmap.QueryOne(doc, "$.a")
	if err != nil || !ok || v != 1 {
		t.Fatalf("QueryOne = %#v %v %v", v, ok, err)
	}
}

func TestQueryDotBracketHyphenAndEscape(t *testing.T) {
	doc := om("my-key", "v", "a'b", 2, "a\"b", 3, "a\\b", 4)
	cases := []struct {
		path string
		want interface{}
	}{
		{"$.my-key", "v"},
		{"$['my-key']", "v"},
		{`$['a\'b']`, 2},
		{`$["a\"b"]`, 3},
		{`$['a\\b']`, 4},
		{`$.["my-key"]`, "v"},
	}
	for _, tc := range cases {
		v, ok, err := orderedmap.QueryOne(doc, tc.path)
		if err != nil || !ok || !reflect.DeepEqual(v, tc.want) {
			t.Errorf("%s => %#v %v %v, want %#v", tc.path, v, ok, err, tc.want)
		}
	}
}

func TestQueryIndexUnionAndIncompatible(t *testing.T) {
	arr := []interface{}{10, 20, 30}
	got, err := orderedmap.Query(arr, "$[2,0,-1]")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{30, 10, 30}) {
		t.Fatalf("union index = %#v", got)
	}
	got, err = orderedmap.Query(arr, "$[5]")
	if err != nil || len(got) != 0 {
		t.Fatalf("out of range = %#v %v", got, err)
	}
	got, err = orderedmap.Query(arr, "$[-4]")
	if err != nil || len(got) != 0 {
		t.Fatalf("negative out of range = %#v %v", got, err)
	}

	doc := om("b", 2, "a", 1)
	got, err = orderedmap.Query(doc, "$['a','b','missing']")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{1, 2}) {
		t.Fatalf("union keys = %#v", got)
	}

	// Index on a map and key on an array are empty, not errors.
	got, err = orderedmap.Query(doc, "$[0]")
	if err != nil || len(got) != 0 {
		t.Fatalf("index on map = %#v %v", got, err)
	}
	got, err = orderedmap.Query(arr, "$.foo")
	if err != nil || len(got) != 0 {
		t.Fatalf("key on array = %#v %v", got, err)
	}
}

func TestQueryWildcardAndLength(t *testing.T) {
	inner := om("n", 1)
	doc := om("b", inner, "a", "xy")
	got, err := orderedmap.Query(doc, "$.*")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{inner, "xy"}) {
		t.Fatalf("wildcard = %#v", got)
	}

	arr := []interface{}{"ab", "cdef"}
	got, err = orderedmap.Query(arr, "$.*")
	if err != nil || !reflect.DeepEqual(got, arr) {
		t.Fatalf("array wildcard = %#v %v", got, err)
	}

	n, ok, err := orderedmap.QueryOne(arr, "$.length()")
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if _, isInt := n.(int); !isInt || n != 2 {
		t.Fatalf("array length = %#v (%T)", n, n)
	}
	n, ok, err = orderedmap.QueryOne(doc, "$.length()")
	if err != nil || !ok || n != 2 {
		t.Fatalf("map length = %#v %v %v", n, ok, err)
	}
	n, ok, err = orderedmap.QueryOne(om("s", "hello"), "$.s.length()")
	if err != nil || !ok || n != 5 {
		t.Fatalf("string length = %#v %v %v", n, ok, err)
	}
	got, err = orderedmap.Query(1, "$.length()")
	if err != nil || len(got) != 0 {
		t.Fatalf("length on number = %#v %v", got, err)
	}
}

func TestQueryScriptIndex(t *testing.T) {
	arr := []interface{}{"a", "b", "c"}
	cases := map[string]interface{}{
		"$[(@.length-1)]":     "c",
		"$[(@.length - 2)]":   "b",
		"$[(@.length()-1)]":   "c",
		"$[( @.length - 1 )]": "c",
	}
	for path, want := range cases {
		v, ok, err := orderedmap.QueryOne(arr, path)
		if err != nil || !ok || v != want {
			t.Errorf("%s => %#v %v %v", path, v, ok, err)
		}
	}
	got, err := orderedmap.Query(arr, "$[(@.length-9)]")
	if err != nil || len(got) != 0 {
		t.Fatalf("script out of range = %#v %v", got, err)
	}
	got, err = orderedmap.Query(om("a", 1), "$[(@.length-1)]")
	if err != nil || len(got) != 0 {
		t.Fatalf("script on map = %#v %v", got, err)
	}
}

func TestQueryRecursive(t *testing.T) {
	inner := om("author", "Ann", "price", 8)
	book := []interface{}{inner, om("author", "Bob", "price", 12)}
	doc := om("store", om("book", book, "meta", om("author", "Ed")))

	got, err := orderedmap.Query(doc, "$..author")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{"Ann", "Bob", "Ed"}) {
		t.Fatalf("authors = %#v", got)
	}

	// Earlier sibling's descendant is visited before a later sibling match.
	nested := om("b", om("a", 1), "a", 2)
	got, err = orderedmap.Query(nested, "$..a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{1, 2}) {
		t.Fatalf("dfs = %#v", got)
	}

	got, err = orderedmap.Query(doc, "$..['price','author']")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{8, 12, "Ann", "Bob", "Ed"}) {
		t.Fatalf("recursive union = %#v", got)
	}

	got, err = orderedmap.Query(nested, "$..*")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0] != nested {
		t.Fatalf("$..* should start with the root, got %#v", got)
	}
	wantNodes := []interface{}{nested, om("a", 1), 1, 2}
	// Second element is the map stored at "b", which is a different pointer than a fresh map.
	b, _ := nested.Get("b")
	wantNodes[1] = b
	if !reflect.DeepEqual(got, wantNodes) {
		t.Fatalf("$..* = %#v", got)
	}
}

func TestQueryFilters(t *testing.T) {
	books := []interface{}{
		om("title", "cheap", "price", 8.95, "tags", []interface{}{"a", "go"}),
		om("title", "dear", "price", 12.99, "tags", []interface{}{"b", "py"}),
		om("title", "free", "price", 0, "ok", false),
		om("title", "", "note", nil),
		om("title", "nested", "author", om("name", "Ada")),
	}
	got, err := orderedmap.Query(books, "$[?(@.price < 10)]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || titleOf(got[0]) != "cheap" || titleOf(got[1]) != "free" {
		t.Fatalf("price filter = %#v", titles(got))
	}

	got, err = orderedmap.Query(books, "$[?(@.title)]")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(titles(got), []interface{}{"cheap", "dear", "free", "nested"}) {
		t.Fatalf("truthiness = %#v", titles(got))
	}

	got, err = orderedmap.Query(books, "$[?(@.tags[1] == 'go')]")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(titles(got), []interface{}{"cheap"}) {
		t.Fatalf("index filter = %#v", titles(got))
	}

	got, err = orderedmap.Query(books, "$[?(@.author.name == 'Ada')]")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(titles(got), []interface{}{"nested"}) {
		t.Fatalf("multi-level = %#v", titles(got))
	}

	// && binds tighter than ||: false && true || true  => true, and the inverse shape is false.
	rows := []interface{}{
		om("a", 0, "b", 0, "c", 3), // (a==1 && b==2) || c==3 => true
		om("a", 1, "b", 2, "c", 0), // true
		om("a", 1, "b", 0, "c", 0), // false
		om("a", 0, "b", 2, "c", 0), // false  (would be true if || bound tighter: a==1 && (b==2 || c==3) is still false)
	}
	got, err = orderedmap.Query(rows, "$[?(@.a == 1 && @.b == 2 || @.c == 3)]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("precedence matches = %d %#v", len(got), got)
	}

	nums := []interface{}{0, 1, false, true, "", "x", nil, []interface{}{}, []interface{}{1}, orderedmap.NewMap(), om("k", 1)}
	got, err = orderedmap.Query(nums, "$[?(@)]")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{1, true, "x", []interface{}{1}, om("k", 1)}) {
		t.Fatalf("scalar truthiness = %#v", got)
	}

	got, err = orderedmap.Query([]interface{}{om("name", "amy"), om("name", "be")}, "$[?(@.name.length() > 2)]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("length filter = %#v", got)
	}

	got, err = orderedmap.Query([]interface{}{om("a", nil), om("b", 1), om("a", 0)}, "$[?(@.a == null)]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("== null should not match a missing field, got %d", len(got))
	}
}

func TestQueryNilMatchChainedAndGrouping(t *testing.T) {
	doc := om("a", nil, "books", []interface{}{
		om("title", "a", "price", 1),
		om("title", "b", "price", 5),
	})
	v, ok, err := orderedmap.QueryOne(doc, "$.a")
	if err != nil || !ok || v != nil {
		t.Fatalf("null match = %#v %v %v", v, ok, err)
	}
	got, err := orderedmap.Query(doc, "$.books[?(@.price >= 5)].title")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{"b"}) {
		t.Fatalf("chained = %#v", got)
	}

	rows := []interface{}{
		om("a", 1, "b", 0, "c", 0),
	}
	got, err = orderedmap.Query(rows, "$[?(@.a == 1 || @.b == 1 && @.c == 1)]")
	if err != nil || len(got) != 1 {
		t.Fatalf("or-and without parens = %#v %v", got, err)
	}
	got, err = orderedmap.Query(rows, "$[?((@.a == 1 || @.b == 1) && @.c == 1)]")
	if err != nil || len(got) != 0 {
		t.Fatalf("grouped = %#v %v", got, err)
	}

	got, err = orderedmap.Query(om("key2", 2, "key1", 1), "$['key1', 'key2']")
	if err != nil || !reflect.DeepEqual(got, []interface{}{1, 2}) {
		t.Fatalf("spaced union = %#v %v", got, err)
	}
	names := []interface{}{"amy", "zoe", "moe"}
	got, err = orderedmap.Query(names, "$[?(@ >= 'm' && @ <= 'z')]")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"moe"}) {
		t.Fatalf("string compare = %#v %v", got, err)
	}
}

func TestQuerySyntaxError(t *testing.T) {
	_, err := orderedmap.Query(nil, "nope")
	var se *orderedmap.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("error type %T", err)
	}
	if se.Position != 0 {
		t.Fatalf("position = %d", se.Position)
	}
	if err.Error() != "syntax error at position 0: "+se.Message {
		t.Fatalf("Error() = %q", err.Error())
	}

	for _, path := range []string{"", "$.", "$.a[", "$.a[?]", "$['unterminated", "$.a[?(@.b ==)]", "$.a[(@.length)]"} {
		_, err := orderedmap.Query(om("a", []interface{}{1}), path)
		if !errors.As(err, &se) {
			t.Errorf("%s: want SyntaxError, got %v", path, err)
		}
	}
}

func titles(nodes []interface{}) []interface{} {
	out := make([]interface{}, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, titleOf(n))
	}
	return out
}

func titleOf(n interface{}) interface{} {
	v, _ := n.(*orderedmap.Map).Get("title")
	return v
}
