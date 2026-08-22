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

	"helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"
)

// manifestStreamHook is a hook document to include in the unified stream.
type manifestStreamHook struct {
	Path     string
	Manifest string
}

type manifestStreamDoc struct {
	source  string
	content string
	isHook  bool
	index   int
}

const sourceCommentPrefix = "# Source:"

// unifiedManifestStream builds a single, stable YAML stream from a release
// manifest and its hooks. Documents are ordered by full Source path
// (lexicographic). Hooks from the same Source are emitted before non-hook
// resources. Within those groups, original top-to-bottom order is kept.
//
// The returned string is empty when there are no documents. Otherwise it
// ends with exactly one trailing newline and does not add extra blank lines.
func unifiedManifestStream(manifest string, hooks []manifestStreamHook) string {
	var docs []manifestStreamDoc
	index := 0

	for _, raw := range splitManifestDocuments(manifest) {
		source, body := splitSourceComment(raw)
		if body == "" && source == "" {
			continue
		}
		docs = append(docs, manifestStreamDoc{
			source:  source,
			content: body,
			index:   index,
		})
		index++
	}

	for _, h := range hooks {
		for _, raw := range splitManifestDocuments(h.Manifest) {
			_, body := splitSourceComment(raw)
			if body == "" && h.Path == "" {
				continue
			}
			docs = append(docs, manifestStreamDoc{
				source:  h.Path,
				content: body,
				isHook:  true,
				index:   index,
			})
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

func unifiedManifestStreamFromV1(rel *releasev1.Release, hooks []*releasev1.Hook) string {
	if rel == nil {
		return unifiedManifestStream("", hooksFromV1(hooks))
	}
	return unifiedManifestStream(rel.Manifest, hooksFromV1(hooks))
}

func unifiedManifestStreamFromAccessor(rac release.Accessor, includeHooks bool) (string, error) {
	var hooks []manifestStreamHook
	if includeHooks {
		for _, hook := range rac.Hooks() {
			hac, err := release.NewHookAccessor(hook)
			if err != nil {
				return "", err
			}
			hooks = append(hooks, manifestStreamHook{
				Path:     hac.Path(),
				Manifest: hac.Manifest(),
			})
		}
	}
	return unifiedManifestStream(rac.Manifest(), hooks), nil
}

func hooksFromV1(hooks []*releasev1.Hook) []manifestStreamHook {
	out := make([]manifestStreamHook, 0, len(hooks))
	for _, h := range hooks {
		if h == nil {
			continue
		}
		out = append(out, manifestStreamHook{Path: h.Path, Manifest: h.Manifest})
	}
	return out
}

func splitManifestDocuments(manifest string) []string {
	if strings.TrimSpace(manifest) == "" {
		return nil
	}
	split := releaseutil.SplitManifests(manifest)
	keys := make([]string, 0, len(split))
	for k := range split {
		keys = append(keys, k)
	}
	sort.Sort(releaseutil.BySplitManifestsOrder(keys))
	docs := make([]string, 0, len(keys))
	for _, k := range keys {
		docs = append(docs, split[k])
	}
	return docs
}

func splitSourceComment(content string) (string, string) {
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
