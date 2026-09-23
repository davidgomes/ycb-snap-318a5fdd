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
	"path"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	release "helm.sh/helm/v4/pkg/release/v1"
)

// StreamDocument is one rendered YAML document in a manifest stream.
type StreamDocument struct {
	// Source is the chart-relative template path recorded as "# Source:".
	Source string
	// Body is the document content without the source comment.
	Body string
	// Hook is true when the document is a hook resource.
	Hook bool
	// Test is true when the document is a test hook.
	Test bool
}

// StreamFormatOptions controls how a manifest stream is written.
type StreamFormatOptions struct {
	// SkipHooks omits hook documents.
	SkipHooks bool
	// SkipTests omits test hooks.
	SkipTests bool
	// HideSecrets replaces v1 Secret bodies with a suppression comment.
	HideSecrets bool
}

// DocumentsInSourceOrder splits rendered template files into documents ordered by
// full source path, then by top-to-bottom position within each file. Hooks stay
// in that rendered order. Partials, empty files, and unknown hooks are omitted.
func DocumentsInSourceOrder(files map[string]string) ([]StreamDocument, error) {
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	var docs []StreamDocument
	for _, filePath := range paths {
		content := files[filePath]
		if strings.HasPrefix(path.Base(filePath), "_") || strings.TrimSpace(content) == "" {
			continue
		}

		entries := SplitManifests(content)
		keys := make([]string, 0, len(entries))
		for entryKey := range entries {
			keys = append(keys, entryKey)
		}
		sort.Sort(BySplitManifestsOrder(keys))

		for _, entryKey := range keys {
			body := entries[entryKey]
			var entry SimpleHead
			if err := yaml.Unmarshal([]byte(body), &entry); err != nil {
				return nil, fmt.Errorf("YAML parse error on %s: %w", filePath, err)
			}

			doc := StreamDocument{Source: filePath, Body: body}
			hookEvents, unknown, isHook := classifyHook(entry)
			if unknown {
				continue
			}
			if isHook {
				doc.Hook = true
				for _, event := range hookEvents {
					if event == release.HookTest {
						doc.Test = true
						break
					}
				}
			}
			docs = append(docs, doc)
		}
	}
	return docs, nil
}

// FormatManifestStream writes documents as a YAML stream. Each sourced document
// is prefixed with a document separator and a "# Source:" comment. The result
// ends with a single trailing newline when it is non-empty.
func FormatManifestStream(docs []StreamDocument, opts StreamFormatOptions) string {
	var b strings.Builder
	for _, doc := range docs {
		if (opts.SkipHooks && doc.Hook) || (opts.SkipTests && doc.Test) {
			continue
		}
		body := strings.TrimSpace(doc.Body)
		if opts.HideSecrets && isV1Secret(body) {
			body = "# HIDDEN: The Secret output has been suppressed"
		}
		if doc.Source != "" {
			fmt.Fprintf(&b, "---\n# Source: %s\n%s\n", doc.Source, body)
			continue
		}
		if body == "" {
			continue
		}
		fmt.Fprintf(&b, "---\n%s\n", body)
	}
	return b.String()
}

// UnifiedReleaseManifest merges a stored release manifest with its hooks into
// one stream ordered by full source path. Documents that share a source path
// keep their original relative order, with hooks placed before non-hook
// resources.
func UnifiedReleaseManifest(manifest string, hooks []StreamDocument) string {
	type ranked struct {
		doc  StreamDocument
		hook bool
		ord  int
	}

	items := make([]ranked, 0, len(hooks)+4)
	for i, hook := range hooks {
		hook.Hook = true
		items = append(items, ranked{doc: hook, hook: true, ord: i})
	}
	offset := len(items)
	for i, doc := range parseStoredManifest(manifest) {
		items = append(items, ranked{doc: doc, hook: false, ord: offset + i})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].doc.Source != items[j].doc.Source {
			return items[i].doc.Source < items[j].doc.Source
		}
		if items[i].hook != items[j].hook {
			return items[i].hook
		}
		return items[i].ord < items[j].ord
	})

	docs := make([]StreamDocument, len(items))
	for i, item := range items {
		docs[i] = item.doc
	}
	return FormatManifestStream(docs, StreamFormatOptions{})
}

// InstallOrderManifest reorders a rendered manifest into install kind order.
// Source comments and the relative order of equal kinds are preserved.
// An empty manifest is returned unchanged.
func InstallOrderManifest(manifest string) (string, error) {
	if strings.TrimSpace(manifest) == "" {
		return manifest, nil
	}
	docs := parseStoredManifest(manifest)
	manifests := make([]Manifest, 0, len(docs))
	for _, doc := range docs {
		var head SimpleHead
		if err := yaml.Unmarshal([]byte(doc.Body), &head); err != nil {
			return "", fmt.Errorf("YAML parse error on %s: %w", doc.Source, err)
		}
		manifests = append(manifests, Manifest{
			Name:    doc.Source,
			Content: strings.TrimSpace(doc.Body),
			Head:    &head,
		})
	}
	manifests = sortManifestsByKind(manifests, InstallOrder)
	ordered := make([]StreamDocument, len(manifests))
	for i, m := range manifests {
		ordered[i] = StreamDocument{Source: m.Name, Body: m.Content}
	}
	return FormatManifestStream(ordered, StreamFormatOptions{}), nil
}

func parseStoredManifest(manifest string) []StreamDocument {
	if strings.TrimSpace(manifest) == "" {
		return nil
	}
	entries := SplitManifests(manifest)
	keys := make([]string, 0, len(entries))
	for entryKey := range entries {
		keys = append(keys, entryKey)
	}
	sort.Sort(BySplitManifestsOrder(keys))

	docs := make([]StreamDocument, 0, len(keys))
	for _, entryKey := range keys {
		source, body := splitSourceComment(entries[entryKey])
		docs = append(docs, StreamDocument{Source: source, Body: body})
	}
	return docs
}

func splitSourceComment(doc string) (string, string) {
	doc = strings.TrimSpace(doc)
	line, rest, found := strings.Cut(doc, "\n")
	const prefix = "# Source:"
	if strings.HasPrefix(line, prefix) {
		if !found {
			rest = ""
		}
		return strings.TrimSpace(strings.TrimPrefix(line, prefix)), rest
	}
	return "", doc
}

func classifyHook(entry SimpleHead) ([]release.HookEvent, bool, bool) {
	if !hasAnyAnnotation(entry) {
		return nil, false, false
	}
	hookTypes, ok := entry.Metadata.Annotations[release.HookAnnotation]
	if !ok {
		return nil, false, false
	}

	var found []release.HookEvent
	for hookType := range strings.SplitSeq(hookTypes, ",") {
		hookType = strings.ToLower(strings.TrimSpace(hookType))
		event, known := events[hookType]
		if !known {
			return nil, true, true
		}
		found = append(found, event)
	}
	return found, false, true
}

func isV1Secret(body string) bool {
	if strings.TrimSpace(body) == "" {
		return false
	}
	var head SimpleHead
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		return false
	}
	return head.Kind == "Secret" && head.Version == "v1"
}
