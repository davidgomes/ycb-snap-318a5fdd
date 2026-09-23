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
	"sort"
	"strings"

	"helm.sh/helm/v4/internal/copystructure"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
)

const (
	// MergeStrategyAnnotationPrefix marks a values path whose array should be
	// appended or key-merged instead of replaced.
	// The annotation key is helm.sh/merge-strategy/<dot-path>.
	MergeStrategyAnnotationPrefix = "helm.sh/merge-strategy/"
	// MergeKeyAnnotationPrefix sets the object field used to match elements
	// when the strategy is merge. The key may itself be a dotted path.
	// The annotation key is helm.sh/merge-key/<dot-path>.
	MergeKeyAnnotationPrefix = "helm.sh/merge-key/"

	// MergeStrategyAppend concatenates chart defaults before user elements.
	MergeStrategyAppend = "append"
	// MergeStrategyMerge matches array objects by merge key and recursively
	// merges matched pairs. User fields win.
	MergeStrategyMerge = "merge"
)

// MergeOverrides carries CLI merge strategy overrides.
// Pair values use path=value form. CLI pairs take precedence over chart
// annotations for the same path.
type MergeOverrides struct {
	MergeStrategies []string
	MergeKeys       []string
	// Disable ignores chart annotations and CLI overrides. Upgrade --reset-values
	// and the render pass of --reuse-values use this.
	Disable bool
}

type coalesceSettings struct {
	strategyPairs []string
	keyPairs      []string
	disable       bool
}

func (o MergeOverrides) settings() coalesceSettings {
	return coalesceSettings{
		strategyPairs: o.MergeStrategies,
		keyPairs:      o.MergeKeys,
		disable:       o.Disable,
	}
}

func (s coalesceSettings) strategiesFor(ch chart.Charter) (map[string]string, map[string]string) {
	if s.disable || ch == nil {
		return nil, nil
	}
	return ResolveMergeStrategies(chartAnnotations(ch), s.strategyPairs, s.keyPairs)
}

func chartAnnotations(ch chart.Charter) map[string]string {
	ac, err := chart.NewAccessor(ch)
	if err != nil || ac == nil {
		return nil
	}
	return ac.Annotations()
}

// ExtractMergeStrategies returns actionable strategies declared by chart annotations.
// A "merge" entry without a companion merge key is returned as "append".
// Annotations with an empty or invalid path are excluded. Unsupported strategy
// values are excluded.
func ExtractMergeStrategies(annotations map[string]string) (map[string]string, map[string]string) {
	return ResolveMergeStrategies(annotations, nil, nil)
}

// ResolveMergeStrategies overlays CLI path=value pairs onto chart annotations
// and returns actionable strategies. CLI values win for the same path.
func ResolveMergeStrategies(annotations map[string]string, strategyPairs, keyPairs []string) (map[string]string, map[string]string) {
	merged := make(map[string]string, len(annotations))
	for k, v := range annotations {
		merged[k] = v
	}
	for path, val := range parsePathPairs(strategyPairs) {
		if !validDotPath(path) {
			continue
		}
		switch strings.TrimSpace(val) {
		case MergeStrategyAppend, MergeStrategyMerge:
			merged[MergeStrategyAnnotationPrefix+path] = strings.TrimSpace(val)
		}
	}
	for path, val := range parsePathPairs(keyPairs) {
		if !validDotPath(path) || !validDotPath(strings.TrimSpace(val)) {
			continue
		}
		merged[MergeKeyAnnotationPrefix+path] = strings.TrimSpace(val)
	}

	rawStrategies := map[string]string{}
	rawKeys := map[string]string{}
	for k, v := range merged {
		if path, ok := strings.CutPrefix(k, MergeStrategyAnnotationPrefix); ok {
			if !validDotPath(path) {
				continue
			}
			rawStrategies[path] = strings.TrimSpace(v)
			continue
		}
		if path, ok := strings.CutPrefix(k, MergeKeyAnnotationPrefix); ok {
			if !validDotPath(path) {
				continue
			}
			key := strings.TrimSpace(v)
			if !validDotPath(key) {
				continue
			}
			rawKeys[path] = key
		}
	}

	strategies := map[string]string{}
	keys := map[string]string{}
	for path, strategy := range rawStrategies {
		switch strategy {
		case MergeStrategyMerge:
			if key, ok := rawKeys[path]; ok {
				strategies[path] = MergeStrategyMerge
				keys[path] = key
			} else {
				strategies[path] = MergeStrategyAppend
			}
		case MergeStrategyAppend:
			strategies[path] = MergeStrategyAppend
		}
	}
	return strategies, keys
}

// LintMergeStrategyAnnotations reports Chart.yaml merge-strategy problems.
// Warnings cover unsupported strategy values, merge without a merge key,
// orphan merge keys, paths missing from chart defaults, and paths that do
// not resolve to an array.
func LintMergeStrategyAnnotations(annotations map[string]string, values map[string]any) []error {
	if len(annotations) == 0 {
		return nil
	}
	strategies := map[string]string{}
	keys := map[string]string{}
	for k, v := range annotations {
		if path, ok := strings.CutPrefix(k, MergeStrategyAnnotationPrefix); ok {
			if !validDotPath(path) {
				continue
			}
			strategies[path] = strings.TrimSpace(v)
			continue
		}
		if path, ok := strings.CutPrefix(k, MergeKeyAnnotationPrefix); ok {
			if !validDotPath(path) {
				continue
			}
			keys[path] = strings.TrimSpace(v)
		}
	}

	var errs []error
	paths := make([]string, 0, len(strategies)+len(keys))
	seen := map[string]struct{}{}
	addPath := func(p string) {
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	for p := range strategies {
		addPath(p)
	}
	for p := range keys {
		addPath(p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		strategy, hasStrategy := strategies[path]
		_, hasKey := keys[path]
		if hasStrategy {
			switch strategy {
			case MergeStrategyAppend, MergeStrategyMerge:
			default:
				errs = append(errs, fmt.Errorf("unsupported merge strategy %q for path %q", strategy, path))
			}
			if strategy == MergeStrategyMerge && !hasKey {
				errs = append(errs, fmt.Errorf("merge strategy for path %q requires a merge key", path))
			}
		} else if hasKey {
			errs = append(errs, fmt.Errorf("merge key for path %q has no merge strategy", path))
		}
		if !hasStrategy {
			continue
		}
		val, found := lookupValuesPath(values, path)
		if !found {
			errs = append(errs, fmt.Errorf("merge strategy path %q was not found in chart default values", path))
			continue
		}
		if _, ok := asSlice(val); !ok {
			errs = append(errs, fmt.Errorf("merge strategy path %q resolves to a non-array value", path))
		}
	}
	return errs
}

func parsePathPairs(pairs []string) map[string]string {
	out := map[string]string{}
	for _, pair := range pairs {
		path, val, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		out[path] = val
	}
	return out
}

func validDotPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	for _, part := range strings.Split(path, ".") {
		if strings.TrimSpace(part) == "" {
			return false
		}
	}
	return true
}

func lookupValuesPath(values map[string]any, path string) (any, bool) {
	var cur any = values
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
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

func strategiesForGlobalScope(strategies, keys map[string]string) (map[string]string, map[string]string) {
	prefix := common.GlobalKey + "."
	outS := map[string]string{}
	outK := map[string]string{}
	for path, strategy := range strategies {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok || !validDotPath(rest) {
			continue
		}
		outS[rest] = strategy
		if key, ok := keys[path]; ok {
			outK[rest] = key
		}
	}
	return outS, outK
}

// applyArrayStrategies pre-merges annotated arrays into dest.
// src is the lower-precedence map (chart defaults, or old config). dest is
// authoritative. Chart/src arrays are deep-copied before they are combined.
// A nil dest value is left untouched so coalescing can delete it and merging
// can preserve it.
func applyArrayStrategies(dest, src map[string]any, strategies, keys map[string]string, preserveNil bool) {
	if len(strategies) == 0 || dest == nil || src == nil {
		return
	}
	paths := make([]string, 0, len(strategies))
	for path := range strategies {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		applyStrategyAtPath(dest, src, path, strategies[path], keys[path], strategies, keys, preserveNil)
	}
}

func applyStrategyAtPath(dest, src map[string]any, path, strategy, mergeKey string, strategies, keys map[string]string, preserveNil bool) {
	dCur := dest
	sCur := src
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		dv, ok := dCur[part]
		if !ok {
			return
		}
		sv, ok := sCur[part]
		if !ok {
			return
		}
		dm, ok := dv.(map[string]any)
		if !ok {
			return
		}
		sm, ok := sv.(map[string]any)
		if !ok {
			return
		}
		dCur = dm
		sCur = sm
	}
	last := parts[len(parts)-1]
	uv, ok := dCur[last]
	if !ok || uv == nil {
		return
	}
	sv, ok := sCur[last]
	if !ok || sv == nil {
		return
	}
	merged, applied := mergeStrategyArrays(uv, sv, strategy, mergeKey, path, strategies, keys, preserveNil)
	if applied {
		dCur[last] = merged
	}
}

func mergeStrategyArrays(user, base any, strategy, mergeKey, path string, strategies, keys map[string]string, preserveNil bool) (any, bool) {
	uArr, uok := asSlice(user)
	bArr, bok := asSlice(base)
	if !uok || !bok {
		return nil, false
	}
	bCopy, ok := asSlice(deepCopyAny(bArr))
	if !ok {
		bCopy = bArr
	}
	if strategy != MergeStrategyMerge || mergeKey == "" {
		return appendArrays(bCopy, uArr), true
	}
	return mergeArraysByKey(uArr, bCopy, mergeKey, path, strategies, keys, preserveNil), true
}

func appendArrays(base, user []any) []any {
	out := make([]any, 0, len(base)+len(user))
	out = append(out, base...)
	for _, el := range user {
		out = append(out, deepCopyAny(el))
	}
	return out
}

func mergeArraysByKey(user, base []any, mergeKey, path string, strategies, keys map[string]string, preserveNil bool) []any {
	result := make([]any, len(base))
	copy(result, base)

	positions := map[string][]int{}
	for i, el := range result {
		if k, ok := elementMergeKey(el, mergeKey); ok {
			positions[k] = append(positions[k], i)
		}
	}
	used := make([]bool, len(result))
	extras := make([]any, 0)
	for _, el := range user {
		k, ok := elementMergeKey(el, mergeKey)
		if !ok {
			extras = append(extras, deepCopyAny(el))
			continue
		}
		idx := -1
		for _, cand := range positions[k] {
			if !used[cand] {
				idx = cand
				break
			}
		}
		if idx == -1 {
			extras = append(extras, deepCopyAny(el))
			continue
		}
		used[idx] = true
		baseMap, _ := result[idx].(map[string]any)
		userMap, _ := el.(map[string]any)
		result[idx] = mergeMaps(userMap, baseMap, path, strategies, keys, preserveNil)
	}
	return append(result, extras...)
}

func mergeMaps(user, base map[string]any, path string, strategies, keys map[string]string, preserveNil bool) map[string]any {
	copied := deepCopyAny(base)
	out, ok := copied.(map[string]any)
	if !ok || out == nil {
		out = map[string]any{}
	}
	for k, uv := range user {
		child := k
		if path != "" {
			child = path + "." + k
		}
		if uv == nil {
			if preserveNil {
				out[k] = nil
			} else {
				delete(out, k)
			}
			continue
		}
		dv, exists := out[k]
		if exists {
			if strategy, ok := strategies[child]; ok {
				if merged, applied := mergeStrategyArrays(uv, dv, strategy, keys[child], child, strategies, keys, preserveNil); applied {
					out[k] = merged
					continue
				}
			}
			if um, uok := uv.(map[string]any); uok {
				if dm, dok := dv.(map[string]any); dok {
					out[k] = mergeMaps(um, dm, child, strategies, keys, preserveNil)
					continue
				}
			}
		}
		out[k] = deepCopyAny(uv)
	}
	return out
}

func elementMergeKey(el any, keyPath string) (string, bool) {
	m, ok := el.(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := valueAtPath(m, keyPath)
	if !ok || v == nil {
		return "", false
	}
	return fmt.Sprint(v), true
}

func valueAtPath(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		table, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = table[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func asSlice(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	if s, ok := v.([]any); ok {
		return s, true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Slice {
		return nil, false
	}
	if rv.Type().Elem().Kind() == reflect.Uint8 {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

func deepCopyAny(v any) any {
	if v == nil {
		return nil
	}
	copied, err := copystructure.Copy(v)
	if err != nil || copied == nil {
		return v
	}
	return copied
}

// CoalesceTablesWithStrategies merges src into dst.
// dst is authoritative. Annotated arrays follow append (src before dst) or
// key merge instead of replacement. Other values use standard table coalescing.
func CoalesceTablesWithStrategies(dst, src map[string]any, annotations map[string]string, strategyPairs, keyPairs []string) map[string]any {
	strategies, keys := ResolveMergeStrategies(annotations, strategyPairs, keyPairs)
	applyArrayStrategies(dst, src, strategies, keys, false)
	return coalesceTablesFullKey(log.Printf, dst, src, "", false)
}
