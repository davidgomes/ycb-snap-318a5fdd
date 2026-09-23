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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/util/testutil"
)

func TestTransactionalReloadHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	storageDir := t.TempDir()
	configDir := t.TempDir()
	configFile := filepath.Join(configDir, "prometheus.yml")
	require.NoError(t, os.WriteFile(configFile, []byte("global:\n  scrape_interval: 30s\n"), 0o644))

	port := testutil.RandomUnprivilegedPort(t)
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	stop := startTransactionalPrometheus(t, configFile, storageDir, port)
	waitReady(t, baseURL)

	statusPath := filepath.Join(storageDir, reloadStatusFilename)
	_, err := os.Stat(statusPath)
	require.True(t, os.IsNotExist(err), "startup must not write a reload outcome")

	initial := getReloadStatus(t, baseURL)
	require.Equal(t, "success", initial.Status)
	require.Equal(t, "", initial.Data.LastReloadID)
	require.False(t, initial.Data.LastReloadSuccessful)
	require.Equal(t, "none", initial.Data.ErrorCategory)
	require.Empty(t, initial.Data.AppliedReloaders)
	require.Empty(t, initial.Data.ReloaderTimingsMS)
	require.NotNil(t, initial.Data.AppliedReloaders)
	require.NotNil(t, initial.Data.ReloaderTimingsMS)

	features := getJSON(t, baseURL+"/api/v1/features")
	require.Equal(t, true, features["data"].(map[string]any)["prometheus"].(map[string]any)["transactional_reload_config"])

	require.NoError(t, os.WriteFile(configFile, []byte("global:\n  scrape_interval: 15s\n"), 0o644))
	require.Equal(t, http.StatusOK, postReload(t, baseURL))
	require.Eventually(t, func() bool {
		return strings.Contains(getConfigYAML(t, baseURL), "scrape_interval: 15s")
	}, 5*time.Second, 100*time.Millisecond)

	success := getReloadStatus(t, baseURL)
	require.True(t, success.Data.LastReloadSuccessful)
	require.Equal(t, "none", success.Data.ErrorCategory)
	require.NotEmpty(t, success.Data.AppliedReloaders)
	require.Contains(t, success.Data.AppliedReloaders, "web_handler")
	_, parseErr := time.Parse(time.RFC3339, success.Data.LastReloadID)
	require.NoError(t, parseErr)
	require.Equal(t, success.Data, readReloadStatusFile(t, statusPath).Data)

	require.NoError(t, os.WriteFile(configFile, []byte("global:\n  scrape_interval: [\n"), 0o644))
	require.Equal(t, http.StatusInternalServerError, postReload(t, baseURL))
	require.Contains(t, getConfigYAML(t, baseURL), "scrape_interval: 15s")
	loadFailure := getReloadStatus(t, baseURL)
	require.Equal(t, "load_error", loadFailure.Data.ErrorCategory)
	require.False(t, loadFailure.Data.LastReloadSuccessful)
	require.False(t, loadFailure.Data.RollbackAttempted)
	require.Empty(t, loadFailure.Data.AppliedReloaders)
	require.NotEmpty(t, loadFailure.Data.ErrorMessage)
	require.NotEqual(t, success.Data.LastReloadID, loadFailure.Data.LastReloadID)

	queryLog := filepath.Join(storageDir, "missing-parent", "query.log")
	require.NoError(t, os.WriteFile(configFile, []byte(fmt.Sprintf("global:\n  scrape_interval: 15s\n  query_log_file: %q\n", queryLog)), 0o644))
	require.Equal(t, http.StatusInternalServerError, postReload(t, baseURL))
	yamlAfterRollback := getConfigYAML(t, baseURL)
	require.Contains(t, yamlAfterRollback, "scrape_interval: 15s")
	require.NotContains(t, yamlAfterRollback, "query_log_file")
	applyFailure := getReloadStatus(t, baseURL)
	require.Equal(t, "apply_error", applyFailure.Data.ErrorCategory)
	require.Equal(t, "query_engine", applyFailure.Data.FailedReloader)
	require.Equal(t, []string{"db_storage", "remote_storage", "web_handler"}, applyFailure.Data.AppliedReloaders)
	require.True(t, applyFailure.Data.RollbackAttempted)
	require.True(t, applyFailure.Data.RollbackSuccessful)
	require.False(t, applyFailure.Data.LastReloadSuccessful)
	require.Contains(t, applyFailure.Data.ReloaderTimingsMS, "query_engine")
	require.NotContains(t, applyFailure.Data.ReloaderTimingsMS, "scrape")

	require.NoError(t, os.WriteFile(configFile, []byte("global:\n  scrape_interval: 15s\n"), 0o644))
	beforeRestart, err := os.ReadFile(statusPath)
	require.NoError(t, err)
	stop()

	port = testutil.RandomUnprivilegedPort(t)
	baseURL = "http://127.0.0.1:" + strconv.Itoa(port)
	startTransactionalPrometheus(t, configFile, storageDir, port)
	waitReady(t, baseURL)

	afterRestart, err := os.ReadFile(statusPath)
	require.NoError(t, err)
	require.Equal(t, string(beforeRestart), string(afterRestart))
	restored := getReloadStatus(t, baseURL)
	require.Equal(t, applyFailure.Data, restored.Data)
	require.Contains(t, getConfigYAML(t, baseURL), "scrape_interval: 15s")
}

func TestTransactionalReloadCorruptFileDoesNotBlockStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	storageDir := t.TempDir()
	configDir := t.TempDir()
	configFile := filepath.Join(configDir, "prometheus.yml")
	require.NoError(t, os.WriteFile(configFile, []byte("global:\n  scrape_interval: 30s\n"), 0o644))
	statusPath := filepath.Join(storageDir, reloadStatusFilename)
	require.NoError(t, os.WriteFile(statusPath, []byte("{not-json"), 0o644))

	port := testutil.RandomUnprivilegedPort(t)
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	startTransactionalPrometheus(t, configFile, storageDir, port)
	waitReady(t, baseURL)

	status := getReloadStatus(t, baseURL)
	require.Equal(t, "", status.Data.LastReloadID)
	require.Equal(t, "none", status.Data.ErrorCategory)
	require.False(t, status.Data.LastReloadSuccessful)

	body, err := os.ReadFile(statusPath)
	require.NoError(t, err)
	require.Equal(t, "{not-json", string(body))
}

type reloadHTTPStatus struct {
	Status string `json:"status"`
	Data   struct {
		LastReloadID         string           `json:"last_reload_id"`
		LastReloadSuccessful bool             `json:"last_reload_successful"`
		ErrorCategory        string           `json:"error_category"`
		ErrorMessage         string           `json:"error_message"`
		AppliedReloaders     []string         `json:"applied_reloaders"`
		RollbackAttempted    bool             `json:"rollback_attempted"`
		RollbackSuccessful   bool             `json:"rollback_successful"`
		FailedReloader       string           `json:"failed_reloader"`
		ReloaderTimingsMS    map[string]int64 `json:"reloader_timings_ms"`
	} `json:"data"`
}

func startTransactionalPrometheus(t *testing.T, configFile, storageDir string, port int) func() {
	t.Helper()

	stdoutPipe, stdoutWriter := io.Pipe()
	stderrPipe, stderrWriter := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		captureLogsToTLog(t, stdoutPipe)
	}()
	go func() {
		defer wg.Done()
		captureLogsToTLog(t, stderrPipe)
	}()

	args := []string{
		"-test.main",
		"--config.file=" + configFile,
		"--web.listen-address=127.0.0.1:" + strconv.Itoa(port),
		"--storage.tsdb.path=" + storageDir,
		"--web.enable-lifecycle",
		"--enable-feature=transactional-reload-config",
	}
	prom := exec.Command(promPath, args...)
	prom.Stdout = stdoutWriter
	prom.Stderr = stderrWriter
	require.NoError(t, prom.Start())

	var once sync.Once
	stop := func() {
		once.Do(func() {
			if prom.Process != nil {
				_ = prom.Process.Kill()
			}
			_ = prom.Wait()
			_ = stdoutWriter.Close()
			_ = stderrWriter.Close()
			wg.Wait()
		})
	}
	t.Cleanup(stop)
	return stop
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
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(baseURL+"/-/reload", "application/json", bytes.NewReader(nil))
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func getReloadStatus(t *testing.T, baseURL string) reloadHTTPStatus {
	t.Helper()
	body := httpGet(t, baseURL+"/api/v1/status/reload")
	var status reloadHTTPStatus
	require.NoError(t, json.Unmarshal(body, &status))
	require.Equal(t, "success", status.Status)
	if status.Data.AppliedReloaders == nil {
		t.Fatal("applied_reloaders must be an array, not null")
	}
	if status.Data.ReloaderTimingsMS == nil {
		t.Fatal("reloader_timings_ms must be an object, not null")
	}
	return status
}

func readReloadStatusFile(t *testing.T, path string) reloadHTTPStatus {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var persisted reloadHTTPStatus
	require.NoError(t, json.Unmarshal(data, &persisted.Data))
	return persisted
}

func getConfigYAML(t *testing.T, baseURL string) string {
	t.Helper()
	body := httpGet(t, baseURL+"/api/v1/status/config")
	var payload struct {
		Data struct {
			YAML string `json:"yaml"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))
	return payload.Data.YAML
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	body := httpGet(t, url)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	return payload
}

func httpGet(t *testing.T, url string) []byte {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return body
}
