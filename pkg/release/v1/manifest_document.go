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

package v1

// ManifestDocument is one rendered YAML document in source order.
// Hook documents stay in this list so printers can emit a single stream.
type ManifestDocument struct {
	// Source is the chart-relative template path recorded in the Source comment.
	Source string
	// Content is the YAML body without the Source comment.
	Content string
	// Hook reports whether this document is a hook.
	Hook bool
	// Kind is the Kubernetes kind, when the document could be parsed.
	Kind string
	// APIVersion is the Kubernetes apiVersion, when the document could be parsed.
	APIVersion string
	// Test reports whether this hook runs on the test event.
	Test bool
	// Hidden reports whether a Secret's contents were suppressed.
	Hidden bool
	// OutsideReleaseDir keeps output-dir files at the output root. Chart CRDs
	// are written there even when the release name is added to template paths.
	OutsideReleaseDir bool
}
