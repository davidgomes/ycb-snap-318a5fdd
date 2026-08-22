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
)

func TestUnifiedStreamFromFilesKeepsInFileOrder(t *testing.T) {
	files := map[string]string{
		"chart/templates/z.yaml": "kind: ConfigMap\nmetadata:\n  name: z\n",
		"chart/templates/a.yaml": `kind: ConfigMap
metadata:
  name: first
---
kind: Job
metadata:
  name: hook
  annotations:
    "helm.sh/hook": pre-install
---
kind: ConfigMap
metadata:
  name: third
`,
	}

	got := UnifiedStreamFromFiles(files)
	assert.True(t, strings.HasSuffix(got, "\n"))
	assert.False(t, strings.HasSuffix(got, "\n\n"))

	first := strings.Index(got, "name: first")
	hook := strings.Index(got, "name: hook")
	third := strings.Index(got, "name: third")
	z := strings.Index(got, "name: z")
	assert.Less(t, first, hook)
	assert.Less(t, hook, third)
	assert.Less(t, third, z)
}

func TestUnifiedStreamHooksBeforeNonHooksSameSource(t *testing.T) {
	manifest := `---
# Source: chart/templates/mixed.yaml
kind: ConfigMap
metadata:
  name: regular
`
	got := UnifiedStream(manifest, []StreamHook{{
		Path: "chart/templates/mixed.yaml",
		Manifest: `kind: Job
metadata:
  name: hook
`,
	}})
	assert.Less(t, strings.Index(got, "name: hook"), strings.Index(got, "name: regular"))
}
