// Copyright The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package reloadstatus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTracker(t *testing.T) {
	dir := t.TempDir()
	tr := &Tracker{}
	tr.Init(dir)
	require.Equal(t, initial(), tr.Get())
	_, err := os.Stat(filepath.Join(dir, FileName))
	require.True(t, os.IsNotExist(err))

	st := Status{LastReloadID: "2026-01-01T00:00:00Z", ErrorCategory: CategoryApply, FailedReloader: "x"}
	require.NoError(t, tr.Record(st))

	tr2 := &Tracker{}
	tr2.Init(dir)
	require.Equal(t, normalize(st), tr2.Get())

	require.NoError(t, os.WriteFile(filepath.Join(dir, FileName), []byte("{bad"), 0o666))
	tr3 := &Tracker{}
	tr3.Init(dir)
	require.Equal(t, initial(), tr3.Get())
}
