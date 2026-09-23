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
	"sort"
	"strings"

	"helm.sh/helm/v4/internal/copystructure"
	chart "helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
)

const (
	// MergeStrategyAnnotationPrefix is the Chart.yaml annotation prefix used to
	// declare the merge strategy of the array at the values path that follows it.
	MergeStrategyAnnotationPrefix = "helm.sh/merge-strategy/"
	// MergeKeyAnnotationPrefix is the Chart.yaml annotation prefix used to
	// declare the key field used by the "merge" strategy for the array at the
	// values path that follows it.
	MergeKeyAnnotationPrefix = "helm.sh/merge-key/"

	// MergeStrategyAppend concatenates chart default elements before user elements.
	MergeStrategyAppend = "append"
	// MergeStrategyMerge matches array-of-object elements by a key field and
	// recursively merges matched pairs, with user fields taking precedence.
	MergeStrategyMerge = "merge"
)

var globalPrefix = common.GlobalKey + "."

// ArrayMergeStrategy describes how a user supplied array is combined with the
// chart default array at the same values path.
type ArrayMergeStrategy struct {
	// Strategy is either MergeStrategyAppend or MergeStrategyMerge.
	Strategy string
	// MergeKey is the dotted path of the field used to match elements when
	// Strategy is MergeStrategyMerge.
	MergeKey string
}

// ExtractMergeStrategies returns the actionable array merge strategies declared
// in chart annotations, keyed by values path.
//
// A "merge" strategy without a companion merge key is returned as "append".
// Annotations with empty or invalid paths, or unsupported strategies, are
// excluded.
func ExtractMergeStrategies(annotations map[string]string) map[string]ArrayMergeStrategy {
	return resolveMergeStrategies(rawMergeStrategies(annotations))
}

// ParseMergeStrategyOverrides parses merge strategy and merge key overrides in
// "path=value" format, as supplied on the command line. The returned overrides
// can be passed to the functions accepting merge strategy overrides, where they
// take precedence over chart annotations for the same path.
func ParseMergeStrategyOverrides(strategies, keys []string) (map[string]ArrayMergeStrategy, error) {
	overrides := make(map[string]ArrayMergeStrategy)
	for _, s := range strategies {
		path, value, err := parseOverride(s)
		if err != nil {
			return nil, fmt.Errorf("invalid merge strategy %q: %w", s, err)
		}
		if value != MergeStrategyAppend && value != MergeStrategyMerge {
			return nil, fmt.Errorf("invalid merge strategy %q: unsupported strategy %q, must be %q or %q", s, value, MergeStrategyAppend, MergeStrategyMerge)
		}
		o := overrides[path]
		o.Strategy = value
		overrides[path] = o
	}
	for _, k := range keys {
		path, value, err := parseOverride(k)
		if err != nil {
			return nil, fmt.Errorf("invalid merge key %q: %w", k, err)
		}
		if !validMergePath(value) {
			return nil, fmt.Errorf("invalid merge key %q: invalid key path %q", k, value)
		}
		o := overrides[path]
		o.MergeKey = value
		overrides[path] = o
	}
	return overrides, nil
}

func parseOverride(s string) (string, string, error) {
	path, value, ok := strings.Cut(s, "=")
	if !ok {
		return "", "", fmt.Errorf("expected format path=value")
	}
	path, value = strings.TrimSpace(path), strings.TrimSpace(value)
	if !validMergePath(path) {
		return "", "", fmt.Errorf("invalid path %q", path)
	}
	return path, value, nil
}

// ChartMergeStrategies returns the actionable array merge strategies for a
// single chart (not its subcharts), combining its annotations with overrides.
// Overrides take precedence over annotations for the same path.
func ChartMergeStrategies(chrt chart.Charter, overrides map[string]ArrayMergeStrategy) map[string]ArrayMergeStrategy {
	ch, err := chart.NewAccessor(chrt)
	if err != nil {
		return nil
	}
	raw := rawMergeStrategies(ch.Annotations())
	for path, o := range overrides {
		r := raw[path]
		if o.Strategy != "" {
			r.Strategy = o.Strategy
		}
		if o.MergeKey != "" {
			r.MergeKey = o.MergeKey
		}
		raw[path] = r
	}
	return resolveMergeStrategies(raw)
}

func rawMergeStrategies(annotations map[string]string) map[string]ArrayMergeStrategy {
	raw := make(map[string]ArrayMergeStrategy)
	for k, v := range annotations {
		if path, ok := strings.CutPrefix(k, MergeStrategyAnnotationPrefix); ok {
			r := raw[path]
			r.Strategy = strings.TrimSpace(v)
			raw[path] = r
		} else if path, ok := strings.CutPrefix(k, MergeKeyAnnotationPrefix); ok {
			r := raw[path]
			r.MergeKey = strings.TrimSpace(v)
			raw[path] = r
		}
	}
	return raw
}

func resolveMergeStrategies(raw map[string]ArrayMergeStrategy) map[string]ArrayMergeStrategy {
	out := make(map[string]ArrayMergeStrategy)
	for path, r := range raw {
		if !validMergePath(path) {
			continue
		}
		switch r.Strategy {
		case MergeStrategyAppend:
			out[path] = ArrayMergeStrategy{Strategy: MergeStrategyAppend}
		case MergeStrategyMerge:
			if validMergePath(r.MergeKey) {
				out[path] = ArrayMergeStrategy{Strategy: MergeStrategyMerge, MergeKey: r.MergeKey}
			} else {
				out[path] = ArrayMergeStrategy{Strategy: MergeStrategyAppend}
			}
		}
	}
	return out
}

// globalMergeStrategies returns the strategies declared for paths under
// "global.", with that prefix stripped.
func globalMergeStrategies(strategies map[string]ArrayMergeStrategy) map[string]ArrayMergeStrategy {
	out := make(map[string]ArrayMergeStrategy)
	for path, s := range strategies {
		if p, ok := strings.CutPrefix(path, globalPrefix); ok {
			out[p] = s
		}
	}
	return out
}

func validMergePath(path string) bool {
	if path == "" {
		return false
	}
	for _, seg := range strings.Split(path, ".") {
		if strings.TrimSpace(seg) == "" {
			return false
		}
	}
	return true
}

// ValidateMergeStrategyAnnotations checks merge strategy annotations for
// problems, validating strategy paths against the chart default values. It
// returns one error per problem found, in a stable order. If values is nil,
// strategy paths are not checked against the default values.
func ValidateMergeStrategyAnnotations(annotations map[string]string, values map[string]any) []error {
	raw := rawMergeStrategies(annotations)
	paths := make([]string, 0, len(raw))
	for path := range raw {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var errs []error
	for _, path := range paths {
		r := raw[path]
		if !validMergePath(path) {
			errs = append(errs, fmt.Errorf("invalid merge strategy annotation path %q", path))
			continue
		}
		switch r.Strategy {
		case "":
			errs = append(errs, fmt.Errorf("merge key annotation %q for path %q has no corresponding %q annotation", MergeKeyAnnotationPrefix+path, path, MergeStrategyAnnotationPrefix+path))
			continue
		case MergeStrategyAppend:
		case MergeStrategyMerge:
			if r.MergeKey == "" {
				errs = append(errs, fmt.Errorf("merge strategy %q for path %q requires a %q annotation", MergeStrategyMerge, path, MergeKeyAnnotationPrefix+path))
			} else if !validMergePath(r.MergeKey) {
				errs = append(errs, fmt.Errorf("invalid merge key %q for path %q", r.MergeKey, path))
			}
		default:
			errs = append(errs, fmt.Errorf("unsupported merge strategy %q for path %q, must be %q or %q", r.Strategy, path, MergeStrategyAppend, MergeStrategyMerge))
			continue
		}

		if values == nil {
			continue
		}
		v, ok := lookupPath(values, path)
		if !ok {
			errs = append(errs, fmt.Errorf("merge strategy path %q not found in chart default values", path))
		} else if _, isArr := v.([]any); !isArr {
			errs = append(errs, fmt.Errorf("merge strategy path %q resolves to a non-array value (%T) in chart default values", path, v))
		}
	}
	return errs
}

// CoalesceValuesWithMergeStrategies is CoalesceValues with merge strategy
// overrides applied to the top-level chart. Overrides take precedence over the
// chart's annotations for the same path.
func CoalesceValuesWithMergeStrategies(chrt chart.Charter, vals map[string]any, overrides map[string]ArrayMergeStrategy) (common.Values, error) {
	valsCopy, err := copyValues(vals)
	if err != nil {
		return vals, err
	}
	return coalesceWithOverrides(log.Printf, chrt, valsCopy, "", false, overrides)
}

// CoalesceTablesWithMergeStrategies merges a source map into a destination
// map like CoalesceTables, except that arrays at the given paths are combined
// according to their strategy, with src elements treated as the defaults
// (e.g. "append" places src elements before dst elements).
//
// dest is considered authoritative.
func CoalesceTablesWithMergeStrategies(dst, src map[string]any, strategies map[string]ArrayMergeStrategy) map[string]any {
	if src == nil {
		return dst
	}
	if dst == nil {
		return src
	}
	applyMergeStrategies(log.Printf, dst, src, strategies, false)
	return coalesceTablesFullKey(log.Printf, dst, src, "", false)
}

// RestoreMergeStrategyPaths resets, in coalesced, every array path covered by
// a merge strategy of chrt (including overrides) or of its subcharts to the
// value found at the same path in original, removing it when original does not
// contain it.
//
// Merge strategies such as "append" are not idempotent. Use this before storing
// already coalesced values as chart defaults, so that strategies are not
// applied a second time when those defaults are coalesced again.
func RestoreMergeStrategyPaths(chrt chart.Charter, coalesced, original map[string]any, overrides map[string]ArrayMergeStrategy) {
	restoreMergeStrategyPaths(chrt, coalesced, original, ChartMergeStrategies(chrt, overrides))
}

func restoreMergeStrategyPaths(chrt chart.Charter, coalesced, original map[string]any, strategies map[string]ArrayMergeStrategy) {
	if coalesced == nil {
		return
	}
	for path := range strategies {
		if v, ok := lookupPath(original, path); ok {
			setPath(coalesced, path, deepCopy(v))
		} else {
			deletePath(coalesced, path)
		}
	}
	ch, err := chart.NewAccessor(chrt)
	if err != nil {
		return
	}
	for _, subchart := range ch.Dependencies() {
		sub, err := chart.NewAccessor(subchart)
		if err != nil {
			continue
		}
		cSub, ok := coalesced[sub.Name()].(map[string]any)
		if !ok {
			continue
		}
		oSub, _ := original[sub.Name()].(map[string]any)
		restoreMergeStrategyPaths(subchart, cSub, oSub, ChartMergeStrategies(subchart, nil))
	}
}

// applyMergeStrategies pre-merges the arrays at each strategy path of user
// with the corresponding chart default arrays, storing the result in user.
// Paths where either side is missing or not an array are left untouched, so
// the regular coalescing rules apply to them.
func applyMergeStrategies(printf printFn, user, defaults map[string]any, strategies map[string]ArrayMergeStrategy, merge bool) {
	for path, s := range strategies {
		uv, ok := lookupPath(user, path)
		if !ok || uv == nil {
			continue
		}
		userArr, ok := uv.([]any)
		if !ok {
			continue
		}
		dv, ok := lookupPath(defaults, path)
		if !ok {
			continue
		}
		chartArr, ok := dv.([]any)
		if !ok {
			continue
		}
		setPath(user, path, mergeArrays(printf, chartArr, userArr, s, path, merge))
	}
}

// mergeArrays combines chart default elements with user elements according to
// the strategy. Chart elements are deep-copied so chart defaults are never
// mutated.
func mergeArrays(printf printFn, chartArr, userArr []any, s ArrayMergeStrategy, path string, merge bool) []any {
	defaults, ok := deepCopy(chartArr).([]any)
	if !ok {
		defaults = append([]any(nil), chartArr...)
	}

	if s.Strategy != MergeStrategyMerge {
		result := make([]any, 0, len(defaults)+len(userArr))
		result = append(result, defaults...)
		return append(result, userArr...)
	}

	userIdx := make(map[string]int)
	for i, e := range userArr {
		if k, ok := mergeKeyOf(e, s.MergeKey); ok {
			if _, dup := userIdx[k]; !dup {
				userIdx[k] = i
			}
		}
	}

	used := make(map[int]bool)
	result := make([]any, 0, len(defaults)+len(userArr))
	for _, d := range defaults {
		k, ok := mergeKeyOf(d, s.MergeKey)
		if !ok {
			result = append(result, d)
			continue
		}
		i, found := userIdx[k]
		if !found || used[i] {
			result = append(result, d)
			continue
		}
		used[i] = true
		userElem, ok := deepCopy(userArr[i]).(map[string]any)
		if !ok {
			userElem = copyMap(userArr[i].(map[string]any))
		}
		result = append(result, coalesceTablesFullKey(printf, userElem, d.(map[string]any), fmt.Sprintf("%s[%s=%s]", path, s.MergeKey, k), merge))
	}
	for i, u := range userArr {
		if !used[i] {
			result = append(result, u)
		}
	}
	return result
}

// mergeKeyOf returns the string form of the scalar at keyPath in element e.
func mergeKeyOf(e any, keyPath string) (string, bool) {
	m, ok := e.(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := lookupPath(m, keyPath)
	if !ok || v == nil {
		return "", false
	}
	switch v.(type) {
	case map[string]any, []any:
		return "", false
	}
	return fmt.Sprint(v), true
}

func lookupPath(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, seg := range strings.Split(path, ".") {
		table, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = table[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// setPath sets val at path in m, creating intermediate tables as needed. It
// does nothing if an intermediate value exists and is not a table.
func setPath(m map[string]any, path string, val any) {
	segs := strings.Split(path, ".")
	cur := m
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg]
		if !ok {
			t := make(map[string]any)
			cur[seg] = t
			cur = t
			continue
		}
		t, ok := next.(map[string]any)
		if !ok {
			return
		}
		cur = t
	}
	cur[segs[len(segs)-1]] = val
}

func deletePath(m map[string]any, path string) {
	segs := strings.Split(path, ".")
	cur := m
	for _, seg := range segs[:len(segs)-1] {
		t, ok := cur[seg].(map[string]any)
		if !ok {
			return
		}
		cur = t
	}
	delete(cur, segs[len(segs)-1])
}

func deepCopy(v any) any {
	c, err := copystructure.Copy(v)
	if err != nil {
		return v
	}
	return c
}
