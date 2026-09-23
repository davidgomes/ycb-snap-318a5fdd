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
	"fmt"
	"reflect"
	"strings"

	"helm.sh/helm/v4/internal/copystructure"
	"helm.sh/helm/v4/pkg/chart/common"
)

const (
	// MergeStrategyPrefix is the Chart.yaml annotation prefix for array merge strategies.
	// The remainder of the key is a dot-separated values path.
	MergeStrategyPrefix = "helm.sh/merge-strategy/"
	// MergeKeyPrefix is the Chart.yaml annotation prefix for the identity field used by
	// the "merge" strategy. The remainder of the key is a dot-separated values path.
	MergeKeyPrefix = "helm.sh/merge-key/"

	// StrategyAppend concatenates chart defaults (or the lower-precedence array)
	// before the higher-precedence elements.
	StrategyAppend = "append"
	// StrategyMerge matches array-of-object elements by a key and merges matched pairs.
	StrategyMerge = "merge"

	globalPathPrefix = common.GlobalKey + "."
)

// ArrayStrategy is an actionable per-path array merge strategy.
type ArrayStrategy struct {
	Path     string
	Strategy string
	// Key is the identity field for StrategyMerge. It may be a dotted path.
	Key string
}

// ExtractArrayStrategies returns actionable strategies from chart annotations.
//
// CLI overrides (path=value entries) replace annotations for the same path.
// A "merge" strategy without a companion merge key is returned as "append".
// Empty or invalid paths are excluded. Unsupported strategy values are excluded.
func ExtractArrayStrategies(annotations map[string]string, strategyOverrides, keyOverrides []string) map[string]ArrayStrategy {
	strategies := map[string]string{}
	keys := map[string]string{}

	for ann, value := range annotations {
		if path, ok := strings.CutPrefix(ann, MergeStrategyPrefix); ok {
			if validPath(path) {
				strategies[path] = strings.TrimSpace(value)
			}
			continue
		}
		if path, ok := strings.CutPrefix(ann, MergeKeyPrefix); ok {
			if validPath(path) {
				keys[path] = strings.TrimSpace(value)
			}
		}
	}

	for path, value := range parsePathOverrides(strategyOverrides) {
		strategies[path] = value
	}
	for path, value := range parsePathOverrides(keyOverrides) {
		keys[path] = value
	}

	out := make(map[string]ArrayStrategy)
	for path, strategy := range strategies {
		switch strategy {
		case StrategyAppend:
			out[path] = ArrayStrategy{Path: path, Strategy: StrategyAppend}
		case StrategyMerge:
			key := keys[path]
			if !validPath(key) {
				out[path] = ArrayStrategy{Path: path, Strategy: StrategyAppend}
				continue
			}
			out[path] = ArrayStrategy{Path: path, Strategy: StrategyMerge, Key: key}
		}
	}
	return out
}

// LintMergeStrategyAnnotations reports Chart.yaml merge-strategy problems and
// checks strategy paths against chart default values.
func LintMergeStrategyAnnotations(annotations map[string]string, values map[string]any) []error {
	if len(annotations) == 0 {
		return nil
	}
	strategies := map[string]string{}
	keys := map[string]string{}
	var errs []error

	for ann, value := range annotations {
		if path, ok := strings.CutPrefix(ann, MergeStrategyPrefix); ok {
			if !validPath(path) {
				continue
			}
			strategy := strings.TrimSpace(value)
			strategies[path] = strategy
			if strategy != StrategyAppend && strategy != StrategyMerge {
				errs = append(errs, fmt.Errorf("unsupported merge strategy %q for path %q", strategy, path))
			}
			continue
		}
		if path, ok := strings.CutPrefix(ann, MergeKeyPrefix); ok {
			if validPath(path) {
				keys[path] = strings.TrimSpace(value)
			}
		}
	}

	for path, strategy := range strategies {
		if strategy == StrategyMerge {
			if strings.TrimSpace(keys[path]) == "" {
				errs = append(errs, fmt.Errorf("merge strategy for path %q requires a merge-key", path))
			}
		}
		errs = append(errs, validateStrategyPath(values, path)...)
	}
	for path := range keys {
		if _, ok := strategies[path]; !ok {
			errs = append(errs, fmt.Errorf("merge-key for path %q has no merge strategy", path))
		}
	}
	return errs
}

func validateStrategyPath(values map[string]any, path string) []error {
	val, ok := lookupPath(values, path)
	if !ok {
		return []error{fmt.Errorf("merge strategy path %q was not found in chart values", path)}
	}
	if _, isArray := asSlice(val); !isArray {
		return []error{fmt.Errorf("merge strategy path %q resolves to a non-array value", path)}
	}
	return nil
}

func parsePathOverrides(items []string) map[string]string {
	out := map[string]string{}
	for _, item := range items {
		path, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		path = strings.TrimSpace(path)
		value = strings.TrimSpace(value)
		if !validPath(path) || value == "" {
			continue
		}
		out[path] = value
	}
	return out
}

func validPath(path string) bool {
	if path == "" || strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") {
		return false
	}
	if strings.Contains(path, "..") {
		return false
	}
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			return false
		}
	}
	return true
}

// ApplyArrayStrategies pre-merges annotated arrays into dest.
// user is higher precedence than chart. Null user values delete keys.
// Chart arrays are deep-copied.
func ApplyArrayStrategies(dest, user, chart map[string]any, specs map[string]ArrayStrategy) {
	applyArrayStrategies(dest, user, chart, specs, false, false)
}

// applyArrayStrategies pre-merges annotated arrays into dest.
//
// user is the higher-precedence map and chart holds defaults (or the
// lower-precedence map). dest receives the merged arrays and is typically
// the same map as user. Chart arrays are deep-copied before use.
// Paths prefixed with "global." are skipped when skipGlobal is set so
// subchart global strategies can be applied separately.
func applyArrayStrategies(dest, user, chart map[string]any, specs map[string]ArrayStrategy, preserveNil, skipGlobal bool) {
	if len(specs) == 0 || dest == nil || user == nil || chart == nil {
		return
	}
	for path, spec := range specs {
		if skipGlobal && strings.HasPrefix(path, globalPathPrefix) {
			continue
		}
		applyStrategyPath(dest, user, chart, strings.Split(path, "."), spec, preserveNil)
	}
}

// globalStrategies returns specs declared on a subchart for paths under global.,
// with the global. prefix removed so they apply to a globals map.
func globalStrategies(specs map[string]ArrayStrategy) map[string]ArrayStrategy {
	out := map[string]ArrayStrategy{}
	for path, spec := range specs {
		stripped, ok := strings.CutPrefix(path, globalPathPrefix)
		if !ok || !validPath(stripped) {
			continue
		}
		spec.Path = stripped
		out[stripped] = spec
	}
	return out
}

func applyStrategyPath(dest, user, chart map[string]any, parts []string, spec ArrayStrategy, preserveNil bool) {
	if len(parts) == 0 || dest == nil || user == nil || chart == nil {
		return
	}
	key := parts[0]
	if len(parts) == 1 {
		uv, uok := user[key]
		cv, cok := chart[key]
		if !uok || !cok {
			return
		}
		ua, uok := asSlice(uv)
		ca, cok := asSlice(cv)
		if !uok || !cok {
			return
		}
		dest[key] = combineArrays(ca, ua, spec, preserveNil)
		return
	}
	dm, dok := asMap(dest[key])
	um, uok := asMap(user[key])
	cm, cok := asMap(chart[key])
	if !dok || !uok || !cok {
		return
	}
	applyStrategyPath(dm, um, cm, parts[1:], spec, preserveNil)
}

func combineArrays(chartArr, userArr []any, spec ArrayStrategy, preserveNil bool) []any {
	switch spec.Strategy {
	case StrategyMerge:
		return mergeArraysByKey(chartArr, userArr, spec.Key, preserveNil)
	default:
		out := make([]any, 0, len(chartArr)+len(userArr))
		for _, el := range chartArr {
			out = append(out, deepCopyValue(el))
		}
		out = append(out, userArr...)
		return out
	}
}

func mergeArraysByKey(chartArr, userArr []any, key string, preserveNil bool) []any {
	used := make([]bool, len(userArr))
	out := make([]any, 0, len(chartArr)+len(userArr))

	for _, raw := range chartArr {
		el := deepCopyValue(raw)
		dm, ok := asMap(el)
		if !ok {
			out = append(out, el)
			continue
		}
		id, ok := valueAtPath(dm, key)
		if !ok {
			out = append(out, el)
			continue
		}
		matched := false
		for i, uraw := range userArr {
			if used[i] {
				continue
			}
			um, uok := asMap(uraw)
			if !uok {
				continue
			}
			uid, uok := valueAtPath(um, key)
			if !uok || !reflect.DeepEqual(id, uid) {
				continue
			}
			out = append(out, mergeObjects(um, dm, preserveNil))
			used[i] = true
			matched = true
			break
		}
		if !matched {
			out = append(out, el)
		}
	}

	for i, uraw := range userArr {
		if used[i] {
			continue
		}
		out = append(out, uraw)
	}
	return out
}

// mergeObjects overlays user onto a deep copy of chart. User fields win.
// Nested maps are merged. When preserveNil is false, a null user value deletes
// the key; when true, the null is kept.
func mergeObjects(user, chart map[string]any, preserveNil bool) map[string]any {
	out := map[string]any{}
	copied := deepCopyValue(chart)
	if cm, ok := asMap(copied); ok {
		out = cm
	}
	for k, uv := range user {
		if uv == nil {
			if preserveNil {
				out[k] = nil
			} else {
				delete(out, k)
			}
			continue
		}
		if cv, ok := out[k]; ok {
			if um, uok := asMap(uv); uok {
				if cm, cok := asMap(cv); cok {
					out[k] = mergeObjects(um, cm, preserveNil)
					continue
				}
			}
		}
		out[k] = uv
	}
	return out
}

func lookupPath(values map[string]any, path string) (any, bool) {
	var cur any = values
	for _, part := range strings.Split(path, ".") {
		m, ok := asMap(cur)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func valueAtPath(v any, path string) (any, bool) {
	cur := v
	for _, part := range strings.Split(path, ".") {
		m, ok := asMap(cur)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case common.Values:
		return map[string]any(m), true
	default:
		return nil, false
	}
}

func asSlice(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

func deepCopyValue(v any) any {
	if v == nil {
		return nil
	}
	copied, err := copystructure.Copy(v)
	if err != nil {
		return v
	}
	return copied
}

// skipSubchartPaths drops strategies whose first path segment names a subchart.
// Those paths belong to the subchart's own coalescing, not the parent.
func skipSubchartPaths(specs map[string]ArrayStrategy, subs map[string]struct{}) map[string]ArrayStrategy {
	if len(subs) == 0 {
		return specs
	}
	out := make(map[string]ArrayStrategy, len(specs))
	for path, spec := range specs {
		first, _, _ := strings.Cut(path, ".")
		if _, ok := subs[first]; ok {
			continue
		}
		out[path] = spec
	}
	return out
}
