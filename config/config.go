package config

import (
	"time"

	"github.com/Owloops/updo/alerts"
	"github.com/spf13/viper"
)

const (
	_defaultRefreshInterval = 5
	_defaultTimeout         = 10
	_defaultMethod          = "GET"

	_alertConsecutiveFailures    = "consecutive_failures"
	_alertConsecutiveRecoveries  = "consecutive_recoveries"
	_alertCooldownSeconds        = "cooldown_seconds"
	_alertLatencyThresholdMs     = "latency_threshold_ms"
	_alertLatencyBreachCount     = "latency_breach_count"
	_alertSSLExpiryThresholdDays = "ssl_expiry_threshold_days"
)

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

// AlertPolicy is the configured alert behavior for a target.
// Zero consecutive counts default to 1. Latency alerting is off unless
// LatencyThresholdMs is positive. SSL expiry alerting is off unless
// SSLExpiryThresholdDays is positive.
type AlertPolicy struct {
	ConsecutiveFailures    int `mapstructure:"consecutive_failures"`
	ConsecutiveRecoveries  int `mapstructure:"consecutive_recoveries"`
	CooldownSeconds        int `mapstructure:"cooldown_seconds"`
	LatencyThresholdMs     int `mapstructure:"latency_threshold_ms"`
	LatencyBreachCount     int `mapstructure:"latency_breach_count"`
	SSLExpiryThresholdDays int `mapstructure:"ssl_expiry_threshold_days"`
}

func (p AlertPolicy) ToAlertsPolicy() alerts.Policy {
	return alerts.Policy{
		ConsecutiveFailures:    p.ConsecutiveFailures,
		ConsecutiveRecoveries:  p.ConsecutiveRecoveries,
		Cooldown:               time.Duration(p.CooldownSeconds) * time.Second,
		LatencyThreshold:       time.Duration(p.LatencyThresholdMs) * time.Millisecond,
		LatencyBreachCount:     p.LatencyBreachCount,
		SSLExpiryThresholdDays: p.SSLExpiryThresholdDays,
	}
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

	config.applyAlertPolicies()

	return &config, nil
}

func (c *Config) applyAlertPolicies() {
	global, targets := alertPoliciesFromSettings(viper.AllSettings())
	if len(targets) != len(c.Targets) {
		global = rawAlertPolicy{policy: c.Global.AlertPolicy, fields: alertFieldsSet(c.Global.AlertPolicy)}
		targets = make([]rawAlertPolicy, len(c.Targets))
		for i := range c.Targets {
			targets[i] = rawAlertPolicy{policy: c.Targets[i].AlertPolicy, fields: alertFieldsSet(c.Targets[i].AlertPolicy)}
		}
	}

	c.Global.AlertPolicy = normalizeAlertPolicy(global.policy)
	for i := range c.Targets {
		merged := overlayAlertPolicy(global.policy, targets[i])
		c.Targets[i].AlertPolicy = normalizeAlertPolicy(merged)
	}
}

type rawAlertPolicy struct {
	policy AlertPolicy
	fields map[string]bool
}

func alertPoliciesFromSettings(settings map[string]interface{}) (rawAlertPolicy, []rawAlertPolicy) {
	globalRaw, _ := settings["global"].(map[string]interface{})
	global := parseRawAlertPolicy(globalRaw["alert_policy"])

	rawTargets, _ := settings["targets"].([]interface{})
	targets := make([]rawAlertPolicy, len(rawTargets))
	for i, item := range rawTargets {
		targetRaw, _ := item.(map[string]interface{})
		targets[i] = parseRawAlertPolicy(targetRaw["alert_policy"])
	}
	return global, targets
}

func parseRawAlertPolicy(raw interface{}) rawAlertPolicy {
	parsed := rawAlertPolicy{fields: map[string]bool{}}
	values, ok := raw.(map[string]interface{})
	if !ok {
		return parsed
	}
	setInt := func(key string, dest *int) {
		number, ok := asInt(values[key])
		if !ok {
			return
		}
		*dest = number
		parsed.fields[key] = true
	}
	setInt(_alertConsecutiveFailures, &parsed.policy.ConsecutiveFailures)
	setInt(_alertConsecutiveRecoveries, &parsed.policy.ConsecutiveRecoveries)
	setInt(_alertCooldownSeconds, &parsed.policy.CooldownSeconds)
	setInt(_alertLatencyThresholdMs, &parsed.policy.LatencyThresholdMs)
	setInt(_alertLatencyBreachCount, &parsed.policy.LatencyBreachCount)
	setInt(_alertSSLExpiryThresholdDays, &parsed.policy.SSLExpiryThresholdDays)
	return parsed
}

func overlayAlertPolicy(base AlertPolicy, over rawAlertPolicy) AlertPolicy {
	if over.fields[_alertConsecutiveFailures] {
		base.ConsecutiveFailures = over.policy.ConsecutiveFailures
	}
	if over.fields[_alertConsecutiveRecoveries] {
		base.ConsecutiveRecoveries = over.policy.ConsecutiveRecoveries
	}
	if over.fields[_alertCooldownSeconds] {
		base.CooldownSeconds = over.policy.CooldownSeconds
	}
	if over.fields[_alertLatencyThresholdMs] {
		base.LatencyThresholdMs = over.policy.LatencyThresholdMs
	}
	if over.fields[_alertLatencyBreachCount] {
		base.LatencyBreachCount = over.policy.LatencyBreachCount
	}
	if over.fields[_alertSSLExpiryThresholdDays] {
		base.SSLExpiryThresholdDays = over.policy.SSLExpiryThresholdDays
	}
	return base
}

func alertFieldsSet(policy AlertPolicy) map[string]bool {
	fields := map[string]bool{}
	if policy.ConsecutiveFailures != 0 {
		fields[_alertConsecutiveFailures] = true
	}
	if policy.ConsecutiveRecoveries != 0 {
		fields[_alertConsecutiveRecoveries] = true
	}
	if policy.CooldownSeconds != 0 {
		fields[_alertCooldownSeconds] = true
	}
	if policy.LatencyThresholdMs != 0 {
		fields[_alertLatencyThresholdMs] = true
	}
	if policy.LatencyBreachCount != 0 {
		fields[_alertLatencyBreachCount] = true
	}
	if policy.SSLExpiryThresholdDays != 0 {
		fields[_alertSSLExpiryThresholdDays] = true
	}
	return fields
}

func asInt(value interface{}) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), true
	case float64:
		return int(number), true
	case uint64:
		return int(number), true
	default:
		return 0, false
	}
}

func normalizeAlertPolicy(policy AlertPolicy) AlertPolicy {
	if policy.ConsecutiveFailures <= 0 {
		policy.ConsecutiveFailures = 1
	}
	if policy.ConsecutiveRecoveries <= 0 {
		policy.ConsecutiveRecoveries = 1
	}
	if policy.CooldownSeconds < 0 {
		policy.CooldownSeconds = 0
	}
	if policy.LatencyThresholdMs < 0 {
		policy.LatencyThresholdMs = 0
	}
	if policy.LatencyThresholdMs > 0 && policy.LatencyBreachCount <= 0 {
		policy.LatencyBreachCount = 1
	}
	if policy.SSLExpiryThresholdDays < 0 {
		policy.SSLExpiryThresholdDays = 0
	}
	return policy
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
