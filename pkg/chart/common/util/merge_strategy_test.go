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
	strategies, keys := ExtractMergeStrategies(map[string]string{
		"helm.sh/merge-strategy/ports":      "append",
		"helm.sh/merge-strategy/servers":    "merge",
		"helm.sh/merge-key/servers":         "name",
		"helm.sh/merge-strategy/items":      "merge",
		"helm.sh/merge-strategy/":           "append",
		"helm.sh/merge-strategy/foo..bar":   "append",
		"helm.sh/merge-strategy/bad":        "replace",
		"helm.sh/merge-key/orphan":          "id",
		"helm.sh/merge-strategy/nested.key": "merge",
		"helm.sh/merge-key/nested.key":      "metadata.name",
	})

	assert.Equal(t, map[string]string{
		"ports":      MergeStrategyAppend,
		"servers":    MergeStrategyMerge,
		"items":      MergeStrategyAppend, // merge without a key falls back to append
		"nested.key": MergeStrategyMerge,
	}, strategies)
	assert.Equal(t, map[string]string{
		"servers":    "name",
		"nested.key": "metadata.name",
	}, keys)
}

func TestResolveMergeStrategiesCLIPrecedence(t *testing.T) {
	strategies, keys := ResolveMergeStrategies(map[string]string{
		"helm.sh/merge-strategy/ports":   "append",
		"helm.sh/merge-strategy/servers": "merge",
		"helm.sh/merge-key/servers":      "name",
	}, []string{"ports=merge", "servers=append", "noequals", "=append"}, []string{"ports=id", "servers=name"})

	assert.Equal(t, MergeStrategyMerge, strategies["ports"])
	assert.Equal(t, "id", keys["ports"])
	assert.Equal(t, MergeStrategyAppend, strategies["servers"])
	_, hasServerKey := keys["servers"]
	assert.False(t, hasServerKey)
}

func TestCoalesceAppendAndMerge(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/ports":         "append",
				"helm.sh/merge-strategy/servers":       "merge",
				"helm.sh/merge-key/servers":            "metadata.name",
				"helm.sh/merge-strategy/servers.ports": "append",
			},
		},
		Values: map[string]any{
			"ports": []any{"80", "443"},
			"servers": []any{
				map[string]any{"metadata": map[string]any{"name": "web"}, "image": "nginx", "ports": []any{"80"}},
				map[string]any{"metadata": map[string]any{"name": "db"}, "image": "postgres"},
				"keep-default",
				map[string]any{"note": "no-key"},
			},
			"replaced": []any{"a"},
		},
	}
	originalPorts := append([]any{}, c.Values["ports"].([]any)...)

	vals := map[string]any{
		"ports": []any{"8080"},
		"servers": []any{
			map[string]any{"metadata": map[string]any{"name": "web"}, "image": "nginx:latest", "ports": []any{"443"}},
			map[string]any{"metadata": map[string]any{"name": "cache"}, "image": "redis"},
			42,
			map[string]any{"note": "user-no-key"},
		},
		"replaced": []any{"b"},
	}

	got, err := CoalesceValues(c, vals)
	require.NoError(t, err)

	assert.Equal(t, []any{"80", "443", "8080"}, got["ports"])
	assert.Equal(t, []any{"b"}, got["replaced"])
	assert.Equal(t, originalPorts, c.Values["ports"])

	servers := got["servers"].([]any)
	require.Len(t, servers, 7)
	web := servers[0].(map[string]any)
	assert.Equal(t, "nginx:latest", web["image"])
	assert.Equal(t, []any{"80", "443"}, web["ports"])
	assert.Equal(t, "db", servers[1].(map[string]any)["metadata"].(map[string]any)["name"])
	assert.Equal(t, "keep-default", servers[2])
	assert.Equal(t, "no-key", servers[3].(map[string]any)["note"])
	assert.Equal(t, "cache", servers[4].(map[string]any)["metadata"].(map[string]any)["name"])
	assert.Equal(t, 42, servers[5])
	assert.Equal(t, "user-no-key", servers[6].(map[string]any)["note"])
}

func TestCoalesceNullDeletesAndMergePreservesNil(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/ports":   "append",
				"helm.sh/merge-strategy/servers": "merge",
				"helm.sh/merge-key/servers":      "name",
			},
		},
		Values: map[string]any{
			"ports": []any{"80"},
			"servers": []any{
				map[string]any{"name": "web", "image": "nginx", "tag": "1"},
			},
			"plain": "keep",
		},
	}

	coalesced, err := CoalesceValues(c, map[string]any{
		"ports": nil,
		"servers": []any{
			map[string]any{"name": "web", "tag": nil},
		},
	})
	require.NoError(t, err)
	_, hasPorts := coalesced["ports"]
	assert.False(t, hasPorts)
	web := coalesced["servers"].([]any)[0].(map[string]any)
	_, hasTag := web["tag"]
	assert.False(t, hasTag)
	assert.Equal(t, "nginx", web["image"])

	merged, err := MergeValues(c, map[string]any{
		"ports": nil,
		"servers": []any{
			map[string]any{"name": "web", "tag": nil},
		},
	})
	require.NoError(t, err)
	assert.Nil(t, merged["ports"])
	mergedWeb := merged["servers"].([]any)[0].(map[string]any)
	tag, hasTag := mergedWeb["tag"]
	assert.True(t, hasTag)
	assert.Nil(t, tag)
}

func TestCoalesceStrategiesAreChartScoped(t *testing.T) {
	parent := withDeps(&chart.Chart{
		Metadata: &chart.Metadata{
			Name: "parent",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/list": "append",
			},
		},
		Values: map[string]any{
			"list": []any{"parent-default"},
			"child": map[string]any{
				"list": []any{"from-parent-values"},
			},
		},
	}, &chart.Chart{
		Metadata: &chart.Metadata{Name: "child"},
		Values: map[string]any{
			"list": []any{"child-default"},
		},
	})

	got, err := CoalesceValues(parent, map[string]any{
		"list": []any{"parent-user"},
		"child": map[string]any{
			"list": []any{"child-user"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"parent-default", "parent-user"}, got["list"])
	child := got["child"].(map[string]any)
	assert.Equal(t, []any{"child-user"}, child["list"])
}

func TestCoalesceGlobalStrategyOnSubchart(t *testing.T) {
	parent := withDeps(&chart.Chart{
		Metadata: &chart.Metadata{Name: "parent"},
		Values: map[string]any{
			"global": map[string]any{
				"tags": []any{"parent-default"},
			},
		},
	}, &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "child",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/global.tags": "append",
			},
		},
		Values: map[string]any{
			"global": map[string]any{
				"tags": []any{"child-default"},
			},
		},
	})

	got, err := CoalesceValues(parent, map[string]any{
		"global": map[string]any{
			"tags": []any{"parent-user"},
		},
		"child": map[string]any{
			"global": map[string]any{
				"tags": []any{"child-user"},
			},
		},
	})
	require.NoError(t, err)
	child := got["child"].(map[string]any)
	global := child["global"].(map[string]any)
	assert.Equal(t, []any{"child-default", "child-user", "parent-user"}, global["tags"])
}

func TestCoalesceCLIOverride(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name: "app",
			Annotations: map[string]string{
				"helm.sh/merge-strategy/ports": "append",
			},
		},
		Values: map[string]any{
			"ports": []any{"80"},
			"servers": []any{
				map[string]any{"name": "web", "image": "nginx"},
			},
		},
	}
	got, err := CoalesceValuesWithOverrides(c, map[string]any{
		"ports": []any{"81"},
		"servers": []any{
			map[string]any{"name": "web", "image": "caddy"},
			map[string]any{"name": "db"},
		},
	}, MergeOverrides{
		MergeStrategies: []string{"ports=append", "servers=merge"},
		MergeKeys:       []string{"servers=name"},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"80", "81"}, got["ports"])
	servers := got["servers"].([]any)
	require.Len(t, servers, 2)
	assert.Equal(t, "caddy", servers[0].(map[string]any)["image"])
	assert.Equal(t, "db", servers[1].(map[string]any)["name"])
}

func TestLintMergeStrategyAnnotations(t *testing.T) {
	errs := LintMergeStrategyAnnotations(map[string]string{
		"helm.sh/merge-strategy/missing": "append",
		"helm.sh/merge-strategy/name":    "append",
		"helm.sh/merge-strategy/servers": "merge",
		"helm.sh/merge-strategy/bad":     "nope",
		"helm.sh/merge-key/orphan":       "id",
		"helm.sh/merge-strategy/ports":   "merge",
		"helm.sh/merge-key/ports":        "name",
	}, map[string]any{
		"name":  "web",
		"ports": []any{"80"},
		"bad":   []any{"x"},
	})
	var messages []string
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	joined := strings.Join(messages, "\n")
	assert.Contains(t, joined, "unsupported")
	assert.Contains(t, joined, "bad")
	assert.Contains(t, joined, "not found")
	assert.Contains(t, joined, "missing")
	assert.Contains(t, joined, "non-array")
	assert.Contains(t, joined, "name")
	assert.Contains(t, joined, "servers")
	assert.Contains(t, joined, "orphan")
}
