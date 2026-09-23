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
		"helm.sh/merge-strategy/a.b":  "append",
		"helm.sh/merge-strategy/c":    "merge",
		"helm.sh/merge-key/c":         "meta.name",
		"helm.sh/merge-strategy/d":    "merge",
		"helm.sh/merge-strategy/e":    "bogus",
		"helm.sh/merge-strategy/":     "append",
		"helm.sh/merge-strategy/x..y": "append",
		"helm.sh/merge-key/orphan":    "name",
		"example.com/unrelated":       "append",
	})
	assert.Equal(t, map[string]MergeStrategy{
		"a.b": {Strategy: "append"},
		"c":   {Strategy: "merge", Key: "meta.name"},
		"d":   {Strategy: "append"},
	}, got)
}

func TestCoalesceWithMergeStrategies(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		defaults    map[string]any
		user        map[string]any
		merge       bool
		expect      map[string]any
	}{
		{
			name:        "append",
			annotations: map[string]string{"helm.sh/merge-strategy/a.list": "append"},
			defaults:    map[string]any{"a": map[string]any{"list": []any{1, 2}}},
			user:        map[string]any{"a": map[string]any{"list": []any{3}}},
			expect:      map[string]any{"a": map[string]any{"list": []any{1, 2, 3}}},
		},
		{
			name: "merge by nested key",
			annotations: map[string]string{
				"helm.sh/merge-strategy/env": "merge",
				"helm.sh/merge-key/env":      "meta.name",
			},
			defaults: map[string]any{"env": []any{
				map[string]any{"meta": map[string]any{"name": "A"}, "value": "1", "keep": true},
				map[string]any{"meta": map[string]any{"name": "B"}, "value": "2"},
				"scalar",
				map[string]any{"nokey": true},
			}},
			user: map[string]any{"env": []any{
				map[string]any{"meta": map[string]any{"name": "A"}, "value": "x"},
				map[string]any{"meta": map[string]any{"name": "C"}, "value": "3"},
				"userscalar",
			}},
			expect: map[string]any{"env": []any{
				map[string]any{"meta": map[string]any{"name": "A"}, "value": "x", "keep": true},
				map[string]any{"meta": map[string]any{"name": "B"}, "value": "2"},
				"scalar",
				map[string]any{"nokey": true},
				map[string]any{"meta": map[string]any{"name": "C"}, "value": "3"},
				"userscalar",
			}},
		},
		{
			name: "null in matched element deleted when coalescing",
			annotations: map[string]string{
				"helm.sh/merge-strategy/env": "merge",
				"helm.sh/merge-key/env":      "name",
			},
			defaults: map[string]any{"env": []any{map[string]any{"name": "A", "value": "1"}}},
			user:     map[string]any{"env": []any{map[string]any{"name": "A", "value": nil}}},
			expect:   map[string]any{"env": []any{map[string]any{"name": "A"}}},
		},
		{
			name: "null in matched element preserved when merging",
			annotations: map[string]string{
				"helm.sh/merge-strategy/env": "merge",
				"helm.sh/merge-key/env":      "name",
			},
			defaults: map[string]any{"env": []any{map[string]any{"name": "A", "value": "1"}}},
			user:     map[string]any{"env": []any{map[string]any{"name": "A", "value": nil}}},
			merge:    true,
			expect:   map[string]any{"env": []any{map[string]any{"name": "A", "value": nil}}},
		},
		{
			name:        "null user value deletes key",
			annotations: map[string]string{"helm.sh/merge-strategy/list": "append"},
			defaults:    map[string]any{"list": []any{1}},
			user:        map[string]any{"list": nil},
			expect:      map[string]any{},
		},
		{
			name:     "no strategy replaces",
			defaults: map[string]any{"list": []any{1}},
			user:     map[string]any{"list": []any{2}},
			expect:   map[string]any{"list": []any{2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &chart.Chart{
				Metadata: &chart.Metadata{Name: "test", Annotations: tt.annotations},
				Values:   tt.defaults,
			}
			fn := CoalesceValues
			if tt.merge {
				fn = MergeValues
			}
			got, err := fn(c, tt.user)
			require.NoError(t, err)
			assert.Equal(t, tt.expect, map[string]any(got))
		})
	}
}

func TestMergeStrategiesDoNotMutateDefaults(t *testing.T) {
	defaults := map[string]any{"env": []any{map[string]any{"name": "A", "v": "1"}}}
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "test", Annotations: map[string]string{
			"helm.sh/merge-strategy/env": "merge",
			"helm.sh/merge-key/env":      "name",
		}},
		Values: defaults,
	}
	_, err := CoalesceValues(c, map[string]any{"env": []any{map[string]any{"name": "A", "v": "2"}}})
	require.NoError(t, err)
	assert.Equal(t, "1", defaults["env"].([]any)[0].(map[string]any)["v"])
}

func TestMergeStrategiesChartScopedAndGlobals(t *testing.T) {
	sub := &chart.Chart{
		Metadata: &chart.Metadata{Name: "sub", Annotations: map[string]string{
			"helm.sh/merge-strategy/global.hosts": "append",
		}},
		Values: map[string]any{"list": []any{"s"}, "global": map[string]any{"hosts": []any{"subdefault"}}},
	}
	parent := &chart.Chart{
		Metadata: &chart.Metadata{Name: "parent", Annotations: map[string]string{
			"helm.sh/merge-strategy/sub.list": "append",
		}},
		Values: map[string]any{"global": map[string]any{"hosts": []any{"parentdefault"}}},
	}
	parent.AddDependency(sub)

	got, err := CoalesceValues(parent, map[string]any{
		"sub": map[string]any{"list": []any{"u"}},
	})
	require.NoError(t, err)
	subVals := got["sub"].(map[string]any)
	// The parent's strategy does not reach into the subchart's defaults.
	assert.Equal(t, []any{"u"}, subVals["list"])
	assert.Equal(t, []any{"subdefault", "parentdefault"}, subVals["global"].(map[string]any)["hosts"])
}

func TestCoalesceTablesWithStrategies(t *testing.T) {
	got := CoalesceTablesWithStrategies(
		map[string]any{"list": []any{"new"}},
		map[string]any{"list": []any{"old"}, "x": 1},
		map[string]MergeStrategy{"list": {Strategy: "append"}},
	)
	assert.Equal(t, map[string]any{"list": []any{"old", "new"}, "x": 1}, got)
}

func TestApplyMergeStrategyOverrides(t *testing.T) {
	got, err := ApplyMergeStrategyOverrides(
		map[string]string{"helm.sh/merge-strategy/env": "append"},
		[]string{"env=merge"}, []string{"env=name"},
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]MergeStrategy{"env": {Strategy: "merge", Key: "name"}}, ExtractMergeStrategies(got))

	_, err = ApplyMergeStrategyOverrides(nil, []string{"bad"}, nil)
	assert.Error(t, err)
}

func TestValidateMergeStrategyAnnotations(t *testing.T) {
	errs := ValidateMergeStrategyAnnotations(map[string]string{
		"helm.sh/merge-strategy/ok":      "append",
		"helm.sh/merge-strategy/bad":     "zip",
		"helm.sh/merge-strategy/nokey":   "merge",
		"helm.sh/merge-key/orphan":       "name",
		"helm.sh/merge-strategy/missing": "append",
		"helm.sh/merge-strategy/scalar":  "append",
	}, map[string]any{"ok": []any{}, "nokey": []any{}, "scalar": "x"})

	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	joined := strings.Join(msgs, "\n")
	assert.Len(t, errs, 5, joined)
	assert.Contains(t, joined, `"bad" is unsupported`)
	assert.Contains(t, joined, `path "nokey" requires`)
	assert.Contains(t, joined, `"orphan"`)
	assert.Contains(t, joined, `"missing" not found`)
	assert.Contains(t, joined, `"scalar" resolves to a non-array`)
}
