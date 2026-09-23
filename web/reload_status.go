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

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/prometheus/common/promslog"

	"github.com/prometheus/prometheus/config"
	api_v1 "github.com/prometheus/prometheus/web/api/v1"
)

// ReloadStatusFileName is the JSON file, under the TSDB storage directory,
// that stores the most recent transactional reload outcome.
const ReloadStatusFileName = "reload_status.json"

// ReloadState tracks the last known-good configuration and the most recent
// reload outcome. A missing or unreadable status file does not prevent use.
type ReloadState struct {
	mu       sync.RWMutex
	status   api_v1.ReloadStatus
	lastGood *config.Config
	path     string
	logger   *slog.Logger
}

// NewReloadState loads a previously persisted outcome from storageDir.
// A missing or corrupted file yields the pre-reload status.
func NewReloadState(storageDir string, logger *slog.Logger) *ReloadState {
	if logger == nil {
		logger = promslog.NewNopLogger()
	}
	s := &ReloadState{
		status: api_v1.EmptyReloadStatus(),
		logger: logger,
	}
	if storageDir == "" {
		return s
	}
	s.path = filepath.Join(storageDir, ReloadStatusFileName)
	st, err := loadReloadStatus(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Warn("ignoring unreadable reload status; continuing with empty status", "path", s.path, "err", err)
		}
		return s
	}
	s.status = st
	return s
}

// Status returns a copy of the most recent reload outcome.
func (s *ReloadState) Status() api_v1.ReloadStatus {
	if s == nil {
		return api_v1.EmptyReloadStatus()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.Clone()
}

// SetLastGood remembers the configuration that was fully applied.
// This includes the configuration loaded at startup, before any reload.
func (s *ReloadState) SetLastGood(cfg *config.Config) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.lastGood = cfg
	s.mu.Unlock()
}

// LastGood returns the last configuration that was fully applied, or nil.
func (s *ReloadState) LastGood() *config.Config {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastGood
}

// Record stores the outcome in memory and persists it under the TSDB directory.
// A persistence failure is logged and does not discard the in-memory outcome.
func (s *ReloadState) Record(status api_v1.ReloadStatus) {
	if s == nil {
		return
	}
	status = status.Clone()
	s.mu.Lock()
	s.status = status
	path := s.path
	s.mu.Unlock()

	if path == "" {
		s.logger.Warn("not persisting reload status: storage directory is not configured")
		return
	}
	if err := writeReloadStatus(path, status); err != nil {
		s.logger.Error("failed to persist reload status", "path", path, "err", err)
	}
}

func loadReloadStatus(path string) (api_v1.ReloadStatus, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return api_v1.ReloadStatus{}, err
	}
	var status api_v1.ReloadStatus
	if err := json.Unmarshal(buf, &status); err != nil {
		return api_v1.ReloadStatus{}, fmt.Errorf("decode reload status: %w", err)
	}
	if !api_v1.ValidReloadErrorCategory(status.ErrorCategory) {
		return api_v1.ReloadStatus{}, fmt.Errorf("invalid reload status error_category %q", status.ErrorCategory)
	}
	return status.Clone(), nil
}

func writeReloadStatus(path string, status api_v1.ReloadStatus) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')

	tmp, err := os.CreateTemp(dir, ".reload_status-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
