package notifications

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Owloops/updo/alerts"
)

const (
	_webhookTimeout = 10 * time.Second
)

func parseHeaders(headers []string) map[string]string {
	headerMap := make(map[string]string, len(headers))
	for _, header := range headers {
		parts := strings.SplitN(header, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			headerMap[key] = value
		}
	}
	return headerMap
}

type WebhookPayload struct {
	Event                 string    `json:"event"`
	Target                string    `json:"target"`
	URL                   string    `json:"url"`
	Timestamp             time.Time `json:"timestamp"`
	ResponseTimeMs        int64     `json:"response_time_ms"`
	Error                 string    `json:"error,omitempty"`
	StatusCode            int       `json:"status_code,omitempty"`
	State                 string    `json:"state"`
	PreviousState         string    `json:"previous_state"`
	Reason                string    `json:"reason"`
	ConsecutiveFailures   int       `json:"consecutive_failures"`
	ConsecutiveRecoveries int       `json:"consecutive_recoveries"`
	LatencyBreaches       int       `json:"latency_breaches"`
	SSLExpiryDays         int       `json:"ssl_expiry_days"`
	Region                string    `json:"region"`
}

func SendWebhook(webhookURL string, headers map[string]string, payload WebhookPayload) error {
	return deliverWebhook(webhookURL, headers, nil, payload)
}

func deliverWebhook(webhookURL string, headers map[string]string, client *http.Client, payload WebhookPayload) error {
	formatter := SelectFormatter(webhookURL)
	data, err := formatter.Format(payload)
	if err != nil {
		return fmt.Errorf("failed to format webhook payload: %w", err)
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	if client == nil {
		client = &http.Client{Timeout: _webhookTimeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

func decisionWebhookPayload(decision alerts.Decision, name string, urlStr string, respTime time.Duration, status int, errStr string, region string) WebhookPayload {
	displayName := name
	if displayName == "" {
		displayName = urlStr
	}
	return WebhookPayload{
		Event:                 decision.Event.String(),
		Target:                displayName,
		URL:                   urlStr,
		Timestamp:             time.Now().UTC(),
		ResponseTimeMs:        respTime.Milliseconds(),
		Error:                 errStr,
		StatusCode:            status,
		State:                 decision.State.String(),
		PreviousState:         decision.PreviousState.String(),
		Reason:                decision.Reason,
		ConsecutiveFailures:   decision.ConsecutiveFailures,
		ConsecutiveRecoveries: decision.ConsecutiveRecoveries,
		LatencyBreaches:       decision.LatencyBreaches,
		SSLExpiryDays:         decision.SSLDaysRemaining,
		Region:                region,
	}
}

func shouldSendDecision(decision alerts.Decision, webhookURL string) bool {
	return webhookURL != "" && decision.Event != alerts.EventNone && !decision.Suppressed
}

// HandleWebhookDecision sends a decision webhook using client.
// Nothing is sent when the decision has no event or is suppressed.
func HandleWebhookDecision(webhookURL string, client *http.Client, decision alerts.Decision, name string, urlStr string, respTime time.Duration, status int, errStr string, region string) error {
	if !shouldSendDecision(decision, webhookURL) {
		return nil
	}
	payload := decisionWebhookPayload(decision, name, urlStr, respTime, status, errStr, region)
	return sendDecisionWebhook(webhookURL, nil, client, payload)
}

// HandleWebhookDecisionWithHeaders sends a decision webhook and preserves custom headers.
// Nothing is sent when the decision has no event or is suppressed.
func HandleWebhookDecisionWithHeaders(webhookURL string, headers []string, decision alerts.Decision, name string, urlStr string, respTime time.Duration, status int, errStr string, region string) error {
	if !shouldSendDecision(decision, webhookURL) {
		return nil
	}
	payload := decisionWebhookPayload(decision, name, urlStr, respTime, status, errStr, region)
	return sendDecisionWebhook(webhookURL, parseHeaders(headers), nil, payload)
}

func sendDecisionWebhook(webhookURL string, headers map[string]string, client *http.Client, payload WebhookPayload) error {
	if err := deliverWebhook(webhookURL, headers, client, payload); err != nil {
		return fmt.Errorf("failed to send webhook for %s: %w", payload.Target, err)
	}
	return nil
}

func HandleWebhookAlert(webhookURL string, headers []string, isUp bool, alertSent *bool, targetName string, targetURL string, responseTime time.Duration, statusCode int, errorMsg string) error {
	displayName := targetName
	if displayName == "" {
		displayName = targetURL
	}

	var event string
	shouldSend := false

	if !isUp && !*alertSent {
		event = "target_down"
		shouldSend = true
		*alertSent = true
	} else if isUp && *alertSent {
		event = "target_up"
		shouldSend = true
		*alertSent = false
	}

	if !shouldSend || webhookURL == "" {
		return nil
	}

	payload := WebhookPayload{
		Event:          event,
		Target:         displayName,
		URL:            targetURL,
		Timestamp:      time.Now().UTC(),
		ResponseTimeMs: responseTime.Milliseconds(),
		StatusCode:     statusCode,
		Error:          errorMsg,
	}

	headerMap := parseHeaders(headers)

	if err := SendWebhook(webhookURL, headerMap, payload); err != nil {
		return fmt.Errorf("failed to send webhook for %s: %w", displayName, err)
	}
	return nil
}
