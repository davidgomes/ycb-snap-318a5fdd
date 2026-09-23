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
	"testing"

	release "helm.sh/helm/v4/pkg/release/v1"
)

func TestGetManifest(t *testing.T) {
	withHooks := release.Mock(&release.MockReleaseOptions{Name: "vesta"})
	withHooks.Manifest = `---
# Source: foo/templates/b.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: b
---
# Source: foo/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: a
`
	withHooks.Hooks = []*release.Hook{
		{
			Name:     "z-hook",
			Kind:     "Job",
			Path:     "foo/templates/z.yaml",
			Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: z-hook\n  annotations:\n    \"helm.sh/hook\": post-install\n",
			Events:   []release.HookEvent{release.HookPostInstall},
		},
		{
			Name:     "b-hook",
			Kind:     "Job",
			Path:     "foo/templates/b.yaml",
			Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: b-hook\n  annotations:\n    \"helm.sh/hook\": pre-install\n",
			Events:   []release.HookEvent{release.HookPreInstall},
		},
	}

	tests := []cmdTestCase{{
		name:   "get manifest with release",
		cmd:    "get manifest juno",
		golden: "output/get-manifest.txt",
		rels:   []*release.Release{release.Mock(&release.MockReleaseOptions{Name: "juno"})},
	}, {
		name:   "get manifest with hooks sharing a source path",
		cmd:    "get manifest vesta",
		golden: "output/get-manifest-with-hooks.txt",
		rels:   []*release.Release{withHooks},
	}, {
		name:      "get manifest without args",
		cmd:       "get manifest",
		golden:    "output/get-manifest-no-args.txt",
		wantError: true,
	}}
	runTestCmd(t, tests)
}

func TestGetManifestCompletion(t *testing.T) {
	checkReleaseCompletion(t, "get manifest", false)
}

func TestGetManifestRevisionCompletion(t *testing.T) {
	revisionFlagCompletionTest(t, "get manifest")
}

func TestGetManifestFileCompletion(t *testing.T) {
	checkFileCompletion(t, "get manifest", false)
	checkFileCompletion(t, "get manifest myrelease", false)
}
