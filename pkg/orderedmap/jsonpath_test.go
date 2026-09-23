// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap_test

import (
	"fmt"
	"testing"

	"carvel.dev/ytt/pkg/orderedmap"
)

func TestJSONPathQuery(t *testing.T) {
	doc := orderedmap.NewMapWithItems([]orderedmap.MapItem{
		{Key: "my-key", Value: "v"},
		{Key: "items", Value: []interface{}{
			orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "n", Value: int64(1)}, {Key: "t", Value: "a"}}),
			orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "n", Value: int64(5)}, {Key: "t", Value: "b"}}),
			orderedmap.NewMapWithItems([]orderedmap.MapItem{{Key: "n", Value: 3.5}}),
		}},
	})
	cases := map[string]string{
		`$.my-key`:                             `[v]`,
		`$['my-key','items'].length()`:         `[1 3]`,
		`$.items[-1].n`:                        `[3.5]`,
		`$.items[5]`:                           `[]`,
		`$.items[2,0].n`:                       `[3.5 1]`,
		`$..n`:                                 `[1 5 3.5]`,
		`$.items[?(@.n > 2 && @.t)].t`:         `[b]`,
		`$.items[?(@.n == 1 || @.t == "b")].n`: `[1 5]`,
		`$.items[?(@.t.length() == 1)].n`:      `[1 5]`,
		`$.items[( @.length - 2 )].n`:          `[5]`,
		`$.my-key[0]`:                          `[]`,
	}
	for path, exp := range cases {
		res, err := orderedmap.Query(doc, path)
		if err != nil {
			t.Fatalf("%s: %s", path, err)
		}
		if got := fmt.Sprint(res); got != exp {
			t.Errorf("%s: got %s, want %s", path, got, exp)
		}
	}
	all, _ := orderedmap.Query(doc, `$..*`)
	if all[0] != doc {
		t.Errorf("expected root first")
	}
	if _, ok, err := orderedmap.QueryOne(doc, `$.nope`); ok || err != nil {
		t.Errorf("expected no match")
	}
	_, err := orderedmap.Query(doc, `$.items[`)
	if se, ok := err.(*orderedmap.SyntaxError); !ok || se.Error() == "" {
		t.Errorf("expected syntax error, got %v", err)
	}
}
