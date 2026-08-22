package scheduler

import (
	"context"
	"log/slog"

	"github.com/getarcaneapp/arcane/backend/internal/services"
)

type DriftDetectionJob struct {
	driftService    *services.DriftDetectionService
	settingsService *services.SettingsService
}

func NewDriftDetectionJob(driftService *services.DriftDetectionService, settingsService *services.SettingsService) *DriftDetectionJob {
	return &DriftDetectionJob{driftService: driftService, settingsService: settingsService}
}

func (j *DriftDetectionJob) Name() string { return "drift-detection" }
func (j *DriftDetectionJob) Schedule(ctx context.Context) string {
	if j == nil || j.settingsService == nil {
		return "0 0 * * * *"
	}
	return j.settingsService.GetStringSetting(ctx, "driftDetectionInterval", "0 0 * * * *")
}
func (j *DriftDetectionJob) Run(ctx context.Context) {
	if j == nil || j.driftService == nil || !j.driftService.IsEnabled(ctx) {
		return
	}
	if err := j.driftService.RunAllEnvironments(ctx); err != nil {
		slog.ErrorContext(ctx, "drift detection run failed", "error", err)
	}
}
