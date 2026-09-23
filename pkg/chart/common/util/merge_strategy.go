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
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"helm.sh/helm/v4/internal/copystructure"
	chart "helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
)

const (
	// MergeStrategyPrefix is the Chart.yaml annotation prefix for array merge strategies.
	// The remainder of the key is a dot-separated values path.
	MergeStrategyPrefix = "helm.sh/merge-strategy/"
	// MergeKeyPrefix is the Chart.yaml annotation prefix for array merge keys.
	// The remainder of the key is a dot-separated values path. The value may
	// itself be a dotted path into each array element.
	MergeKeyPrefix = "helm.sh/merge-key/"

	// StrategyAppend concatenates chart defaults before user elements.
	StrategyAppend = "append"
	// StrategyMerge key-merges array-of-object elements.
	StrategyMerge = "merge"
)

// StrategyOverrides carries CLI merge-strategy overrides.
// MergeStrategies and MergeKeys use path=value entries and take precedence
// over chart annotations for the same path. Skip disables strategy application.
type StrategyOverrides struct {
	MergeStrategies []string
	MergeKeys       []string
	Skip            bool
}

// ExtractMergeStrategies returns actionable strategies and their merge keys.
// A "merge" strategy without a companion merge key is returned as "append".
// Annotations and overrides with an empty or invalid path are excluded, as are
// unsupported strategy values. CLI entries override annotations on the same path.
func ExtractMergeStrategies(annotations map[string]string, mergeStrategies, mergeKeys []string) (map[string]string, map[string]string) {
	strategies := map[string]string{}
	keys := map[string]string{}

	for annKey, annVal := range annotations {
		if path, ok := strings.CutPrefix(annKey, MergeStrategyPrefix); ok {
			if !validMergePath(path) {
				continue
			}
			strategies[path] = strings.TrimSpace(annVal)
			continue
		}
		if path, ok := strings.CutPrefix(annKey, MergeKeyPrefix); ok {
			if !validMergePath(path) {
				continue
			}
			key := strings.TrimSpace(annVal)
			if key == "" || !validMergePath(key) {
				continue
			}
			keys[path] = key
		}
	}

	for path, val := range parseStrategyOverrides(mergeStrategies) {
		val = strings.TrimSpace(val)
		if val != StrategyAppend && val != StrategyMerge {
			continue
		}
		strategies[path] = val
	}
	for path, val := range parseStrategyOverrides(mergeKeys) {
		val = strings.TrimSpace(val)
		if val == "" || !validMergePath(val) {
			continue
		}
		keys[path] = val
	}

	for path, strat := range strategies {
		switch strat {
		case StrategyAppend:
		case StrategyMerge:
			if keys[path] == "" {
				strategies[path] = StrategyAppend
			}
		default:
			delete(strategies, path)
		}
	}
	for path := range keys {
		if strategies[path] != StrategyMerge {
			delete(keys, path)
		}
	}
	return strategies, keys
}

// StrategiesForChartTree collects actionable strategies for a chart and its
// subcharts. Paths are prefixed with each subchart's scope so they can be
// applied to a root values document. A parent's strategy is not copied onto
// its subcharts. CLI overrides win over each chart's annotations for the same
// chart-relative path.
func StrategiesForChartTree(chrt chart.Charter, mergeStrategies, mergeKeys []string) (map[string]string, map[string]string, error) {
	strategies := map[string]string{}
	keys := map[string]string{}
	if err := collectChartStrategies(chrt, "", mergeStrategies, mergeKeys, strategies, keys); err != nil {
		return nil, nil, err
	}
	return strategies, keys, nil
}

func collectChartStrategies(chrt chart.Charter, prefix string, mergeStrategies, mergeKeys []string, strategies, keys map[string]string) error {
	if chrt == nil {
		return nil
	}
	ac, err := chart.NewAccessor(chrt)
	if err != nil {
		return err
	}
	relStrategies, relKeys := ExtractMergeStrategies(ac.Annotations(), mergeStrategies, mergeKeys)
	for path, strat := range relStrategies {
		full := joinStrategyPath(prefix, path)
		strategies[full] = strat
		if key, ok := relKeys[path]; ok {
			keys[full] = key
		}
	}
	for _, dep := range ac.Dependencies() {
		if dep == nil {
			continue
		}
		depAcc, err := chart.NewAccessor(dep)
		if err != nil {
			return err
		}
		if err := collectChartStrategies(dep, joinStrategyPath(prefix, depAcc.Name()), mergeStrategies, mergeKeys, strategies, keys); err != nil {
			return err
		}
	}
	return nil
}

// CoalesceTablesWithStrategies merges src into dst the way CoalesceTables does,
// after applying array strategies. dst is authoritative. Append places src
// elements before dst elements (old before new when dst is the new values).
func CoalesceTablesWithStrategies(dst, src map[string]any, strategies, keys map[string]string) map[string]any {
	if dst != nil && src != nil && len(strategies) > 0 {
		applyStrategies(logPrintf, dst, src, strategies, keys, false)
	}
	return CoalesceTables(dst, src)
}

// ValidateMergeStrategyAnnotations reports Chart.yaml merge-strategy problems.
// checkPaths validates strategy paths against chart default values.
func ValidateMergeStrategyAnnotations(annotations map[string]string, values map[string]any, checkPaths bool) []error {
	if len(annotations) == 0 {
		return nil
	}

	strategies := map[string]string{}
	keyPaths := map[string]string{}
	for annKey, annVal := range annotations {
		if path, ok := strings.CutPrefix(annKey, MergeStrategyPrefix); ok {
			strategies[path] = strings.TrimSpace(annVal)
			continue
		}
		if path, ok := strings.CutPrefix(annKey, MergeKeyPrefix); ok {
			keyPaths[path] = strings.TrimSpace(annVal)
		}
	}

	var errs []error
	paths := mapKeys(strategies)
	for _, path := range paths {
		strat := strategies[path]
		switch strat {
		case StrategyAppend, StrategyMerge:
			if strat == StrategyMerge {
				if strings.TrimSpace(keyPaths[path]) == "" {
					errs = append(errs, fmt.Errorf("merge strategy on path %q requires a merge key", path))
				}
			}
		default:
			errs = append(errs, fmt.Errorf("unsupported merge strategy %q on path %q", strat, path))
		}
		if !checkPaths {
			continue
		}
		if !validMergePath(path) {
			errs = append(errs, fmt.Errorf("merge strategy path %q was not found in chart values", path))
			continue
		}
		val, found := getAtPath(values, path)
		if !found {
			errs = append(errs, fmt.Errorf("merge strategy path %q was not found in chart values", path))
			continue
		}
		if !isSlice(val) {
			errs = append(errs, fmt.Errorf("merge strategy path %q resolves to a non-array value", path))
		}
	}

	var orphans []string
	for path := range keyPaths {
		if _, ok := strategies[path]; !ok {
			orphans = append(orphans, path)
		}
	}
	sort.Strings(orphans)
	for _, path := range orphans {
		errs = append(errs, fmt.Errorf("merge key on path %q has no merge strategy", path))
	}
	return errs
}

// ValuesForMergeStrategyLint loads values.yaml for merge-strategy path checks.
// The boolean is false when the file exists but cannot be read; callers should
// skip path checks in that case. A missing file is treated as empty values.
func ValuesForMergeStrategyLint(chartDir string) (map[string]any, bool) {
	path := filepath.Join(chartDir, "values.yaml")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, true
		}
		return nil, false
	}
	if info.IsDir() {
		return nil, false
	}
	vals, err := common.ReadValuesFile(path)
	if err != nil {
		return nil, false
	}
	if vals == nil {
		vals = map[string]any{}
	}
	return vals, true
}

func applyStrategies(printf printFn, user, base map[string]any, strategies, keys map[string]string, preserveNil bool) {
	if len(strategies) == 0 || user == nil || base == nil {
		return
	}
	for _, path := range mapKeys(strategies) {
		userVal, ok := getAtPath(user, path)
		if !ok || userVal == nil {
			continue
		}
		baseVal, ok := getAtPath(base, path)
		if !ok || baseVal == nil {
			continue
		}
		if !isSlice(userVal) || !isSlice(baseVal) {
			continue
		}

		var (
			merged  any
			applied bool
		)
		switch strategies[path] {
		case StrategyAppend:
			merged, applied = appendArrays(baseVal, userVal)
		case StrategyMerge:
			merged, applied = mergeArraysByKey(printf, baseVal, userVal, keys[path], path, preserveNil)
		default:
			continue
		}
		if !applied {
			continue
		}
		setAtPath(user, path, merged)
	}
}

func appendArrays(base, user any) (any, bool) {
	baseCopy, err := copystructure.Copy(base)
	if err != nil {
		return nil, false
	}
	baseVal := reflect.ValueOf(baseCopy)
	userVal := reflect.ValueOf(user)
	if !baseVal.IsValid() || !userVal.IsValid() || baseVal.Kind() != reflect.Slice || userVal.Kind() != reflect.Slice {
		return nil, false
	}
	// Keep the chart slice type when the user slice matches, so []string stays
	// []string. Mixed slice types are normalized to []any.
	if baseVal.Type() == userVal.Type() {
		out := reflect.MakeSlice(baseVal.Type(), 0, baseVal.Len()+userVal.Len())
		out = reflect.AppendSlice(out, baseVal)
		out = reflect.AppendSlice(out, userVal)
		return out.Interface(), true
	}
	baseSlice, ok := toAnySlice(baseCopy)
	if !ok {
		return nil, false
	}
	userSlice, ok := toAnySlice(user)
	if !ok {
		return nil, false
	}
	return append(baseSlice, userSlice...), true
}

func mergeArraysByKey(printf printFn, base, user any, keyPath, prefix string, preserveNil bool) (any, bool) {
	if strings.TrimSpace(keyPath) == "" {
		return appendArrays(base, user)
	}
	baseCopy, err := copystructure.Copy(base)
	if err != nil {
		printf("warning: unable to copy chart array for merge strategy %s: %s", prefix, err)
		return nil, false
	}
	result, ok := toAnySlice(baseCopy)
	if !ok {
		return nil, false
	}
	userSlice, ok := toAnySlice(user)
	if !ok {
		return nil, false
	}

	for _, elem := range userSlice {
		elemMap, isMap := asMap(elem)
		if !isMap {
			result = append(result, elem)
			continue
		}
		keyVal, hasKey := lookupMergeKey(elemMap, keyPath)
		if !hasKey {
			result = append(result, copyValueOrOriginal(elem))
			continue
		}
		idx := findMergeIndex(result, keyPath, keyVal)
		if idx < 0 {
			result = append(result, copyValueOrOriginal(elem))
			continue
		}
		baseMap, ok := asMap(result[idx])
		if !ok {
			result = append(result, copyValueOrOriginal(elem))
			continue
		}
		result[idx] = mergeObjects(printf, elemMap, baseMap, prefix, preserveNil)
	}
	return result, true
}

func mergeObjects(printf printFn, user, base map[string]any, prefix string, preserveNil bool) map[string]any {
	userCopy, userErr := copystructure.Copy(user)
	baseCopy, baseErr := copystructure.Copy(base)
	userMap, userOK := asMap(userCopy)
	baseMap, baseOK := asMap(baseCopy)
	if userErr != nil || baseErr != nil || !userOK || !baseOK {
		printf("warning: unable to copy values while merging %s", prefix)
		userMap, userOK = asMap(user)
		baseMap, baseOK = asMap(base)
		if !userOK || !baseOK {
			if userOK {
				return userMap
			}
			return map[string]any{}
		}
	}
	return coalesceTablesFullKey(printf, userMap, baseMap, prefix, preserveNil)
}

func findMergeIndex(items []any, keyPath string, keyVal any) int {
	for i, item := range items {
		itemMap, ok := asMap(item)
		if !ok {
			continue
		}
		existing, ok := lookupMergeKey(itemMap, keyPath)
		if !ok {
			continue
		}
		if reflect.DeepEqual(existing, keyVal) {
			return i
		}
	}
	return -1
}

func lookupMergeKey(m map[string]any, keyPath string) (any, bool) {
	if !validMergePath(keyPath) {
		return nil, false
	}
	cur := any(m)
	parts := strings.Split(keyPath, ".")
	for i, part := range parts {
		curMap, ok := asMap(cur)
		if !ok {
			return nil, false
		}
		val, exists := curMap[part]
		if !exists || val == nil {
			return nil, false
		}
		if i == len(parts)-1 {
			return val, true
		}
		cur = val
	}
	return nil, false
}

func globalScopeStrategies(strategies, keys map[string]string) (map[string]string, map[string]string) {
	prefix := common.GlobalKey + "."
	outStrategies := map[string]string{}
	outKeys := map[string]string{}
	for path, strat := range strategies {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok || !validMergePath(rest) {
			continue
		}
		outStrategies[rest] = strat
		if key, ok := keys[path]; ok {
			outKeys[rest] = key
		}
	}
	return outStrategies, outKeys
}

func parseStrategyOverrides(entries []string) map[string]string {
	out := map[string]string{}
	for _, entry := range entries {
		path, val, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		path = strings.TrimSpace(path)
		if !validMergePath(path) {
			continue
		}
		out[path] = val
	}
	return out
}

func validMergePath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	for _, seg := range strings.Split(path, ".") {
		if strings.TrimSpace(seg) == "" {
			return false
		}
	}
	return true
}

func getAtPath(root map[string]any, path string) (any, bool) {
	if root == nil || !validMergePath(path) {
		return nil, false
	}
	cur := any(root)
	for _, part := range strings.Split(path, ".") {
		curMap, ok := asMap(cur)
		if !ok {
			return nil, false
		}
		val, exists := curMap[part]
		if !exists {
			return nil, false
		}
		cur = val
	}
	return cur, true
}

func setAtPath(root map[string]any, path string, val any) bool {
	if root == nil || !validMergePath(path) {
		return false
	}
	parts := strings.Split(path, ".")
	cur := any(root)
	for _, part := range parts[:len(parts)-1] {
		curMap, ok := asMap(cur)
		if !ok {
			return false
		}
		next, exists := curMap[part]
		if !exists {
			return false
		}
		cur = next
	}
	curMap, ok := asMap(cur)
	if !ok {
		return false
	}
	curMap[parts[len(parts)-1]] = val
	return true
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

func isSlice(v any) bool {
	if v == nil {
		return false
	}
	kind := reflect.ValueOf(v).Kind()
	return kind == reflect.Slice || kind == reflect.Array
}

func toAnySlice(v any) ([]any, bool) {
	if v == nil {
		return []any{}, true
	}
	rv := reflect.ValueOf(v)
	kind := rv.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return nil, false
	}
	if kind == reflect.Slice && rv.IsNil() {
		return []any{}, true
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		elem := rv.Index(i)
		if !elem.IsValid() || (elem.Kind() == reflect.Interface || elem.Kind() == reflect.Pointer) && elem.IsNil() {
			out[i] = nil
			continue
		}
		out[i] = elem.Interface()
	}
	return out, true
}

func copyValueOrOriginal(v any) any {
	copied, err := copystructure.Copy(v)
	if err != nil || copied == nil {
		return v
	}
	return copied
}

func joinStrategyPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	if path == "" {
		return prefix
	}
	return prefix + "." + path
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func logPrintf(format string, v ...any) {
	// CoalesceTables logs through the standard logger. Strategy merges invoked
	// from that path use the same destination.
	log.Printf(format, v...)
}
