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
	"maps"
	"slices"
)

// Reload error categories reported by GET /api/v1/status/reload.
const (
	ReloadErrorCategoryNone     = "none"
	ReloadErrorCategoryLoad     = "load_error"
	ReloadErrorCategoryApply    = "apply_error"
	ReloadErrorCategoryRollback = "rollback_error"
)

// ReloadStatus is the outcome of the most recent configuration reload attempt.
// The same shape is returned by GET /api/v1/status/reload and persisted as JSON.
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
		ErrorCategory:     ReloadErrorCategoryNone,
		AppliedReloaders:  []string{},
		ReloaderTimingsMS: map[string]int64{},
	}
}

// ValidReloadErrorCategory reports whether category is one of the supported values.
func ValidReloadErrorCategory(category string) bool {
	switch category {
	case ReloadErrorCategoryNone, ReloadErrorCategoryLoad, ReloadErrorCategoryApply, ReloadErrorCategoryRollback:
		return true
	default:
		return false
	}
}

// Normalized returns a copy safe to serve and persist. Empty collections are
// non-nil so JSON encodes them as [] and {} rather than null.
func (s ReloadStatus) Normalized() ReloadStatus {
	if !ValidReloadErrorCategory(s.ErrorCategory) {
		s.ErrorCategory = ReloadErrorCategoryNone
	}
	if s.AppliedReloaders == nil {
		s.AppliedReloaders = []string{}
	} else {
		s.AppliedReloaders = slices.Clone(s.AppliedReloaders)
	}
	if s.ReloaderTimingsMS == nil {
		s.ReloaderTimingsMS = map[string]int64{}
	} else {
		s.ReloaderTimingsMS = maps.Clone(s.ReloaderTimingsMS)
	}
	return s
}
