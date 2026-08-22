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

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/config"
)

func TestTransactionalReloadRollsBackAfterApplyFailure(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "prometheus.yml")
	require.NoError(t, os.WriteFile(configFile, []byte("global:\n  scrape_interval: 30s\n"), 0o644))

	statusStore := newReloadStateStore(dir, promslog.NewNopLogger())
	manager := newTransactionalReloadManager(statusStore)
	noStepInterval := &safePromQLNoStepSubqueryInterval{}
	reloaders := []reloader{
		{
			name: "first",
			reloader: func(conf *config.Config) error {
				if conf.GlobalConfig.ScrapeInterval.String() == "15s" {
					return nil
				}
				return nil
			},
		},
		{
			name: "second",
			reloader: func(conf *config.Config) error {
				if conf.GlobalConfig.ScrapeInterval.String() == "15s" {
					return errors.New("apply failed")
				}
				return nil
			},
		},
	}

	require.NoError(t, manager.initialize(configFile, false, promslog.NewNopLogger(), noStepInterval, reloaders...))
	_, err := os.Stat(filepath.Join(dir, reloadStateFilename))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.WriteFile(configFile, []byte("global:\n  scrape_interval: 15s\n"), 0o644))

	err = manager.reload(configFile, false, promslog.NewNopLogger(), noStepInterval, func(bool) {}, reloaders...)
	require.Error(t, err)

	status := statusStore.get()
	require.False(t, status.LastReloadSuccessful)
	_, err = time.Parse(time.RFC3339Nano, status.LastReloadID)
	require.NoError(t, err)
	require.Equal(t, "apply_error", status.ErrorCategory)
	require.Equal(t, "second", status.FailedReloader)
	require.Equal(t, []string{"first"}, status.AppliedReloaders)
	require.True(t, status.RollbackAttempted)
	require.True(t, status.RollbackSuccessful)
	require.Contains(t, status.ReloaderTimingsMS, "first")
	require.Contains(t, status.ReloaderTimingsMS, "second")

	persisted, err := os.ReadFile(filepath.Join(dir, reloadStateFilename))
	require.NoError(t, err)
	var persistedStatus map[string]any
	require.NoError(t, json.Unmarshal(persisted, &persistedStatus))
	require.Equal(t, "apply_error", persistedStatus["error_category"])
	require.NotEmpty(t, persistedStatus["last_reload_id"])

	reloadedStore := newReloadStateStore(dir, promslog.NewNopLogger())
	require.Equal(t, status, reloadedStore.get())
}

func TestTransactionalReloadLoadFailureDoesNotCallReloaders(t *testing.T) {
	dir := t.TempDir()
	statusStore := newReloadStateStore(dir, promslog.NewNopLogger())
	manager := newTransactionalReloadManager(statusStore)
	called := false

	err := manager.reload(
		filepath.Join(dir, "missing.yml"),
		false,
		promslog.NewNopLogger(),
		&safePromQLNoStepSubqueryInterval{},
		func(bool) {},
		reloader{name: "unexpected", reloader: func(*config.Config) error {
			called = true
			return nil
		}},
	)
	require.Error(t, err)
	require.False(t, called)

	status := statusStore.get()
	require.Equal(t, "load_error", status.ErrorCategory)
	require.False(t, status.RollbackAttempted)
	require.Empty(t, status.AppliedReloaders)
	require.Empty(t, status.ReloaderTimingsMS)
}

func TestReloadStateStoreIgnoresCorruptState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, reloadStateFilename)
	require.NoError(t, os.WriteFile(statePath, []byte("{not json"), 0o644))

	store := newReloadStateStore(dir, promslog.NewNopLogger())
	status := store.get()
	require.Equal(t, "none", status.ErrorCategory)
	require.Empty(t, status.LastReloadID)
	require.Empty(t, status.AppliedReloaders)
	require.Empty(t, status.ReloaderTimingsMS)
}

func TestTransactionalReloadStateStartsEmpty(t *testing.T) {
	store := newReloadStateStore(t.TempDir(), promslog.NewNopLogger())
	status := store.get()
	require.Equal(t, "none", status.ErrorCategory)
	require.Empty(t, status.LastReloadID)
	require.False(t, status.LastReloadSuccessful)
	require.Equal(t, []string{}, status.AppliedReloaders)
	require.Equal(t, map[string]int64{}, status.ReloaderTimingsMS)
}
