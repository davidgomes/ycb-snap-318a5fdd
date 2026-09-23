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
	"strings"

	"helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"
)

// manifestStream returns the unified manifest stream of a release: every
// document, hooks included, ordered by Source path.
//
// Freshly rendered releases keep the rendered order within each template.
// Otherwise the stream is reconstructed from the stored manifest and hooks;
// as the rendered position of hooks is not recorded, hooks are placed before
// non-hook documents sharing the same Source.
func manifestStream(rel release.Releaser) (string, error) {
	if r, ok := rel.(*releasev1.Release); ok && r.ManifestStream != "" {
		return r.ManifestStream, nil
	}

	rac, err := release.NewAccessor(rel)
	if err != nil {
		return "", err
	}
	var docs []releaseutil.StreamDocument
	for _, h := range rac.Hooks() {
		hac, err := release.NewHookAccessor(h)
		if err != nil {
			return "", err
		}
		docs = append(docs, releaseutil.StreamDocument{Source: hac.Path(), Content: strings.TrimSpace(hac.Manifest())})
	}
	docs = append(docs, releaseutil.ParseManifestStream(rac.Manifest())...)
	return releaseutil.FormatManifestStream(docs), nil
}
