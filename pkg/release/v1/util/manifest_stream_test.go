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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	release "helm.sh/helm/v4/pkg/release/v1"
)

func TestOrderManifestsSourceOrder(t *testing.T) {
	files := map[string]string{
		"demo/templates/b.yaml": `apiVersion: v1
kind: Service
metadata:
  name: from-b
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-b
`,
		"demo/templates/a.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: from-a
---
apiVersion: batch/v1
kind: Job
metadata:
  name: hook-a
  annotations:
    "helm.sh/hook": pre-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-a
`,
	}

	hooks, docs, err := OrderManifests(files, nil)
	require.NoError(t, err)
	require.Len(t, hooks, 1)
	assert.Equal(t, "demo/templates/a.yaml", hooks[0].Path)
	assert.Equal(t, "hook-a", hooks[0].Name)

	require.Len(t, docs, 5)
	got := make([]string, 0, len(docs))
	for _, doc := range docs {
		got = append(got, doc.Source+" "+doc.Kind+" hook="+boolString(doc.Hook))
	}
	assert.Equal(t, []string{
		"demo/templates/a.yaml Deployment hook=false",
		"demo/templates/a.yaml Job hook=true",
		"demo/templates/a.yaml ConfigMap hook=false",
		"demo/templates/b.yaml Service hook=false",
		"demo/templates/b.yaml ConfigMap hook=false",
	}, got)

	stream := FormatManifestDocuments(docs)
	assert.True(t, strings.HasSuffix(stream, "\n"))
	assert.False(t, strings.HasSuffix(stream, "\n\n"))
	deploy := strings.Index(stream, "name: from-a")
	hook := strings.Index(stream, "name: hook-a")
	cm := strings.LastIndex(stream, "name: from-a")
	assert.Less(t, deploy, hook)
	assert.Less(t, hook, cm)
}

func TestReleaseManifestStreamHooksBeforeResources(t *testing.T) {
	manifest := `---
# Source: demo/templates/b.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: b
---
# Source: demo/templates/a.yaml
apiVersion: v1
kind: Service
metadata:
  name: svc
---
# Source: demo/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: a
`
	hooks := []*release.Hook{
		{
			Name:     "hook-b",
			Kind:     "Job",
			Path:     "demo/templates/b.yaml",
			Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: hook-b\n",
		},
		{
			Name:     "hook-a",
			Kind:     "Pod",
			Path:     "demo/templates/a.yaml",
			Manifest: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: hook-a\n",
		},
	}

	stream := ReleaseManifestStream(manifest, hooks)
	assert.Equal(t, `---
# Source: demo/templates/a.yaml
apiVersion: v1
kind: Pod
metadata:
  name: hook-a
---
# Source: demo/templates/a.yaml
apiVersion: v1
kind: Service
metadata:
  name: svc
---
# Source: demo/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: a
---
# Source: demo/templates/b.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: hook-b
---
# Source: demo/templates/b.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: b
`, stream)
	assert.False(t, strings.HasSuffix(strings.TrimRight(stream, "\n"), "\n\n"))
}

func TestKindSortedManifestRestoresInstallOrder(t *testing.T) {
	manifest := `---
# Source: demo/templates/a.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: deploy
---
# Source: demo/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm
---
# Source: demo/templates/b.yaml
apiVersion: v1
kind: Service
metadata:
  name: svc
`
	sorted, err := KindSortedManifest(manifest, InstallOrder)
	require.NoError(t, err)
	assert.Equal(t, `---
# Source: demo/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm
---
# Source: demo/templates/b.yaml
apiVersion: v1
kind: Service
metadata:
  name: svc
---
# Source: demo/templates/a.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: deploy
`, sorted)
}

func TestReleaseManifestStreamPlainManifest(t *testing.T) {
	manifest := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: fixture\n"
	assert.Equal(t, manifest, ReleaseManifestStream(manifest, nil))
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
