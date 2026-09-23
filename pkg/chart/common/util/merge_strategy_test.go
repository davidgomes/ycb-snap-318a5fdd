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

	v3chart "helm.sh/helm/v4/internal/chart/v3"
	"helm.sh/helm/v4/pkg/chart/common"
	chart "helm.sh/helm/v4/pkg/chart/v2"
)

func mustValues(t *testing.T, y string) map[string]any {
	t.Helper()
	v, err := common.ReadValues([]byte(y))
	require.NoError(t, err)
	return v
}

func strategyChart(name string, annotations map[string]string, values map[string]any, deps ...*chart.Chart) *chart.Chart {
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: name, Annotations: annotations},
		Values:   values,
	}
	c.AddDependency(deps...)
	return c
}

func TestExtractMergeStrategies(t *testing.T) {
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
			name: "append and merge",
			annotations: map[string]string{
				"helm.sh/merge-strategy/args":        "append",
				"helm.sh/merge-strategy/server.env":  "merge",
				"helm.sh/merge-key/server.env":       "name",
				"helm.sh/merge-strategy/containers":  "merge",
				"helm.sh/merge-key/containers":       "metadata.name",
				"example.com/unrelated":              "append",
				"helm.sh/merge-strategy/unsupported": "replace",
			},
			expected: map[string]MergeStrategy{
				"args":       {Strategy: MergeStrategyAppend},
				"server.env": {Strategy: MergeStrategyMerge, MergeKey: "name"},
				"containers": {Strategy: MergeStrategyMerge, MergeKey: "metadata.name"},
			},
		},
		{
			name: "merge without key falls back to append",
			annotations: map[string]string{
				"helm.sh/merge-strategy/env":   "merge",
				"helm.sh/merge-strategy/ports": "merge",
				"helm.sh/merge-key/ports":      "",
				"helm.sh/merge-strategy/other": "merge",
				"helm.sh/merge-key/other":      "a..b",
			},
			expected: map[string]MergeStrategy{
				"env":   {Strategy: MergeStrategyAppend},
				"ports": {Strategy: MergeStrategyAppend},
				"other": {Strategy: MergeStrategyAppend},
			},
		},
		{
			name: "invalid paths are excluded",
			annotations: map[string]string{
				"helm.sh/merge-strategy/":     "append",
				"helm.sh/merge-strategy/.a":   "append",
				"helm.sh/merge-strategy/a.":   "append",
				"helm.sh/merge-strategy/a..b": "append",
			},
			expected: map[string]MergeStrategy{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ExtractMergeStrategies(tt.annotations))
		})
	}
}

func TestCoalesceValuesWithMergeStrategies(t *testing.T) {
	annotations := map[string]string{
		"helm.sh/merge-strategy/args":            "append",
		"helm.sh/merge-strategy/env":             "merge",
		"helm.sh/merge-key/env":                  "name",
		"helm.sh/merge-strategy/app.containers":  "merge",
		"helm.sh/merge-key/app.containers":       "meta.name",
		"helm.sh/merge-strategy/mergeNoKey":      "merge",
		"helm.sh/merge-strategy/notInDefaults":   "append",
		"helm.sh/merge-strategy/scalarInDefault": "append",
	}
	defaults := `
args: [--a, --b]
env:
  - name: A
    value: a
    extra: keep
  - name: B
    value: b
  - just-a-string
  - value: no-name
app:
  containers:
    - meta: {name: main}
      image: main:1
      ports: [80]
    - meta: {name: sidecar}
      image: sidecar:1
mergeNoKey: [1]
scalarInDefault: nope
plain: [x]
`
	tests := []struct {
		name     string
		user     string
		expected string
	}{
		{
			name:     "defaults are used when the user sets nothing",
			user:     `{}`,
			expected: defaults,
		},
		{
			name:     "append concatenates defaults before user elements",
			user:     `args: [--c]`,
			expected: strings.Replace(defaults, "args: [--a, --b]", "args: [--a, --b, --c]", 1),
		},
		{
			name:     "append does not repeat defaults already present",
			user:     `args: [--a, --b, --c]`,
			expected: strings.Replace(defaults, "args: [--a, --b]", "args: [--a, --b, --c]", 1),
		},
		{
			name: "merge by key",
			user: `
env:
  - name: B
    value: user-b
  - name: C
    value: c
  - other-string
  - just-a-string
`,
			expected: `
args: [--a, --b]
env:
  - name: A
    value: a
    extra: keep
  - name: B
    value: user-b
  - just-a-string
  - value: no-name
  - name: C
    value: c
  - other-string
app:
  containers:
    - meta: {name: main}
      image: main:1
      ports: [80]
    - meta: {name: sidecar}
      image: sidecar:1
mergeNoKey: [1]
scalarInDefault: nope
plain: [x]
`,
		},
		{
			name: "merge by nested key and nulls delete fields",
			user: `
app:
  containers:
    - meta: {name: sidecar}
      image: sidecar:2
    - meta: {name: main}
      ports: [443]
      image: null
`,
			expected: `
args: [--a, --b]
env:
  - name: A
    value: a
    extra: keep
  - name: B
    value: b
  - just-a-string
  - value: no-name
app:
  containers:
    - meta: {name: main}
      ports: [443]
    - meta: {name: sidecar}
      image: sidecar:2
mergeNoKey: [1]
scalarInDefault: nope
plain: [x]
`,
		},
		{
			name: "merge without key appends, arrays without strategies are replaced",
			user: `
mergeNoKey: [2]
notInDefaults: [a]
scalarInDefault: [b]
plain: [y]
`,
			expected: `
args: [--a, --b]
env:
  - name: A
    value: a
    extra: keep
  - name: B
    value: b
  - just-a-string
  - value: no-name
app:
  containers:
    - meta: {name: main}
      image: main:1
      ports: [80]
    - meta: {name: sidecar}
      image: sidecar:1
mergeNoKey: [1, 2]
notInDefaults: [a]
scalarInDefault: [b]
plain: [y]
`,
		},
		{
			name:     "null user value deletes the key",
			user:     `args: null`,
			expected: strings.Replace(defaults, "args: [--a, --b]\n", "", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defaultValues := mustValues(t, defaults)
			c := strategyChart("root", annotations, defaultValues)
			v, err := CoalesceValues(c, mustValues(t, tt.user))
			require.NoError(t, err)
			assert.Equal(t, mustValues(t, tt.expected), map[string]any(v))
			assert.Equal(t, mustValues(t, defaults), defaultValues, "chart defaults must not be modified")
		})
	}
}

func TestMergeValuesWithMergeStrategiesPreservesNil(t *testing.T) {
	c := strategyChart("root", map[string]string{
		"helm.sh/merge-strategy/list": "append",
		"helm.sh/merge-strategy/env":  "merge",
		"helm.sh/merge-key/env":       "name",
	}, mustValues(t, `
list: [a]
removed: [z]
env:
  - name: A
    value: a
`))

	v, err := MergeValues(c, mustValues(t, `
list: [b, null]
removed: null
env:
  - name: A
    value: null
`))
	require.NoError(t, err)
	assert.Equal(t, mustValues(t, `
list: [a, b, null]
removed: null
env:
  - name: A
    value: null
`), map[string]any(v))
}

func TestMergeStrategiesAreChartScoped(t *testing.T) {
	sub := strategyChart("sub", map[string]string{
		"helm.sh/merge-strategy/list": "append",
	}, mustValues(t, `
list: [sub-default]
other: [sub-other]
`))
	parent := strategyChart("parent", map[string]string{
		"helm.sh/merge-strategy/list":      "append",
		"helm.sh/merge-strategy/sub.other": "append",
	}, mustValues(t, `
list: [parent-default]
sub:
  other: [parent-other]
`), sub)

	v, err := CoalesceValues(parent, mustValues(t, `
list: [user]
sub:
  list: [user-sub]
  other: [user-other]
`))
	require.NoError(t, err)
	assert.Equal(t, mustValues(t, `
list: [parent-default, user]
sub:
  list: [sub-default, user-sub]
  other: [parent-other, user-other]
  global: {}
`), map[string]any(v))
}

func TestMergeStrategiesForGlobals(t *testing.T) {
	sub := strategyChart("sub", map[string]string{
		"helm.sh/merge-strategy/global.hosts": "append",
		"helm.sh/merge-strategy/global.users": "merge",
		"helm.sh/merge-key/global.users":      "name",
	}, mustValues(t, `
global:
  hosts: [sub.example.com]
  users:
    - name: admin
      role: admin
`))
	parent := strategyChart("parent", nil, mustValues(t, `
global:
  hosts: [parent.example.com]
  users:
    - name: admin
      role: owner
    - name: dev
`), sub)

	v, err := CoalesceValues(parent, mustValues(t, `{}`))
	require.NoError(t, err)

	// Parent scope keeps plain replacement semantics.
	assert.Equal(t, []any{"parent.example.com"}, v["global"].(map[string]any)["hosts"])

	subGlobals := v["sub"].(map[string]any)["global"].(map[string]any)
	assert.Equal(t, []any{"sub.example.com", "parent.example.com"}, subGlobals["hosts"])
	assert.Equal(t, mustValues(t, `
users:
  - name: admin
    role: owner
  - name: dev
`)["users"], subGlobals["users"])
}

// Coalescing values that were already coalesced must not duplicate array
// elements. Helm does this when dependency processing stores coalesced
// subchart values in the parent chart.
func TestMergeStrategiesRepeatedCoalescing(t *testing.T) {
	sub := strategyChart("sub", map[string]string{
		"helm.sh/merge-strategy/list":         "append",
		"helm.sh/merge-strategy/env":          "merge",
		"helm.sh/merge-key/env":               "name",
		"helm.sh/merge-strategy/global.hosts": "append",
	}, mustValues(t, `
list: [d]
env: [{name: A, value: a}, keyless]
global:
  hosts: [sub-host]
`))
	parent := strategyChart("parent", nil, mustValues(t, `
global:
  hosts: [parent-host]
sub:
  list: [p]
  env: [{name: B}]
`), sub)

	baked, err := MergeValues(parent, nil)
	require.NoError(t, err)
	parent.Values = baked

	v, err := CoalesceValues(parent, mustValues(t, `{}`))
	require.NoError(t, err)
	subVals := v["sub"].(map[string]any)
	assert.Equal(t, []any{"d", "p"}, subVals["list"])
	assert.Equal(t, mustValues(t, `env: [{name: A, value: a}, keyless, {name: B}]`)["env"], subVals["env"])
	assert.Equal(t, []any{"sub-host", "parent-host"}, subVals["global"].(map[string]any)["hosts"])
}

func TestMergeStrategiesV3Chart(t *testing.T) {
	c := &v3chart.Chart{
		Metadata: &v3chart.Metadata{Name: "v3", Annotations: map[string]string{
			"helm.sh/merge-strategy/list": "append",
		}},
		Values: mustValues(t, `list: [a]`),
	}
	v, err := CoalesceValues(c, mustValues(t, `list: [b]`))
	require.NoError(t, err)
	assert.Equal(t, []any{"a", "b"}, v["list"])
}

func TestCoalesceTablesWithStrategies(t *testing.T) {
	strategies := map[string]MergeStrategy{
		"list":      {Strategy: MergeStrategyAppend},
		"nested.kv": {Strategy: MergeStrategyMerge, MergeKey: "k"},
	}
	src := mustValues(t, `
list: [old]
plain: [old]
nested:
  kv: [{k: a, v: old}, {k: b, v: old}]
removed: [old]
`)
	dst := mustValues(t, `
list: [new]
plain: [new]
nested:
  kv: [{k: b, v: new}, {k: c, v: new}]
removed: null
`)
	result := CoalesceTablesWithStrategies(dst, src, strategies)
	assert.Equal(t, mustValues(t, `
list: [old, new]
plain: [new]
nested:
  kv: [{k: a, v: old}, {k: b, v: new}, {k: c, v: new}]
`), result)

	assert.Equal(t, map[string]any{"a": 1}, CoalesceTablesWithStrategies(nil, map[string]any{"a": 1}, strategies))
	assert.Equal(t, map[string]any{"a": 1}, CoalesceTablesWithStrategies(map[string]any{"a": 1}, nil, strategies))
}

func TestMergeArraysByKeyValueTypes(t *testing.T) {
	defaults := []any{
		map[string]any{"id": 1, "v": "int"},
		map[string]any{"id": true, "v": "bool"},
		map[string]any{"id": []any{"x"}, "v": "list"},
	}
	user := []any{
		map[string]any{"id": float64(1), "v": "float"},
		map[string]any{"id": true, "v": "user-bool"},
		map[string]any{"id": []any{"x"}, "v": "user-list"},
	}
	result := mergeArraysByKey(func(string, ...any) {}, defaults, user, []string{"id"}, "", false)
	assert.Equal(t, []any{
		map[string]any{"id": float64(1), "v": "float"},
		map[string]any{"id": true, "v": "user-bool"},
		map[string]any{"id": []any{"x"}, "v": "list"},
		map[string]any{"id": []any{"x"}, "v": "user-list"},
	}, result)
}

func TestValidateMergeStrategies(t *testing.T) {
	values := mustValues(t, `
list: [a]
env: [{name: a}]
scalar: 1
nested:
  items: []
`)
	tests := []struct {
		name        string
		annotations map[string]string
		values      map[string]any
		expected    []string
	}{
		{
			name: "valid",
			annotations: map[string]string{
				"helm.sh/merge-strategy/list":         "append",
				"helm.sh/merge-strategy/env":          "merge",
				"helm.sh/merge-key/env":               "name",
				"helm.sh/merge-strategy/nested.items": "append",
				"unrelated":                           "value",
			},
			values: values,
		},
		{
			name: "unsupported strategy",
			annotations: map[string]string{
				"helm.sh/merge-strategy/list": "prepend",
			},
			values:   values,
			expected: []string{`unsupported merge strategy "prepend" for path "list"`},
		},
		{
			name: "merge without merge key",
			annotations: map[string]string{
				"helm.sh/merge-strategy/env": "merge",
			},
			values:   values,
			expected: []string{`merge strategy "merge" for path "env" requires`},
		},
		{
			name: "orphan merge key",
			annotations: map[string]string{
				"helm.sh/merge-key/env": "name",
			},
			values:   values,
			expected: []string{`merge key for path "env" has no corresponding`},
		},
		{
			name: "path not found and non-array",
			annotations: map[string]string{
				"helm.sh/merge-strategy/missing":        "append",
				"helm.sh/merge-strategy/nested.missing": "append",
				"helm.sh/merge-strategy/scalar":         "append",
			},
			values: values,
			expected: []string{
				`merge strategy path "missing" not found`,
				`merge strategy path "nested.missing" not found`,
				`merge strategy path "scalar" resolves to a non-array value`,
			},
		},
		{
			name: "paths are not checked without values",
			annotations: map[string]string{
				"helm.sh/merge-strategy/missing": "append",
			},
		},
		{
			name: "invalid paths",
			annotations: map[string]string{
				"helm.sh/merge-strategy/a..b": "append",
				"helm.sh/merge-key/":          "name",
			},
			values: values,
			expected: []string{
				`merge key annotation "helm.sh/merge-key/" has an invalid path ""`,
				`merge strategy annotation "helm.sh/merge-strategy/a..b" has an invalid path "a..b"`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateMergeStrategies(tt.annotations, tt.values)
			require.Len(t, errs, len(tt.expected), "%v", errs)
			for i, e := range tt.expected {
				assert.Contains(t, errs[i].Error(), e)
			}
		})
	}
}

func TestMergeStrategyOverrideAnnotations(t *testing.T) {
	annotations, err := MergeStrategyOverrideAnnotations(
		[]string{"env=merge", "args = append"},
		[]string{"env=metadata.name"},
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"helm.sh/merge-strategy/env":  "merge",
		"helm.sh/merge-strategy/args": "append",
		"helm.sh/merge-key/env":       "metadata.name",
	}, annotations)

	for _, tt := range []struct {
		strategies []string
		keys       []string
	}{
		{strategies: []string{"env"}},
		{strategies: []string{"=append"}},
		{strategies: []string{"env=replace"}},
		{keys: []string{"env="}},
		{keys: []string{"env"}},
	} {
		_, err := MergeStrategyOverrideAnnotations(tt.strategies, tt.keys)
		assert.Error(t, err, "strategies=%v keys=%v", tt.strategies, tt.keys)
	}
}
