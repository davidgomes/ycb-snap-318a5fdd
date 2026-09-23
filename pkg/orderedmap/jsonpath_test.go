// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap_test

import (
	"errors"
	"testing"

	"carvel.dev/ytt/pkg/orderedmap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// m builds an ordered map from alternating keys and values.
func m(kvs ...interface{}) *orderedmap.Map {
	result := orderedmap.NewMap()
	for i := 0; i < len(kvs); i += 2 {
		result.Set(kvs[i], kvs[i+1])
	}
	return result
}

func list(items ...interface{}) []interface{} {
	return append([]interface{}{}, items...)
}

func storeDoc() *orderedmap.Map {
	return m(
		"store", m(
			"book", list(
				m("category", "reference", "author", "Nigel Rees", "title", "Sayings of the Century", "price", 8.95),
				m("category", "fiction", "author", "Evelyn Waugh", "title", "Sword of Honour", "price", 12.99),
				m("category", "fiction", "author", "Herman Melville", "title", "Moby Dick", "isbn", "0-553-21311-3", "price", 8.99),
				m("category", "fiction", "author", "J. R. R. Tolkien", "title", "The Lord of the Rings", "isbn", "0-395-19395-8", "price", 22.99),
			),
			"bicycle", m("color", "red", "price", 19.95),
		),
		"expensive", 10,
	)
}

type queryCase struct {
	desc     string
	doc      interface{}
	path     string
	expected []interface{}
}

func runQueryCases(t *testing.T, cases []queryCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.desc+" "+tc.path, func(t *testing.T) {
			results, err := orderedmap.Query(tc.doc, tc.path)
			require.NoError(t, err)
			require.NotNil(t, results)
			assert.Equal(t, tc.expected, results)
		})
	}
}

func TestQueryDotAndBracketNotation(t *testing.T) {
	doc := storeDoc()

	runQueryCases(t, []queryCase{
		{"root", doc, "$", list(doc)},
		{"nested keys", doc, "$.store.bicycle.color", list("red")},
		{"hyphenated key", m("my-key", 1), "$.my-key", list(1)},
		{"underscores and digits", m("a_b1", 1, "0", "zero"), "$.a_b1", list(1)},
		{"digit-only key", m("a_b1", 1, "0", "zero"), "$.0", list("zero")},
		{"unicode key", m("café", "au lait"), "$.café", list("au lait")},
		{"key named length", m("length", 5), "$.length", list(5)},
		{"missing key", doc, "$.store.missing", list()},
		{"key on array", doc, "$.store.book.title", list()},
		{"key on scalar", doc, "$.expensive.value", list()},
		{"wildcard on map", m("a", 1, "b", 2), "$.*", list(1, 2)},
		{"wildcard on array", list(1, 2), "$[*]", list(1, 2)},
		{"wildcard on scalar", 5, "$.*", list()},
		{"single quotes", doc, "$['store']['bicycle']['color']", list("red")},
		{"double quotes", doc, `$["store"]["bicycle"]["color"]`, list("red")},
		{"spaces in key", m("my key", 1), "$['my key']", list(1)},
		{"dot in key", m("a.b", 1, "a", m("b", 2)), "$['a.b']", list(1)},
		{"escaped single quote", m("it's", 1), `$['it\'s']`, list(1)},
		{"escaped double quote", m(`say "hi"`, 1), `$["say \"hi\""]`, list(1)},
		{"unescaped other quote", m(`say "hi"`, 1), `$['say "hi"']`, list(1)},
		{"escaped backslash", m(`back\slash`, 1), `$['back\\slash']`, list(1)},
		{"control escapes", m("a\tb\nc", 1), `$['a\tb\nc']`, list(1)},
		{"unicode escape", m("é", 1), `$['\u00e9']`, list(1)},
		{"surrogate pair escape", m("😀", 1), `$['\ud83d\ude00']`, list(1)},
		{"mixed notation", doc, "$.store['book'][0].author", list("Nigel Rees")},
		{"whitespace inside brackets", doc, "$[ 'store' ][ 'bicycle' ].color", list("red")},
	})
}

func TestQueryIndices(t *testing.T) {
	doc := m("list", list("a", "b", "c"), "map", m("0", "zero"), "str", "abc")

	runQueryCases(t, []queryCase{
		{"first", doc, "$.list[0]", list("a")},
		{"last", doc, "$.list[2]", list("c")},
		{"negative", doc, "$.list[-1]", list("c")},
		{"negative first", doc, "$.list[-3]", list("a")},
		{"out of range", doc, "$.list[3]", list()},
		{"negative out of range", doc, "$.list[-4]", list()},
		{"huge index", doc, "$.list[99999999999999999999]", list()},
		{"index on map", doc, "$.map[0]", list()},
		{"index on string", doc, "$.str[0]", list()},
		{"whitespace", doc, "$.list[ 1 ]", list("b")},
		{"nested arrays", list(list(1, 2), list(3, 4)), "$[1][0]", list(3)},
	})
}

func TestQueryUnions(t *testing.T) {
	doc := m("a", 1, "b", 2, "c", 3, "list", list("x", "y", "z"))

	runQueryCases(t, []queryCase{
		{"keys in given order", doc, "$['c','a']", list(3, 1)},
		{"keys with whitespace", doc, `$[ "b" , 'a' ]`, list(2, 1)},
		{"missing keys skipped", doc, "$['a','missing','b']", list(1, 2)},
		{"duplicate keys", doc, "$['a','a']", list(1, 1)},
		{"indices in given order", doc, "$.list[2,0]", list("z", "x")},
		{"negative indices", doc, "$.list[-1, 0]", list("z", "x")},
		{"out of range indices skipped", doc, "$.list[5,1]", list("y")},
		{"keys on array", doc, "$.list['a','b']", list()},
		{"indices on map", doc, "$[0,1]", list()},
	})
}

func TestQueryRecursiveDescent(t *testing.T) {
	doc := storeDoc()
	nested := m("a", list(1, 2), "b", m("c", 3))

	runQueryCases(t, []queryCase{
		{"all authors", doc, "$..author", list("Nigel Rees", "Evelyn Waugh", "Herman Melville", "J. R. R. Tolkien")},
		{"all prices", doc, "$..price", list(8.95, 12.99, 8.99, 22.99, 19.95)},
		{"below a key", doc, "$.store.bicycle..price", list(19.95)},
		{"pre-order", m("a", m("b", 1), "b", 2), "$..b", list(2, 1)},
		{"through arrays", m("x", list(m("id", 1), m("y", m("id", 2)))), "$..id", list(1, 2)},
		{"everything including root", nested, "$..*", list(nested, list(1, 2), 1, 2, m("c", 3), 3)},
		{"bracket wildcard", nested, "$..[*]", list(nested, list(1, 2), 1, 2, m("c", 3), 3)},
		{"everything below a node", nested, "$.b..*", list(m("c", 3), 3)},
		{"everything in scalar", 5, "$..*", list(5)},
		{"union of keys", m("k1", m("k2", 5), "k2", 3), "$..['k2','k1']", list(3, m("k2", 5), 5)},
		{"indices", m("a", list(1, 2), "b", m("c", list(3))), "$..[0]", list(1, 3)},
		{"then child", doc, "$..book[2].title", list("Moby Dick")},
		{"no match", doc, "$..nothing", list()},
	})
}

func TestQueryFilters(t *testing.T) {
	doc := storeDoc()
	bookList, _, err := orderedmap.QueryOne(doc, "$.store.book")
	require.NoError(t, err)
	book := bookList.([]interface{})

	runQueryCases(t, []queryCase{
		{"less than", doc, "$.store.book[?(@.price < 10)]", list(book[0], book[2])},
		{"less or equal", doc, "$.store.book[?(@.price <= 8.99)].title", list("Sayings of the Century", "Moby Dick")},
		{"greater than", doc, "$.store.book[?(@.price > 20)].title", list("The Lord of the Rings")},
		{"greater or equal", doc, "$.store.book[?(@.price >= 12.99)].title", list("Sword of Honour", "The Lord of the Rings")},
		{"equal string", doc, "$.store.book[?(@.category == 'reference')].title", list("Sayings of the Century")},
		{"equal double quoted string", doc, `$.store.book[?(@.author == "Herman Melville")].title`, list("Moby Dick")},
		{"not equal", doc, "$.store.book[?(@.category != 'fiction')].title", list("Sayings of the Century")},
		{"existence", doc, "$.store.book[?(@.isbn)].title", list("Moby Dick", "The Lord of the Rings")},
		{"root reference", doc, "$.store.book[?(@.price > $.expensive)].title", list("Sword of Honour", "The Lord of the Rings")},
		{"literal on left", doc, "$.store.book[?(10 > @.price)].title", list("Sayings of the Century", "Moby Dick")},
		{"no whitespace", doc, "$.store.book[?(@.price<9)].title", list("Sayings of the Century", "Moby Dick")},
		{"extra whitespace", doc, "$.store.book[ ?( @.price  <  9 ) ].title", list("Sayings of the Century", "Moby Dick")},
		{"without parentheses", doc, "$.store.book[?@.price < 9].title", list("Sayings of the Century", "Moby Dick")},
		{"after descent", doc, "$..book[?(@.author == 'Herman Melville')].title", list("Moby Dick")},
		{"on map values", m("a", m("x", 1), "b", m("x", 2)), "$[?(@.x > 1)]", list(m("x", 2))},
		{"on scalar", 5, "$[?(@ > 1)]", list()},
		{"current node", list(1, 5, 10), "$[?(@ >= 5)]", list(5, 10)},
		{"negative number", list(-2, -1, 0), "$[?(@ < -1)]", list(-2)},
		{"exponent", list(100, 1000), "$[?(@ > 1.5e2)]", list(1000)},
		{"true literal", list(m("on", true), m("on", false)), "$[?(@.on == true)]", list(m("on", true))},
		{"false literal", list(m("on", true), m("on", false)), "$[?(@.on == false)]", list(m("on", false))},
		{"null literal", list(m("v", nil), m("v", 1), m("w", 1)), "$[?(@.v == null)]", list(m("v", nil))},
		{"not null", list(m("v", nil), m("v", 1), m("w", 1)), "$[?(@.v != null)]", list(m("v", 1), m("w", 1))},
		{"missing never equal", list(m("v", 1), m("w", 1)), "$[?(@.v == 1)]", list(m("v", 1))},
		{"missing never ordered", list(m("v", 1), m("w", 1)), "$[?(@.v < 5)]", list(m("v", 1))},
	})
}

func TestQueryFilterPaths(t *testing.T) {
	alice := m("name", "alice", "tags", list("x", "y"), "owner", m("team", m("name", "core")))
	bob := m("name", "bob", "tags", list("z"), "owner", m("team", m("name", "infra")))
	doc := m("people", list(alice, bob), "wanted", "infra")

	runQueryCases(t, []queryCase{
		{"multi-level", doc, "$.people[?(@.owner.team.name == 'core')].name", list("alice")},
		{"bracket segments", doc, "$.people[?(@['owner']['team']['name'] == 'core')].name", list("alice")},
		{"array index", doc, "$.people[?(@.tags[0] == 'z')].name", list("bob")},
		{"negative array index", doc, "$.people[?(@.tags[-1] == 'y')].name", list("alice")},
		{"script index", doc, "$.people[?(@.tags[(@.length-1)] == 'y')].name", list("alice")},
		{"root path", doc, "$.people[?(@.owner.team.name == $.wanted)].name", list("bob")},
		{"missing intermediate", doc, "$.people[?(@.nope.team.name == 'core')].name", list()},
	})
}

func TestQueryLogicalFilters(t *testing.T) {
	items := list(
		m("id", 1, "a", 1, "b", 0, "c", 0),
		m("id", 2, "a", 0, "b", 1, "c", 1),
		m("id", 3, "a", 0, "b", 1, "c", 0),
		m("id", 4, "a", 1, "b", 1, "c", 1),
	)
	ids := func(path string) []interface{} {
		t.Helper()
		results, err := orderedmap.Query(items, path+".id")
		require.NoError(t, err)
		return results
	}

	assert.Equal(t, list(4), ids("$[?(@.a == 1 && @.b == 1)]"))
	assert.Equal(t, list(1, 2, 3, 4), ids("$[?(@.a == 1 || @.b == 1)]"))
	// && binds tighter than ||
	assert.Equal(t, list(1, 2, 4), ids("$[?(@.a == 1 || @.b == 1 && @.c == 1)]"))
	assert.Equal(t, list(2, 4), ids("$[?(@.c == 1 && @.b == 1 || @.a == 1 && @.c == 1)]"))
	assert.Equal(t, list(4), ids("$[?((@.a == 1 || @.b == 1) && @.c == 1 && @.a)]"))
	assert.Equal(t, list(2, 4), ids("$[?((@.a == 1 || @.b == 1) && @.c == 1)]"))
	assert.Equal(t, list(1, 2, 4), ids("$[?(@.a||@.c)]"))
	assert.Equal(t, list(3), ids("$[?(!@.a && !@.c)]"))
	assert.Equal(t, list(2, 3), ids("$[?(!(@.a == 1))]"))
}

func TestQueryTruthiness(t *testing.T) {
	values := list(nil, false, 0, int64(0), 0.0, "", list(), orderedmap.NewMap(), map[string]interface{}{},
		true, 1, -1, 0.5, "a", "false", list(nil), m("k", nil))

	results, err := orderedmap.Query(values, "$[?(@)]")
	require.NoError(t, err)
	assert.Equal(t, list(true, 1, -1, 0.5, "a", "false", list(nil), m("k", nil)), results)

	items := list(m("v", nil), m("v", false), m("v", 0), m("v", ""), m("v", list()), m("v", m()),
		m("v", true), m("v", 2), m("v", "s"), m("v", list(0)), m("v", m("k", 0)), m("w", 1))
	results, err = orderedmap.Query(items, "$[?(@.v)]")
	require.NoError(t, err)
	assert.Equal(t, list(m("v", true), m("v", 2), m("v", "s"), m("v", list(0)), m("v", m("k", 0))), results)
}

func TestQueryFilterValueComparison(t *testing.T) {
	runQueryCases(t, []queryCase{
		{"numbers across Go types", list(2, int64(2), 2.0, uint64(2), float32(2), "2", true), "$[?(@ == 2)]",
			list(2, int64(2), 2.0, uint64(2), float32(2))},
		{"int versus float", list(1, 2, 3), "$[?(@ > 1.5)]", list(2, 3)},
		{"strings are ordered", list("a", "b", "c", 1), "$[?(@ < 'b')]", list("a")},
		{"mixed types are not ordered", list("1", 1, true, nil), "$[?(@ >= 1)]", list(1)},
		{"strings do not equal numbers", list("1", 1), "$[?(@ == '1')]", list("1")},
		{"booleans do not equal numbers", list(true, 1), "$[?(@ == true)]", list(true)},
		{"arrays compared deeply", list(m("v", list(1, 2)), m("v", list(2, 1))), "$[?(@.v == $[0].v)]", list(m("v", list(1, 2)))},
		{"maps compared regardless of order", list(m("v", m("a", 1, "b", 2)), m("v", m("b", 2, "a", 1)), m("v", m("a", 1))),
			"$[?(@.v == $[0].v)]", list(m("v", m("a", 1, "b", 2)), m("v", m("b", 2, "a", 1)))},
	})
}

func TestQueryLength(t *testing.T) {
	doc := m("list", list(1, 2, 3), "map", m("a", 1, "b", 2), "str", "héllo", "empty", list(), "num", 42, "null", nil)

	runQueryCases(t, []queryCase{
		{"array", doc, "$.list.length()", list(3)},
		{"map", doc, "$.map.length()", list(2)},
		{"string counts characters", doc, "$.str.length()", list(5)},
		{"empty array", doc, "$.empty.length()", list(0)},
		{"root", doc, "$.length()", list(6)},
		{"bracketed key", doc, "$['list'].length()", list(3)},
		{"number", doc, "$.num.length()", list()},
		{"null", doc, "$.null.length()", list()},
		{"missing", doc, "$.missing.length()", list()},
		{"of each item", list("a", "bb", list(1, 2, 3)), "$[*].length()", list(1, 2, 3)},
	})

	results, err := orderedmap.Query(doc, "$.list.length()")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.IsType(t, 0, results[0])

	people := list(
		m("name", "al", "tags", list("x", "y")),
		m("name", "bob", "tags", list("z")),
		m("name", "carol", "tags", list()),
	)
	runQueryCases(t, []queryCase{
		{"method style in filter", people, "$[?(@.tags.length() > 1)].name", list("al")},
		{"function style in filter", people, "$[?(length(@.tags) == 1)].name", list("bob")},
		{"string length in filter", people, "$[?(@.name.length() >= 3)].name", list("bob", "carol")},
		{"length of current node", list("a", "abc"), "$[?(@.length() == 3)]", list("abc")},
		{"length of missing", people, "$[?(@.nope.length() >= 0)].name", list()},
	})
}

func TestQueryScript(t *testing.T) {
	doc := m("list", list("a", "b", "c"), "map", m("a", 1))

	runQueryCases(t, []queryCase{
		{"last", doc, "$.list[(@.length-1)]", list("c")},
		{"second to last", doc, "$.list[(@.length-2)]", list("b")},
		{"first", doc, "$.list[(@.length-3)]", list("a")},
		{"whitespace", doc, "$.list[( @.length - 2 )]", list("b")},
		{"whitespace around brackets", doc, "$.list[ (@.length-1) ]", list("c")},
		{"out of range", doc, "$.list[(@.length-4)]", list()},
		{"length itself", doc, "$.list[(@.length)]", list()},
		{"on map", doc, "$.map[(@.length-1)]", list()},
		{"after descent", m("x", list(1, 2), "y", m("z", list(3, 4))), "$..[(@.length-1)]", list(2, 4)},
	})
}

func TestQueryGoMaps(t *testing.T) {
	doc := map[string]interface{}{"b": 2, "a": map[interface{}]interface{}{"y": 3, "x": 4}}

	runQueryCases(t, []queryCase{
		{"key", doc, "$.b", list(2)},
		{"wildcard is sorted", doc, "$.a.*", list(4, 3)},
		{"length", doc, "$.a.length()", list(2)},
	})
}

func TestQueryOne(t *testing.T) {
	doc := storeDoc()

	value, found, err := orderedmap.QueryOne(doc, "$..price")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, 8.95, value)

	value, found, err = orderedmap.QueryOne(doc, "$.store.missing")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, value)

	value, found, err = orderedmap.QueryOne(m("a", nil), "$.a")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Nil(t, value)

	value, found, err = orderedmap.QueryOne(doc, "store")
	assert.Nil(t, value)
	assert.False(t, found)
	var syntaxErr *orderedmap.SyntaxError
	require.True(t, errors.As(err, &syntaxErr))
	assert.Equal(t, 0, syntaxErr.Position)
}

func TestQuerySyntaxErrors(t *testing.T) {
	cases := []struct {
		path     string
		position int
	}{
		{"", 0},
		{"store.book", 0},
		{" $", 0},
		{"$.", 2},
		{"$..", 3},
		{"$...a", 3},
		{"$a", 1},
		{"$.a b", 3},
		{"$.a.", 4},
		{"$[", 2},
		{"$[]", 2},
		{"$[0", 3},
		{"$[0,]", 4},
		{"$[1 2]", 4},
		{"$[1:2]", 3},
		{"$[a]", 2},
		{"$[*", 3},
		{"$[*,0]", 3},
		{"$['abc", 2},
		{"$['a'", 5},
		{`$['a\qb']`, 4},
		{`$['a\`, 4},
		{`$['\u12']`, 3},
		{`$['\uzzzz']`, 3},
		{"$.length(", 9},
		{"$.length(x)", 9},
		{"$[?(@.a = 1)]", 8},
		{"$[?(@.a == )]", 11},
		{"$[?(@.a == 1]", 12},
		{"$[?(@.a == 1)", 13},
		{"$[?(@.a && )]", 11},
		{"$[?(@.a & @.b)]", 8},
		{"$[?(foo)]", 4},
		{"$[?(@.a == 'x)]", 11},
		{"$[?(@.a == 1.)]", 13},
		{"$[?(@.a == -)]", 12},
		{"$[?()]", 4},
		{"$[?(@..a)]", 5},
		{"$[?(@.*)]", 5},
		{"$[?(@['a','b'])]", 5},
		{"$[?(@[?(@.a)])]", 5},
		{"$[(@.size-1)]", 4},
		{"$[(@.length*2)]", 11},
		{"$[(@.length-)]", 12},
		{"$[(@.length-1]", 13},
		{"$[(1)]", 3},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			results, err := orderedmap.Query(storeDoc(), tc.path)
			require.Error(t, err)
			assert.Nil(t, results)

			syntaxErr, ok := err.(*orderedmap.SyntaxError)
			require.True(t, ok, "expected *orderedmap.SyntaxError, got %T: %v", err, err)
			assert.Equal(t, tc.position, syntaxErr.Position, "message: %s", syntaxErr.Message)
			assert.NotEmpty(t, syntaxErr.Message)
		})
	}
}

func TestSyntaxErrorFormat(t *testing.T) {
	err := &orderedmap.SyntaxError{Message: "unexpected character", Position: 7}
	assert.Equal(t, "syntax error at position 7: unexpected character", err.Error())

	_, err2 := orderedmap.Query(nil, "$.")
	require.Error(t, err2)
	assert.Equal(t, "syntax error at position 2: unexpected end of path, expected a name or '*'", err2.Error())
}
