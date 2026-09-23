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

package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"helm.sh/helm/v4/pkg/chart/v2/lint/support"
)

func TestChartfileMergeStrategies(t *testing.T) {
	const chartHeader = `apiVersion: v2
name: strategies
version: 1.0.0
icon: https://example.com/icon.png
`
	values := `
list: [a]
env: [{name: a}]
scalar: 1
`
	tests := []struct {
		name        string
		annotations string
		values      *string
		expected    []string
	}{
		{
			name: "valid annotations",
			annotations: `
  helm.sh/merge-strategy/list: append
  helm.sh/merge-strategy/env: merge
  helm.sh/merge-key/env: name
`,
			values: &values,
		},
		{
			name: "invalid annotations",
			annotations: `
  helm.sh/merge-strategy/list: prepend
  helm.sh/merge-strategy/env: merge
  helm.sh/merge-key/orphan: name
  helm.sh/merge-strategy/missing: append
  helm.sh/merge-strategy/scalar: append
`,
			values: &values,
			expected: []string{
				`merge key for path "orphan" has no corresponding`,
				`merge strategy "merge" for path "env" requires`,
				`unsupported merge strategy "prepend" for path "list"`,
				`merge strategy path "missing" not found`,
				`merge strategy path "scalar" resolves to a non-array value`,
			},
		},
		{
			name: "missing values file",
			annotations: `
  helm.sh/merge-strategy/list: append
`,
			expected: []string{`merge strategy path "list" not found`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartHeader+"annotations:"+tt.annotations), 0o644))
			if tt.values != nil {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(*tt.values), 0o644))
			}

			linter := support.Linter{ChartDir: dir}
			Chartfile(&linter)

			require.Len(t, linter.Messages, len(tt.expected), "%v", linter.Messages)
			for i, e := range tt.expected {
				assert.Equal(t, support.WarningSev, linter.Messages[i].Severity)
				assert.Equal(t, "Chart.yaml", linter.Messages[i].Path)
				assert.Contains(t, linter.Messages[i].Err.Error(), e)
			}
		})
	}
}
