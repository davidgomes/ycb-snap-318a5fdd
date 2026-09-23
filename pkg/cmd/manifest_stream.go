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
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	release "helm.sh/helm/v4/pkg/release/v1"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"
)

const manifestSourcePrefix = "# Source: "

// manifestDocument is a single YAML document of a manifest stream together
// with the path of the template it was rendered from.
type manifestDocument struct {
	source  string
	content string
}

func hookDocument(path, manifest string) manifestDocument {
	return manifestDocument{source: path, content: strings.TrimSpace(manifest)}
}

// splitManifestStream splits a stream of YAML documents, taking the source of
// each document from its leading "# Source:" comment. A document without that
// comment belongs to the same source as the document before it.
func splitManifestStream(stream string) []manifestDocument {
	entries := releaseutil.SplitManifests(stream)
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Sort(releaseutil.BySplitManifestsOrder(keys))

	docs := make([]manifestDocument, 0, len(keys))
	var source string
	for _, key := range keys {
		content := entries[key]
		if header, ok := strings.CutPrefix(content, manifestSourcePrefix); ok {
			source, content, _ = strings.Cut(header, "\n")
		}
		docs = append(docs, manifestDocument{source: source, content: content})
	}
	return docs
}

// mergeManifestStream orders hooks and the documents of manifest into a single
// stream by source path. Where a hook shares its source with other resources
// its position within the template is unknown, so the hook is placed first.
func mergeManifestStream(manifest string, hooks []manifestDocument) []manifestDocument {
	docs := slices.Concat(hooks, splitManifestStream(manifest))
	slices.SortStableFunc(docs, func(a, b manifestDocument) int {
		return strings.Compare(a.source, b.source)
	})
	return docs
}

// releaseManifestStream returns the resources of rel and the hooks accepted by
// includeHook as a single stream ordered by source path. The rendered stream of
// a dry run is used when available since it keeps hooks in template order.
func releaseManifestStream(rel *release.Release, includeHook func(*release.Hook) bool) []manifestDocument {
	if rel.ManifestStream == "" {
		var hooks []manifestDocument
		for _, h := range rel.Hooks {
			if includeHook(h) {
				hooks = append(hooks, hookDocument(h.Path, h.Manifest))
			}
		}
		return mergeManifestStream(rel.Manifest, hooks)
	}

	excluded := make(map[manifestDocument]bool)
	for _, h := range rel.Hooks {
		if !includeHook(h) {
			excluded[hookDocument(h.Path, h.Manifest)] = true
		}
	}
	return slices.DeleteFunc(splitManifestStream(rel.ManifestStream), func(doc manifestDocument) bool {
		return excluded[doc]
	})
}

func allHooks(*release.Hook) bool { return true }

func writeManifestStream(out io.Writer, docs []manifestDocument) {
	for _, doc := range docs {
		if doc.source == "" {
			fmt.Fprintf(out, "---\n%s\n", doc.content)
		} else {
			fmt.Fprintf(out, "---\n%s%s\n%s\n", manifestSourcePrefix, doc.source, doc.content)
		}
	}
}
