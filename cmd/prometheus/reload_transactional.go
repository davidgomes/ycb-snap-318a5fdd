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
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/util/reloadstatus"
)

// transactionalReloader applies configuration reloads as a single unit: the
// reloaders run in sequence, the first failure stops the attempt, and the
// components that already applied the new configuration are rolled back to
// the last known-good configuration. It must not be used concurrently.
type transactionalReloader struct {
	lastGood *config.Config
	store    *reloadstatus.Store
}

// reload loads filename and applies it through rls. When persist is false the
// outcome is not recorded, which is used for the initial configuration load
// at startup.
func (t *transactionalReloader) reload(filename string, enableExemplarStorage bool, logger *slog.Logger, noStepSubqueryInterval *safePromQLNoStepSubqueryInterval, callback func(bool), persist bool, rls ...reloader) (err error) {
	start := time.Now()
	timingsLogger := logger
	logger.Info("Loading configuration file", "filename", filename, "mode", "transactional")

	status := reloadstatus.Status{
		LastReloadID:      start.UTC().Format(time.RFC3339Nano),
		ErrorCategory:     reloadstatus.ErrorCategoryNone,
		AppliedReloaders:  []string{},
		ReloaderTimingsMs: map[string]float64{},
	}

	defer func() {
		if err == nil {
			configSuccess.Set(1)
			configSuccessTime.SetToCurrentTime()
			callback(true)
		} else {
			configSuccess.Set(0)
			callback(false)
			status.ErrorMessage = err.Error()
		}
		status.LastReloadSuccessful = err == nil
		if persist {
			if perr := t.store.Record(status); perr != nil {
				logger.Error("Failed to persist configuration reload status", "err", perr)
			}
		}
	}()

	conf, err := config.LoadFile(filename, agentMode, logger)
	if err != nil {
		status.ErrorCategory = reloadstatus.ErrorCategoryLoad
		return fmt.Errorf("couldn't load configuration (--config.file=%q): %w", filename, err)
	}

	if enableExemplarStorage {
		if conf.StorageConfig.ExemplarsConfig == nil {
			conf.StorageConfig.ExemplarsConfig = &config.DefaultExemplarsConfig
		}
	}

	for i, rl := range rls {
		rstart := time.Now()
		rerr := rl.reloader(conf)
		d := time.Since(rstart)
		status.ReloaderTimingsMs[rl.name] = float64(d) / float64(time.Millisecond)
		timingsLogger = timingsLogger.With(rl.name, d)
		if rerr == nil {
			status.AppliedReloaders = append(status.AppliedReloaders, rl.name)
			continue
		}

		logger.Error("Failed to apply configuration", "reloader", rl.name, "err", rerr)
		status.FailedReloader = rl.name
		status.ErrorCategory = reloadstatus.ErrorCategoryApply
		applyErr := fmt.Errorf("reloader %q failed to apply the new configuration (--config.file=%q): %w", rl.name, filename, rerr)
		if len(status.AppliedReloaders) == 0 || t.lastGood == nil {
			return applyErr
		}

		status.RollbackAttempted = true
		// The failed reloader is included as it may have partially applied the new configuration.
		if rbErr := t.rollback(logger, rls[:i+1]); rbErr != nil {
			status.ErrorCategory = reloadstatus.ErrorCategoryRollback
			logger.Error("Failed to roll back to the last known-good configuration", "err", rbErr)
			return fmt.Errorf("%w; rollback to the last known-good configuration failed: %w", applyErr, rbErr)
		}
		status.RollbackSuccessful = true
		logger.Info("Rolled back to the last known-good configuration")
		return fmt.Errorf("%w; rolled back to the last known-good configuration", applyErr)
	}

	t.lastGood = conf
	updateGoGC(conf, logger)
	noStepSubqueryInterval.Set(conf.GlobalConfig.EvaluationInterval)
	timingsLogger.Info("Completed loading of configuration file", "filename", filename, "totalDuration", time.Since(start))
	return nil
}

func (t *transactionalReloader) rollback(logger *slog.Logger, rls []reloader) error {
	var errs []error
	for _, rl := range rls {
		if err := rl.reloader(t.lastGood); err != nil {
			logger.Error("Failed to roll back configuration", "reloader", rl.name, "err", err)
			errs = append(errs, fmt.Errorf("reloader %q: %w", rl.name, err))
		}
	}
	return errors.Join(errs...)
}
