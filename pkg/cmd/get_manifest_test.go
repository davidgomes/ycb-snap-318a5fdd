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
# Source: vesta/templates/z-namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: vesta-extra
---
# Source: vesta/templates/app.yaml
apiVersion: v1
kind: Service
metadata:
  name: vesta-app
---
# Source: vesta/templates/app.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vesta-app
`
	withHooks.Hooks = []*release.Hook{
		{
			Name:     "vesta-test",
			Kind:     "Pod",
			Path:     "vesta/templates/tests/test-app.yaml",
			Manifest: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: vesta-test\n  annotations:\n    \"helm.sh/hook\": test",
			Events:   []release.HookEvent{release.HookTest},
		},
		{
			Name:     "vesta-setup",
			Kind:     "ConfigMap",
			Path:     "vesta/templates/app.yaml",
			Manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: vesta-setup\n  annotations:\n    \"helm.sh/hook\": pre-install",
			Events:   []release.HookEvent{release.HookPreInstall},
		},
	}

	tests := []cmdTestCase{{
		name:   "get manifest with release",
		cmd:    "get manifest juno",
		golden: "output/get-manifest.txt",
		rels:   []*release.Release{release.Mock(&release.MockReleaseOptions{Name: "juno"})},
	}, {
		name:   "get manifest orders hooks and resources by source, hooks first",
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
