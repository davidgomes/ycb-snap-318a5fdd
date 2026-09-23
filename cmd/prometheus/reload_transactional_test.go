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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/util/features"
	"github.com/prometheus/prometheus/util/testutil"
	"github.com/prometheus/prometheus/web"
	apiv1 "github.com/prometheus/prometheus/web/api/v1"
)

func TestSetFeatureListTransactionalReload(t *testing.T) {
	cfg := &flagConfig{featureList: []string{"transactional-reload-config"}}
	require.NoError(t, cfg.setFeatureListOptions(promslog.NewNopLogger()))
	require.True(t, cfg.enableTransactionalReload)
	require.True(t, features.Get()[features.Prometheus]["transactional_reload_config"])
}

func TestTransactionalReloadOutcomes(t *testing.T) {
	logger := promslog.NewNopLogger()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "prometheus.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("global:\n  scrape_interval: 30s\n"), 0o644))

	state := web.NewReloadState(dir, logger)
	interval := &safePromQLNoStepSubqueryInterval{}
	var seen []model.Duration

	reloaders := []reloader{
		{
			name: "first",
			reloader: func(cfg *config.Config) error {
				seen = append(seen, cfg.GlobalConfig.ScrapeInterval)
				return nil
			},
		},
		{
			name: "second",
			reloader: func(cfg *config.Config) error {
				if cfg.GlobalConfig.ScrapeInterval == model.Duration(15*time.Second) {
					return errors.New("second rejected 15s")
				}
				seen = append(seen, cfg.GlobalConfig.ScrapeInterval)
				return nil
			},
		},
	}

	opts := reloadOptions{transactional: true, recordOutcome: false, state: state}
	require.NoError(t, reloadConfig(cfgPath, false, logger, interval, func(bool) {}, opts, reloaders...))
	_, err := os.Stat(filepath.Join(dir, web.ReloadStatusFileName))
	require.True(t, os.IsNotExist(err), "startup load must not write a reload status file")
	require.Equal(t, apiEmptyStatus(), state.Status())
	require.NotNil(t, state.LastGood())

	// Load failure does not invoke reloaders and does not roll back.
	require.NoError(t, os.WriteFile(cfgPath, []byte("global:\n  scrape_interval: [\n"), 0o644))
	callsBefore := len(seen)
	opts.recordOutcome = true
	err = reloadConfig(cfgPath, false, logger, interval, func(bool) {}, opts, reloaders...)
	require.Error(t, err)
	require.Equal(t, callsBefore, len(seen))
	status := state.Status()
	require.Equal(t, apiv1.ReloadErrorLoad, status.ErrorCategory)
	require.False(t, status.LastReloadSuccessful)
	require.False(t, status.RollbackAttempted)
	require.Empty(t, status.AppliedReloaders)
	require.Empty(t, status.ReloaderTimingsMS)
	require.NotEmpty(t, status.LastReloadID)
	_, parseErr := time.Parse(time.RFC3339Nano, status.LastReloadID)
	require.NoError(t, parseErr)
	assertStatusFileMatches(t, dir, status)

	// Apply failure of a later reloader rolls back to the startup configuration.
	require.NoError(t, os.WriteFile(cfgPath, []byte("global:\n  scrape_interval: 15s\n"), 0o644))
	seen = nil
	err = reloadConfig(cfgPath, false, logger, interval, func(bool) {}, opts, reloaders...)
	require.Error(t, err)
	status = state.Status()
	require.Equal(t, apiv1.ReloadErrorApply, status.ErrorCategory)
	require.Equal(t, []string{"first"}, status.AppliedReloaders)
	require.Equal(t, "second", status.FailedReloader)
	require.True(t, status.RollbackAttempted)
	require.True(t, status.RollbackSuccessful)
	require.Contains(t, status.ReloaderTimingsMS, "first")
	require.Contains(t, status.ReloaderTimingsMS, "second")
	require.NotContains(t, status.ReloaderTimingsMS, "third")
	// Forward apply of 15s, then rollback apply of the 30s startup config.
	require.Equal(t, []model.Duration{
		model.Duration(15 * time.Second),
		model.Duration(30 * time.Second),
	}, seen)

	// A successful reload becomes the new known-good configuration.
	require.NoError(t, os.WriteFile(cfgPath, []byte("global:\n  scrape_interval: 45s\n"), 0o644))
	seen = nil
	require.NoError(t, reloadConfig(cfgPath, false, logger, interval, func(bool) {}, opts, reloaders...))
	status = state.Status()
	require.True(t, status.LastReloadSuccessful)
	require.Equal(t, apiv1.ReloadErrorNone, status.ErrorCategory)
	require.Equal(t, []string{"first", "second"}, status.AppliedReloaders)
	require.False(t, status.RollbackAttempted)
	require.Empty(t, status.FailedReloader)
	require.Equal(t, "", status.ErrorMessage)

	// The next partial failure rolls back to 45s, not the original 30s.
	require.NoError(t, os.WriteFile(cfgPath, []byte("global:\n  scrape_interval: 15s\n"), 0o644))
	seen = nil
	require.Error(t, reloadConfig(cfgPath, false, logger, interval, func(bool) {}, opts, reloaders...))
	require.Equal(t, []model.Duration{
		model.Duration(15 * time.Second),
		model.Duration(45 * time.Second),
	}, seen)

	// Failure of the first reloader does not roll back.
	rollbackCalls := 0
	failFirst := []reloader{
		{
			name: "first",
			reloader: func(*config.Config) error {
				return errors.New("first failed")
			},
		},
		{
			name: "second",
			reloader: func(*config.Config) error {
				rollbackCalls++
				return nil
			},
		},
	}
	require.NoError(t, os.WriteFile(cfgPath, []byte("global:\n  scrape_interval: 20s\n"), 0o644))
	err = reloadConfig(cfgPath, false, logger, interval, func(bool) {}, opts, failFirst...)
	require.Error(t, err)
	status = state.Status()
	require.Equal(t, apiv1.ReloadErrorApply, status.ErrorCategory)
	require.False(t, status.RollbackAttempted)
	require.Empty(t, status.AppliedReloaders)
	require.Equal(t, "first", status.FailedReloader)
	require.Equal(t, 0, rollbackCalls)

	// A rollback failure is reported as rollback_error. Rollback continues
	// for the other reloaders that already applied the new configuration.
	var rolled []string
	phase := "apply"
	flaky := []reloader{
		{
			name: "first",
			reloader: func(*config.Config) error {
				if phase == "rollback" {
					rolled = append(rolled, "first")
				}
				return nil
			},
		},
		{
			name: "second",
			reloader: func(*config.Config) error {
				if phase == "rollback" {
					rolled = append(rolled, "second")
					return errors.New("rollback second")
				}
				return nil
			},
		},
		{
			name: "third",
			reloader: func(cfg *config.Config) error {
				if cfg.GlobalConfig.ScrapeInterval == model.Duration(15*time.Second) {
					phase = "rollback"
					return errors.New("third rejected 15s")
				}
				return nil
			},
		},
	}
	// Seed a known-good config that both reloaders accept.
	require.NoError(t, os.WriteFile(cfgPath, []byte("global:\n  scrape_interval: 45s\n"), 0o644))
	seed := web.NewReloadState(dir, logger)
	seedOpts := reloadOptions{transactional: true, recordOutcome: false, state: seed}
	require.NoError(t, reloadConfig(cfgPath, false, logger, interval, func(bool) {}, seedOpts, flaky...))
	require.NoError(t, os.WriteFile(cfgPath, []byte("global:\n  scrape_interval: 15s\n"), 0o644))
	seedOpts.recordOutcome = true
	rolled = nil
	require.Error(t, reloadConfig(cfgPath, false, logger, interval, func(bool) {}, seedOpts, flaky...))
	status = seed.Status()
	require.Equal(t, apiv1.ReloadErrorRollback, status.ErrorCategory)
	require.True(t, status.RollbackAttempted)
	require.False(t, status.RollbackSuccessful)
	require.Equal(t, []string{"second", "first"}, rolled)
	require.Contains(t, status.ErrorMessage, "rollback failed")
}

func TestNonTransactionalReloadDoesNotRecord(t *testing.T) {
	logger := promslog.NewNopLogger()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "prometheus.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("global:\n  scrape_interval: 30s\n"), 0o644))

	state := web.NewReloadState(dir, logger)
	var ran []string
	reloaders := []reloader{
		{name: "first", reloader: func(*config.Config) error { ran = append(ran, "first"); return errors.New("nope") }},
		{name: "second", reloader: func(*config.Config) error { ran = append(ran, "second"); return nil }},
	}
	err := reloadConfig(cfgPath, false, logger, &safePromQLNoStepSubqueryInterval{}, func(bool) {}, reloadOptions{state: state}, reloaders...)
	require.Error(t, err)
	require.Equal(t, []string{"first", "second"}, ran)
	require.Equal(t, apiEmptyStatus(), state.Status())
	_, statErr := os.Stat(filepath.Join(dir, web.ReloadStatusFileName))
	require.True(t, os.IsNotExist(statErr))
}

func TestTransactionalReloadHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	configFile := filepath.Join(tmpDir, "prometheus.yml")
	writeCfg := func(body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(configFile, []byte(body), 0o644))
	}
	writeCfg("global:\n  scrape_interval: 30s\n")

	// A corrupt status file must not block startup.
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, web.ReloadStatusFileName), []byte("{{"), 0o644))

	port := testutil.RandomUnprivilegedPort(t)
	prom := prometheusCommandWithLogging(
		t,
		configFile,
		port,
		"--storage.tsdb.path="+dataDir,
		"--web.enable-lifecycle",
		"--enable-feature=transactional-reload-config",
	)
	require.NoError(t, prom.Start())
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	waitReady(t, baseURL)

	var featuresResp struct {
		Status string                     `json:"status"`
		Data   map[string]map[string]bool `json:"data"`
	}
	getJSON(t, baseURL+"/api/v1/features", &featuresResp)
	require.True(t, featuresResp.Data["prometheus"]["transactional_reload_config"])

	status := getReloadStatus(t, baseURL)
	require.Equal(t, "", status.LastReloadID)
	require.False(t, status.LastReloadSuccessful)
	require.Equal(t, apiv1.ReloadErrorNone, status.ErrorCategory)
	require.Empty(t, status.AppliedReloaders)
	require.Empty(t, status.ReloaderTimingsMS)

	// Startup replaces nothing; the corrupt file is left until a real attempt.
	// A successful reload records one outcome.
	writeCfg("global:\n  scrape_interval: 15s\n")
	require.Equal(t, http.StatusOK, postReload(t, baseURL))
	status = getReloadStatus(t, baseURL)
	require.True(t, status.LastReloadSuccessful)
	require.Equal(t, apiv1.ReloadErrorNone, status.ErrorCategory)
	require.NotEmpty(t, status.LastReloadID)
	_, err := time.Parse(time.RFC3339Nano, status.LastReloadID)
	require.NoError(t, err)
	require.Contains(t, status.AppliedReloaders, "web_handler")
	require.Contains(t, status.AppliedReloaders, "query_engine")
	assertStatusFileMatches(t, dataDir, status)
	require.True(t, verifyScrapeInterval(t, baseURL, "15s"))

	writeCfg("global:\n  scrape_interval: [\n")
	require.Equal(t, http.StatusInternalServerError, postReload(t, baseURL))
	status = getReloadStatus(t, baseURL)
	require.False(t, status.LastReloadSuccessful)
	require.Equal(t, apiv1.ReloadErrorLoad, status.ErrorCategory)
	require.False(t, status.RollbackAttempted)
	require.Empty(t, status.AppliedReloaders)
	require.True(t, verifyScrapeInterval(t, baseURL, "15s"))

	writeCfg("global:\n  scrape_interval: 45s\n  query_log_file: missing-dir/query.log\n")
	require.Equal(t, http.StatusInternalServerError, postReload(t, baseURL))
	status = getReloadStatus(t, baseURL)
	require.Equal(t, apiv1.ReloadErrorApply, status.ErrorCategory)
	require.Equal(t, "query_engine", status.FailedReloader)
	require.True(t, status.RollbackAttempted)
	require.True(t, status.RollbackSuccessful)
	require.Equal(t, []string{"db_storage", "remote_storage", "web_handler"}, status.AppliedReloaders)
	require.Contains(t, status.ReloaderTimingsMS, "query_engine")
	require.NotContains(t, status.ReloaderTimingsMS, "scrape")
	require.True(t, verifyScrapeInterval(t, baseURL, "15s"))
	require.NotContains(t, configYAML(t, baseURL), "query_log_file")
	assertStatusFileMatches(t, dataDir, status)
	persistedID := status.LastReloadID

	// The outcome survives a restart, and startup itself is not a new attempt.
	require.NoError(t, prom.Process.Kill())
	_ = prom.Wait()
	writeCfg("global:\n  scrape_interval: 15s\n")

	port = testutil.RandomUnprivilegedPort(t)
	prom = prometheusCommandWithLogging(
		t,
		configFile,
		port,
		"--storage.tsdb.path="+dataDir,
		"--web.enable-lifecycle",
		"--enable-feature=transactional-reload-config",
	)
	require.NoError(t, prom.Start())
	baseURL = "http://127.0.0.1:" + strconv.Itoa(port)
	waitReady(t, baseURL)
	status = getReloadStatus(t, baseURL)
	require.Equal(t, persistedID, status.LastReloadID)
	require.Equal(t, apiv1.ReloadErrorApply, status.ErrorCategory)
	require.Equal(t, "query_engine", status.FailedReloader)
}

func apiEmptyStatus() apiv1.ReloadStatus {
	return apiv1.EmptyReloadStatus()
}

func assertStatusFileMatches(t *testing.T, dir string, status apiv1.ReloadStatus) {
	t.Helper()
	buf, err := os.ReadFile(filepath.Join(dir, web.ReloadStatusFileName))
	require.NoError(t, err)
	var persisted apiv1.ReloadStatus
	require.NoError(t, json.Unmarshal(buf, &persisted))
	require.Equal(t, status.LastReloadID, persisted.LastReloadID)
	require.Equal(t, status.LastReloadSuccessful, persisted.LastReloadSuccessful)
	require.Equal(t, status.ErrorCategory, persisted.ErrorCategory)
	require.Equal(t, status.ErrorMessage, persisted.ErrorMessage)
	require.Equal(t, status.AppliedReloaders, persisted.AppliedReloaders)
	require.Equal(t, status.RollbackAttempted, persisted.RollbackAttempted)
	require.Equal(t, status.RollbackSuccessful, persisted.RollbackSuccessful)
	require.Equal(t, status.FailedReloader, persisted.FailedReloader)
	require.Equal(t, status.ReloaderTimingsMS, persisted.ReloaderTimingsMS)
}

func waitReady(t *testing.T, baseURL string) {
	t.Helper()
	require.Eventually(t, func() bool {
		resp, err := http.Get(baseURL + "/-/ready")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 15*time.Second, 100*time.Millisecond, "Prometheus didn't become ready in time")
}

func postReload(t *testing.T, baseURL string) int {
	t.Helper()
	resp, err := http.Post(baseURL+"/-/reload", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func getJSON(t *testing.T, url string, dest any) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(dest))
}

func getReloadStatus(t *testing.T, baseURL string) apiv1.ReloadStatus {
	t.Helper()
	var body struct {
		Status string             `json:"status"`
		Data   apiv1.ReloadStatus `json:"data"`
	}
	getJSON(t, baseURL+"/api/v1/status/reload", &body)
	require.Equal(t, "success", body.Status)
	return body.Data
}

func configYAML(t *testing.T, baseURL string) string {
	t.Helper()
	var body struct {
		Data struct {
			YAML string `json:"yaml"`
		} `json:"data"`
	}
	getJSON(t, baseURL+"/api/v1/status/config", &body)
	return body.Data.YAML
}
