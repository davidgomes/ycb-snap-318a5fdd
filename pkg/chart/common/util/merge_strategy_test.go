/*
Copyright The Helm Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

    10|Unless required by applicable law or agreed to in writing, software
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
		"helm.sh/merge-strategy/env":            "append",
		"helm.sh/merge-strategy/servers":        "merge",
		"helm.sh/merge-key/servers":             "name",
		"helm.sh/merge-strategy/orphaned":       "merge",
		"helm.sh/merge-key/onlykey":             "id",
		"helm.sh/merge-strategy/":               "append",
		"helm.sh/merge-strategy/foo..bar":       "append",
		"helm.sh/merge-strategy/nested.objects": "merge",
		"helm.sh/merge-key/nested.objects":      "meta.id",
		"unrelated":                             "x",
	}

	got := ExtractMergeStrategies(annotations)
	byPath := map[string]MergeStrategy{}
	for _, s := range got {
		byPath[s.Path] = s
	}

	require.Contains(t, byPath, "env")
	assert.Equal(t, MergeStrategyAppend, byPath["env"].Strategy)

	require.Contains(t, byPath, "servers")
	assert.Equal(t, MergeStrategyMerge, byPath["servers"].Strategy)
	assert.Equal(t, "name", byPath["servers"].MergeKey)

	require.Contains(t, byPath, "orphaned")
	assert.Equal(t, MergeStrategyAppend, byPath["orphaned"].Strategy, "merge without key becomes append")

	assert.NotContains(t, byPath, "onlykey")
	assert.NotContains(t, byPath, "")
	assert.NotContains(t, byPath, "foo..bar")

	require.Contains(t, byPath, "nested.objects")
	assert.Equal(t, "meta.id", byPath["nested.objects"].MergeKey)
}

func TestExtractMergeStrategiesWithOverrides(t *testing.T) {
	annotations := map[string]string{
		"helm.sh/merge-strategy/env": "append",
	}
	got := ExtractMergeStrategiesWithOverrides(annotations, []string{"env=merge", "extra=append"}, []string{"env=name"})
	byPath := map[string]MergeStrategy{}
	for _, s := range got {
		byPath[s.Path] = s
	}
	assert.Equal(t, MergeStrategyMerge, byPath["env"].Strategy)
	assert.Equal(t, "name", byPath["env"].MergeKey)
	assert.Equal(t, MergeStrategyAppend, byPath["extra"].Strategy)
}

func TestValidateMergeStrategyAnnotations(t *testing.T) {
	values := map[string]any{
		"env":     []any{"a"},
		"scalar":  "nope",
		"servers": []any{map[string]any{"name": "a"}},
	}
	annotations := map[string]string{
		"helm.sh/merge-strategy/env":     "concat",
		"helm.sh/merge-strategy/servers": "merge",
		"helm.sh/merge-strategy/missing": "append",
		"helm.sh/merge-strategy/scalar":  "append",
		"helm.sh/merge-key/onlykey":      "id",
	}

	errs := ValidateMergeStrategyAnnotations(annotations, values)
	var msgs []string
	for _, err := range errs {
		msgs = append(msgs, err.Error())
	}
	joined := strings.Join(msgs, "\n")

	assert.Contains(t, joined, "unsupported")
	assert.Contains(t, joined, "env")
	assert.Contains(t, joined, "servers")
	assert.Contains(t, joined, "onlykey")
	assert.Contains(t, joined, "not found")
	assert.Contains(t, joined, "missing")
	assert.Contains(t, joined, "non-array")
	assert.Contains(t, joined, "scalar")
}

func TestCoalesceValuesAppendAndMerge(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "moby",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/env":     "append",
				"helm.sh/merge-strategy/servers": "merge",
				"helm.sh/merge-key/servers":      "name",
			},
		},
		Values: map[string]any{
			"env": []any{"CHART"},
			"servers": []any{
				map[string]any{"name": "a", "port": 80, "extra": "keep"},
				map[string]any{"name": "b", "port": 81},
			},
			"plain": []any{"old"},
		},
	}
	vals := map[string]any{
		"env": []any{"USER"},
		"servers": []any{
			map[string]any{"name": "a", "port": 8080},
			map[string]any{"name": "c", "port": 90},
			"bare",
			map[string]any{"port": 1},
		},
		"plain": []any{"new"},
	}

	got, err := CoalesceValues(c, vals)
	require.NoError(t, err)

	assert.Equal(t, []any{"CHART", "USER"}, got["env"])
	assert.Equal(t, []any{"new"}, got["plain"])

	servers, ok := got["servers"].([]any)
	require.True(t, ok)
	require.Len(t, servers, 5)
	first := servers[0].(map[string]any)
	assert.Equal(t, "a", first["name"])
	assert.Equal(t, 8080, first["port"])
	assert.Equal(t, "keep", first["extra"])
	assert.Equal(t, map[string]any{"name": "b", "port": 81}, servers[1])
	assert.Equal(t, map[string]any{"name": "c", "port": 90}, servers[2])
	assert.Equal(t, "bare", servers[3])
	assert.Equal(t, map[string]any{"port": 1}, servers[4])
}

func TestCoalesceValuesDoesNotMutateChartDefaults(t *testing.T) {
	chartEnv := []any{"CHART"}
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "moby",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/env": "append",
			},
		},
		Values: map[string]any{"env": chartEnv},
	}
	vals := map[string]any{"env": []any{"USER"}}
	_, err := CoalesceValues(c, vals)
	require.NoError(t, err)
	assert.Equal(t, []any{"CHART"}, c.Values["env"])
	assert.Equal(t, []any{"CHART"}, chartEnv)
}

func TestCoalesceValuesNullDeletes(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "moby",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/env": "append",
			},
		},
		Values: map[string]any{"env": []any{"CHART"}, "keep": "yes"},
	}
	vals := map[string]any{"env": nil}
	got, err := CoalesceValues(c, vals)
	require.NoError(t, err)
	_, exists := got["env"]
	assert.False(t, exists)
}

func TestMergeValuesPreservesNil(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "moby",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/servers": "merge",
				"helm.sh/merge-key/servers":      "name",
			},
		},
		Values: map[string]any{
			"servers": []any{
				map[string]any{"name": "a", "port": 80, "extra": "keep"},
			},
		},
	}
	vals := map[string]any{
		"servers": []any{
			map[string]any{"name": "a", "extra": nil},
		},
	}
	got, err := MergeValues(c, vals)
	require.NoError(t, err)
	servers := got["servers"].([]any)
	merged := servers[0].(map[string]any)
	assert.Nil(t, merged["extra"])
	assert.Equal(t, 80, merged["port"])
}

func TestParentStrategyDoesNotAffectSubchart(t *testing.T) {
	c := withDeps(&chart.Chart{
		Metadata: &chart.Metadata{
			Name: "parent",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/env": "append",
			},
		},
		Values: map[string]any{"env": []any{"P"}},
	}, &chart.Chart{
		Metadata: &chart.Metadata{Name: "child"},
		Values:   map[string]any{"env": []any{"CDEFAULT"}},
	})
	vals := map[string]any{
		"env":   []any{"PUSER"},
		"child": map[string]any{"env": []any{"CUSER"}},
	}
	got, err := CoalesceValues(c, vals)
	require.NoError(t, err)
	assert.Equal(t, []any{"P", "PUSER"}, got["env"])
	child := got["child"].(map[string]any)
	assert.Equal(t, []any{"CUSER"}, child["env"])
}

func TestGlobalStrategyOnSubchart(t *testing.T) {
	c := withDeps(&chart.Chart{
		Metadata: &chart.Metadata{Name: "parent"},
		Values:   map[string]any{},
	}, &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "child",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/global.env": "append",
			},
		},
		Values: map[string]any{
			"global": map[string]any{"env": []any{"C"}},
		},
	})
	vals := map[string]any{
		"global": map[string]any{"env": []any{"U"}},
	}
	got, err := CoalesceValues(c, vals)
	require.NoError(t, err)
	child := got["child"].(map[string]any)
	g := child["global"].(map[string]any)
	assert.Equal(t, []any{"C", "U"}, g["env"])
}

func TestCLIOverridesTakePrecedence(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "moby",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/env": "append",
			},
		},
		Values: map[string]any{"env": []any{"CHART"}},
	}
	vals := map[string]any{"env": []any{"USER"}}
	got, err := CoalesceValuesWithStrategies(c, vals, []string{"env=replace"}, nil)
	require.NoError(t, err)
	// invalid override is ignored; chart annotation still applies
	assert.Equal(t, []any{"CHART", "USER"}, got["env"])

	c.Metadata.Annotations = map[string]string{}
	got, err = CoalesceValuesWithStrategies(c, vals, []string{"env=append"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []any{"CHART", "USER"}, got["env"])
}

func TestNestedMergeKey(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "moby",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/items": "merge",
				"helm.sh/merge-key/items":      "meta.id",
			},
		},
		Values: map[string]any{
			"items": []any{
				map[string]any{"meta": map[string]any{"id": "1"}, "v": "old"},
			},
		},
	}
	vals := map[string]any{
		"items": []any{
			map[string]any{"meta": map[string]any{"id": "1"}, "v": "new"},
		},
	}
	got, err := CoalesceValues(c, vals)
	require.NoError(t, err)
	items := got["items"].([]any)
	assert.Equal(t, "new", items[0].(map[string]any)["v"])
}

func TestCoalesceTablesWithStrategiesAppendOldBeforeNew(t *testing.T) {
	dst := map[string]any{"env": []any{"new"}}
	src := map[string]any{"env": []any{"old"}}
	got := CoalesceTablesWithStrategies(dst, src, []MergeStrategy{{Path: "env", Strategy: MergeStrategyAppend}})
	assert.Equal(t, []any{"old", "new"}, got["env"])
}
