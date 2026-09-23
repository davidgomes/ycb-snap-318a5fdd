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

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnifiedManifestOrdersBySourceAndHooksFirst(t *testing.T) {
	manifest := "---\n# Source: chart/templates/b.yaml\nkind: ConfigMap\nmetadata:\n  name: b\n---\n# Source: chart/templates/a.yaml\nkind: Service\nmetadata:\n  name: late\n---\n# Source: chart/templates/a.yaml\nkind: ConfigMap\nmetadata:\n  name: early\n"
	hooks := []manifestHook{{
		path:     "chart/templates/a.yaml",
		manifest: "kind: Job\nmetadata:\n  name: hook\n",
	}, {
		path:     "chart/templates/c.yaml",
		manifest: "kind: Job\nmetadata:\n  name: other\n",
	}}

	got := unifiedManifest(manifest, hooks)
	want := `---
# Source: chart/templates/a.yaml
kind: Job
metadata:
  name: hook
---
# Source: chart/templates/a.yaml
kind: Service
metadata:
  name: late
---
# Source: chart/templates/a.yaml
kind: ConfigMap
metadata:
  name: early
---
# Source: chart/templates/b.yaml
kind: ConfigMap
metadata:
  name: b
---
# Source: chart/templates/c.yaml
kind: Job
metadata:
  name: other
`
	assert.Equal(t, want, got)
	assert.True(t, len(got) > 0 && got[len(got)-1] == '\n')
	assert.False(t, len(got) > 1 && got[len(got)-2] == '\n')
}
