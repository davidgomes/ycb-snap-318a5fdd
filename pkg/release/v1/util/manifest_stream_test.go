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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestManifestStreamOrdersBySourceAndKeepsDocumentOrder(t *testing.T) {
	files := map[string]string{
		"chart/templates/b.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: b\n",
		"chart/templates/a.yaml": "" +
			"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: first\n" +
			"---\n" +
			"apiVersion: v1\nkind: Pod\nmetadata:\n  name: hook\n  annotations:\n    helm.sh/hook: pre-install\n" +
			"---\n" +
			"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: third\n",
		"chart/templates/_helpers.tpl": "{{- define \"x\" -}}x{{- end -}}\n",
		"chart/templates/empty.yaml":   "\n",
		"chart/templates/skip.yaml":    "apiVersion: v1\nkind: Job\nmetadata:\n  name: skipped\n  annotations:\n    helm.sh/hook: not-a-real-hook\n",
	}

	got := ManifestStream(files, false)
	want := `---
# Source: chart/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: first
---
# Source: chart/templates/a.yaml
apiVersion: v1
kind: Pod
metadata:
  name: hook
  annotations:
    helm.sh/hook: pre-install
---
# Source: chart/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: third
---
# Source: chart/templates/b.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: b
`
	assert.Equal(t, want, got)
	assert.NotContains(t, got, "skipped")
	assert.NotContains(t, got, "define")
}

func TestManifestStreamHidesNonHookSecrets(t *testing.T) {
	files := map[string]string{
		"chart/templates/secret.yaml": "" +
			"apiVersion: v1\nkind: Secret\nmetadata:\n  name: visible\n" +
			"---\n" +
			"apiVersion: v1\nkind: Secret\nmetadata:\n  name: hidden\n  annotations:\n    helm.sh/hook: pre-install\n",
	}
	got := ManifestStream(files, true)
	assert.Contains(t, got, "# HIDDEN: The Secret output has been suppressed")
	assert.Contains(t, got, "name: hidden")
	assert.NotContains(t, got, "name: visible")
}

func TestStoredManifestStreamHooksBeforeNonHooksSameSource(t *testing.T) {
	manifest := `---
# Source: chart/templates/b.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-b
---
# Source: chart/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-a
---
# Source: chart/templates/b.yaml
apiVersion: v1
kind: Secret
metadata:
  name: also-b
`
	hooks := []SourcedHook{
		{Path: "chart/templates/b.yaml", Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: hook-b\n"},
		{Path: "chart/templates/a.yaml", Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: hook-a\n"},
	}
	got := StoredManifestStream(manifest, hooks)
	want := `---
# Source: chart/templates/a.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: hook-a
---
# Source: chart/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-a
---
# Source: chart/templates/b.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: hook-b
---
# Source: chart/templates/b.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-b
---
# Source: chart/templates/b.yaml
apiVersion: v1
kind: Secret
metadata:
  name: also-b
`
	assert.Equal(t, want, got)
	assert.False(t, len(got) > 0 && got[len(got)-1] != '\n')
	assert.NotContains(t, got, "\n\n---")
}
