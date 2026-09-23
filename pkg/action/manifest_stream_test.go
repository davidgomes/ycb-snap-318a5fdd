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
)

func TestRenderResourcesWithStream_RenderedOrder(t *testing.T) {
	cfg := actionConfigFixture(t)
	modTime := time.Now()
	ch := buildChartWithTemplates([]*common.File{
		{Name: "templates/b.yaml", ModTime: modTime, Data: []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: b1
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: b2
  annotations:
    "helm.sh/hook": pre-install
---
apiVersion: v1
kind: Namespace
metadata:
  name: b3
`)},
		{Name: "templates/a.yaml", ModTime: modTime, Data: []byte(`apiVersion: v1
kind: Service
metadata:
  name: a1
`)},
	})

	hooks, buf, stream, _, err := cfg.renderResourcesWithStream(ch, map[string]any{}, "test-release", "", false, false, false, nil, false, false, false)
	require.NoError(t, err)
	require.Len(t, hooks, 1)

	expectedStream := `---
# Source: hello/templates/a.yaml
apiVersion: v1
kind: Service
metadata:
  name: a1
---
# Source: hello/templates/b.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: b1
---
# Source: hello/templates/b.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: b2
  annotations:
    "helm.sh/hook": pre-install
---
# Source: hello/templates/b.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: b3
`
	assert.Equal(t, expectedStream, stream)

	// The release manifest keeps install order, which is unaffected by the stream.
	expectedManifest := `---
# Source: hello/templates/b.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: b3
---
# Source: hello/templates/a.yaml
apiVersion: v1
kind: Service
metadata:
  name: a1
---
# Source: hello/templates/b.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: b1
`
	assert.Equal(t, expectedManifest, buf.String())
}
