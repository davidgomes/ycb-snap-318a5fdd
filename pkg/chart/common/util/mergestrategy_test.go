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

func TestExtractArrayStrategies(t *testing.T) {
	annotations := map[string]string{
		"helm.sh/merge-strategy/servers":      "append",
		"helm.sh/merge-strategy/ports":        "merge",
		"helm.sh/merge-key/ports":             "name",
		"helm.sh/merge-strategy/nokey":        "merge",
		"helm.sh/merge-strategy/":             "append",
		"helm.sh/merge-strategy/bad..path":    "append",
		"helm.sh/merge-strategy/weird":        "prepend",
		"helm.sh/merge-key/orphan":            "id",
		"helm.sh/merge-strategy/nested.items": "merge",
		"helm.sh/merge-key/nested.items":      "metadata.name",
	}

	got := ExtractArrayStrategies(annotations, []string{"ports=append", "extra=merge", " =append", "novalue"}, []string{"extra=id"})

	require.Equal(t, StrategyAppend, got["servers"].Strategy)
	require.Equal(t, StrategyAppend, got["ports"].Strategy, "CLI strategy overrides annotation")
	require.Equal(t, StrategyAppend, got["nokey"].Strategy, "merge without key becomes append")
	require.Equal(t, StrategyMerge, got["extra"].Strategy)
	require.Equal(t, "id", got["extra"].Key)
	require.Equal(t, StrategyMerge, got["nested.items"].Strategy)
	require.Equal(t, "metadata.name", got["nested.items"].Key)
	_, ok := got[""]
	require.False(t, ok)
	_, ok = got["bad..path"]
	require.False(t, ok)
	_, ok = got["weird"]
	require.False(t, ok)
	_, ok = got["orphan"]
	require.False(t, ok)
}

func TestLintMergeStrategyAnnotations(t *testing.T) {
	values := map[string]any{
		"servers": []any{"a"},
		"ports":   "not-an-array",
		"items":   []any{map[string]any{"name": "a"}},
	}
	errs := LintMergeStrategyAnnotations(map[string]string{
		"helm.sh/merge-strategy/missing": "append",
		"helm.sh/merge-strategy/ports":   "append",
		"helm.sh/merge-strategy/items":   "merge",
		"helm.sh/merge-strategy/nope":    "concat",
		"helm.sh/merge-key/orphan":       "name",
		"helm.sh/merge-strategy/servers": "append",
	}, values)

	var unsupported, notFound, nonArray, needsKey, orphan bool
	for _, err := range errs {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "unsupported") && strings.Contains(msg, "nope"):
			unsupported = true
		case strings.Contains(msg, "not found") && strings.Contains(msg, "missing"):
			notFound = true
		case strings.Contains(msg, "non-array") && strings.Contains(msg, "ports"):
			nonArray = true
		case strings.Contains(msg, "items") && strings.Contains(msg, "merge-key"):
			needsKey = true
		case strings.Contains(msg, "orphan"):
			orphan = true
		}
	}
	require.True(t, unsupported)
	require.True(t, notFound)
	require.True(t, nonArray)
	require.True(t, needsKey)
	require.True(t, orphan)
}

func TestCoalesceAppendAndMerge(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/servers":      "append",
				"helm.sh/merge-strategy/ports":        "merge",
				"helm.sh/merge-key/ports":             "name",
				"helm.sh/merge-strategy/nested.rules": "merge",
				"helm.sh/merge-key/nested.rules":      "metadata.name",
			},
		},
		Values: map[string]any{
			"servers": []any{"a", "b"},
			"ports": []any{
				map[string]any{"name": "http", "port": 80, "extra": "keep"},
				map[string]any{"name": "metrics", "port": 9090},
				"raw-default",
				map[string]any{"port": 1},
			},
			"nested": map[string]any{
				"rules": []any{
					map[string]any{"metadata": map[string]any{"name": "r1"}, "path": "/old"},
				},
			},
			"plain": []any{"only-default"},
		},
	}
	// Mutating the result must not change chart defaults.
	c.Values["ports"].([]any)[0].(map[string]any)["extra"] = "keep"

	vals := map[string]any{
		"servers": []any{"c"},
		"ports": []any{
			map[string]any{"name": "http", "port": 8080, "extra": nil},
			map[string]any{"name": "admin", "port": 9000},
			"raw-user",
			map[string]any{"port": 2},
		},
		"nested": map[string]any{
			"rules": []any{
				map[string]any{"metadata": map[string]any{"name": "r1"}, "path": "/new"},
				map[string]any{"metadata": map[string]any{"name": "r2"}, "path": "/two"},
			},
		},
	}

	got, err := CoalesceValues(c, vals)
	require.NoError(t, err)

	assert.Equal(t, []any{"a", "b", "c"}, got["servers"])
	ports := got["ports"].([]any)
	require.Len(t, ports, 7)
	http := ports[0].(map[string]any)
	assert.Equal(t, "http", http["name"])
	assert.Equal(t, 8080, http["port"])
	_, hasExtra := http["extra"]
	assert.False(t, hasExtra, "null user value deletes the key while coalescing")
	assert.Equal(t, map[string]any{"name": "metrics", "port": 9090}, ports[1])
	assert.Equal(t, "raw-default", ports[2])
	assert.Equal(t, map[string]any{"port": 1}, ports[3])
	assert.Equal(t, map[string]any{"name": "admin", "port": 9000}, ports[4])
	assert.Equal(t, "raw-user", ports[5])
	assert.Equal(t, map[string]any{"port": 2}, ports[6])

	rules := got["nested"].(map[string]any)["rules"].([]any)
	require.Len(t, rules, 2)
	assert.Equal(t, "/new", rules[0].(map[string]any)["path"])
	assert.Equal(t, "keep", c.Values["ports"].([]any)[0].(map[string]any)["extra"], "chart defaults are not mutated")
	assert.Equal(t, []any{"only-default"}, got["plain"])
}

func TestMergeValuesPreservesNil(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/ports": "merge",
				"helm.sh/merge-key/ports":      "name",
			},
		},
		Values: map[string]any{
			"ports": []any{map[string]any{"name": "http", "port": 80}},
		},
	}
	got, err := MergeValues(c, map[string]any{
		"ports": []any{map[string]any{"name": "http", "port": nil}},
	})
	require.NoError(t, err)
	ports := got["ports"].([]any)
	assert.Nil(t, ports[0].(map[string]any)["port"])
}

func TestStrategyDoesNotCrossSubcharts(t *testing.T) {
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
	child := &chart.Chart{
		Metadata: &chart.Metadata{Name: "child"},
		Values:   map[string]any{"items": []any{"child-default"}},
	}
	parent.AddDependency(child)

	got, err := CoalesceValues(parent, map[string]any{
		"items": []any{"parent-user"},
		"child": map[string]any{"items": []any{"child-user"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"parent-default", "parent-user"}, got["items"])
	assert.Equal(t, []any{"child-user"}, got["child"].(map[string]any)["items"])
}

func TestSubchartGlobalStrategy(t *testing.T) {
	parent := &chart.Chart{
		Metadata: &chart.Metadata{Name: "parent"},
		Values: map[string]any{
			"global": map[string]any{"tags": []any{"from-parent"}},
		},
	}
	child := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "child",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/global.tags": "append",
			},
		},
		Values: map[string]any{
			"global": map[string]any{"tags": []any{"from-child"}},
		},
	}
	parent.AddDependency(child)

	got, err := CoalesceValues(parent, map[string]any{
		"global": map[string]any{"tags": []any{"from-user"}},
	})
	require.NoError(t, err)
	// Root has no strategy, so the user array replaces the parent default.
	assert.Equal(t, []any{"from-user"}, got["global"].(map[string]any)["tags"])
	childTags := got["child"].(map[string]any)["global"].(map[string]any)["tags"]
	assert.Equal(t, []any{"from-child", "from-user"}, childTags)
}

func TestCLIStrategyOverride(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/servers": "merge",
				"helm.sh/merge-key/servers":      "name",
			},
		},
		Values: map[string]any{"servers": []any{"a"}},
	}
	got, err := CoalesceValuesWithOptions(c, map[string]any{"servers": []any{"b"}}, CoalesceOptions{
		MergeStrategies: []string{"servers=append"},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"a", "b"}, got["servers"])
}
