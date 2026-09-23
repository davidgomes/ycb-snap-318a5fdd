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

package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"

	api_v1 "github.com/prometheus/prometheus/web/api/v1"
)

func TestReloadStateMissingOrCorrupt(t *testing.T) {
	logger := promslog.NewNopLogger()

	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		state := NewReloadState(dir, logger)
		status := state.Status()
		require.Equal(t, api_v1.EmptyReloadStatus(), status)
		_, err := os.Stat(filepath.Join(dir, ReloadStatusFileName))
		require.True(t, os.IsNotExist(err))
	})

	t.Run("corrupt file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ReloadStatusFileName)
		require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

		state := NewReloadState(dir, logger)
		require.Equal(t, api_v1.EmptyReloadStatus(), state.Status())
	})

	t.Run("invalid category", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ReloadStatusFileName)
		require.NoError(t, os.WriteFile(path, []byte(`{"error_category":"nope","last_reload_successful":true}`), 0o644))

		state := NewReloadState(dir, logger)
		require.Equal(t, api_v1.EmptyReloadStatus(), state.Status())
		require.False(t, state.Status().LastReloadSuccessful)
	})
}

func TestReloadStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	logger := promslog.NewNopLogger()
	state := NewReloadState(dir, logger)

	status := api_v1.ReloadStatus{
		LastReloadID:         "2026-09-23T10:00:00.123456789Z",
		LastReloadSuccessful: false,
		ErrorCategory:        api_v1.ReloadErrorApply,
		ErrorMessage:         "failed to apply",
		AppliedReloaders:     []string{"db_storage", "web_handler"},
		RollbackAttempted:    true,
		RollbackSuccessful:   true,
		FailedReloader:       "query_engine",
		ReloaderTimingsMS:    map[string]int64{"db_storage": 2, "query_engine": 4},
	}
	state.Record(status)

	path := filepath.Join(dir, ReloadStatusFileName)
	buf, err := os.ReadFile(path)
	require.NoError(t, err)

	var persisted api_v1.ReloadStatus
	require.NoError(t, json.Unmarshal(buf, &persisted))
	require.Equal(t, status, persisted)

	reloaded := NewReloadState(dir, logger)
	require.Equal(t, status, reloaded.Status())
}

func TestEmptyReloadStatusJSON(t *testing.T) {
	buf, err := json.Marshal(api_v1.EmptyReloadStatus())
	require.NoError(t, err)
	require.JSONEq(t, `{
		"last_reload_id": "",
		"last_reload_successful": false,
		"error_category": "none",
		"error_message": "",
		"applied_reloaders": [],
		"rollback_attempted": false,
		"rollback_successful": false,
		"failed_reloader": "",
		"reloader_timings_ms": {}
	}`, string(buf))
}
