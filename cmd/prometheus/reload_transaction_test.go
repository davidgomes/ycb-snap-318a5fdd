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
	"runtime/debug"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/util/testutil"
	api_v1 "github.com/prometheus/prometheus/web/api/v1"
)

func TestTransactionalReloadFeatureFlag(t *testing.T) {
	cfg := &flagConfig{}
	cfg.featureList = []string{"transactional-reload-config"}
	require.NoError(t, cfg.setFeatureListOptions(promslog.NewNopLogger()))
	require.True(t, cfg.enableTransactionalReload)

	cfg = &flagConfig{}
	cfg.featureList = []string{"exemplar-storage,auto-reload-config"}
	require.NoError(t, cfg.setFeatureListOptions(promslog.NewNopLogger()))
	require.False(t, cfg.enableTransactionalReload)
}

func TestTransactionalReloadOutcomes(t *testing.T) {
	restoreRuntime(t)

	dir := t.TempDir()
	storageDir := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(storageDir, 0o755))
	cfgPath := filepath.Join(dir, "prometheus.yml")
	writeReloadConfig(t, cfgPath, "15s")

	logger := promslog.NewNopLogger()
	fixed := time.Date(2026, 9, 23, 9, 8, 0, 0, time.UTC)
	coord := newReloadCoordinator(true, storageDir, logger)
	coord.now = func() time.Time { return fixed }
	noStep := &safePromQLNoStepSubqueryInterval{}

	var startup *config.Config
	var secondCalls int
	reloaders := []reloader{
		{
			name: "first",
			reloader: func(cfg *config.Config) error {
				if startup == nil {
					startup = cfg
				}
				return nil
			},
		},
		{
			name: "second",
			reloader: func(cfg *config.Config) error {
				secondCalls++
				if startup != nil && cfg != startup {
					return errors.New("second failed")
				}
				return nil
			},
		},
	}

	require.NoError(t, reloadConfig(cfgPath, false, logger, noStep, func(bool) {}, coord, false, reloaders...))
	_, err := os.Stat(api_v1.ReloadStatusPath(storageDir))
	require.True(t, os.IsNotExist(err))
	require.Equal(t, api_v1.EmptyReloadStatus(), coord.Status())
	require.Equal(t, 1, secondCalls)

	// Load/parse failure does not roll back and does not call reloaders.
	err = reloadConfig(filepath.Join(dir, "missing.yml"), false, logger, noStep, func(bool) {}, coord, true, reloaders...)
	require.Error(t, err)
	require.Equal(t, 1, secondCalls)
	status := coord.Status()
	require.Equal(t, fixed.Format(time.RFC3339), status.LastReloadID)
	require.False(t, status.LastReloadSuccessful)
	require.Equal(t, api_v1.ReloadErrorLoad, status.ErrorCategory)
	require.NotEmpty(t, status.ErrorMessage)
	require.Empty(t, status.AppliedReloaders)
	require.False(t, status.RollbackAttempted)
	require.False(t, status.RollbackSuccessful)
	require.Empty(t, status.FailedReloader)
	assertPersistedReloadStatus(t, storageDir, status)

	// The first reloader fails: nothing was applied, so do not roll back.
	reloaders[0].reloader = func(*config.Config) error { return errors.New("first failed") }
	err = reloadConfig(cfgPath, false, logger, noStep, func(bool) {}, coord, true, reloaders...)
	require.Error(t, err)
	require.Equal(t, 1, secondCalls)
	status = coord.Status()
	require.Equal(t, api_v1.ReloadErrorApply, status.ErrorCategory)
	require.Equal(t, "first", status.FailedReloader)
	require.Empty(t, status.AppliedReloaders)
	require.False(t, status.RollbackAttempted)
	require.Contains(t, status.ReloaderTimingsMS, "first")
	_, sawSecond := status.ReloaderTimingsMS["second"]
	require.False(t, sawSecond)

	// A later failure rolls back to the startup configuration.
	reloaders[0].reloader = func(cfg *config.Config) error {
		if startup == nil {
			startup = cfg
		}
		return nil
	}
	writeReloadConfig(t, cfgPath, "30s")
	err = reloadConfig(cfgPath, false, logger, noStep, func(bool) {}, coord, true, reloaders...)
	require.Error(t, err)
	status = coord.Status()
	require.Equal(t, api_v1.ReloadErrorApply, status.ErrorCategory)
	require.Equal(t, []string{"first"}, status.AppliedReloaders)
	require.Equal(t, "second", status.FailedReloader)
	require.True(t, status.RollbackAttempted)
	require.True(t, status.RollbackSuccessful)
	require.False(t, status.LastReloadSuccessful)
	require.Contains(t, status.ErrorMessage, "second failed")
	require.Contains(t, status.ReloaderTimingsMS, "first")
	require.Contains(t, status.ReloaderTimingsMS, "second")
	assertPersistedReloadStatus(t, storageDir, status)

	// last known-good is still the startup config, so another partial apply rolls back successfully.
	writeReloadConfig(t, cfgPath, "45s")
	err = reloadConfig(cfgPath, false, logger, noStep, func(bool) {}, coord, true, reloaders...)
	require.Error(t, err)
	require.True(t, coord.Status().RollbackSuccessful)
	require.Equal(t, api_v1.ReloadErrorApply, coord.Status().ErrorCategory)

	// Rollback failure is recorded as rollback_error and does not replace the known-good config.
	reloaders[0].reloader = func(cfg *config.Config) error {
		if cfg == startup {
			return errors.New("rollback broke")
		}
		return nil
	}
	err = reloadConfig(cfgPath, false, logger, noStep, func(bool) {}, coord, true, reloaders...)
	require.Error(t, err)
	status = coord.Status()
	require.Equal(t, api_v1.ReloadErrorRollback, status.ErrorCategory)
	require.True(t, status.RollbackAttempted)
	require.False(t, status.RollbackSuccessful)
	require.Contains(t, status.ErrorMessage, "rollback failed")
	require.Contains(t, status.ErrorMessage, "rollback broke")

	// A fully successful reload becomes the new known-good config.
	var reloaded *config.Config
	reloaders[0].reloader = func(*config.Config) error { return nil }
	reloaders[1].reloader = func(cfg *config.Config) error {
		reloaded = cfg
		return nil
	}
	err = reloadConfig(cfgPath, false, logger, noStep, func(bool) {}, coord, true, reloaders...)
	require.NoError(t, err)
	status = coord.Status()
	require.True(t, status.LastReloadSuccessful)
	require.Equal(t, api_v1.ReloadErrorNone, status.ErrorCategory)
	require.Equal(t, []string{"first", "second"}, status.AppliedReloaders)
	require.False(t, status.RollbackAttempted)
	require.Empty(t, status.FailedReloader)
	require.Empty(t, status.ErrorMessage)
	require.NotNil(t, reloaded)

	reloaders[1].reloader = func(cfg *config.Config) error {
		if cfg != reloaded {
			return errors.New("second failed")
		}
		return nil
	}
	writeReloadConfig(t, cfgPath, "60s")
	err = reloadConfig(cfgPath, false, logger, noStep, func(bool) {}, coord, true, reloaders...)
	require.Error(t, err)
	require.True(t, coord.Status().RollbackSuccessful)
	require.Equal(t, api_v1.ReloadErrorApply, coord.Status().ErrorCategory)

	// Restart sees the persisted outcome, including when that file is corrupt.
	restarted := newReloadCoordinator(true, storageDir, logger)
	require.NoError(t, restarted.loadPersisted())
	require.Equal(t, api_v1.ReloadErrorApply, restarted.Status().ErrorCategory)
	require.NotEmpty(t, restarted.Status().LastReloadID)

	require.NoError(t, os.WriteFile(api_v1.ReloadStatusPath(storageDir), []byte("{"), 0o644))
	corrupt := newReloadCoordinator(true, storageDir, logger)
	require.Error(t, corrupt.loadPersisted())
	require.Equal(t, api_v1.EmptyReloadStatus(), corrupt.Status())
}

func TestReloadWithoutTransactionalModeKeepsLegacyBehavior(t *testing.T) {
	restoreRuntime(t)

	dir := t.TempDir()
	storageDir := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(storageDir, 0o755))
	cfgPath := filepath.Join(dir, "prometheus.yml")
	writeReloadConfig(t, cfgPath, "15s")

	logger := promslog.NewNopLogger()
	coord := newReloadCoordinator(false, storageDir, logger)
	noStep := &safePromQLNoStepSubqueryInterval{}
	var calls []string
	reloaders := []reloader{
		{name: "first", reloader: func(*config.Config) error {
			calls = append(calls, "first")
			return errors.New("first failed")
		}},
		{name: "second", reloader: func(*config.Config) error {
			calls = append(calls, "second")
			return nil
		}},
	}

	err := reloadConfig(cfgPath, false, logger, noStep, func(bool) {}, coord, true, reloaders...)
	require.Error(t, err)
	require.Equal(t, []string{"first", "second"}, calls)
	_, statErr := os.Stat(api_v1.ReloadStatusPath(storageDir))
	require.True(t, os.IsNotExist(statErr))
	require.Equal(t, api_v1.EmptyReloadStatus(), coord.Status())
}

func TestTransactionalReloadHTTPStatusSurvivesRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	configDir := t.TempDir()
	storageDir := filepath.Join(configDir, "data")
	configFilePath := filepath.Join(configDir, "prometheus.yml")
	writeReloadConfig(t, configFilePath, "15s")

	port := testutil.RandomUnprivilegedPort(t)
	args := transactionalReloadArgs(storageDir)
	prom := prometheusCommandWithLogging(t, configFilePath, port, args...)
	require.NoError(t, prom.Start())
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	waitPrometheusReady(t, baseURL)

	status := getReloadStatus(t, baseURL)
	require.Equal(t, "", status.LastReloadID)
	require.False(t, status.LastReloadSuccessful)
	require.Equal(t, "none", status.ErrorCategory)
	require.Empty(t, status.AppliedReloaders)
	require.NotNil(t, status.ReloaderTimingsMS)
	require.Empty(t, status.ReloaderTimingsMS)
	_, err := os.Stat(api_v1.ReloadStatusPath(storageDir))
	require.True(t, os.IsNotExist(err))

	body := getJSON(t, baseURL+"/api/v1/features")
	var features struct {
		Data map[string]map[string]bool `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &features))
	require.True(t, features.Data["prometheus"]["transactional_reload_config"])

	writeReloadConfig(t, configFilePath, "30s")
	require.Equal(t, http.StatusOK, postReload(t, baseURL))
	status = getReloadStatus(t, baseURL)
	require.True(t, status.LastReloadSuccessful)
	require.Equal(t, "none", status.ErrorCategory)
	require.NotEmpty(t, status.LastReloadID)
	_, err = time.Parse(time.RFC3339, status.LastReloadID)
	require.NoError(t, err)
	require.Contains(t, status.AppliedReloaders, "db_storage")
	require.Contains(t, status.ReloaderTimingsMS, "db_storage")
	require.False(t, status.RollbackAttempted)
	persisted, loadErr := api_v1.LoadReloadStatus(storageDir)
	require.NoError(t, loadErr)
	require.Equal(t, status.LastReloadID, persisted.LastReloadID)
	require.True(t, persisted.LastReloadSuccessful)

	require.NoError(t, os.WriteFile(configFilePath, []byte("global:\n  scrape_interval: 15s\nnot: [valid\n"), 0o644))
	require.Equal(t, http.StatusInternalServerError, postReload(t, baseURL))
	status = getReloadStatus(t, baseURL)
	require.False(t, status.LastReloadSuccessful)
	require.Equal(t, "load_error", status.ErrorCategory)
	require.NotEmpty(t, status.ErrorMessage)
	require.Empty(t, status.AppliedReloaders)
	require.False(t, status.RollbackAttempted)
	require.False(t, status.RollbackSuccessful)
	require.Contains(t, getConfigYAML(t, baseURL), "scrape_interval: 30s")
	failedID := status.LastReloadID

	// Restart from a valid file. Startup must not overwrite the failed outcome.
	writeReloadConfig(t, configFilePath, "30s")
	require.NoError(t, prom.Process.Kill())
	_ = prom.Wait()

	port2 := testutil.RandomUnprivilegedPort(t)
	prom2 := prometheusCommandWithLogging(t, configFilePath, port2, args...)
	require.NoError(t, prom2.Start())
	baseURL2 := "http://127.0.0.1:" + strconv.Itoa(port2)
	waitPrometheusReady(t, baseURL2)
	status = getReloadStatus(t, baseURL2)
	require.Equal(t, failedID, status.LastReloadID)
	require.Equal(t, "load_error", status.ErrorCategory)
	require.False(t, status.LastReloadSuccessful)
}

func restoreRuntime(t *testing.T) {
	t.Helper()
	oldGC := debug.SetGCPercent(100)
	oldEnv, had := os.LookupEnv("GOGC")
	t.Cleanup(func() {
		debug.SetGCPercent(oldGC)
		if had {
			os.Setenv("GOGC", oldEnv)
		} else {
			os.Unsetenv("GOGC")
		}
	})
}

func writeReloadConfig(t *testing.T, path, interval string) {
	t.Helper()
	body := "global:\n  scrape_interval: " + interval + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func assertPersistedReloadStatus(t *testing.T, dir string, want api_v1.ReloadStatus) {
	t.Helper()
	got, err := api_v1.LoadReloadStatus(dir)
	require.NoError(t, err)
	require.Equal(t, want.LastReloadID, got.LastReloadID)
	require.Equal(t, want.LastReloadSuccessful, got.LastReloadSuccessful)
	require.Equal(t, want.ErrorCategory, got.ErrorCategory)
	require.Equal(t, want.ErrorMessage, got.ErrorMessage)
	require.Equal(t, want.AppliedReloaders, got.AppliedReloaders)
	require.Equal(t, want.RollbackAttempted, got.RollbackAttempted)
	require.Equal(t, want.RollbackSuccessful, got.RollbackSuccessful)
	require.Equal(t, want.FailedReloader, got.FailedReloader)
}

func transactionalReloadArgs(storageDir string) []string {
	return []string{
		"--storage.tsdb.path=" + storageDir,
		"--enable-feature=transactional-reload-config",
		"--web.enable-lifecycle",
	}
}

func waitPrometheusReady(t *testing.T, baseURL string) {
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

func getReloadStatus(t *testing.T, baseURL string) api_v1.ReloadStatus {
	t.Helper()
	body := getJSON(t, baseURL+"/api/v1/status/reload")
	var resp struct {
		Status string              `json:"status"`
		Data   api_v1.ReloadStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, "success", resp.Status)
	if resp.Data.AppliedReloaders == nil {
		resp.Data.AppliedReloaders = []string{}
	}
	if resp.Data.ReloaderTimingsMS == nil {
		resp.Data.ReloaderTimingsMS = map[string]int64{}
	}
	return resp.Data
}

func getConfigYAML(t *testing.T, baseURL string) string {
	t.Helper()
	body := getJSON(t, baseURL+"/api/v1/status/config")
	var resp struct {
		Data struct {
			YAML string `json:"yaml"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Data.YAML
}

func getJSON(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return body
}

func postReload(t *testing.T, baseURL string) int {
	t.Helper()
	resp, err := http.Post(baseURL+"/-/reload", "", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}
