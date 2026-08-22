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
	"path"
	"sort"
	"strings"
)

const sourceCommentPrefix = "# Source:"

// StreamHook is a hook document to merge into a unified manifest stream.
type StreamHook struct {
	Path     string
	Manifest string
}

type streamDoc struct {
	source  string
	content string
	isHook  bool
	index   int
}

// UnifiedStreamFromFiles builds a YAML stream from rendered template files.
// Documents are ordered by full Source path (lexicographic). Within a file,
// documents stay in top-to-bottom rendered order, including hooks.
//
// Partials (names starting with '_') and empty files are skipped. The result
// is empty when there are no documents; otherwise it ends with one newline.
func UnifiedStreamFromFiles(files map[string]string) string {
	var docs []streamDoc
	index := 0

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		if strings.HasPrefix(path.Base(p), "_") {
			continue
		}
		for _, raw := range splitManifestDocuments(files[p]) {
			_, body := SplitSourceComment(raw)
			if body == "" {
				continue
			}
			docs = append(docs, streamDoc{source: p, content: body, index: index})
			index++
		}
	}

	return formatStreamDocs(docs)
}

// UnifiedStream builds a YAML stream from a stored release manifest and hooks.
// Documents are ordered by full Source path. When a hook and a non-hook share
// a Source path, hooks are emitted first. Within those groups, original order
// is kept.
func UnifiedStream(manifest string, hooks []StreamHook) string {
	var docs []streamDoc
	index := 0

	for _, raw := range splitManifestDocuments(manifest) {
		source, body := SplitSourceComment(raw)
		if body == "" && source == "" {
			continue
		}
		docs = append(docs, streamDoc{source: source, content: body, index: index})
		index++
	}

	for _, h := range hooks {
		for _, raw := range splitManifestDocuments(h.Manifest) {
			_, body := SplitSourceComment(raw)
			if body == "" && h.Path == "" {
				continue
			}
			docs = append(docs, streamDoc{source: h.Path, content: body, isHook: true, index: index})
			index++
		}
	}

	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].source != docs[j].source {
			return docs[i].source < docs[j].source
		}
		if docs[i].isHook != docs[j].isHook {
			return docs[i].isHook
		}
		return docs[i].index < docs[j].index
	})

	return formatStreamDocs(docs)
}

// SplitSourceComment extracts a leading "# Source: path" comment from a document.
func SplitSourceComment(content string) (string, string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", ""
	}
	if !strings.HasPrefix(content, sourceCommentPrefix) {
		return "", content
	}
	rest := strings.TrimPrefix(content, sourceCommentPrefix)
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return strings.TrimSpace(rest), ""
	}
	return strings.TrimSpace(rest[:nl]), strings.TrimSpace(rest[nl+1:])
}

func splitManifestDocuments(manifest string) []string {
	if strings.TrimSpace(manifest) == "" {
		return nil
	}
	split := SplitManifests(manifest)
	keys := make([]string, 0, len(split))
	for k := range split {
		keys = append(keys, k)
	}
	sort.Sort(BySplitManifestsOrder(keys))
	docs := make([]string, 0, len(keys))
	for _, k := range keys {
		docs = append(docs, split[k])
	}
	return docs
}

func formatStreamDocs(docs []streamDoc) string {
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, d := range docs {
		b.WriteString("---\n")
		if d.source != "" {
			b.WriteString(sourceCommentPrefix)
			b.WriteByte(' ')
			b.WriteString(d.source)
			b.WriteByte('\n')
		}
		if d.content != "" {
			b.WriteString(d.content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
