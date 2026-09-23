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

	"github.com/stretchr/testify/require"
)

func TestStoreInitial(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, nil)

	require.Equal(t, Initial(), s.Get())
	_, err := os.Stat(filepath.Join(dir, FileName))
	require.ErrorIs(t, err, os.ErrNotExist)

	b, err := json.Marshal(s.Get())
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

func TestStoreRecordSurvivesReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st := Status{
		LastReloadID:         "2026-09-23T11:11:00Z",
		ErrorCategory:        ErrorCategoryRollback,
		ErrorMessage:         "boom",
		AppliedReloaders:     []string{"a"},
		RollbackAttempted:    true,
		FailedReloader:       "b",
		ReloaderTimingsMs:    map[string]float64{"a": 1, "b": 2},
		LastReloadSuccessful: false,
	}
	require.NoError(t, NewStore(dir, nil).Record(st))

	b, err := os.ReadFile(filepath.Join(dir, FileName))
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(b, &onDisk))
	require.Equal(t, "2026-09-23T11:11:00Z", onDisk["last_reload_id"])
	require.Equal(t, false, onDisk["last_reload_successful"])
	require.Equal(t, "rollback_error", onDisk["error_category"])

	require.Equal(t, st, NewStore(dir, nil).Get())
}

func TestStoreIgnoresCorruptedFile(t *testing.T) {
	for name, content := range map[string]string{
		"garbage":          "{not json",
		"empty":            "",
		"unknown category": `{"last_reload_id":"x","error_category":"weird"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644))
			s := NewStore(dir, nil)
			got := s.Get()
			require.Equal(t, ErrorCategoryNone, got.ErrorCategory)
			require.NotNil(t, got.AppliedReloaders)
			require.NotNil(t, got.ReloaderTimingsMs)

			require.NoError(t, s.Record(Status{LastReloadID: "y", LastReloadSuccessful: true}))
			require.Equal(t, "y", NewStore(dir, nil).Get().LastReloadID)
		})
	}
}

func TestStoreGetReturnsCopy(t *testing.T) {
	s := NewStore(t.TempDir(), nil)
	require.NoError(t, s.Record(Status{AppliedReloaders: []string{"a"}, ReloaderTimingsMs: map[string]float64{"a": 1}}))
	got := s.Get()
	got.AppliedReloaders[0] = "changed"
	got.ReloaderTimingsMs["a"] = 42
	require.Equal(t, []string{"a"}, s.Get().AppliedReloaders)
	require.Equal(t, map[string]float64{"a": 1}, s.Get().ReloaderTimingsMs)
}
