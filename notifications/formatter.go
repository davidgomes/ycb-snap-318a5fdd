package notifications

import (
	"strings"

	"github.com/Owloops/updo/alerts"
)

type WebhookFormatter interface {
	Format(payload WebhookPayload) ([]byte, error)
}

func isRecoveredEvent(event string) bool {
	switch alerts.Event(event) {
	case alerts.EventTargetRecovered, alerts.EventTargetHealthy:
		return true
	default:
		return event == _eventTargetUp
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
