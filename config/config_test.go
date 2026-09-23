package config

import (
	"os"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestLoadConfig(t *testing.T) {
	configContent := `
[global]
refresh_interval = 30
timeout = 15
follow_redirects = false
receive_alert = false

[[targets]]
url = "https://example.com"
name = "Example"
refresh_interval = 60
timeout = 20
method = "POST"
`

	tmpFile, err := os.CreateTemp("", "test-config-*.toml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			t.Logf("Failed to remove temp file: %v", err)
		}
	}()

	if _, err := tmpFile.WriteString(configContent); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	config, err := LoadConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if config.Global.RefreshInterval != 30 {
		t.Errorf("Expected RefreshInterval=30, got %d", config.Global.RefreshInterval)
	}
	if config.Global.Timeout != 15 {
		t.Errorf("Expected Timeout=15, got %d", config.Global.Timeout)
	}

	if len(config.Targets) != 1 {
		t.Fatalf("Expected 1 target, got %d", len(config.Targets))
	}

	target := config.Targets[0]
	if target.URL != "https://example.com" {
		t.Errorf("Expected URL=https://example.com, got %s", target.URL)
	}
	if target.Name != "Example" {
		t.Errorf("Expected Name=Example, got %s", target.Name)
	}
	if target.RefreshInterval != 60 {
		t.Errorf("Expected RefreshInterval=60, got %d", target.RefreshInterval)
	}
	if target.Method != "POST" {
		t.Errorf("Expected Method=POST, got %s", target.Method)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	configContent := `
[[targets]]
url = "https://example.com"
`

	tmpFile, err := os.CreateTemp("", "test-config-defaults-*.toml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			t.Logf("Failed to remove temp file: %v", err)
		}
	}()

	if _, err := tmpFile.WriteString(configContent); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	config, err := LoadConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	target := config.Targets[0]
	if target.RefreshInterval != _defaultRefreshInterval {
		t.Errorf("Expected default RefreshInterval=%d, got %d", _defaultRefreshInterval, target.RefreshInterval)
	}
	if target.Timeout != _defaultTimeout {
		t.Errorf("Expected default Timeout=%d, got %d", _defaultTimeout, target.Timeout)
	}
	if target.Method != _defaultMethod {
		t.Errorf("Expected default Method=%s, got %s", _defaultMethod, target.Method)
	}
}

func TestTargetGetMethods(t *testing.T) {
	target := Target{
		RefreshInterval: 30,
		Timeout:         15,
	}

	expectedRefresh := 30 * time.Second
	if got := target.GetRefreshInterval(); got != expectedRefresh {
		t.Errorf("GetRefreshInterval() = %v, want %v", got, expectedRefresh)
	}

	expectedTimeout := 15 * time.Second
	if got := target.GetTimeout(); got != expectedTimeout {
		t.Errorf("GetTimeout() = %v, want %v", got, expectedTimeout)
	}
}

func TestGlobalGetMethods(t *testing.T) {
	global := Global{
		RefreshInterval: 45,
		Timeout:         25,
	}

	expectedRefresh := 45 * time.Second
	if got := global.GetRefreshInterval(); got != expectedRefresh {
		t.Errorf("GetRefreshInterval() = %v, want %v", got, expectedRefresh)
	}

	expectedTimeout := 25 * time.Second
	if got := global.GetTimeout(); got != expectedTimeout {
		t.Errorf("GetTimeout() = %v, want %v", got, expectedTimeout)
	}
}

func TestFilterTargets(t *testing.T) {
	config := &Config{
		Targets: []Target{
			{URL: "https://example.com", Name: "Example"},
			{URL: "https://google.com", Name: "Google"},
			{URL: "https://github.com", Name: "GitHub"},
			{URL: "https://unnamed.com"},
		},
	}

	tests := []struct {
		name      string
		only      []string
		skip      []string
		expected  int
		expectURL string
	}{
		{
			name:     "no filters returns all",
			expected: 4,
		},
		{
			name:      "only by name",
			only:      []string{"Example"},
			expected:  1,
			expectURL: "https://example.com",
		},
		{
			name:      "only by URL",
			only:      []string{"https://google.com"},
			expected:  1,
			expectURL: "https://google.com",
		},
		{
			name:     "skip by name",
			skip:     []string{"Google"},
			expected: 3,
		},
		{
			name:     "skip by URL for unnamed target",
			skip:     []string{"https://unnamed.com"},
			expected: 3,
		},
		{
			name:     "only multiple",
			only:     []string{"Example", "GitHub"},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := config.FilterTargets(tt.only, tt.skip)
			if len(result) != tt.expected {
				t.Errorf("Expected %d targets, got %d", tt.expected, len(result))
			}
			if tt.expectURL != "" && len(result) > 0 {
				if result[0].URL != tt.expectURL {
					t.Errorf("Expected URL %s, got %s", tt.expectURL, result[0].URL)
				}
			}
		})
	}
}

func TestGetTargetName(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		expected string
	}{
		{
			name:     "with name",
			target:   Target{Name: "Example", URL: "https://example.com"},
			expected: "Example",
		},
		{
			name:     "without name",
			target:   Target{URL: "https://example.com"},
			expected: "https://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getTargetName(tt.target)
			if got != tt.expected {
				t.Errorf("getTargetName() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestContainsTarget(t *testing.T) {
	tests := []struct {
		name     string
		list     []string
		target   string
		url      string
		expected bool
	}{
		{
			name:     "found by target name",
			list:     []string{"Example", "Google"},
			target:   "Example",
			url:      "https://example.com",
			expected: true,
		},
		{
			name:     "found by URL",
			list:     []string{"Example", "https://google.com"},
			target:   "Google",
			url:      "https://google.com",
			expected: true,
		},
		{
			name:     "not found",
			list:     []string{"Example", "Google"},
			target:   "GitHub",
			url:      "https://github.com",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsTarget(tt.list, tt.target, tt.url)
			if got != tt.expected {
				t.Errorf("containsTarget() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAlertPolicyInheritanceAndDefaults(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	configContent := `
[global]
refresh_interval = 5

[global.alert_policy]
consecutive_failures = 4
consecutive_recoveries = 3
cooldown_seconds = 45
latency_threshold_ms = 800
latency_breach_count = 5
ssl_expiry_threshold_days = 21

[[targets]]
url = "https://inherit.example"
name = "Inherit"

[[targets]]
url = "https://override.example"
name = "Override"

[targets.alert_policy]
consecutive_failures = 2
latency_threshold_ms = 250

[[targets]]
url = "https://latency-default.example"
name = "LatencyDefault"

[targets.alert_policy]
latency_threshold_ms = 100
`
	tmpFile, err := os.CreateTemp("", "test-alert-policy-*.toml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			t.Logf("Failed to remove temp file: %v", err)
		}
	}()
	if _, err := tmpFile.WriteString(configContent); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	loaded, err := LoadConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	global := loaded.Global.AlertPolicy
	if global.ConsecutiveFailures != 4 || global.ConsecutiveRecoveries != 3 || global.CooldownSeconds != 45 {
		t.Fatalf("global policy = %+v", global)
	}
	if global.LatencyThresholdMs != 800 || global.LatencyBreachCount != 5 || global.SSLExpiryThresholdDays != 21 {
		t.Fatalf("global latency/ssl = %+v", global)
	}

	if len(loaded.Targets) != 3 {
		t.Fatalf("targets = %d", len(loaded.Targets))
	}

	inherited := loaded.Targets[0].AlertPolicy
	if inherited != global {
		t.Fatalf("inherited = %+v, global = %+v", inherited, global)
	}

	overridden := loaded.Targets[1].AlertPolicy
	if overridden.ConsecutiveFailures != 2 || overridden.LatencyThresholdMs != 250 {
		t.Fatalf("override = %+v", overridden)
	}
	if overridden.ConsecutiveRecoveries != 3 || overridden.CooldownSeconds != 45 || overridden.LatencyBreachCount != 5 || overridden.SSLExpiryThresholdDays != 21 {
		t.Fatalf("override should inherit unset fields: %+v", overridden)
	}

	latencyDefault := loaded.Targets[2].AlertPolicy
	if latencyDefault.LatencyThresholdMs != 100 || latencyDefault.LatencyBreachCount != 5 {
		t.Fatalf("latency default inherited breach count = %+v", latencyDefault)
	}

	runtime := overridden.ToAlertsPolicy()
	if runtime.ConsecutiveFailures != 2 || runtime.Cooldown != 45*time.Second || runtime.LatencyThreshold != 250*time.Millisecond {
		t.Fatalf("runtime policy = %+v", runtime)
	}
}

func TestAlertPolicyDefaultsWhenUnset(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	configContent := `
[[targets]]
url = "https://example.com"
name = "Example"

[[targets]]
url = "https://slow.example"
name = "Slow"

[targets.alert_policy]
latency_threshold_ms = 300
`
	tmpFile, err := os.CreateTemp("", "test-alert-defaults-*.toml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			t.Logf("Failed to remove temp file: %v", err)
		}
	}()
	if _, err := tmpFile.WriteString(configContent); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	loaded, err := LoadConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	policy := loaded.Targets[0].AlertPolicy
	if policy.ConsecutiveFailures != 1 || policy.ConsecutiveRecoveries != 1 {
		t.Fatalf("defaults = %+v", policy)
	}
	if policy.LatencyThresholdMs != 0 || policy.LatencyBreachCount != 0 || policy.SSLExpiryThresholdDays != 0 || policy.CooldownSeconds != 0 {
		t.Fatalf("alerting should stay disabled: %+v", policy)
	}
	if loaded.Global.AlertPolicy.ConsecutiveFailures != 1 || loaded.Global.AlertPolicy.ConsecutiveRecoveries != 1 {
		t.Fatalf("global defaults = %+v", loaded.Global.AlertPolicy)
	}

	slow := loaded.Targets[1].AlertPolicy
	if slow.LatencyThresholdMs != 300 || slow.LatencyBreachCount != 1 {
		t.Fatalf("breach count default = %+v", slow)
	}
	if slow.ConsecutiveFailures != 1 || slow.SSLExpiryThresholdDays != 0 {
		t.Fatalf("other defaults = %+v", slow)
	}
}
