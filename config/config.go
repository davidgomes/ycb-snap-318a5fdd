package config

import (
	"time"

	"github.com/Owloops/updo/alerts"
	"github.com/spf13/viper"
)

const (
	_defaultRefreshInterval       = 5
	_defaultTimeout               = 10
	_defaultMethod                = "GET"
	_defaultConsecutiveFailures   = 1
	_defaultConsecutiveRecoveries = 1
	_defaultLatencyBreachCount    = 1
)

// AlertPolicy is the configured alerting behavior for a target.
// Zero numeric fields inherit the global policy. After inheritance,
// consecutive failure and recovery counts default to 1. Latency alerting
// stays disabled unless LatencyThresholdMs is positive; a non-positive
// LatencyBreachCount is then treated as 1. SSL expiry alerting stays
// disabled unless SSLExpiryThresholdDays is positive.
type AlertPolicy struct {
	ConsecutiveFailures    int `mapstructure:"consecutive_failures"`
	ConsecutiveRecoveries  int `mapstructure:"consecutive_recoveries"`
	CooldownSeconds        int `mapstructure:"cooldown_seconds"`
	LatencyThresholdMs     int `mapstructure:"latency_threshold_ms"`
	LatencyBreachCount     int `mapstructure:"latency_breach_count"`
	SSLExpiryThresholdDays int `mapstructure:"ssl_expiry_threshold_days"`
}

type Target struct {
	URL             string      `mapstructure:"url"`
	Name            string      `mapstructure:"name"`
	RefreshInterval int         `mapstructure:"refresh_interval"`
	Timeout         int         `mapstructure:"timeout"`
	ShouldFail      bool        `mapstructure:"should_fail"`
	FollowRedirects bool        `mapstructure:"follow_redirects"`
	AcceptRedirects bool        `mapstructure:"accept_redirects"`
	SkipSSL         bool        `mapstructure:"skip_ssl"`
	AssertText      string      `mapstructure:"assert_text"`
	ReceiveAlert    bool        `mapstructure:"receive_alert"`
	Headers         []string    `mapstructure:"headers"`
	Method          string      `mapstructure:"method"`
	Body            string      `mapstructure:"body"`
	WebhookURL      string      `mapstructure:"webhook_url"`
	WebhookHeaders  []string    `mapstructure:"webhook_headers"`
	Regions         []string    `mapstructure:"regions"`
	AlertPolicy     AlertPolicy `mapstructure:"alert_policy"`
}

type Global struct {
	RefreshInterval int         `mapstructure:"refresh_interval"`
	Timeout         int         `mapstructure:"timeout"`
	ShouldFail      bool        `mapstructure:"should_fail"`
	FollowRedirects bool        `mapstructure:"follow_redirects"`
	AcceptRedirects bool        `mapstructure:"accept_redirects"`
	SkipSSL         bool        `mapstructure:"skip_ssl"`
	ReceiveAlert    bool        `mapstructure:"receive_alert"`
	Count           int         `mapstructure:"count"`
	Simple          bool        `mapstructure:"simple"`
	Log             bool        `mapstructure:"log"`
	Only            []string    `mapstructure:"only"`
	Skip            []string    `mapstructure:"skip"`
	WebhookURL      string      `mapstructure:"webhook_url"`
	WebhookHeaders  []string    `mapstructure:"webhook_headers"`
	Regions         []string    `mapstructure:"regions"`
	AlertPolicy     AlertPolicy `mapstructure:"alert_policy"`
}

type Config struct {
	Global  Global   `mapstructure:"global"`
	Targets []Target `mapstructure:"targets"`
}

func LoadConfig(configFile string) (*Config, error) {
	viper.SetConfigFile(configFile)
	viper.SetConfigType("toml")

	viper.SetDefault("global.refresh_interval", _defaultRefreshInterval)
	viper.SetDefault("global.timeout", _defaultTimeout)
	viper.SetDefault("global.follow_redirects", true)
	viper.SetDefault("global.receive_alert", true)
	viper.SetDefault("global.count", 0)
	viper.SetDefault("global.method", _defaultMethod)

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	for i := range config.Targets {
		target := &config.Targets[i]
		if target.RefreshInterval == 0 {
			target.RefreshInterval = config.Global.RefreshInterval
		}
		if target.Timeout == 0 {
			target.Timeout = config.Global.Timeout
		}
		if target.Method == "" {
			target.Method = _defaultMethod
		}
		if !target.FollowRedirects && config.Global.FollowRedirects {
			target.FollowRedirects = config.Global.FollowRedirects
		}
		if !target.AcceptRedirects && config.Global.AcceptRedirects {
			target.AcceptRedirects = config.Global.AcceptRedirects
		}
		if !target.ReceiveAlert && config.Global.ReceiveAlert {
			target.ReceiveAlert = config.Global.ReceiveAlert
		}
		if target.WebhookURL == "" && config.Global.WebhookURL != "" {
			target.WebhookURL = config.Global.WebhookURL
		}
		if len(target.WebhookHeaders) == 0 && len(config.Global.WebhookHeaders) > 0 {
			target.WebhookHeaders = config.Global.WebhookHeaders
		}
		if len(target.Regions) == 0 && len(config.Global.Regions) > 0 {
			target.Regions = config.Global.Regions
		}
	}

	applyAlertPolicyDefaults(&config.Global.AlertPolicy)
	for i := range config.Targets {
		inheritAlertPolicy(&config.Targets[i].AlertPolicy, config.Global.AlertPolicy)
		applyAlertPolicyDefaults(&config.Targets[i].AlertPolicy)
	}

	return &config, nil
}

func inheritAlertPolicy(dst *AlertPolicy, src AlertPolicy) {
	if dst.ConsecutiveFailures == 0 {
		dst.ConsecutiveFailures = src.ConsecutiveFailures
	}
	if dst.ConsecutiveRecoveries == 0 {
		dst.ConsecutiveRecoveries = src.ConsecutiveRecoveries
	}
	if dst.CooldownSeconds == 0 {
		dst.CooldownSeconds = src.CooldownSeconds
	}
	if dst.LatencyThresholdMs == 0 {
		dst.LatencyThresholdMs = src.LatencyThresholdMs
	}
	if dst.LatencyBreachCount == 0 {
		dst.LatencyBreachCount = src.LatencyBreachCount
	}
	if dst.SSLExpiryThresholdDays == 0 {
		dst.SSLExpiryThresholdDays = src.SSLExpiryThresholdDays
	}
}

func applyAlertPolicyDefaults(policy *AlertPolicy) {
	if policy.ConsecutiveFailures <= 0 {
		policy.ConsecutiveFailures = _defaultConsecutiveFailures
	}
	if policy.ConsecutiveRecoveries <= 0 {
		policy.ConsecutiveRecoveries = _defaultConsecutiveRecoveries
	}
	if policy.LatencyThresholdMs > 0 && policy.LatencyBreachCount <= 0 {
		policy.LatencyBreachCount = _defaultLatencyBreachCount
	}
}

// ToAlertsPolicy converts the configured policy into the runtime policy.
func (p AlertPolicy) ToAlertsPolicy() alerts.Policy {
	cooldown := time.Duration(0)
	if p.CooldownSeconds > 0 {
		cooldown = time.Duration(p.CooldownSeconds) * time.Second
	}
	latency := time.Duration(0)
	if p.LatencyThresholdMs > 0 {
		latency = time.Duration(p.LatencyThresholdMs) * time.Millisecond
	}
	return alerts.Policy{
		ConsecutiveFailures:    p.ConsecutiveFailures,
		ConsecutiveRecoveries:  p.ConsecutiveRecoveries,
		Cooldown:               cooldown,
		LatencyThreshold:       latency,
		LatencyBreachCount:     p.LatencyBreachCount,
		SSLExpiryThresholdDays: p.SSLExpiryThresholdDays,
	}
}

func (t *Target) GetRefreshInterval() time.Duration {
	return time.Duration(t.RefreshInterval) * time.Second
}

func (t *Target) GetTimeout() time.Duration {
	return time.Duration(t.Timeout) * time.Second
}

func (g *Global) GetRefreshInterval() time.Duration {
	return time.Duration(g.RefreshInterval) * time.Second
}

func (g *Global) GetTimeout() time.Duration {
	return time.Duration(g.Timeout) * time.Second
}

func (c *Config) FilterTargets(onlyFlags, skipFlags []string) []Target {
	only := onlyFlags
	skip := skipFlags

	if len(only) == 0 {
		only = c.Global.Only
	}
	if len(skip) == 0 {
		skip = c.Global.Skip
	}

	if len(only) == 0 && len(skip) == 0 {
		return c.Targets
	}

	var filtered []Target

	for _, target := range c.Targets {
		targetName := getTargetName(target)

		if len(only) > 0 && !containsTarget(only, targetName, target.URL) {
			continue
		}

		if len(skip) > 0 && containsTarget(skip, targetName, target.URL) {
			continue
		}

		filtered = append(filtered, target)
	}

	return filtered
}

func getTargetName(target Target) string {
	if target.Name != "" {
		return target.Name
	}
	return target.URL
}

func containsTarget(list []string, targetName, targetURL string) bool {
	for _, name := range list {
		if name == targetName || name == targetURL {
			return true
		}
	}
	return false
}
