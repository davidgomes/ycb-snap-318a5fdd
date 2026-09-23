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
	"errors"
	"fmt"
	"log"
	"maps"
	"reflect"
	"slices"
	"strings"

	"helm.sh/helm/v4/internal/copystructure"
	chart "helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
)

const (
	// MergeStrategyAnnotationPrefix prefixes the Chart.yaml annotation declaring
	// how the array at the dot-notation values path following the prefix is
	// combined with the chart's default array at the same path.
	MergeStrategyAnnotationPrefix = "helm.sh/merge-strategy/"
	// MergeKeyAnnotationPrefix prefixes the Chart.yaml annotation declaring the
	// (dot-notation) field used to match array elements for the merge strategy
	// of the values path following the prefix.
	MergeKeyAnnotationPrefix = "helm.sh/merge-key/"

	// MergeStrategyAppend concatenates the chart default elements before the
	// user supplied elements.
	MergeStrategyAppend = "append"
	// MergeStrategyMerge matches objects by their merge key, recursively merging
	// matched pairs (user supplied fields win), preserving unmatched chart
	// defaults and appending unmatched user supplied elements.
	MergeStrategyMerge = "merge"
)

// MergeStrategy describes how an array of user supplied values is combined with
// the array at the same path in the chart default values, instead of replacing it.
type MergeStrategy struct {
	// Strategy is either MergeStrategyAppend or MergeStrategyMerge.
	Strategy string
	// MergeKey is the dot-notation path of the field used to match elements when
	// Strategy is MergeStrategyMerge.
	MergeKey string
}

// MergeStrategiesFromAnnotations returns the actionable merge strategies declared
// by chart annotations, keyed by values path.
//
// A "merge" strategy without a companion merge key is returned as "append".
// Annotations with an empty or invalid path, or an unsupported strategy, are
// excluded.
func MergeStrategiesFromAnnotations(annotations map[string]string) map[string]MergeStrategy {
	strategies := make(map[string]MergeStrategy)
	for name, value := range annotations {
		path, ok := strings.CutPrefix(name, MergeStrategyAnnotationPrefix)
		if !ok || !isValidValuesPath(path) {
			continue
		}
		switch strings.TrimSpace(value) {
		case MergeStrategyAppend:
			strategies[path] = MergeStrategy{Strategy: MergeStrategyAppend}
		case MergeStrategyMerge:
			key := strings.TrimSpace(annotations[MergeKeyAnnotationPrefix+path])
			if isValidValuesPath(key) {
				strategies[path] = MergeStrategy{Strategy: MergeStrategyMerge, MergeKey: key}
			} else {
				strategies[path] = MergeStrategy{Strategy: MergeStrategyAppend}
			}
		}
	}
	return strategies
}

// ChartMergeStrategies returns the merge strategies declared by a chart and,
// recursively, by its subcharts, keyed by path in the chart's values. Paths
// declared by a subchart are prefixed with the subchart name. When a chart and
// one of its subcharts declare the same path, the chart's strategy wins.
func ChartMergeStrategies(chrt chart.Charter) (map[string]MergeStrategy, error) {
	ch, err := chart.NewAccessor(chrt)
	if err != nil {
		return nil, err
	}
	strategies := make(map[string]MergeStrategy)
	for _, subchart := range ch.Dependencies() {
		sub, err := chart.NewAccessor(subchart)
		if err != nil {
			return nil, err
		}
		subStrategies, err := ChartMergeStrategies(subchart)
		if err != nil {
			return nil, err
		}
		for path, strategy := range subStrategies {
			strategies[concatPrefix(sub.Name(), path)] = strategy
		}
	}
	maps.Copy(strategies, MergeStrategiesFromAnnotations(ch.Annotations()))
	return strategies, nil
}

// ParseMergeStrategyOverrides converts merge strategies given as "path=strategy"
// and merge keys given as "path=key" into the equivalent chart annotations.
func ParseMergeStrategyOverrides(strategies, keys []string) (map[string]string, error) {
	annotations := make(map[string]string, len(strategies)+len(keys))
	for _, s := range strategies {
		path, strategy, err := parseMergeOverride(s)
		if err != nil {
			return nil, fmt.Errorf("invalid merge strategy %q: %w", s, err)
		}
		if strategy != MergeStrategyAppend && strategy != MergeStrategyMerge {
			return nil, fmt.Errorf("invalid merge strategy %q: unsupported strategy %q, must be %q or %q", s, strategy, MergeStrategyAppend, MergeStrategyMerge)
		}
		annotations[MergeStrategyAnnotationPrefix+path] = strategy
	}
	for _, k := range keys {
		path, key, err := parseMergeOverride(k)
		if err != nil {
			return nil, fmt.Errorf("invalid merge key %q: %w", k, err)
		}
		if !isValidValuesPath(key) {
			return nil, fmt.Errorf("invalid merge key %q: invalid key %q", k, key)
		}
		annotations[MergeKeyAnnotationPrefix+path] = key
	}
	return annotations, nil
}

func parseMergeOverride(s string) (string, string, error) {
	path, value, ok := strings.Cut(s, "=")
	if !ok {
		return "", "", errors.New("expected the form path=value")
	}
	path = strings.TrimSpace(path)
	if !isValidValuesPath(path) {
		return "", "", fmt.Errorf("invalid path %q", path)
	}
	return path, strings.TrimSpace(value), nil
}

// ValidateMergeStrategyAnnotations checks the merge strategy and merge key
// annotations of a chart, returning one error per problem found.
//
// When values is not nil, the path of each supported strategy is also checked
// against the chart default values: it must exist and resolve to an array.
func ValidateMergeStrategyAnnotations(annotations map[string]string, values map[string]any) []error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(annotations)) {
		if path, ok := strings.CutPrefix(name, MergeStrategyAnnotationPrefix); ok {
			if !isValidValuesPath(path) {
				errs = append(errs, fmt.Errorf("merge strategy annotation %q has an invalid path", name))
				continue
			}
			switch strategy := strings.TrimSpace(annotations[name]); strategy {
			case MergeStrategyAppend:
			case MergeStrategyMerge:
				if !isValidValuesPath(strings.TrimSpace(annotations[MergeKeyAnnotationPrefix+path])) {
					errs = append(errs, fmt.Errorf("merge strategy %q for path %q requires a merge key in the %q annotation; falling back to %q", strategy, path, MergeKeyAnnotationPrefix+path, MergeStrategyAppend))
				}
			default:
				errs = append(errs, fmt.Errorf("merge strategy %q for path %q is unsupported; supported strategies are %q and %q", strategy, path, MergeStrategyAppend, MergeStrategyMerge))
				continue
			}
			if values == nil {
				continue
			}
			v, found := lookupValue(values, strings.Split(path, "."))
			if !found {
				errs = append(errs, fmt.Errorf("merge strategy path %q not found in chart default values", path))
			} else if _, isArray := toArray(v); !isArray {
				errs = append(errs, fmt.Errorf("merge strategy path %q resolves to a non-array value (%T) in chart default values", path, v))
			}
		} else if path, ok := strings.CutPrefix(name, MergeKeyAnnotationPrefix); ok {
			if _, hasStrategy := annotations[MergeStrategyAnnotationPrefix+path]; !hasStrategy {
				errs = append(errs, fmt.Errorf("merge key for path %q has no matching %q annotation", path, MergeStrategyAnnotationPrefix+path))
			}
		}
	}
	return errs
}

// CoalesceTablesWithStrategies merges a source map into a destination map like
// CoalesceTables, except that arrays at paths with a merge strategy are combined
// with the source arrays instead of replacing them.
//
// dest is considered authoritative: for "append" the src elements come first,
// and for "merge" the dst fields win.
func CoalesceTablesWithStrategies(dst, src map[string]any, strategies map[string]MergeStrategy) map[string]any {
	if dst != nil && src != nil {
		applyMergeStrategies(log.Printf, dst, src, strategies, "", false)
	}
	return coalesceTablesFullKey(log.Printf, dst, src, "", false)
}

// ResetMergeStrategyValues sets the value at each merge strategy path in vals to
// a copy of the value at the same path in base, or removes it from vals when base
// has no value there.
//
// Coalesced values that become the default values of a chart must go through
// this, otherwise the arrays they contain are combined with the chart defaults
// a second time when the values are coalesced again.
func ResetMergeStrategyValues(vals, base map[string]any, strategies map[string]MergeStrategy) {
	if vals == nil {
		return
	}
	for path := range strategies {
		segments := strings.Split(path, ".")
		parentPath, key := segments[:len(segments)-1], segments[len(segments)-1]
		if v, ok := lookupValue(base, segments); ok {
			if parent, ok := ensureTable(vals, parentPath); ok {
				parent[key] = copyValue(v)
			}
		} else if parent, ok := lookupTable(vals, parentPath); ok {
			delete(parent, key)
		}
	}
}

// globalMergeStrategies returns the strategies for paths prefixed with "global.",
// with the prefix stripped so they apply to the globals map.
func globalMergeStrategies(strategies map[string]MergeStrategy) map[string]MergeStrategy {
	globals := make(map[string]MergeStrategy)
	for path, strategy := range strategies {
		if p, ok := strings.CutPrefix(path, common.GlobalKey+"."); ok {
			globals[p] = strategy
		}
	}
	return globals
}

// applyMergeStrategies pre-merges the arrays at the strategy paths: wherever both
// dst and src hold an array, the dst array is replaced with the combination of
// the src (lower precedence) and dst (higher precedence) arrays. Neither input
// array is modified.
func applyMergeStrategies(printf printFn, dst, src map[string]any, strategies map[string]MergeStrategy, prefix string, merge bool) {
	for _, path := range slices.Sorted(maps.Keys(strategies)) {
		segments := strings.Split(path, ".")
		parent, ok := lookupTable(dst, segments[:len(segments)-1])
		if !ok {
			continue
		}
		key := segments[len(segments)-1]
		overlay, ok := toArray(parent[key])
		if !ok {
			continue
		}
		v, ok := lookupValue(src, segments)
		if !ok {
			continue
		}
		base, ok := toArray(v)
		if !ok {
			continue
		}
		parent[key] = combineArrays(printf, base, overlay, strategies[path], concatPrefix(prefix, path), merge)
	}
}

func combineArrays(printf printFn, base, overlay []any, strategy MergeStrategy, fullkey string, merge bool) []any {
	result := copyArray(base)
	overlay = copyArray(overlay)
	if strategy.Strategy != MergeStrategyMerge {
		return append(result, overlay...)
	}

	keyPath := strings.Split(strategy.MergeKey, ".")
	index := make(map[string]int, len(result))
	for i, elem := range result {
		if k, ok := mergeKeyOf(elem, keyPath); ok {
			if _, exists := index[k]; !exists {
				index[k] = i
			}
		}
	}
	for _, elem := range overlay {
		k, ok := mergeKeyOf(elem, keyPath)
		if !ok {
			result = append(result, elem)
			continue
		}
		i, matched := index[k]
		if !matched {
			result = append(result, elem)
			continue
		}
		result[i] = coalesceTablesFullKey(printf, elem.(map[string]any), result[i].(map[string]any), fmt.Sprintf("%s[%d]", fullkey, i), merge)
	}
	return result
}

// mergeKeyOf returns the string form of the merge key of an array element, and
// whether the element is an object holding a scalar value at the key path.
func mergeKeyOf(elem any, keyPath []string) (string, bool) {
	m, ok := elem.(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := lookupValue(m, keyPath)
	if !ok || v == nil {
		return "", false
	}
	if _, isArray := toArray(v); isArray || istable(v) {
		return "", false
	}
	return fmt.Sprint(v), true
}

func isValidValuesPath(path string) bool {
	if path == "" {
		return false
	}
	for segment := range strings.SplitSeq(path, ".") {
		if strings.TrimSpace(segment) == "" {
			return false
		}
	}
	return true
}

// lookupTable returns the table found by following path from m.
func lookupTable(m map[string]any, path []string) (map[string]any, bool) {
	for _, segment := range path {
		next, ok := m[segment].(map[string]any)
		if !ok {
			return nil, false
		}
		m = next
	}
	return m, m != nil
}

// ensureTable is like lookupTable, but creates missing tables along the path.
func ensureTable(m map[string]any, path []string) (map[string]any, bool) {
	for _, segment := range path {
		v, exists := m[segment]
		if !exists {
			v = make(map[string]any)
			m[segment] = v
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		m = next
	}
	return m, true
}

// lookupValue returns the value found by following path from m.
func lookupValue(m map[string]any, path []string) (any, bool) {
	parent, ok := lookupTable(m, path[:len(path)-1])
	if !ok {
		return nil, false
	}
	v, ok := parent[path[len(path)-1]]
	return v, ok
}

// toArray returns v as a []any when it is a slice of any element type.
func toArray(v any) ([]any, bool) {
	if a, ok := v.([]any); ok {
		return a, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice {
		return nil, false
	}
	a := make([]any, rv.Len())
	for i := range a {
		a[i] = rv.Index(i).Interface()
	}
	return a, true
}

func copyArray(a []any) []any {
	c, err := copystructure.Copy(a)
	if err != nil {
		return slices.Clone(a)
	}
	return c.([]any)
}

func copyValue(v any) any {
	if v == nil {
		return nil
	}
	c, err := copystructure.Copy(v)
	if err != nil {
		return v
	}
	return c
}
