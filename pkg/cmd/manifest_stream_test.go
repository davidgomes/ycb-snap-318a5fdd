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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	release "helm.sh/helm/v4/pkg/release/v1"
)

func TestUnifiedManifestStreamOrder(t *testing.T) {
	manifest := `---
# Source: chart/templates/z.yaml
kind: ConfigMap
metadata:
  name: z
---
# Source: chart/templates/a.yaml
kind: Secret
metadata:
  name: a-second
---
# Source: chart/templates/a.yaml
kind: ConfigMap
metadata:
  name: a-first
`
	hooks := []manifestStreamHook{{
		Path: "chart/templates/m.yaml",
		Manifest: `kind: Job
metadata:
  name: hook
  annotations:
    "helm.sh/hook": pre-install
`,
	}}

	got := unifiedManifestStream(manifest, hooks)
	assert.True(t, strings.HasSuffix(got, "\n"), "unified stream must end with a trailing newline")
	assert.False(t, strings.HasSuffix(got, "\n\n"), "unified stream must not add extra trailing blank lines")

	sources := sourceOrder(got)
	assert.Equal(t, []string{
		"chart/templates/a.yaml",
		"chart/templates/a.yaml",
		"chart/templates/m.yaml",
		"chart/templates/z.yaml",
	}, sources)

	// Within a.yaml, documents stay in rendered (manifest) order.
	assert.Contains(t, got, "name: a-second")
	second := strings.Index(got, "name: a-second")
	first := strings.Index(got, "name: a-first")
	assert.Greater(t, first, second)
}

func TestUnifiedManifestStreamHooksBeforeNonHooksSameSource(t *testing.T) {
	manifest := `---
# Source: chart/templates/mixed.yaml
kind: ConfigMap
metadata:
  name: regular
`
	hooks := []manifestStreamHook{{
		Path: "chart/templates/mixed.yaml",
		Manifest: `kind: Job
metadata:
  name: hook
  annotations:
    "helm.sh/hook": pre-install
`,
	}}

	got := unifiedManifestStream(manifest, hooks)
	hookAt := strings.Index(got, "name: hook")
	regularAt := strings.Index(got, "name: regular")
	assert.Greater(t, hookAt, -1)
	assert.Greater(t, regularAt, -1)
	assert.Less(t, hookAt, regularAt)
}

func TestUnifiedManifestStreamEmpty(t *testing.T) {
	assert.Equal(t, "", unifiedManifestStream("", nil))
	assert.Equal(t, "", unifiedManifestStream("\n---\n\n", nil))
}

func TestUnifiedManifestStreamFromV1IncludesHooks(t *testing.T) {
	rel := release.Mock(&release.MockReleaseOptions{Name: "juno"})
	got := unifiedManifestStreamFromV1(rel, rel.Hooks)
	assert.Contains(t, got, "# Source: pre-install-hook.yaml")
	assert.Contains(t, got, "kind: Job")
	assert.Contains(t, got, "kind: Secret")
	assert.True(t, strings.HasSuffix(got, "\n"))
}

func sourceOrder(stream string) []string {
	var sources []string
	for line := range strings.SplitSeq(stream, "\n") {
		if strings.HasPrefix(line, "# Source:") {
			sources = append(sources, strings.TrimSpace(strings.TrimPrefix(line, "# Source:")))
		}
	}
	return sources
}
