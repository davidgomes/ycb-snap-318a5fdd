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
	"github.com/prometheus/prometheus/util/reloadstatus"
	"github.com/prometheus/prometheus/util/testutil"
)

// fakeComponent records the scrape interval of the configuration it last applied.
type fakeComponent struct {
	name       string
	applied    model.Duration
	failOn     model.Duration
	failAlways bool
}

func (c *fakeComponent) reloader() reloader {
	return reloader{name: c.name, reloader: func(cfg *config.Config) error {
		if c.failAlways || (c.failOn != 0 && cfg.GlobalConfig.ScrapeInterval == c.failOn) {
			return errors.New(c.name + " failed")
		}
		c.applied = cfg.GlobalConfig.ScrapeInterval
		return nil
	}}
}

func writeScrapeIntervalConfig(t *testing.T, path string, seconds int) {
	t.Helper()
	cfg := "global:\n  scrape_interval: " + strconv.Itoa(seconds) + "s\n"
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o644))
}

func TestTransactionalReload(t *testing.T) {
	const (
		good    = model.Duration(10 * time.Second)
		next    = model.Duration(20 * time.Second)
		failing = model.Duration(30 * time.Second)
	)

	setup := func(t *testing.T, comps ...*fakeComponent) (*transactionalReloader, string, string, []reloader) {
		dir := t.TempDir()
		cfgFile := filepath.Join(dir, "prometheus.yml")
		writeScrapeIntervalConfig(t, cfgFile, 10)
		tr := &transactionalReloader{store: reloadstatus.NewStore(dir, nil)}
		var rls []reloader
		for _, c := range comps {
			rls = append(rls, c.reloader())
		}
		require.NoError(t, tr.reload(cfgFile, false, promslog.NewNopLogger(), &safePromQLNoStepSubqueryInterval{}, func(bool) {}, false, rls...))
		_, err := os.Stat(filepath.Join(dir, reloadstatus.FileName))
		require.ErrorIs(t, err, os.ErrNotExist, "initial load must not be recorded")
		require.Equal(t, reloadstatus.Initial(), tr.store.Get())
		return tr, dir, cfgFile, rls
	}
	reload := func(t *testing.T, tr *transactionalReloader, cfgFile string, rls []reloader) (bool, error) {
		var success bool
		err := tr.reload(cfgFile, false, promslog.NewNopLogger(), &safePromQLNoStepSubqueryInterval{}, func(s bool) { success = s }, true, rls...)
		return success, err
	}
	checkPersisted := func(t *testing.T, tr *transactionalReloader, dir string) reloadstatus.Status {
		st := tr.store.Get()
		require.Equal(t, st, reloadstatus.NewStore(dir, nil).Get())
		_, err := time.Parse(time.RFC3339, st.LastReloadID)
		require.NoError(t, err)
		return st
	}

	t.Run("success", func(t *testing.T) {
		a, b := &fakeComponent{name: "a"}, &fakeComponent{name: "b"}
		tr, dir, cfgFile, rls := setup(t, a, b)
		writeScrapeIntervalConfig(t, cfgFile, 20)

		success, err := reload(t, tr, cfgFile, rls)
		require.NoError(t, err)
		require.True(t, success)
		require.Equal(t, next, a.applied)
		require.Equal(t, next, b.applied)

		st := checkPersisted(t, tr, dir)
		require.True(t, st.LastReloadSuccessful)
		require.Equal(t, reloadstatus.ErrorCategoryNone, st.ErrorCategory)
		require.Empty(t, st.ErrorMessage)
		require.Equal(t, []string{"a", "b"}, st.AppliedReloaders)
		require.Empty(t, st.FailedReloader)
		require.False(t, st.RollbackAttempted)
		require.Contains(t, st.ReloaderTimingsMs, "a")
		require.Contains(t, st.ReloaderTimingsMs, "b")
		require.Equal(t, next, tr.lastGood.GlobalConfig.ScrapeInterval)
	})

	t.Run("load error does not roll back", func(t *testing.T) {
		a := &fakeComponent{name: "a"}
		tr, dir, cfgFile, rls := setup(t, a)
		require.NoError(t, os.WriteFile(cfgFile, []byte("global:\n  invalid"), 0o644))

		success, err := reload(t, tr, cfgFile, rls)
		require.Error(t, err)
		require.False(t, success)
		require.Equal(t, good, a.applied)

		st := checkPersisted(t, tr, dir)
		require.False(t, st.LastReloadSuccessful)
		require.Equal(t, reloadstatus.ErrorCategoryLoad, st.ErrorCategory)
		require.NotEmpty(t, st.ErrorMessage)
		require.Empty(t, st.AppliedReloaders)
		require.False(t, st.RollbackAttempted)
		require.Empty(t, st.FailedReloader)
	})

	t.Run("first reloader fails without rollback", func(t *testing.T) {
		a, b := &fakeComponent{name: "a", failOn: next}, &fakeComponent{name: "b"}
		tr, dir, cfgFile, rls := setup(t, a, b)
		writeScrapeIntervalConfig(t, cfgFile, 20)

		_, err := reload(t, tr, cfgFile, rls)
		require.Error(t, err)
		require.Equal(t, good, b.applied, "reloaders after the failed one must not run")

		st := checkPersisted(t, tr, dir)
		require.Equal(t, reloadstatus.ErrorCategoryApply, st.ErrorCategory)
		require.Equal(t, "a", st.FailedReloader)
		require.Empty(t, st.AppliedReloaders)
		require.False(t, st.RollbackAttempted)
		require.NotContains(t, st.ReloaderTimingsMs, "b")
	})

	t.Run("later reloader fails and rolls back to startup config", func(t *testing.T) {
		a, b, c := &fakeComponent{name: "a"}, &fakeComponent{name: "b", failOn: next}, &fakeComponent{name: "c"}
		tr, dir, cfgFile, rls := setup(t, a, b, c)
		writeScrapeIntervalConfig(t, cfgFile, 20)

		success, err := reload(t, tr, cfgFile, rls)
		require.Error(t, err)
		require.False(t, success)
		require.Equal(t, good, a.applied)
		require.Equal(t, good, b.applied)
		require.Equal(t, good, c.applied)

		st := checkPersisted(t, tr, dir)
		require.False(t, st.LastReloadSuccessful)
		require.Equal(t, reloadstatus.ErrorCategoryApply, st.ErrorCategory)
		require.Equal(t, []string{"a"}, st.AppliedReloaders)
		require.Equal(t, "b", st.FailedReloader)
		require.True(t, st.RollbackAttempted)
		require.True(t, st.RollbackSuccessful)
		require.Equal(t, good, tr.lastGood.GlobalConfig.ScrapeInterval)
	})

	t.Run("rolls back to last successful reload", func(t *testing.T) {
		a, b := &fakeComponent{name: "a"}, &fakeComponent{name: "b", failOn: failing}
		tr, _, cfgFile, rls := setup(t, a, b)
		writeScrapeIntervalConfig(t, cfgFile, 20)
		_, err := reload(t, tr, cfgFile, rls)
		require.NoError(t, err)

		writeScrapeIntervalConfig(t, cfgFile, 30)
		_, err = reload(t, tr, cfgFile, rls)
		require.Error(t, err)
		require.Equal(t, next, a.applied)
		require.True(t, tr.store.Get().RollbackSuccessful)
	})

	t.Run("rollback error", func(t *testing.T) {
		a, b := &fakeComponent{name: "a"}, &fakeComponent{name: "b"}
		tr, dir, cfgFile, rls := setup(t, a, b)
		b.failAlways = true
		writeScrapeIntervalConfig(t, cfgFile, 20)

		_, err := reload(t, tr, cfgFile, rls)
		require.Error(t, err)

		st := checkPersisted(t, tr, dir)
		require.Equal(t, reloadstatus.ErrorCategoryRollback, st.ErrorCategory)
		require.Equal(t, "b", st.FailedReloader)
		require.True(t, st.RollbackAttempted)
		require.False(t, st.RollbackSuccessful)
		require.Equal(t, good, a.applied)
	})
}

func TestTransactionalReloadEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	t.Parallel()

	dir := t.TempDir()
	storageDir := filepath.Join(dir, "data")
	statusFile := filepath.Join(storageDir, reloadstatus.FileName)
	cfgFile := filepath.Join(dir, "prometheus.yml")
	rulesFile := filepath.Join(dir, "rules.yml")
	writeScrapeIntervalConfig(t, cfgFile, 10)

	start := func() (string, func()) {
		port := testutil.RandomUnprivilegedPort(t)
		prom := prometheusCommandWithLogging(t, cfgFile, port,
			"--enable-feature=transactional-reload-config",
			"--web.enable-lifecycle",
			"--storage.tsdb.path="+storageDir,
		)
		require.NoError(t, prom.Start())
		stop := func() {
			prom.Process.Kill()
			prom.Process.Wait()
		}
		baseURL := "http://localhost:" + strconv.Itoa(port)
		require.Eventually(t, func() bool {
			resp, err := http.Get(baseURL + "/-/ready")
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}, 10*time.Second, 100*time.Millisecond, "Prometheus didn't become ready in time")
		return baseURL, stop
	}
	getStatus := func(baseURL string) reloadstatus.Status {
		resp, err := http.Get(baseURL + "/api/v1/status/reload")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var body struct {
			Data reloadstatus.Status `json:"data"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		return body.Data
	}
	triggerReload := func(baseURL string) {
		resp, err := http.Post(baseURL+"/-/reload", "", nil)
		require.NoError(t, err)
		resp.Body.Close()
	}

	baseURL, stop := start()

	st := getStatus(baseURL)
	require.Equal(t, reloadstatus.Initial(), st)
	_, err := os.Stat(statusFile)
	require.ErrorIs(t, err, os.ErrNotExist)

	resp, err := http.Get(baseURL + "/api/v1/features")
	require.NoError(t, err)
	var features struct {
		Data map[string]map[string]bool `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&features))
	resp.Body.Close()
	require.True(t, features.Data["prometheus"]["transactional_reload_config"])

	// Load error.
	require.NoError(t, os.WriteFile(cfgFile, []byte("global:\n  invalid"), 0o644))
	triggerReload(baseURL)
	st = getStatus(baseURL)
	require.False(t, st.LastReloadSuccessful)
	require.Equal(t, reloadstatus.ErrorCategoryLoad, st.ErrorCategory)
	require.False(t, st.RollbackAttempted)
	require.FileExists(t, statusFile)

	// The rules reloader fails after earlier components applied the new config.
	require.NoError(t, os.WriteFile(rulesFile, []byte("groups:\n- name: g\n  rules:\n  - record: r\n    expr: 'up{'\n"), 0o644))
	require.NoError(t, os.WriteFile(cfgFile, []byte("global:\n  scrape_interval: 20s\nrule_files: ["+strconv.Quote(rulesFile)+"]\n"), 0o644))
	triggerReload(baseURL)
	st = getStatus(baseURL)
	require.False(t, st.LastReloadSuccessful)
	require.Equal(t, reloadstatus.ErrorCategoryApply, st.ErrorCategory)
	require.Equal(t, "rules", st.FailedReloader)
	require.Contains(t, st.AppliedReloaders, "web_handler")
	require.True(t, st.RollbackAttempted)
	require.True(t, st.RollbackSuccessful)
	require.True(t, verifyScrapeInterval(t, baseURL, "10s"), "config must be rolled back to the startup config")

	// The outcome survives a restart; the startup load is not recorded.
	stop()
	writeScrapeIntervalConfig(t, cfgFile, 10)
	baseURL, stop = start()
	require.Equal(t, st, getStatus(baseURL))

	// A corrupted state file does not prevent startup or the endpoint from working.
	stop()
	require.NoError(t, os.WriteFile(statusFile, []byte("{corrupted"), 0o644))
	baseURL, _ = start()
	require.Equal(t, reloadstatus.Initial(), getStatus(baseURL))

	writeScrapeIntervalConfig(t, cfgFile, 20)
	triggerReload(baseURL)
	st = getStatus(baseURL)
	require.True(t, st.LastReloadSuccessful)
	require.Equal(t, reloadstatus.ErrorCategoryNone, st.ErrorCategory)
	require.Empty(t, st.FailedReloader)
	require.Contains(t, st.ReloaderTimingsMs, "rules")
	_, err = time.Parse(time.RFC3339, st.LastReloadID)
	require.NoError(t, err)
}
