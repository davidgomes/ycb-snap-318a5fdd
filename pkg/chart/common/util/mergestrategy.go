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
	"helm.sh/helm/v4/pkg/chart/common"
)

const (
	// MergeStrategyAnnotationPrefix is the Chart.yaml annotation prefix used to
	// declare an array merge strategy for a values path.
	MergeStrategyAnnotationPrefix = "helm.sh/merge-strategy/"
	// MergeKeyAnnotationPrefix is the Chart.yaml annotation prefix used to
	// declare the key field used by the "merge" strategy for a values path.
	MergeKeyAnnotationPrefix = "helm.sh/merge-key/"

	// MergeStrategyAppend concatenates chart default elements before user elements.
	MergeStrategyAppend = "append"
	// MergeStrategyMerge matches array-of-objects elements by a key field.
	MergeStrategyMerge = "merge"
)

// MergeStrategy describes how an array at a values path is combined.
type MergeStrategy struct {
	Strategy string
	Key      string
}

func validStrategyPath(path string) bool {
	if path == "" {
		return false
	}
	for _, seg := range strings.Split(path, ".") {
		if seg == "" {
			return false
		}
	}
	return true
}

// ExtractMergeStrategies returns the actionable merge strategies declared in
// the given annotations, keyed by values path. A "merge" strategy without a
// merge key is returned as "append". Unsupported strategies and invalid paths
// are excluded.
func ExtractMergeStrategies(annotations map[string]string) map[string]MergeStrategy {
	out := map[string]MergeStrategy{}
	for k, v := range annotations {
		path, ok := strings.CutPrefix(k, MergeStrategyAnnotationPrefix)
		if !ok || !validStrategyPath(path) {
			continue
		}
		switch strings.TrimSpace(v) {
		case MergeStrategyAppend:
			out[path] = MergeStrategy{Strategy: MergeStrategyAppend}
		case MergeStrategyMerge:
			key := strings.TrimSpace(annotations[MergeKeyAnnotationPrefix+path])
			if validStrategyPath(key) {
				out[path] = MergeStrategy{Strategy: MergeStrategyMerge, Key: key}
			} else {
				out[path] = MergeStrategy{Strategy: MergeStrategyAppend}
			}
		}
	}
	return out
}

// ApplyMergeStrategyOverrides returns a copy of annotations with the given
// overrides applied. strategies and keys are in "path=value" format and take
// precedence over annotations for the same path.
func ApplyMergeStrategyOverrides(annotations map[string]string, strategies, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(annotations)+len(strategies)+len(keys))
	for k, v := range annotations {
		out[k] = v
	}
	parse := func(prefix string, entries []string) error {
		for _, e := range entries {
			path, val, ok := strings.Cut(e, "=")
			path = strings.TrimSpace(path)
			if !ok || !validStrategyPath(path) || strings.TrimSpace(val) == "" {
				return fmt.Errorf("invalid merge override %q: expected path=value", e)
			}
			out[prefix+path] = strings.TrimSpace(val)
		}
		return nil
	}
	if err := parse(MergeStrategyAnnotationPrefix, strategies); err != nil {
		return nil, err
	}
	if err := parse(MergeKeyAnnotationPrefix, keys); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidateMergeStrategyAnnotations checks merge strategy annotations for
// problems and returns one error per problem found. When values is non-nil,
// strategy paths are also checked against it.
func ValidateMergeStrategyAnnotations(annotations map[string]string, values map[string]any) []error {
	var errs []error
	keys := make([]string, 0, len(annotations))
	for k := range annotations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.TrimSpace(annotations[k])
		if path, ok := strings.CutPrefix(k, MergeStrategyAnnotationPrefix); ok {
			switch v {
			case MergeStrategyAppend, MergeStrategyMerge:
			default:
				errs = append(errs, fmt.Errorf("merge strategy %q for path %q is unsupported (must be %q or %q)", v, path, MergeStrategyAppend, MergeStrategyMerge))
				continue
			}
			if v == MergeStrategyMerge && strings.TrimSpace(annotations[MergeKeyAnnotationPrefix+path]) == "" {
				errs = append(errs, fmt.Errorf("merge strategy for path %q requires annotation %s%s", path, MergeKeyAnnotationPrefix, path))
			}
			if values != nil && validStrategyPath(path) {
				val, found := lookupPath(values, path)
				if !found {
					errs = append(errs, fmt.Errorf("merge strategy path %q not found in chart values", path))
				} else if _, isArr := val.([]any); !isArr {
					errs = append(errs, fmt.Errorf("merge strategy path %q resolves to a non-array value", path))
				}
			}
		} else if path, ok := strings.CutPrefix(k, MergeKeyAnnotationPrefix); ok {
			if _, has := annotations[MergeStrategyAnnotationPrefix+path]; !has {
				errs = append(errs, fmt.Errorf("merge key for path %q has no corresponding %s%s annotation", path, MergeStrategyAnnotationPrefix, path))
			}
		}
	}
	return errs
}

func lookupPath(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, seg := range strings.Split(path, ".") {
		cm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = cm[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(m map[string]any, path string, val any) {
	segs := strings.Split(path, ".")
	for _, seg := range segs[:len(segs)-1] {
		next, ok := m[seg].(map[string]any)
		if !ok {
			return
		}
		m = next
	}
	m[segs[len(segs)-1]] = val
}

// applyMergeStrategies pre-merges annotated arrays of defaults into user,
// writing the merged arrays into user. Only paths where both sides hold an
// array are affected.
func applyMergeStrategies(printf printFn, strategies map[string]MergeStrategy, user, defaults map[string]any, merge bool) {
	for path, s := range strategies {
		uv, ok := lookupPath(user, path)
		if !ok {
			continue
		}
		ua, ok := uv.([]any)
		if !ok {
			continue
		}
		dv, ok := lookupPath(defaults, path)
		if !ok {
			continue
		}
		da, ok := dv.([]any)
		if !ok {
			continue
		}
		setPath(user, path, mergeArrays(printf, s, ua, da, path, merge))
	}
}

func mergeArrays(printf printFn, s MergeStrategy, user, defaults []any, path string, merge bool) []any {
	if c, err := copystructure.Copy(defaults); err == nil {
		defaults = c.([]any)
	} else {
		printf("warning: unable to copy array for %s, err: %s", path, err)
	}
	if s.Strategy != MergeStrategyMerge || s.Key == "" {
		out := make([]any, 0, len(defaults)+len(user))
		out = append(out, defaults...)
		return append(out, user...)
	}

	keyOf := func(e any) (string, bool) {
		m, ok := e.(map[string]any)
		if !ok {
			return "", false
		}
		v, ok := lookupPath(m, s.Key)
		if !ok || v == nil {
			return "", false
		}
		return fmt.Sprintf("%T:%v", v, v), true
	}

	userIdx := map[string]int{}
	for i, e := range user {
		if k, ok := keyOf(e); ok {
			if _, dup := userIdx[k]; !dup {
				userIdx[k] = i
			}
		}
	}

	used := make([]bool, len(user))
	out := make([]any, 0, len(defaults)+len(user))
	for _, d := range defaults {
		k, ok := keyOf(d)
		if !ok {
			out = append(out, d)
			continue
		}
		i, matched := userIdx[k]
		if !matched || used[i] {
			out = append(out, d)
			continue
		}
		used[i] = true
		um := copyAny(user[i]).(map[string]any)
		out = append(out, coalesceTablesFullKey(printf, um, d.(map[string]any), path, merge))
	}
	for i, e := range user {
		if !used[i] {
			out = append(out, e)
		}
	}
	return out
}

func copyAny(v any) any {
	c, err := copystructure.Copy(v)
	if err != nil {
		return v
	}
	return c
}

func globalStrategies(strategies map[string]MergeStrategy) map[string]MergeStrategy {
	out := map[string]MergeStrategy{}
	for p, s := range strategies {
		if rest, ok := strings.CutPrefix(p, common.GlobalKey+"."); ok && rest != "" {
			out[rest] = s
		}
	}
	return out
}

// CoalesceTablesWithStrategies merges src into dst like CoalesceTables, first
// combining arrays at the annotated paths using the given strategies. src
// elements are treated as defaults (placed first for "append").
func CoalesceTablesWithStrategies(dst, src map[string]any, strategies map[string]MergeStrategy) map[string]any {
	if dst != nil && src != nil && len(strategies) > 0 {
		applyMergeStrategies(log.Printf, strategies, dst, src, false)
	}
	return CoalesceTables(dst, src)
}
