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

// Package reloadstatus keeps track of the outcome of the most recent
// configuration reload attempt and persists it across restarts.
package reloadstatus

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

	"github.com/prometheus/common/promslog"

	"github.com/prometheus/prometheus/tsdb/fileutil"
)

// Filename is the name of the file, inside the storage directory, that holds
// the outcome of the most recent reload attempt.
const Filename = "reload_status.json"

// ErrorCategory classifies the outcome of a reload attempt.
type ErrorCategory string

const (
	// ErrorCategoryNone is used for successful reloads and before the first
	// reload attempt.
	ErrorCategoryNone ErrorCategory = "none"
	// ErrorCategoryLoad means the configuration could not be loaded or
	// parsed, so it was not applied to any component.
	ErrorCategoryLoad ErrorCategory = "load_error"
	// ErrorCategoryApply means a component failed to apply the configuration.
	// Components that had already applied it, if any, were successfully
	// rolled back to the last known-good configuration.
	ErrorCategoryApply ErrorCategory = "apply_error"
	// ErrorCategoryRollback means a component failed to apply the
	// configuration and rolling back to the last known-good configuration
	// failed too, so components may be running different configurations.
	ErrorCategoryRollback ErrorCategory = "rollback_error"
)

func (c ErrorCategory) valid() bool {
	switch c {
	case ErrorCategoryNone, ErrorCategoryLoad, ErrorCategoryApply, ErrorCategoryRollback:
		return true
	}
	return false
}

// Status is the outcome of a configuration reload attempt.
type Status struct {
	// LastReloadID is the start time of the attempt in RFC 3339 format. It is
	// empty if no reload has been attempted yet.
	LastReloadID         string        `json:"last_reload_id"`
	LastReloadSuccessful bool          `json:"last_reload_successful"`
	ErrorCategory        ErrorCategory `json:"error_category"`
	ErrorMessage         string        `json:"error_message"`
	// AppliedReloaders lists, in order, the reloaders that applied the new
	// configuration.
	AppliedReloaders   []string `json:"applied_reloaders"`
	RollbackAttempted  bool     `json:"rollback_attempted"`
	RollbackSuccessful bool     `json:"rollback_successful"`
	FailedReloader     string   `json:"failed_reloader"`
	// ReloaderTimingsMs holds, for every reloader that ran, the time it took
	// to apply the new configuration in milliseconds.
	ReloaderTimingsMs map[string]int64 `json:"reloader_timings_ms"`
}

// NewStatus returns the status of a reload attempt identified by id before
// any reloader ran. An empty id yields the status reported before the first
// reload attempt.
func NewStatus(id string) Status {
	return Status{
		LastReloadID:      id,
		ErrorCategory:     ErrorCategoryNone,
		AppliedReloaders:  []string{},
		ReloaderTimingsMs: map[string]int64{},
	}
}

// clone returns a deep copy of s whose collections are never nil, so that
// they are encoded as empty JSON arrays and objects rather than null.
func (s Status) clone() Status {
	s.AppliedReloaders = slices.Clone(s.AppliedReloaders)
	if s.AppliedReloaders == nil {
		s.AppliedReloaders = []string{}
	}
	s.ReloaderTimingsMs = maps.Clone(s.ReloaderTimingsMs)
	if s.ReloaderTimingsMs == nil {
		s.ReloaderTimingsMs = map[string]int64{}
	}
	return s
}

func (s Status) validate() error {
	if !s.ErrorCategory.valid() {
		return fmt.Errorf("invalid error category %q", s.ErrorCategory)
	}
	if s.LastReloadID != "" {
		if _, err := time.Parse(time.RFC3339, s.LastReloadID); err != nil {
			return fmt.Errorf("invalid reload ID %q: %w", s.LastReloadID, err)
		}
	}
	return nil
}

// Store holds the outcome of the most recent reload attempt and persists it
// as JSON in a directory. It is safe for concurrent use.
type Store struct {
	path   string
	logger *slog.Logger

	// writeMtx serializes Set calls so that the persisted status always
	// matches the in-memory one.
	writeMtx sync.Mutex
	mtx      sync.RWMutex
	status   Status
}

// NewStore returns a Store persisting to Filename in dir, initialized with the
// status persisted there. A missing, unreadable or corrupted file is logged
// and otherwise ignored: the Store then starts with the status reported
// before the first reload attempt.
func NewStore(dir string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = promslog.NewNopLogger()
	}
	s := &Store{
		path:   filepath.Join(dir, Filename),
		logger: logger,
		status: NewStatus(""),
	}

	st, err := readStatusFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		logger.Warn("Ignoring unusable persisted configuration reload status", "file", s.path, "err", err)
	default:
		s.status = st
	}
	return s
}

// Get returns the outcome of the most recent reload attempt.
func (s *Store) Get() Status {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.status.clone()
}

// Set records st as the outcome of the most recent reload attempt and
// persists it. Get returns st even if persisting it fails.
func (s *Store) Set(st Status) error {
	st = st.clone()

	s.writeMtx.Lock()
	defer s.writeMtx.Unlock()

	s.mtx.Lock()
	s.status = st
	s.mtx.Unlock()

	if err := writeStatusFile(s.path, st); err != nil {
		return fmt.Errorf("persist configuration reload status to %q: %w", s.path, err)
	}
	return nil
}

func readStatusFile(path string) (Status, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Status{}, err
	}
	var st Status
	if err := json.Unmarshal(b, &st); err != nil {
		return Status{}, err
	}
	if err := st.validate(); err != nil {
		return Status{}, err
	}
	return st.clone(), nil
}

// writeStatusFile atomically replaces the file at path with st.
func writeStatusFile(path string, st Status) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	tmp := path + ".tmp"
	defer os.Remove(tmp)

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	return fileutil.Replace(tmp, path)
}
