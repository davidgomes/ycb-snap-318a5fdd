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
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/common/promslog"

	"github.com/prometheus/prometheus/config"
	api_v1 "github.com/prometheus/prometheus/web/api/v1"
)

// reloadCoordinator tracks the last known-good configuration and the single
// outcome of the most recent transactional reload attempt.
type reloadCoordinator struct {
	transactional bool
	dir           string
	logger        *slog.Logger
	now           func() time.Time

	mu       sync.RWMutex
	lastGood *config.Config
	status   api_v1.ReloadStatus
}

func newReloadCoordinator(transactional bool, dir string, logger *slog.Logger) *reloadCoordinator {
	if logger == nil {
		logger = promslog.NewNopLogger()
	}
	return &reloadCoordinator{
		transactional: transactional,
		dir:           dir,
		logger:        logger,
		now:           time.Now,
		status:        api_v1.EmptyReloadStatus(),
	}
}

// Status returns a copy of the most recent reload outcome.
func (c *reloadCoordinator) Status() api_v1.ReloadStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status.Clone()
}

// loadPersisted replaces in-memory status with the file under the storage directory.
// Missing or corrupt files leave the empty pre-reload status in place.
func (c *reloadCoordinator) loadPersisted() error {
	status, err := api_v1.LoadReloadStatus(c.dir)
	c.mu.Lock()
	c.status = status.Clone()
	c.mu.Unlock()
	return err
}

func (c *reloadCoordinator) rememberGood(conf *config.Config) {
	c.mu.Lock()
	c.lastGood = conf
	c.mu.Unlock()
}

func (c *reloadCoordinator) knownGood() *config.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastGood
}

func (c *reloadCoordinator) recordLoadFailure(err error) {
	status := api_v1.EmptyReloadStatus()
	status.LastReloadID = c.now().UTC().Format(time.RFC3339)
	status.LastReloadSuccessful = false
	status.ErrorCategory = api_v1.ReloadErrorLoad
	status.ErrorMessage = err.Error()
	c.store(status)
}

// apply runs reloaders in order. On the first failure after a successful apply,
// it restores the last known-good configuration and records one outcome.
func (c *reloadCoordinator) apply(conf *config.Config, rls []reloader, timingsLogger *slog.Logger) (*slog.Logger, error) {
	status := api_v1.EmptyReloadStatus()
	status.LastReloadID = c.now().UTC().Format(time.RFC3339)
	status.LastReloadSuccessful = false
	status.ErrorCategory = api_v1.ReloadErrorNone

	var applyErr error
	failedIdx := -1
	for i, rl := range rls {
		rstart := time.Now()
		err := rl.reloader(conf)
		elapsed := time.Since(rstart)
		timingsLogger = timingsLogger.With(rl.name, elapsed)
		status.ReloaderTimingsMS[rl.name] = elapsed.Milliseconds()
		if err != nil {
			applyErr = err
			failedIdx = i
			status.FailedReloader = rl.name
			status.ErrorCategory = api_v1.ReloadErrorApply
			status.ErrorMessage = err.Error()
			c.logger.Error("Failed to apply configuration", "err", err, "reloader", rl.name)
			break
		}
		status.AppliedReloaders = append(status.AppliedReloaders, rl.name)
	}

	if applyErr == nil {
		status.LastReloadSuccessful = true
		status.ErrorCategory = api_v1.ReloadErrorNone
		c.mu.Lock()
		c.lastGood = conf
		c.mu.Unlock()
		c.store(status)
		return timingsLogger, nil
	}

	if len(status.AppliedReloaders) == 0 {
		c.store(status)
		return timingsLogger, fmt.Errorf("failed to apply configuration with %s: %w", status.FailedReloader, applyErr)
	}

	status.RollbackAttempted = true
	good := c.knownGood()
	rollbackOK := good != nil
	var rollbackErr error
	if good == nil {
		rollbackErr = fmt.Errorf("no last known-good configuration")
		c.logger.Error("Cannot roll back configuration", "err", rollbackErr)
	} else {
		c.logger.Info("Rolling back configuration to last known-good", "failed_reloader", status.FailedReloader)
		for i := 0; i <= failedIdx; i++ {
			rl := rls[i]
			if err := rl.reloader(good); err != nil {
				rollbackOK = false
				if rollbackErr == nil {
					rollbackErr = fmt.Errorf("%s: %w", rl.name, err)
				}
				c.logger.Error("Failed to roll back configuration", "err", err, "reloader", rl.name)
			}
		}
	}

	status.RollbackSuccessful = rollbackOK
	if !rollbackOK {
		status.ErrorCategory = api_v1.ReloadErrorRollback
		if rollbackErr != nil {
			status.ErrorMessage = fmt.Sprintf("%s; rollback failed: %s", applyErr.Error(), rollbackErr.Error())
		}
		c.store(status)
		return timingsLogger, fmt.Errorf("failed to apply configuration with %s: %w; rollback failed: %v", status.FailedReloader, applyErr, rollbackErr)
	}

	c.store(status)
	return timingsLogger, fmt.Errorf("failed to apply configuration with %s: %w", status.FailedReloader, applyErr)
}

func (c *reloadCoordinator) store(status api_v1.ReloadStatus) {
	status = status.Clone()
	c.mu.Lock()
	c.status = status
	dir := c.dir
	c.mu.Unlock()
	if err := api_v1.SaveReloadStatus(dir, status); err != nil {
		c.logger.Error("Failed to persist reload status", "err", err, "dir", dir)
	}
}
