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

func TestPrintResultIncludesAlertState(t *testing.T) {
	manager := &OutputManager{
		targets: []config.Target{{
			Name: "Example",
			URL:  "https://example.com",
		}},
		isSingle:     true,
		sslExpiry:    map[string]int{},
		sslCollected: map[string]bool{},
	}
	result := TargetResult{
		Target: manager.targets[0],
		Result: net.WebsiteCheckResult{
			IsUp:         true,
			StatusCode:   200,
			ResponseTime: 20 * time.Millisecond,
		},
		Stats: stats.Stats{UptimePercent: 100},
		AlertDecision: alerts.Decision{
			State: alerts.StateHealthy,
			Event: alerts.EventNone,
		},
		Sequence: 1,
	}

	line := capturePrint(t, func() { manager.PrintResult(result) })
	if !strings.Contains(line, "alert=healthy") {
		t.Fatalf("line = %q", line)
	}
	if strings.Contains(line, "event=") {
		t.Fatalf("quiet check should omit event: %q", line)
	}

	result.AlertDecision = alerts.Decision{
		State: alerts.StateDegraded,
		Event: alerts.EventTargetDegraded,
	}
	line = capturePrint(t, func() { manager.PrintResult(result) })
	if !strings.Contains(line, "alert=degraded") || !strings.Contains(line, "event=target_degraded") {
		t.Fatalf("line = %q", line)
	}

	manager.isSingle = false
	result.AlertDecision = alerts.Decision{
		State: alerts.StateDown,
		Event: alerts.EventTargetDown,
	}
	result.Result.IsUp = false
	line = capturePrint(t, func() { manager.PrintResult(result) })
	if !strings.Contains(line, "Example response") || !strings.Contains(line, "alert=down") || !strings.Contains(line, "event=target_down") {
		t.Fatalf("line = %q", line)
	}
}

func capturePrint(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	fn()
	if err := writer.Close(); err != nil {
		os.Stdout = original
		t.Fatal(err)
	}
	os.Stdout = original
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
