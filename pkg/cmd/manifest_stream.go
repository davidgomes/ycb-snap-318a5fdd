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

type manifestStreamHook struct {
	Path     string
	Manifest string
}

func unifiedManifestStream(manifest string, hooks []manifestStreamHook) string {
	converted := make([]releaseutil.StreamHook, 0, len(hooks))
	for _, h := range hooks {
		converted = append(converted, releaseutil.StreamHook{Path: h.Path, Manifest: h.Manifest})
	}
	return releaseutil.UnifiedStream(manifest, converted)
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

// filterHooksFromStream removes documents that match the given hooks so that
// --skip-tests / --no-hooks can drop hook YAML already present in a stream.
func filterHooksFromStream(stream string, hooks []*releasev1.Hook) string {
	if stream == "" || len(hooks) == 0 {
		return stream
	}
	skip := make(map[string]struct{}, len(hooks))
	for _, h := range hooks {
		if h == nil {
			continue
		}
		skip[h.Path+"\n"+strings.TrimSpace(h.Manifest)] = struct{}{}
	}

	split := releaseutil.SplitManifests(stream)
	keys := make([]string, 0, len(split))
	for k := range split {
		keys = append(keys, k)
	}
	sort.Sort(releaseutil.BySplitManifestsOrder(keys))

	var b strings.Builder
	for _, k := range keys {
		source, body := releaseutil.SplitSourceComment(split[k])
		if _, drop := skip[source+"\n"+body]; drop {
			continue
		}
		b.WriteString("---\n")
		if source != "" {
			b.WriteString("# Source: ")
			b.WriteString(source)
			b.WriteByte('\n')
		}
		if body != "" {
			b.WriteString(body)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
