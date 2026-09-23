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

import (
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/release/common"
)

type ApplyMethod string

const ApplyMethodClientSideApply ApplyMethod = "csa"
const ApplyMethodServerSideApply ApplyMethod = "ssa"

// Release describes a deployment of a chart, together with the chart
// and the variables used to deploy that chart.
type Release struct {
	// Name is the name of the release
	Name string `json:"name,omitempty"`
	// Info provides information about a release
	Info *Info `json:"info,omitempty"`
	// Chart is the chart that was released.
	Chart *chart.Chart `json:"chart,omitempty"`
	// Config is the set of extra Values added to the chart.
	// These values override the default values inside of the chart.
	Config map[string]any `json:"config,omitempty"`
	// Manifest is the string representation of the rendered template.
	Manifest string `json:"manifest,omitempty"`
	// Hooks are all of the hooks declared for this release.
	Hooks []*Hook `json:"hooks,omitempty"`
	// Version is an int which represents the revision of the release.
	Version int `json:"version,omitempty"`
	// Namespace is the kubernetes namespace of the release.
	Namespace string `json:"namespace,omitempty"`
	// Labels of the release.
	// Disabled encoding into Json cause labels are stored in storage driver metadata field.
	Labels map[string]string `json:"-"`
	// ApplyMethod stores whether server-side or client-side apply was used for the release
	// Unset (empty string) should be treated as the default of client-side apply
	ApplyMethod string `json:"apply_method,omitempty"` // "ssa" | "csa"

	// ManifestDocuments is a display-only, source-ordered rendering of the
	// chart, including hooks. It is not stored with the release. The manifest
	// used for install and upgrade remains in install order.
	ManifestDocuments []ManifestDocument `json:"-"`
}

// ManifestDocument is one rendered YAML document in source order.
type ManifestDocument struct {
	// Source is the chart-relative path of the template that produced this document.
	Source string
	// Body is the YAML document without a leading document separator.
	Body string
	// Hook reports whether this document is a hook.
	Hook bool
	// Test reports whether this hook runs on the test event.
	Test bool
}

// SetStatus is a helper for setting the status on a release.
func (r *Release) SetStatus(status common.Status, msg string) {
	r.Info.Status = status
	r.Info.Description = msg
}
