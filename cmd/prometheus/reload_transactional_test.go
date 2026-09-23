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
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/util/reloadstatus"
	"github.com/prometheus/prometheus/util/testutil"
)

// txnReloadTest drives a transactionalReloader with fake reloaders. Every
// configuration is identified by its global scrape interval.
type txnReloadTest struct {
	t          *testing.T
	dataDir    string
	configFile string
	store      *reloadstatus.Store
	reloader   *transactionalReloader
	rls        []reloader

	// calls records every reloader invocation as "<reloader>@<scrape interval>".
	calls []string
	// fail makes a reloader fail when applying the configuration with the
	// given scrape interval.
	fail map[string]string
	// results records the values passed to the reload callback.
	results []bool
}

func newTxnReloadTest(t *testing.T, names ...string) *txnReloadTest {
	dir := t.TempDir()
	tt := &txnReloadTest{
		t:          t,
		dataDir:    filepath.Join(dir, "data"),
		configFile: filepath.Join(dir, "prometheus.yml"),
		fail:       map[string]string{},
	}
	tt.store = reloadstatus.NewStore(tt.dataDir, promslog.NewNopLogger())
	tt.reloader = &transactionalReloader{status: tt.store}
	for _, name := range names {
		tt.rls = append(tt.rls, reloader{
			name: name,
			reloader: func(c *config.Config) error {
				interval := c.GlobalConfig.ScrapeInterval.String()
				tt.calls = append(tt.calls, name+"@"+interval)
				if tt.fail[name] == interval {
					return errors.New(name + " failed")
				}
				return nil
			},
		})
	}
	return tt
}

func (tt *txnReloadTest) reload(scrapeInterval string) error {
	tt.t.Helper()
	tt.calls = nil
	content := "global:\n  scrape_interval: " + scrapeInterval + "\n"
	require.NoError(tt.t, os.WriteFile(tt.configFile, []byte(content), 0o644))
	return tt.reloader.reload(tt.configFile, false, promslog.NewNopLogger(), &safePromQLNoStepSubqueryInterval{}, func(ok bool) {
		tt.results = append(tt.results, ok)
	}, tt.rls...)
}

// requireStatus checks that the recorded status matches expected, ignoring the
// reload ID and timing values, and that it has been persisted.
func (tt *txnReloadTest) requireStatus(expected reloadstatus.Status, timedReloaders ...string) reloadstatus.Status {
	tt.t.Helper()
	st := tt.store.Get()
	require.Equal(tt.t, st, reloadstatus.NewStore(tt.dataDir, promslog.NewNopLogger()).Get(), "persisted status differs")

	_, err := time.Parse(time.RFC3339, st.LastReloadID)
	require.NoError(tt.t, err)
	require.Equal(tt.t, timedReloaders, slices.Sorted(maps.Keys(st.ReloaderTimingsMs)))

	actual := st
	actual.LastReloadID = ""
	actual.ReloaderTimingsMs = map[string]int64{}
	require.Equal(tt.t, expected, actual)
	return st
}

func TestTransactionalReloadInitialLoadIsNotRecorded(t *testing.T) {
	tt := newTxnReloadTest(t, "a", "b")

	require.NoError(t, tt.reload("1m"))
	require.Equal(t, []string{"a@1m", "b@1m"}, tt.calls)
	require.Equal(t, reloadstatus.NewStatus(""), tt.store.Get())
	require.NoFileExists(t, filepath.Join(tt.dataDir, reloadstatus.Filename))
}

func TestTransactionalReloadInitialLoadFailureIsNotRecorded(t *testing.T) {
	tt := newTxnReloadTest(t, "a", "b", "c")
	tt.fail["b"] = "1m"

	require.EqualError(t, tt.reload("1m"), `reloader "b" failed to apply the new configuration (--config.file="`+tt.configFile+`"): b failed`)
	require.Equal(t, []string{"a@1m", "b@1m"}, tt.calls, "no later reloader must run and nothing can be rolled back")
	require.Equal(t, reloadstatus.NewStatus(""), tt.store.Get())
	require.NoFileExists(t, filepath.Join(tt.dataDir, reloadstatus.Filename))
}

func TestTransactionalReloadSuccess(t *testing.T) {
	tt := newTxnReloadTest(t, "a", "b", "c")
	require.NoError(t, tt.reload("1m"))

	require.NoError(t, tt.reload("2m"))
	require.Equal(t, []string{"a@2m", "b@2m", "c@2m"}, tt.calls)
	require.Equal(t, []bool{true, true}, tt.results)

	expected := reloadstatus.NewStatus("")
	expected.LastReloadSuccessful = true
	expected.AppliedReloaders = []string{"a", "b", "c"}
	first := tt.requireStatus(expected, "a", "b", "c")

	time.Sleep(time.Millisecond)
	require.NoError(t, tt.reload("3m"))
	second := tt.requireStatus(expected, "a", "b", "c")
	require.NotEqual(t, first.LastReloadID, second.LastReloadID)
}

func TestTransactionalReloadLoadError(t *testing.T) {
	tt := newTxnReloadTest(t, "a", "b")
	require.NoError(t, tt.reload("1m"))

	err := tt.reload("not-a-duration")
	require.ErrorContains(t, err, "couldn't load configuration")
	require.Empty(t, tt.calls, "no reloader must run and nothing must be rolled back")
	require.Equal(t, []bool{true, false}, tt.results)

	expected := reloadstatus.NewStatus("")
	expected.ErrorCategory = reloadstatus.ErrorCategoryLoad
	expected.ErrorMessage = err.Error()
	tt.requireStatus(expected)
}

func TestTransactionalReloadFirstReloaderFails(t *testing.T) {
	tt := newTxnReloadTest(t, "a", "b")
	require.NoError(t, tt.reload("1m"))
	tt.fail["a"] = "2m"

	err := tt.reload("2m")
	require.Error(t, err)
	require.Equal(t, []string{"a@2m"}, tt.calls, "nothing was applied, so nothing must be rolled back")

	expected := reloadstatus.NewStatus("")
	expected.ErrorCategory = reloadstatus.ErrorCategoryApply
	expected.ErrorMessage = err.Error()
	expected.FailedReloader = "a"
	tt.requireStatus(expected, "a")
}

func TestTransactionalReloadRollsBackToStartupConfig(t *testing.T) {
	tt := newTxnReloadTest(t, "a", "b", "c", "d")
	require.NoError(t, tt.reload("1m"))
	tt.fail["c"] = "2m"

	err := tt.reload("2m")
	require.EqualError(t, err, `reloader "c" failed to apply the new configuration (--config.file="`+tt.configFile+`"): c failed`)
	require.Equal(t, []string{
		"a@2m", "b@2m", "c@2m",
		"a@1m", "b@1m", "c@1m",
	}, tt.calls, "reloaders that ran must be rolled back in order and later ones must not run")
	require.Equal(t, []bool{true, false}, tt.results)

	expected := reloadstatus.NewStatus("")
	expected.ErrorCategory = reloadstatus.ErrorCategoryApply
	expected.ErrorMessage = err.Error()
	expected.AppliedReloaders = []string{"a", "b"}
	expected.RollbackAttempted = true
	expected.RollbackSuccessful = true
	expected.FailedReloader = "c"
	tt.requireStatus(expected, "a", "b", "c")
}

func TestTransactionalReloadRollsBackToLastSuccessfulReload(t *testing.T) {
	tt := newTxnReloadTest(t, "a", "b", "c")
	require.NoError(t, tt.reload("1m"))
	tt.fail["b"] = "3m"
	require.Error(t, tt.reload("3m"))

	require.NoError(t, tt.reload("2m"))
	require.Error(t, tt.reload("3m"))
	require.Equal(t, []string{"a@3m", "b@3m", "a@2m", "b@2m"}, tt.calls)

	st := tt.store.Get()
	require.Equal(t, reloadstatus.ErrorCategoryApply, st.ErrorCategory)
	require.True(t, st.RollbackSuccessful)
}

func TestTransactionalReloadRollbackError(t *testing.T) {
	tt := newTxnReloadTest(t, "a", "b", "c")
	require.NoError(t, tt.reload("1m"))
	tt.fail["b"] = "2m"
	tt.fail["a"] = "1m"

	err := tt.reload("2m")
	require.ErrorContains(t, err, `reloader "b" failed to apply the new configuration`)
	require.ErrorContains(t, err, `rollback to the last known-good configuration failed: reloader "a": a failed`)
	require.Equal(t, []string{"a@2m", "b@2m", "a@1m", "b@1m"}, tt.calls, "rollback must carry on after a failure")

	expected := reloadstatus.NewStatus("")
	expected.ErrorCategory = reloadstatus.ErrorCategoryRollback
	expected.ErrorMessage = err.Error()
	expected.AppliedReloaders = []string{"a"}
	expected.RollbackAttempted = true
	expected.FailedReloader = "b"
	tt.requireStatus(expected, "a", "b")
}

func TestTransactionalReloadPersistenceFailureDoesNotFailReload(t *testing.T) {
	tt := newTxnReloadTest(t, "a")
	require.NoError(t, tt.reload("1m"))
	require.NoError(t, os.WriteFile(tt.dataDir, nil, 0o644))

	require.NoError(t, tt.reload("2m"))
	require.True(t, tt.store.Get().LastReloadSuccessful)
}

// promInstance is a Prometheus process started from the test binary.
type promInstance struct {
	baseURL string
	cmd     *exec.Cmd
}

func startProm(t *testing.T, configFile string, args ...string) *promInstance {
	t.Helper()
	port := testutil.RandomUnprivilegedPort(t)
	cmd := prometheusCommandWithLogging(t, configFile, port, args...)
	require.NoError(t, cmd.Start())

	p := &promInstance{baseURL: "http://127.0.0.1:" + strconv.Itoa(port), cmd: cmd}
	require.Eventually(t, func() bool {
		resp, err := http.Get(p.baseURL + "/-/ready")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 100*time.Millisecond, "Prometheus didn't become ready in time")
	return p
}

func (p *promInstance) stop(t *testing.T) {
	t.Helper()
	require.NoError(t, p.cmd.Process.Kill())
	p.cmd.Wait()
}

func (p *promInstance) reload(t *testing.T) int {
	t.Helper()
	resp, err := http.Post(p.baseURL+"/-/reload", "", http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func (p *promInstance) getJSON(t *testing.T, path string, data any) {
	t.Helper()
	resp, err := http.Get(p.baseURL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body := struct {
		Status string `json:"status"`
		Data   any    `json:"data"`
	}{Data: data}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "success", body.Status)
}

func (p *promInstance) reloadStatus(t *testing.T) reloadstatus.Status {
	t.Helper()
	var st reloadstatus.Status
	p.getJSON(t, "/api/v1/status/reload", &st)
	return st
}

func (p *promInstance) transactionalReloadFeature(t *testing.T) bool {
	t.Helper()
	var features map[string]map[string]bool
	p.getJSON(t, "/api/v1/features", &features)
	enabled, ok := features["prometheus"]["transactional_reload_config"]
	require.True(t, ok, "transactional_reload_config missing from features")
	return enabled
}

func TestTransactionalReloadStatusAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	t.Parallel()

	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configFile := filepath.Join(dir, "prometheus.yml")
	statusFile := filepath.Join(dataDir, reloadstatus.Filename)
	writeConfig := func(content string) {
		require.NoError(t, os.WriteFile(configFile, []byte(content), 0o644))
	}
	args := []string{"--storage.tsdb.path=" + dataDir, "--web.enable-lifecycle", "--enable-feature=transactional-reload-config"}

	writeConfig("global:\n  scrape_interval: 30s\n")
	prom := startProm(t, configFile, args...)
	require.True(t, prom.transactionalReloadFeature(t))

	// The startup load is not a reload attempt.
	require.Equal(t, reloadstatus.NewStatus(""), prom.reloadStatus(t))
	require.NoFileExists(t, statusFile)

	// Load errors are recorded without a rollback.
	writeConfig("global:\n  scrape_interval: 15s\ninvalid_syntax\n")
	require.Equal(t, http.StatusInternalServerError, prom.reload(t))
	st := prom.reloadStatus(t)
	_, err := time.Parse(time.RFC3339, st.LastReloadID)
	require.NoError(t, err)
	require.False(t, st.LastReloadSuccessful)
	require.Equal(t, reloadstatus.ErrorCategoryLoad, st.ErrorCategory)
	require.Contains(t, st.ErrorMessage, "couldn't load configuration")
	require.Equal(t, []string{}, st.AppliedReloaders)
	require.False(t, st.RollbackAttempted)
	require.False(t, st.RollbackSuccessful)
	require.Empty(t, st.FailedReloader)
	require.Equal(t, map[string]int64{}, st.ReloaderTimingsMs)
	require.Equal(t, st, reloadstatus.NewStore(dataDir, nil).Get())

	// The query engine can't open a query log in a missing directory, after
	// the storage and web handler reloaders applied the new configuration.
	queryLog := filepath.Join(dir, "missing", "query.log")
	writeConfig("global:\n  scrape_interval: 13s\n  query_log_file: " + queryLog + "\n")
	require.Equal(t, http.StatusInternalServerError, prom.reload(t))
	applyErr := prom.reloadStatus(t)
	require.NotEqual(t, st.LastReloadID, applyErr.LastReloadID)
	require.False(t, applyErr.LastReloadSuccessful)
	require.Equal(t, reloadstatus.ErrorCategoryApply, applyErr.ErrorCategory)
	require.Contains(t, applyErr.ErrorMessage, queryLog)
	require.Equal(t, []string{"db_storage", "remote_storage", "web_handler"}, applyErr.AppliedReloaders)
	require.True(t, applyErr.RollbackAttempted)
	require.True(t, applyErr.RollbackSuccessful)
	require.Equal(t, "query_engine", applyErr.FailedReloader)
	require.Equal(t, []string{"db_storage", "query_engine", "remote_storage", "web_handler"}, slices.Sorted(maps.Keys(applyErr.ReloaderTimingsMs)))
	require.True(t, verifyScrapeInterval(t, prom.baseURL, "30s"), "web handler wasn't rolled back to the startup configuration")

	raw, err := os.ReadFile(statusFile)
	require.NoError(t, err)
	var persisted map[string]any
	require.NoError(t, json.Unmarshal(raw, &persisted))
	require.Equal(t, applyErr.LastReloadID, persisted["last_reload_id"])
	require.Equal(t, false, persisted["last_reload_successful"])
	require.Equal(t, "apply_error", persisted["error_category"])

	// The outcome survives a restart.
	prom.stop(t)
	writeConfig("global:\n  scrape_interval: 30s\n")
	prom = startProm(t, configFile, args...)
	require.Equal(t, applyErr, prom.reloadStatus(t))

	writeConfig("global:\n  scrape_interval: 20s\n")
	require.Equal(t, http.StatusOK, prom.reload(t))
	ok := prom.reloadStatus(t)
	require.NotEqual(t, applyErr.LastReloadID, ok.LastReloadID)
	require.True(t, ok.LastReloadSuccessful)
	require.Equal(t, reloadstatus.ErrorCategoryNone, ok.ErrorCategory)
	require.Empty(t, ok.ErrorMessage)
	allReloaders := []string{"db_storage", "remote_storage", "web_handler", "query_engine", "scrape", "scrape_sd", "notify", "notify_sd", "rules", "tracing"}
	require.Equal(t, allReloaders, ok.AppliedReloaders)
	require.False(t, ok.RollbackAttempted)
	require.Empty(t, ok.FailedReloader)
	require.Equal(t, slices.Sorted(slices.Values(allReloaders)), slices.Sorted(maps.Keys(ok.ReloaderTimingsMs)))
	require.True(t, verifyScrapeInterval(t, prom.baseURL, "20s"))

	// A corrupted state file doesn't prevent startup.
	prom.stop(t)
	require.NoError(t, os.WriteFile(statusFile, []byte(`{"last_reload_id": "2026-`), 0o644))
	prom = startProm(t, configFile, args...)
	require.Equal(t, reloadstatus.NewStatus(""), prom.reloadStatus(t))
}

func TestReloadStatusAPIWithoutTransactionalReload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	t.Parallel()

	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configFile := filepath.Join(dir, "prometheus.yml")
	require.NoError(t, os.WriteFile(configFile, []byte("global:\n  scrape_interval: 30s\n"), 0o644))

	prom := startProm(t, configFile, "--storage.tsdb.path="+dataDir, "--web.enable-lifecycle")
	require.False(t, prom.transactionalReloadFeature(t))

	require.NoError(t, os.WriteFile(configFile, []byte("invalid_syntax\n"), 0o644))
	require.Equal(t, http.StatusInternalServerError, prom.reload(t))
	require.Equal(t, reloadstatus.NewStatus(""), prom.reloadStatus(t))
	require.NoFileExists(t, filepath.Join(dataDir, reloadstatus.Filename))
}
