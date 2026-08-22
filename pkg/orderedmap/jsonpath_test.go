// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"carvel.dev/ytt/pkg/orderedmap"
)

func omap(pairs ...interface{}) *orderedmap.Map {
	m := orderedmap.NewMap()
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func mustQuery(t *testing.T, doc interface{}, path string) []interface{} {
	t.Helper()
	got, err := orderedmap.Query(doc, path)
	if err != nil {
		t.Fatalf("Query(%q) unexpected error: %v", path, err)
	}
	if got == nil {
		t.Fatalf("Query(%q) returned nil slice", path)
	}
	return got
}

func assertQuery(t *testing.T, doc interface{}, path string, want []interface{}) {
	t.Helper()
	got := mustQuery(t, doc, path)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Query(%q)\n got %#v\nwant %#v", path, got, want)
	}
}

func assertEmpty(t *testing.T, doc interface{}, path string) {
	t.Helper()
	got := mustQuery(t, doc, path)
	if len(got) != 0 {
		t.Fatalf("Query(%q) expected empty, got %#v", path, got)
	}
}

func storeDoc() interface{} {
	return omap(
		"store", omap(
			"book", []interface{}{
				omap("category", "reference", "author", "Nigel Rees", "title", "Sayings of the Century", "price", 8.95),
				omap("category", "fiction", "author", "Evelyn Waugh", "title", "Sword of Honour", "price", 12.99),
				omap("category", "fiction", "author", "Herman Melville", "title", "Moby Dick", "isbn", "0-553-21311-3", "price", 8.99),
				omap("category", "fiction", "author", "J. R. R. Tolkien", "title", "The Lord of the Rings", "isbn", "0-395-19395-8", "price", 22.99),
			},
			"bicycle", omap("color", "red", "price", 19.95),
			"my-key", "hyphen",
		),
		"expensive", 10,
	)
}

func TestQueryRoot(t *testing.T) {
	doc := omap("a", 1)
	assertQuery(t, doc, "$", []interface{}{doc})
	assertQuery(t, 42, "$", []interface{}{42})
}

func TestQueryDotAndBracketKeys(t *testing.T) {
	doc := storeDoc()
	assertQuery(t, doc, "$.store.bicycle.color", []interface{}{"red"})
	assertQuery(t, doc, "$['store']['bicycle']['color']", []interface{}{"red"})
	assertQuery(t, doc, `$["store"]["bicycle"]["color"]`, []interface{}{"red"})
	assertQuery(t, doc, "$.store.my-key", []interface{}{"hyphen"})
}

func TestQueryEscapedKeys(t *testing.T) {
	doc := omap("say \"hi\"", 1, "o's", 2, "a\\b", 3)
	assertQuery(t, doc, `$['say "hi"']`, []interface{}{1})
	assertQuery(t, doc, `$["o's"]`, []interface{}{2})
	assertQuery(t, doc, `$['o\'s']`, []interface{}{2})
	assertQuery(t, doc, `$["say \"hi\""]`, []interface{}{1})
	assertQuery(t, doc, `$['a\\b']`, []interface{}{3})
}

func TestQueryIndex(t *testing.T) {
	doc := []interface{}{"a", "b", "c"}
	assertQuery(t, doc, "$[0]", []interface{}{"a"})
	assertQuery(t, doc, "$[2]", []interface{}{"c"})
	assertQuery(t, doc, "$[-1]", []interface{}{"c"})
	assertQuery(t, doc, "$[-3]", []interface{}{"a"})
	assertEmpty(t, doc, "$[3]")
	assertEmpty(t, doc, "$[-4]")
	assertEmpty(t, omap("a", 1), "$[0]")
}

func TestQueryUnion(t *testing.T) {
	doc := omap("a", 1, "b", 2, "c", 3)
	assertQuery(t, doc, "$['c','a']", []interface{}{3, 1})
	assertQuery(t, []interface{}{"x", "y", "z"}, "$[2,0]", []interface{}{"z", "x"})
	assertQuery(t, []interface{}{"x", "y", "z"}, "$[-1,0]", []interface{}{"z", "x"})
}

func TestQueryWildcard(t *testing.T) {
	doc := omap("a", 1, "b", 2)
	assertQuery(t, doc, "$.*", []interface{}{1, 2})
	assertQuery(t, []interface{}{10, 20}, "$[*]", []interface{}{10, 20})
	assertEmpty(t, "nope", "$.*")
}

func TestQueryRecursiveDescent(t *testing.T) {
	doc := storeDoc()
	got := mustQuery(t, doc, "$..price")
	want := []interface{}{8.95, 12.99, 8.99, 22.99, 19.95}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("$..price got %#v want %#v", got, want)
	}

	got = mustQuery(t, doc, "$..['author','title']")
	if len(got) != 8 {
		t.Fatalf("$..['author','title'] expected 8 results, got %d (%#v)", len(got), got)
	}
	if got[0] != "Nigel Rees" || got[1] != "Sayings of the Century" {
		t.Fatalf("union order: got %#v", got[:2])
	}

	rootAndDesc := mustQuery(t, doc, "$..*")
	if len(rootAndDesc) == 0 || !reflect.DeepEqual(rootAndDesc[0], doc) {
		t.Fatalf("$..* should start with the root document, got %#v", rootAndDesc)
	}
}

func TestQueryFilter(t *testing.T) {
	doc := storeDoc()
	got := mustQuery(t, doc, "$.store.book[?(@.price < 10)]")
	if len(got) != 2 {
		t.Fatalf("expected 2 cheap books, got %#v", got)
	}

	got = mustQuery(t, doc, `$.store.book[?(@.author == "Evelyn Waugh")]`)
	if len(got) != 1 {
		t.Fatalf("expected 1 book, got %#v", got)
	}

	got = mustQuery(t, doc, `$.store.book[?(@.isbn)]`)
	if len(got) != 2 {
		t.Fatalf("expected 2 books with isbn, got %#v", got)
	}

	got = mustQuery(t, doc, `$.store.book[?(@.price < 10 && @.category == "fiction")]`)
	if len(got) != 1 {
		t.Fatalf("expected 1 cheap fiction book, got %#v", got)
	}

	got = mustQuery(t, doc, `$.store.book[?(@.price > 20 || @.author == "Nigel Rees")]`)
	if len(got) != 2 {
		t.Fatalf("expected 2 books for or-filter, got %#v", got)
	}

	got = mustQuery(t, doc, `$.store.book[?(@.price >= 12.99 && @.price <= 12.99)]`)
	if len(got) != 1 {
		t.Fatalf("expected 1 book at 12.99, got %#v", got)
	}

	got = mustQuery(t, doc, `$.store.book[?(@.category != "fiction")]`)
	if len(got) != 1 {
		t.Fatalf("expected 1 non-fiction, got %#v", got)
	}
}

func TestQueryFilterLogicalPrecedence(t *testing.T) {
	docs := []interface{}{
		omap("a", true, "b", false, "c", true),
		omap("a", false, "b", true, "c", true),
		omap("a", false, "b", false, "c", false),
	}
	// a || b && c  ==  a || (b && c)
	got := mustQuery(t, docs, `$[?(@.a || @.b && @.c)]`)
	if len(got) != 2 {
		t.Fatalf("precedence: expected 2 matches, got %#v", got)
	}
}

func TestQueryFilterMultiLevelAndIndex(t *testing.T) {
	doc := []interface{}{
		omap("meta", omap("tags", []interface{}{"x", "keep"})),
		omap("meta", omap("tags", []interface{}{"y", "drop"})),
	}
	got := mustQuery(t, doc, `$[?(@.meta.tags[1] == "keep")]`)
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %#v", got)
	}
}

func TestQueryFilterLiterals(t *testing.T) {
	doc := []interface{}{
		omap("n", 1, "s", "hi", "b", true, "z", nil),
		omap("n", 2, "s", "no", "b", false, "z", "x"),
	}
	assertQuery(t, doc, `$[?(@.b == true)]`, []interface{}{doc[0]})
	assertQuery(t, doc, `$[?(@.b == false)]`, []interface{}{doc[1]})
	assertQuery(t, doc, `$[?(@.z == null)]`, []interface{}{doc[0]})
	assertQuery(t, doc, `$[?(@.s == 'hi')]`, []interface{}{doc[0]})
	assertQuery(t, doc, `$[?(@.n != 1)]`, []interface{}{doc[1]})
}

func TestQueryLength(t *testing.T) {
	doc := omap(
		"arr", []interface{}{1, 2, 3},
		"map", omap("a", 1, "b", 2),
		"str", "abcd",
		"items", []interface{}{
			omap("xs", []interface{}{1, 2, 3}),
			omap("xs", []interface{}{1}),
		},
	)
	got := mustQuery(t, doc, "$.arr.length()")
	if len(got) != 1 {
		t.Fatalf("arr.length() got %#v", got)
	}
	if _, ok := got[0].(int); !ok {
		t.Fatalf("length() must return Go int, got %T", got[0])
	}
	if got[0].(int) != 3 {
		t.Fatalf("arr.length() = %v", got[0])
	}
	assertQuery(t, doc, "$.map.length()", []interface{}{2})
	assertQuery(t, doc, "$.str.length()", []interface{}{4})

	got = mustQuery(t, doc, `$.items[?(@.xs.length() > 2)]`)
	if len(got) != 1 {
		t.Fatalf("filter length expected 1, got %#v", got)
	}
	assertEmpty(t, doc, "$.arr.foo.length()")
}

func TestQueryScriptIndex(t *testing.T) {
	doc := []interface{}{"a", "b", "c", "d"}
	assertQuery(t, doc, "$[(@.length-1)]", []interface{}{"d"})
	assertQuery(t, doc, "$[(@.length-2)]", []interface{}{"c"})
	assertQuery(t, doc, "$[( @.length - 1 )]", []interface{}{"d"})
	assertQuery(t, doc, "$[(@.length-0)]", []interface{}{})
	assertEmpty(t, omap("a", 1), "$[(@.length-1)]")
}

func TestQueryIncompatibleTypesEmpty(t *testing.T) {
	assertEmpty(t, omap("a", 1), "$[0]")
	assertEmpty(t, []interface{}{1, 2}, "$.a")
	assertEmpty(t, omap("a", true), "$[?(@.a)]")
	assertEmpty(t, 5, "$.a")
	assertEmpty(t, 5, "$[0]")
	assertEmpty(t, 5, "$.length()")
}

func TestQueryNoMatchEmptySlice(t *testing.T) {
	got, err := orderedmap.Query(omap("a", 1), "$.missing")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected empty non-nil slice, got %#v", got)
	}
}

func TestQueryOne(t *testing.T) {
	doc := omap("a", []interface{}{10, 20})
	v, ok, err := orderedmap.QueryOne(doc, "$.a[1]")
	if err != nil || !ok || v != 20 {
		t.Fatalf("QueryOne got %v %v %v", v, ok, err)
	}
	v, ok, err = orderedmap.QueryOne(doc, "$.missing")
	if err != nil || ok || v != nil {
		t.Fatalf("QueryOne missing got %v %v %v", v, ok, err)
	}
}

func TestQuerySyntaxError(t *testing.T) {
	cases := []struct {
		path string
		pos  int
	}{
		{"", 0},
		{"foo", 0},
		{"$.", 2},
		{"$[0", 1},
		{"$['unterminated]", 2},
		{"$[?(@.x == )]", 11},
	}
	for _, tc := range cases {
		_, err := orderedmap.Query(nil, tc.path)
		if err == nil {
			t.Fatalf("%q: expected syntax error", tc.path)
			continue
		}
		var se *orderedmap.SyntaxError
		if !errors.As(err, &se) {
			t.Fatalf("%q: error type %T (%v)", tc.path, err, err)
		}
		if se.Message == "" {
			t.Fatalf("%q: empty Message", tc.path)
		}
		want := fmt.Sprintf("syntax error at position %d: %s", se.Position, se.Message)
		if err.Error() != want {
			t.Fatalf("%q: Error() = %q, want %q", tc.path, err.Error(), want)
		}
	}

	_, err := orderedmap.Query(nil, "foo")
	var se *orderedmap.SyntaxError
	if !errors.As(err, &se) {
		t.Fatal(err)
	}
	if se.Position != 0 {
		t.Fatalf("position for 'foo': %d", se.Position)
	}
}

func TestQueryTruthiness(t *testing.T) {
	doc := []interface{}{
		nil,
		false,
		true,
		0,
		1,
		"",
		"x",
		[]interface{}{},
		[]interface{}{1},
		omap(),
		omap("a", 1),
	}
	got := mustQuery(t, doc, "$[?(@)]")
	if len(got) != 5 {
		t.Fatalf("truthy values: %#v", got)
	}
}

func TestQueryPlainGoMaps(t *testing.T) {
	doc := map[string]interface{}{
		"arr": []interface{}{
			map[string]interface{}{"name": "a", "n": 1},
			map[string]interface{}{"name": "b", "n": 2},
		},
	}
	got := mustQuery(t, doc, "$.arr[?(@.n > 1)].name")
	if !reflect.DeepEqual(got, []interface{}{"b"}) {
		t.Fatalf("got %#v", got)
	}
}

func TestQueryChainedAfterRecursive(t *testing.T) {
	doc := omap(
		"x", omap("item", omap("n", 1)),
		"y", omap("item", omap("n", 2)),
	)
	assertQuery(t, doc, "$..item.n", []interface{}{1, 2})
}

func TestQueryGoessnerBookPaths(t *testing.T) {
	doc := storeDoc()
	authors := mustQuery(t, doc, "$.store.book[*].author")
	if len(authors) != 4 || authors[0] != "Nigel Rees" {
		t.Fatalf("authors: %#v", authors)
	}
	assertQuery(t, doc, "$.store.book[2].title", []interface{}{"Moby Dick"})
	assertQuery(t, doc, "$..book[-1].title", []interface{}{"The Lord of the Rings"})
	assertQuery(t, doc, "$..book[(@.length-1)].title", []interface{}{"The Lord of the Rings"})
	assertQuery(t, doc, "$.store.book[?(@.price<10)].title", []interface{}{"Sayings of the Century", "Moby Dick"})
	assertQuery(t, doc, "$.store.book[0,1].author", []interface{}{"Nigel Rees", "Evelyn Waugh"})
}
