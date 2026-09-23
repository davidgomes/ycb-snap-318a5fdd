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
	"sort"
	"strings"

	"helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"
)

// formatManifestDocuments writes a unified manifest stream. Documents are
// emitted in the order given. include, when non-nil, drops documents it rejects.
// The result ends with a trailing newline when it is non-empty.
func formatManifestDocuments(docs []releasev1.ManifestDocument, include func(releasev1.ManifestDocument) bool) string {
	var b strings.Builder
	for _, doc := range docs {
		if include != nil && !include(doc) {
			continue
		}
		writeManifestDocument(&b, doc.Source, doc.Body)
	}
	return b.String()
}

func writeManifestDocument(b *strings.Builder, source, body string) {
	body = strings.TrimSpace(body)
	b.WriteString("---\n")
	if source != "" && !strings.Contains(body, "# Source:") {
		fmt.Fprintf(b, "# Source: %s\n", source)
	}
	if body != "" {
		b.WriteString(body)
		b.WriteByte('\n')
	}
}

type manifestHook struct {
	path     string
	manifest string
}

// unifiedManifest merges a stored manifest and its hooks into one stream.
// Documents are ordered by full source path. When a hook and a non-hook share
// a path, the hook is emitted first. Order within each of those groups is kept.
func unifiedManifest(manifest string, hooks []manifestHook) string {
	type item struct {
		source string
		body   string
		hook   bool
		index  int
	}

	items := make([]item, 0)
	split := releaseutil.SplitManifests(manifest)
	keys := make([]string, 0, len(split))
	for key := range split {
		keys = append(keys, key)
	}
	sort.Sort(releaseutil.BySplitManifestsOrder(keys))
	for i, key := range keys {
		body := split[key]
		items = append(items, item{
			source: sourcePath(body),
			body:   body,
			index:  i,
		})
	}
	base := len(items)
	for i, hook := range hooks {
		body := strings.TrimSpace(hook.manifest)
		if hook.path != "" {
			body = fmt.Sprintf("# Source: %s\n%s", hook.path, body)
		}
		items = append(items, item{
			source: hook.path,
			body:   body,
			hook:   true,
			index:  base + i,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].source != items[j].source {
			return items[i].source < items[j].source
		}
		if items[i].hook != items[j].hook {
			return items[i].hook
		}
		return items[i].index < items[j].index
	})

	var b strings.Builder
	for _, item := range items {
		writeManifestDocument(&b, "", item.body)
	}
	return b.String()
}

func sourcePath(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if path, ok := strings.CutPrefix(line, "# Source:"); ok {
			return strings.TrimSpace(path)
		}
	}
	return ""
}

func hooksFromRelease(rel release.Accessor) ([]manifestHook, error) {
	hooks := make([]manifestHook, 0, len(rel.Hooks()))
	for _, hook := range rel.Hooks() {
		hac, err := release.NewHookAccessor(hook)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, manifestHook{path: hac.Path(), manifest: hac.Manifest()})
	}
	return hooks, nil
}
