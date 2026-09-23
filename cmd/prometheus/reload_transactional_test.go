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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/config"
	v1 "github.com/prometheus/prometheus/web/api/v1"
)

func TestReloadStatusBeforeFirstAttempt(t *testing.T) {
	dir := t.TempDir()
	logger := promslog.NewNopLogger()
	state := newReloadState(dir, logger)
	state.load()
	configReloadState = state
	t.Cleanup(func() { configReloadState = nil })

	got := state.snapshot()
	require.Equal(t, v1.EmptyReloadStatus(), got)
	_, err := os.Stat(filepath.Join(dir, reloadStatusFilename))
	require.True(t, os.IsNotExist(err))
}

func TestTransactionalReloadRollbackAndPersist(t *testing.T) {
	dir := t.TempDir()
	logger := promslog.NewNopLogger()
	state := newReloadState(dir, logger)
	configReloadState = state
	t.Cleanup(func() { configReloadState = nil })

	goodPath := filepath.Join(dir, "good.yml")
	badPath := filepath.Join(dir, "bad.yml")
	require.NoError(t, os.WriteFile(goodPath, []byte("global:\n  scrape_interval: 15s\n"), 0o644))
	require.NoError(t, os.WriteFile(badPath, []byte("global:\n  scrape_interval: 30s\n"), 0o644))

	var applied []string
	reloaders := []reloader{
		{name: "first", reloader: func(c *config.Config) error {
			applied = append(applied, "first:"+c.GlobalConfig.ScrapeInterval.String())
			return nil
		}},
		{name: "second", reloader: func(c *config.Config) error {
			if c.GlobalConfig.ScrapeInterval != model.Duration(15*time.Second) {
				return os.ErrInvalid
			}
			applied = append(applied, "second")
			return nil
		}},
	}
	interval := &safePromQLNoStepSubqueryInterval{}

	require.NoError(t, reloadConfig(goodPath, false, true, false, logger, interval, func(bool) {}, reloaders...))
	_, err := os.Stat(filepath.Join(dir, reloadStatusFilename))
	require.True(t, os.IsNotExist(err))
	require.Equal(t, v1.EmptyReloadStatus(), state.snapshot())

	applied = nil
	err = reloadConfig(badPath, false, true, true, logger, interval, func(bool) {}, reloaders...)
	require.Error(t, err)
	require.Equal(t, []string{
		"first:30s",
		"first:15s",
		"second",
	}, applied)

	st := state.snapshot()
	require.False(t, st.LastReloadSuccessful)
	require.Equal(t, reloadErrApply, st.ErrorCategory)
	require.Equal(t, []string{"first"}, st.AppliedReloaders)
	require.True(t, st.RollbackAttempted)
	require.True(t, st.RollbackSuccessful)
	require.Equal(t, "second", st.FailedReloader)
	require.NotEmpty(t, st.LastReloadID)
	_, parseErr := time.Parse(time.RFC3339Nano, st.LastReloadID)
	require.NoError(t, parseErr)
	require.Contains(t, st.ReloaderTimingsMS, "first")
	require.Contains(t, st.ReloaderTimingsMS, "second")

	b, err := os.ReadFile(filepath.Join(dir, reloadStatusFilename))
	require.NoError(t, err)
	var persisted v1.ReloadStatus
	require.NoError(t, json.Unmarshal(b, &persisted))
	require.Equal(t, st.LastReloadID, persisted.LastReloadID)
	require.Equal(t, st.LastReloadSuccessful, persisted.LastReloadSuccessful)
	require.Equal(t, st.ErrorCategory, persisted.ErrorCategory)

	reloaded := newReloadState(dir, logger)
	reloaded.load()
	require.Equal(t, st.LastReloadID, reloaded.snapshot().LastReloadID)

	require.NoError(t, os.WriteFile(filepath.Join(dir, reloadStatusFilename), []byte("{"), 0o644))
	corrupt := newReloadState(dir, logger)
	corrupt.load()
	require.Equal(t, v1.EmptyReloadStatus(), corrupt.snapshot())
}

func TestTransactionalReloadLoadErrorSkipsRollback(t *testing.T) {
	dir := t.TempDir()
	logger := promslog.NewNopLogger()
	state := newReloadState(dir, logger)
	state.setLastGood(&config.Config{})
	configReloadState = state
	t.Cleanup(func() { configReloadState = nil })

	called := false
	reloaders := []reloader{{name: "first", reloader: func(*config.Config) error {
		called = true
		return nil
	}}}
	err := reloadConfig(filepath.Join(dir, "missing.yml"), false, true, true, logger, &safePromQLNoStepSubqueryInterval{}, func(bool) {}, reloaders...)
	require.Error(t, err)
	require.False(t, called)
	st := state.snapshot()
	require.Equal(t, reloadErrLoad, st.ErrorCategory)
	require.False(t, st.RollbackAttempted)
	require.False(t, st.LastReloadSuccessful)
	require.Empty(t, st.AppliedReloaders)
}

func TestTransactionalReloadRollbackError(t *testing.T) {
	dir := t.TempDir()
	logger := promslog.NewNopLogger()
	state := newReloadState(dir, logger)
	configReloadState = state
	t.Cleanup(func() { configReloadState = nil })

	goodPath := filepath.Join(dir, "good.yml")
	badPath := filepath.Join(dir, "bad.yml")
	require.NoError(t, os.WriteFile(goodPath, []byte("{}\n"), 0o644))
	require.NoError(t, os.WriteFile(badPath, []byte("{}\n"), 0o644))

	phase := "good"
	reloaders := []reloader{
		{name: "ok", reloader: func(*config.Config) error { return nil }},
		{name: "flaky", reloader: func(*config.Config) error {
			if phase == "bad" || phase == "rollback" {
				if phase == "bad" {
					phase = "rollback"
					return os.ErrClosed
				}
				return os.ErrPermission
			}
			return nil
		}},
	}
	require.NoError(t, reloadConfig(goodPath, false, true, false, logger, &safePromQLNoStepSubqueryInterval{}, func(bool) {}, reloaders...))
	phase = "bad"
	err := reloadConfig(badPath, false, true, true, logger, &safePromQLNoStepSubqueryInterval{}, func(bool) {}, reloaders...)
	require.Error(t, err)
	st := state.snapshot()
	require.Equal(t, reloadErrRollback, st.ErrorCategory)
	require.True(t, st.RollbackAttempted)
	require.False(t, st.RollbackSuccessful)
	require.Equal(t, "flaky", st.FailedReloader)
	require.Equal(t, []string{"ok"}, st.AppliedReloaders)
}
