// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0
package orderedmap_test

import (
	"errors"
	"reflect"
	"testing"

	"carvel.dev/ytt/pkg/orderedmap"
)

func jpTestDoc() interface{} {
	book := func(title string, price interface{}, extra ...orderedmap.MapItem) *orderedmap.Map {
		items := []orderedmap.MapItem{{Key: "title", Value: title}, {Key: "price", Value: price}}
		return orderedmap.NewMapWithItems(append(items, extra...))
	}
	return orderedmap.NewMapWithItems([]orderedmap.MapItem{
		{Key: "store", Value: orderedmap.NewMapWithItems([]orderedmap.MapItem{
			{Key: "book", Value: []interface{}{
				book("A", int64(8), orderedmap.MapItem{Key: "tags", Value: []interface{}{"x", "y"}}),
				book("B", 12.5, orderedmap.MapItem{Key: "isbn", Value: "123"}),
				book("C", int64(9), orderedmap.MapItem{Key: "isbn", Value: ""}),
				book("D", int64(23), orderedmap.MapItem{Key: "tags", Value: []interface{}{"z"}}),
			}},
			{Key: "bicycle", Value: orderedmap.NewMapWithItems([]orderedmap.MapItem{
				{Key: "color", Value: "red"},
				{Key: "price", Value: 19.95},
			})},
		})},
		{Key: "my-key", Value: "dash"},
		{Key: "odd key's", Value: "quoted"},
		{Key: "flag", Value: nil},
	})
}

func titles(t *testing.T, results []interface{}) []interface{} {
	t.Helper()
	out := []interface{}{}
	for _, r := range results {
		m, ok := r.(*orderedmap.Map)
		if !ok {
			t.Fatalf("expected map result, got %T", r)
		}
		v, _ := m.Get("title")
		out = append(out, v)
	}
	return out
}

func TestQuerySelectors(t *testing.T) {
	doc := jpTestDoc()
	cases := []struct {
		path     string
		expected []interface{}
	}{
		{`$.store.bicycle.color`, []interface{}{"red"}},
		{`$.my-key`, []interface{}{"dash"}},
		{`$['my-key']`, []interface{}{"dash"}},
		{`$["odd key's"]`, []interface{}{"quoted"}},
		{`$['odd key\'s']`, []interface{}{"quoted"}},
		{`$.store.book[0].title`, []interface{}{"A"}},
		{`$.store.book[-1].title`, []interface{}{"D"}},
		{`$.store.book[10].title`, []interface{}{}},
		{`$.store.book[-10]`, []interface{}{}},
		{`$.store.book[2,0].title`, []interface{}{"C", "A"}},
		{`$.store.bicycle['price','color']`, []interface{}{19.95, "red"}},
		{`$.store.bicycle[ 'color' , 'price' ]`, []interface{}{"red", 19.95}},
		{`$.store.book[*].price`, []interface{}{int64(8), 12.5, int64(9), int64(23)}},
		{`$.store.bicycle.*`, []interface{}{"red", 19.95}},
		{`$..price`, []interface{}{int64(8), 12.5, int64(9), int64(23), 19.95}},
		{`$..['color','isbn']`, []interface{}{"123", "", "red"}},
		{`$.store.book.length()`, []interface{}{4}},
		{`$.store.bicycle.length()`, []interface{}{2}},
		{`$.store.bicycle.color.length()`, []interface{}{3}},
		{`$.store.bicycle.price.length()`, []interface{}{}},
		{`$.store.book[(@.length-1)].title`, []interface{}{"D"}},
		{`$.store.book[( @.length - 2 )].title`, []interface{}{"C"}},
		{`$.store.book[(@.length-5)]`, []interface{}{}},
		{`$.missing`, []interface{}{}},
		{`$.store[0]`, []interface{}{}},
		{`$.store.book.title`, []interface{}{}},
		{`$.flag`, []interface{}{nil}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			results, err := orderedmap.Query(doc, tc.path)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if !reflect.DeepEqual(results, tc.expected) {
				t.Fatalf("expected %#v, got %#v", tc.expected, results)
			}
		})
	}
}

func TestQueryRoot(t *testing.T) {
	doc := jpTestDoc()
	results, err := orderedmap.Query(doc, "$")
	if err != nil || len(results) != 1 || results[0] != doc {
		t.Fatalf("expected root document, got %#v (err: %v)", results, err)
	}

	small := orderedmap.NewMapWithItems([]orderedmap.MapItem{
		{Key: "a", Value: []interface{}{int64(1), int64(2)}},
		{Key: "b", Value: "x"},
	})
	results, err = orderedmap.Query(small, "$..*")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 || results[0] != small ||
		!reflect.DeepEqual(results[1:], []interface{}{[]interface{}{int64(1), int64(2)}, int64(1), int64(2), "x"}) {
		t.Fatalf("unexpected $..* results: %#v", results)
	}
}

func TestQueryFilters(t *testing.T) {
	doc := jpTestDoc()
	cases := []struct {
		path     string
		expected []interface{}
	}{
		{`$.store.book[?(@.price < 10)]`, []interface{}{"A", "C"}},
		{`$.store.book[?(@.price<=9)]`, []interface{}{"A", "C"}},
		{`$.store.book[?(@.price > 12.5)]`, []interface{}{"D"}},
		{`$.store.book[?(@.price >= 12.5)]`, []interface{}{"B", "D"}},
		{`$.store.book[?(@.title == 'B')]`, []interface{}{"B"}},
		{`$.store.book[?(@.title != "B")]`, []interface{}{"A", "C", "D"}},
		{`$.store.book[?(@.isbn)]`, []interface{}{"B"}},
		{`$.store.book[?(@.tags[0] == 'z')]`, []interface{}{"D"}},
		{`$.store.book[?(@.tags[-1] == 'y')]`, []interface{}{"A"}},
		{`$.store.book[?(@.tags.length() >= 2)]`, []interface{}{"A"}},
		{`$.store.book[?(@.title.length() == 1 && @.price > 10)]`, []interface{}{"B", "D"}},
		{`$.store.book[?(@.price < 9 || @.price > 20 && @.title == 'D')]`, []interface{}{"A", "D"}},
		{`$.store.book[?((@.price < 9 || @.price > 20) && @.title == 'D')]`, []interface{}{"D"}},
		{`$.store.book[?(@.missing == null)]`, []interface{}{}},
		{`$.store.book[?(@.price == $.store.book[0].price)]`, []interface{}{"A"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			results, err := orderedmap.Query(doc, tc.path)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if got := titles(t, results); !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("expected %#v, got %#v", tc.expected, got)
			}
		})
	}
}

func TestQueryFilterLiteralsAndTruthiness(t *testing.T) {
	doc := []interface{}{
		orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "v", Value: nil}}),
		orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "v", Value: false}}),
		orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "v", Value: int64(0)}}),
		orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "v", Value: ""}}),
		orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "v", Value: []interface{}{}}}),
		orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "v", Value: orderedmap.NewMap()}}),
		orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "v", Value: true}}),
		orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "v", Value: 0.5}}),
	}
	count := func(path string) int {
		results, err := orderedmap.Query(doc, path)
		if err != nil {
			t.Fatalf("unexpected error for %s: %s", path, err)
		}
		return len(results)
	}
	if n := count(`$[?(@.v)]`); n != 2 {
		t.Fatalf("expected 2 truthy, got %d", n)
	}
	if n := count(`$[?(@.v == null)]`); n != 1 {
		t.Fatalf("expected 1 null, got %d", n)
	}
	if n := count(`$[?(@.v == true)]`); n != 1 {
		t.Fatalf("expected 1 true, got %d", n)
	}
	if n := count(`$[?(@.v == false)]`); n != 1 {
		t.Fatalf("expected 1 false, got %d", n)
	}
	if n := count(`$[?(@.v == 0)]`); n != 1 {
		t.Fatalf("expected 1 zero, got %d", n)
	}
	if n := count(`$[?(@.v == 5e-1)]`); n != 1 {
		t.Fatalf("expected 1 half, got %d", n)
	}
	if n := count(`$[?(@.v.length() == 0)]`); n != 3 {
		t.Fatalf("expected 3 empty, got %d", n)
	}
}

func TestQueryUnorderedGoMaps(t *testing.T) {
	doc := map[string]interface{}{"b": int64(2), "a": []interface{}{map[string]interface{}{"x": "y"}}}
	results, err := orderedmap.Query(doc, "$.a[0].x")
	if err != nil || !reflect.DeepEqual(results, []interface{}{"y"}) {
		t.Fatalf("unexpected: %#v (err: %v)", results, err)
	}
}

func TestQueryOne(t *testing.T) {
	doc := jpTestDoc()
	val, found, err := orderedmap.QueryOne(doc, "$..price")
	if err != nil || !found || val != int64(8) {
		t.Fatalf("unexpected: %#v %v %v", val, found, err)
	}
	val, found, err = orderedmap.QueryOne(doc, "$.nope")
	if err != nil || found || val != nil {
		t.Fatalf("unexpected: %#v %v %v", val, found, err)
	}
	val, found, err = orderedmap.QueryOne(doc, "$.flag")
	if err != nil || !found || val != nil {
		t.Fatalf("unexpected: %#v %v %v", val, found, err)
	}
	if _, _, err := orderedmap.QueryOne(doc, "store"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestQueryEmptyResultIsNonNil(t *testing.T) {
	results, err := orderedmap.Query(jpTestDoc(), "$.nope")
	if err != nil || results == nil || len(results) != 0 {
		t.Fatalf("expected empty non-nil slice, got %#v (err: %v)", results, err)
	}
}

func TestQuerySyntaxErrors(t *testing.T) {
	cases := []struct {
		path     string
		position int
	}{
		{``, 0},
		{`store`, 0},
		{`$.`, 2},
		{`$..`, 3},
		{`$.a b`, 3},
		{`$[`, 2},
		{`$[]`, 2},
		{`$['a`, 2},
		{`$['a\q']`, 4},
		{`$[1`, 3},
		{`$[1.5]`, 3},
		{`$[?(@.a ==)]`, 10},
		{`$[?(@.a = 1)]`, 8},
		{`$[?(@.a == 1]`, 12},
		{`$[?(@.a == foo)]`, 11},
		{`$[(@.size-1)]`, 5},
		{`$[(@.length-)]`, 12},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			_, err := orderedmap.Query(nil, tc.path)
			var synErr *orderedmap.SyntaxError
			if !errors.As(err, &synErr) {
				t.Fatalf("expected *SyntaxError, got %#v", err)
			}
			if synErr.Position != tc.position {
				t.Fatalf("expected position %d, got %d (%s)", tc.position, synErr.Position, synErr)
			}
		})
	}

	err := &orderedmap.SyntaxError{Message: "boom", Position: 7}
	if err.Error() != "syntax error at position 7: boom" {
		t.Fatalf("unexpected message: %s", err.Error())
	}
}
