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

package action

import (
	"maps"
	"strings"

	"helm.sh/helm/v4/internal/copystructure"
	"helm.sh/helm/v4/pkg/chart/common/util"
	chart "helm.sh/helm/v4/pkg/chart/v2"
)

// applyMergeStrategyOverrides records the merge strategy and merge key
// overrides as annotations on the chart, replacing chart annotations for the
// same paths. The chart's annotations map is replaced rather than modified.
func applyMergeStrategyOverrides(c *chart.Chart, strategies, keys []string) error {
	if len(strategies) == 0 && len(keys) == 0 {
		return nil
	}
	overrides, err := util.MergeStrategyOverrideAnnotations(strategies, keys)
	if err != nil {
		return err
	}
	if c.Metadata == nil {
		c.Metadata = &chart.Metadata{}
	}
	annotations := make(map[string]string, len(c.Metadata.Annotations)+len(overrides))
	maps.Copy(annotations, c.Metadata.Annotations)
	maps.Copy(annotations, overrides)
	c.Metadata.Annotations = annotations
	return nil
}

func chartMergeStrategies(c *chart.Chart) map[string]util.MergeStrategy {
	if c.Metadata == nil {
		return nil
	}
	return util.ExtractMergeStrategies(c.Metadata.Annotations)
}

// resetStrategyPaths replaces the values at the strategy paths with the
// corresponding chart defaults, removing them when there is no default.
func resetStrategyPaths(vals, defaults map[string]any, strategies map[string]util.MergeStrategy) {
	for path := range strategies {
		segments := strings.Split(path, ".")
		last := segments[len(segments)-1]

		parent, ok := tableAt(vals, segments[:len(segments)-1])
		if !ok {
			continue
		}
		defaultParent, ok := tableAt(defaults, segments[:len(segments)-1])
		if !ok {
			delete(parent, last)
			continue
		}
		dv, ok := defaultParent[last]
		if !ok {
			delete(parent, last)
			continue
		}
		if copied, err := copystructure.Copy(dv); err == nil {
			dv = copied
		}
		parent[last] = dv
	}
}

func tableAt(m map[string]any, segments []string) (map[string]any, bool) {
	for _, seg := range segments {
		next, ok := m[seg].(map[string]any)
		if !ok {
			return nil, false
		}
		m = next
	}
	return m, m != nil
}
