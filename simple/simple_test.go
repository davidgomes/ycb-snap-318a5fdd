package simple

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Owloops/updo/alerts"
	"github.com/Owloops/updo/config"
	"github.com/Owloops/updo/net"
)

func capturePrintResult(t *testing.T, result TargetResult) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	NewOutputManager([]config.Target{result.Target}).PrintResult(result)

	if err := w.Close(); err != nil {
		t.Fatalf("Failed to close pipe: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}
	return buf.String()
}

func TestPrintResultAlertInfo(t *testing.T) {
	target := config.Target{Name: "Example", URL: "https://example.com"}
	tests := []struct {
		name        string
		decision    alerts.Decision
		wantContain string
		wantEvent   bool
	}{
		{
			name:        "no event",
			decision:    alerts.Decision{State: alerts.StateHealthy},
			wantContain: "alert=healthy",
		},
		{
			name:        "down event",
			decision:    alerts.Decision{Event: alerts.EventTargetDown, State: alerts.StateDown, Reason: "1 consecutive failed checks"},
			wantContain: "alert=down event=target_down",
			wantEvent:   true,
		},
		{
			name:        "suppressed degraded event",
			decision:    alerts.Decision{Event: alerts.EventTargetDegraded, State: alerts.StateDegraded, Reason: "slow", Suppressed: true},
			wantContain: "alert=degraded event=target_degraded",
			wantEvent:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := capturePrintResult(t, TargetResult{
				Target:        target,
				Result:        net.WebsiteCheckResult{IsUp: true, StatusCode: 200, ResponseTime: 42 * time.Millisecond},
				Sequence:      1,
				AlertDecision: tc.decision,
			})
			if !strings.Contains(out, tc.wantContain) {
				t.Errorf("output %q does not contain %q", out, tc.wantContain)
			}
			if got := strings.Contains(out, "event="); got != tc.wantEvent {
				t.Errorf("output %q contains event= %v, want %v", out, got, tc.wantEvent)
			}
		})
	}
}
