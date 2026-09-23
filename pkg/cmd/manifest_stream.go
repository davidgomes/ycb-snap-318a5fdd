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
	"regexp"
	"sort"
	"strings"
)

type manifestDoc struct {
	path    string
	content string
	hook    bool
}

var (
	manifestSepRegex    = regexp.MustCompile(`(?m)^---[ \t]*$`)
	manifestSourceRegex = regexp.MustCompile(`(?m)^# Source: (.+)$`)
)

// splitManifestDocs splits a rendered manifest into documents, preserving order.
func splitManifestDocs(manifest string) []manifestDoc {
	var docs []manifestDoc
	for _, part := range manifestSepRegex.Split(manifest, -1) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		doc := manifestDoc{content: part}
		if m := manifestSourceRegex.FindStringSubmatch(part); m != nil {
			doc.path = strings.TrimSpace(m[1])
		}
		docs = append(docs, doc)
	}
	return docs
}

// hookManifestDoc builds a document for a hook, prefixing its Source comment.
func hookManifestDoc(path, manifest string) manifestDoc {
	return manifestDoc{
		path:    path,
		content: "# Source: " + path + "\n" + strings.TrimSpace(manifest),
		hook:    true,
	}
}

// unifiedManifestStream orders documents by Source path. Documents sharing a
// path keep their rendered order, with hooks placed before non-hook resources.
// The result ends with exactly one trailing newline, or is empty.
func unifiedManifestStream(docs []manifestDoc) string {
	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].path != docs[j].path {
			return docs[i].path < docs[j].path
		}
		return docs[i].hook && !docs[j].hook
	})
	var b strings.Builder
	for _, d := range docs {
		b.WriteString("---\n")
		b.WriteString(d.content)
		b.WriteString("\n")
	}
	return b.String()
}
