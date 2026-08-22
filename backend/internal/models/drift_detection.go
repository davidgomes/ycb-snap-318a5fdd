package models

import (
	"encoding/json"
	"time"
)

type ContainerConfig struct {
	Image         string            `json:"image"`
	RestartPolicy string            `json:"restartPolicy"`
	NetworkMode   string            `json:"networkMode"`
	Env           []string          `json:"env"`
	Ports         []string          `json:"ports"`
	Volumes       []string          `json:"volumes"`
	Labels        map[string]string `json:"labels"`
	MemoryLimit   int64             `json:"memoryLimit"`
	CpuLimit      float64           `json:"cpuLimit"`
}

type EnvironmentBaseline struct {
	BaseModel
	EnvironmentID    string    `json:"environmentId" gorm:"column:environment_id;index"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	CreatedBy        string    `json:"createdBy"`
	ContainerConfigs JSON      `json:"containerConfigs" gorm:"column:container_configs;type:text"`
	CapturedAt       time.Time `json:"capturedAt"`
	ContainerCount   int       `json:"containerCount"`
	IsActive         bool      `json:"isActive"`
}

func (EnvironmentBaseline) TableName() string { return "environment_baselines" }

func (b *EnvironmentBaseline) GetContainerConfigs() (map[string]ContainerConfig, error) {
	result := make(map[string]ContainerConfig)
	raw, err := json.Marshal(b.ContainerConfigs)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return result, nil
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

func (b *EnvironmentBaseline) SetContainerConfigs(configs map[string]ContainerConfig) error {
	raw, err := json.Marshal(configs)
	if err != nil {
		return err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	b.ContainerConfigs = JSON(value)
	return nil
}

type DriftRecord struct {
	BaseModel
	BaselineID    string     `json:"baselineId" gorm:"column:baseline_id;index"`
	EnvironmentID string     `json:"environmentId" gorm:"column:environment_id"`
	ContainerName string     `json:"containerName"`
	ContainerID   string     `json:"containerId"`
	DriftType     string     `json:"driftType"`
	Field         string     `json:"field"`
	ExpectedValue string     `json:"expectedValue"`
	ActualValue   string     `json:"actualValue"`
	Severity      string     `json:"severity"`
	Status        string     `json:"status"`
	DetectedAt    time.Time  `json:"detectedAt"`
	ResolvedAt    *time.Time `json:"resolvedAt,omitempty"`
}

func (DriftRecord) TableName() string { return "drift_records" }

type ComplianceSnapshot struct {
	BaseModel
	EnvironmentID       string  `json:"environmentId" gorm:"column:environment_id"`
	BaselineID          string  `json:"baselineId" gorm:"column:baseline_id"`
	TotalContainers     int     `json:"totalContainers"`
	CompliantContainers int     `json:"compliantContainers"`
	DriftedContainers   int     `json:"driftedContainers"`
	MissingContainers   int     `json:"missingContainers"`
	AddedContainers     int     `json:"addedContainers"`
	CriticalDrifts      int     `json:"criticalDrifts"`
	HighDrifts          int     `json:"highDrifts"`
	MediumDrifts        int     `json:"mediumDrifts"`
	LowDrifts           int     `json:"lowDrifts"`
	ComplianceScore     float64 `json:"complianceScore"`
}

func (ComplianceSnapshot) TableName() string { return "compliance_snapshots" }
