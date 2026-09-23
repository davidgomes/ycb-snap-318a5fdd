package notifications

import (
	"strings"

	"github.com/Owloops/updo/alerts"
)

type WebhookFormatter interface {
	Format(payload WebhookPayload) ([]byte, error)
}

func isPositiveEvent(event string) bool {
	switch event {
	case _eventTargetUp, string(alerts.EventTargetRecovered), string(alerts.EventTargetHealthy):
		return true
	default:
		return false
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
