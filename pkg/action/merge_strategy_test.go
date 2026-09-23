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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"helm.sh/helm/v4/pkg/chart/common"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	rcommon "helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
)

func withAnnotations(annotations map[string]string) chartOption {
	return func(opts *chartOptions) {
		opts.Metadata.Annotations = annotations
	}
}

func buildStrategyChart(defaults []any, annotations map[string]string) *chart.Chart {
	return buildChartWithTemplates([]*common.File{
		{Name: "templates/values", ModTime: time.Now(), Data: []byte(`list: {{ toJson .Values.list }}
env: {{ toJson .Values.env }}`)},
	}, withValues(map[string]any{"list": defaults}), withAnnotations(annotations))
}

func TestInstallMergeStrategies(t *testing.T) {
	annotations := map[string]string{"helm.sh/merge-strategy/list": "append"}
	chrt := buildStrategyChart([]any{"d"}, annotations)
	chrt.Values["env"] = []any{
		map[string]any{"name": "A", "value": "a"},
		map[string]any{"name": "B", "value": "b"},
	}

	instAction := installAction(t)
	instAction.MergeStrategies = []string{"env=merge"}
	instAction.MergeKeys = []string{"env=name"}
	resi, err := instAction.Run(chrt, map[string]any{
		"list": []any{"u"},
		"env":  []any{map[string]any{"name": "B", "value": "user"}},
	})
	require.NoError(t, err)
	res, err := releaserToV1Release(resi)
	require.NoError(t, err)

	assert.Contains(t, res.Manifest, `list: ["d","u"]`)
	assert.Contains(t, res.Manifest, `env: [{"name":"A","value":"a"},{"name":"B","value":"user"}]`)
	assert.Equal(t, map[string]string{
		"helm.sh/merge-strategy/list": "append",
		"helm.sh/merge-strategy/env":  "merge",
		"helm.sh/merge-key/env":       "name",
	}, res.Chart.Metadata.Annotations)
	assert.Equal(t, map[string]string{"helm.sh/merge-strategy/list": "append"}, annotations, "the original annotations map must not be modified")
}

func TestInstallMergeStrategyOverridesTakePrecedence(t *testing.T) {
	chrt := buildStrategyChart([]any{map[string]any{"name": "A", "value": "a"}}, map[string]string{
		"helm.sh/merge-strategy/list": "merge",
		"helm.sh/merge-key/list":      "value",
	})

	instAction := installAction(t)
	instAction.MergeKeys = []string{"list=name"}
	resi, err := instAction.Run(chrt, map[string]any{"list": []any{map[string]any{"name": "A", "value": "user"}}})
	require.NoError(t, err)
	res, err := releaserToV1Release(resi)
	require.NoError(t, err)
	assert.Contains(t, res.Manifest, `list: [{"name":"A","value":"user"}]`)
}

func TestInstallInvalidMergeStrategyOverride(t *testing.T) {
	instAction := installAction(t)
	instAction.MergeStrategies = []string{"list=replace"}
	_, err := instAction.Run(buildStrategyChart([]any{"d"}, nil), map[string]any{})
	assert.ErrorContains(t, err, "unsupported strategy")
}

func TestUpgradeMergeStrategies(t *testing.T) {
	annotations := map[string]string{"helm.sh/merge-strategy/list": "append"}
	tests := []struct {
		name             string
		configure        func(*Upgrade)
		expectedConfig   map[string]any
		expectedManifest string
	}{
		{
			name:             "reset values ignores the old config",
			configure:        func(u *Upgrade) { u.ResetValues = true },
			expectedConfig:   map[string]any{"list": []any{"new"}},
			expectedManifest: `list: ["new-default","new"]`,
		},
		{
			name:             "reuse values appends new values after the old config",
			configure:        func(u *Upgrade) { u.ReuseValues = true },
			expectedConfig:   map[string]any{"list": []any{"old", "new"}},
			expectedManifest: `list: ["old-default","old","new"]`,
		},
		{
			name:             "reset then reuse values uses the new chart defaults as base",
			configure:        func(u *Upgrade) { u.ResetThenReuseValues = true },
			expectedConfig:   map[string]any{"list": []any{"old", "new"}},
			expectedManifest: `list: ["new-default","old","new"]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upAction := upgradeAction(t)
			now := time.Now()
			rel := &release.Release{
				Name: "strategies",
				Info: &release.Info{
					FirstDeployed: now,
					LastDeployed:  now,
					Status:        rcommon.StatusDeployed,
				},
				Chart:   buildStrategyChart([]any{"old-default"}, annotations),
				Config:  map[string]any{"list": []any{"old"}},
				Version: 1,
			}
			require.NoError(t, upAction.cfg.Releases.Create(rel))

			tt.configure(upAction)
			resi, err := upAction.Run(rel.Name, buildStrategyChart([]any{"new-default"}, annotations), map[string]any{"list": []any{"new"}})
			require.NoError(t, err)
			res, err := releaserToV1Release(resi)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedConfig, res.Config)
			assert.Contains(t, res.Manifest, tt.expectedManifest)
		})
	}
}
