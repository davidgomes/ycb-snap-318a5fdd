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
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/prometheus/config"
	api_v1 "github.com/prometheus/prometheus/web/api/v1"
)

const reloadStateFilename = "reload_state.json"

type reloadStateStore struct {
	mtx    sync.RWMutex
	path   string
	logger *slog.Logger
	status api_v1.ReloadStatus
}

func newReloadStateStore(dir string, logger *slog.Logger) *reloadStateStore {
	store := &reloadStateStore{
		path:   filepath.Join(dir, reloadStateFilename),
		logger: logger,
		status: emptyReloadStatus(),
	}

	data, err := os.ReadFile(store.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("Failed to read persisted configuration reload status", "filename", store.path, "err", err)
		}
		return store
	}

	if err := json.Unmarshal(data, &store.status); err != nil || !validReloadErrorCategory(store.status.ErrorCategory) {
		logger.Warn("Ignoring corrupted persisted configuration reload status", "filename", store.path)
		store.status = emptyReloadStatus()
		return store
	}
	normalizeReloadStatus(&store.status)
	return store
}

func emptyReloadStatus() api_v1.ReloadStatus {
	return api_v1.ReloadStatus{
		ErrorCategory:     "none",
		AppliedReloaders:  []string{},
		ReloaderTimingsMS: map[string]int64{},
	}
}

func validReloadErrorCategory(category string) bool {
	switch category {
	case "none", "load_error", "apply_error", "rollback_error":
		return true
	default:
		return false
	}
}

func normalizeReloadStatus(status *api_v1.ReloadStatus) {
	if status.ErrorCategory == "" {
		status.ErrorCategory = "none"
	}
	if status.AppliedReloaders == nil {
		status.AppliedReloaders = []string{}
	}
	if status.ReloaderTimingsMS == nil {
		status.ReloaderTimingsMS = map[string]int64{}
	}
}

func (s *reloadStateStore) get() api_v1.ReloadStatus {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	status := s.status
	status.AppliedReloaders = make([]string, len(s.status.AppliedReloaders))
	copy(status.AppliedReloaders, s.status.AppliedReloaders)
	status.ReloaderTimingsMS = make(map[string]int64, len(status.ReloaderTimingsMS))
	for name, timing := range s.status.ReloaderTimingsMS {
		status.ReloaderTimingsMS[name] = timing
	}
	return status
}

func (s *reloadStateStore) record(status api_v1.ReloadStatus) {
	normalizeReloadStatus(&status)

	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.status = status

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		s.logger.Error("Failed to serialize configuration reload status", "err", err)
		return
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		s.logger.Error("Failed to persist configuration reload status", "filename", s.path, "err", err)
		return
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		s.logger.Error("Failed to persist configuration reload status", "filename", s.path, "err", err)
		_ = os.Remove(tmpPath)
	}
}

type transactionalReloadManager struct {
	status         *reloadStateStore
	lastGoodConfig *config.Config
}

func newTransactionalReloadManager(status *reloadStateStore) *transactionalReloadManager {
	return &transactionalReloadManager{status: status}
}

func (m *transactionalReloadManager) initialize(
	filename string,
	enableExemplarStorage bool,
	logger *slog.Logger,
	noStepSubqueryInterval *safePromQLNoStepSubqueryInterval,
	rls ...reloader,
) error {
	start := time.Now()
	logger.Info("Loading configuration file", "filename", filename)

	conf, err := loadConfigFile(filename, enableExemplarStorage, logger)
	if err != nil {
		return err
	}
	for _, rl := range rls {
		if err := rl.reloader(conf); err != nil {
			return fmt.Errorf("one or more errors occurred while applying the new configuration (--config.file=%q): %w", filename, err)
		}
	}

	updateGoGC(conf, logger)
	noStepSubqueryInterval.Set(conf.GlobalConfig.EvaluationInterval)
	m.lastGoodConfig = conf
	logger.Info("Completed loading of configuration file", "filename", filename, "totalDuration", time.Since(start))
	return nil
}

func (m *transactionalReloadManager) reload(
	filename string,
	enableExemplarStorage bool,
	logger *slog.Logger,
	noStepSubqueryInterval *safePromQLNoStepSubqueryInterval,
	callback func(bool),
	rls ...reloader,
) (err error) {
	start := time.Now()
	reloadID := time.Now().UTC().Format(time.RFC3339Nano)
	status := emptyReloadStatus()
	status.LastReloadID = reloadID
	logger.Info("Loading configuration file", "filename", filename)

	conf, err := loadConfigFile(filename, enableExemplarStorage, logger)
	if err != nil {
		configSuccess.Set(0)
		status.ErrorCategory = "load_error"
		status.ErrorMessage = err.Error()
		m.status.record(status)
		callback(false)
		return err
	}

	failedIndex := -1
	for i, rl := range rls {
		start := time.Now()
		applyErr := rl.reloader(conf)
		status.ReloaderTimingsMS[rl.name] = time.Since(start).Milliseconds()
		if applyErr != nil {
			failedIndex = i
			status.FailedReloader = rl.name
			err = fmt.Errorf("failed to apply reloader %q: %w", rl.name, applyErr)
			break
		}
		status.AppliedReloaders = append(status.AppliedReloaders, rl.name)
	}

	if failedIndex >= 0 {
		status.ErrorCategory = "apply_error"
		status.ErrorMessage = err.Error()
		if len(status.AppliedReloaders) > 0 && m.lastGoodConfig != nil {
			status.RollbackAttempted = true
			status.RollbackSuccessful = true
			var rollbackErrs []error
			for i := failedIndex; i >= 0; i-- {
				if applyErr := rls[i].reloader(m.lastGoodConfig); applyErr != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("%s: %w", rls[i].name, applyErr))
					status.RollbackSuccessful = false
				}
			}
			rollbackErr := errors.Join(rollbackErrs...)
			if rollbackErr != nil {
				status.ErrorCategory = "rollback_error"
				status.ErrorMessage = fmt.Sprintf("%s; rollback failed: %v", err, rollbackErr)
			}
			updateGoGC(m.lastGoodConfig, logger)
			noStepSubqueryInterval.Set(m.lastGoodConfig.GlobalConfig.EvaluationInterval)
		}
		configSuccess.Set(0)
		m.status.record(status)
		callback(false)
		return err
	}

	updateGoGC(conf, logger)
	noStepSubqueryInterval.Set(conf.GlobalConfig.EvaluationInterval)
	m.lastGoodConfig = conf
	configSuccess.Set(1)
	configSuccessTime.SetToCurrentTime()
	status.LastReloadSuccessful = true
	logger.Info("Completed loading of configuration file", "filename", filename, "totalDuration", time.Since(start))
	m.status.record(status)
	callback(true)
	return nil
}

func loadConfigFile(filename string, enableExemplarStorage bool, logger *slog.Logger) (*config.Config, error) {
	conf, err := config.LoadFile(filename, agentMode, logger)
	if err != nil {
		return nil, fmt.Errorf("couldn't load configuration (--config.file=%q): %w", filename, err)
	}
	if enableExemplarStorage && conf.StorageConfig.ExemplarsConfig == nil {
		conf.StorageConfig.ExemplarsConfig = &config.DefaultExemplarsConfig
	}
	return conf, nil
}
