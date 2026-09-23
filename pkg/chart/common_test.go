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

package chart

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v3chart "helm.sh/helm/v4/internal/chart/v3"
	v2chart "helm.sh/helm/v4/pkg/chart/v2"
)

func TestAccessorAnnotationsFromMetadata(t *testing.T) {
	annotations := map[string]string{"helm.sh/merge-strategy/items": "append"}

	tests := []struct {
		name     string
		chart    Charter
		expected map[string]string
	}{
		{name: "v2", chart: &v2chart.Chart{Metadata: &v2chart.Metadata{Annotations: annotations}}, expected: annotations},
		{name: "v2 by value", chart: v2chart.Chart{Metadata: &v2chart.Metadata{Annotations: annotations}}, expected: annotations},
		{name: "v2 without metadata", chart: &v2chart.Chart{}},
		{name: "v3", chart: &v3chart.Chart{Metadata: &v3chart.Metadata{Annotations: annotations}}, expected: annotations},
		{name: "v3 without metadata", chart: &v3chart.Chart{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accessor, err := NewAccessor(tt.chart)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, accessor.Annotations())
		})
	}
}
