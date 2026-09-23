package notifications

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Owloops/updo/alerts"
)

func TestHandleWebhookDecisionSkipsNoneAndSuppressed(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := server.Client()
	none := alerts.Decision{Event: alerts.EventNone, State: alerts.StateHealthy}
	if err := HandleWebhookDecision(server.URL, client, none, "Site", "https://example.com", time.Second, 200, "", ""); err != nil {
		t.Fatalf("none: %v", err)
	}
	suppressed := alerts.Decision{Event: alerts.EventTargetDown, State: alerts.StateDown, Suppressed: true, Reason: "target failed 1 consecutive checks"}
	if err := HandleWebhookDecision(server.URL, client, suppressed, "Site", "https://example.com", time.Second, 500, "down", "us-east-1"); err != nil {
		t.Fatalf("suppressed: %v", err)
	}
	if called {
		t.Fatal("webhook was called")
	}
}

func TestHandleWebhookDecisionWithHeadersPreservesHeadersAndFields(t *testing.T) {
	var body map[string]json.RawMessage
	var header http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	decision := alerts.Decision{
		Event:                 alerts.EventTargetDegraded,
		State:                 alerts.StateDegraded,
		PreviousState:         alerts.StateHealthy,
		Reason:                "response time 80ms exceeded 50ms latency threshold",
		ConsecutiveFailures:   0,
		ConsecutiveRecoveries: 4,
		LatencyBreaches:       2,
		SSLDaysRemaining:      0,
	}
	err := HandleWebhookDecisionWithHeaders(
		server.URL,
		[]string{"X-Alert-Token: secret", "Authorization: Bearer abc"},
		decision,
		"",
		"https://example.com",
		80*time.Millisecond,
		200,
		"",
		"eu-west-1",
	)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if header.Get("X-Alert-Token") != "secret" || header.Get("Authorization") != "Bearer abc" {
		t.Fatalf("headers = %v", header)
	}
	if header.Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %s", header.Get("Content-Type"))
	}

	required := []string{"event", "state", "previous_state", "reason", "consecutive_failures", "consecutive_recoveries", "latency_breaches", "ssl_expiry_days", "region"}
	for _, key := range required {
		if _, ok := body[key]; !ok {
			t.Errorf("missing json field %s in %s", key, body)
		}
	}
	if string(body["event"]) != `"target_degraded"` || string(body["state"]) != `"degraded"` || string(body["previous_state"]) != `"healthy"` {
		t.Fatalf("identity fields = %s", body)
	}
	if string(body["consecutive_failures"]) != "0" || string(body["ssl_expiry_days"]) != "0" || string(body["region"]) != `"eu-west-1"` {
		t.Fatalf("zero-valued fields = %s", body)
	}
	if string(body["target"]) != `"https://example.com"` {
		t.Fatalf("empty name should fall back to url, got %s", body["target"])
	}
}

func TestHandleWebhookDecisionUsesClient(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	decision := alerts.Decision{
		Event:         alerts.EventTargetRecovered,
		State:         alerts.StateHealthy,
		PreviousState: alerts.StateDown,
		Reason:        "target recovered after 1 consecutive successful checks",
	}
	if err := HandleWebhookDecision(server.URL, server.Client(), decision, "Site", "https://example.com", 20*time.Millisecond, 200, "", "local"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !called {
		t.Fatal("expected webhook call")
	}
}
