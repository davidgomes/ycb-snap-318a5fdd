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

package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"
)

func TestReloadStatusPersistence(t *testing.T) {
	dir := t.TempDir()

	missing, err := LoadReloadStatus(dir)
	require.NoError(t, err)
	require.Equal(t, EmptyReloadStatus(), missing)
	_, statErr := os.Stat(ReloadStatusPath(dir))
	require.True(t, os.IsNotExist(statErr))

	status := EmptyReloadStatus()
	status.LastReloadID = "2026-09-23T09:08:00Z"
	status.LastReloadSuccessful = false
	status.ErrorCategory = ReloadErrorApply
	status.ErrorMessage = "rules failed"
	status.AppliedReloaders = []string{"db_storage"}
	status.RollbackAttempted = true
	status.RollbackSuccessful = true
	status.FailedReloader = "rules"
	status.ReloaderTimingsMS = map[string]int64{"db_storage": 4, "rules": 2}

	require.NoError(t, SaveReloadStatus(dir, status))

	loaded, err := LoadReloadStatus(dir)
	require.NoError(t, err)
	require.Equal(t, status, loaded)

	raw, err := os.ReadFile(ReloadStatusPath(dir))
	require.NoError(t, err)
	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj))
	require.Equal(t, "2026-09-23T09:08:00Z", obj["last_reload_id"])
	require.Equal(t, false, obj["last_reload_successful"])
	require.Equal(t, "apply_error", obj["error_category"])

	require.NoError(t, os.WriteFile(ReloadStatusPath(dir), []byte("{"), 0o644))
	corrupt, err := LoadReloadStatus(dir)
	require.Error(t, err)
	require.Equal(t, EmptyReloadStatus(), corrupt)

	require.NoError(t, os.WriteFile(ReloadStatusPath(dir), []byte(`{"error_category":"nope"}`), 0o644))
	invalid, err := LoadReloadStatus(dir)
	require.Error(t, err)
	require.Equal(t, EmptyReloadStatus(), invalid)
}

func TestServeReloadStatus(t *testing.T) {
	api := &API{}
	got := api.serveReloadStatus(httptest.NewRequest(http.MethodGet, "/api/v1/status/reload", http.NoBody))
	require.Nil(t, got.err)
	require.Equal(t, EmptyReloadStatus(), got.data)

	dir := t.TempDir()
	status := EmptyReloadStatus()
	status.LastReloadID = "2026-09-23T09:08:00Z"
	status.ErrorCategory = ReloadErrorLoad
	status.ErrorMessage = "bad yaml"
	status.LastReloadSuccessful = false
	require.NoError(t, SaveReloadStatus(dir, status))

	api.dbDir = dir
	api.logger = promslog.NewNopLogger()
	got = api.serveReloadStatus(httptest.NewRequest(http.MethodGet, "/api/v1/status/reload", http.NoBody))
	require.Nil(t, got.err)
	require.Equal(t, status, got.data)

	require.NoError(t, os.WriteFile(filepath.Join(dir, ReloadStatusFilename), []byte("not-json"), 0o644))
	got = api.serveReloadStatus(httptest.NewRequest(http.MethodGet, "/api/v1/status/reload", http.NoBody))
	require.Nil(t, got.err)
	require.Equal(t, EmptyReloadStatus(), got.data)

	api.SetReloadStatus(func() ReloadStatus {
		live := EmptyReloadStatus()
		live.LastReloadSuccessful = true
		live.ErrorCategory = ReloadErrorNone
		live.LastReloadID = "2026-09-23T10:00:00Z"
		return live
	})
	got = api.serveReloadStatus(httptest.NewRequest(http.MethodGet, "/api/v1/status/reload", http.NoBody))
	require.Nil(t, got.err)
	require.Equal(t, true, got.data.(ReloadStatus).LastReloadSuccessful)
	require.Equal(t, "2026-09-23T10:00:00Z", got.data.(ReloadStatus).LastReloadID)
}
