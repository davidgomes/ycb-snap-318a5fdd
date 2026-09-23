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
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/util/reloadstatus"
)

func TestServeReloadStatus(t *testing.T) {
	api := &API{}
	req, err := http.NewRequest(http.MethodGet, "/api/v1/status/reload", nil)
	require.NoError(t, err)

	res := api.serveReloadStatus(req)
	require.Nil(t, res.err)
	require.Equal(t, reloadstatus.Initial(), res.data)

	want := reloadstatus.Status{
		LastReloadID:      "2026-09-23T11:11:00Z",
		ErrorCategory:     reloadstatus.ErrorCategoryApply,
		AppliedReloaders:  []string{"a"},
		FailedReloader:    "b",
		RollbackAttempted: true,
	}
	api.SetReloadStatusFunc(func() reloadstatus.Status { return want })
	res = api.serveReloadStatus(req)
	require.Nil(t, res.err)
	require.Equal(t, want, res.data)
}
