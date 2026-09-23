package simple

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Owloops/updo/alerts"
	"github.com/Owloops/updo/config"
	"github.com/Owloops/updo/net"
	"github.com/Owloops/updo/notifications"
	"github.com/Owloops/updo/stats"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	defer func() {
		os.Stdout = original
	}()
	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("Failed to close pipe: %v", err)
	}
	return <-done
}

func TestPrintResultIncludesAlertState(t *testing.T) {
	target := config.Target{Name: "API", URL: "https://api.example.com"}
	base := TargetResult{
		Target:   target,
		Result:   net.WebsiteCheckResult{IsUp: false, StatusCode: 503, ResponseTime: 120 * time.Millisecond},
		Stats:    stats.Stats{UptimePercent: 50},
		Sequence: 2,
	}

	tests := []struct {
		name     string
		isSingle bool
		decision alerts.Decision
		want     string
	}{
		{
			name:     "single target without event",
			isSingle: true,
			decision: alerts.Decision{State: alerts.StateHealthy, PreviousState: alerts.StateHealthy},
			want:     "Response: seq=2 time=120ms status=503 (DOWN) uptime=50.0% alert=healthy\n",
		},
		{
			name:     "single target with event",
			isSingle: true,
			decision: alerts.Decision{Event: alerts.EventTargetDown, State: alerts.StateDown, PreviousState: alerts.StateHealthy, Reason: "failed"},
			want:     "Response: seq=2 time=120ms status=503 (DOWN) uptime=50.0% alert=down event=target_down\n",
		},
		{
			name:     "multi target with event",
			decision: alerts.Decision{Event: alerts.EventTargetDegraded, State: alerts.StateDegraded, PreviousState: alerts.StateHealthy, Reason: "slow"},
			want:     "API response: seq=2 time=120ms status=503 (DOWN) uptime=50.0% alert=degraded event=target_degraded\n",
		},
		{
			name:     "unevaluated decision reports healthy",
			decision: alerts.Decision{},
			want:     "API response: seq=2 time=120ms status=503 (DOWN) uptime=50.0% alert=healthy\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			manager := &OutputManager{targets: []config.Target{target}, isSingle: tc.isSingle}
			result := base
			result.AlertDecision = tc.decision

			got := captureStdout(t, func() { manager.PrintResult(result) })
			if got != tc.want {
				t.Errorf("PrintResult() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStartMultiTargetMonitoringAppliesAlertPolicy(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer site.Close()

	var mu sync.Mutex
	var payloads []notifications.WebhookPayload
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload notifications.WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("Failed to decode webhook payload: %v", err)
		}
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer webhook.Close()

	target := config.Target{
		Name:            "Flaky",
		URL:             site.URL,
		RefreshInterval: 1,
		Timeout:         2,
		Method:          "GET",
		WebhookURL:      webhook.URL,
		WebhookHeaders:  []string{"X-Test: yes"},
		AlertPolicy:     config.AlertPolicy{ConsecutiveFailures: 2},
	}

	output := captureStdout(t, func() {
		StartMultiTargetMonitoring([]config.Target{target}, MonitoringOptions{Count: 3})
	})

	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "Response") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("Expected 3 result lines, got %d:\n%s", len(lines), output)
	}

	wantSuffixes := []string{
		"alert=healthy",
		"alert=down event=target_down",
		"alert=down",
	}
	for i, suffix := range wantSuffixes {
		if !strings.HasSuffix(lines[i], suffix) {
			t.Errorf("line %d = %q, want suffix %q", i+1, lines[i], suffix)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 1 {
		t.Fatalf("Expected exactly 1 webhook delivery, got %d", len(payloads))
	}
	got := payloads[0]
	if got.Event != "target_down" || got.State != "down" || got.PreviousState != "healthy" || got.ConsecutiveFailures != 2 || got.Reason == "" {
		t.Errorf("unexpected webhook payload: %+v", got)
	}
}
