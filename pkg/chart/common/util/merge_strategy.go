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
	"maps"
	"reflect"
	"sort"
	"strings"

	v3chart "helm.sh/helm/v4/internal/chart/v3"
	"helm.sh/helm/v4/internal/copystructure"
	chart "helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
	v2chart "helm.sh/helm/v4/pkg/chart/v2"
)

const (
	// MergeStrategyPrefix is the Chart.yaml annotation prefix for array merge strategies.
	// The remainder of the key is a dot-separated values path.
	MergeStrategyPrefix = "helm.sh/merge-strategy/"
	// MergeKeyPrefix is the Chart.yaml annotation prefix for array merge keys.
	// The remainder of the key is a dot-separated values path.
	MergeKeyPrefix = "helm.sh/merge-key/"

	// StrategyAppend concatenates chart defaults before user elements.
	StrategyAppend = "append"
	// StrategyMerge key-merges array-of-object elements.
	StrategyMerge = "merge"

	globalStrategyPrefix = "global."
)

// ArrayStrategy is an actionable merge strategy for one values path.
type ArrayStrategy struct {
	// Path is the dot-separated values path of the array.
	Path string
	// Strategy is StrategyAppend or StrategyMerge.
	Strategy string
	// MergeKey is the field used to match objects when Strategy is StrategyMerge.
	MergeKey string
}

// ExtractMergeStrategies returns actionable strategies from chart annotations.
// A "merge" entry without a companion merge key is returned as "append".
// Annotations with an empty or invalid path are excluded, as are unsupported
// strategy values.
func ExtractMergeStrategies(annotations map[string]string) map[string]ArrayStrategy {
	if len(annotations) == 0 {
		return nil
	}
	keys := mergeKeyIndex(annotations)
	out := make(map[string]ArrayStrategy)
	for annKey, raw := range annotations {
		path, ok := annotationPath(annKey, MergeStrategyPrefix)
		if !ok || !validMergePath(path) {
			continue
		}
		strategy := strings.TrimSpace(raw)
		mergeKey := strings.TrimSpace(keys[path])
		switch strategy {
		case StrategyMerge:
			if mergeKey == "" || !validMergePath(mergeKey) {
				strategy = StrategyAppend
				mergeKey = ""
			}
		case StrategyAppend:
			mergeKey = ""
		default:
			continue
		}
		out[path] = ArrayStrategy{Path: path, Strategy: strategy, MergeKey: mergeKey}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// OverlayMergeAnnotations returns a copy of annotations with CLI overrides applied.
// strategies and keys are "path=value" entries. They take precedence over chart
// annotations for the same path. Empty or invalid entries are ignored.
func OverlayMergeAnnotations(annotations map[string]string, strategies, keys []string) map[string]string {
	out := make(map[string]string, len(annotations)+len(strategies)+len(keys))
	maps.Copy(out, annotations)
	for _, entry := range strategies {
		path, value, ok := splitPathValue(entry)
		if !ok || !validMergePath(path) {
			continue
		}
		out[MergeStrategyPrefix+path] = value
	}
	for _, entry := range keys {
		path, value, ok := splitPathValue(entry)
		if !ok || !validMergePath(path) {
			continue
		}
		out[MergeKeyPrefix+path] = value
	}
	return out
}

// ValidateMergeStrategyAnnotations checks merge-strategy annotations against
// chart default values. Returned errors are lint warnings.
func ValidateMergeStrategyAnnotations(annotations map[string]string, values map[string]any) []error {
	if len(annotations) == 0 {
		return nil
	}
	strategyPaths := map[string]string{}
	keyPaths := map[string]struct{}{}
	var paths []string

	for annKey, raw := range annotations {
		if path, ok := annotationPath(annKey, MergeStrategyPrefix); ok {
			strategyPaths[path] = strings.TrimSpace(raw)
			paths = append(paths, path)
			continue
		}
		if path, ok := annotationPath(annKey, MergeKeyPrefix); ok {
			keyPaths[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	seen := map[string]struct{}{}
	var errs []error
	for _, path := range paths {
		if _, dup := seen[path]; dup {
			continue
		}
		// A path can appear once as a strategy and once as a key; handle both
		// the first time we see either annotation for it.
		seen[path] = struct{}{}

		strategy, hasStrategy := strategyPaths[path]
		_, hasKey := keyPaths[path]
		if hasStrategy {
			switch strategy {
			case StrategyAppend, StrategyMerge:
			default:
				errs = append(errs, fmt.Errorf("unsupported merge strategy %q for path %q", strategy, path))
			}
			if strategy == StrategyMerge && !hasKey {
				errs = append(errs, fmt.Errorf("merge strategy for path %q requires helm.sh/merge-key/%s", path, path))
			}
			if !validMergePath(path) {
				errs = append(errs, fmt.Errorf("merge strategy path %q is invalid", path))
				continue
			}
			errs = append(errs, validateStrategyPath(path, values)...)
		}
		if hasKey && !hasStrategy {
			errs = append(errs, fmt.Errorf("merge key for path %q has no merge strategy", path))
		}
	}
	return errs
}

func validateStrategyPath(path string, values map[string]any) []error {
	val, found := lookupPath(values, path)
	if !found {
		return []error{fmt.Errorf("merge strategy path %q was not found in chart values", path)}
	}
	if _, ok := asSlice(val); !ok {
		return []error{fmt.Errorf("merge strategy path %q resolves to a non-array", path)}
	}
	return nil
}

// WithChartMergeOverrides applies CLI merge strategy overrides to the root
// chart's annotations. The returned function restores the previous annotations.
func WithChartMergeOverrides(ch chart.Charter, strategies, keys []string) func() {
	if len(strategies) == 0 && len(keys) == 0 {
		return func() {}
	}
	ref := annotationMapPtr(ch)
	if ref == nil {
		return func() {}
	}
	original := copyStringMap(*ref)
	overlaid := OverlayMergeAnnotations(original, strategies, keys)
	*ref = overlaid
	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		*ref = original
	}
}

// WithoutChartMergeStrategies removes merge-strategy annotations from a chart
// and its subcharts. The returned function restores them. Used when an upgrade
// sets ResetValues and strategies must be ignored.
func WithoutChartMergeStrategies(ch chart.Charter) func() {
	type saved struct {
		ref  *map[string]string
		orig map[string]string
	}
	var snapshots []saved
	walkCharts(ch, func(ref *map[string]string) {
		if ref == nil || len(*ref) == 0 {
			return
		}
		snapshots = append(snapshots, saved{ref: ref, orig: copyStringMap(*ref)})
		stripped := copyStringMap(*ref)
		for key := range stripped {
			if strings.HasPrefix(key, MergeStrategyPrefix) || strings.HasPrefix(key, MergeKeyPrefix) {
				delete(stripped, key)
			}
		}
		*ref = stripped
	})
	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		for _, snap := range snapshots {
			*snap.ref = snap.orig
		}
	}
}

// CoalesceTablesWithStrategies merges src into dst the way CoalesceTables does,
// after applying chart array strategies. dst is higher precedence. Append places
// src elements before dst elements. Strategies are scoped to each chart.
func CoalesceTablesWithStrategies(dst, src map[string]any, ch chart.Charter) map[string]any {
	if dst != nil && src != nil && ch != nil {
		applyScopedStrategies(log.Printf, dst, src, ch, false)
	}
	return CoalesceTables(dst, src)
}

// RestoreStrategyBasePaths puts chart-default arrays back onto strategy paths in
// coalesced values so a later coalesce does not append an already-merged array
// a second time. Paths missing from defaults are removed.
func RestoreStrategyBasePaths(coalesced, defaults map[string]any, ch chart.Charter) {
	if coalesced == nil || ch == nil {
		return
	}
	for path := range ExtractMergeStrategies(chartAnnotations(ch)) {
		if defaults == nil {
			deletePath(coalesced, path)
			continue
		}
		def, ok := lookupPath(defaults, path)
		if !ok {
			deletePath(coalesced, path)
			continue
		}
		if _, isArr := asSlice(def); !isArr {
			deletePath(coalesced, path)
			continue
		}
		setPath(coalesced, path, deepCopyAny(def))
	}
}

func applyScopedStrategies(printf printFn, dst, src map[string]any, ch chart.Charter, merge bool) {
	if dst == nil || src == nil || ch == nil {
		return
	}
	applyArrayStrategies(printf, dst, src, ExtractMergeStrategies(chartAnnotations(ch)), merge)
	for key, sub := range subchartValueKeys(ch) {
		dm, dok := asStringMap(dst[key])
		sm, sok := asStringMap(src[key])
		if dok && sok {
			applyScopedStrategies(printf, dm, sm, sub, merge)
		}
	}
}

func applyArrayStrategies(printf printFn, overlay, base map[string]any, strategies map[string]ArrayStrategy, merge bool) {
	if overlay == nil || len(strategies) == 0 {
		return
	}
	// Stable order keeps nested paths deterministic.
	paths := make([]string, 0, len(strategies))
	for path := range strategies {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		overVal, overOK := lookupPath(overlay, path)
		if !overOK || overVal == nil {
			continue
		}
		overSlice, overIs := asSlice(overVal)
		if !overIs {
			continue
		}
		if base == nil {
			continue
		}
		baseVal, baseOK := lookupPath(base, path)
		if !baseOK || baseVal == nil {
			continue
		}
		baseSlice, baseIs := asSlice(baseVal)
		if !baseIs {
			continue
		}
		// Deep-copy chart arrays before merging so chart defaults are not mutated.
		copiedBase := deepCopySlice(baseSlice)
		merged := combineArrays(printf, copiedBase, overSlice, strategies[path], merge, path)
		setPath(overlay, path, merged)
	}
}

func combineArrays(printf printFn, base, user []any, st ArrayStrategy, merge bool, path string) []any {
	if st.Strategy == StrategyMerge && st.MergeKey != "" {
		return mergeArraysByKey(printf, base, user, st.MergeKey, merge, path)
	}
	return appendArrays(base, user)
}

func appendArrays(base, user []any) []any {
	out := make([]any, 0, len(base)+len(user))
	out = append(out, base...)
	for _, el := range user {
		out = append(out, deepCopyAny(el))
	}
	return out
}

func mergeArraysByKey(printf printFn, base, user []any, keyPath string, merge bool, path string) []any {
	result := append([]any{}, base...)
	type indexed struct {
		key any
		idx int
	}
	var index []indexed
	for i, el := range result {
		m, ok := asStringMap(el)
		if !ok {
			continue
		}
		k, ok := mapValueAt(m, keyPath)
		if !ok {
			continue
		}
		index = append(index, indexed{key: k, idx: i})
	}

	for _, el := range user {
		m, isMap := asStringMap(el)
		if !isMap {
			result = append(result, deepCopyAny(el))
			continue
		}
		k, hasKey := mapValueAt(m, keyPath)
		if !hasKey {
			result = append(result, deepCopyAny(el))
			continue
		}
		match := -1
		matchPos := -1
		for i, ix := range index {
			if keysEqual(ix.key, k) {
				match = ix.idx
				matchPos = i
				break
			}
		}
		if match == -1 {
			copied := deepCopyAny(el)
			result = append(result, copied)
			if cm, ok := asStringMap(copied); ok {
				if nk, ok := mapValueAt(cm, keyPath); ok {
					index = append(index, indexed{key: nk, idx: len(result) - 1})
				}
			}
			continue
		}
		dest, ok := asStringMap(result[match])
		if !ok {
			result = append(result, deepCopyAny(el))
			continue
		}
		userCopy, ok := asStringMap(deepCopyAny(m))
		if !ok {
			result = append(result, deepCopyAny(el))
			continue
		}
		// userCopy is authoritative. Missing keys are filled from the default
		// element. Nulls delete keys when coalescing and are preserved when merging.
		coalesceTablesFullKey(printf, userCopy, dest, path, merge)
		result[match] = userCopy
		if nk, ok := mapValueAt(userCopy, keyPath); ok && matchPos >= 0 {
			index[matchPos].key = nk
		}
	}
	return result
}

func globalScopedStrategies(in map[string]ArrayStrategy) map[string]ArrayStrategy {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ArrayStrategy)
	for path, st := range in {
		if !strings.HasPrefix(path, globalStrategyPrefix) {
			continue
		}
		stripped := strings.TrimPrefix(path, globalStrategyPrefix)
		if !validMergePath(stripped) {
			continue
		}
		st.Path = stripped
		out[stripped] = st
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func chartAnnotations(ch chart.Charter) map[string]string {
	if ch == nil {
		return nil
	}
	acc, err := chart.NewAccessor(ch)
	if err != nil {
		return nil
	}
	return acc.Annotations()
}

func annotationMapPtr(ch chart.Charter) *map[string]string {
	switch c := ch.(type) {
	case *v2chart.Chart:
		if c == nil || c.Metadata == nil {
			return nil
		}
		return &c.Metadata.Annotations
	case *v3chart.Chart:
		if c == nil || c.Metadata == nil {
			return nil
		}
		return &c.Metadata.Annotations
	default:
		return nil
	}
}

func walkCharts(ch chart.Charter, fn func(*map[string]string)) {
	if ch == nil {
		return
	}
	if ref := annotationMapPtr(ch); ref != nil {
		fn(ref)
	}
	acc, err := chart.NewAccessor(ch)
	if err != nil {
		return
	}
	for _, dep := range acc.Dependencies() {
		walkCharts(dep, fn)
	}
}

func subchartValueKeys(ch chart.Charter) map[string]chart.Charter {
	acc, err := chart.NewAccessor(ch)
	if err != nil {
		return nil
	}
	byName := map[string]chart.Charter{}
	for _, dep := range acc.Dependencies() {
		dacc, derr := chart.NewAccessor(dep)
		if derr != nil || dacc.Name() == "" {
			continue
		}
		byName[dacc.Name()] = dep
	}
	keys := make(map[string]chart.Charter, len(byName))
	for name, sub := range byName {
		keys[name] = sub
	}
	for _, md := range acc.MetaDependencies() {
		name, alias := dependencyNameAlias(md)
		sub := byName[name]
		if sub == nil || alias == "" {
			continue
		}
		keys[alias] = sub
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}

func dependencyNameAlias(dep chart.Dependency) (name, alias string) {
	rv := reflect.ValueOf(dep)
	if !rv.IsValid() {
		return "", ""
	}
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", ""
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return "", ""
	}
	if f := rv.FieldByName("Name"); f.IsValid() && f.Kind() == reflect.String {
		name = f.String()
	}
	if f := rv.FieldByName("Alias"); f.IsValid() && f.Kind() == reflect.String {
		alias = f.String()
	}
	return name, alias
}

func mergeKeyIndex(annotations map[string]string) map[string]string {
	out := make(map[string]string)
	for key, value := range annotations {
		path, ok := annotationPath(key, MergeKeyPrefix)
		if !ok || !validMergePath(path) {
			continue
		}
		out[path] = value
	}
	return out
}

func annotationPath(key, prefix string) (string, bool) {
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	return key[len(prefix):], true
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

func splitPathValue(entry string) (path, value string, ok bool) {
	entry = strings.TrimSpace(entry)
	i := strings.IndexByte(entry, '=')
	if i <= 0 || i >= len(entry)-1 {
		return "", "", false
	}
	path = strings.TrimSpace(entry[:i])
	value = strings.TrimSpace(entry[i+1:])
	if path == "" || value == "" {
		return "", "", false
	}
	return path, value, true
}

func lookupPath(root map[string]any, path string) (any, bool) {
	if root == nil || !validMergePath(path) {
		return nil, false
	}
	var cur any = root
	for _, seg := range strings.Split(path, ".") {
		m, ok := asStringMap(cur)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(root map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	cur := root
	for _, seg := range parts[:len(parts)-1] {
		m, ok := asStringMap(cur[seg])
		if !ok {
			return
		}
		cur = m
	}
	cur[parts[len(parts)-1]] = val
}

func deletePath(root map[string]any, path string) {
	if root == nil || !validMergePath(path) {
		return
	}
	parts := strings.Split(path, ".")
	cur := root
	for _, seg := range parts[:len(parts)-1] {
		m, ok := asStringMap(cur[seg])
		if !ok {
			return
		}
		cur = m
	}
	delete(cur, parts[len(parts)-1])
}

func mapValueAt(m map[string]any, path string) (any, bool) {
	if m == nil || !validMergePath(path) {
		return nil, false
	}
	var cur any = m
	parts := strings.Split(path, ".")
	for i, seg := range parts {
		mm, ok := asStringMap(cur)
		if !ok {
			return nil, false
		}
		next, ok := mm[seg]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return next, true
		}
		cur = next
	}
	return nil, false
}

func asStringMap(v any) (map[string]any, bool) {
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
	if rv.Kind() != reflect.Slice {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i)
		if !item.CanInterface() {
			return nil, false
		}
		out[i] = item.Interface()
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

func deepCopySlice(in []any) []any {
	if in == nil {
		return nil
	}
	copied, ok := asSlice(deepCopyAny(in))
	if !ok {
		out := make([]any, len(in))
		copy(out, in)
		return out
	}
	return copied
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func keysEqual(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	af, aok := numericFloat(a)
	bf, bok := numericFloat(b)
	return aok && bok && af == bf
}

func numericFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
