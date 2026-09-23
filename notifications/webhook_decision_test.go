package notifications

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Owloops/updo/alerts"
)

func TestWebhookPayloadIncludesDecisionFields(t *testing.T) {
	payload := WebhookPayload{
		Event:     "target_down",
		Target:    "Example",
		URL:       "https://example.com",
		Timestamp: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	required := []string{
		"event",
		"state",
		"previous_state",
		"reason",
		"consecutive_failures",
		"consecutive_recoveries",
		"latency_breaches",
		"ssl_expiry_days",
		"region",
	}
	for _, key := range required {
		if _, ok := body[key]; !ok {
			t.Errorf("missing json field %s in %s", key, data)
		}
	}
}

func TestHandleWebhookDecision(t *testing.T) {
	decision := alerts.Decision{
		Event:                 alerts.EventTargetDown,
		State:                 alerts.StateDown,
		PreviousState:         alerts.StateHealthy,
		Reason:                "target is down after 2 consecutive failed checks",
		ConsecutiveFailures:   2,
		ConsecutiveRecoveries: 0,
		LatencyBreaches:       0,
		SSLDaysRemaining:      -1,
	}

	var got WebhookPayload
	var gotHeaders http.Header
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotHeaders = r.Header.Clone()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode: %v", err)
		}
		if !jsonContainsKey(body, "state") || !jsonContainsKey(body, "ssl_expiry_days") || !jsonContainsKey(body, "region") {
			t.Errorf("zero-valued decision fields missing: %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	transportCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		transportCalls++
		return http.DefaultTransport.RoundTrip(r)
	})}

	err := HandleWebhookDecision(server.URL, client, decision, "Example", "https://example.com", 1500*time.Millisecond, 500, "Non-success status code: 500", "us-east-1")
	if err != nil {
		t.Fatalf("HandleWebhookDecision: %v", err)
	}
	if transportCalls != 1 || requests != 1 {
		t.Fatalf("transport calls=%d requests=%d", transportCalls, requests)
	}
	if got.Event != string(alerts.EventTargetDown) || got.State != string(alerts.StateDown) || got.PreviousState != string(alerts.StateHealthy) {
		t.Fatalf("payload identity = %+v", got)
	}
	if got.Reason == "" || got.ConsecutiveFailures != 2 || got.SSLExpiryDays != -1 || got.Region != "us-east-1" {
		t.Fatalf("payload details = %+v", got)
	}
	if got.Target != "Example" || got.URL != "https://example.com" || got.StatusCode != 500 {
		t.Fatalf("target payload = %+v", got)
	}
	if gotHeaders.Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %s", gotHeaders.Get("Content-Type"))
	}

	err = HandleWebhookDecision(server.URL, client, alerts.Decision{Event: alerts.EventNone, State: alerts.StateHealthy}, "Example", "https://example.com", 0, 200, "", "")
	if err != nil {
		t.Fatalf("none: %v", err)
	}
	suppressed := decision
	suppressed.Suppressed = true
	err = HandleWebhookDecision(server.URL, client, suppressed, "Example", "https://example.com", 0, 500, "down", "")
	if err != nil {
		t.Fatalf("suppressed: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected no further requests, got %d", requests)
	}
}

func TestHandleWebhookDecisionWithHeaders(t *testing.T) {
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	decision := alerts.Decision{
		Event:         alerts.EventTargetDegraded,
		State:         alerts.StateDegraded,
		PreviousState: alerts.StateHealthy,
		Reason:        "response time exceeded latency threshold",
	}
	err := HandleWebhookDecisionWithHeaders(
		server.URL,
		[]string{"X-Token: secret", "X-Region: eu-west-1"},
		decision,
		"",
		"https://example.com",
		20*time.Millisecond,
		200,
		"",
		"eu-west-1",
	)
	if err != nil {
		t.Fatalf("with headers: %v", err)
	}
	if gotHeaders.Get("X-Token") != "secret" || gotHeaders.Get("X-Region") != "eu-west-1" {
		t.Fatalf("headers = %v", gotHeaders)
	}

	calls := 0
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer silent.Close()
	if err := HandleWebhookDecisionWithHeaders(silent.URL, []string{"X-Token: secret"}, alerts.Decision{Suppressed: true, Event: alerts.EventTargetDown}, "n", "u", 0, 0, "", ""); err != nil {
		t.Fatalf("suppressed headers: %v", err)
	}
	if err := HandleWebhookDecisionWithHeaders("", []string{"X-Token: secret"}, decision, "n", "u", 0, 0, "", ""); err != nil {
		t.Fatalf("empty url: %v", err)
	}
	if calls != 0 {
		t.Fatalf("unexpected calls %d", calls)
	}
}

func jsonContainsKey(body []byte, key string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return false
	}
	_, ok := raw[key]
	return ok
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
