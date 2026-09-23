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
	"fmt"
	"sort"
	"strings"

	release "helm.sh/helm/v4/pkg/release/v1"
)

const sourcePrefix = "# Source: "

// StreamDocument is a single YAML document of a unified manifest stream.
type StreamDocument struct {
	// Source is the chart path of the template that rendered the document.
	Source string
	// Content is the YAML of the document, without the Source comment.
	Content string
	// Head is the parsed head of the document, when known.
	Head *SimpleHead
	// Hook is set when the document is a hook.
	Hook *release.Hook
}

// ParseManifestStream splits a stream of YAML documents, as produced by
// FormatManifestStream or stored as a release manifest, into its documents.
// Documents without a leading "# Source: " comment get an empty Source.
func ParseManifestStream(stream string) []StreamDocument {
	split := SplitManifests(stream)
	keys := make([]string, 0, len(split))
	for k := range split {
		keys = append(keys, k)
	}
	sort.Sort(BySplitManifestsOrder(keys))

	docs := make([]StreamDocument, 0, len(keys))
	for _, k := range keys {
		content := split[k]
		var source string
		if rest, ok := strings.CutPrefix(content, sourcePrefix); ok {
			source, content, _ = strings.Cut(rest, "\n")
		}
		docs = append(docs, StreamDocument{Source: source, Content: content})
	}
	return docs
}

// FormatManifestStream writes the documents as a single YAML stream, ordered
// lexicographically by Source. Documents sharing a Source keep their relative
// order. Every document is terminated by a newline.
func FormatManifestStream(docs []StreamDocument) string {
	sorted := make([]StreamDocument, len(docs))
	copy(sorted, docs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Source < sorted[j].Source
	})

	var b strings.Builder
	for _, d := range sorted {
		if d.Source == "" {
			fmt.Fprintf(&b, "---\n%s\n", d.Content)
		} else {
			fmt.Fprintf(&b, "---\n%s%s\n%s\n", sourcePrefix, d.Source, d.Content)
		}
	}
	return b.String()
}
