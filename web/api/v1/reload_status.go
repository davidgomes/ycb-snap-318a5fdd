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

package v1

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
)

const (
	// ReloadStatusFilename is the JSON file, under the TSDB storage directory,
	// that stores the most recent transactional reload outcome.
	ReloadStatusFilename = "reload_status.json"

	// ReloadErrorNone means the last attempt succeeded, or no attempt has been recorded.
	ReloadErrorNone = "none"
	// ReloadErrorLoad means configuration loading or parsing failed. Rollback is not attempted.
	ReloadErrorLoad = "load_error"
	// ReloadErrorApply means at least one reloader failed while applying a parsed configuration.
	ReloadErrorApply = "apply_error"
	// ReloadErrorRollback means applying failed and restoring the last known-good configuration also failed.
	ReloadErrorRollback = "rollback_error"
)

// ReloadStatus is the most recent transactional configuration reload outcome.
// The same shape is served by GET /api/v1/status/reload and persisted as JSON.
type ReloadStatus struct {
	LastReloadID         string           `json:"last_reload_id"`
	LastReloadSuccessful bool             `json:"last_reload_successful"`
	ErrorCategory        string           `json:"error_category"`
	ErrorMessage         string           `json:"error_message"`
	AppliedReloaders     []string         `json:"applied_reloaders"`
	RollbackAttempted    bool             `json:"rollback_attempted"`
	RollbackSuccessful   bool             `json:"rollback_successful"`
	FailedReloader       string           `json:"failed_reloader"`
	ReloaderTimingsMS    map[string]int64 `json:"reloader_timings_ms"`
}

// EmptyReloadStatus is the outcome reported before any reload attempt.
func EmptyReloadStatus() ReloadStatus {
	return ReloadStatus{
		ErrorCategory:     ReloadErrorNone,
		AppliedReloaders:  []string{},
		ReloaderTimingsMS: map[string]int64{},
	}
}

// ReloadStatusPath returns the persisted outcome path for a storage directory.
func ReloadStatusPath(dir string) string {
	return filepath.Join(dir, ReloadStatusFilename)
}

// ValidReloadErrorCategory reports whether category is one of the supported values.
func ValidReloadErrorCategory(category string) bool {
	switch category {
	case ReloadErrorNone, ReloadErrorLoad, ReloadErrorApply, ReloadErrorRollback:
		return true
	default:
		return false
	}
}

// Clone returns a deep copy safe to hand to HTTP handlers while a reload updates state.
func (s ReloadStatus) Clone() ReloadStatus {
	s = s.normalized()
	applied := make([]string, len(s.AppliedReloaders))
	copy(applied, s.AppliedReloaders)
	timings := make(map[string]int64, len(s.ReloaderTimingsMS))
	maps.Copy(timings, s.ReloaderTimingsMS)
	s.AppliedReloaders = applied
	s.ReloaderTimingsMS = timings
	return s
}

func (s ReloadStatus) normalized() ReloadStatus {
	if s.ErrorCategory == "" {
		s.ErrorCategory = ReloadErrorNone
	}
	if s.AppliedReloaders == nil {
		s.AppliedReloaders = []string{}
	}
	if s.ReloaderTimingsMS == nil {
		s.ReloaderTimingsMS = map[string]int64{}
	}
	return s
}

// LoadReloadStatus reads the persisted outcome. A missing file yields the empty
// pre-reload status and a nil error. Corrupt contents yield the empty status and an error.
func LoadReloadStatus(dir string) (ReloadStatus, error) {
	buf, err := os.ReadFile(ReloadStatusPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return EmptyReloadStatus(), nil
		}
		return EmptyReloadStatus(), err
	}

	var status ReloadStatus
	if err := json.Unmarshal(buf, &status); err != nil {
		return EmptyReloadStatus(), fmt.Errorf("corrupt reload status: %w", err)
	}
	if !ValidReloadErrorCategory(status.ErrorCategory) {
		return EmptyReloadStatus(), fmt.Errorf("corrupt reload status: invalid error_category %q", status.ErrorCategory)
	}
	return status.normalized(), nil
}

// SaveReloadStatus atomically writes the outcome under the storage directory.
func SaveReloadStatus(dir string, status ReloadStatus) error {
	buf, err := json.MarshalIndent(status.normalized(), "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')

	tmp := filepath.Join(dir, ".reload_status.json.tmp")
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ReloadStatusPath(dir))
}
