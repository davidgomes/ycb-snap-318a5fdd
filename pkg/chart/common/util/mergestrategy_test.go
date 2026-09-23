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

	chart "helm.sh/helm/v4/pkg/chart/v2"
)

func TestExtractMergeStrategies(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    map[string]ArrayMergeStrategy
	}{
		{
			name:        "nil annotations",
			annotations: nil,
			expected:    map[string]ArrayMergeStrategy{},
		},
		{
			name: "append and merge",
			annotations: map[string]string{
				"helm.sh/merge-strategy/env":            "append",
				"helm.sh/merge-strategy/app.containers": "merge",
				"helm.sh/merge-key/app.containers":      "name",
				"unrelated":                             "value",
			},
			expected: map[string]ArrayMergeStrategy{
				"env":            {Strategy: MergeStrategyAppend},
				"app.containers": {Strategy: MergeStrategyMerge, MergeKey: "name"},
			},
		},
		{
			name: "merge without key becomes append",
			annotations: map[string]string{
				"helm.sh/merge-strategy/list": "merge",
			},
			expected: map[string]ArrayMergeStrategy{
				"list": {Strategy: MergeStrategyAppend},
			},
		},
		{
			name: "invalid paths, unsupported strategies and orphan keys are excluded",
			annotations: map[string]string{
				"helm.sh/merge-strategy/":     "append",
				"helm.sh/merge-strategy/a..b": "append",
				"helm.sh/merge-strategy/.a":   "append",
				"helm.sh/merge-strategy/bad":  "prepend",
				"helm.sh/merge-key/orphan":    "name",
			},
			expected: map[string]ArrayMergeStrategy{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ExtractMergeStrategies(tt.annotations))
		})
	}
}

func TestParseMergeStrategyOverrides(t *testing.T) {
	tests := []struct {
		name       string
		strategies []string
		keys       []string
		expected   map[string]ArrayMergeStrategy
		wantErr    bool
	}{
		{
			name:       "valid",
			strategies: []string{"env=append", "containers=merge"},
			keys:       []string{"containers=metadata.name"},
			expected: map[string]ArrayMergeStrategy{
				"env":        {Strategy: MergeStrategyAppend},
				"containers": {Strategy: MergeStrategyMerge, MergeKey: "metadata.name"},
			},
		},
		{name: "missing separator", strategies: []string{"env"}, wantErr: true},
		{name: "unsupported strategy", strategies: []string{"env=prepend"}, wantErr: true},
		{name: "empty path", strategies: []string{"=append"}, wantErr: true},
		{name: "empty key", keys: []string{"env="}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMergeStrategyOverrides(tt.strategies, tt.keys)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestMergeArrays(t *testing.T) {
	merge := ArrayMergeStrategy{Strategy: MergeStrategyMerge, MergeKey: "name"}
	tests := []struct {
		name     string
		chart    []any
		user     []any
		strategy ArrayMergeStrategy
		merge    bool
		expected []any
	}{
		{
			name:     "append puts defaults first",
			chart:    []any{"a", "b"},
			user:     []any{"c"},
			strategy: ArrayMergeStrategy{Strategy: MergeStrategyAppend},
			expected: []any{"a", "b", "c"},
		},
		{
			name: "merge by key",
			chart: []any{
				map[string]any{"name": "a", "value": "1", "keep": true},
				map[string]any{"name": "b", "value": "2"},
			},
			user: []any{
				map[string]any{"name": "a", "value": "10"},
				map[string]any{"name": "c", "value": "3"},
			},
			strategy: merge,
			expected: []any{
				map[string]any{"name": "a", "value": "10", "keep": true},
				map[string]any{"name": "b", "value": "2"},
				map[string]any{"name": "c", "value": "3"},
			},
		},
		{
			name:  "recursive merge of matched pairs",
			chart: []any{map[string]any{"name": "a", "res": map[string]any{"cpu": "1", "mem": "1Gi"}}},
			user:  []any{map[string]any{"name": "a", "res": map[string]any{"cpu": "2"}}},
			expected: []any{
				map[string]any{"name": "a", "res": map[string]any{"cpu": "2", "mem": "1Gi"}},
			},
			strategy: merge,
		},
		{
			name:     "non-map elements and elements missing the key are preserved",
			chart:    []any{"scalar", map[string]any{"other": 1}, map[string]any{"name": "a", "v": 1}},
			user:     []any{42, map[string]any{"nokey": true}, map[string]any{"name": "a", "v": 2}},
			strategy: merge,
			expected: []any{
				"scalar",
				map[string]any{"other": 1},
				map[string]any{"name": "a", "v": 2},
				42,
				map[string]any{"nokey": true},
			},
		},
		{
			name:     "dotted merge key",
			chart:    []any{map[string]any{"meta": map[string]any{"id": 1}, "v": "a", "d": "x"}},
			user:     []any{map[string]any{"meta": map[string]any{"id": 1}, "v": "b"}},
			strategy: ArrayMergeStrategy{Strategy: MergeStrategyMerge, MergeKey: "meta.id"},
			expected: []any{map[string]any{"meta": map[string]any{"id": 1}, "v": "b", "d": "x"}},
		},
		{
			name:     "null user field is removed when coalescing",
			chart:    []any{map[string]any{"name": "a", "v": 1, "w": 2}},
			user:     []any{map[string]any{"name": "a", "v": nil}},
			strategy: merge,
			expected: []any{map[string]any{"name": "a", "w": 2}},
		},
		{
			name:     "null user field is preserved when merging",
			chart:    []any{map[string]any{"name": "a", "v": 1, "w": 2}},
			user:     []any{map[string]any{"name": "a", "v": nil}},
			strategy: merge,
			merge:    true,
			expected: []any{map[string]any{"name": "a", "v": nil, "w": 2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeArrays(t.Logf, tt.chart, tt.user, tt.strategy, "path", tt.merge)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func strategyChart(name string, annotations map[string]string, values map[string]any) *chart.Chart {
	return &chart.Chart{
		Metadata: &chart.Metadata{Name: name, Annotations: annotations},
		Values:   values,
	}
}

func TestCoalesceValuesWithMergeStrategies(t *testing.T) {
	c := strategyChart("parent", map[string]string{
		"helm.sh/merge-strategy/env":        "append",
		"helm.sh/merge-strategy/app.ports":  "merge",
		"helm.sh/merge-key/app.ports":       "name",
		"helm.sh/merge-strategy/untouched":  "append",
		"helm.sh/merge-strategy/notanarray": "append",
	}, map[string]any{
		"env": []any{"A"},
		"app": map[string]any{
			"ports": []any{
				map[string]any{"name": "http", "port": 80},
				map[string]any{"name": "metrics", "port": 9090},
			},
		},
		"untouched":  []any{"x"},
		"notanarray": "scalar",
		"replaced":   []any{"default"},
	})

	vals := map[string]any{
		"env": []any{"B"},
		"app": map[string]any{
			"ports": []any{map[string]any{"name": "http", "port": 8080}},
		},
		"notanarray": []any{"y"},
		"replaced":   []any{"user"},
	}

	out, err := CoalesceValues(c, vals)
	require.NoError(t, err)
	assert.Equal(t, []any{"A", "B"}, out["env"])
	assert.Equal(t, []any{
		map[string]any{"name": "http", "port": 8080},
		map[string]any{"name": "metrics", "port": 9090},
	}, out["app"].(map[string]any)["ports"])
	assert.Equal(t, []any{"x"}, out["untouched"])
	assert.Equal(t, []any{"y"}, out["notanarray"])
	assert.Equal(t, []any{"user"}, out["replaced"])

	// Chart defaults must not be mutated.
	assert.Equal(t, []any{"A"}, c.Values["env"])
	assert.Equal(t, map[string]any{"name": "http", "port": 80}, c.Values["app"].(map[string]any)["ports"].([]any)[0])

	t.Run("null user value deletes the key when coalescing", func(t *testing.T) {
		out, err := CoalesceValues(c, map[string]any{"env": nil})
		require.NoError(t, err)
		assert.NotContains(t, out, "env")
	})

	t.Run("null user value is preserved when merging", func(t *testing.T) {
		out, err := MergeValues(c, map[string]any{"env": nil})
		require.NoError(t, err)
		assert.Contains(t, out, "env")
		assert.Nil(t, out["env"])
	})

	t.Run("overrides take precedence over annotations", func(t *testing.T) {
		out, err := CoalesceValuesWithMergeStrategies(c, map[string]any{
			"replaced": []any{"user"},
			"app": map[string]any{
				"ports": []any{map[string]any{"name": "http", "port": 8080}},
			},
		}, map[string]ArrayMergeStrategy{
			"replaced":  {Strategy: MergeStrategyAppend},
			"app.ports": {Strategy: MergeStrategyAppend},
		})
		require.NoError(t, err)
		assert.Equal(t, []any{"default", "user"}, out["replaced"])
		assert.Len(t, out["app"].(map[string]any)["ports"], 3)
	})
}

func TestCoalesceValuesMergeStrategiesAreChartScoped(t *testing.T) {
	sub := strategyChart("sub", map[string]string{
		"helm.sh/merge-strategy/list": "append",
	}, map[string]any{"list": []any{"sub-default"}})
	parent := strategyChart("parent", map[string]string{
		// The parent's strategy for "list" must not affect the subchart.
		"helm.sh/merge-strategy/other": "append",
		"helm.sh/merge-strategy/list":  "append",
	}, map[string]any{
		"list":  []any{"parent-default"},
		"other": []any{"o"},
		"sub":   map[string]any{"list": []any{"parent-for-sub"}},
	})
	parent.AddDependency(sub)

	out, err := CoalesceValues(parent, map[string]any{
		"list": []any{"user"},
		"sub":  map[string]any{"list": []any{"user-for-sub"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"parent-default", "user"}, out["list"])
	// The parent chart has no strategy for "sub.list", so the user value
	// replaces the parent's value, then the subchart appends it to its default.
	assert.Equal(t, []any{"sub-default", "user-for-sub"}, out["sub"].(map[string]any)["list"])

	noStrategySub := strategyChart("plain", nil, map[string]any{"list": []any{"plain-default"}})
	parent2 := strategyChart("parent2", map[string]string{
		"helm.sh/merge-strategy/list": "append",
	}, map[string]any{"list": []any{"p"}})
	parent2.AddDependency(noStrategySub)
	out, err = CoalesceValues(parent2, map[string]any{"plain": map[string]any{"list": []any{"u"}}})
	require.NoError(t, err)
	assert.Equal(t, []any{"u"}, out["plain"].(map[string]any)["list"])
}

func TestCoalesceValuesGlobalMergeStrategies(t *testing.T) {
	sub := strategyChart("sub", map[string]string{
		"helm.sh/merge-strategy/global.hosts": "append",
	}, map[string]any{})
	parent := strategyChart("parent", nil, map[string]any{
		"global": map[string]any{"hosts": []any{"parent-host"}},
	})
	parent.AddDependency(sub)

	out, err := CoalesceValues(parent, map[string]any{
		"sub": map[string]any{"global": map[string]any{"hosts": []any{"sub-host"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"parent-host"}, out["global"].(map[string]any)["hosts"])
	subGlobal := out["sub"].(map[string]any)["global"].(map[string]any)
	assert.Equal(t, []any{"sub-host", "parent-host"}, subGlobal["hosts"])
}

func TestCoalesceTablesWithMergeStrategies(t *testing.T) {
	strategies := map[string]ArrayMergeStrategy{
		"list":  {Strategy: MergeStrategyAppend},
		"items": {Strategy: MergeStrategyMerge, MergeKey: "id"},
	}
	oldVals := map[string]any{
		"list":  []any{"old"},
		"items": []any{map[string]any{"id": 1, "a": "old", "b": "old"}},
		"other": []any{"old"},
	}
	newVals := map[string]any{
		"list":  []any{"new"},
		"items": []any{map[string]any{"id": 1, "a": "new"}},
		"other": []any{"new"},
	}
	out := CoalesceTablesWithMergeStrategies(newVals, oldVals, strategies)
	assert.Equal(t, []any{"old", "new"}, out["list"])
	assert.Equal(t, []any{map[string]any{"id": 1, "a": "new", "b": "old"}}, out["items"])
	assert.Equal(t, []any{"new"}, out["other"])
}

func TestRestoreMergeStrategyPaths(t *testing.T) {
	sub := strategyChart("sub", map[string]string{
		"helm.sh/merge-strategy/list": "append",
	}, map[string]any{"list": []any{"sub-default"}})
	parent := strategyChart("parent", nil, map[string]any{"name": "p"})
	parent.AddDependency(sub)

	cvals, err := CoalesceValues(parent, nil)
	require.NoError(t, err)
	require.Equal(t, []any{"sub-default"}, cvals["sub"].(map[string]any)["list"])

	RestoreMergeStrategyPaths(parent, cvals, parent.Values, nil)
	assert.NotContains(t, cvals["sub"].(map[string]any), "list")

	// Using the restored values as defaults applies the strategy only once.
	parent.Values = cvals
	out, err := CoalesceValues(parent, map[string]any{"sub": map[string]any{"list": []any{"u"}}})
	require.NoError(t, err)
	assert.Equal(t, []any{"sub-default", "u"}, out["sub"].(map[string]any)["list"])
}

func TestValidateMergeStrategyAnnotations(t *testing.T) {
	values := map[string]any{
		"list":   []any{},
		"scalar": "x",
		"nested": map[string]any{"items": []any{}},
	}
	tests := []struct {
		name        string
		annotations map[string]string
		values      map[string]any
		contains    []string
	}{
		{
			name: "valid",
			annotations: map[string]string{
				"helm.sh/merge-strategy/list":         "append",
				"helm.sh/merge-strategy/nested.items": "merge",
				"helm.sh/merge-key/nested.items":      "name",
			},
			values: values,
		},
		{
			name:        "unsupported strategy",
			annotations: map[string]string{"helm.sh/merge-strategy/list": "prepend"},
			values:      values,
			contains:    []string{"unsupported", "list"},
		},
		{
			name:        "merge without key",
			annotations: map[string]string{"helm.sh/merge-strategy/list": "merge"},
			values:      values,
			contains:    []string{"list", "helm.sh/merge-key/list"},
		},
		{
			name:        "orphan merge key",
			annotations: map[string]string{"helm.sh/merge-key/list": "name"},
			values:      values,
			contains:    []string{"list"},
		},
		{
			name:        "path not found",
			annotations: map[string]string{"helm.sh/merge-strategy/missing": "append"},
			values:      values,
			contains:    []string{"not found", "missing"},
		},
		{
			name:        "non-array path",
			annotations: map[string]string{"helm.sh/merge-strategy/scalar": "append"},
			values:      values,
			contains:    []string{"non-array", "scalar"},
		},
		{
			name:        "nil values skips path checks",
			annotations: map[string]string{"helm.sh/merge-strategy/missing": "append"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateMergeStrategyAnnotations(tt.annotations, tt.values)
			if len(tt.contains) == 0 {
				assert.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			for _, s := range tt.contains {
				assert.Contains(t, errs[0].Error(), s)
			}
		})
	}
}
