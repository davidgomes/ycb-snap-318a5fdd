package notifications

import (
	"strings"

	"github.com/Owloops/updo/alerts"
)

type WebhookFormatter interface {
	Format(payload WebhookPayload) ([]byte, error)
}

type eventSeverity int

const (
	_severityCritical eventSeverity = iota
	_severityWarning
	_severityResolved
)

func severityOf(event string) eventSeverity {
	switch alerts.Event(event) {
	case _eventTargetUp, alerts.EventTargetRecovered, alerts.EventTargetHealthy:
		return _severityResolved
	case alerts.EventTargetDegraded, alerts.EventSSLExpiring:
		return _severityWarning
	default:
		return _severityCritical
	}
}

func SelectFormatter(url string) WebhookFormatter {
	lowerURL := strings.ToLower(url)

	if strings.Contains(lowerURL, "hooks.slack.com") {
		return &SlackFormatter{}
	}

	if strings.Contains(lowerURL, "discord.com/api/webhooks") {
		return &DiscordFormatter{}
	}

	return &GenericFormatter{}
}
