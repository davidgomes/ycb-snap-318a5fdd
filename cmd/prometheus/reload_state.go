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
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/web/api/v1"
)

const (
	reloadStatusFilename = "reload_status.json"

	reloadErrNone     = "none"
	reloadErrLoad     = "load_error"
	reloadErrApply    = "apply_error"
	reloadErrRollback = "rollback_error"
)

// configReloadState tracks the last known-good configuration and the most
// recent transactional reload outcome. It is nil until main initializes it.
var configReloadState *reloadState

// reloadOutcome is the in-memory result of one transactional reload attempt.
type reloadOutcome struct {
	successful    bool
	category      string
	message       string
	applied       []string
	timings       map[string]int64
	rollbackTried bool
	rollbackOK    bool
	failedName    string
}

// reloadState persists the latest reload outcome under the TSDB directory.
type reloadState struct {
	mu       sync.RWMutex
	dir      string
	logger   *slog.Logger
	status   v1.ReloadStatus
	goodConf *config.Config
}

func newReloadState(dir string, logger *slog.Logger) *reloadState {
	return &reloadState{
		dir:    dir,
		logger: logger,
		status: v1.EmptyReloadStatus(),
	}
}

// load reads a previously persisted outcome. A missing or unreadable file
// leaves the empty status in place so startup and the status endpoint still work.
func (s *reloadState) load() {
	path := filepath.Join(s.dir, reloadStatusFilename)
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("Failed to read reload status; continuing with empty status", "path", path, "err", err)
		}
		return
	}
	var st v1.ReloadStatus
	if err := json.Unmarshal(b, &st); err != nil {
		s.logger.Warn("Ignoring corrupted reload status", "path", path, "err", err)
		return
	}
	if st.AppliedReloaders == nil {
		st.AppliedReloaders = []string{}
	}
	if st.ReloaderTimingsMS == nil {
		st.ReloaderTimingsMS = map[string]int64{}
	}
	if st.ErrorCategory == "" {
		st.ErrorCategory = reloadErrNone
	}
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
}

func (s *reloadState) snapshot() v1.ReloadStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.Clone()
}

func (s *reloadState) setLastGood(conf *config.Config) {
	s.mu.Lock()
	s.goodConf = conf
	s.mu.Unlock()
}

func (s *reloadState) lastGood() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.goodConf
}

func (s *reloadState) record(o reloadOutcome) {
	applied := o.applied
	if applied == nil {
		applied = []string{}
	}
	timings := o.timings
	if timings == nil {
		timings = map[string]int64{}
	}
	st := v1.ReloadStatus{
		LastReloadID:         time.Now().UTC().Format(time.RFC3339Nano),
		LastReloadSuccessful: o.successful,
		ErrorCategory:        o.category,
		ErrorMessage:         o.message,
		AppliedReloaders:     applied,
		RollbackAttempted:    o.rollbackTried,
		RollbackSuccessful:   o.rollbackOK,
		FailedReloader:       o.failedName,
		ReloaderTimingsMS:    timings,
	}
	s.mu.Lock()
	s.status = st
	dir := s.dir
	s.mu.Unlock()
	if dir == "" {
		return
	}
	if err := persistReloadStatus(dir, st); err != nil {
		s.logger.Error("Failed to persist reload status", "err", err)
	}
}

func recordReloadOutcome(o reloadOutcome) {
	if configReloadState == nil {
		return
	}
	configReloadState.record(o)
}

func persistReloadStatus(dir string, st v1.ReloadStatus) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "reload_status-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, reloadStatusFilename)); err != nil {
		return err
	}
	cleanup = false
	return nil
}
