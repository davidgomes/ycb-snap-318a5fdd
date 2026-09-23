/*
Copyright The Helm Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chart "helm.sh/helm/v4/pkg/chart/v2"
)

func TestExtractMergeStrategies(t *testing.T) {
	got := ExtractMergeStrategies(map[string]string{
		"helm.sh/merge-strategy/items":       "append",
		"helm.sh/merge-strategy/needskey":    "merge",
		"helm.sh/merge-strategy/keyed":       " merge ",
		"helm.sh/merge-key/keyed":            " id.name ",
		"helm.sh/merge-strategy/nope":        "replace",
		"helm.sh/merge-strategy/":            "append",
		"helm.sh/merge-strategy/.bad":        "append",
		"helm.sh/merge-strategy/also.bad.":   "append",
		"helm.sh/merge-strategy/nested.list": "append",
		"unrelated":                          "append",
	})

	require.Len(t, got, 4)
	assert.Equal(t, ArrayStrategy{Path: "items", Strategy: StrategyAppend}, got["items"])
	assert.Equal(t, ArrayStrategy{Path: "needskey", Strategy: StrategyAppend}, got["needskey"])
	assert.Equal(t, ArrayStrategy{Path: "keyed", Strategy: StrategyMerge, MergeKey: "id.name"}, got["keyed"])
	assert.Equal(t, ArrayStrategy{Path: "nested.list", Strategy: StrategyAppend}, got["nested.list"])
	assert.Nil(t, ExtractMergeStrategies(nil))
}

func TestOverlayMergeAnnotationsPrecedence(t *testing.T) {
	base := map[string]string{
		"helm.sh/merge-strategy/items": "append",
		"helm.sh/merge-key/items":      "id",
		"keep":                         "yes",
	}
	got := OverlayMergeAnnotations(base, []string{" items = merge ", "bad", "=nope", "also="}, []string{"items=name"})
	assert.Equal(t, "yes", got["keep"])
	assert.Equal(t, "merge", got["helm.sh/merge-strategy/items"])
	assert.Equal(t, "name", got["helm.sh/merge-key/items"])
	assert.Equal(t, "append", base["helm.sh/merge-strategy/items"])

	strategies := ExtractMergeStrategies(got)
	assert.Equal(t, StrategyMerge, strategies["items"].Strategy)
	assert.Equal(t, "name", strategies["items"].MergeKey)
}

func TestValidateMergeStrategyAnnotations(t *testing.T) {
	values := map[string]any{
		"items": []any{"a"},
		"name":  "chart",
		"obj":   map[string]any{"k": "v"},
	}
	errs := ValidateMergeStrategyAnnotations(map[string]string{
		"helm.sh/merge-strategy/missing": "append",
		"helm.sh/merge-strategy/name":    "append",
		"helm.sh/merge-strategy/items":   "nope",
		"helm.sh/merge-strategy/obj":     "merge",
		"helm.sh/merge-key/orphan":       "name",
	}, values)

	assertMergeWarning(t, errs, "unsupported", "items")
	assertMergeWarning(t, errs, "obj", "merge-key")
	assertMergeWarning(t, errs, "orphan")
	assertMergeWarning(t, errs, "missing", "not found")
	assertMergeWarning(t, errs, "name", "non-array")
	assert.Nil(t, ValidateMergeStrategyAnnotations(nil, values))
}

func assertMergeWarning(t *testing.T, errs []error, parts ...string) {
	t.Helper()
	for _, err := range errs {
		msg := err.Error()
		ok := true
		for _, part := range parts {
			if !strings.Contains(msg, part) {
				ok = false
				break
			}
		}
		if ok {
			return
		}
	}
	t.Errorf("no warning containing %q in %v", parts, errs)
}

func TestCoalesceAppendAndNull(t *testing.T) {
	ch := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items":      "append",
				"helm.sh/merge-strategy/spec.ports": "append",
				"helm.sh/merge-strategy/untouched":  "append",
			},
		},
		Values: map[string]any{
			"items": []any{"default"},
			"spec": map[string]any{
				"ports": []any{"80"},
				"keep":  "chart",
			},
			"untouched": []any{"stay"},
			"replaced":  []any{"chart"},
			"dropped":   "chart",
		},
	}
	originalItem := ch.Values["items"].([]any)[0]

	vals, err := CoalesceValues(ch, map[string]any{
		"items": []any{"user"},
		"spec": map[string]any{
			"ports": []string{"443"},
		},
		"replaced": []any{"user"},
		"dropped":  nil,
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"default", "user"}, vals["items"])
	assert.Equal(t, []any{"80", "443"}, vals["spec"].(map[string]any)["ports"])
	assert.Equal(t, "chart", vals["spec"].(map[string]any)["keep"])
	assert.Equal(t, []any{"stay"}, vals["untouched"])
	assert.Equal(t, []any{"user"}, vals["replaced"])
	assert.NotContains(t, vals, "dropped")
	assert.Equal(t, "default", originalItem)
	assert.Equal(t, []any{"default"}, ch.Values["items"])

	vals["items"].([]any)[0] = "mutated"
	assert.Equal(t, "default", ch.Values["items"].([]any)[0])

	nullVals, err := CoalesceValues(ch, map[string]any{"items": nil, "keep": "x"})
	require.NoError(t, err)
	assert.NotContains(t, nullVals, "items")
	assert.Equal(t, "x", nullVals["keep"])

	merged, err := MergeValues(ch, map[string]any{"items": nil})
	require.NoError(t, err)
	assert.Nil(t, merged["items"])
}

func TestCoalesceMergeByKey(t *testing.T) {
	ch := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      "id.name",
			},
		},
		Values: map[string]any{
			"items": []any{
				map[string]any{"id": map[string]any{"name": "a"}, "keep": "default", "drop": "gone", "nested": map[string]any{"x": 1, "y": 1}},
				"plain",
				map[string]any{"note": "no-key"},
				map[string]any{"id": map[string]any{"name": "b"}, "keep": "b"},
			},
		},
	}

	user := map[string]any{
		"items": []any{
			map[string]any{"id": map[string]any{"name": "a"}, "drop": nil, "nested": map[string]any{"y": 2}},
			"user-plain",
			map[string]any{"other": 1},
			map[string]any{"id": map[string]any{"name": "c"}, "keep": "c"},
		},
	}
	vals, err := CoalesceValues(ch, user)
	require.NoError(t, err)

	items := vals["items"].([]any)
	require.Len(t, items, 7)
	matched := items[0].(map[string]any)
	assert.Equal(t, map[string]any{"name": "a"}, matched["id"])
	assert.Equal(t, "default", matched["keep"])
	assert.NotContains(t, matched, "drop")
	assert.Equal(t, map[string]any{"x": 1, "y": 2}, matched["nested"])
	assert.Equal(t, "plain", items[1])
	assert.Equal(t, map[string]any{"note": "no-key"}, items[2])
	assert.Equal(t, map[string]any{"id": map[string]any{"name": "b"}, "keep": "b"}, items[3])
	assert.Equal(t, "user-plain", items[4])
	assert.Equal(t, map[string]any{"other": 1}, items[5])
	assert.Equal(t, map[string]any{"id": map[string]any{"name": "c"}, "keep": "c"}, items[6])

	// Chart defaults are not mutated by the merge.
	assert.Equal(t, "gone", ch.Values["items"].([]any)[0].(map[string]any)["drop"])

	preserved, err := MergeValues(ch, map[string]any{
		"items": []any{
			map[string]any{"id": map[string]any{"name": "a"}, "drop": nil},
		},
	})
	require.NoError(t, err)
	assert.Nil(t, preserved["items"].([]any)[0].(map[string]any)["drop"])
}

func TestCoalesceMergeKeyNumericEquality(t *testing.T) {
	ch := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      "id",
			},
		},
		Values: map[string]any{
			"items": []any{map[string]any{"id": 1, "from": "chart"}},
		},
	}
	vals, err := CoalesceValues(ch, map[string]any{
		"items": []any{map[string]any{"id": float64(1), "from": "user"}},
	})
	require.NoError(t, err)
	items := vals["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "user", items[0].(map[string]any)["from"])
}

func TestCoalesceStrategiesAreChartScoped(t *testing.T) {
	sub := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "sub",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "append",
			},
		},
		Values: map[string]any{"items": []any{"sub-default"}},
	}
	parent := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "parent",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "append",
			},
		},
		Values: map[string]any{"items": []any{"parent-default"}},
	}
	parent.AddDependency(sub)

	vals, err := CoalesceValues(parent, map[string]any{
		"items": []any{"parent-user"},
		"sub": map[string]any{
			"items": []any{"sub-user"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"parent-default", "parent-user"}, vals["items"])
	subVals := vals["sub"].(map[string]any)
	assert.Equal(t, []any{"sub-default", "sub-user"}, subVals["items"])
}

func TestCoalesceGlobalStrategyOnSubchart(t *testing.T) {
	sub := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "sub",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/global.tokens": "append",
			},
		},
		Values: map[string]any{
			"global": map[string]any{"tokens": []any{"sub"}},
		},
	}
	parent := &chart.Chart{
		Metadata: &chart.Metadata{Name: "parent"},
		Values:   map[string]any{"global": map[string]any{"tokens": []any{"parent"}}},
	}
	parent.AddDependency(sub)

	vals, err := CoalesceValues(parent, map[string]any{
		"global": map[string]any{"tokens": []any{"user"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"user"}, vals["global"].(map[string]any)["tokens"])
	subGlobal := vals["sub"].(map[string]any)["global"].(map[string]any)
	assert.Equal(t, []any{"sub", "user"}, subGlobal["tokens"])

	parent.Metadata.Annotations = map[string]string{
		"helm.sh/merge-strategy/global.tokens": "append",
	}
	both, err := CoalesceValues(parent, map[string]any{
		"global": map[string]any{"tokens": []any{"user"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"parent", "user"}, both["global"].(map[string]any)["tokens"])
	bothSub := both["sub"].(map[string]any)["global"].(map[string]any)
	assert.Equal(t, []any{"sub", "parent", "user"}, bothSub["tokens"])
}

func TestWithChartMergeOverridesAndReset(t *testing.T) {
	ch := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "append",
			},
		},
		Values: map[string]any{
			"items": []any{
				map[string]any{"name": "a", "v": "default"},
				map[string]any{"name": "b", "v": "default"},
			},
		},
	}
	user := map[string]any{
		"items": []any{
			map[string]any{"name": "a", "v": "user"},
			map[string]any{"name": "c", "v": "user"},
		},
	}

	restore := WithChartMergeOverrides(ch, []string{"items=merge"}, []string{"items=name"})
	overridden, err := CoalesceValues(ch, user)
	require.NoError(t, err)
	assert.Equal(t, []any{
		map[string]any{"name": "a", "v": "user"},
		map[string]any{"name": "b", "v": "default"},
		map[string]any{"name": "c", "v": "user"},
	}, overridden["items"])
	restore()
	assert.Equal(t, "append", ch.Metadata.Annotations["helm.sh/merge-strategy/items"])
	_, hasKey := ch.Metadata.Annotations["helm.sh/merge-key/items"]
	assert.False(t, hasKey)

	suppressed := WithoutChartMergeStrategies(ch)
	reset, err := CoalesceValues(ch, user)
	require.NoError(t, err)
	assert.Equal(t, user["items"], reset["items"])
	suppressed()
	assert.Equal(t, "append", ch.Metadata.Annotations["helm.sh/merge-strategy/items"])

	again, err := CoalesceValues(ch, user)
	require.NoError(t, err)
	assert.Equal(t, []any{
		map[string]any{"name": "a", "v": "default"},
		map[string]any{"name": "b", "v": "default"},
		map[string]any{"name": "a", "v": "user"},
		map[string]any{"name": "c", "v": "user"},
	}, again["items"])
}

func TestUpgradeStrategySequences(t *testing.T) {
	ann := map[string]string{"helm.sh/merge-strategy/items": "append"}
	oldChart := &chart.Chart{
		Metadata: &chart.Metadata{Name: "app", Annotations: ann},
		Values:   map[string]any{"items": []any{"default"}},
	}
	newChart := &chart.Chart{
		Metadata: &chart.Metadata{Name: "app", Annotations: ann},
		Values:   map[string]any{"items": []any{"default"}, "extra": "from-chart"},
	}
	oldConfig := map[string]any{"items": []any{"old"}}
	newVals := map[string]any{"items": []any{"new"}}

	// ReuseValues: old config before new values, then chart defaults are the coalesce base.
	reused := CoalesceTablesWithStrategies(newVals, oldConfig, newChart)
	assert.Equal(t, []any{"old", "new"}, reused["items"])
	oldCoalesced, err := CoalesceValues(oldChart, oldConfig)
	require.NoError(t, err)
	newChart.Values = oldCoalesced
	RestoreStrategyBasePaths(newChart.Values, oldChart.Values, newChart)
	rendered, err := CoalesceValues(newChart, reused)
	require.NoError(t, err)
	assert.Equal(t, []any{"default", "old", "new"}, rendered["items"])

	// ResetThenReuseValues keeps the new chart defaults as the base.
	fresh := &chart.Chart{
		Metadata: &chart.Metadata{Name: "app", Annotations: ann},
		Values:   map[string]any{"items": []any{"default"}, "extra": "from-chart"},
	}
	resetReuse := CoalesceTablesWithStrategies(map[string]any{"items": []any{"new"}}, oldConfig, fresh)
	rendered, err = CoalesceValues(fresh, resetReuse)
	require.NoError(t, err)
	assert.Equal(t, []any{"default", "old", "new"}, rendered["items"])
	assert.Equal(t, "from-chart", rendered["extra"])
	assert.Equal(t, []any{"default"}, fresh.Values["items"])
}
