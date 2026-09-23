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
	"sort"
	"strings"

	release "helm.sh/helm/v4/pkg/release/v1"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"
)

const sourcePrefix = "# Source: "

// manifestDoc is a single YAML document in a unified manifest stream.
type manifestDoc struct {
	source  string
	content string
	hook    bool
}

// manifestDocs splits a rendered manifest stream into documents, keeping the
// order in which they appear.
func manifestDocs(manifest string) []manifestDoc {
	split := releaseutil.SplitManifests(manifest)
	keys := make([]string, 0, len(split))
	for k := range split {
		keys = append(keys, k)
	}
	sort.Sort(releaseutil.BySplitManifestsOrder(keys))

	docs := make([]manifestDoc, 0, len(keys))
	for _, k := range keys {
		content := split[k]
		docs = append(docs, manifestDoc{source: docSource(content), content: content})
	}
	return docs
}

func hookDoc(path, manifest string) manifestDoc {
	return manifestDoc{
		source:  path,
		content: sourcePrefix + path + "\n" + manifest,
		hook:    true,
	}
}

func docSource(content string) string {
	firstLine, _, _ := strings.Cut(content, "\n")
	if source, ok := strings.CutPrefix(firstLine, sourcePrefix); ok {
		return strings.TrimSpace(source)
	}
	return ""
}

// unifiedManifestStream orders docs lexicographically by Source path. Docs
// sharing a Source keep their relative order, with hooks placed before
// non-hook resources. Each document ends with a single newline.
func unifiedManifestStream(docs []manifestDoc) string {
	sorted := make([]manifestDoc, len(docs))
	copy(sorted, docs)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].source != sorted[j].source {
			return sorted[i].source < sorted[j].source
		}
		return sorted[i].hook && !sorted[j].hook
	})
	return writeDocs(sorted)
}

// withoutHooks removes the given hooks from a unified manifest stream, keeping
// the order of the remaining documents.
func withoutHooks(stream string, hooks []*release.Hook) string {
	if len(hooks) == 0 {
		return stream
	}
	omit := make(map[string]int, len(hooks))
	for _, h := range hooks {
		omit[strings.TrimSpace(hookDoc(h.Path, h.Manifest).content)]++
	}

	var kept []manifestDoc
	for _, d := range manifestDocs(stream) {
		if omit[d.content] > 0 {
			omit[d.content]--
			continue
		}
		kept = append(kept, d)
	}
	return writeDocs(kept)
}

func writeDocs(docs []manifestDoc) string {
	var b strings.Builder
	for _, d := range docs {
		b.WriteString("---\n")
		b.WriteString(strings.TrimSpace(d.content))
		b.WriteString("\n")
	}
	return b.String()
}
