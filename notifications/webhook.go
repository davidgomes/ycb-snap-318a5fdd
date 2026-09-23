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
	State                 string    `json:"state"`
	PreviousState         string    `json:"previous_state"`
	Reason                string    `json:"reason"`
	ConsecutiveFailures   int       `json:"consecutive_failures"`
	ConsecutiveRecoveries int       `json:"consecutive_recoveries"`
	LatencyBreaches       int       `json:"latency_breaches"`
	SSLExpiryDays         int       `json:"ssl_expiry_days"`
	Region                string    `json:"region"`
	Target                string    `json:"target"`
	URL                   string    `json:"url"`
	Timestamp             time.Time `json:"timestamp"`
	ResponseTimeMs        int64     `json:"response_time_ms"`
	Error                 string    `json:"error,omitempty"`
	StatusCode            int       `json:"status_code,omitempty"`
}

func SendWebhook(webhookURL string, headers map[string]string, payload WebhookPayload) error {
	client := &http.Client{
		Timeout: _webhookTimeout,
	}
	return postWebhook(webhookURL, client, headers, payload)
}

func postWebhook(webhookURL string, client *http.Client, headers map[string]string, payload WebhookPayload) error {
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

func HandleWebhookDecision(url string, client *http.Client, decision alerts.Decision, name string, urlStr string, respTime time.Duration, status int, errStr string, region string) error {
	return sendDecisionWebhook(url, client, nil, decision, name, urlStr, respTime, status, errStr, region)
}

func HandleWebhookDecisionWithHeaders(url string, headers []string, decision alerts.Decision, name string, urlStr string, respTime time.Duration, status int, errStr string, region string) error {
	client := &http.Client{Timeout: _webhookTimeout}
	return sendDecisionWebhook(url, client, parseHeaders(headers), decision, name, urlStr, respTime, status, errStr, region)
}

func sendDecisionWebhook(url string, client *http.Client, headers map[string]string, decision alerts.Decision, name string, urlStr string, respTime time.Duration, status int, errStr string, region string) error {
	if decision.Event == alerts.EventNone || decision.Suppressed || url == "" {
		return nil
	}

	displayName := name
	if displayName == "" {
		displayName = urlStr
	}

	payload := WebhookPayload{
		Event:                 string(decision.Event),
		State:                 string(decision.State),
		PreviousState:         string(decision.PreviousState),
		Reason:                decision.Reason,
		ConsecutiveFailures:   decision.ConsecutiveFailures,
		ConsecutiveRecoveries: decision.ConsecutiveRecoveries,
		LatencyBreaches:       decision.LatencyBreaches,
		SSLExpiryDays:         decision.SSLDaysRemaining,
		Region:                region,
		Target:                displayName,
		URL:                   urlStr,
		Timestamp:             time.Now().UTC(),
		ResponseTimeMs:        respTime.Milliseconds(),
		StatusCode:            status,
		Error:                 errStr,
	}

	if err := postWebhook(url, client, headers, payload); err != nil {
		return fmt.Errorf("failed to send webhook for %s: %w", displayName, err)
	}
	return nil
}
