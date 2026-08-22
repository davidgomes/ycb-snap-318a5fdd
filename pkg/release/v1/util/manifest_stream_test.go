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

func TestFormatManifestStream(t *testing.T) {
	entries := []ManifestStreamEntry{
		{Source: "chart/templates/b.yaml", Content: "kind: ConfigMap\nmetadata:\n  name: b", Order: 0},
		{Source: "chart/templates/a.yaml", Content: "kind: ConfigMap\nmetadata:\n  name: a", Order: 0},
	}

	got := FormatManifestStream(entries, FormatManifestStreamOptions{TrailingNewline: true})
	expected := `---
# Source: chart/templates/b.yaml
kind: ConfigMap
metadata:
  name: b
---
# Source: chart/templates/a.yaml
kind: ConfigMap
metadata:
  name: a
`
	assert.Equal(t, expected, got)
}

func TestSortStreamEntriesForGetManifest(t *testing.T) {
	entries := []ManifestStreamEntry{
		{Source: "chart/templates/mixed.yaml", Content: "kind: ConfigMap\nmetadata:\n  name: regular", IsHook: false, Order: 0},
		{Source: "chart/templates/mixed.yaml", Content: "kind: Job\nmetadata:\n  name: hook\n  annotations:\n    helm.sh/hook: pre-install", IsHook: true, Order: 1},
		{Source: "chart/templates/a.yaml", Content: "kind: Service\nmetadata:\n  name: svc", IsHook: false, Order: 0},
	}

	got := SortStreamEntriesForGetManifest(entries)
	require.Len(t, got, 3)
	assert.Equal(t, "chart/templates/a.yaml", got[0].Source)
	assert.True(t, got[1].IsHook)
	assert.Equal(t, "chart/templates/mixed.yaml", got[1].Source)
	assert.False(t, got[2].IsHook)
	assert.Equal(t, "chart/templates/mixed.yaml", got[2].Source)
}

func TestBuildLegacyGetManifestStream(t *testing.T) {
	manifest := `apiVersion: v1
kind: Secret
metadata:
  name: fixture`
	hooks := []*release.Hook{{
		Path: "pre-install-hook.yaml",
		Manifest: `apiVersion: v1
kind: Job
metadata:
  annotations:
    "helm.sh/hook": pre-install
`,
	}}

	got := BuildGetManifestStream(manifest, hooks)
	expected := `apiVersion: v1
kind: Secret
metadata:
  name: fixture
---
# Source: pre-install-hook.yaml
apiVersion: v1
kind: Job
metadata:
  annotations:
    "helm.sh/hook": pre-install
`
	assert.Equal(t, expected, got)
}

func TestParseAndBuildGetManifestStream(t *testing.T) {
	manifest := `---
# Source: chart/templates/mixed.yaml
kind: ConfigMap
metadata:
  name: regular
---
# Source: chart/templates/a.yaml
kind: Service
metadata:
  name: svc
`
	hooks := []*release.Hook{{
		Path: "chart/templates/mixed.yaml",
		Manifest: `kind: Job
metadata:
  name: hook
  annotations:
    helm.sh/hook: pre-install`,
	}}

	got := BuildGetManifestStream(manifest, hooks)
	assert.Contains(t, got, "# Source: chart/templates/mixed.yaml")
	assert.Contains(t, got, "name: hook")
	assert.Contains(t, got, "name: regular")

	hookIdx := strings.Index(got, "name: hook")
	regularIdx := strings.Index(got, "name: regular")
	assert.Less(t, hookIdx, regularIdx)
}
