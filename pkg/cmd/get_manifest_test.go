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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestGetManifestUnifiedStream(t *testing.T) {
	rel := release.Mock(&release.MockReleaseOptions{Name: "ordered"})
	rel.Manifest = strings.Join([]string{
		"---",
		"# Source: demo/templates/b.yaml",
		"apiVersion: v1",
		"kind: ConfigMap",
		"metadata:",
		"  name: b",
		"---",
		"# Source: demo/templates/a.yaml",
		"apiVersion: v1",
		"kind: Service",
		"metadata:",
		"  name: svc",
		"---",
		"# Source: demo/templates/a.yaml",
		"apiVersion: v1",
		"kind: ConfigMap",
		"metadata:",
		"  name: a",
	}, "\n")
	rel.Hooks = []*release.Hook{{
		Name:     "hook-a",
		Kind:     "Pod",
		Path:     "demo/templates/a.yaml",
		Manifest: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: hook-a\n",
		Events:   []release.HookEvent{release.HookPreInstall},
	}, {
		Name:     "hook-b",
		Kind:     "Job",
		Path:     "demo/templates/b.yaml",
		Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: hook-b\n",
		Events:   []release.HookEvent{release.HookPreInstall},
	}}

	out := ""
	var err error
	storage := storageFixture()
	require.NoError(t, storage.Create(rel))
	_, out, err = executeActionCommandC(storage, "get manifest ordered")
	require.NoError(t, err)

	hookA := strings.Index(out, "name: hook-a")
	svc := strings.Index(out, "name: svc")
	cmA := strings.Index(out, "name: a")
	hookB := strings.Index(out, "name: hook-b")
	cmB := strings.Index(out, "name: b")
	require.NotEqual(t, -1, hookA)
	assert.Less(t, hookA, svc)
	assert.Less(t, svc, cmA)
	assert.Less(t, cmA, hookB)
	assert.Less(t, hookB, cmB)
	assert.True(t, strings.HasSuffix(out, "\n"))
	assert.NotContains(t, out, "\n\n\n")
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
