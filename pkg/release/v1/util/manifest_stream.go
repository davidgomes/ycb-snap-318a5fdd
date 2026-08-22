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
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	release "helm.sh/helm/v4/pkg/release/v1"
)

const hiddenSecretContent = "# HIDDEN: The Secret output has been suppressed"

var manifestStreamDocSeparator = regexp.MustCompile(`(?:^|\n)---\n# Source: `)

// ManifestStreamEntry represents one document in a unified manifest stream.
type ManifestStreamEntry struct {
	Source  string
	Content string
	IsHook  bool
	Order   int
}

// FormatManifestStreamOptions configures unified manifest stream formatting.
type FormatManifestStreamOptions struct {
	TrailingNewline bool
}

// FormatManifestStream renders manifest stream entries into Helm's standard
// multi-document YAML output format.
func FormatManifestStream(entries []ManifestStreamEntry, opts FormatManifestStreamOptions) string {
	if len(entries) == 0 {
		if opts.TrailingNewline {
			return "\n"
		}
		return ""
	}

	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "---\n# Source: %s\n", e.Source)
		content := strings.TrimRight(e.Content, "\n")
		if content != "" {
			fmt.Fprintln(&b, content)
		}
	}

	result := strings.TrimRight(b.String(), "\n")
	if opts.TrailingNewline {
		result += "\n"
	}
	return result
}

// SortStreamBySourcePath orders stream entries by source path and preserves
// render order within each source path.
func SortStreamBySourcePath(entries []ManifestStreamEntry) []ManifestStreamEntry {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Source != entries[j].Source {
			return entries[i].Source < entries[j].Source
		}
		return entries[i].Order < entries[j].Order
	})
	return entries
}

// SortStreamEntriesForGetManifest orders stream entries by source path and
// places hook documents before non-hook documents from the same source path.
func SortStreamEntriesForGetManifest(entries []ManifestStreamEntry) []ManifestStreamEntry {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Source != entries[j].Source {
			return entries[i].Source < entries[j].Source
		}
		if entries[i].IsHook != entries[j].IsHook {
			return entries[i].IsHook
		}
		return entries[i].Order < entries[j].Order
	})
	return entries
}

// ParseManifestStream splits a stored manifest stream into individual entries.
func ParseManifestStream(manifest string) []ManifestStreamEntry {
	manifest = strings.TrimSpace(manifest)
	if manifest == "" {
		return nil
	}

	parts := manifestStreamDocSeparator.Split(manifest, -1)
	var entries []ManifestStreamEntry
	orderBySource := make(map[string]int)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		lines := strings.SplitN(part, "\n", 2)
		if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
			continue
		}

		source := lines[0]
		content := ""
		if len(lines) > 1 {
			content = strings.TrimRight(lines[1], "\n")
		}

		order := orderBySource[source]
		orderBySource[source] = order + 1

		entries = append(entries, ManifestStreamEntry{
			Source:  source,
			Content: content,
			IsHook:  isHookManifest(content),
			Order:   order,
		})
	}

	return entries
}

// BuildGetManifestStream assembles the manifest stream for helm get manifest.
func BuildGetManifestStream(manifest string, hooks []*release.Hook) string {
	entries := ParseManifestStream(manifest)
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		seen[manifestStreamEntryKey(e)] = struct{}{}
	}

	for _, h := range hooks {
		entry := ManifestStreamEntry{
			Source:  h.Path,
			Content: h.Manifest,
			IsHook:  true,
		}
		key := manifestStreamEntryKey(entry)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, entry)
	}

	entries = SortStreamEntriesForGetManifest(entries)
	return FormatManifestStream(entries, FormatManifestStreamOptions{TrailingNewline: true})
}

// FilterManifestStreamEntries removes hook entries based on CLI options.
func FilterManifestStreamEntries(entries []ManifestStreamEntry, disableHooks, skipTests bool) []ManifestStreamEntry {
	if !disableHooks && !skipTests {
		return entries
	}

	filtered := make([]ManifestStreamEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsHook {
			filtered = append(filtered, e)
			continue
		}
		if disableHooks {
			continue
		}
		if skipTests && isTestHookManifest(e.Content) {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

func manifestStreamEntryKey(e ManifestStreamEntry) string {
	return e.Source + "\x00" + e.Content
}

// IsSecretV1Manifest reports whether content represents a v1 Secret manifest.
func IsSecretV1Manifest(content string) bool {
	if content == "" || content == hiddenSecretContent {
		return false
	}

	var entry SimpleHead
	if err := yaml.Unmarshal([]byte(content), &entry); err != nil {
		return false
	}
	return entry.Kind == "Secret" && entry.Version == "v1"
}

func isHookManifest(content string) bool {
	if content == "" || content == hiddenSecretContent {
		return false
	}

	var entry SimpleHead
	if err := yaml.Unmarshal([]byte(content), &entry); err != nil {
		return false
	}
	if entry.Metadata == nil || entry.Metadata.Annotations == nil {
		return false
	}
	_, ok := entry.Metadata.Annotations[release.HookAnnotation]
	return ok
}

func isTestHookManifest(content string) bool {
	var entry SimpleHead
	if err := yaml.Unmarshal([]byte(content), &entry); err != nil {
		return false
	}
	if entry.Metadata == nil || entry.Metadata.Annotations == nil {
		return false
	}
	hookTypes, ok := entry.Metadata.Annotations[release.HookAnnotation]
	if !ok {
		return false
	}
	for hookType := range strings.SplitSeq(hookTypes, ",") {
		if strings.EqualFold(strings.TrimSpace(hookType), release.HookTest.String()) {
			return true
		}
	}
	return false
}
