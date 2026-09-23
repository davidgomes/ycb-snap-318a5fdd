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
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	release "helm.sh/helm/v4/pkg/release/v1"
)

func TestSplitManifestStream(t *testing.T) {
	tests := []struct {
		name     string
		stream   string
		expected []manifestDocument
	}{
		{
			name:     "empty",
			stream:   "",
			expected: []manifestDocument{},
		},
		{
			name:   "documents with sources",
			stream: "---\n# Source: c/templates/b.yaml\nb: 1\n---\n# Source: c/templates/a.yaml\na: 1\n",
			expected: []manifestDocument{
				{source: "c/templates/b.yaml", content: "b: 1"},
				{source: "c/templates/a.yaml", content: "a: 1"},
			},
		},
		{
			name:   "documents without a source belong to the previous source",
			stream: "first: 1\n---\n# Source: c/templates/a.yaml\na: 1\n---\na: 2\n",
			expected: []manifestDocument{
				{source: "", content: "first: 1"},
				{source: "c/templates/a.yaml", content: "a: 1"},
				{source: "c/templates/a.yaml", content: "a: 2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, splitManifestStream(tt.stream))
		})
	}
}

func TestMergeManifestStream(t *testing.T) {
	manifest := "---\n# Source: c/templates/z.yaml\nz: 1\n---\n# Source: c/templates/a.yaml\na: 1\n---\n# Source: c/templates/a.yaml\na: 2\n"
	hooks := []manifestDocument{
		{source: "c/templates/tests/test.yaml", content: "test: 1"},
		{source: "c/templates/a.yaml", content: "hook: 1"},
		{source: "c/templates/a.yaml", content: "hook: 2"},
	}

	assert.Equal(t, []manifestDocument{
		{source: "c/templates/a.yaml", content: "hook: 1"},
		{source: "c/templates/a.yaml", content: "hook: 2"},
		{source: "c/templates/a.yaml", content: "a: 1"},
		{source: "c/templates/a.yaml", content: "a: 2"},
		{source: "c/templates/tests/test.yaml", content: "test: 1"},
		{source: "c/templates/z.yaml", content: "z: 1"},
	}, mergeManifestStream(manifest, hooks))
}

func TestReleaseManifestStream(t *testing.T) {
	setupHook := &release.Hook{Path: "c/templates/a.yaml", Manifest: "hook: setup\n", Events: []release.HookEvent{release.HookPreInstall}}
	testHook := &release.Hook{Path: "c/templates/tests/test.yaml", Manifest: "hook: test", Events: []release.HookEvent{release.HookTest}}
	rel := &release.Release{
		Manifest: "---\n# Source: c/templates/z.yaml\nz: 1\n---\n# Source: c/templates/a.yaml\na: 1\n",
		Hooks:    []*release.Hook{testHook, setupHook},
	}
	rendered := *rel
	rendered.ManifestStream = "---\n# Source: c/templates/a.yaml\na: 1\n---\n# Source: c/templates/a.yaml\nhook: setup\n---\n# Source: c/templates/tests/test.yaml\nhook: test\n---\n# Source: c/templates/z.yaml\nz: 1\n"

	noTests := func(h *release.Hook) bool { return !isTestHook(h) }

	tests := []struct {
		name        string
		rel         *release.Release
		includeHook func(*release.Hook) bool
		expected    string
	}{
		{
			name:        "rendered stream keeps hooks in template order",
			rel:         &rendered,
			includeHook: allHooks,
			expected:    rendered.ManifestStream,
		},
		{
			name:        "rendered stream without excluded hooks",
			rel:         &rendered,
			includeHook: noTests,
			expected:    "---\n# Source: c/templates/a.yaml\na: 1\n---\n# Source: c/templates/a.yaml\nhook: setup\n---\n# Source: c/templates/z.yaml\nz: 1\n",
		},
		{
			name:        "stored release places hooks before resources of the same source",
			rel:         rel,
			includeHook: allHooks,
			expected:    "---\n# Source: c/templates/a.yaml\nhook: setup\n---\n# Source: c/templates/a.yaml\na: 1\n---\n# Source: c/templates/tests/test.yaml\nhook: test\n---\n# Source: c/templates/z.yaml\nz: 1\n",
		},
		{
			name:        "stored release without excluded hooks",
			rel:         rel,
			includeHook: noTests,
			expected:    "---\n# Source: c/templates/a.yaml\nhook: setup\n---\n# Source: c/templates/a.yaml\na: 1\n---\n# Source: c/templates/z.yaml\nz: 1\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			writeManifestStream(&out, releaseManifestStream(tt.rel, tt.includeHook))
			assert.Equal(t, tt.expected, out.String())
		})
	}
}
