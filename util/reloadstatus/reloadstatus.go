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

// Package reloadstatus records the outcome of the most recent configuration
// reload attempt and persists it so that it survives restarts.
package reloadstatus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/prometheus/common/promslog"

	"github.com/prometheus/prometheus/tsdb/fileutil"
)

// FileName is the name of the file, relative to the storage directory, in
// which the most recent reload outcome is persisted.
const FileName = "reload_status.json"

// ErrorCategory classifies the outcome of a reload attempt.
type ErrorCategory string

const (
	// ErrorCategoryNone means the reload succeeded, or no reload was attempted yet.
	ErrorCategoryNone ErrorCategory = "none"
	// ErrorCategoryLoad means the configuration could not be loaded or parsed.
	ErrorCategoryLoad ErrorCategory = "load_error"
	// ErrorCategoryApply means a reloader failed and, if a rollback was
	// attempted, the rollback succeeded.
	ErrorCategoryApply ErrorCategory = "apply_error"
	// ErrorCategoryRollback means a reloader failed and the rollback to the
	// last known-good configuration failed as well.
	ErrorCategoryRollback ErrorCategory = "rollback_error"
)

// Status is the outcome of a single configuration reload attempt.
type Status struct {
	// LastReloadID identifies the attempt; it is the RFC3339 start time of the attempt.
	LastReloadID         string             `json:"last_reload_id"`
	LastReloadSuccessful bool               `json:"last_reload_successful"`
	ErrorCategory        ErrorCategory      `json:"error_category"`
	ErrorMessage         string             `json:"error_message"`
	AppliedReloaders     []string           `json:"applied_reloaders"`
	RollbackAttempted    bool               `json:"rollback_attempted"`
	RollbackSuccessful   bool               `json:"rollback_successful"`
	FailedReloader       string             `json:"failed_reloader"`
	ReloaderTimingsMs    map[string]float64 `json:"reloader_timings_ms"`
}

// Initial returns the status reported before any reload was attempted.
func Initial() Status {
	return Status{
		ErrorCategory:     ErrorCategoryNone,
		AppliedReloaders:  []string{},
		ReloaderTimingsMs: map[string]float64{},
	}
}

func (s Status) normalized() Status {
	switch s.ErrorCategory {
	case ErrorCategoryNone, ErrorCategoryLoad, ErrorCategoryApply, ErrorCategoryRollback:
	default:
		s.ErrorCategory = ErrorCategoryNone
	}
	if s.AppliedReloaders == nil {
		s.AppliedReloaders = []string{}
	} else {
		s.AppliedReloaders = slices.Clone(s.AppliedReloaders)
	}
	if s.ReloaderTimingsMs == nil {
		s.ReloaderTimingsMs = map[string]float64{}
	} else {
		s.ReloaderTimingsMs = maps.Clone(s.ReloaderTimingsMs)
	}
	return s
}

// Store holds the most recent reload status and persists it to disk.
// It is safe for concurrent use.
type Store struct {
	mtx    sync.RWMutex
	path   string
	status Status
	logger *slog.Logger
}

// NewStore returns a Store persisting to FileName under dir. A previously
// persisted status is loaded if present; a missing or corrupted file is
// ignored so that it never prevents startup.
func NewStore(dir string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = promslog.NewNopLogger()
	}
	s := &Store{
		path:   filepath.Join(dir, FileName),
		status: Initial(),
		logger: logger,
	}
	st, err := readFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		logger.Warn("Ignoring unreadable persisted reload status", "file", s.path, "err", err)
	default:
		s.status = st
	}
	return s
}

func readFile(path string) (Status, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Status{}, err
	}
	var st Status
	if err := json.Unmarshal(b, &st); err != nil {
		return Status{}, fmt.Errorf("decode: %w", err)
	}
	return st.normalized(), nil
}

// Get returns a copy of the most recent reload status.
func (s *Store) Get() Status {
	if s == nil {
		return Initial()
	}
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.status.normalized()
}

// Record sets the most recent reload status and persists it atomically.
// The in-memory status is updated even if persisting fails.
func (s *Store) Record(st Status) error {
	st = st.normalized()

	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.status = st

	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o777); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := fileutil.Replace(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
