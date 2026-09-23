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

func TestPrintResultIncludesAlertAndEvent(t *testing.T) {
	manager := NewOutputManager([]config.Target{{Name: "Example", URL: "https://example.com"}})
	result := TargetResult{
		Target: config.Target{Name: "Example", URL: "https://example.com"},
		Result: net.WebsiteCheckResult{
			StatusCode:   200,
			ResponseTime: 20 * time.Millisecond,
			IsUp:         true,
		},
		Stats:    stats.Stats{UptimePercent: 100},
		Sequence: 3,
		AlertDecision: alerts.Decision{
			State: alerts.StateDegraded,
			Event: alerts.EventTargetDegraded,
		},
	}

	line := captureStdout(t, func() {
		manager.PrintResult(result)
	})
	if !strings.Contains(line, "alert=degraded") || !strings.Contains(line, "event=target_degraded") {
		t.Fatalf("line = %q", line)
	}

	result.AlertDecision = alerts.Decision{State: alerts.StateHealthy, Event: alerts.EventNone}
	quiet := captureStdout(t, func() {
		manager.PrintResult(result)
	})
	if !strings.Contains(quiet, "alert=healthy") || strings.Contains(quiet, "event=") {
		t.Fatalf("quiet line = %q", quiet)
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
	defer func() {
		os.Stdout = original
	}()

	fn()
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return buf.String()
}
