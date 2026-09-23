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
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/util/testutil"
	api_v1 "github.com/prometheus/prometheus/web/api/v1"
)

type recordingReloader struct {
	name    string
	applied []string
	failOn  func(*config.Config) bool
}

func (r *recordingReloader) reloader() reloader {
	return reloader{name: r.name, reloader: func(c *config.Config) error {
		if r.failOn != nil && r.failOn(c) {
			return errors.New(r.name + " failed")
		}
		r.applied = append(r.applied, c.GlobalConfig.EvaluationInterval.String())
		return nil
	}}
}

func writeConfig(t *testing.T, path, evalInterval string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("global:\n  evaluation_interval: "+evalInterval+"\n"), 0o666))
}

func readPersisted(t *testing.T, dir string) api_v1.ReloadStatus {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, reloadStatusFilename))
	require.NoError(t, err)
	var s api_v1.ReloadStatus
	require.NoError(t, json.Unmarshal(b, &s))
	return s
}

func TestTransactionalReload(t *testing.T) {
	logger := promslog.NewNopLogger()
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "prometheus.yml")
	nsi := &safePromQLNoStepSubqueryInterval{}

	failNew := func(c *config.Config) bool { return c.GlobalConfig.EvaluationInterval.String() == "2m" }
	a := &recordingReloader{name: "a"}
	b := &recordingReloader{name: "b", failOn: failNew}
	c := &recordingReloader{name: "c"}
	rls := []reloader{a.reloader(), b.reloader(), c.reloader()}

	tracker := newReloadStatusTracker(dir, logger)
	txn := newTransactionalReload(tracker)
	require.Equal(t, api_v1.DefaultReloadStatus(), tracker.Get())

	// Initial load is not recorded but establishes the last known-good config.
	writeConfig(t, cfgFile, "1m")
	require.NoError(t, txn.reload(cfgFile, false, logger, nsi, func(bool) {}, false, rls...))
	_, err := os.Stat(filepath.Join(dir, reloadStatusFilename))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Equal(t, api_v1.DefaultReloadStatus(), tracker.Get())

	// Apply failure after a successful component triggers a rollback to the startup config.
	writeConfig(t, cfgFile, "2m")
	err = txn.reload(cfgFile, false, logger, nsi, func(bool) {}, true, rls...)
	require.Error(t, err)
	s := tracker.Get()
	require.False(t, s.LastReloadSuccessful)
	_, perr := time.Parse(time.RFC3339, s.LastReloadID)
	require.NoError(t, perr)
	require.Equal(t, api_v1.ReloadErrorCategoryApply, s.ErrorCategory)
	require.Equal(t, "b", s.FailedReloader)
	require.Equal(t, []string{"a"}, s.AppliedReloaders)
	require.True(t, s.RollbackAttempted)
	require.True(t, s.RollbackSuccessful)
	require.Contains(t, s.ReloaderTimingsMs, "a")
	require.Contains(t, s.ReloaderTimingsMs, "b")
	require.NotContains(t, s.ReloaderTimingsMs, "c")
	require.Equal(t, []string{"1m", "2m", "1m"}, a.applied)
	require.Equal(t, []string{"1m", "1m"}, b.applied)
	require.Equal(t, []string{"1m"}, c.applied)
	require.Equal(t, s, readPersisted(t, dir))

	// Load errors don't attempt a rollback.
	require.NoError(t, os.WriteFile(cfgFile, []byte("global: [\n"), 0o666))
	require.Error(t, txn.reload(cfgFile, false, logger, nsi, func(bool) {}, true, rls...))
	s = tracker.Get()
	require.Equal(t, api_v1.ReloadErrorCategoryLoad, s.ErrorCategory)
	require.False(t, s.RollbackAttempted)
	require.Empty(t, s.AppliedReloaders)
	require.NotEmpty(t, s.ErrorMessage)

	// Successful reload.
	writeConfig(t, cfgFile, "3m")
	require.NoError(t, txn.reload(cfgFile, false, logger, nsi, func(bool) {}, true, rls...))
	s = tracker.Get()
	require.True(t, s.LastReloadSuccessful)
	require.Equal(t, api_v1.ReloadErrorCategoryNone, s.ErrorCategory)
	require.Equal(t, []string{"a", "b", "c"}, s.AppliedReloaders)
	require.Len(t, s.ReloaderTimingsMs, 3)

	// A failing rollback is reported as rollback_error.
	b.failOn = func(*config.Config) bool { return true }
	writeConfig(t, cfgFile, "4m")
	require.Error(t, txn.reload(cfgFile, false, logger, nsi, func(bool) {}, true, rls...))
	s = tracker.Get()
	require.Equal(t, api_v1.ReloadErrorCategoryRollback, s.ErrorCategory)
	require.True(t, s.RollbackAttempted)
	require.False(t, s.RollbackSuccessful)
	require.Equal(t, []string{"1m", "2m", "1m", "3m", "4m", "3m"}, a.applied)

	// The persisted state survives a restart.
	require.Equal(t, s, newReloadStatusTracker(dir, logger).Get())
}

func TestTransactionalReloadFirstReloaderFails(t *testing.T) {
	logger := promslog.NewNopLogger()
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "prometheus.yml")
	nsi := &safePromQLNoStepSubqueryInterval{}

	a := &recordingReloader{name: "a"}
	b := &recordingReloader{name: "b"}
	txn := newTransactionalReload(newReloadStatusTracker(dir, logger))

	writeConfig(t, cfgFile, "1m")
	require.NoError(t, txn.reload(cfgFile, false, logger, nsi, func(bool) {}, false, a.reloader(), b.reloader()))

	a.failOn = func(*config.Config) bool { return true }
	writeConfig(t, cfgFile, "2m")
	require.Error(t, txn.reload(cfgFile, false, logger, nsi, func(bool) {}, true, a.reloader(), b.reloader()))
	s := txn.tracker.Get()
	require.Equal(t, api_v1.ReloadErrorCategoryApply, s.ErrorCategory)
	require.Equal(t, "a", s.FailedReloader)
	require.False(t, s.RollbackAttempted)
	require.Equal(t, []string{"1m"}, b.applied)
}

func TestReloadStatusTrackerCorruptedState(t *testing.T) {
	logger := promslog.NewNopLogger()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, reloadStatusFilename), []byte("{not json"), 0o666))

	tracker := newReloadStatusTracker(dir, logger)
	require.Equal(t, api_v1.DefaultReloadStatus(), tracker.Get())

	tracker.record(api_v1.ReloadStatus{LastReloadID: "x", LastReloadSuccessful: true})
	s := readPersisted(t, dir)
	require.Equal(t, "x", s.LastReloadID)
	require.Equal(t, api_v1.ReloadErrorCategoryNone, s.ErrorCategory)
}

func TestReloadStatusTrackerMissingDir(t *testing.T) {
	tracker := newReloadStatusTracker(filepath.Join(t.TempDir(), "does", "not", "exist"), promslog.NewNopLogger())
	require.Equal(t, api_v1.DefaultReloadStatus(), tracker.Get())
}

func TestTransactionalReloadEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	t.Parallel()

	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	cfgFile := filepath.Join(dir, "prometheus.yml")
	ruleFile := filepath.Join(dir, "rules.yml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("global:\n  scrape_interval: 30s\n"), 0o644))
	require.NoError(t, os.WriteFile(ruleFile, []byte("groups: [\n"), 0o644))

	start := func() (*http.Client, string, func()) {
		port := testutil.RandomUnprivilegedPort(t)
		prom := prometheusCommandWithLogging(t, cfgFile, port,
			"--storage.tsdb.path="+dataDir,
			"--web.enable-lifecycle",
			"--enable-feature=transactional-reload-config",
		)
		require.NoError(t, prom.Start())
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
		require.Eventually(t, func() bool {
			resp, err := http.Get(baseURL + "/-/ready")
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}, 10*time.Second, 100*time.Millisecond)
		return http.DefaultClient, baseURL, func() {
			_ = prom.Process.Kill()
			_, _ = prom.Process.Wait()
		}
	}

	getStatus := func(baseURL string) api_v1.ReloadStatus {
		resp, err := http.Get(baseURL + "/api/v1/status/reload")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var body struct {
			Status string              `json:"status"`
			Data   api_v1.ReloadStatus `json:"data"`
		}
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &body))
		require.Equal(t, "success", body.Status)
		return body.Data
	}

	client, baseURL, stop := start()

	resp, err := http.Get(baseURL + "/api/v1/features")
	require.NoError(t, err)
	var feats struct {
		Data map[string]map[string]bool `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&feats))
	resp.Body.Close()
	require.True(t, feats.Data["prometheus"]["transactional_reload_config"])

	require.Equal(t, api_v1.DefaultReloadStatus(), getStatus(baseURL))
	_, err = os.Stat(filepath.Join(dataDir, reloadStatusFilename))
	require.ErrorIs(t, err, os.ErrNotExist)

	// The rules reloader runs after most other components, so a broken rule file forces a rollback.
	require.NoError(t, os.WriteFile(cfgFile, []byte("global:\n  scrape_interval: 15s\nrule_files: ["+ruleFile+"]\n"), 0o644))
	resp, err = client.Post(baseURL+"/-/reload", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	s := getStatus(baseURL)
	require.False(t, s.LastReloadSuccessful)
	require.Equal(t, api_v1.ReloadErrorCategoryApply, s.ErrorCategory)
	require.Equal(t, "rules", s.FailedReloader)
	require.Contains(t, s.AppliedReloaders, "scrape")
	require.True(t, s.RollbackAttempted)
	require.True(t, s.RollbackSuccessful)
	require.Contains(t, s.ReloaderTimingsMs, "rules")
	_, err = time.Parse(time.RFC3339, s.LastReloadID)
	require.NoError(t, err)

	stop()

	// A restart with a valid config keeps reporting the persisted outcome.
	require.NoError(t, os.WriteFile(cfgFile, []byte("global:\n  scrape_interval: 30s\n"), 0o644))
	_, baseURL, stop = start()
	defer stop()
	require.Equal(t, s, getStatus(baseURL))
}
