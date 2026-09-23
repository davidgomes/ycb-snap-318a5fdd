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

// ReloadStatus is the most recent configuration reload outcome.
// Before any reload attempt the zero status is returned: an empty id,
// last_reload_successful false, error_category "none", and empty collections.
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

// EmptyReloadStatus is the status served before the first reload attempt.
func EmptyReloadStatus() ReloadStatus {
	return ReloadStatus{
		ErrorCategory:     "none",
		AppliedReloaders:  []string{},
		ReloaderTimingsMS: map[string]int64{},
	}
}

// Clone returns a copy safe to share across goroutines.
func (s ReloadStatus) Clone() ReloadStatus {
	out := s
	out.AppliedReloaders = append([]string{}, s.AppliedReloaders...)
	if out.AppliedReloaders == nil {
		out.AppliedReloaders = []string{}
	}
	out.ReloaderTimingsMS = make(map[string]int64, len(s.ReloaderTimingsMS))
	for k, v := range s.ReloaderTimingsMS {
		out.ReloaderTimingsMS[k] = v
	}
	return out
}
