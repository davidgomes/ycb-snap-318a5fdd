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

// SourcedHook is a rendered hook used to rebuild a manifest stream from a
// stored release. Path is the template Source path.
type SourcedHook struct {
	Path     string
	Manifest string
}

// ManifestStream renders files into one manifest stream.
//
// Documents are ordered by full source path, sorted lexicographically. Within a
// file, multi-document YAML keeps the rendered top-to-bottom order, including
// hooks. Partials, empty files, and unknown hooks are omitted. v1 Secrets are
// replaced with a hidden marker when hideSecret is set; hook secrets are left
// intact so hook output can still be matched back to the hook manifest.
func ManifestStream(files map[string]string, hideSecret bool) string {
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	var b strings.Builder
	for _, filePath := range paths {
		content := files[filePath]
		if strings.HasPrefix(path.Base(filePath), "_") || strings.TrimSpace(content) == "" {
			continue
		}
		manifestFile := &manifestFile{
			entries: SplitManifests(content),
			path:    filePath,
		}
		manifestFile.writeStream(&b, hideSecret)
	}
	return b.String()
}

// StoredManifestStream builds a manifest stream from a stored release manifest
// and its hooks.
//
// Documents are ordered by the Source path recorded on each document. When a
// hook and a non-hook resource share a source path, the hook is emitted first.
// Relative order among hooks, and among non-hooks, is preserved.
func StoredManifestStream(manifest string, hooks []SourcedHook) string {
	type item struct {
		source string
		body   string
		hook   bool
		seq    int
	}
	var items []item
	if strings.TrimSpace(manifest) != "" {
		split := SplitManifests(manifest)
		keys := make([]string, 0, len(split))
		for key := range split {
			keys = append(keys, key)
		}
		sort.Sort(BySplitManifestsOrder(keys))
		for _, key := range keys {
			source, body := splitSourceComment(split[key])
			if body == "" && source == "" {
				continue
			}
			items = append(items, item{source: source, body: body, seq: len(items)})
		}
	}
	hookSeq := 0
	for _, hook := range hooks {
		body := strings.TrimSpace(hook.Manifest)
		if body == "" && hook.Path == "" {
			continue
		}
		items = append(items, item{source: hook.Path, body: body, hook: true, seq: hookSeq})
		hookSeq++
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].source != items[j].source {
			return items[i].source < items[j].source
		}
		if items[i].hook != items[j].hook {
			return items[i].hook
		}
		return items[i].seq < items[j].seq
	})

	var b strings.Builder
	for _, item := range items {
		writeManifestDocument(&b, item.source, item.body)
	}
	return b.String()
}

func (file *manifestFile) writeStream(b *strings.Builder, hideSecret bool) {
	keys := make([]string, 0, len(file.entries))
	for entryKey := range file.entries {
		keys = append(keys, entryKey)
	}
	sort.Sort(BySplitManifestsOrder(keys))

	for _, entryKey := range keys {
		content := file.entries[entryKey]
		var entry SimpleHead
		if err := yaml.Unmarshal([]byte(content), &entry); err != nil {
			// CRDs are not run through SortManifests. Keep unparseable documents
			// so they are still present in the stream.
			writeManifestDocument(b, file.path, strings.TrimSpace(content))
			continue
		}
		if isUnknownHook(entry) {
			continue
		}
		if hideSecret && !isHook(entry) && entry.Kind == "Secret" && entry.Version == "v1" {
			fmt.Fprintf(b, "---\n# Source: %s\n# HIDDEN: The Secret output has been suppressed\n", file.path)
			continue
		}
		writeManifestDocument(b, file.path, content)
	}
}

func writeManifestDocument(b *strings.Builder, source, content string) {
	content = strings.TrimSpace(content)
	if source == "" {
		if content == "" {
			return
		}
		fmt.Fprintf(b, "---\n%s\n", content)
		return
	}
	if content == "" {
		fmt.Fprintf(b, "---\n# Source: %s\n", source)
		return
	}
	fmt.Fprintf(b, "---\n# Source: %s\n%s\n", source, content)
}

func splitSourceComment(doc string) (source, body string) {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return "", ""
	}
	line, rest, _ := strings.Cut(doc, "\n")
	const prefix = "# Source: "
	if strings.HasPrefix(line, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(line, prefix)), strings.TrimSpace(rest)
	}
	return "", doc
}

func isHook(entry SimpleHead) bool {
	if !hasAnyAnnotation(entry) {
		return false
	}
	_, ok := entry.Metadata.Annotations[release.HookAnnotation]
	return ok
}

// isUnknownHook reports whether entry declares a helm hook type that Helm does
// not recognize. Those documents are omitted from both install and output.
func isUnknownHook(entry SimpleHead) bool {
	if !isHook(entry) {
		return false
	}
	hookTypes := entry.Metadata.Annotations[release.HookAnnotation]
	unknown := false
	recognized := false
	for hookType := range strings.SplitSeq(hookTypes, ",") {
		hookType = strings.ToLower(strings.TrimSpace(hookType))
		if _, ok := events[hookType]; ok {
			recognized = true
			continue
		}
		unknown = true
	}
	return unknown || !recognized
}
