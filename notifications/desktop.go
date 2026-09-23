package notifications

import (
	"fmt"

	"github.com/Owloops/updo/alerts"
	"github.com/gen2brain/beeep"
)

func alert(message string) error {
	err := beeep.Alert("Website Status", message, "assets/information.png")
	return err
}

func HandleAlerts(isUp bool, alertSent *bool, targetName string, targetURL string) error {
	displayName := targetName
	if displayName == "" {
		displayName = targetURL
	}

	if !isUp && !*alertSent {
		err := alert(fmt.Sprintf("%s is down!", displayName))
		*alertSent = true
		if err != nil {
			return fmt.Errorf("failed to send alert: %w", err)
		}
	} else if isUp && *alertSent {
		err := alert(fmt.Sprintf("%s is back up!", displayName))
		*alertSent = false
		if err != nil {
			return fmt.Errorf("failed to send alert: %w", err)
		}
	}
	return nil
}

func decisionMessage(decision alerts.Decision, displayName string) string {
	switch decision.Event {
	case alerts.EventTargetDown:
		return fmt.Sprintf("%s is down!", displayName)
	case alerts.EventTargetRecovered:
		return fmt.Sprintf("%s is back up!", displayName)
	case alerts.EventTargetDegraded:
		return fmt.Sprintf("%s is degraded: %s", displayName, decision.Reason)
	case alerts.EventTargetHealthy:
		return fmt.Sprintf("%s is healthy again", displayName)
	case alerts.EventSSLExpiring:
		return fmt.Sprintf("%s: %s", displayName, decision.Reason)
	default:
		return ""
	}
}

func HandleAlertDecision(decision alerts.Decision, targetName string, targetURL string) error {
	if decision.Event == alerts.EventNone || decision.Suppressed {
		return nil
	}

	displayName := targetName
	if displayName == "" {
		displayName = targetURL
	}

	if err := alert(decisionMessage(decision, displayName)); err != nil {
		return fmt.Errorf("failed to send alert: %w", err)
	}
	return nil
}
