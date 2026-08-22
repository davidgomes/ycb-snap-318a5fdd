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
	"fmt"
	"strings"

	"helm.sh/helm/v4/internal/copystructure"
)

const (
	// MergeStrategyAnnotationPrefix is the Chart.yaml annotation prefix for array merge strategies.
	MergeStrategyAnnotationPrefix = "helm.sh/merge-strategy/"
	// MergeKeyAnnotationPrefix is the Chart.yaml annotation prefix for array merge keys.
	MergeKeyAnnotationPrefix = "helm.sh/merge-key/"

	// MergeStrategyAppend concatenates chart defaults before user elements.
	MergeStrategyAppend = "append"
	// MergeStrategyMerge key-merges arrays of objects.
	MergeStrategyMerge = "merge"
)

// MergeStrategy describes how an array path should be combined during coalescing.
type MergeStrategy struct {
	Path     string
	Strategy string
	MergeKey string
}

// ParsePathValuePairs parses string slices in path=value format.
func ParsePathValuePairs(items []string) map[string]string {
	out := make(map[string]string, len(items))
	for _, item := range items {
		path, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		path = strings.TrimSpace(path)
		if !validStrategyPath(path) {
			continue
		}
		out[path] = strings.TrimSpace(value)
	}
	return out
}

// ExtractMergeStrategies returns only actionable strategies from chart annotations.
// Entries with "merge" that lack a companion merge-key are returned as "append".
// Annotations with empty or invalid paths are excluded.
func ExtractMergeStrategies(annotations map[string]string) []MergeStrategy {
	return extractMergeStrategies(annotations, nil, nil)
}

// ExtractMergeStrategiesWithOverrides extracts chart strategies and overlays CLI
// overrides. Overrides take precedence for the same path.
func ExtractMergeStrategiesWithOverrides(annotations map[string]string, mergeStrategies, mergeKeys []string) []MergeStrategy {
	return extractMergeStrategies(annotations, mergeStrategies, mergeKeys)
}

// ApplyMergeStrategyOverrides writes CLI path=value overrides onto chart annotations.
func ApplyMergeStrategyOverrides(annotations map[string]string, mergeStrategies, mergeKeys []string) map[string]string {
	if annotations == nil {
		annotations = map[string]string{}
	}
	for path, value := range ParsePathValuePairs(mergeStrategies) {
		annotations[MergeStrategyAnnotationPrefix+path] = value
	}
	for path, value := range ParsePathValuePairs(mergeKeys) {
		annotations[MergeKeyAnnotationPrefix+path] = value
	}
	return annotations
}

func extractMergeStrategies(annotations map[string]string, mergeStrategies, mergeKeys []string) []MergeStrategy {
	byPath := make(map[string]*MergeStrategy)

	for key, value := range annotations {
		switch {
		case strings.HasPrefix(key, MergeStrategyAnnotationPrefix):
			path := strings.TrimPrefix(key, MergeStrategyAnnotationPrefix)
			if !validStrategyPath(path) {
				continue
			}
			s := byPath[path]
			if s == nil {
				s = &MergeStrategy{Path: path}
				byPath[path] = s
			}
			s.Strategy = strings.TrimSpace(value)
		case strings.HasPrefix(key, MergeKeyAnnotationPrefix):
			path := strings.TrimPrefix(key, MergeKeyAnnotationPrefix)
			if !validStrategyPath(path) {
				continue
			}
			s := byPath[path]
			if s == nil {
				s = &MergeStrategy{Path: path}
				byPath[path] = s
			}
			s.MergeKey = strings.TrimSpace(value)
		}
	}

	for path, value := range ParsePathValuePairs(mergeStrategies) {
		if value != MergeStrategyAppend && value != MergeStrategyMerge {
			continue
		}
		s := byPath[path]
		if s == nil {
			s = &MergeStrategy{Path: path}
			byPath[path] = s
		}
		s.Strategy = value
	}
	for path, value := range ParsePathValuePairs(mergeKeys) {
		s := byPath[path]
		if s == nil {
			s = &MergeStrategy{Path: path}
			byPath[path] = s
		}
		s.MergeKey = value
	}

	out := make([]MergeStrategy, 0, len(byPath))
	for _, s := range byPath {
		if s.Strategy == "" {
			continue
		}
		if s.Strategy == MergeStrategyMerge && s.MergeKey == "" {
			s.Strategy = MergeStrategyAppend
		}
		if s.Strategy != MergeStrategyAppend && s.Strategy != MergeStrategyMerge {
			continue
		}
		out = append(out, *s)
	}
	return out
}

func validStrategyPath(path string) bool {
	if path == "" {
		return false
	}
	if strings.TrimSpace(path) != path {
		return false
	}
	if strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") {
		return false
	}
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			return false
		}
	}
	return true
}

// ValidateMergeStrategyAnnotations returns lint warnings for merge-strategy annotations.
func ValidateMergeStrategyAnnotations(annotations map[string]string, values map[string]any) []error {
	if len(annotations) == 0 {
		return nil
	}

	type info struct {
		strategy string
		key      string
		hasStrat bool
		hasKey   bool
	}
	byPath := map[string]*info{}

	for key, value := range annotations {
		switch {
		case strings.HasPrefix(key, MergeStrategyAnnotationPrefix):
			path := strings.TrimPrefix(key, MergeStrategyAnnotationPrefix)
			if path == "" {
				continue
			}
			i := byPath[path]
			if i == nil {
				i = &info{}
				byPath[path] = i
			}
			i.strategy = strings.TrimSpace(value)
			i.hasStrat = true
		case strings.HasPrefix(key, MergeKeyAnnotationPrefix):
			path := strings.TrimPrefix(key, MergeKeyAnnotationPrefix)
			if path == "" {
				continue
			}
			i := byPath[path]
			if i == nil {
				i = &info{}
				byPath[path] = i
			}
			i.key = strings.TrimSpace(value)
			i.hasKey = true
		}
	}

	var errs []error
	for path, i := range byPath {
		if i.hasKey && !i.hasStrat {
			errs = append(errs, fmt.Errorf("merge-key annotation for path %s has no merge-strategy", path))
			continue
		}
		if !i.hasStrat {
			continue
		}
		if i.strategy != MergeStrategyAppend && i.strategy != MergeStrategyMerge {
			errs = append(errs, fmt.Errorf("unsupported merge strategy %q for path %s", i.strategy, path))
		}
		if i.strategy == MergeStrategyMerge && i.key == "" {
			errs = append(errs, fmt.Errorf("merge strategy for path %s requires a merge-key", path))
		}
		if values != nil {
			val, ok := lookupPath(values, path)
			if !ok {
				errs = append(errs, fmt.Errorf("merge strategy path %s not found in chart values", path))
			} else if _, isArr := asArray(val); !isArr {
				errs = append(errs, fmt.Errorf("merge strategy path %s resolves to a non-array value", path))
			}
		}
	}
	return errs
}

// applyMergeStrategies pre-merges annotated arrays from chart defaults into user values.
// Chart arrays are deep-copied before being combined. dest (user) is mutated.
func applyMergeStrategies(dest, src map[string]any, strategies []MergeStrategy, merge bool) {
	if dest == nil || src == nil || len(strategies) == 0 {
		return
	}
	for _, s := range strategies {
		userVal, userOK := lookupPath(dest, s.Path)
		chartVal, chartOK := lookupPath(src, s.Path)
		if !userOK || !chartOK {
			continue
		}
		if userVal == nil {
			// Null handling is left to the existing coalescing loop.
			continue
		}
		userArr, userIsArr := asArray(userVal)
		chartArr, chartIsArr := asArray(chartVal)
		if !userIsArr || !chartIsArr {
			continue
		}
		copied, err := copystructure.Copy(chartArr)
		if err != nil {
			continue
		}
		chartCopy, ok := asArray(copied)
		if !ok {
			chartCopy = chartArr
		}
		var combined []any
		switch s.Strategy {
		case MergeStrategyMerge:
			combined = mergeArraysByKey(chartCopy, userArr, s.MergeKey, merge)
		default:
			combined = append(append([]any{}, chartCopy...), userArr...)
		}
		setPath(dest, s.Path, combined)
	}
}

func mergeArraysByKey(defaults, user []any, mergeKey string, preserveNil bool) []any {
	result := make([]any, 0, len(defaults)+len(user))
	usedUser := make([]bool, len(user))

	for _, def := range defaults {
		defMap, defIsMap := def.(map[string]any)
		if !defIsMap {
			result = append(result, def)
			continue
		}
		defKey, defHasKey := lookupPath(defMap, mergeKey)
		if !defHasKey || defKey == nil {
			result = append(result, def)
			continue
		}
		matched := false
		for i, u := range user {
			if usedUser[i] {
				continue
			}
			uMap, uIsMap := u.(map[string]any)
			if !uIsMap {
				continue
			}
			uKey, uHasKey := lookupPath(uMap, mergeKey)
			if !uHasKey || !keysEqual(defKey, uKey) {
				continue
			}
			result = append(result, mergeMatchedMaps(defMap, uMap, preserveNil))
			usedUser[i] = true
			matched = true
			break
		}
		if !matched {
			result = append(result, def)
		}
	}

	for i, u := range user {
		if usedUser[i] {
			continue
		}
		result = append(result, u)
	}
	return result
}

func mergeMatchedMaps(defaults, user map[string]any, preserveNil bool) map[string]any {
	out := copyMapDeep(defaults)
	for k, uv := range user {
		if uv == nil && !preserveNil {
			delete(out, k)
			continue
		}
		if dv, ok := out[k]; ok && istable(dv) && istable(uv) {
			out[k] = mergeMatchedMaps(dv.(map[string]any), uv.(map[string]any), preserveNil)
			continue
		}
		out[k] = uv
	}
	return out
}

func copyMapDeep(src map[string]any) map[string]any {
	copied, err := copystructure.Copy(src)
	if err != nil {
		return copyMap(src)
	}
	if m, ok := copied.(map[string]any); ok {
		return m
	}
	return copyMap(src)
}

func lookupPath(m map[string]any, path string) (any, bool) {
	if m == nil || path == "" {
		return nil, false
	}
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		mp, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mp[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(m map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	cur := m
	for i, part := range parts {
		if i == len(parts)-1 {
			cur[part] = val
			return
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
}

func deletePath(m map[string]any, path string) {
	parts := strings.Split(path, ".")
	cur := m
	for i, part := range parts {
		if i == len(parts)-1 {
			delete(cur, part)
			return
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}

func asArray(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	default:
		return nil, false
	}
}

func keysEqual(a, b any) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func strategiesForPrefix(strategies []MergeStrategy, prefix string) []MergeStrategy {
	var out []MergeStrategy
	for _, s := range strategies {
		if s.Path == prefix || strings.HasPrefix(s.Path, prefix+".") {
			stripped := strings.TrimPrefix(s.Path, prefix+".")
			if s.Path == prefix {
				continue
			}
			cp := s
			cp.Path = stripped
			out = append(out, cp)
		}
	}
	return out
}
