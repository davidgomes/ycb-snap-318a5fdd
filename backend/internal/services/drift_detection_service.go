package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getarcaneapp/arcane/backend/internal/database"
	"github.com/getarcaneapp/arcane/backend/internal/models"
	"github.com/moby/moby/client"
	"gorm.io/gorm"
)

type DriftDetectionService struct {
	db                  *database.DB
	dockerService       *DockerClientService
	containerService    *ContainerService
	eventService        *EventService
	settingsService     *SettingsService
	notificationService *NotificationService
}

func NewDriftDetectionService(db *database.DB, dockerService *DockerClientService, containerService *ContainerService, eventService *EventService, settingsService *SettingsService, notificationService *NotificationService) *DriftDetectionService {
	return &DriftDetectionService{db: db, dockerService: dockerService, containerService: containerService, eventService: eventService, settingsService: settingsService, notificationService: notificationService}
}

func (s *DriftDetectionService) CaptureBaselineFromConfigs(ctx context.Context, envID, name, description, userID string, configs map[string]models.ContainerConfig) (*models.EnvironmentBaseline, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database is required")
	}
	baseline := &models.EnvironmentBaseline{EnvironmentID: envID, Name: name, Description: description, CreatedBy: userID, CapturedAt: time.Now(), ContainerCount: len(configs), IsActive: true}
	if err := baseline.SetContainerConfigs(configs); err != nil {
		return nil, fmt.Errorf("marshal container configs: %w", err)
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.EnvironmentBaseline{}).Where("environment_id = ? AND is_active = ?", envID, true).Update("is_active", false).Error; err != nil {
			return err
		}
		return tx.Create(baseline).Error
	})
	if err != nil {
		return nil, fmt.Errorf("capture baseline: %w", err)
	}
	return baseline, nil
}

func (s *DriftDetectionService) GetBaseline(ctx context.Context, id string) (*models.EnvironmentBaseline, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database is required")
	}
	var b models.EnvironmentBaseline
	err := s.db.WithContext(ctx).First(&b, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *DriftDetectionService) ListBaselines(ctx context.Context, envID string, limit, offset int) ([]models.EnvironmentBaseline, int64, error) {
	var items []models.EnvironmentBaseline
	q := s.db.WithContext(ctx).Where("environment_id = ?", envID).Order("captured_at DESC")
	var total int64
	if err := q.Model(&models.EnvironmentBaseline{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	return items, total, q.Find(&items).Error
}

func (s *DriftDetectionService) SetActiveBaseline(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var b models.EnvironmentBaseline
		if err := tx.First(&b, "id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.EnvironmentBaseline{}).Where("environment_id = ?", b.EnvironmentID).Update("is_active", false).Error; err != nil {
			return err
		}
		return tx.Model(&b).Update("is_active", true).Error
	})
}

func (s *DriftDetectionService) DeleteBaseline(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("baseline_id = ?", id).Delete(&models.DriftRecord{}).Error; err != nil {
			return err
		}
		if err := tx.Where("baseline_id = ?", id).Delete(&models.ComplianceSnapshot{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.EnvironmentBaseline{}, "id = ?", id).Error
	})
}

func (s *DriftDetectionService) DetectDriftFromConfigs(ctx context.Context, envID string, current map[string]models.ContainerConfig) (*models.ComplianceSnapshot, error) {
	var baseline models.EnvironmentBaseline
	if err := s.db.WithContext(ctx).Where("environment_id = ? AND is_active = ?", envID, true).First(&baseline).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("no active baseline")
		}
		return nil, err
	}
	expected, err := baseline.GetContainerConfigs()
	if err != nil {
		return nil, err
	}
	var prior []models.DriftRecord
	if err := s.db.WithContext(ctx).Where("baseline_id = ?", baseline.ID).Find(&prior).Error; err != nil {
		return nil, err
	}
	existing := make(map[string]models.DriftRecord)
	for _, r := range prior {
		existing[driftKey(r.ContainerName, r.DriftType, r.Field)] = r
	}
	conditions := make(map[string]struct{})
	snapshot := &models.ComplianceSnapshot{EnvironmentID: envID, BaselineID: baseline.ID, TotalContainers: len(expected)}
	drifted := make(map[string]struct{})
	for name, want := range expected {
		got, ok := current[name]
		if !ok {
			s.addDrift(ctx, &baseline, existing, conditions, snapshot, name, "", "container_missing", "", "missing", "critical", drifted)
			snapshot.MissingContainers++
			continue
		}
		for _, c := range compareConfig(want, got) {
			s.addDrift(ctx, &baseline, existing, conditions, snapshot, name, c.field, c.typ, c.expected, c.actual, c.severity, drifted)
		}
	}
	for name := range current {
		if _, ok := expected[name]; !ok {
			s.addDrift(ctx, &baseline, existing, conditions, snapshot, name, "", "container_added", "absent", "present", "medium", drifted)
			snapshot.AddedContainers++
		}
	}
	snapshot.DriftedContainers = len(drifted)
	snapshot.CompliantContainers = len(expected) - snapshot.DriftedContainers
	if snapshot.CompliantContainers < 0 {
		snapshot.CompliantContainers = 0
	}
	if snapshot.TotalContainers == 0 {
		snapshot.ComplianceScore = 100
	} else {
		snapshot.ComplianceScore = float64(snapshot.CompliantContainers) / float64(snapshot.TotalContainers) * 100
	}
	now := time.Now()
	for _, r := range prior {
		if r.Status == "detected" {
			if _, ok := conditions[driftKey(r.ContainerName, r.DriftType, r.Field)]; !ok {
				r.Status = "resolved"
				r.ResolvedAt = &now
				if err := s.db.WithContext(ctx).Save(&r).Error; err != nil {
					return nil, err
				}
			}
		}
	}
	if err := s.db.WithContext(ctx).Create(snapshot).Error; err != nil {
		return nil, err
	}
	return snapshot, nil
}

type driftChange struct{ typ, field, expected, actual, severity string }

func compareConfig(a, b models.ContainerConfig) []driftChange {
	var out []driftChange
	if a.Image != b.Image {
		out = append(out, driftChange{"image_changed", "", a.Image, b.Image, "critical"})
	}
	if a.RestartPolicy != b.RestartPolicy {
		out = append(out, driftChange{"restart_policy_changed", "", a.RestartPolicy, b.RestartPolicy, "medium"})
	}
	if a.NetworkMode != b.NetworkMode {
		out = append(out, driftChange{"network_changed", "", a.NetworkMode, b.NetworkMode, "high"})
	}
	if !equalSorted(a.Env, b.Env) {
		out = append(out, driftChange{"env_changed", "", formatValue(a.Env), formatValue(b.Env), "high"})
	}
	if !equalSorted(a.Ports, b.Ports) {
		out = append(out, driftChange{"config_changed", "ports", formatValue(a.Ports), formatValue(b.Ports), "high"})
	}
	if !equalSorted(a.Volumes, b.Volumes) {
		out = append(out, driftChange{"config_changed", "volumes", formatValue(a.Volumes), formatValue(b.Volumes), "high"})
	}
	if !reflect.DeepEqual(a.Labels, b.Labels) {
		out = append(out, driftChange{"label_changed", "", formatValue(a.Labels), formatValue(b.Labels), "low"})
	}
	if a.MemoryLimit != b.MemoryLimit {
		out = append(out, driftChange{"resource_changed", "memoryLimit", strconv.FormatInt(a.MemoryLimit, 10), strconv.FormatInt(b.MemoryLimit, 10), "medium"})
	}
	if a.CpuLimit != b.CpuLimit {
		out = append(out, driftChange{"resource_changed", "cpuLimit", strconv.FormatFloat(a.CpuLimit, 'f', -1, 64), strconv.FormatFloat(b.CpuLimit, 'f', -1, 64), "medium"})
	}
	return out
}

func equalSorted(a, b []string) bool {
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}
func formatValue(v any) string                { b, _ := json.Marshal(v); return string(b) }
func driftKey(name, typ, field string) string { return name + "\x00" + typ + "\x00" + field }

func (s *DriftDetectionService) addDrift(ctx context.Context, b *models.EnvironmentBaseline, existing map[string]models.DriftRecord, conditions map[string]struct{}, snapshot *models.ComplianceSnapshot, name, field, typ, expected, actual, severity string, drifted map[string]struct{}) {
	key := driftKey(name, typ, field)
	conditions[key] = struct{}{}
	drifted[name] = struct{}{}
	switch severity {
	case "critical":
		snapshot.CriticalDrifts++
	case "high":
		snapshot.HighDrifts++
	case "medium":
		snapshot.MediumDrifts++
	case "low":
		snapshot.LowDrifts++
	}
	if record, ok := existing[key]; ok && record.Status != "resolved" {
		return
	}
	r := models.DriftRecord{BaselineID: b.ID, EnvironmentID: b.EnvironmentID, ContainerName: name, DriftType: typ, Field: field, ExpectedValue: expected, ActualValue: actual, Severity: severity, Status: "detected", DetectedAt: time.Now()}
	_ = s.db.WithContext(ctx).Create(&r).Error
}

func (s *DriftDetectionService) GetActiveDrifts(ctx context.Context, envID string) ([]models.DriftRecord, error) {
	var out []models.DriftRecord
	err := s.db.WithContext(ctx).Where("environment_id = ? AND status = ?", envID, "detected").Order("detected_at DESC").Find(&out).Error
	return out, err
}
func (s *DriftDetectionService) AcknowledgeDrift(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&models.DriftRecord{}).Where("id = ?", id).Update("status", "acknowledged").Error
}
func (s *DriftDetectionService) IgnoreDrift(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&models.DriftRecord{}).Where("id = ?", id).Update("status", "ignored").Error
}
func (s *DriftDetectionService) GetComplianceHistory(ctx context.Context, envID string, limit, offset int) ([]models.ComplianceSnapshot, error) {
	var out []models.ComplianceSnapshot
	q := s.db.WithContext(ctx).Where("environment_id = ?", envID).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	return out, q.Find(&out).Error
}
func (s *DriftDetectionService) GetDriftRecords(ctx context.Context, envID string, limit, offset int) ([]models.DriftRecord, int64, error) {
	var out []models.DriftRecord
	var total int64
	q := s.db.WithContext(ctx).Where("environment_id = ?", envID)
	if err := q.Model(&models.DriftRecord{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q = q.Order("detected_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	return out, total, q.Find(&out).Error
}

func (s *DriftDetectionService) IsEnabled(ctx context.Context) bool {
	return s == nil || s.settingsService == nil || s.settingsService.GetStringSetting(ctx, "driftDetectionEnabled", "true") != "false"
}

func (s *DriftDetectionService) RunAllEnvironments(ctx context.Context) error {
	if s == nil || s.dockerService == nil || s.containerService == nil || !s.IsEnabled(ctx) {
		return nil
	}
	var envIDs []string
	if err := s.db.WithContext(ctx).Model(&models.EnvironmentBaseline{}).Distinct("environment_id").Pluck("environment_id", &envIDs).Error; err != nil {
		return err
	}
	configs, err := s.liveConfigs(ctx)
	if err != nil {
		return err
	}
	for _, envID := range envIDs {
		if _, err := s.DetectDriftFromConfigs(ctx, envID, configs); err != nil && !strings.Contains(err.Error(), "no active baseline") {
			return err
		}
	}
	return nil
}

func (s *DriftDetectionService) liveConfigs(ctx context.Context) (map[string]models.ContainerConfig, error) {
	d, err := s.dockerService.GetClient(ctx)
	if err != nil {
		return nil, err
	}
	list, err := d.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make(map[string]models.ContainerConfig, len(list.Items))
	for _, item := range list.Items {
		name := strings.TrimPrefix(item.Names[0], "/")
		inspect, err := d.ContainerInspect(ctx, item.ID, client.ContainerInspectOptions{})
		if err != nil {
			return nil, err
		}
		cfg := models.ContainerConfig{Image: inspect.Container.Config.Image, Env: inspect.Container.Config.Env, Labels: inspect.Container.Config.Labels}
		if inspect.Container.HostConfig != nil {
			cfg.RestartPolicy = string(inspect.Container.HostConfig.RestartPolicy.Name)
			cfg.NetworkMode = string(inspect.Container.HostConfig.NetworkMode)
			cfg.Volumes = inspect.Container.HostConfig.Binds
			cfg.MemoryLimit = inspect.Container.HostConfig.Memory
			cfg.CpuLimit = float64(inspect.Container.HostConfig.NanoCPUs) / 1e9
		}
		out[name] = cfg
	}
	return out, nil
}
