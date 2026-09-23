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

	"sigs.k8s.io/yaml"

	release "helm.sh/helm/v4/pkg/release/v1"
)

const sourcePrefix = "# Source: "

// FormatManifestDocuments renders documents as one YAML stream. Documents are
// emitted in the order given. Each document ends with a single trailing newline.
func FormatManifestDocuments(docs []release.ManifestDocument) string {
	var b strings.Builder
	for _, doc := range docs {
		if doc.Hidden {
			fmt.Fprintf(&b, "---\n# Source: %s\n# HIDDEN: The Secret output has been suppressed\n", doc.Source)
			continue
		}
		content := strings.TrimSpace(doc.Content)
		if doc.Source == "" {
			if content == "" {
				continue
			}
			fmt.Fprintf(&b, "---\n%s\n", content)
			continue
		}
		fmt.Fprintf(&b, "---\n# Source: %s\n%s\n", doc.Source, content)
	}
	return b.String()
}

// ReleaseManifestStream builds the unified stream stored releases can print.
// Documents are ordered by full Source path. Within one path, hooks are emitted
// before non-hook resources, and each of those groups keeps its original order.
// A manifest that has no Source comments and no hooks is returned unchanged.
func ReleaseManifestStream(manifest string, hooks []*release.Hook) string {
	if len(hooks) == 0 && !strings.Contains(manifest, sourcePrefix) {
		return manifest
	}

	type item struct {
		doc release.ManifestDocument
		seq int
	}
	split := SplitManifests(manifest)
	items := make([]item, 0, len(split)+len(hooks))
	seq := 0
	for _, key := range splitManifestKeysFrom(split) {
		raw := strings.TrimSpace(split[key])
		source, body := splitSourceHeader(raw)
		if source == "" && strings.TrimSpace(body) == "" {
			continue
		}
		content := body
		if source == "" {
			content = raw
		}
		items = append(items, item{
			seq: seq,
			doc: release.ManifestDocument{
				Source:  source,
				Content: content,
			},
		})
		seq++
	}
	for _, h := range hooks {
		if h == nil {
			continue
		}
		items = append(items, item{
			seq: seq,
			doc: release.ManifestDocument{
				Source:  h.Path,
				Content: h.Manifest,
				Hook:    true,
			},
		})
		seq++
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].doc.Source != items[j].doc.Source {
			return items[i].doc.Source < items[j].doc.Source
		}
		if items[i].doc.Hook != items[j].doc.Hook {
			return items[i].doc.Hook
		}
		return items[i].seq < items[j].seq
	})

	docs := make([]release.ManifestDocument, len(items))
	for i := range items {
		docs[i] = items[i].doc
	}
	return FormatManifestDocuments(docs)
}

// KindSortedManifest reorders a rendered manifest into kind order while keeping
// each document's text. Install and upgrade use this so resource apply order
// stays the install order when the stored manifest is source-ordered.
func KindSortedManifest(manifest string, ordering KindSortOrder) (string, error) {
	if strings.TrimSpace(manifest) == "" {
		return manifest, nil
	}
	split := SplitManifests(manifest)
	if len(split) == 0 {
		return manifest, nil
	}

	manifests := make([]Manifest, 0, len(split))
	for _, key := range splitManifestKeysFrom(split) {
		raw := strings.TrimSpace(split[key])
		source, body := splitSourceHeader(raw)
		parseBody := body
		if source == "" {
			parseBody = raw
		}
		head := new(SimpleHead)
		if err := yaml.Unmarshal([]byte(parseBody), head); err != nil {
			return "", fmt.Errorf("YAML parse error: %w", err)
		}
		manifests = append(manifests, Manifest{
			Name:    source,
			Content: raw,
			Head:    head,
		})
	}

	sorted := sortManifestsByKind(manifests, ordering)
	var b strings.Builder
	for _, m := range sorted {
		fmt.Fprintf(&b, "---\n%s\n", strings.TrimSpace(m.Content))
	}
	return b.String(), nil
}

func splitManifestKeysFrom(split map[string]string) []string {
	keys := make([]string, 0, len(split))
	for key := range split {
		keys = append(keys, key)
	}
	sort.Sort(BySplitManifestsOrder(keys))
	return keys
}

func splitSourceHeader(doc string) (source, body string) {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return "", ""
	}
	line, rest, _ := strings.Cut(doc, "\n")
	first := strings.TrimSpace(line)
	if strings.HasPrefix(first, sourcePrefix) {
		return strings.TrimSpace(strings.TrimPrefix(first, sourcePrefix)), strings.TrimSpace(rest)
	}
	return "", doc
}
