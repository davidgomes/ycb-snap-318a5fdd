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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/prometheus/config"
	api_v1 "github.com/prometheus/prometheus/web/api/v1"
)

const (
	// reloadStatusFilename is the persisted most recent reload outcome, stored
	// directly under the configured TSDB (or agent WAL) storage directory.
	reloadStatusFilename = "reload_status.json"

	// maxReloadStatusBytes bounds how much of a corrupt or unexpected file is read at startup.
	maxReloadStatusBytes int64 = 1 << 20
)

// reloadState tracks the last known-good configuration and the most recent
// transactional reload outcome. The outcome is served over HTTP and written
// to disk so it survives restarts. A missing or unreadable file is ignored.
type reloadState struct {
	mu            sync.RWMutex
	dir           string
	transactional bool
	logger        *slog.Logger
	status        api_v1.ReloadStatus
	lastGood      *config.Config
}

func newReloadState(dir string, transactional bool, logger *slog.Logger) *reloadState {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	rs := &reloadState{
		dir:           dir,
		transactional: transactional,
		logger:        logger,
		status:        api_v1.EmptyReloadStatus(),
	}
	if err := rs.load(); err != nil {
		logger.Warn("Ignoring reload outcome state; startup will continue with an empty outcome", "err", err, "file", rs.path())
		rs.status = api_v1.EmptyReloadStatus()
	}
	return rs
}

func (rs *reloadState) path() string {
	return filepath.Join(rs.dir, reloadStatusFilename)
}

// Status returns a copy of the most recent reload outcome.
func (rs *reloadState) Status() api_v1.ReloadStatus {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.status.Normalized()
}

func (rs *reloadState) setLastGood(conf *config.Config) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.lastGood = conf
}

func (rs *reloadState) record(status api_v1.ReloadStatus) {
	status = status.Normalized()
	if err := rs.persist(status); err != nil {
		rs.logger.Error("Failed to persist reload outcome", "err", err, "file", rs.path())
	}
	rs.mu.Lock()
	rs.status = status
	rs.mu.Unlock()
}

func (rs *reloadState) recordLoadError(start time.Time, err error) {
	rs.record(api_v1.ReloadStatus{
		LastReloadID:         start.UTC().Format(time.RFC3339Nano),
		LastReloadSuccessful: false,
		ErrorCategory:        api_v1.ReloadErrorCategoryLoad,
		ErrorMessage:         err.Error(),
		AppliedReloaders:     []string{},
		ReloaderTimingsMS:    map[string]int64{},
	})
}

// applyTransactionalReload applies reloaders in order and stops at the first
// error. When at least one reloader has already applied the new configuration,
// it rolls those components, and the reloader that failed, back to the last
// known-good configuration. The attempt produces one outcome.
func applyTransactionalReload(filename string, logger *slog.Logger, noStepSubqueryInterval *safePromQLNoStepSubqueryInterval, rs *reloadState, start time.Time, conf *config.Config, rls []reloader) error {
	applied := make([]string, 0, len(rls))
	timings := make(map[string]int64, len(rls))
	timingsLogger := logger

	var failedName string
	var failedErr error
	for _, rl := range rls {
		rstart := time.Now()
		err := rl.reloader(conf)
		elapsed := time.Since(rstart)
		timings[rl.name] = elapsed.Milliseconds()
		timingsLogger = timingsLogger.With(rl.name, elapsed)
		if err != nil {
			logger.Error("Failed to apply configuration", "err", err, "reloader", rl.name)
			failedName = rl.name
			failedErr = err
			break
		}
		applied = append(applied, rl.name)
	}

	reloadID := start.UTC().Format(time.RFC3339Nano)
	if failedErr == nil {
		updateGoGC(conf, logger)
		noStepSubqueryInterval.Set(conf.GlobalConfig.EvaluationInterval)
		rs.setLastGood(conf)
		rs.record(api_v1.ReloadStatus{
			LastReloadID:         reloadID,
			LastReloadSuccessful: true,
			ErrorCategory:        api_v1.ReloadErrorCategoryNone,
			AppliedReloaders:     applied,
			ReloaderTimingsMS:    timings,
		})
		timingsLogger.Info("Completed loading of configuration file", "filename", filename, "totalDuration", time.Since(start))
		return nil
	}

	status := api_v1.ReloadStatus{
		LastReloadID:         reloadID,
		LastReloadSuccessful: false,
		ErrorCategory:        api_v1.ReloadErrorCategoryApply,
		ErrorMessage:         failedErr.Error(),
		AppliedReloaders:     applied,
		FailedReloader:       failedName,
		ReloaderTimingsMS:    timings,
	}
	if len(applied) > 0 {
		status.RollbackAttempted = true
		logger.Info("Rolling back configuration to last known-good", "failed_reloader", failedName, "applied_reloaders", applied)
		ok, rollbackName, rollbackErr := rs.rollback(logger, failedName, rls)
		status.RollbackSuccessful = ok
		if !ok {
			status.ErrorCategory = api_v1.ReloadErrorCategoryRollback
			switch {
			case rollbackErr != nil && rollbackName != "":
				status.ErrorMessage = fmt.Sprintf("%s; rollback of %s failed: %s", failedErr.Error(), rollbackName, rollbackErr.Error())
			case rollbackErr != nil:
				status.ErrorMessage = fmt.Sprintf("%s; rollback failed: %s", failedErr.Error(), rollbackErr.Error())
			default:
				status.ErrorMessage = fmt.Sprintf("%s; rollback failed", failedErr.Error())
			}
		}
	}
	rs.record(status)
	return fmt.Errorf("failed to apply configuration (--config.file=%q) in reloader %q: %w", filename, failedName, failedErr)
}

func (rs *reloadState) load() error {
	path := rs.path()
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() == 0 {
		return errors.New("reload outcome file is empty")
	}
	if info.Size() > maxReloadStatusBytes {
		return fmt.Errorf("reload outcome file is too large (%d bytes)", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("reload outcome file is empty")
	}
	var status api_v1.ReloadStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return fmt.Errorf("decode reload outcome: %w", err)
	}
	if !api_v1.ValidReloadErrorCategory(status.ErrorCategory) {
		return fmt.Errorf("invalid error_category %q", status.ErrorCategory)
	}
	rs.status = status.Normalized()
	return nil
}

func (rs *reloadState) persist(status api_v1.ReloadStatus) error {
	if rs.dir == "" {
		return errors.New("storage directory is not configured")
	}
	if err := os.MkdirAll(rs.dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomicFile(rs.path(), data)
}

// rollback reapplies the last known-good configuration to every reloader that
// ran during the failed attempt, including the one that returned the error,
// in reverse order. lastGood is the configuration successfully loaded at
// startup or by the previous fully successful reload.
func (rs *reloadState) rollback(logger *slog.Logger, failedName string, rls []reloader) (bool, string, error) {
	rs.mu.RLock()
	lastGood := rs.lastGood
	rs.mu.RUnlock()
	if lastGood == nil {
		return false, "", errors.New("no known-good configuration")
	}

	names := make([]string, 0, len(rls))
	for _, rl := range rls {
		names = append(names, rl.name)
		if rl.name == failedName {
			break
		}
	}

	var firstName string
	var firstErr error
	for i := len(names) - 1; i >= 0; i-- {
		var rl reloader
		for _, candidate := range rls {
			if candidate.name == names[i] {
				rl = candidate
				break
			}
		}
		if rl.reloader == nil {
			continue
		}
		if err := rl.reloader(lastGood); err != nil {
			logger.Error("Failed to roll back configuration", "reloader", rl.name, "err", err)
			if firstErr == nil {
				firstErr = err
				firstName = rl.name
			}
		}
	}
	if firstErr != nil {
		return false, firstName, firstErr
	}
	return true, "", nil
}

func writeAtomicFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".reload_status.*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	success := false
	defer func() {
		if !success {
			os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	success = true
	return nil
}
