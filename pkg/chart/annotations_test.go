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

func TestAccessorAnnotations(t *testing.T) {
	v2 := &v2chart.Chart{Metadata: &v2chart.Metadata{Name: "v2", Annotations: map[string]string{"k": "v"}}}
	acc, err := NewDefaultAccessor(v2)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"k": "v"}, acc.Annotations())

	empty := &v2chart.Chart{}
	acc, err = NewDefaultAccessor(empty)
	require.NoError(t, err)
	assert.Nil(t, acc.Annotations())

	v3 := &v3chart.Chart{Metadata: &v3chart.Metadata{Name: "v3", Annotations: map[string]string{"a": "b"}}}
	acc, err = NewDefaultAccessor(v3)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "b"}, acc.Annotations())
}
