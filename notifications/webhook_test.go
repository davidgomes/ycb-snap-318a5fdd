package notifications

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Owloops/updo/alerts"
)

func TestSendWebhook(t *testing.T) {
	tests := []struct {
		name           string
		payload        WebhookPayload
		headers        []string
		responseStatus int
		expectError    bool
	}{
		{
			name: "successful webhook",
			payload: WebhookPayload{
				Event:          "target_down",
				Target:         "Test Site",
				URL:            "https://example.com",
				Timestamp:      time.Now().UTC(),
				ResponseTimeMs: 1500,
				StatusCode:     500,
				Error:          "Internal Server Error",
			},
			headers:        []string{"X-Custom: test"},
			responseStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name: "webhook returns error status",
			payload: WebhookPayload{
				Event:          "target_up",
				Target:         "Test Site",
				URL:            "https://example.com",
				Timestamp:      time.Now().UTC(),
				ResponseTimeMs: 200,
				StatusCode:     200,
			},
			headers:        nil,
			responseStatus: http.StatusInternalServerError,
			expectError:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var receivedPayload WebhookPayload
			var receivedHeaders http.Header

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedHeaders = r.Header

				if r.Method != "POST" {
					t.Errorf("Expected POST method, got %s", r.Method)
				}

				if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
					t.Errorf("Expected Content-Type application/json, got %s", contentType)
				}

				if err := json.NewDecoder(r.Body).Decode(&receivedPayload); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
				}

				w.WriteHeader(tc.responseStatus)
			}))
			defer server.Close()

			headerMap := parseHeaders(tc.headers)

			err := SendWebhook(server.URL, headerMap, tc.payload)

			if tc.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tc.expectError {
				if receivedPayload.Event != tc.payload.Event {
					t.Errorf("Event mismatch: expected %s, got %s", tc.payload.Event, receivedPayload.Event)
				}
				if receivedPayload.Target != tc.payload.Target {
					t.Errorf("Target mismatch: expected %s, got %s", tc.payload.Target, receivedPayload.Target)
				}

				expectedHeaders := parseHeaders(tc.headers)

				for key, value := range expectedHeaders {
					if receivedHeaders.Get(key) != value {
						t.Errorf("Header %s mismatch: expected %s, got %s", key, value, receivedHeaders.Get(key))
					}
				}
			}
		})
	}
}

func TestHandleWebhookAlert(t *testing.T) {
	tests := []struct {
		name              string
		isUp              bool
		initialAlertSent  bool
		expectedAlertSent bool
		expectWebhookCall bool
		targetName        string
		targetURL         string
	}{
		{
			name:              "target goes down",
			isUp:              false,
			initialAlertSent:  false,
			expectedAlertSent: true,
			expectWebhookCall: true,
			targetName:        "Test Site",
			targetURL:         "https://example.com",
		},
		{
			name:              "target still down",
			isUp:              false,
			initialAlertSent:  true,
			expectedAlertSent: true,
			expectWebhookCall: false,
			targetName:        "Test Site",
			targetURL:         "https://example.com",
		},
		{
			name:              "target comes up",
			isUp:              true,
			initialAlertSent:  true,
			expectedAlertSent: false,
			expectWebhookCall: true,
			targetName:        "Test Site",
			targetURL:         "https://example.com",
		},
		{
			name:              "target still up",
			isUp:              true,
			initialAlertSent:  false,
			expectedAlertSent: false,
			expectWebhookCall: false,
			targetName:        "Test Site",
			targetURL:         "https://example.com",
		},
		{
			name:              "empty target name uses URL",
			isUp:              false,
			initialAlertSent:  false,
			expectedAlertSent: true,
			expectWebhookCall: true,
			targetName:        "",
			targetURL:         "https://example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			webhookCalled := false
			var receivedPayload WebhookPayload

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				webhookCalled = true
				if err := json.NewDecoder(r.Body).Decode(&receivedPayload); err != nil {
					t.Errorf("Failed to decode webhook payload: %v", err)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			alertSent := tc.initialAlertSent

			_ = HandleWebhookAlert(
				server.URL,
				nil,
				tc.isUp,
				&alertSent,
				tc.targetName,
				tc.targetURL,
				1500*time.Millisecond,
				200,
				"",
			)

			if alertSent != tc.expectedAlertSent {
				t.Errorf("Expected alertSent to be %v, got %v", tc.expectedAlertSent, alertSent)
			}

			if webhookCalled != tc.expectWebhookCall {
				t.Errorf("Expected webhook to be called: %v, but was: %v", tc.expectWebhookCall, webhookCalled)
			}

			if tc.expectWebhookCall && webhookCalled {
				expectedTarget := tc.targetName
				if expectedTarget == "" {
					expectedTarget = tc.targetURL
				}
				if receivedPayload.Target != expectedTarget {
					t.Errorf("Expected target %s, got %s", expectedTarget, receivedPayload.Target)
				}

				expectedEvent := "target_down"
				if tc.isUp {
					expectedEvent = "target_up"
				}
				if receivedPayload.Event != expectedEvent {
					t.Errorf("Expected event %s, got %s", expectedEvent, receivedPayload.Event)
				}
			}
		})
	}
}

func TestHandleWebhookDecision(t *testing.T) {
	tests := []struct {
		name       string
		decision   alerts.Decision
		expectCall bool
	}{
		{
			name: "emitted event is delivered",
			decision: alerts.Decision{
				Event:               alerts.EventTargetDown,
				State:               alerts.StateDown,
				PreviousState:       alerts.StateHealthy,
				Reason:              "3 consecutive failed checks",
				ConsecutiveFailures: 3,
				SSLDaysRemaining:    -1,
			},
			expectCall: true,
		},
		{
			name:       "no event is not delivered",
			decision:   alerts.Decision{Event: alerts.EventNone, State: alerts.StateHealthy},
			expectCall: false,
		},
		{
			name: "suppressed event is not delivered",
			decision: alerts.Decision{
				Event:      alerts.EventTargetDegraded,
				State:      alerts.StateDegraded,
				Reason:     "slow",
				Suppressed: true,
			},
			expectCall: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			var received WebhookPayload
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Errorf("Failed to decode webhook payload: %v", err)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			err := HandleWebhookDecision(server.URL, server.Client(), tc.decision, "", "https://example.com", 250*time.Millisecond, 503, "boom", "us-east-1")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if called != tc.expectCall {
				t.Fatalf("webhook called = %v, want %v", called, tc.expectCall)
			}
			if !tc.expectCall {
				return
			}

			want := WebhookPayload{
				Event:               alerts.EventTargetDown,
				Target:              "https://example.com",
				URL:                 "https://example.com",
				Timestamp:           received.Timestamp,
				ResponseTimeMs:      250,
				StatusCode:          503,
				Error:               "boom",
				State:               alerts.StateDown,
				PreviousState:       alerts.StateHealthy,
				Reason:              "3 consecutive failed checks",
				ConsecutiveFailures: 3,
				SSLExpiryDays:       -1,
				Region:              "us-east-1",
			}
			if received != want {
				t.Errorf("payload = %+v, want %+v", received, want)
			}
		})
	}
}

func TestHandleWebhookDecisionWithHeaders(t *testing.T) {
	var receivedHeaders http.Header
	var raw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("Failed to decode webhook payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	decision := alerts.Decision{
		Event:         alerts.EventTargetRecovered,
		State:         alerts.StateHealthy,
		PreviousState: alerts.StateDown,
		Reason:        "1 consecutive successful checks",
	}
	headers := []string{"Authorization: Bearer token", "X-Custom: value"}
	if err := HandleWebhookDecisionWithHeaders(server.URL, headers, decision, "API", "https://api.example.com", 0, 200, "", ""); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if got := receivedHeaders.Get("Authorization"); got != "Bearer token" {
		t.Errorf("Authorization header = %q", got)
	}
	if got := receivedHeaders.Get("X-Custom"); got != "value" {
		t.Errorf("X-Custom header = %q", got)
	}

	for _, key := range []string{"event", "state", "previous_state", "reason", "consecutive_failures", "consecutive_recoveries", "latency_breaches", "ssl_expiry_days", "region"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("payload missing required field %q", key)
		}
	}
	if raw["event"] != alerts.EventTargetRecovered || raw["previous_state"] != alerts.StateDown {
		t.Errorf("payload = %v", raw)
	}

	suppressed := decision
	suppressed.Suppressed = true
	raw = nil
	if err := HandleWebhookDecisionWithHeaders(server.URL, headers, suppressed, "API", "https://api.example.com", 0, 200, "", ""); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if raw != nil {
		t.Error("suppressed decision should not be delivered")
	}
}

func TestHandleWebhookAlertEmptyURL(t *testing.T) {
	alertSent := false
	webhookCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_ = HandleWebhookAlert(
		"",
		nil,
		false,
		&alertSent,
		"Test Site",
		"https://example.com",
		1500*time.Millisecond,
		500,
		"Server Error",
	)

	if webhookCalled {
		t.Error("Webhook should not be called when URL is empty")
	}

	if !alertSent {
		t.Error("Alert state should still be updated even without webhook URL")
	}
}
