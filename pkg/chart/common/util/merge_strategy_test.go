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
	annotations := map[string]string{
		"helm.sh/merge-strategy/items":    "append",
		"helm.sh/merge-strategy/servers":  "merge",
		"helm.sh/merge-key/servers":       "name",
		"helm.sh/merge-strategy/ports":    "merge",
		"helm.sh/merge-strategy/replaced": "append",
		"helm.sh/merge-strategy/":         "append",
		"helm.sh/merge-strategy/foo..bar": "append",
		"helm.sh/merge-strategy/bad":      "replace",
		"helm.sh/merge-key/orphan":        "id",
		"unrelated":                       "append",
	}

	strategies, keys := ExtractMergeStrategies(annotations, []string{"replaced=merge", "nope=replace", " =append"}, []string{"replaced=id", "servers=hostname"})

	assert.Equal(t, map[string]string{
		"items":    StrategyAppend,
		"servers":  StrategyMerge,
		"ports":    StrategyAppend, // merge without a key is returned as append
		"replaced": StrategyMerge,
	}, strategies)
	assert.Equal(t, map[string]string{
		"servers":  "hostname",
		"replaced": "id",
	}, keys)
}

func TestCoalesceAppendAndNull(t *testing.T) {
	chrt := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "append",
				"helm.sh/merge-strategy/tags":  "append",
			},
		},
		Values: map[string]any{
			"items": []any{"chart"},
			"tags":  []string{"stable"},
			"keep":  "yes",
		},
	}
	vals, err := CoalesceValues(chrt, map[string]any{
		"items": []any{"user"},
		"tags":  []string{"user"},
		"keep":  nil,
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"chart", "user"}, vals["items"])
	assert.Equal(t, []string{"stable", "user"}, vals["tags"])
	_, hasKeep := vals["keep"]
	assert.False(t, hasKeep)

	chrt.Values["items"].([]any)[0] = "mutated"
	chrt.Values["tags"].([]string)[0] = "changed"
	assert.Equal(t, []any{"chart", "user"}, vals["items"])
	assert.Equal(t, []string{"stable", "user"}, vals["tags"])

	deleted, err := CoalesceValues(chrt, map[string]any{"items": nil})
	require.NoError(t, err)
	_, ok := deleted["items"]
	assert.False(t, ok)
	assert.Equal(t, "yes", deleted["keep"])
}

func TestMergeArraysByKey(t *testing.T) {
	chrt := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      "name",
				"helm.sh/merge-strategy/objs":  "merge",
				"helm.sh/merge-key/objs":       "meta.name",
			},
		},
		Values: map[string]any{
			"items": []any{
				map[string]any{"name": "a", "v": 1, "spec": map[string]any{"cpu": "1", "mem": "2"}},
				"keep-default",
				map[string]any{"v": 9},
				map[string]any{"name": "b", "v": 1},
			},
			"objs": []any{
				map[string]any{"meta": map[string]any{"name": "a"}, "v": 1},
			},
		},
	}

	vals, err := CoalesceValues(chrt, map[string]any{
		"items": []any{
			map[string]any{"name": "a", "v": 2, "spec": map[string]any{"cpu": "3"}},
			"keep-user",
			map[string]any{"v": 8},
			map[string]any{"name": "c", "v": 3},
		},
		"objs": []any{
			map[string]any{"meta": map[string]any{"name": "a"}, "v": 2},
			map[string]any{"v": 4},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []any{
		map[string]any{"name": "a", "v": 2, "spec": map[string]any{"cpu": "3", "mem": "2"}},
		"keep-default",
		map[string]any{"v": 9},
		map[string]any{"name": "b", "v": 1},
		"keep-user",
		map[string]any{"v": 8},
		map[string]any{"name": "c", "v": 3},
	}, vals["items"])
	assert.Equal(t, []any{
		map[string]any{"meta": map[string]any{"name": "a"}, "v": 2},
		map[string]any{"v": 4},
	}, vals["objs"])

	// Chart defaults are not mutated by the merge.
	assert.Equal(t, 1, chrt.Values["items"].([]any)[0].(map[string]any)["v"])
	assert.Equal(t, "1", chrt.Values["items"].([]any)[0].(map[string]any)["spec"].(map[string]any)["cpu"])
}

func TestMergeValuesPreservesNil(t *testing.T) {
	chrt := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      "name",
			},
		},
		Values: map[string]any{
			"items": []any{
				map[string]any{"name": "a", "x": "keep", "y": "chart"},
			},
			"plain": "chart",
		},
	}

	merged, err := MergeValues(chrt, map[string]any{
		"items": []any{
			map[string]any{"name": "a", "x": nil},
		},
		"plain": nil,
	})
	require.NoError(t, err)
	item := merged["items"].([]any)[0].(map[string]any)
	assert.Nil(t, item["x"])
	assert.Equal(t, "chart", item["y"])
	assert.Nil(t, merged["plain"])

	coalesced, err := CoalesceValues(chrt, map[string]any{
		"items": []any{
			map[string]any{"name": "a", "x": nil},
		},
		"plain": nil,
	})
	require.NoError(t, err)
	coalescedItem := coalesced["items"].([]any)[0].(map[string]any)
	_, hasX := coalescedItem["x"]
	assert.False(t, hasX)
	assert.Equal(t, "chart", coalescedItem["y"])
	_, hasPlain := coalesced["plain"]
	assert.False(t, hasPlain)
}

func TestStrategiesAreChartScoped(t *testing.T) {
	sub := &chart.Chart{
		Metadata: &chart.Metadata{Name: "sub"},
		Values: map[string]any{
			"items": []any{"sub-default"},
		},
	}
	parent := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "parent",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "append",
			},
		},
		Values: map[string]any{
			"items": []any{"parent-default"},
		},
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
	assert.Equal(t, []any{"sub-user"}, subVals["items"])
}

func TestSubchartGlobalStrategy(t *testing.T) {
	sub := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "sub",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/global.items": "append",
			},
		},
		Values: map[string]any{
			"global": map[string]any{
				"items": []any{"sub-default"},
			},
		},
	}
	parent := &chart.Chart{
		Metadata: &chart.Metadata{Name: "parent"},
		Values: map[string]any{
			"global": map[string]any{
				"items": []any{"parent-default"},
			},
		},
	}
	parent.AddDependency(sub)

	vals, err := CoalesceValues(parent, map[string]any{
		"global": map[string]any{
			"items": []any{"parent-user"},
		},
		"sub": map[string]any{
			"global": map[string]any{
				"items": []any{"sub-user"},
			},
		},
	})
	require.NoError(t, err)

	parentGlobal := vals["global"].(map[string]any)
	assert.Equal(t, []any{"parent-user"}, parentGlobal["items"])

	subGlobal := vals["sub"].(map[string]any)["global"].(map[string]any)
	assert.Equal(t, []any{"sub-default", "sub-user", "parent-user"}, subGlobal["items"])
}

func TestCLIStrategyOverrides(t *testing.T) {
	chrt := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "append",
			},
		},
		Values: map[string]any{
			"items": []any{
				map[string]any{"name": "a", "v": 1},
				map[string]any{"name": "b", "v": 1},
			},
		},
	}
	vals, err := CoalesceValuesWithStrategies(chrt, map[string]any{
		"items": []any{
			map[string]any{"name": "a", "v": 2},
			map[string]any{"name": "c", "v": 3},
		},
	}, []string{"items=merge"}, []string{"items=name"})
	require.NoError(t, err)
	assert.Equal(t, []any{
		map[string]any{"name": "a", "v": 2},
		map[string]any{"name": "b", "v": 1},
		map[string]any{"name": "c", "v": 3},
	}, vals["items"])
}

func TestCoalesceTablesWithStrategiesOldBeforeNew(t *testing.T) {
	dst := map[string]any{"items": []any{"new"}}
	src := map[string]any{"items": []any{"old"}, "kept": "yes"}
	got := CoalesceTablesWithStrategies(dst, src, map[string]string{"items": StrategyAppend}, nil)
	assert.Equal(t, []any{"old", "new"}, got["items"])
	assert.Equal(t, "yes", got["kept"])
}

func TestStrategiesForChartTreeDoesNotCopyParentStrategy(t *testing.T) {
	sub := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "sub",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/global.extra": "merge",
				"helm.sh/merge-key/global.extra":      "id",
			},
		},
	}
	parent := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "parent",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "append",
			},
		},
	}
	parent.AddDependency(sub)

	strategies, keys, err := StrategiesForChartTree(parent, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"items":            StrategyAppend,
		"sub.global.extra": StrategyMerge,
	}, strategies)
	assert.Equal(t, map[string]string{"sub.global.extra": "id"}, keys)
	_, copied := strategies["sub.items"]
	assert.False(t, copied)
}

func TestValidateMergeStrategyAnnotations(t *testing.T) {
	values := map[string]any{
		"name": "app",
		"items": []any{
			map[string]any{"name": "a"},
		},
		"nested": map[string]any{
			"list": []any{1},
		},
	}
	errs := ValidateMergeStrategyAnnotations(map[string]string{
		"helm.sh/merge-strategy/missing":     "nope",
		"helm.sh/merge-strategy/name":        "append",
		"helm.sh/merge-strategy/items":       "merge",
		"helm.sh/merge-strategy/absent":      "append",
		"helm.sh/merge-strategy/nested.list": "append",
		"helm.sh/merge-key/orphan":           "id",
	}, values, true)

	var messages []string
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	has := func(substrs ...string) bool {
		for _, msg := range messages {
			ok := true
			for _, substr := range substrs {
				if !strings.Contains(msg, substr) {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
		return false
	}
	assert.True(t, has("unsupported", "missing"), messages)
	assert.True(t, has("non-array", "name"), messages)
	assert.True(t, has("not found", "absent"), messages)
	assert.True(t, has("items"), messages)
	assert.True(t, has("orphan"), messages)
	for _, msg := range messages {
		assert.NotContains(t, msg, "nested.list")
	}
}
