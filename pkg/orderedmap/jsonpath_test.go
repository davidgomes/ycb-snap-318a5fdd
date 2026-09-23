// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

//revive:disable:add-constant,line-length-limit,cognitive-complexity,cyclomatic,use-any

package orderedmap_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
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

func assertQuery(t *testing.T, doc interface{}, path string, want []interface{}) {
	t.Helper()
	got, err := orderedmap.Query(doc, path)
	if err != nil {
		t.Fatalf("Query(%q) error: %v", path, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Query(%q)\n got: %#v\nwant: %#v", path, got, want)
	}
}

func assertEmpty(t *testing.T, doc interface{}, path string) {
	t.Helper()
	got, err := orderedmap.Query(doc, path)
	if err != nil {
		t.Fatalf("Query(%q) error: %v", path, err)
	}
	if got == nil {
		t.Fatalf("Query(%q) returned a nil slice", path)
	}
	if len(got) != 0 {
		t.Fatalf("Query(%q) = %#v, want empty", path, got)
	}
}

func TestQueryRootAndDot(t *testing.T) {
	doc := om("my-key", "v", "n", 3, "child", om("my_key", true))
	assertQuery(t, doc, "$", []interface{}{doc})
	assertQuery(t, doc, "$.my-key", []interface{}{"v"})
	assertQuery(t, doc, "$.n", []interface{}{3})
	assertQuery(t, doc, "$.child.my_key", []interface{}{true})
	assertQuery(t, doc, "$.missing", []interface{}{})
	assertQuery(t, "hello", "$", []interface{}{"hello"})
}

func TestQueryBracketAndEscapes(t *testing.T) {
	doc := om(
		"my-key", 1,
		"a'b", 2,
		"a\"b", 3,
		"a\\b", 4,
		"A", 5,
		"line", "x\ny",
		"my key", 6,
	)
	assertQuery(t, doc, "$['my-key']", []interface{}{1})
	assertQuery(t, doc, `$["my-key"]`, []interface{}{1})
	assertQuery(t, doc, `$['a\'b']`, []interface{}{2})
	assertQuery(t, doc, `$["a\"b"]`, []interface{}{3})
	assertQuery(t, doc, `$['a\\b']`, []interface{}{4})
	assertQuery(t, doc, `$['\u0041']`, []interface{}{5})
	assertQuery(t, doc, `$['my key']`, []interface{}{6})
	lines := []interface{}{om("line", "x\ny"), om("line", "xy")}
	assertQuery(t, lines, `$[?(@.line == 'x\ny')].line`, []interface{}{"x\ny"})
	assertQuery(t, doc, "$.child['nope']", []interface{}{})
}

func TestQueryIndexAndUnion(t *testing.T) {
	arr := []interface{}{"a", "b", "c", "d"}
	assertQuery(t, arr, "$[0]", []interface{}{"a"})
	assertQuery(t, arr, "$[-1]", []interface{}{"d"})
	assertQuery(t, arr, "$[-2]", []interface{}{"c"})
	assertQuery(t, arr, "$[2,0]", []interface{}{"c", "a"})
	assertQuery(t, arr, "$[ 1 , -1 ]", []interface{}{"b", "d"})
	assertEmpty(t, arr, "$[4]")
	assertEmpty(t, arr, "$[-5]")
	assertEmpty(t, []interface{}{}, "$[0]")
	assertEmpty(t, []interface{}{}, "$[-1]")

	doc := om("b", 1, "a", 2, "c", 3)
	assertQuery(t, doc, "$['c','a']", []interface{}{3, 2})
	assertQuery(t, doc, `$["b","missing","a"]`, []interface{}{1, 2})
	assertQuery(t, doc, "$.*", []interface{}{1, 2, 3})
	assertQuery(t, doc, "$[*]", []interface{}{1, 2, 3})
	assertQuery(t, arr, "$.*", []interface{}{"a", "b", "c", "d"})
	assertQuery(t, arr, "$[*]", []interface{}{"a", "b", "c", "d"})
}

func TestQueryIncompatibleSelector(t *testing.T) {
	doc := om("a", 1)
	arr := []interface{}{1, 2}
	assertEmpty(t, doc, "$[0]")
	assertEmpty(t, doc, "$[-1]")
	assertEmpty(t, doc, "$[1,0]")
	assertEmpty(t, arr, "$.a")
	assertEmpty(t, arr, "$['a','b']")
	assertEmpty(t, 5, "$.a")
	assertEmpty(t, 5, "$[0]")
	assertEmpty(t, "abc", "$[0]")
	assertEmpty(t, doc, "$[?(@.a == 1)]")
	assertEmpty(t, doc, "$[(@.length-1)]")
	assertEmpty(t, 5, "$.length()")
}

func TestQueryRecursive(t *testing.T) {
	inner := om("c", 1)
	doc := om("a", inner, "b", 2)
	got, err := orderedmap.Query(doc, "$..*")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0] != doc {
		t.Fatalf("$..* did not start with the root: %#v", got)
	}
	wantNodes := []interface{}{doc, inner, 1, 2}
	if !reflect.DeepEqual(got, wantNodes) {
		t.Fatalf("$..* = %#v, want %#v", got, wantNodes)
	}

	tree := om(
		"b", om("a", 1),
		"a", 2,
		"child", []interface{}{om("a", 3), "x"},
	)
	assertQuery(t, tree, "$..a", []interface{}{2, 1, 3})
	assertQuery(t, tree, "$..['a','b']", []interface{}{
		2,
		om("a", 1),
		1,
		3,
	})
	books := om("book", []interface{}{
		om("author", "A"),
		om("author", "B"),
	})
	assertQuery(t, books, "$.book[*].author", []interface{}{"A", "B"})
	assertQuery(t, books, "$.book[-1].author", []interface{}{"B"})
	assertQuery(t, books, "$..author", []interface{}{"A", "B"})
	assertQuery(t, om("my-key", om("inner-key", 9)), "$.my-key.inner-key", []interface{}{9})
	assertQuery(t, []interface{}{om("k", "v")}, "$..k", []interface{}{"v"})
	assertEmpty(t, []interface{}{1, 2}, "$..missing")
}

func TestQueryFilter(t *testing.T) {
	items := []interface{}{
		om("price", 8, "title", "a", "ok", true, "name", nil),
		om("price", 12, "title", "m", "ok", false, "name", "bee"),
		om("price", 5, "title", "z", "ok", true),
		om("title", ""),
	}
	doc := om("items", items)

	assertQuery(t, doc, "$.items[?(@.price < 10)].title", []interface{}{"a", "z"})
	assertQuery(t, doc, "$.items[?(@.price<=5)].price", []interface{}{5})
	assertQuery(t, doc, "$.items[?(@.price > 8)].price", []interface{}{12})
	assertQuery(t, doc, "$.items[?(@.price >= 12)].title", []interface{}{"m"})
	assertQuery(t, doc, "$.items[?(@.title == 'm')].price", []interface{}{12})
	assertQuery(t, doc, `$.items[?(@.title != "a")].title`, []interface{}{"m", "z", ""})
	assertQuery(t, doc, "$.items[?(@.ok == true)].title", []interface{}{"a", "z"})
	assertQuery(t, doc, "$.items[?(@.ok == false)].title", []interface{}{"m"})
	assertQuery(t, doc, "$.items[?(@.name == null)].title", []interface{}{"a"})
	assertQuery(t, doc, "$.items[?(@.name != null)].title", []interface{}{"m"})
	assertQuery(t, doc, "$.items[?(@.title)].title", []interface{}{"a", "m", "z"})
	assertQuery(t, doc, "$.items[?(@.title > 'a')].title", []interface{}{"m", "z"})

	nested := om("rows", []interface{}{
		om("user", om("name", "ann"), "tags", []interface{}{"x", "y"}),
		om("user", om("name", "bob"), "tags", []interface{}{"z"}),
	})
	assertQuery(t, nested, "$.rows[?(@.user.name == 'bob')].user.name", []interface{}{"bob"})
	assertQuery(t, nested, "$.rows[?(@.tags[0] == 'x')].user.name", []interface{}{"ann"})
	assertQuery(t, nested, "$.rows[?(@.tags[-1] == 'y')].user.name", []interface{}{"ann"})
}

func TestQueryLogicalPrecedence(t *testing.T) {
	items := []interface{}{
		om("a", 1, "b", 0, "c", 1),
		om("a", 0, "b", 1, "c", 1),
		om("a", 0, "b", 1, "c", 0),
	}
	doc := om("items", items)
	path := "$.items[?(@.a == 1 || @.b == 1 && @.c == 0)].a"
	assertQuery(t, doc, path, []interface{}{1, 0})

	grouped := "$.items[?((@.a == 1 || @.b == 1) && @.c == 1)].a"
	assertQuery(t, doc, grouped, []interface{}{1, 0})

	spaced := "$.items[?( @.a == 1 && @.c == 1 )].b"
	assertQuery(t, doc, spaced, []interface{}{0})
	assertQuery(t, doc, "$.items[?(@.a==1||@.b==1&&@.c==0)].a", []interface{}{1, 0})
	assertQuery(t, doc, "$.items[?(@.a==0&&@.b==1)].c", []interface{}{1, 0})
}

func TestQueryTruthiness(t *testing.T) {
	emptyMap := orderedmap.NewMap()
	fullMap := om("k", 1)
	doc := []interface{}{
		nil,
		false,
		true,
		0,
		int64(0),
		float64(0),
		1,
		float64(2),
		"",
		"x",
		[]interface{}{},
		[]interface{}{1},
		emptyMap,
		fullMap,
	}
	want := []interface{}{true, 1, float64(2), "x", []interface{}{1}, fullMap}
	assertQuery(t, doc, "$[?(@)]", want)
}

func TestQueryLength(t *testing.T) {
	doc := om(
		"arr", []interface{}{1, 2, 3},
		"name", "abcd",
		"child", om("a", 1, "b", 2),
	)
	assertQuery(t, doc, "$.arr.length()", []interface{}{3})
	assertQuery(t, doc, "$.name.length()", []interface{}{4})
	assertQuery(t, doc, "$.child.length()", []interface{}{2})
	assertQuery(t, doc, "$.length()", []interface{}{3})

	nums := []interface{}{1, 2, 3, 4}
	assertQuery(t, nums, "$[?(@ > 2)]", []interface{}{3, 4})

	rows := []interface{}{
		om("name", "ab", "tags", []interface{}{"a"}),
		om("name", "abcd", "tags", []interface{}{"a", "b", "c"}),
	}
	assertQuery(t, om("rows", rows), "$.rows[?(@.name.length() > 2)].name", []interface{}{"abcd"})
	assertQuery(t, om("rows", rows), "$.rows[?(@.tags.length() >= 3)].name", []interface{}{"abcd"})
	assertQuery(t, om("rows", rows), "$.rows[?(@.tags.length() == 1)].tags", []interface{}{[]interface{}{"a"}})

	got, err := orderedmap.Query(doc, "$.arr.length()")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[0].(int); !ok {
		t.Fatalf("length type = %T, want int", got[0])
	}
}

func TestQueryScript(t *testing.T) {
	arr := []interface{}{"a", "b", "c"}
	assertQuery(t, arr, "$[(@.length-1)]", []interface{}{"c"})
	assertQuery(t, arr, "$[(@.length - 2)]", []interface{}{"b"})
	assertQuery(t, arr, "$[(@.length-3)]", []interface{}{"a"})
	assertQuery(t, arr, "$[( @.length - 1 )]", []interface{}{"c"})
	assertQuery(t, arr, "$[(@.length()-1)]", []interface{}{"c"})
	assertQuery(t, arr, "$[(\t@.length\t-\t1\t)]", []interface{}{"c"})
	assertEmpty(t, arr, "$[(@.length-4)]")
	assertEmpty(t, []interface{}{}, "$[(@.length-1)]")

	doc := om("items", []interface{}{
		om("name", "first"),
		om("name", "last"),
	})
	assertQuery(t, doc, "$.items[(@.length-1)].name", []interface{}{"last"})
}

func TestQueryOne(t *testing.T) {
	doc := om("a", []interface{}{om("n", 1), om("n", 2)})
	got, ok, err := orderedmap.QueryOne(doc, "$.a[*].n")
	if err != nil || !ok || got != 1 {
		t.Fatalf("QueryOne = (%#v, %v, %v)", got, ok, err)
	}
	got, ok, err = orderedmap.QueryOne(doc, "$.missing")
	if err != nil || ok || got != nil {
		t.Fatalf("missing QueryOne = (%#v, %v, %v)", got, ok, err)
	}
	_, _, err = orderedmap.QueryOne(doc, "nope")
	if err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestSyntaxError(t *testing.T) {
	cases := []struct {
		path string
		pos  int
	}{
		{path: "", pos: 0},
		{path: "foo", pos: 0},
		{path: ".a", pos: 0},
		{path: "$.", pos: 2},
		{path: "$.a[", pos: 3},
		{path: "$..", pos: 3},
		{path: "$['abc]", pos: 2},
	}
	for _, tc := range cases {
		_, err := orderedmap.Query(om("a", 1), tc.path)
		var se *orderedmap.SyntaxError
		if !errors.As(err, &se) {
			t.Fatalf("Query(%q) err = %v, want *SyntaxError", tc.path, err)
		}
		if se.Position != tc.pos {
			t.Fatalf("Query(%q) position = %d, want %d (%s)", tc.path, se.Position, tc.pos, se.Message)
		}
		if se.Message == "" {
			t.Fatalf("Query(%q) empty message", tc.path)
		}
		want := "syntax error at position " + strconv.Itoa(se.Position) + ": " + se.Message
		if se.Error() != want {
			t.Fatalf("Error() = %q, want %q", se.Error(), want)
		}
	}

	_, err := orderedmap.Query(nil, "$.a[?(@.b == )]")
	var se *orderedmap.SyntaxError
	if !errors.As(err, &se) {
		t.Fatal(err)
	}
	if se.Position != 13 {
		t.Fatalf("filter position = %d (%s)", se.Position, se.Message)
	}
}

func TestQueryJSONNumbers(t *testing.T) {
	var doc interface{}
	raw := []byte(`{"books":[{"title":"a","price":8.5},{"title":"b","price":12}]}`)
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	assertQuery(t, doc, "$.books[?(@.price < 10)].title", []interface{}{"a"})
	assertQuery(t, doc, "$.books[(@.length-1)].title", []interface{}{"b"})
	assertQuery(t, doc, "$.books.length()", []interface{}{2})
	assertQuery(t, doc, "$..title", []interface{}{"a", "b"})
}

func TestQueryEdges(t *testing.T) {
	assertQuery(t, orderedmap.NewMap(), "$.length()", []interface{}{0})
	assertQuery(t, []interface{}{}, "$.length()", []interface{}{0})
	assertQuery(t, "", "$.length()", []interface{}{0})
	assertQuery(t, []interface{}{[]interface{}{"x", "y"}}, "$[0][1]", []interface{}{"y"})

	nums := om("n", -3)
	assertQuery(t, []interface{}{nums, om("n", 0), om("n", 4)}, "$[?(@.n<=-1)].n", []interface{}{-3})
	assertQuery(t, []interface{}{om("n", 15)}, "$[?(@.n == 1.5e1)].n", []interface{}{15})
	assertQuery(t, om("rows", []interface{}{
		om("tags", []interface{}{"ab", "c"}),
	}), "$.rows[?(@.tags[0].length()==2)].tags[1]", []interface{}{"c"})
}
