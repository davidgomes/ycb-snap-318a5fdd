// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap_test

import (
	"reflect"
	"testing"
	"unicode/utf8"

	"carvel.dev/ytt/pkg/orderedmap"
)

func omap(kv ...interface{}) *orderedmap.Map {
	m := orderedmap.NewMap()
	for i := 0; i < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return m
}

func TestSyntaxErrorFormat(t *testing.T) {
	err := &orderedmap.SyntaxError{Message: "boom", Position: 4}
	if got, want := err.Error(), "syntax error at position 4: boom"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestQuerySyntaxErrors(t *testing.T) {
	_, err := orderedmap.Query(nil, "nope")
	se, ok := err.(*orderedmap.SyntaxError)
	if !ok {
		t.Fatalf("got %T (%v), want *SyntaxError", err, err)
	}
	if se.Position != 0 {
		t.Fatalf("position = %d, want 0", se.Position)
	}
	if se.Error() != "syntax error at position 0: "+se.Message {
		t.Fatalf("Error() = %q", se.Error())
	}

	_, err = orderedmap.Query(nil, "")
	if _, ok := err.(*orderedmap.SyntaxError); !ok {
		t.Fatalf("empty path: got %v", err)
	}

	bad := []string{
		"$.",
		"$[",
		"$..",
		"$.foo[",
		"$['unterminated",
		"$.a[?",
	}
	for _, path := range bad {
		_, err := orderedmap.Query(nil, path)
		if _, ok := err.(*orderedmap.SyntaxError); !ok {
			t.Errorf("%s: got %v, want *SyntaxError", path, err)
		}
	}
}

func TestQueryRootAndMissing(t *testing.T) {
	doc := omap("a", 1)
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
		t.Fatalf("QueryOne missing = (%v, %v, %v)", v, ok, err)
	}

	v, ok, err = orderedmap.QueryOne(nil, "$")
	if err != nil || !ok || v != nil {
		t.Fatalf("QueryOne nil root = (%v, %v, %v)", v, ok, err)
	}

	_, _, err = orderedmap.QueryOne(doc, "not-a-path")
	if _, isSE := err.(*orderedmap.SyntaxError); !isSE {
		t.Fatalf("QueryOne syntax = %v", err)
	}
}

func TestQueryDotAndBracket(t *testing.T) {
	doc := omap(
		"my-key", 7,
		"a", omap("b", "bee"),
		"a'b", 1,
		"a\"b", 2,
		"a\\b", 3,
		"line\n", 4,
		"A", 5,
		"length", "not-a-function",
	)
	tests := []struct {
		path string
		want []interface{}
	}{
		{"$.my-key", []interface{}{7}},
		{"$['my-key']", []interface{}{7}},
		{`$["my-key"]`, []interface{}{7}},
		{"$.a.b", []interface{}{"bee"}},
		{"$.a['b']", []interface{}{"bee"}},
		{`$['a\'b']`, []interface{}{1}},
		{`$["a\"b"]`, []interface{}{2}},
		{`$['a\\b']`, []interface{}{3}},
		{`$['line\n']`, []interface{}{4}},
		{`$['\u0041']`, []interface{}{5}},
		{"$.length", []interface{}{"not-a-function"}},
		{"$.nope.child", []interface{}{}},
	}
	for _, tt := range tests {
		got, err := orderedmap.Query(doc, tt.path)
		if err != nil {
			t.Errorf("%s: %v", tt.path, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s = %#v, want %#v", tt.path, got, tt.want)
		}
	}
}

func TestQueryIndexesAndUnions(t *testing.T) {
	arr := []interface{}{10, 20, 30, 40}
	doc := omap("arr", arr, "a", 1, "b", 2, "c", 3)

	tests := []struct {
		path string
		want []interface{}
	}{
		{"$.arr[0]", []interface{}{10}},
		{"$.arr[-1]", []interface{}{40}},
		{"$.arr[ -2 ]", []interface{}{30}},
		{"$.arr[5]", []interface{}{}},
		{"$.arr[-5]", []interface{}{}},
		{"$.arr[2,0]", []interface{}{30, 10}},
		{"$.arr[0,0]", []interface{}{10, 10}},
		{"$.arr[5,1]", []interface{}{20}},
		{"$['c','a']", []interface{}{3, 1}},
		{"$['missing','b']", []interface{}{2}},
		{"$[0]", []interface{}{}},
		{"$.arr.key", []interface{}{}},
	}
	for _, tt := range tests {
		got, err := orderedmap.Query(doc, tt.path)
		if err != nil {
			t.Errorf("%s: %v", tt.path, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s = %#v, want %#v", tt.path, got, tt.want)
		}
	}

	// Falsy values are still matches when selected directly.
	got, err := orderedmap.Query(omap("z", 0, "s", "", "f", false, "n", nil), "$.z")
	if err != nil || !reflect.DeepEqual(got, []interface{}{0}) {
		t.Fatalf("zero = %#v, %v", got, err)
	}
	v, ok, err := orderedmap.QueryOne(omap("n", nil), "$.n")
	if err != nil || !ok || v != nil {
		t.Fatalf("explicit null = (%v, %v, %v)", v, ok, err)
	}
}

func TestQueryWildcardAndRecursive(t *testing.T) {
	inner := omap("target", 1)
	doc := omap(
		"a", inner,
		"b", 2,
		"target", 3,
	)

	got, err := orderedmap.Query(doc, "$.*")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{inner, 2, 3}) {
		t.Fatalf("$.* = %#v", got)
	}
	got, err = orderedmap.Query(doc, "$[*]")
	if err != nil || !reflect.DeepEqual(got, []interface{}{inner, 2, 3}) {
		t.Fatalf("$[*] = %#v, %v", got, err)
	}

	got, err = orderedmap.Query(doc, "$..target")
	if err != nil {
		t.Fatal(err)
	}
	// Root is visited first, so its own "target" is emitted before descendants.
	if !reflect.DeepEqual(got, []interface{}{3, 1}) {
		t.Fatalf("$..target = %#v", got)
	}

	nested := omap(
		"a", omap("target", omap("target", 1)),
		"target", 2,
	)
	got, err = orderedmap.Query(nested, "$..target")
	if err != nil {
		t.Fatal(err)
	}
	wantNested := []interface{}{2, omap("target", 1), 1}
	if !reflect.DeepEqual(got, wantNested) {
		t.Fatalf("nested $..target = %#v", got)
	}

	got, err = orderedmap.Query(doc, "$..*")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0] != doc {
		t.Fatalf("$..* should start with the root, got %#v", got)
	}
	if !reflect.DeepEqual(got, []interface{}{doc, inner, 1, 2, 3}) {
		t.Fatalf("$..* = %#v", got)
	}

	got, err = orderedmap.Query(doc, "$..['b','a']")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{2, inner}) {
		t.Fatalf("$..['b','a'] = %#v", got)
	}

	arrDoc := []interface{}{10, []interface{}{20, 30}}
	got, err = orderedmap.Query(arrDoc, "$..*")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{arrDoc, 10, []interface{}{20, 30}, 20, 30}) {
		t.Fatalf("array $..* = %#v", got)
	}

	got, err = orderedmap.Query(omap("a", []interface{}{5, 3, []interface{}{omap("j", 4), omap("k", 6)}}), "$.a..[0, 1]")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{5, 3, omap("j", 4), omap("k", 6)}) {
		t.Fatalf("$.a..[0, 1] = %#v", got)
	}
}

func TestQueryFilters(t *testing.T) {
	books := []interface{}{
		omap("title", "cheap", "price", 5, "ok", true),
		omap("title", "dear", "price", 15, "ok", false),
		omap("title", "free", "price", 0, "ok", true),
		omap("title", "", "price", 3),
		omap("price", nil),
		omap(),
	}
	doc := omap("books", books)

	got, err := orderedmap.Query(doc, "$.books[?(@.price < 10)].title")
	if err != nil {
		t.Fatal(err)
	}
	// price 5, 0, 3. Missing price is not < 10. null price is not < 10.
	if !reflect.DeepEqual(got, []interface{}{"cheap", "free", ""}) {
		t.Fatalf("price < 10 titles = %#v", got)
	}

	got, err = orderedmap.Query(doc, "$.books[?(@.ok)]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("truthy ok = %#v", got)
	}

	got, err = orderedmap.Query(doc, "$.books[?(@.price == null)]")
	if err != nil {
		t.Fatal(err)
	}
	// Explicit null and a missing price both evaluate to nil.
	if len(got) != 2 {
		t.Fatalf("== null = %#v", got)
	}

	got, err = orderedmap.Query(doc, "$.books[?(@.title != null)]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("!= null len = %d (%#v)", len(got), got)
	}

	items := []interface{}{
		omap("a", 1, "b", 0, "c", 0),
		omap("a", 0, "b", 1, "c", 1),
		omap("a", 0, "b", 1, "c", 0),
		omap("a", 0, "b", 0, "c", 1),
	}
	got, err = orderedmap.Query(items, "$[?(@.a == 1 || @.b == 1 && @.c == 1)]")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{items[0], items[1]}) {
		t.Fatalf("precedence = %#v", got)
	}

	got, err = orderedmap.Query(items, "$[?((@.a == 1 || @.b == 1) && @.c == 1)]")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{items[1]}) {
		t.Fatalf("grouping = %#v", got)
	}

	tagged := []interface{}{
		omap("meta", omap("tags", []interface{}{"x", "y"})),
		omap("meta", omap("tags", []interface{}{"y"})),
		omap("meta", omap("tags", []interface{}{})),
	}
	got, err = orderedmap.Query(tagged, "$[?(@.meta.tags[0] == 'x')]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("multi-level index filter = %#v", got)
	}
	got, err = orderedmap.Query(tagged, "$[?(@.meta.tags[-1] == 'y')]")
	if err != nil || len(got) != 2 {
		t.Fatalf("negative filter index = %#v, %v", got, err)
	}

	rels := []interface{}{
		omap("n", 2, "s", "b", "flag", true),
		omap("n", 2, "s", "a", "flag", false),
		omap("n", int64(2), "s", "b"),
		omap("n", 2.5),
	}
	got, err = orderedmap.Query(rels, "$[?(@.n == 2)]")
	if err != nil || len(got) != 3 {
		t.Fatalf("numeric == = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(rels, "$[?(@.n >= 2 && @.s < 'b')]")
	if err != nil || len(got) != 1 {
		t.Fatalf("string compare = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(rels, "$[?(@.flag == false)]")
	if err != nil || len(got) != 1 {
		t.Fatalf("bool == false = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(rels, "$[?(@.flag == true)]")
	if err != nil || len(got) != 1 {
		t.Fatalf("bool == true = %#v, %v", got, err)
	}

	// Truthiness of the usual falsy values.
	falsy := []interface{}{
		omap("v", nil),
		omap("v", false),
		omap("v", 0),
		omap("v", int64(0)),
		omap("v", 0.0),
		omap("v", ""),
		omap("v", []interface{}{}),
		omap("v", orderedmap.NewMap()),
		omap("v", 1),
		omap("v", "x"),
		omap("v", true),
		omap("v", []interface{}{0}),
		omap("v", omap("a", 1)),
		omap("v", -1),
	}
	got, err = orderedmap.Query(falsy, "$[?(@.v)]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("truthiness matches = %d (%#v)", len(got), got)
	}
}

func TestQueryLengthAndScript(t *testing.T) {
	doc := omap(
		"arr", []interface{}{"a", "b", "c"},
		"s", "héllo",
		"m", omap("a", 1, "b", 2),
		"n", 5,
		"items", []interface{}{
			omap("name", "ab", "tags", []interface{}{"x"}),
			omap("name", "abcd", "tags", []interface{}{"x", "y"}),
		},
	)

	got, err := orderedmap.Query(doc, "$.arr.length()")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatal(got)
	}
	n, ok := got[0].(int)
	if !ok || n != 3 {
		t.Fatalf("array length = %#v (%T)", got[0], got[0])
	}

	got, err = orderedmap.Query(doc, "$.s.length()")
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != utf8.RuneCountInString("héllo") {
		t.Fatalf("string length = %#v", got[0])
	}
	if _, ok := got[0].(int); !ok {
		t.Fatalf("string length type %T", got[0])
	}

	got, err = orderedmap.Query(doc, "$.m.length()")
	if err != nil || !reflect.DeepEqual(got, []interface{}{2}) {
		t.Fatalf("map length = %#v, %v", got, err)
	}
	if _, ok := got[0].(int); !ok {
		t.Fatalf("map length type %T", got[0])
	}

	got, err = orderedmap.Query(doc, "$.n.length()")
	if err != nil || len(got) != 0 {
		t.Fatalf("length of number = %#v, %v", got, err)
	}

	got, err = orderedmap.Query(doc, "$.items[?(@.name.length() > 2)].name")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"abcd"}) {
		t.Fatalf("length filter = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(doc, "$.items[?(length(@.tags) >= 2)].name")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"abcd"}) {
		t.Fatalf("length() call filter = %#v, %v", got, err)
	}

	got, err = orderedmap.Query(doc, "$.arr[(@.length-1)]")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"c"}) {
		t.Fatalf("script = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(doc, "$.arr[( @.length - 2 )]")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"b"}) {
		t.Fatalf("script ws = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(doc, "$.arr[(@.length-5)]")
	if err != nil || len(got) != 0 {
		t.Fatalf("script oob = %#v, %v", got, err)
	}

	v, ok, err := orderedmap.QueryOne(doc, "$.items[?(@.name.length() >= 1)].name")
	if err != nil || !ok || v != "ab" {
		t.Fatalf("QueryOne first = (%v, %v, %v)", v, ok, err)
	}
}

func TestQueryPlainMaps(t *testing.T) {
	doc := map[string]interface{}{
		"a": map[string]interface{}{"b": []interface{}{1, 2, 3}},
	}
	got, err := orderedmap.Query(doc, "$.a.b[-1]")
	if err != nil || !reflect.DeepEqual(got, []interface{}{3}) {
		t.Fatalf("plain map = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(doc, "$.a.b.length()")
	if err != nil || !reflect.DeepEqual(got, []interface{}{3}) {
		t.Fatalf("plain length = %#v, %v", got, err)
	}

	books := []map[string]interface{}{
		{"title": "cheap", "price": 5},
		{"title": "dear", "price": 15},
	}
	got, err = orderedmap.Query(map[string]interface{}{"books": books}, "$.books[?(@.price < 10)].title")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"cheap"}) {
		t.Fatalf("typed slice = %#v, %v", got, err)
	}

	counts := map[string]int{"b": 2, "a": 1}
	got, err = orderedmap.Query(counts, "$.*")
	if err != nil || !reflect.DeepEqual(got, []interface{}{1, 2}) {
		t.Fatalf("typed map wildcard = %#v, %v", got, err)
	}
	got, err = orderedmap.Query([]int{1, 2, 3}, "$[?(@ > 1)]")
	if err != nil || !reflect.DeepEqual(got, []interface{}{2, 3}) {
		t.Fatalf("typed int slice filter = %#v, %v", got, err)
	}
	got, err = orderedmap.Query([]int{10, 20, 30}, "$[(@.length()-1)]")
	if err != nil || !reflect.DeepEqual(got, []interface{}{30}) {
		t.Fatalf("script length() = %#v, %v", got, err)
	}
}

func TestQueryBookstore(t *testing.T) {
	book := func(category, author, title string, price float64, isbn string) *orderedmap.Map {
		m := omap("category", category, "author", author, "title", title, "price", price)
		if isbn != "" {
			m.Set("isbn", isbn)
		}
		return m
	}
	books := []interface{}{
		book("reference", "Nigel Rees", "Sayings of the Century", 8.95, ""),
		book("fiction", "Evelyn Waugh", "Sword of Honour", 12.99, ""),
		book("fiction", "Herman Melville", "Moby Dick", 8.99, "0-553-21311-3"),
		book("fiction", "J. R. R. Tolkien", "The Lord of the Rings", 22.99, "0-395-19395-8"),
	}
	doc := omap("store", omap(
		"book", books,
		"bicycle", omap("color", "red", "price", 399),
	))

	got, err := orderedmap.Query(doc, "$.store.book[*].author")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []interface{}{"Nigel Rees", "Evelyn Waugh", "Herman Melville", "J. R. R. Tolkien"}) {
		t.Fatalf("authors = %#v", got)
	}

	got, err = orderedmap.Query(doc, "$..author")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"Nigel Rees", "Evelyn Waugh", "Herman Melville", "J. R. R. Tolkien"}) {
		t.Fatalf("$..author = %#v, %v", got, err)
	}

	got, err = orderedmap.Query(doc, "$..book[2].title")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"Moby Dick"}) {
		t.Fatalf("third book = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(doc, "$..book[(@.length-1)].title")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"The Lord of the Rings"}) {
		t.Fatalf("last book = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(doc, "$..book[0,1].title")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"Sayings of the Century", "Sword of Honour"}) {
		t.Fatalf("first two = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(doc, "$..book[?(@.isbn)].title")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"Moby Dick", "The Lord of the Rings"}) {
		t.Fatalf("isbn = %#v, %v", got, err)
	}
	got, err = orderedmap.Query(doc, "$..book[?(@.price<10)].title")
	if err != nil || !reflect.DeepEqual(got, []interface{}{"Sayings of the Century", "Moby Dick"}) {
		t.Fatalf("cheap = %#v, %v", got, err)
	}

	v, ok, err := orderedmap.QueryOne([]interface{}{false, 0, ""}, "$[0]")
	if err != nil || !ok || v != false {
		t.Fatalf("QueryOne false = (%v, %v, %v)", v, ok, err)
	}
}
