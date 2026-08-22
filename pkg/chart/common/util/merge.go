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
	"reflect"
	"sort"
	"strings"

	"helm.sh/helm/v4/internal/copystructure"
	"helm.sh/helm/v4/pkg/chart"
)

const (
	// MergeStrategyAnnotationPrefix is the Chart.yaml annotation prefix for
	// configuring how a value array is merged.
	MergeStrategyAnnotationPrefix = "helm.sh/merge-strategy/"
	// MergeKeyAnnotationPrefix is the Chart.yaml annotation prefix for
	// configuring the key used to match objects in a value array.
	MergeKeyAnnotationPrefix = "helm.sh/merge-key/"

	MergeStrategyAppend = "append"
	MergeStrategyMerge  = "merge"
)

// MergeStrategyOptions contains command-line merge strategy overrides.
//
// The entries in MergeStrategies and MergeKeys use path=value syntax. Paths
// are relative to the root chart values unless they name a subchart.
type MergeStrategyOptions struct {
	MergeStrategies []string
	MergeKeys       []string
	Ignore          bool
}

type mergeRule struct {
	strategy string
	key      string
}

// ExtractMergeStrategies returns actionable merge strategies from Chart.yaml
// annotations. A merge strategy without a merge key is an append strategy.
func ExtractMergeStrategies(annotations map[string]string) map[string]string {
	rules := annotationMergeRules(annotations)
	strategies := make(map[string]string, len(rules))
	for path, rule := range rules {
		strategies[path] = rule.strategy
	}
	return strategies
}

// ExtractMergeKeys returns merge keys for actionable merge strategies from
// Chart.yaml annotations.
func ExtractMergeKeys(annotations map[string]string) map[string]string {
	rules := annotationMergeRules(annotations)
	keys := make(map[string]string)
	for path, rule := range rules {
		if rule.strategy == MergeStrategyMerge && rule.key != "" {
			keys[path] = rule.key
		}
	}
	return keys
}

func annotationMergeRules(annotations map[string]string) map[string]mergeRule {
	strategies := make(map[string]string)
	keys := make(map[string]string)
	for name, value := range annotations {
		if strings.HasPrefix(name, MergeStrategyAnnotationPrefix) {
			path := strings.TrimPrefix(name, MergeStrategyAnnotationPrefix)
			if validMergePath(path) {
				strategies[path] = strings.TrimSpace(value)
			}
		} else if strings.HasPrefix(name, MergeKeyAnnotationPrefix) {
			path := strings.TrimPrefix(name, MergeKeyAnnotationPrefix)
			if validMergePath(path) && validMergePath(value) {
				keys[path] = value
			}
		}
	}

	rules := make(map[string]mergeRule, len(strategies))
	for path, strategy := range strategies {
		switch strategy {
		case MergeStrategyAppend:
			rules[path] = mergeRule{strategy: strategy}
		case MergeStrategyMerge:
			if key, ok := keys[path]; ok {
				rules[path] = mergeRule{strategy: strategy, key: key}
			} else {
				rules[path] = mergeRule{strategy: MergeStrategyAppend}
			}
		}
	}
	return rules
}

func validMergePath(path string) bool {
	if path == "" {
		return false
	}
	for part := range strings.SplitSeq(path, ".") {
		if part == "" || strings.TrimSpace(part) != part {
			return false
		}
	}
	return true
}

func parseMergeOverrides(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		path, value, ok := strings.Cut(entry, "=")
		if ok && validMergePath(path) {
			values[path] = strings.TrimSpace(value)
		}
	}
	return values
}

func mergeRulesForChart(accessor chart.Accessor, valuePrefix string, options MergeStrategyOptions) map[string]mergeRule {
	if options.Ignore {
		return nil
	}

	annotations := accessor.Annotations()
	rules := annotationMergeRules(annotations)
	annotationStrategies := make(map[string]string)
	annotationKeys := make(map[string]string)
	for name, value := range annotations {
		if strings.HasPrefix(name, MergeStrategyAnnotationPrefix) {
			path := strings.TrimPrefix(name, MergeStrategyAnnotationPrefix)
			if validMergePath(path) {
				strategy := strings.TrimSpace(value)
				if strategy == MergeStrategyAppend || strategy == MergeStrategyMerge {
					annotationStrategies[path] = strategy
				}
			}
		} else if strings.HasPrefix(name, MergeKeyAnnotationPrefix) {
			path := strings.TrimPrefix(name, MergeKeyAnnotationPrefix)
			if validMergePath(path) && validMergePath(value) {
				annotationKeys[path] = value
			}
		}
	}
	strategyOverrides := parseMergeOverrides(options.MergeStrategies)
	keyOverrides := parseMergeOverrides(options.MergeKeys)
	for path, strategy := range strategyOverrides {
		if localPath, ok := localMergePath(valuePrefix, path); ok &&
			(strategy == MergeStrategyAppend || strategy == MergeStrategyMerge) {
			annotationStrategies[localPath] = strategy
		}
	}
	for path, key := range keyOverrides {
		if localPath, ok := localMergePath(valuePrefix, path); ok && validMergePath(key) {
			annotationKeys[localPath] = key
		}
	}

	result := make(map[string]mergeRule, len(rules)+len(annotationStrategies))
	for path, strategy := range annotationStrategies {
		rule := mergeRule{strategy: strategy, key: annotationKeys[path]}
		if annotationStrategy, ok := annotations[MergeStrategyAnnotationPrefix+path]; ok &&
			strings.TrimSpace(annotationStrategy) == MergeStrategyMerge &&
			rule.key == "" {
			rule.strategy = MergeStrategyAppend
		}
		if rule.strategy == MergeStrategyMerge && rule.key == "" {
			rule.strategy = MergeStrategyAppend
		}
		result[path] = rule
	}
	for path, rule := range rules {
		if _, exists := result[path]; !exists {
			result[path] = rule
		}
	}

	// A command-line strategy can add a strategy to a path not annotated by
	// the chart. A merge strategy without a key has append semantics.
	for path, strategy := range strategyOverrides {
		localPath, ok := localMergePath(valuePrefix, path)
		if !ok || (strategy != MergeStrategyAppend && strategy != MergeStrategyMerge) {
			continue
		}
		rule := result[localPath]
		rule.strategy = strategy
		if rule.strategy == MergeStrategyMerge && rule.key == "" {
			rule.strategy = MergeStrategyAppend
		}
		result[localPath] = rule
	}
	for path, key := range keyOverrides {
		localPath, ok := localMergePath(valuePrefix, path)
		if !ok || !validMergePath(key) {
			continue
		}
		if rule, exists := result[localPath]; exists {
			rule.key = key
			if rawStrategy, ok := annotationStrategies[localPath]; ok && rawStrategy == MergeStrategyMerge {
				rule.strategy = MergeStrategyMerge
			}
			cliPath := localPath
			if valuePrefix != "" {
				cliPath = valuePrefix + "." + localPath
			}
			if cliStrategy, ok := strategyOverrides[cliPath]; ok && cliStrategy == MergeStrategyMerge {
				rule.strategy = MergeStrategyMerge
			}
			result[localPath] = rule
		}
	}
	return result
}

func localMergePath(valuePrefix, path string) (string, bool) {
	if valuePrefix == "" {
		return path, true
	}
	prefix := valuePrefix + "."
	if strings.HasPrefix(path, prefix) {
		return strings.TrimPrefix(path, prefix), true
	}
	return "", false
}

func collectMergeRules(chrt chart.Charter, valuePrefix string, options MergeStrategyOptions, result map[string]mergeRule) error {
	accessor, err := chart.NewAccessor(chrt)
	if err != nil {
		return err
	}
	for path, rule := range mergeRulesForChart(accessor, valuePrefix, options) {
		fullPath := path
		if valuePrefix != "" {
			fullPath = valuePrefix + "." + path
		}
		result[fullPath] = rule
	}
	for _, dependency := range accessor.Dependencies() {
		dependencyAccessor, err := chart.NewAccessor(dependency)
		if err != nil {
			return err
		}
		dependencyPrefix := dependencyAccessor.Name()
		if valuePrefix != "" {
			dependencyPrefix = valuePrefix + "." + dependencyPrefix
		}
		if err := collectMergeRules(dependency, dependencyPrefix, options, result); err != nil {
			return err
		}
	}
	return nil
}

func mergeRulesForValues(chrt chart.Charter, options MergeStrategyOptions) map[string]mergeRule {
	rules := make(map[string]mergeRule)
	if err := collectMergeRules(chrt, "", options, rules); err != nil {
		return nil
	}
	return rules
}

func mergeRulesForLocalChart(accessor chart.Accessor, valuePrefix string, options MergeStrategyOptions) map[string]mergeRule {
	return mergeRulesForChart(accessor, valuePrefix, options)
}

func chartValuePrefix(prefix, chartName string) string {
	if prefix == "" {
		return ""
	}
	parts := strings.Split(prefix, ".")
	if len(parts) == 1 {
		return chartName
	}
	return strings.Join(append(parts[1:], chartName), ".")
}

// CoalesceTablesWithMergeStrategies merges a source map into a destination map
// using the strategies declared by the chart and any command-line overrides.
func CoalesceTablesWithMergeStrategies(chrt chart.Charter, dst, src map[string]any, options MergeStrategyOptions) map[string]any {
	rules := mergeRulesForValues(chrt, options)
	source, ok := copyMergeValue(src).(map[string]any)
	if !ok {
		source = src
	}
	return coalesceTablesFullKeyWithRules(log.Printf, dst, source, "", false, rules)
}

// MergeTablesWithMergeStrategies is the nil-preserving variant of
// CoalesceTablesWithMergeStrategies.
func MergeTablesWithMergeStrategies(chrt chart.Charter, dst, src map[string]any, options MergeStrategyOptions) map[string]any {
	rules := mergeRulesForValues(chrt, options)
	source, ok := copyMergeValue(src).(map[string]any)
	if !ok {
		source = src
	}
	return coalesceTablesFullKeyWithRules(log.Printf, dst, source, "", true, rules)
}

func lookupMergePath(values map[string]any, path string) (any, bool) {
	var current any = values
	for part := range strings.SplitSeq(path, ".") {
		table, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = table[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func setMergePath(values map[string]any, path string, value any) bool {
	parts := strings.Split(path, ".")
	current := values
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
	return true
}

func copyMergeValue(value any) any {
	if value == nil {
		return nil
	}
	copied, err := copystructure.Copy(value)
	if err != nil {
		return value
	}
	return copied
}

func anySlice(value any) ([]any, bool) {
	if value == nil {
		return nil, false
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Array && reflected.Kind() != reflect.Slice {
		return nil, false
	}
	result := make([]any, reflected.Len())
	for i := range result {
		result[i] = reflected.Index(i).Interface()
	}
	return result, true
}

func mergeableArrays(defaults, user any) bool {
	_, defaultsOK := anySlice(defaults)
	_, userOK := anySlice(user)
	return defaultsOK && userOK
}

func mergeArrayValues(defaults, user any, rule mergeRule, path string, nestedRules map[string]mergeRule) (any, bool) {
	defaultElements, defaultsOK := anySlice(defaults)
	userElements, userOK := anySlice(user)
	if !defaultsOK || !userOK {
		return user, false
	}

	if rule.strategy == MergeStrategyAppend {
		result := make([]any, 0, len(defaultElements)+len(userElements))
		for _, element := range defaultElements {
			result = append(result, copyMergeValue(element))
		}
		for _, element := range userElements {
			result = append(result, copyMergeValue(element))
		}
		return result, true
	}

	result := make([]any, len(defaultElements))
	for i, element := range defaultElements {
		result[i] = copyMergeValue(element)
	}
	for _, userElement := range userElements {
		userMap, userIsMap := userElement.(map[string]any)
		matched := false
		if userIsMap {
			userKey, userHasKey := lookupMergePath(userMap, rule.key)
			if userHasKey {
				for i, defaultElement := range result {
					defaultMap, defaultIsMap := defaultElement.(map[string]any)
					if !defaultIsMap {
						continue
					}
					defaultKey, defaultHasKey := lookupMergePath(defaultMap, rule.key)
					if defaultHasKey && reflect.DeepEqual(defaultKey, userKey) {
						result[i] = mergeMapValues(defaultMap, userMap, path, nestedRules)
						matched = true
						break
					}
				}
			}
		}
		if !matched {
			result = append(result, copyMergeValue(userElement))
		}
	}
	return result, true
}

func mergeMapValues(defaults, user map[string]any, path string, rules map[string]mergeRule) map[string]any {
	result := make(map[string]any, len(defaults)+len(user))
	for key, value := range defaults {
		result[key] = copyMergeValue(value)
	}
	for key, userValue := range user {
		fullPath := key
		if path != "" {
			fullPath = path + "." + key
		}
		if defaultValue, ok := defaults[key]; ok {
			if defaultMap, ok := defaultValue.(map[string]any); ok {
				if userMap, ok := userValue.(map[string]any); ok {
					result[key] = mergeMapValues(defaultMap, userMap, fullPath, rules)
					continue
				}
			}
			if rule, ok := rules[fullPath]; ok {
				if merged, ok := mergeArrayValues(defaultValue, userValue, rule, fullPath, rules); ok {
					result[key] = merged
					continue
				}
			}
		}
		result[key] = copyMergeValue(userValue)
	}
	return result
}

func applyMergeStrategies(defaults, user map[string]any, rules map[string]mergeRule) {
	paths := make([]string, 0, len(rules))
	for path := range rules {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		left, right := strings.Count(paths[i], "."), strings.Count(paths[j], ".")
		if left == right {
			return paths[i] < paths[j]
		}
		return left < right
	})
	for _, path := range paths {
		defaultValue, defaultsOK := lookupMergePath(defaults, path)
		userValue, userOK := lookupMergePath(user, path)
		if !defaultsOK || !userOK || userValue == nil {
			continue
		}
		if rule := rules[path]; rule.strategy == MergeStrategyAppend || rule.strategy == MergeStrategyMerge {
			if merged, ok := mergeArrayValues(defaultValue, userValue, rule, path, rules); ok {
				setMergePath(user, path, merged)
			}
		}
	}
}

// ValidateMergeStrategies validates merge annotations against chart defaults.
// It returns all warnings as one joined error so that the caller can emit them
// through its existing Chart.yaml lint rule.
func ValidateMergeStrategies(annotations map[string]string, values map[string]any) error {
	var warnings []error
	strategyPaths := make([]string, 0)
	keyPaths := make([]string, 0)
	for annotation := range annotations {
		switch {
		case strings.HasPrefix(annotation, MergeStrategyAnnotationPrefix):
			path := strings.TrimPrefix(annotation, MergeStrategyAnnotationPrefix)
			if validMergePath(path) {
				strategyPaths = append(strategyPaths, path)
			}
		case strings.HasPrefix(annotation, MergeKeyAnnotationPrefix):
			path := strings.TrimPrefix(annotation, MergeKeyAnnotationPrefix)
			if validMergePath(path) {
				keyPaths = append(keyPaths, path)
			}
		}
	}
	sort.Strings(strategyPaths)
	sort.Strings(keyPaths)

	for _, path := range strategyPaths {
		strategy := strings.TrimSpace(annotations[MergeStrategyAnnotationPrefix+path])
		switch strategy {
		case MergeStrategyAppend:
		case MergeStrategyMerge:
			key, hasKey := annotations[MergeKeyAnnotationPrefix+path]
			if !hasKey || !validMergePath(key) {
				warnings = append(warnings, fmt.Errorf("merge strategy for %q has no merge-key", path))
			}
		default:
			warnings = append(warnings, fmt.Errorf("unsupported merge strategy %q for path %q", strategy, path))
		}

		value, found := lookupMergePath(values, path)
		if !found {
			warnings = append(warnings, fmt.Errorf("merge strategy path %q not found in chart defaults", path))
		} else if _, ok := anySlice(value); !ok {
			warnings = append(warnings, fmt.Errorf("merge strategy path %q resolves to a non-array value", path))
		}
	}
	for _, path := range keyPaths {
		if _, ok := annotations[MergeStrategyAnnotationPrefix+path]; !ok {
			warnings = append(warnings, fmt.Errorf("merge-key for path %q has no merge strategy", path))
		}
	}
	if len(warnings) == 0 {
		return nil
	}
	return errors.Join(warnings...)
}
