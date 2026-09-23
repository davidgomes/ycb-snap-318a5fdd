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
	"testing"

	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/require"
)

func TestEmptyReloadStatusJSON(t *testing.T) {
	data, err := jsoniter.ConfigCompatibleWithStandardLibrary.Marshal(EmptyReloadStatus())
	require.NoError(t, err)
	require.JSONEq(t, `{
		"last_reload_id": "",
		"last_reload_successful": false,
		"error_category": "none",
		"error_message": "",
		"applied_reloaders": [],
		"rollback_attempted": false,
		"rollback_successful": false,
		"failed_reloader": "",
		"reloader_timings_ms": {}
	}`, string(data))
}

func TestReloadStatusNormalizedCopiesCollections(t *testing.T) {
	original := ReloadStatus{
		ErrorCategory:        ReloadErrorCategoryApply,
		AppliedReloaders:     []string{"db_storage"},
		ReloaderTimingsMS:    map[string]int64{"db_storage": 3},
		FailedReloader:       "scrape",
		LastReloadSuccessful: false,
	}
	copied := original.Normalized()
	copied.AppliedReloaders[0] = "changed"
	copied.ReloaderTimingsMS["db_storage"] = 9

	require.Equal(t, "db_storage", original.AppliedReloaders[0])
	require.Equal(t, int64(3), original.ReloaderTimingsMS["db_storage"])
}

func TestValidReloadErrorCategory(t *testing.T) {
	for _, category := range []string{
		ReloadErrorCategoryNone,
		ReloadErrorCategoryLoad,
		ReloadErrorCategoryApply,
		ReloadErrorCategoryRollback,
	} {
		require.True(t, ValidReloadErrorCategory(category), category)
	}
	require.False(t, ValidReloadErrorCategory(""))
	require.False(t, ValidReloadErrorCategory("other"))
}
