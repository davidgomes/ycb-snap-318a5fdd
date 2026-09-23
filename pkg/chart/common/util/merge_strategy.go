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
	"log"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"helm.sh/helm/v4/internal/copystructure"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
)

const (
	// MergeStrategyAnnotationPrefix is the Chart.yaml annotation prefix used to
	// declare the merge strategy for the array at the values path that follows
	// the prefix, e.g. "helm.sh/merge-strategy/env: append".
	MergeStrategyAnnotationPrefix = "helm.sh/merge-strategy/"
	// MergeKeyAnnotationPrefix is the Chart.yaml annotation prefix used to
	// declare the field that identifies elements of an array using the
	// "merge" strategy, e.g. "helm.sh/merge-key/env: name".
	MergeKeyAnnotationPrefix = "helm.sh/merge-key/"

	// MergeStrategyAppend concatenates chart default elements before user
	// supplied elements.
	MergeStrategyAppend = "append"
	// MergeStrategyMerge matches array-of-object elements by a merge key and
	// recursively merges matched pairs, with user supplied fields winning.
	MergeStrategyMerge = "merge"
)

// MergeStrategy describes how the array at a values path is combined with the
// chart default array during coalescing, instead of being replaced.
type MergeStrategy struct {
	// Strategy is either MergeStrategyAppend or MergeStrategyMerge.
	Strategy string
	// MergeKey is the (possibly dotted) path of the field identifying array
	// elements. It is only set when Strategy is MergeStrategyMerge.
	MergeKey string
}

// ExtractMergeStrategies returns the actionable merge strategies declared in
// chart annotations, keyed by values path.
//
// Annotations with empty or invalid paths and unsupported strategies are
// excluded. A "merge" strategy without a valid companion merge key is returned
// as "append".
func ExtractMergeStrategies(annotations map[string]string) map[string]MergeStrategy {
	strategies := make(map[string]MergeStrategy)
	for name, value := range annotations {
		path, ok := strings.CutPrefix(name, MergeStrategyAnnotationPrefix)
		if !ok || !validValuesPath(path) {
			continue
		}
		switch strings.TrimSpace(value) {
		case MergeStrategyAppend:
			strategies[path] = MergeStrategy{Strategy: MergeStrategyAppend}
		case MergeStrategyMerge:
			key := strings.TrimSpace(annotations[MergeKeyAnnotationPrefix+path])
			if validValuesPath(key) {
				strategies[path] = MergeStrategy{Strategy: MergeStrategyMerge, MergeKey: key}
			} else {
				strategies[path] = MergeStrategy{Strategy: MergeStrategyAppend}
			}
		}
	}
	return strategies
}

// ValidateMergeStrategies checks merge strategy annotations and returns one
// error per problem found, in a stable order.
//
// When values is non-nil, each supported strategy path is also checked to
// exist in values and to resolve to an array.
func ValidateMergeStrategies(annotations map[string]string, values map[string]any) []error {
	var errs []error
	for _, name := range sortedKeys(annotations) {
		value := annotations[name]
		if path, ok := strings.CutPrefix(name, MergeStrategyAnnotationPrefix); ok {
			if !validValuesPath(path) {
				errs = append(errs, fmt.Errorf("merge strategy annotation %q has an invalid path %q", name, path))
				continue
			}
			strategy := strings.TrimSpace(value)
			switch strategy {
			case MergeStrategyAppend:
			case MergeStrategyMerge:
				key, hasKey := annotations[MergeKeyAnnotationPrefix+path]
				key = strings.TrimSpace(key)
				if !hasKey || key == "" {
					errs = append(errs, fmt.Errorf("merge strategy %q for path %q requires a %q annotation; falling back to %q", MergeStrategyMerge, path, MergeKeyAnnotationPrefix+path, MergeStrategyAppend))
				} else if !validValuesPath(key) {
					errs = append(errs, fmt.Errorf("merge key %q for path %q is invalid; falling back to %q", key, path, MergeStrategyAppend))
				}
			default:
				errs = append(errs, fmt.Errorf("unsupported merge strategy %q for path %q; supported strategies are %q and %q", strategy, path, MergeStrategyAppend, MergeStrategyMerge))
				continue
			}
			if values == nil {
				continue
			}
			v, found := lookupPath(values, splitPath(path))
			if !found {
				errs = append(errs, fmt.Errorf("merge strategy path %q not found in chart default values", path))
			} else if _, isArray := asSlice(v); !isArray {
				errs = append(errs, fmt.Errorf("merge strategy path %q resolves to a non-array value (%T)", path, v))
			}
		} else if path, ok := strings.CutPrefix(name, MergeKeyAnnotationPrefix); ok {
			if !validValuesPath(path) {
				errs = append(errs, fmt.Errorf("merge key annotation %q has an invalid path %q", name, path))
				continue
			}
			if _, hasStrategy := annotations[MergeStrategyAnnotationPrefix+path]; !hasStrategy {
				errs = append(errs, fmt.Errorf("merge key for path %q has no corresponding %q annotation", path, MergeStrategyAnnotationPrefix+path))
			}
		}
	}
	return errs
}

// MergeStrategyOverrideAnnotations converts merge strategy and merge key
// overrides given in "path=value" form into the equivalent chart annotations.
func MergeStrategyOverrideAnnotations(strategies, keys []string) (map[string]string, error) {
	annotations := make(map[string]string, len(strategies)+len(keys))
	for _, s := range strategies {
		path, value, err := parseOverride(s)
		if err != nil {
			return nil, fmt.Errorf("invalid merge strategy %q: %w", s, err)
		}
		if value != MergeStrategyAppend && value != MergeStrategyMerge {
			return nil, fmt.Errorf("invalid merge strategy %q: unsupported strategy %q; supported strategies are %q and %q", s, value, MergeStrategyAppend, MergeStrategyMerge)
		}
		annotations[MergeStrategyAnnotationPrefix+path] = value
	}
	for _, k := range keys {
		path, value, err := parseOverride(k)
		if err != nil {
			return nil, fmt.Errorf("invalid merge key %q: %w", k, err)
		}
		if !validValuesPath(value) {
			return nil, fmt.Errorf("invalid merge key %q: invalid key path %q", k, value)
		}
		annotations[MergeKeyAnnotationPrefix+path] = value
	}
	return annotations, nil
}

func parseOverride(s string) (string, string, error) {
	path, value, ok := strings.Cut(s, "=")
	if !ok {
		return "", "", fmt.Errorf("expected format path=value")
	}
	path, value = strings.TrimSpace(path), strings.TrimSpace(value)
	if !validValuesPath(path) {
		return "", "", fmt.Errorf("invalid path %q", path)
	}
	return path, value, nil
}

// CoalesceTablesWithStrategies merges a source map into a destination map
// like CoalesceTables, except that arrays at paths with a merge strategy are
// combined with the source arrays (source elements first) instead of being
// replaced.
//
// dest is considered authoritative.
func CoalesceTablesWithStrategies(dst, src map[string]any, strategies map[string]MergeStrategy) map[string]any {
	if dst != nil && src != nil {
		applyMergeStrategies(log.Printf, dst, src, strategies, "", false)
	}
	return coalesceTablesFullKey(log.Printf, dst, src, "", false)
}

// coalesceGlobalsMergeStrategies applies the strategies a subchart declares
// for global paths to the globals just merged into its values (dest), using
// the subchart's default globals as base. The global prefix is stripped from
// the strategy paths since they are applied to the globals map.
func coalesceGlobalsMergeStrategies(printf printFn, dest map[string]any, sub chart.Accessor, prefix string) {
	strategies := make(map[string]MergeStrategy)
	for path, s := range ExtractMergeStrategies(sub.Annotations()) {
		if rest, ok := strings.CutPrefix(path, common.GlobalKey+"."); ok {
			strategies[rest] = s
		}
	}
	if len(strategies) == 0 {
		return
	}
	chartGlobals, ok := sub.Values()[common.GlobalKey].(map[string]any)
	if !ok {
		return
	}
	dg, ok := dest[common.GlobalKey].(map[string]any)
	if !ok {
		return
	}
	// The merged globals share arrays and tables with the parent's globals,
	// which must not be modified.
	dgCopy, err := copystructure.Copy(dg)
	if err != nil {
		printf("warning: unable to copy globals, skipping merge strategies: %s", err)
		return
	}
	dg = dgCopy.(map[string]any)
	applyMergeStrategies(printf, dg, chartGlobals, strategies, concatPrefix(prefix, common.GlobalKey), true)
	dest[common.GlobalKey] = dg
}

// applyMergeStrategies combines, in place, each array in override that has a
// merge strategy with the corresponding array in base. base is never
// modified. Paths missing from either map, nil override values, and non-array
// values are left for regular coalescing.
func applyMergeStrategies(printf printFn, override, base map[string]any, strategies map[string]MergeStrategy, prefix string, merge bool) {
	for _, path := range sortedKeys(strategies) {
		s := strategies[path]
		segments := splitPath(path)

		ov, ok := lookupPath(override, segments)
		if !ok || ov == nil {
			continue
		}
		userArr, ok := asSlice(ov)
		if !ok {
			continue
		}
		bv, ok := lookupPath(base, segments)
		if !ok {
			continue
		}
		defaultArr, ok := asSlice(bv)
		if !ok {
			continue
		}
		copied, err := copystructure.Copy(defaultArr)
		if err != nil {
			printf("warning: unable to copy values for %s, skipping merge strategy: %s", concatPrefix(prefix, path), err)
			continue
		}
		defaultArr = copied.([]any)

		var result []any
		switch s.Strategy {
		case MergeStrategyAppend:
			result = appendArrays(defaultArr, userArr)
		case MergeStrategyMerge:
			result = mergeArraysByKey(printf, defaultArr, userArr, splitPath(s.MergeKey), concatPrefix(prefix, path), merge)
		default:
			continue
		}
		setPath(override, segments, result)
	}
}

// appendArrays returns the defaults followed by the user elements. When the
// user elements already start with the defaults, e.g. because the values were
// coalesced before, they are returned unchanged so repeated coalescing does
// not duplicate the defaults.
func appendArrays(defaults, user []any) []any {
	if len(defaults) == 0 || hasPrefix(user, defaults) {
		return user
	}
	result := make([]any, 0, len(defaults)+len(user))
	result = append(result, defaults...)
	return append(result, user...)
}

// mergeArraysByKey merges user elements into the defaults, matching map
// elements by the value at keyPath. Matched pairs are coalesced with the user
// element winning, unmatched defaults keep their position and unmatched user
// elements are appended. Elements that are not maps or that lack the key are
// preserved; a user element equal to such a default element is not repeated.
func mergeArraysByKey(printf printFn, defaults, user []any, keyPath []string, prefix string, merge bool) []any {
	result := make([]any, 0, len(defaults)+len(user))
	index := make(map[string]int)
	var keyless []int
	for i, d := range defaults {
		result = append(result, d)
		if k, ok := elementKey(d, keyPath); ok {
			if _, dup := index[k]; !dup {
				index[k] = i
			}
		} else {
			keyless = append(keyless, i)
		}
	}

	for _, u := range user {
		k, ok := elementKey(u, keyPath)
		if !ok {
			if j := slices.IndexFunc(keyless, func(i int) bool { return reflect.DeepEqual(defaults[i], u) }); j >= 0 {
				keyless = slices.Delete(keyless, j, j+1)
				continue
			}
			result = append(result, u)
			continue
		}
		if i, found := index[k]; found {
			result[i] = coalesceTablesFullKey(printf, u.(map[string]any), result[i].(map[string]any), fmt.Sprintf("%s[%d]", prefix, i), merge)
			continue
		}
		result = append(result, u)
		index[k] = len(result) - 1
	}
	return result
}

// elementKey returns a comparable representation of the scalar at keyPath in
// a map element.
func elementKey(element any, keyPath []string) (string, bool) {
	m, ok := element.(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := lookupPath(m, keyPath)
	if !ok {
		return "", false
	}
	switch k := v.(type) {
	case string:
		return "s:" + k, true
	case bool:
		return "b:" + strconv.FormatBool(k), true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		f, err := strconv.ParseFloat(fmt.Sprint(k), 64)
		if err != nil {
			return "", false
		}
		return "n:" + strconv.FormatFloat(f, 'g', -1, 64), true
	default:
		return "", false
	}
}

func hasPrefix(s, prefix []any) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i := range prefix {
		if !reflect.DeepEqual(s[i], prefix[i]) {
			return false
		}
	}
	return true
}

// asSlice returns v as []any if it is a slice.
func asSlice(v any) ([]any, bool) {
	if s, ok := v.([]any); ok {
		return s, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice {
		return nil, false
	}
	s := make([]any, rv.Len())
	for i := range s {
		s[i] = rv.Index(i).Interface()
	}
	return s, true
}

func splitPath(path string) []string {
	return strings.Split(path, ".")
}

// validValuesPath reports whether path is a non-empty dot separated path
// without empty segments.
func validValuesPath(path string) bool {
	return path != "" && !slices.Contains(splitPath(path), "")
}

func lookupPath(m map[string]any, segments []string) (any, bool) {
	var cur any = m
	for _, seg := range segments {
		table, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = table[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// setPath sets the value at segments, which must already resolve to a value
// inside m.
func setPath(m map[string]any, segments []string, value any) {
	for _, seg := range segments[:len(segments)-1] {
		m = m[seg].(map[string]any)
	}
	m[segments[len(segments)-1]] = value
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
