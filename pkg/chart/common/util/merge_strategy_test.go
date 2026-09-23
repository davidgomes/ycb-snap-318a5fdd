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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"helm.sh/helm/v4/internal/copystructure"
	chart "helm.sh/helm/v4/pkg/chart/v2"
)

func TestMergeStrategiesFromAnnotationsExtractsActionableStrategies(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    map[string]MergeStrategy
	}{
		{
			name:        "no annotations",
			annotations: nil,
			expected:    map[string]MergeStrategy{},
		},
		{
			name: "append",
			annotations: map[string]string{
				"helm.sh/merge-strategy/items": "append",
			},
			expected: map[string]MergeStrategy{
				"items": {Strategy: MergeStrategyAppend},
			},
		},
		{
			name: "merge with merge key",
			annotations: map[string]string{
				"helm.sh/merge-strategy/spec.containers": "merge",
				"helm.sh/merge-key/spec.containers":      "metadata.name",
			},
			expected: map[string]MergeStrategy{
				"spec.containers": {Strategy: MergeStrategyMerge, MergeKey: "metadata.name"},
			},
		},
		{
			name: "merge without merge key is append",
			annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/other":      "name",
			},
			expected: map[string]MergeStrategy{
				"items": {Strategy: MergeStrategyAppend},
			},
		},
		{
			name: "merge with empty merge key is append",
			annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      " ",
			},
			expected: map[string]MergeStrategy{
				"items": {Strategy: MergeStrategyAppend},
			},
		},
		{
			name: "unsupported strategies and invalid paths are excluded",
			annotations: map[string]string{
				"helm.sh/merge-strategy/unsupported": "prepend",
				"helm.sh/merge-strategy/":            "append",
				"helm.sh/merge-strategy/a..b":        "append",
				"helm.sh/merge-strategy/.a":          "append",
				"helm.sh/merge-strategy/a.":          "append",
				"helm.sh/merge-key/items":            "name",
				"example.com/unrelated":              "append",
			},
			expected: map[string]MergeStrategy{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, MergeStrategiesFromAnnotations(tt.annotations))
		})
	}
}

func newMergeStrategyTestChart(name string, annotations map[string]string, values map[string]any, deps ...*chart.Chart) *chart.Chart {
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: name, Annotations: annotations},
		Values:   values,
	}
	c.AddDependency(deps...)
	return c
}

func TestCoalesceValuesAppliesMergeStrategies(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		defaults    map[string]any
		vals        map[string]any
		merge       bool
		expected    map[string]any
	}{
		{
			name:        "append puts chart defaults before user elements",
			annotations: map[string]string{"helm.sh/merge-strategy/items": "append"},
			defaults:    map[string]any{"items": []any{"a", "b"}},
			vals:        map[string]any{"items": []any{"c", "a"}},
			expected:    map[string]any{"items": []any{"a", "b", "c", "a"}},
		},
		{
			name:        "arrays without a strategy are replaced",
			annotations: map[string]string{"helm.sh/merge-strategy/other": "append"},
			defaults:    map[string]any{"items": []any{"a", "b"}, "other": []any{"x"}},
			vals:        map[string]any{"items": []any{"c"}},
			expected:    map[string]any{"items": []any{"c"}, "other": []any{"x"}},
		},
		{
			name:        "chart defaults are kept when the user sets nothing",
			annotations: map[string]string{"helm.sh/merge-strategy/items": "append"},
			defaults:    map[string]any{"items": []any{"a"}},
			vals:        map[string]any{},
			expected:    map[string]any{"items": []any{"a"}},
		},
		{
			name:        "append at a nested path",
			annotations: map[string]string{"helm.sh/merge-strategy/config.env": "append"},
			defaults:    map[string]any{"config": map[string]any{"env": []any{"A=1"}, "debug": false}},
			vals:        map[string]any{"config": map[string]any{"env": []any{"B=2"}}},
			expected:    map[string]any{"config": map[string]any{"env": []any{"A=1", "B=2"}, "debug": false}},
		},
		{
			name: "merge matches objects by key",
			annotations: map[string]string{
				"helm.sh/merge-strategy/containers": "merge",
				"helm.sh/merge-key/containers":      "name",
			},
			defaults: map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": "app:1", "resources": map[string]any{"cpu": "1", "memory": "1Gi"}},
				map[string]any{"name": "sidecar", "image": "sidecar:1"},
			}},
			vals: map[string]any{"containers": []any{
				map[string]any{"name": "extra", "image": "extra:1"},
				map[string]any{"name": "app", "image": "app:2", "resources": map[string]any{"cpu": "2"}},
			}},
			expected: map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": "app:2", "resources": map[string]any{"cpu": "2", "memory": "1Gi"}},
				map[string]any{"name": "sidecar", "image": "sidecar:1"},
				map[string]any{"name": "extra", "image": "extra:1"},
			}},
		},
		{
			name: "merge key can be a dotted path",
			annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      "metadata.name",
			},
			defaults: map[string]any{"items": []any{
				map[string]any{"metadata": map[string]any{"name": "a"}, "value": 1},
				map[string]any{"metadata": map[string]any{"name": "b"}, "value": 2},
			}},
			vals: map[string]any{"items": []any{
				map[string]any{"metadata": map[string]any{"name": "b", "labels": map[string]any{"x": "y"}}, "value": 3},
			}},
			expected: map[string]any{"items": []any{
				map[string]any{"metadata": map[string]any{"name": "a"}, "value": 1},
				map[string]any{"metadata": map[string]any{"name": "b", "labels": map[string]any{"x": "y"}}, "value": 3},
			}},
		},
		{
			name: "merge keys match across numeric types",
			annotations: map[string]string{
				"helm.sh/merge-strategy/ports": "merge",
				"helm.sh/merge-key/ports":      "port",
			},
			defaults: map[string]any{"ports": []any{map[string]any{"port": float64(80), "name": "http"}}},
			vals:     map[string]any{"ports": []any{map[string]any{"port": int64(80), "name": "web"}}},
			expected: map[string]any{"ports": []any{map[string]any{"port": int64(80), "name": "web"}}},
		},
		{
			name: "merge preserves non-map elements and elements missing the key",
			annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      "name",
			},
			defaults: map[string]any{"items": []any{
				"plain",
				map[string]any{"name": "a", "value": 1},
				map[string]any{"value": "no key"},
				nil,
			}},
			vals: map[string]any{"items": []any{
				map[string]any{"name": "a", "value": 2},
				"user plain",
				map[string]any{"other": "no key"},
				map[string]any{"name": nil},
			}},
			expected: map[string]any{"items": []any{
				"plain",
				map[string]any{"name": "a", "value": 2},
				map[string]any{"value": "no key"},
				nil,
				"user plain",
				map[string]any{"other": "no key"},
				map[string]any{"name": nil},
			}},
		},
		{
			name:        "merge without merge key appends",
			annotations: map[string]string{"helm.sh/merge-strategy/items": "merge"},
			defaults:    map[string]any{"items": []any{map[string]any{"name": "a"}}},
			vals:        map[string]any{"items": []any{map[string]any{"name": "a"}}},
			expected:    map[string]any{"items": []any{map[string]any{"name": "a"}, map[string]any{"name": "a"}}},
		},
		{
			name: "null user fields in matched elements are removed when coalescing",
			annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      "name",
			},
			defaults: map[string]any{"items": []any{map[string]any{"name": "a", "drop": "me", "keep": true}}},
			vals:     map[string]any{"items": []any{map[string]any{"name": "a", "drop": nil}}},
			expected: map[string]any{"items": []any{map[string]any{"name": "a", "keep": true}}},
		},
		{
			name: "null user fields in matched elements are preserved when merging",
			annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      "name",
			},
			defaults: map[string]any{"items": []any{map[string]any{"name": "a", "drop": "me", "keep": true}}},
			vals:     map[string]any{"items": []any{map[string]any{"name": "a", "drop": nil}}},
			merge:    true,
			expected: map[string]any{"items": []any{map[string]any{"name": "a", "drop": nil, "keep": true}}},
		},
		{
			name:        "null user array deletes the key when coalescing",
			annotations: map[string]string{"helm.sh/merge-strategy/items": "append"},
			defaults:    map[string]any{"items": []any{"a"}, "other": 1},
			vals:        map[string]any{"items": nil},
			expected:    map[string]any{"other": 1},
		},
		{
			name:        "null user array is preserved when merging",
			annotations: map[string]string{"helm.sh/merge-strategy/items": "append"},
			defaults:    map[string]any{"items": []any{"a"}},
			vals:        map[string]any{"items": nil},
			merge:       true,
			expected:    map[string]any{"items": nil},
		},
		{
			name:        "non-array user value is left to regular coalescing",
			annotations: map[string]string{"helm.sh/merge-strategy/items": "append"},
			defaults:    map[string]any{"items": []any{"a"}},
			vals:        map[string]any{"items": "scalar"},
			expected:    map[string]any{"items": "scalar"},
		},
		{
			name:        "typed slices are combined",
			annotations: map[string]string{"helm.sh/merge-strategy/items": "append"},
			defaults:    map[string]any{"items": []string{"a"}},
			vals:        map[string]any{"items": []string{"b"}},
			expected:    map[string]any{"items": []any{"a", "b"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newMergeStrategyTestChart("parent", tt.annotations, tt.defaults)
			defaultsCopy, err := copystructure.Copy(tt.defaults)
			require.NoError(t, err)

			coalesceFn := CoalesceValues
			if tt.merge {
				coalesceFn = MergeValues
			}
			v, err := coalesceFn(c, tt.vals)
			require.NoError(t, err)

			assert.Equal(t, tt.expected, map[string]any(v))
			assert.Equal(t, defaultsCopy, c.Values, "chart default values must not be modified")
		})
	}
}

func TestCoalesceValuesMergeStrategiesAreChartScoped(t *testing.T) {
	sub := newMergeStrategyTestChart("sub", nil, map[string]any{"items": []any{"sub-default"}})
	parent := newMergeStrategyTestChart("parent",
		map[string]string{"helm.sh/merge-strategy/items": "append"},
		map[string]any{"items": []any{"parent-default"}},
		sub,
	)

	v, err := CoalesceValues(parent, map[string]any{
		"items": []any{"user"},
		"sub":   map[string]any{"items": []any{"user-sub"}},
	})
	require.NoError(t, err)

	assert.Equal(t, []any{"parent-default", "user"}, v["items"])
	assert.Equal(t, []any{"user-sub"}, v["sub"].(map[string]any)["items"], "parent strategies must not apply to subcharts")
}

func TestCoalesceValuesSubchartMergeStrategiesApplyToSubchartValues(t *testing.T) {
	sub := newMergeStrategyTestChart("sub",
		map[string]string{"helm.sh/merge-strategy/items": "append"},
		map[string]any{"items": []any{"sub-default"}},
	)
	parent := newMergeStrategyTestChart("parent", nil,
		map[string]any{
			"items": []any{"parent-default"},
			"sub":   map[string]any{"items": []any{"parent-override"}},
		},
		sub,
	)

	v, err := CoalesceValues(parent, map[string]any{"items": []any{"user"}})
	require.NoError(t, err)
	assert.Equal(t, []any{"user"}, v["items"])
	assert.Equal(t, []any{"sub-default", "parent-override"}, v["sub"].(map[string]any)["items"])

	v, err = CoalesceValues(parent, map[string]any{"sub": map[string]any{"items": []any{"user-sub"}}})
	require.NoError(t, err)
	assert.Equal(t, []any{"sub-default", "user-sub"}, v["sub"].(map[string]any)["items"])
}

func TestCoalesceValuesSubchartGlobalMergeStrategies(t *testing.T) {
	newCharts := func(annotations map[string]string) *chart.Chart {
		sub := newMergeStrategyTestChart("sub", annotations, map[string]any{
			"global": map[string]any{
				"hosts":  []any{"sub-default"},
				"nested": map[string]any{"hosts": []any{"sub-nested-default"}},
			},
		})
		return newMergeStrategyTestChart("parent", nil, map[string]any{
			"global": map[string]any{
				"hosts":  []any{"parent-default"},
				"nested": map[string]any{"hosts": []any{"parent-nested-default"}},
			},
		}, sub)
	}
	vals := func() map[string]any {
		return map[string]any{
			"sub": map[string]any{
				"global": map[string]any{
					"hosts":  []any{"sub-scoped"},
					"nested": map[string]any{"hosts": []any{"sub-nested-scoped"}},
				},
			},
		}
	}

	t.Run("without strategies globals replace arrays", func(t *testing.T) {
		v, err := CoalesceValues(newCharts(nil), vals())
		require.NoError(t, err)

		subGlobals := v["sub"].(map[string]any)["global"].(map[string]any)
		assert.Equal(t, []any{"parent-default"}, subGlobals["hosts"])
		assert.Equal(t, []any{"parent-nested-default"}, subGlobals["nested"].(map[string]any)["hosts"])
	})

	t.Run("subchart global strategies apply when globals are merged into its scope", func(t *testing.T) {
		v, err := CoalesceValues(newCharts(map[string]string{
			"helm.sh/merge-strategy/global.hosts":        "append",
			"helm.sh/merge-strategy/global.nested.hosts": "append",
		}), vals())
		require.NoError(t, err)

		subGlobals := v["sub"].(map[string]any)["global"].(map[string]any)
		assert.Equal(t, []any{"sub-default", "sub-scoped", "parent-default"}, subGlobals["hosts"])
		assert.Equal(t, []any{"sub-nested-default", "sub-nested-scoped", "parent-nested-default"}, subGlobals["nested"].(map[string]any)["hosts"])

		parentGlobals := v["global"].(map[string]any)
		assert.Equal(t, []any{"parent-default"}, parentGlobals["hosts"], "parent globals must not be modified")
		assert.Equal(t, []any{"parent-nested-default"}, parentGlobals["nested"].(map[string]any)["hosts"], "parent globals must not be modified")
	})

	t.Run("subchart global strategies combine user globals with subchart defaults", func(t *testing.T) {
		v, err := CoalesceValues(newCharts(map[string]string{
			"helm.sh/merge-strategy/global.hosts": "append",
		}), map[string]any{"global": map[string]any{"hosts": []any{"user"}}})
		require.NoError(t, err)

		assert.Equal(t, []any{"user"}, v["global"].(map[string]any)["hosts"])
		assert.Equal(t, []any{"sub-default", "user"}, v["sub"].(map[string]any)["global"].(map[string]any)["hosts"])
	})
}

func TestCoalesceTablesWithStrategiesCombinesArrays(t *testing.T) {
	strategies := map[string]MergeStrategy{
		"items":      {Strategy: MergeStrategyAppend},
		"nested.env": {Strategy: MergeStrategyMerge, MergeKey: "name"},
	}
	src := map[string]any{
		"items":    []any{"old"},
		"replaced": []any{"old"},
		"nested": map[string]any{"env": []any{
			map[string]any{"name": "A", "value": "1"},
			map[string]any{"name": "B", "value": "2"},
		}},
	}
	dst := map[string]any{
		"items":    []any{"new"},
		"replaced": []any{"new"},
		"nested": map[string]any{"env": []any{
			map[string]any{"name": "B", "value": "3"},
			map[string]any{"name": "C", "value": "4"},
		}},
	}

	result := CoalesceTablesWithStrategies(dst, src, strategies)
	assert.Equal(t, map[string]any{
		"items":    []any{"old", "new"},
		"replaced": []any{"new"},
		"nested": map[string]any{"env": []any{
			map[string]any{"name": "A", "value": "1"},
			map[string]any{"name": "B", "value": "3"},
			map[string]any{"name": "C", "value": "4"},
		}},
	}, result)
	assert.Equal(t, []any{"old"}, src["items"])

	assert.Equal(t, map[string]any{"items": []any{"a"}}, CoalesceTablesWithStrategies(map[string]any{"items": []any{"a"}}, nil, strategies))
	assert.Equal(t, map[string]any{"items": []any{"a"}}, CoalesceTablesWithStrategies(nil, map[string]any{"items": []any{"a"}}, strategies))
	assert.Equal(t, map[string]any{}, CoalesceTablesWithStrategies(map[string]any{"items": nil}, map[string]any{"items": []any{"a"}}, strategies))
}

func TestChartMergeStrategiesPrefixesSubchartPaths(t *testing.T) {
	subsub := newMergeStrategyTestChart("subsub", map[string]string{"helm.sh/merge-strategy/items": "append"}, nil)
	sub := newMergeStrategyTestChart("sub", map[string]string{
		"helm.sh/merge-strategy/global.hosts": "append",
		"helm.sh/merge-strategy/list":         "merge",
		"helm.sh/merge-key/list":              "id",
	}, nil, subsub)
	parent := newMergeStrategyTestChart("parent", map[string]string{
		"helm.sh/merge-strategy/items":    "append",
		"helm.sh/merge-strategy/sub.list": "append",
	}, nil, sub)

	strategies, err := ChartMergeStrategies(parent)
	require.NoError(t, err)
	assert.Equal(t, map[string]MergeStrategy{
		"items":            {Strategy: MergeStrategyAppend},
		"sub.list":         {Strategy: MergeStrategyAppend},
		"sub.global.hosts": {Strategy: MergeStrategyAppend},
		"sub.subsub.items": {Strategy: MergeStrategyAppend},
	}, strategies)

	_, err = ChartMergeStrategies("not a chart")
	assert.Error(t, err)
}

func TestResetMergeStrategyValuesRestoresBase(t *testing.T) {
	strategies := map[string]MergeStrategy{
		"items":         {Strategy: MergeStrategyAppend},
		"sub.items":     {Strategy: MergeStrategyAppend},
		"sub.nullified": {Strategy: MergeStrategyAppend},
		"missing.items": {Strategy: MergeStrategyAppend},
		"created.items": {Strategy: MergeStrategyAppend},
	}
	base := map[string]any{
		"items":   []any{"default"},
		"sub":     map[string]any{"nullified": nil},
		"created": map[string]any{"items": []any{"created"}},
	}
	vals := map[string]any{
		"items": []any{"default", "user"},
		"sub": map[string]any{
			"items":     []any{"sub-default"},
			"nullified": []any{"sub-default"},
			"other":     true,
		},
	}

	ResetMergeStrategyValues(vals, base, strategies)
	assert.Equal(t, map[string]any{
		"items":   []any{"default"},
		"sub":     map[string]any{"nullified": nil, "other": true},
		"created": map[string]any{"items": []any{"created"}},
	}, vals)

	vals["items"].([]any)[0] = "changed"
	assert.Equal(t, []any{"default"}, base["items"], "base values must be copied")
}

func TestParseMergeStrategyOverridesToAnnotations(t *testing.T) {
	annotations, err := ParseMergeStrategyOverrides(
		[]string{"items=append", " spec.containers = merge "},
		[]string{"spec.containers=metadata.name"},
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"helm.sh/merge-strategy/items":           "append",
		"helm.sh/merge-strategy/spec.containers": "merge",
		"helm.sh/merge-key/spec.containers":      "metadata.name",
	}, annotations)

	for _, tt := range []struct {
		name       string
		strategies []string
		keys       []string
	}{
		{name: "missing separator", strategies: []string{"items"}},
		{name: "unsupported strategy", strategies: []string{"items=prepend"}},
		{name: "empty strategy", strategies: []string{"items="}},
		{name: "invalid strategy path", strategies: []string{"a..b=append"}},
		{name: "empty key", keys: []string{"items="}},
		{name: "invalid key path", keys: []string{"=name"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMergeStrategyOverrides(tt.strategies, tt.keys)
			assert.Error(t, err)
		})
	}
}

func TestValidateMergeStrategyAnnotationsReportsProblems(t *testing.T) {
	values := map[string]any{
		"items":  []any{"a"},
		"scalar": "value",
		"config": map[string]any{"env": []any{}},
	}

	tests := []struct {
		name        string
		annotations map[string]string
		values      map[string]any
		expected    []string
	}{
		{
			name: "valid annotations",
			annotations: map[string]string{
				"helm.sh/merge-strategy/items":      "append",
				"helm.sh/merge-strategy/config.env": "merge",
				"helm.sh/merge-key/config.env":      "name",
				"example.com/other":                 "value",
			},
			values: values,
		},
		{
			name:        "unsupported strategy",
			annotations: map[string]string{"helm.sh/merge-strategy/items": "prepend"},
			values:      values,
			expected:    []string{`merge strategy "prepend" for path "items" is unsupported`},
		},
		{
			name:        "merge without merge key",
			annotations: map[string]string{"helm.sh/merge-strategy/items": "merge"},
			values:      values,
			expected:    []string{`merge strategy "merge" for path "items" requires a merge key`},
		},
		{
			name:        "orphan merge key",
			annotations: map[string]string{"helm.sh/merge-key/items": "name"},
			values:      values,
			expected:    []string{`merge key for path "items" has no matching "helm.sh/merge-strategy/items" annotation`},
		},
		{
			name:        "invalid path",
			annotations: map[string]string{"helm.sh/merge-strategy/a..b": "append"},
			values:      values,
			expected:    []string{`merge strategy annotation "helm.sh/merge-strategy/a..b" has an invalid path`},
		},
		{
			name: "paths are checked against the default values",
			annotations: map[string]string{
				"helm.sh/merge-strategy/missing":        "append",
				"helm.sh/merge-strategy/scalar":         "append",
				"helm.sh/merge-strategy/config":         "append",
				"helm.sh/merge-strategy/scalar.missing": "append",
			},
			values: values,
			expected: []string{
				`merge strategy path "config" resolves to a non-array value`,
				`merge strategy path "missing" not found in chart default values`,
				`merge strategy path "scalar" resolves to a non-array value`,
				`merge strategy path "scalar.missing" not found in chart default values`,
			},
		},
		{
			name:        "paths are not checked without values",
			annotations: map[string]string{"helm.sh/merge-strategy/missing": "append"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateMergeStrategyAnnotations(tt.annotations, tt.values)
			require.Len(t, errs, len(tt.expected), "errors: %v", errs)
			for i, expected := range tt.expected {
				assert.Contains(t, errs[i].Error(), expected)
			}
		})
	}
}
