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

func TestUnifiedManifestStream(t *testing.T) {
	manifest := "---\n# Source: c/templates/b.yaml\nb1: 1\n---\n# Source: c/templates/b.yaml\nb2: 2\n---\n# Source: c/templates/a.yaml\na: 1\n"
	docs := splitManifestDocs(manifest)
	docs = append(docs, hookManifestDoc("c/templates/b.yaml", "hook: 1\n"))

	expected := "---\n# Source: c/templates/a.yaml\na: 1\n" +
		"---\n# Source: c/templates/b.yaml\nhook: 1\n" +
		"---\n# Source: c/templates/b.yaml\nb1: 1\n" +
		"---\n# Source: c/templates/b.yaml\nb2: 2\n"
	assert.Equal(t, expected, unifiedManifestStream(docs))
	assert.Equal(t, "", unifiedManifestStream(nil))
}
