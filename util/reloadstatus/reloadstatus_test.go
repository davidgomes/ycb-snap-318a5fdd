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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"
)

func failedStatus() Status {
	return Status{
		LastReloadID:         "2026-09-23T11:12:13.456789Z",
		LastReloadSuccessful: false,
		ErrorCategory:        ErrorCategoryApply,
		ErrorMessage:         "reloader \"rules\" failed",
		AppliedReloaders:     []string{"db_storage", "scrape"},
		RollbackAttempted:    true,
		RollbackSuccessful:   true,
		FailedReloader:       "rules",
		ReloaderTimingsMs:    map[string]int64{"db_storage": 1, "scrape": 2, "rules": 3},
	}
}

func TestNewStatusEncodesEmptyCollections(t *testing.T) {
	b, err := json.Marshal(NewStatus(""))
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
	}`, string(b))
}

func TestNewStoreWithoutPersistedStatus(t *testing.T) {
	for name, dir := range map[string]string{
		"empty dir":   t.TempDir(),
		"missing dir": filepath.Join(t.TempDir(), "missing"),
	} {
		t.Run(name, func(t *testing.T) {
			s := NewStore(dir, promslog.NewNopLogger())
			require.Equal(t, NewStatus(""), s.Get())
			require.NoFileExists(t, filepath.Join(dir, Filename))
		})
	}
}

func TestNewStoreIgnoresCorruptedStatus(t *testing.T) {
	for name, content := range map[string]string{
		"empty":            "",
		"truncated":        `{"last_reload_id": "2026-09-23T11:12:13Z", "last_reload_succ`,
		"not an object":    `["none"]`,
		"missing category": `{"last_reload_id": "2026-09-23T11:12:13Z", "last_reload_successful": true}`,
		"unknown category": `{"last_reload_id": "2026-09-23T11:12:13Z", "error_category": "oops"}`,
		"invalid id":       `{"last_reload_id": "yesterday", "error_category": "none"}`,
		"wrong type":       `{"last_reload_id": "2026-09-23T11:12:13Z", "error_category": "none", "applied_reloaders": "rules"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, Filename), []byte(content), 0o644))

			s := NewStore(dir, promslog.NewNopLogger())
			require.Equal(t, NewStatus(""), s.Get())

			// A corrupted file must not prevent recording new outcomes.
			require.NoError(t, s.Set(failedStatus()))
			require.Equal(t, failedStatus(), NewStore(dir, promslog.NewNopLogger()).Get())
		})
	}
}

func TestNewStoreIgnoresUnreadableStatus(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, Filename), 0o755))

	s := NewStore(dir, promslog.NewNopLogger())
	require.Equal(t, NewStatus(""), s.Get())
}

func TestNewStoreFillsMissingCollections(t *testing.T) {
	dir := t.TempDir()
	content := `{"last_reload_id": "2026-09-23T11:12:13Z", "last_reload_successful": false, "error_category": "load_error"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, Filename), []byte(content), 0o644))

	expected := NewStatus("2026-09-23T11:12:13Z")
	expected.ErrorCategory = ErrorCategoryLoad
	require.Equal(t, expected, NewStore(dir, promslog.NewNopLogger()).Get())
}

func TestStoreSetPersistsStatus(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, promslog.NewNopLogger())

	require.NoError(t, s.Set(failedStatus()))
	require.Equal(t, failedStatus(), s.Get())

	b, err := os.ReadFile(filepath.Join(dir, Filename))
	require.NoError(t, err)
	var persisted map[string]any
	require.NoError(t, json.Unmarshal(b, &persisted))
	require.Equal(t, "2026-09-23T11:12:13.456789Z", persisted["last_reload_id"])
	require.Equal(t, false, persisted["last_reload_successful"])
	require.Equal(t, "apply_error", persisted["error_category"])
	require.NoFileExists(t, filepath.Join(dir, Filename+".tmp"))

	require.Equal(t, failedStatus(), NewStore(dir, promslog.NewNopLogger()).Get())

	succeeded := NewStatus("2026-09-23T11:15:00Z")
	succeeded.LastReloadSuccessful = true
	succeeded.AppliedReloaders = []string{"db_storage", "scrape", "rules"}
	require.NoError(t, s.Set(succeeded))
	require.Equal(t, succeeded, NewStore(dir, promslog.NewNopLogger()).Get())
}

func TestStoreIsolatesCallers(t *testing.T) {
	s := NewStore(t.TempDir(), promslog.NewNopLogger())

	st := failedStatus()
	require.NoError(t, s.Set(st))
	st.AppliedReloaders[0] = "changed"
	st.ReloaderTimingsMs["changed"] = 1

	got := s.Get()
	require.Equal(t, failedStatus(), got)
	got.AppliedReloaders[0] = "changed"
	got.ReloaderTimingsMs["changed"] = 1
	require.Equal(t, failedStatus(), s.Get())
}

func TestStoreSetKeepsStatusWhenPersistingFails(t *testing.T) {
	parent := t.TempDir()
	notADir := filepath.Join(parent, "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o644))

	s := NewStore(notADir, promslog.NewNopLogger())
	require.Error(t, s.Set(failedStatus()))
	require.Equal(t, failedStatus(), s.Get())
}
