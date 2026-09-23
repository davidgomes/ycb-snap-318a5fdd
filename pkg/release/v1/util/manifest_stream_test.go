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
)

func TestDocumentsInSourceOrder(t *testing.T) {
	files := map[string]string{
		"chart/templates/b.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: b\n",
		"chart/templates/a.yaml": strings.Join([]string{
			"apiVersion: v1",
			"kind: ConfigMap",
			"metadata:",
			"  name: first",
			"---",
			"apiVersion: v1",
			"kind: Job",
			"metadata:",
			"  name: hook",
			"  annotations:",
			"    \"helm.sh/hook\": pre-install",
			"---",
			"apiVersion: v1",
			"kind: Secret",
			"metadata:",
			"  name: second",
		}, "\n"),
		"chart/templates/_helpers.tpl": "{{/* partial */}}",
	}

	docs, err := DocumentsInSourceOrder(files)
	require.NoError(t, err)
	require.Len(t, docs, 4)

	assert.Equal(t, []string{
		"chart/templates/a.yaml",
		"chart/templates/a.yaml",
		"chart/templates/a.yaml",
		"chart/templates/b.yaml",
	}, []string{docs[0].Source, docs[1].Source, docs[2].Source, docs[3].Source})
	assert.False(t, docs[0].Hook)
	assert.Contains(t, docs[0].Body, "name: first")
	assert.True(t, docs[1].Hook)
	assert.Contains(t, docs[1].Body, "name: hook")
	assert.False(t, docs[2].Hook)
	assert.Contains(t, docs[2].Body, "name: second")
	assert.Contains(t, docs[3].Body, "name: b")
}

func TestFormatManifestStreamTrailingNewlineAndHiddenSecret(t *testing.T) {
	docs := []StreamDocument{{
		Source: "chart/templates/secret.yaml",
		Body:   "apiVersion: v1\nkind: Secret\nmetadata:\n  name: s\n",
	}, {
		Source: "chart/templates/cm.yaml",
		Body:   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n",
	}}

	stream := FormatManifestStream(docs, StreamFormatOptions{HideSecrets: true})
	assert.True(t, strings.HasSuffix(stream, "\n"))
	assert.False(t, strings.HasSuffix(stream, "\n\n"))
	assert.NotContains(t, stream, "kind: Secret")
	assert.Contains(t, stream, "# HIDDEN: The Secret output has been suppressed")
	assert.Contains(t, stream, "kind: ConfigMap")

	// Source order is the caller's responsibility; formatting keeps input order.
	assert.Less(t, strings.Index(stream, "secret.yaml"), strings.Index(stream, "cm.yaml"))
}

func TestInstallOrderManifestPreservesSourceAndKindOrder(t *testing.T) {
	manifest := strings.Join([]string{
		"---",
		"# Source: chart/templates/deploy.yaml",
		"apiVersion: apps/v1",
		"kind: Deployment",
		"metadata:",
		"  name: app",
		"---",
		"# Source: chart/templates/ns.yaml",
		"apiVersion: v1",
		"kind: Namespace",
		"metadata:",
		"  name: demo",
		"---",
		"# Source: chart/templates/deploy.yaml",
		"apiVersion: v1",
		"kind: ConfigMap",
		"metadata:",
		"  name: cfg",
	}, "\n")

	sorted, err := InstallOrderManifest(manifest)
	require.NoError(t, err)
	positions := []string{
		"# Source: chart/templates/ns.yaml",
		"kind: Namespace",
		"# Source: chart/templates/deploy.yaml",
		"kind: ConfigMap",
		"kind: Deployment",
	}
	last := -1
	for _, part := range positions {
		idx := strings.Index(sorted, part)
		require.Greaterf(t, idx, last, "expected %q later in:\n%s", part, sorted)
		last = idx
	}
}

func TestUnifiedReleaseManifestHooksBeforeResources(t *testing.T) {
	manifest := strings.Join([]string{
		"---",
		"# Source: chart/templates/z.yaml",
		"apiVersion: v1",
		"kind: ConfigMap",
		"metadata:",
		"  name: z",
		"---",
		"# Source: chart/templates/shared.yaml",
		"apiVersion: v1",
		"kind: ConfigMap",
		"metadata:",
		"  name: main",
		"---",
		"# Source: chart/templates/shared.yaml",
		"apiVersion: v1",
		"kind: Service",
		"metadata:",
		"  name: main-svc",
	}, "\n")

	hooks := []StreamDocument{{
		Source: "chart/templates/shared.yaml",
		Body:   "apiVersion: v1\nkind: Job\nmetadata:\n  name: hook-b\n",
	}, {
		Source: "chart/templates/a.yaml",
		Body:   "apiVersion: v1\nkind: Job\nmetadata:\n  name: early\n",
	}, {
		Source: "chart/templates/shared.yaml",
		Body:   "apiVersion: v1\nkind: Job\nmetadata:\n  name: hook-a\n",
	}}

	stream := UnifiedReleaseManifest(manifest, hooks)
	assert.True(t, strings.HasSuffix(stream, "\n"))
	assert.False(t, strings.HasSuffix(stream, "\n\n"))

	positions := []string{
		"# Source: chart/templates/a.yaml",
		"name: early",
		"# Source: chart/templates/shared.yaml",
		"name: hook-b",
		"name: hook-a",
		"name: main",
		"name: main-svc",
		"# Source: chart/templates/z.yaml",
		"name: z",
	}
	last := -1
	for _, part := range positions {
		idx := strings.Index(stream, part)
		require.Greaterf(t, idx, last, "expected %q after previous marker in:\n%s", part, stream)
		last = idx
	}
}
