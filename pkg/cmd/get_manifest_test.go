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
	tests := []cmdTestCase{{
		name:   "get manifest with release",
		cmd:    "get manifest juno",
		golden: "output/get-manifest.txt",
		rels:   []*release.Release{release.Mock(&release.MockReleaseOptions{Name: "juno"})},
	}, {
		name:      "get manifest without args",
		cmd:       "get manifest",
		golden:    "output/get-manifest-no-args.txt",
		wantError: true,
	}}
	runTestCmd(t, tests)
}

func TestGetManifestHooksBeforeResourcesSharingSource(t *testing.T) {
	defer resetEnv()()
	rel := release.Mock(&release.MockReleaseOptions{Name: "shared-source"})
	rel.Manifest = `---
# Source: chart/templates/b.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-b
---
# Source: chart/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-a
`
	rel.Hooks = []*release.Hook{{
		Name:     "hook-b",
		Kind:     "Job",
		Path:     "chart/templates/b.yaml",
		Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: hook-b\n",
		Events:   []release.HookEvent{release.HookPreInstall},
	}}

	storage := storageFixture()
	if err := storage.Create(rel); err != nil {
		t.Fatal(err)
	}
	_, out, err := executeActionCommandC(storage, "get manifest shared-source")
	if err != nil {
		t.Fatal(err)
	}
	want := `---
# Source: chart/templates/a.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-a
---
# Source: chart/templates/b.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: hook-b
---
# Source: chart/templates/b.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: from-b
`
	if out != want {
		t.Fatalf("manifest stream mismatch\nWANT:\n%s\nGOT:\n%s", want, out)
	}
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
