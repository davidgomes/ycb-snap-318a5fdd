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
	"github.com/stretchr/testify/require"
)

func TestSortManifestsWithStream(t *testing.T) {
	files := map[string]string{
		"chart/templates/b.yaml": `kind: Deployment
apiVersion: apps/v1
metadata:
  name: b-deploy
---
kind: Job
apiVersion: batch/v1
metadata:
  name: b-hook
  annotations:
    "helm.sh/hook": pre-install
---
kind: Namespace
apiVersion: v1
metadata:
  name: b-ns
`,
		"chart/templates/a.yaml": `kind: Service
apiVersion: v1
metadata:
  name: a-svc
`,
		"chart/templates/_helpers.tpl": `{{/* partial */}}`,
		"chart/templates/empty.yaml":   "  \n",
	}

	hooks, manifests, stream, err := SortManifestsWithStream(files, nil, InstallOrder)
	require.NoError(t, err)
	require.Len(t, hooks, 1)
	require.Len(t, manifests, 3)

	var names []string
	for _, d := range stream {
		names = append(names, d.Head.Metadata.Name)
	}
	assert.Equal(t, []string{"a-svc", "b-deploy", "b-hook", "b-ns"}, names)
	assert.Same(t, hooks[0], stream[2].Hook)
	assert.Nil(t, stream[1].Hook)
	assert.Equal(t, "chart/templates/b.yaml", stream[2].Source)
}

func TestFormatManifestStream(t *testing.T) {
	tests := []struct {
		name string
		docs []StreamDocument
		want string
	}{
		{
			name: "empty",
			want: "",
		},
		{
			name: "orders by source and keeps order within a source",
			docs: []StreamDocument{
				{Source: "chart/templates/b.yaml", Content: "name: b1"},
				{Source: "chart/charts/sub/templates/a.yaml", Content: "name: sub"},
				{Source: "chart/templates/b.yaml", Content: "name: b2"},
				{Source: "chart/templates/a.yaml", Content: "name: a"},
			},
			want: "---\n# Source: chart/charts/sub/templates/a.yaml\nname: sub\n" +
				"---\n# Source: chart/templates/a.yaml\nname: a\n" +
				"---\n# Source: chart/templates/b.yaml\nname: b1\n" +
				"---\n# Source: chart/templates/b.yaml\nname: b2\n",
		},
		{
			name: "documents without source",
			docs: []StreamDocument{
				{Source: "chart/templates/a.yaml", Content: "name: a"},
				{Content: "name: unknown"},
			},
			want: "---\nname: unknown\n---\n# Source: chart/templates/a.yaml\nname: a\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatManifestStream(tt.docs))
		})
	}
}

func TestParseManifestStream(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		want   []StreamDocument
	}{
		{
			name:   "empty",
			stream: "",
			want:   []StreamDocument{},
		},
		{
			name:   "release manifest",
			stream: "---\n# Source: chart/templates/a.yaml\nname: a\n---\n# Source: chart/templates/b.yaml\n# HIDDEN: The Secret output has been suppressed\n",
			want: []StreamDocument{
				{Source: "chart/templates/a.yaml", Content: "name: a"},
				{Source: "chart/templates/b.yaml", Content: "# HIDDEN: The Secret output has been suppressed"},
			},
		},
		{
			name:   "documents without source",
			stream: "name: a\n---\nname: b\n",
			want: []StreamDocument{
				{Content: "name: a"},
				{Content: "name: b"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseManifestStream(tt.stream)
			assert.Equal(t, tt.want, got)
			if len(got) > 0 {
				assert.Equal(t, got, ParseManifestStream(FormatManifestStream(got)))
			}
		})
	}
}
