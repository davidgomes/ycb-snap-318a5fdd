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

// Package reloadstatus tracks and persists the outcome of the most recent
// configuration reload attempt.
package reloadstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// FileName is the name of the state file inside the storage directory.
const FileName = "reload_status.json"

const (
	CategoryNone     = "none"
	CategoryLoad     = "load_error"
	CategoryApply    = "apply_error"
	CategoryRollback = "rollback_error"
)

// Status is the outcome of a reload attempt.
type Status struct {
	LastReloadID         string           `json:"last_reload_id"`
	LastReloadSuccessful bool             `json:"last_reload_successful"`
	ErrorCategory        string           `json:"error_category"`
	ErrorMessage         string           `json:"error_message"`
	AppliedReloaders     []string         `json:"applied_reloaders"`
	RollbackAttempted    bool             `json:"rollback_attempted"`
	RollbackSuccessful   bool             `json:"rollback_successful"`
	FailedReloader       string           `json:"failed_reloader"`
	ReloaderTimingsMs    map[string]int64 `json:"reloader_timings_ms"`
}

func initial() Status {
	return Status{ErrorCategory: CategoryNone, AppliedReloaders: []string{}, ReloaderTimingsMs: map[string]int64{}}
}

func normalize(s Status) Status {
	if s.AppliedReloaders == nil {
		s.AppliedReloaders = []string{}
	}
	if s.ReloaderTimingsMs == nil {
		s.ReloaderTimingsMs = map[string]int64{}
	}
	switch s.ErrorCategory {
	case CategoryNone, CategoryLoad, CategoryApply, CategoryRollback:
	default:
		s.ErrorCategory = CategoryNone
	}
	return s
}

// Tracker holds the latest status and persists it to dir.
type Tracker struct {
	mtx    sync.RWMutex
	dir    string
	status Status
}

// Default is the process-wide tracker served by the web API.
var Default = &Tracker{status: initial()}

// Init sets the storage directory and loads any persisted state.
// Missing or corrupted state is ignored.
func (t *Tracker) Init(dir string) {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	t.dir = dir
	t.status = initial()
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return
	}
	var s Status
	if json.Unmarshal(b, &s) != nil {
		return
	}
	t.status = normalize(s)
}

// Get returns a copy of the current status.
func (t *Tracker) Get() Status {
	t.mtx.RLock()
	defer t.mtx.RUnlock()
	s := t.status
	s.AppliedReloaders = append([]string{}, s.AppliedReloaders...)
	s.ReloaderTimingsMs = make(map[string]int64, len(t.status.ReloaderTimingsMs))
	for k, v := range t.status.ReloaderTimingsMs {
		s.ReloaderTimingsMs[k] = v
	}
	return s
}

// Record stores s and writes it atomically to disk.
func (t *Tracker) Record(s Status) error {
	s = normalize(s)
	t.mtx.Lock()
	defer t.mtx.Unlock()
	t.status = s
	if t.dir == "" {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(t.dir, 0o777); err != nil {
		return err
	}
	tmp := filepath.Join(t.dir, FileName+".tmp")
	if err := os.WriteFile(tmp, b, 0o666); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(t.dir, FileName))
}
