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

import "net/http"

// Reload error categories reported by GET /api/v1/status/reload.
const (
	ReloadErrorNone     = "none"
	ReloadErrorLoad     = "load_error"
	ReloadErrorApply    = "apply_error"
	ReloadErrorRollback = "rollback_error"
)

// ReloadStatus is the outcome of the most recent configuration reload attempt.
// Before any reload has been attempted the zero-response values are used:
// an empty id, success false, category "none", and empty applied/timing collections.
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

// EmptyReloadStatus returns the status served before the first reload attempt.
func EmptyReloadStatus() ReloadStatus {
	return ReloadStatus{
		ErrorCategory:     ReloadErrorNone,
		AppliedReloaders:  []string{},
		ReloaderTimingsMS: map[string]int64{},
	}
}

// ValidReloadErrorCategory reports whether category is one of the accepted values.
func ValidReloadErrorCategory(category string) bool {
	switch category {
	case ReloadErrorNone, ReloadErrorLoad, ReloadErrorApply, ReloadErrorRollback:
		return true
	default:
		return false
	}
}

// Clone returns a copy safe for callers to retain.
func (s ReloadStatus) Clone() ReloadStatus {
	out := s
	if len(s.AppliedReloaders) == 0 {
		out.AppliedReloaders = []string{}
	} else {
		out.AppliedReloaders = append([]string(nil), s.AppliedReloaders...)
	}
	out.ReloaderTimingsMS = make(map[string]int64, len(s.ReloaderTimingsMS))
	for name, ms := range s.ReloaderTimingsMS {
		out.ReloaderTimingsMS[name] = ms
	}
	if out.ErrorCategory == "" {
		out.ErrorCategory = ReloadErrorNone
	}
	return out
}

func (api *API) serveReloadStatus(*http.Request) apiFuncResult {
	if api.reloadStatus == nil {
		return apiFuncResult{EmptyReloadStatus(), nil, nil, nil}
	}
	return apiFuncResult{api.reloadStatus(), nil, nil, nil}
}
