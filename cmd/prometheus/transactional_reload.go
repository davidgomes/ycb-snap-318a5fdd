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
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/prometheus/config"
	api_v1 "github.com/prometheus/prometheus/web/api/v1"
)

const reloadStatusFilename = "reload_status.json"

// reloadStatusTracker keeps the outcome of the most recent reload attempt and
// persists it to disk so that it survives restarts.
type reloadStatusTracker struct {
	mtx    sync.RWMutex
	path   string
	status api_v1.ReloadStatus
	logger *slog.Logger
}

// newReloadStatusTracker loads any previously persisted status from dir.
// A missing or unreadable state file results in the default status.
func newReloadStatusTracker(dir string, logger *slog.Logger) *reloadStatusTracker {
	t := &reloadStatusTracker{
		path:   filepath.Join(dir, reloadStatusFilename),
		status: api_v1.DefaultReloadStatus(),
		logger: logger,
	}

	b, err := os.ReadFile(t.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return t
	case err != nil:
		logger.Warn("Failed to read persisted reload status, ignoring it", "file", t.path, "err", err)
		return t
	}

	var s api_v1.ReloadStatus
	if err := json.Unmarshal(b, &s); err != nil {
		logger.Warn("Persisted reload status is corrupted, ignoring it", "file", t.path, "err", err)
		return t
	}
	t.status = normalizeReloadStatus(s)
	return t
}

func normalizeReloadStatus(s api_v1.ReloadStatus) api_v1.ReloadStatus {
	switch s.ErrorCategory {
	case api_v1.ReloadErrorCategoryNone, api_v1.ReloadErrorCategoryLoad, api_v1.ReloadErrorCategoryApply, api_v1.ReloadErrorCategoryRollback:
	default:
		s.ErrorCategory = api_v1.ReloadErrorCategoryNone
	}
	if s.AppliedReloaders == nil {
		s.AppliedReloaders = []string{}
	}
	if s.ReloaderTimingsMs == nil {
		s.ReloaderTimingsMs = map[string]float64{}
	}
	return s
}

// Get returns a copy of the most recent reload status.
func (t *reloadStatusTracker) Get() api_v1.ReloadStatus {
	t.mtx.RLock()
	defer t.mtx.RUnlock()

	s := t.status
	s.AppliedReloaders = slices.Clone(s.AppliedReloaders)
	s.ReloaderTimingsMs = maps.Clone(s.ReloaderTimingsMs)
	return s
}

// record stores s as the most recent outcome and persists it.
// Persistence failures are logged but do not affect the in-memory status.
func (t *reloadStatusTracker) record(s api_v1.ReloadStatus) {
	s = normalizeReloadStatus(s)

	t.mtx.Lock()
	defer t.mtx.Unlock()
	t.status = s

	if err := t.persist(s); err != nil {
		t.logger.Error("Failed to persist reload status", "file", t.path, "err", err)
	}
}

func (t *reloadStatusTracker) persist(s api_v1.ReloadStatus) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o777); err != nil {
		return err
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o666); err != nil {
		return err
	}
	return os.Rename(tmp, t.path)
}

// transactionalReload applies reloaders sequentially, stops at the first
// failure and rolls back already applied components to the last known-good
// configuration.
type transactionalReload struct {
	lastGood *config.Config
	tracker  *reloadStatusTracker
}

func newTransactionalReload(tracker *reloadStatusTracker) *transactionalReload {
	return &transactionalReload{tracker: tracker}
}

// reload loads filename and applies it. If record is false, the outcome is not
// stored in the tracker (used for the initial configuration load at startup).
func (t *transactionalReload) reload(filename string, enableExemplarStorage bool, logger *slog.Logger, noStepSubqueryInterval *safePromQLNoStepSubqueryInterval, callback func(bool), record bool, rls ...reloader) (err error) {
	start := time.Now()
	timingsLogger := logger
	logger.Info("Loading configuration file", "filename", filename, "mode", "transactional")

	status := api_v1.DefaultReloadStatus()
	status.LastReloadID = start.UTC().Format(time.RFC3339Nano)

	defer func() {
		if err == nil {
			configSuccess.Set(1)
			configSuccessTime.SetToCurrentTime()
			callback(true)
		} else {
			configSuccess.Set(0)
			callback(false)
		}
		if record && t.tracker != nil {
			t.tracker.record(status)
		}
	}()

	conf, err := config.LoadFile(filename, agentMode, logger)
	if err != nil {
		err = fmt.Errorf("couldn't load configuration (--config.file=%q): %w", filename, err)
		status.ErrorCategory = api_v1.ReloadErrorCategoryLoad
		status.ErrorMessage = err.Error()
		return err
	}

	if enableExemplarStorage {
		if conf.StorageConfig.ExemplarsConfig == nil {
			conf.StorageConfig.ExemplarsConfig = &config.DefaultExemplarsConfig
		}
	}

	for i, rl := range rls {
		rstart := time.Now()
		rerr := rl.reloader(conf)
		elapsed := time.Since(rstart)
		status.ReloaderTimingsMs[rl.name] = float64(elapsed) / float64(time.Millisecond)
		timingsLogger = timingsLogger.With(rl.name, elapsed)

		if rerr == nil {
			status.AppliedReloaders = append(status.AppliedReloaders, rl.name)
			continue
		}

		logger.Error("Failed to apply configuration", "reloader", rl.name, "err", rerr)
		status.FailedReloader = rl.name
		status.ErrorCategory = api_v1.ReloadErrorCategoryApply
		err = fmt.Errorf("failed to apply configuration in reloader %q (--config.file=%q): %w", rl.name, filename, rerr)

		if i > 0 && t.lastGood != nil {
			status.RollbackAttempted = true
			// The failed reloader is included as it may have partially applied the new configuration.
			if rbErr := rollbackReloaders(t.lastGood, rls[:i+1]); rbErr != nil {
				logger.Error("Failed to roll back to last known-good configuration", "err", rbErr)
				status.ErrorCategory = api_v1.ReloadErrorCategoryRollback
				err = fmt.Errorf("%w; rollback to last known-good configuration failed: %w", err, rbErr)
			} else {
				logger.Info("Rolled back to last known-good configuration", "reloaders", status.AppliedReloaders)
				status.RollbackSuccessful = true
			}
		}
		status.ErrorMessage = err.Error()
		return err
	}

	t.lastGood = conf
	status.LastReloadSuccessful = true

	updateGoGC(conf, logger)
	noStepSubqueryInterval.Set(conf.GlobalConfig.EvaluationInterval)
	timingsLogger.Info("Completed loading of configuration file", "filename", filename, "totalDuration", time.Since(start))
	return nil
}

func rollbackReloaders(conf *config.Config, rls []reloader) error {
	var errs []error
	for _, rl := range rls {
		if err := rl.reloader(conf); err != nil {
			errs = append(errs, fmt.Errorf("reloader %q: %w", rl.name, err))
		}
	}
	return errors.Join(errs...)
}
