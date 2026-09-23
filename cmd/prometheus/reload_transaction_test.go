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
	"runtime/debug"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/config"
	api_v1 "github.com/prometheus/prometheus/web/api/v1"
)

func preserveGOGC(t *testing.T) {
	t.Helper()
	prev := debug.SetGCPercent(100)
	debug.SetGCPercent(prev)
	t.Cleanup(func() {
		debug.SetGCPercent(prev)
	})
}

func writeReloadConfig(t *testing.T, dir, name, interval string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "global:\n  scrape_interval: " + interval + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func parseTestDuration(t *testing.T, value string) model.Duration {
	t.Helper()
	d, err := model.ParseDuration(value)
	require.NoError(t, err)
	return d
}

type spyReloader struct {
	name      string
	intervals []model.Duration
	errAt     map[int]error
	calls     int
}

func (s *spyReloader) apply(conf *config.Config) error {
	n := s.calls
	s.calls++
	s.intervals = append(s.intervals, conf.GlobalConfig.ScrapeInterval)
	if err, ok := s.errAt[n]; ok {
		return err
	}
	return nil
}

func (s *spyReloader) asReloader() reloader {
	return reloader{name: s.name, reloader: s.apply}
}

func testReload(t *testing.T, filename string, rs *reloadState, recordOutcome bool, rls ...reloader) error {
	t.Helper()
	return reloadConfig(filename, false, promslog.NewNopLogger(), &safePromQLNoStepSubqueryInterval{}, func(bool) {}, rs, recordOutcome, rls...)
}

func TestTransactionalReloadInitialState(t *testing.T) {
	preserveGOGC(t)
	dir := t.TempDir()
	rs := newReloadState(dir, true, promslog.NewNopLogger())
	require.Equal(t, api_v1.EmptyReloadStatus(), rs.Status())

	cfg := writeReloadConfig(t, dir, "prometheus.yml", "15s")
	first := &spyReloader{name: "first"}
	require.NoError(t, testReload(t, cfg, rs, false, first.asReloader()))

	_, err := os.Stat(rs.path())
	require.True(t, os.IsNotExist(err))
	require.Equal(t, api_v1.EmptyReloadStatus(), rs.Status())
	require.Equal(t, []model.Duration{parseTestDuration(t, "15s")}, first.intervals)
}

func TestTransactionalReloadLoadErrorDoesNotRollback(t *testing.T) {
	preserveGOGC(t)
	dir := t.TempDir()
	rs := newReloadState(dir, true, promslog.NewNopLogger())
	good := writeReloadConfig(t, dir, "good.yml", "15s")
	bad := filepath.Join(dir, "bad.yml")
	require.NoError(t, os.WriteFile(bad, []byte("global:\n  scrape_interval: [\n"), 0o644))

	spy := &spyReloader{name: "first"}
	require.NoError(t, testReload(t, good, rs, false, spy.asReloader()))
	err := testReload(t, bad, rs, true, spy.asReloader())
	require.Error(t, err)

	status := rs.Status()
	require.False(t, status.LastReloadSuccessful)
	require.Equal(t, api_v1.ReloadErrorCategoryLoad, status.ErrorCategory)
	require.NotEmpty(t, status.ErrorMessage)
	require.Empty(t, status.AppliedReloaders)
	require.False(t, status.RollbackAttempted)
	require.False(t, status.RollbackSuccessful)
	require.Empty(t, status.FailedReloader)
	require.Empty(t, status.ReloaderTimingsMS)
	_, parseErr := time.Parse(time.RFC3339, status.LastReloadID)
	require.NoError(t, parseErr)
	require.Equal(t, 1, spy.calls, "load failure must not apply or roll back components")

	persisted := readPersistedReloadStatus(t, rs.path())
	require.Equal(t, status, persisted)
}

func TestTransactionalReloadApplyErrorWithoutRollback(t *testing.T) {
	preserveGOGC(t)
	dir := t.TempDir()
	rs := newReloadState(dir, true, promslog.NewNopLogger())
	initial := writeReloadConfig(t, dir, "initial.yml", "15s")
	next := writeReloadConfig(t, dir, "next.yml", "30s")

	first := &spyReloader{name: "first", errAt: map[int]error{1: errors.New("first failed")}}
	second := &spyReloader{name: "second"}
	require.NoError(t, testReload(t, initial, rs, false, first.asReloader(), second.asReloader()))
	err := testReload(t, next, rs, true, first.asReloader(), second.asReloader())
	require.Error(t, err)

	status := rs.Status()
	require.Equal(t, api_v1.ReloadErrorCategoryApply, status.ErrorCategory)
	require.False(t, status.LastReloadSuccessful)
	require.Equal(t, "first failed", status.ErrorMessage)
	require.Equal(t, "first", status.FailedReloader)
	require.Empty(t, status.AppliedReloaders)
	require.False(t, status.RollbackAttempted)
	require.False(t, status.RollbackSuccessful)
	require.Contains(t, status.ReloaderTimingsMS, "first")
	require.NotContains(t, status.ReloaderTimingsMS, "second")
	require.Equal(t, 1, second.calls)
	require.Equal(t, []model.Duration{parseTestDuration(t, "15s")}, second.intervals)
}

func TestTransactionalReloadRollsBackToStartupConfig(t *testing.T) {
	preserveGOGC(t)
	dir := t.TempDir()
	rs := newReloadState(dir, true, promslog.NewNopLogger())
	initial := writeReloadConfig(t, dir, "initial.yml", "15s")
	next := writeReloadConfig(t, dir, "next.yml", "30s")

	first := &spyReloader{name: "first"}
	second := &spyReloader{name: "second", errAt: map[int]error{1: errors.New("second failed")}}
	require.NoError(t, testReload(t, initial, rs, false, first.asReloader(), second.asReloader()))
	err := testReload(t, next, rs, true, first.asReloader(), second.asReloader())
	require.Error(t, err)

	want15 := parseTestDuration(t, "15s")
	want30 := parseTestDuration(t, "30s")
	require.Equal(t, []model.Duration{want15, want30, want15}, first.intervals)
	require.Equal(t, []model.Duration{want15, want30, want15}, second.intervals)

	status := rs.Status()
	require.Equal(t, api_v1.ReloadErrorCategoryApply, status.ErrorCategory)
	require.Equal(t, []string{"first"}, status.AppliedReloaders)
	require.Equal(t, "second", status.FailedReloader)
	require.True(t, status.RollbackAttempted)
	require.True(t, status.RollbackSuccessful)
	require.Equal(t, "second failed", status.ErrorMessage)
	require.False(t, status.LastReloadSuccessful)
}

func TestTransactionalReloadRollsBackToLastSuccessfulReload(t *testing.T) {
	preserveGOGC(t)
	dir := t.TempDir()
	rs := newReloadState(dir, true, promslog.NewNopLogger())
	initial := writeReloadConfig(t, dir, "initial.yml", "15s")
	secondCfg := writeReloadConfig(t, dir, "second.yml", "30s")
	thirdCfg := writeReloadConfig(t, dir, "third.yml", "45s")

	first := &spyReloader{name: "first"}
	second := &spyReloader{name: "second", errAt: map[int]error{2: errors.New("later failed")}}
	require.NoError(t, testReload(t, initial, rs, false, first.asReloader(), second.asReloader()))
	require.NoError(t, testReload(t, secondCfg, rs, true, first.asReloader(), second.asReloader()))
	success := rs.Status()
	require.True(t, success.LastReloadSuccessful)
	require.Equal(t, api_v1.ReloadErrorCategoryNone, success.ErrorCategory)
	require.Equal(t, []string{"first", "second"}, success.AppliedReloaders)
	require.Empty(t, success.FailedReloader)
	require.False(t, success.RollbackAttempted)

	err := testReload(t, thirdCfg, rs, true, first.asReloader(), second.asReloader())
	require.Error(t, err)

	want30 := parseTestDuration(t, "30s")
	require.Equal(t, want30, first.intervals[len(first.intervals)-1])
	require.Equal(t, want30, second.intervals[len(second.intervals)-1])
	status := rs.Status()
	require.Equal(t, api_v1.ReloadErrorCategoryApply, status.ErrorCategory)
	require.True(t, status.RollbackSuccessful)
	require.NotEqual(t, success.LastReloadID, status.LastReloadID)
}

func TestTransactionalReloadRollbackError(t *testing.T) {
	preserveGOGC(t)
	dir := t.TempDir()
	rs := newReloadState(dir, true, promslog.NewNopLogger())
	initial := writeReloadConfig(t, dir, "initial.yml", "15s")
	next := writeReloadConfig(t, dir, "next.yml", "30s")

	first := &spyReloader{name: "first", errAt: map[int]error{2: errors.New("rollback failed")}}
	second := &spyReloader{name: "second", errAt: map[int]error{1: errors.New("apply failed")}}
	require.NoError(t, testReload(t, initial, rs, false, first.asReloader(), second.asReloader()))
	err := testReload(t, next, rs, true, first.asReloader(), second.asReloader())
	require.Error(t, err)

	status := rs.Status()
	require.Equal(t, api_v1.ReloadErrorCategoryRollback, status.ErrorCategory)
	require.True(t, status.RollbackAttempted)
	require.False(t, status.RollbackSuccessful)
	require.Equal(t, "second", status.FailedReloader)
	require.Contains(t, status.ErrorMessage, "apply failed")
	require.Contains(t, status.ErrorMessage, "rollback of first failed")
	require.False(t, status.LastReloadSuccessful)
}

func TestTransactionalReloadPersistsAcrossNewState(t *testing.T) {
	preserveGOGC(t)
	dir := t.TempDir()
	logger := promslog.NewNopLogger()
	rs := newReloadState(dir, true, logger)
	cfg := writeReloadConfig(t, dir, "prometheus.yml", "15s")
	spy := &spyReloader{name: "first"}
	require.NoError(t, testReload(t, cfg, rs, false, spy.asReloader()))
	require.NoError(t, testReload(t, cfg, rs, true, spy.asReloader()))

	reloaded := newReloadState(dir, true, logger)
	require.Equal(t, rs.Status(), reloaded.Status())
	require.True(t, reloaded.Status().LastReloadSuccessful)

	raw := map[string]any{}
	data, err := os.ReadFile(rs.path())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Contains(t, raw, "last_reload_id")
	require.Contains(t, raw, "last_reload_successful")
	require.Contains(t, raw, "error_category")
	require.Contains(t, raw, "reloader_timings_ms")
}

func TestTransactionalReloadCorruptStateIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, reloadStatusFilename)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	rs := newReloadState(dir, true, promslog.NewNopLogger())
	require.Equal(t, api_v1.EmptyReloadStatus(), rs.Status())
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{not json", string(body))

	require.NoError(t, os.WriteFile(path, []byte(`{"error_category":"nope"}`), 0o644))
	rs = newReloadState(dir, true, promslog.NewNopLogger())
	require.Equal(t, api_v1.EmptyReloadStatus(), rs.Status())

	require.NoError(t, os.WriteFile(path, []byte("   \n"), 0o644))
	rs = newReloadState(dir, true, promslog.NewNopLogger())
	require.Equal(t, api_v1.EmptyReloadStatus(), rs.Status())
}

func TestTransactionalReloadAcceptsPartialPersistedState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, reloadStatusFilename)
	require.NoError(t, os.WriteFile(path, []byte(`{"error_category":"load_error","error_message":"bad yaml","extra":true}`), 0o644))

	rs := newReloadState(dir, true, promslog.NewNopLogger())
	status := rs.Status()
	require.Equal(t, api_v1.ReloadErrorCategoryLoad, status.ErrorCategory)
	require.Equal(t, "bad yaml", status.ErrorMessage)
	require.Empty(t, status.AppliedReloaders)
	require.NotNil(t, status.AppliedReloaders)
	require.NotNil(t, status.ReloaderTimingsMS)
	require.Empty(t, status.ReloaderTimingsMS)
}

func TestReloadWithoutTransactionalModeDoesNotRecordOrRollback(t *testing.T) {
	preserveGOGC(t)
	dir := t.TempDir()
	rs := newReloadState(dir, false, promslog.NewNopLogger())
	initial := writeReloadConfig(t, dir, "initial.yml", "15s")
	next := writeReloadConfig(t, dir, "next.yml", "30s")

	first := &spyReloader{name: "first"}
	second := &spyReloader{name: "second", errAt: map[int]error{1: errors.New("second failed")}}
	require.NoError(t, testReload(t, initial, rs, false, first.asReloader(), second.asReloader()))
	err := testReload(t, next, rs, true, first.asReloader(), second.asReloader())
	require.Error(t, err)

	require.Equal(t, api_v1.EmptyReloadStatus(), rs.Status())
	_, statErr := os.Stat(rs.path())
	require.True(t, os.IsNotExist(statErr))
	require.Equal(t, []model.Duration{parseTestDuration(t, "15s"), parseTestDuration(t, "30s")}, first.intervals)
	require.Equal(t, 2, second.calls)
}

func TestTransactionalReloadWithoutKnownGoodReportsRollbackError(t *testing.T) {
	preserveGOGC(t)
	dir := t.TempDir()
	rs := newReloadState(dir, true, promslog.NewNopLogger())
	cfg := writeReloadConfig(t, dir, "prometheus.yml", "15s")
	first := &spyReloader{name: "first"}
	second := &spyReloader{name: "second", errAt: map[int]error{0: errors.New("apply failed")}}
	err := testReload(t, cfg, rs, true, first.asReloader(), second.asReloader())
	require.Error(t, err)

	status := rs.Status()
	require.Equal(t, api_v1.ReloadErrorCategoryRollback, status.ErrorCategory)
	require.True(t, status.RollbackAttempted)
	require.False(t, status.RollbackSuccessful)
	require.Contains(t, status.ErrorMessage, "no known-good configuration")
	require.Equal(t, []string{"first"}, status.AppliedReloaders)
}

func readPersistedReloadStatus(t *testing.T, path string) api_v1.ReloadStatus {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var status api_v1.ReloadStatus
	require.NoError(t, json.Unmarshal(data, &status))
	return status.Normalized()
}
