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
	"github.com/Owloops/updo/stats"
)

func TestPrintResultAlertSuffix(t *testing.T) {
	manager := NewOutputManager([]config.Target{{
		Name: "Example",
		URL:  "https://example.com",
	}})

	healthy := captureStdout(t, func() {
		manager.PrintResult(TargetResult{
			Target: config.Target{Name: "Example", URL: "https://example.com"},
			Result: net.WebsiteCheckResult{
				IsUp:         true,
				StatusCode:   200,
				ResponseTime: 15 * time.Millisecond,
				ResolvedIP:   "1.2.3.4",
			},
			Stats:    stats.Stats{UptimePercent: 100},
			Sequence: 3,
			AlertDecision: alerts.Decision{
				State: alerts.StateHealthy,
			},
		})
	})
	if !strings.Contains(healthy, "alert=healthy") {
		t.Fatalf("missing alert state: %s", healthy)
	}
	if strings.Contains(healthy, "event=") {
		t.Fatalf("did not expect an event: %s", healthy)
	}

	multi := NewOutputManager([]config.Target{
		{Name: "Example", URL: "https://example.com"},
		{Name: "Other", URL: "https://other.example"},
	})
	degraded := captureStdout(t, func() {
		multi.PrintResult(TargetResult{
			Target: config.Target{Name: "Example", URL: "https://example.com"},
			Result: net.WebsiteCheckResult{
				IsUp:         true,
				StatusCode:   200,
				ResponseTime: 900 * time.Millisecond,
			},
			Stats:    stats.Stats{UptimePercent: 100},
			Sequence: 4,
			Region:   "us-east-1",
			AlertDecision: alerts.Decision{
				Event: alerts.EventTargetDegraded,
				State: alerts.StateDegraded,
			},
		})
	})
	if !strings.Contains(degraded, "alert=degraded") || !strings.Contains(degraded, "event=target_degraded") {
		t.Fatalf("missing degraded event: %s", degraded)
	}
	if !strings.Contains(degraded, "[us-east-1]") {
		t.Fatalf("missing region: %s", degraded)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	original := os.Stdout
	os.Stdout = writer
	fn()
	writer.Close()
	os.Stdout = original

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return buf.String()
}
