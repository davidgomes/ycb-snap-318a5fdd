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

	release "helm.sh/helm/v4/pkg/release/v1"
)

func TestUnifiedManifestStream(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		hooks    []*release.Hook
		expected string
	}{
		{
			name:     "empty",
			expected: "",
		},
		{
			name:     "sorted by source path, keeping in-file order",
			manifest: "---\n# Source: c/templates/b.yaml\nkind: B2\n---\n# Source: c/templates/a.yaml\nkind: A\n---\n# Source: c/templates/b.yaml\nkind: B1\n",
			expected: "---\n# Source: c/templates/a.yaml\nkind: A\n---\n# Source: c/templates/b.yaml\nkind: B2\n---\n# Source: c/templates/b.yaml\nkind: B1\n",
		},
		{
			name:     "hooks included and placed before resources sharing a source",
			manifest: "---\n# Source: c/templates/a.yaml\nkind: ConfigMap\n---\n# Source: c/templates/z.yaml\nkind: Service\n",
			hooks: []*release.Hook{
				{Path: "c/templates/a.yaml", Manifest: "kind: Job\n"},
				{Path: "c/templates/m.yaml", Manifest: "kind: Pod"},
			},
			expected: "---\n# Source: c/templates/a.yaml\nkind: Job\n---\n# Source: c/templates/a.yaml\nkind: ConfigMap\n---\n# Source: c/templates/m.yaml\nkind: Pod\n---\n# Source: c/templates/z.yaml\nkind: Service\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := manifestDocs(tt.manifest)
			for _, h := range tt.hooks {
				docs = append(docs, hookDoc(h.Path, h.Manifest))
			}
			assert.Equal(t, tt.expected, unifiedManifestStream(docs))
		})
	}
}

func TestWithoutHooks(t *testing.T) {
	stream := "---\n# Source: c/templates/a.yaml\nkind: Job\n---\n# Source: c/templates/a.yaml\nkind: ConfigMap\n---\n# Source: c/templates/t.yaml\nkind: Pod\n"

	tests := []struct {
		name     string
		hooks    []*release.Hook
		expected string
	}{
		{
			name:     "no hooks omitted",
			expected: stream,
		},
		{
			name:     "omits only matching hook documents",
			hooks:    []*release.Hook{{Path: "c/templates/t.yaml", Manifest: "kind: Pod"}},
			expected: "---\n# Source: c/templates/a.yaml\nkind: Job\n---\n# Source: c/templates/a.yaml\nkind: ConfigMap\n",
		},
		{
			name: "omits all hooks",
			hooks: []*release.Hook{
				{Path: "c/templates/a.yaml", Manifest: "kind: Job"},
				{Path: "c/templates/t.yaml", Manifest: "kind: Pod"},
			},
			expected: "---\n# Source: c/templates/a.yaml\nkind: ConfigMap\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, withoutHooks(stream, tt.hooks))
		})
	}
}
