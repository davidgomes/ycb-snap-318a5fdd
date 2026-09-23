// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0
package orderedmap_test

import (
	"errors"
	"reflect"
	"testing"

	"carvel.dev/ytt/pkg/orderedmap"
)

func omap(kvs ...interface{}) *orderedmap.Map {
	m := orderedmap.NewMap()
	for i := 0; i < len(kvs); i += 2 {
		m.Set(kvs[i], kvs[i+1])
	}
	return m
}

func list(items ...interface{}) []interface{} { return append([]interface{}{}, items...) }

func jsonPathTestDoc() *orderedmap.Map {
	return omap(
		"store", omap(
			"book", list(
				omap("category", "reference", "author", "Nigel Rees", "title", "Sayings", "price", 8.95),
				omap("category", "fiction", "author", "Evelyn Waugh", "title", "Sword", "price", int64(12)),
				omap("category", "fiction", "author", "Herman Melville", "title", "Moby Dick", "isbn", "0-553", "price", 8.99),
				omap("category", "fiction", "author", "J. R. R. Tolkien", "title", "LOTR", "isbn", "0-395", "price", 22.99),
			),
			"bicycle", omap("color", "red", "price", int64(19)),
		),
		"my-key", "hyphen",
		"quo'te", "quoted",
		"empty", list(),
		"flags", list(true, false, int64(0), "", nil, list(), omap(), int64(1), "x", list(int64(1)), omap("a", int64(1))),
		"nums", list(int64(1), int64(2), int64(3), int64(4), int64(5)),
	)
}

func TestJSONPathQuery(t *testing.T) {
	doc := jsonPathTestDoc()
	store, _ := doc.Get("store")
	books, _ := store.(*orderedmap.Map).Get("book")
	bicycle, _ := store.(*orderedmap.Map).Get("bicycle")

	tests := []struct {
		path     string
		expected []interface{}
	}{
		{"$", list(doc)},
		{"$.store.bicycle.color", list("red")},
		{"$.my-key", list("hyphen")},
		{"$['my-key']", list("hyphen")},
		{`$["my-key"]`, list("hyphen")},
		{`$['quo\'te']`, list("quoted")},
		{`$["quo'te"]`, list("quoted")},
		{"$.missing", list()},
		{"$.store.book[0].title", list("Sayings")},
		{"$.store.book[-1].title", list("LOTR")},
		{"$.store.book[4]", list()},
		{"$.store.book[-5]", list()},
		{"$.store.book[2,0].title", list("Moby Dick", "Sayings")},
		{"$.store.bicycle['price','color']", list(int64(19), "red")},
		{"$.store.bicycle[ 'color' , 'price' ]", list("red", int64(19))},
		{"$.store.book[*].author", list("Nigel Rees", "Evelyn Waugh", "Herman Melville", "J. R. R. Tolkien")},
		{"$.store.*", list(books, bicycle)},
		{"$.store.bicycle[*]", list("red", int64(19))},
		{"$..author", list("Nigel Rees", "Evelyn Waugh", "Herman Melville", "J. R. R. Tolkien")},
		{"$.store..price", list(8.95, int64(12), 8.99, 22.99, int64(19))},
		{"$.store.bicycle..*", list(bicycle, "red", int64(19))},
		{"$.store.bicycle..['color','price']", list("red", int64(19))},
		{"$..book[1].title", list("Sword")},

		// Incompatible selectors
		{"$.store[0]", list()},
		{"$.store.book.title", list()},
		{"$.my-key[0]", list()},
		{"$.my-key.*", list()},

		// Length
		{"$.store.book.length()", list(4)},
		{"$.store.bicycle.length()", list(2)},
		{"$.my-key.length()", list(6)},
		{"$.store.bicycle.price.length()", list()},
		{"$.empty.length()", list(0)},

		// Script
		{"$.nums[(@.length-1)]", list(int64(5))},
		{"$.nums[( @.length - 2 )]", list(int64(4))},
		{"$.nums[(@.length-6)]", list()},
		{"$.nums[(@.length)]", list()},
		{"$.store[(@.length-1)]", list()},

		// Filters
		{"$.store.book[?(@.price < 10)].title", list("Sayings", "Moby Dick")},
		{"$.store.book[?(@.price >= 12)].title", list("Sword", "LOTR")},
		{"$.store.book[?(@.price > 12)].title", list("LOTR")},
		{"$.store.book[?(@.price <= 12)].title", list("Sayings", "Sword", "Moby Dick")},
		{"$.store.book[?(@.price == 12.0)].title", list("Sword")},
		{"$.store.book[?(@.category == 'reference')].title", list("Sayings")},
		{`$.store.book[?(@.category != "reference")].title`, list("Sword", "Moby Dick", "LOTR")},
		{"$.store.book[?(@.isbn)].title", list("Moby Dick", "LOTR")},
		{"$.store.book[?(@.category == 'fiction' && @.price < 20)].title", list("Sword", "Moby Dick")},
		{"$.store.book[?(@.price < 9 || @.price > 20 && @.isbn)].title", list("Sayings", "Moby Dick", "LOTR")},
		{"$.store.book[?((@.price < 9 || @.price > 20) && @.isbn)].title", list("Moby Dick", "LOTR")},
		{"$.store.book[?(@.title.length() > 5)].title", list("Sayings", "Moby Dick")},
		{"$.store.book[?(length(@.title) == 4)].title", list("LOTR")},
		{"$.store.book[?(@.price < $.store.bicycle.price)].title", list("Sayings", "Sword", "Moby Dick")},
		{"$.nums[?(@ > 3)]", list(int64(4), int64(5))},
		{"$.flags[?(@)]", list(true, int64(1), "x", list(int64(1)), omap("a", int64(1)))},
		{"$.flags[?(@ == null)]", list(nil)},
		{"$.flags[?(@ == true)]", list(true)},
		{"$.flags[?(@ == false)]", list(false)},
		{"$.store[?(@.color == 'red')]", list(bicycle)},
		{"$[?(@.book[0].price == 8.95)]", list(store)},
		{"$[?(@['book'][-1]['title'] == 'LOTR')]", list(store)},
		{"$.store.book[?(@.missing != 1)].title", list("Sayings", "Sword", "Moby Dick", "LOTR")},
		{"$.store.book[?(@.missing == 1)].title", list()},
		{"$.store.book[?(@.missing < 1)].title", list()},
		{"$.store.book[?(@.title < 'N')].title", list("Moby Dick", "LOTR")},
		{"$.store.book[?(@.title > 5)]", list()},
		{"$.flags[?(@.length > 0)]", list("x", list(int64(1)))},
		{"$.nums[?(@ == 1)][0]", list()},
		{"$..[?(@.color)]", list(bicycle)},
		{"$.store.book[?(@.price==8.95)].title", list("Sayings")},
		{"$[ 'nums' ][ 0 ]", list(int64(1))},
		{"$.nums[?(@ == -1e0 || @ == 2.0)]", list(int64(2))},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result, err := orderedmap.Query(doc, tc.path)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if result == nil {
				t.Fatalf("expected non-nil result")
			}
			if !reflect.DeepEqual(result, tc.expected) {
				t.Fatalf("\nexpected: %#v\n     got: %#v", tc.expected, result)
			}
		})
	}
}

func TestJSONPathRecursiveWildcardStartsWithRoot(t *testing.T) {
	doc := omap("a", list(int64(1), omap("b", "c")), "d", "e")
	a, _ := doc.Get("a")
	expected := list(doc, a, int64(1), a.([]interface{})[1], "c", "e")

	result, err := orderedmap.Query(doc, "$..*")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("\nexpected: %#v\n     got: %#v", expected, result)
	}
}

func TestJSONPathRecursiveDescentIsDepthFirst(t *testing.T) {
	doc := omap("x", omap("k", omap("k", int64(1))), "k", int64(2))

	result, err := orderedmap.Query(doc, "$..k")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	expected := list(int64(2), omap("k", int64(1)), int64(1))
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("\nexpected: %#v\n     got: %#v", expected, result)
	}
}

func TestJSONPathUnorderedGoMaps(t *testing.T) {
	doc := map[string]interface{}{"b": int64(2), "a": []interface{}{map[interface{}]interface{}{"c": "d"}}}

	result, err := orderedmap.Query(doc, "$.a[0].c")
	if err != nil || !reflect.DeepEqual(result, list("d")) {
		t.Fatalf("unexpected result %#v (err: %v)", result, err)
	}

	result, err = orderedmap.Query(doc, "$.*.length()")
	if err != nil || !reflect.DeepEqual(result, list(1)) {
		t.Fatalf("unexpected result %#v (err: %v)", result, err)
	}
}

func TestJSONPathQueryOne(t *testing.T) {
	doc := jsonPathTestDoc()

	val, found, err := orderedmap.QueryOne(doc, "$.store.book[*].title")
	if err != nil || !found || val != "Sayings" {
		t.Fatalf("unexpected result %#v, %v, %v", val, found, err)
	}

	val, found, err = orderedmap.QueryOne(doc, "$.nope")
	if err != nil || found || val != nil {
		t.Fatalf("unexpected result %#v, %v, %v", val, found, err)
	}

	val, found, err = orderedmap.QueryOne(doc, "$.flags[4]")
	if err != nil || !found || val != nil {
		t.Fatalf("unexpected result %#v, %v, %v", val, found, err)
	}

	_, found, err = orderedmap.QueryOne(doc, "store")
	var syntaxErr *orderedmap.SyntaxError
	if found || !errors.As(err, &syntaxErr) {
		t.Fatalf("expected syntax error, got %v", err)
	}
}

func TestJSONPathSyntaxErrors(t *testing.T) {
	tests := []struct {
		path     string
		position int
	}{
		{"", 0},
		{"store", 0},
		{"$.", 2},
		{"$..", 3},
		{"$.a.", 4},
		{"$a", 1},
		{"$[", 2},
		{"$[]", 2},
		{"$['a'", 5},
		{"$['a", 2},
		{`$['a\q']`, 4},
		{"$[1,]", 4},
		{"$[1 2]", 4},
		{"$[?(@.a = 1)]", 8},
		{"$[?(@.a == )]", 11},
		{"$[?(@.a == 1]", 12},
		{"$[?(@.a == 1)", 13},
		{"$[?(@.a && )]", 11},
		{"$[?(@..a)]", 5},
		{"$[?(foo)]", 4},
		{"$[(@.foo)]", 3},
		{"$[(@.length * 2)]", 12},
		{"$.a.foo()", 4},
		{"$.a.length(", 11},
		{"$.a b", 3},
		{"$[99999999999999999999]", 2},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			_, err := orderedmap.Query(omap(), tc.path)
			var syntaxErr *orderedmap.SyntaxError
			if !errors.As(err, &syntaxErr) {
				t.Fatalf("expected *SyntaxError, got %T: %v", err, err)
			}
			if syntaxErr.Position != tc.position {
				t.Fatalf("expected position %d, got %d (%s)", tc.position, syntaxErr.Position, syntaxErr.Message)
			}
		})
	}
}

func TestJSONPathSyntaxErrorFormat(t *testing.T) {
	err := &orderedmap.SyntaxError{Message: "boom", Position: 3}
	if err.Error() != "syntax error at position 3: boom" {
		t.Fatalf("unexpected message: %s", err.Error())
	}
}
