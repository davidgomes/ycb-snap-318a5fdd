package alerts

import (
	"testing"
	"time"
)

func TestTrackerDownRecoverAndCooldown(t *testing.T) {
	tr := NewTracker(Policy{ConsecutiveFailures: 2, ConsecutiveRecoveries: 2, Cooldown: time.Minute, LatencyThreshold: 100 * time.Millisecond, SSLExpiryThresholdDays: 10})
	now := time.Now()
	down := Check{IsUp: false, SSLDaysRemaining: -1}
	up := Check{IsUp: true, ResponseTime: 10 * time.Millisecond, SSLDaysRemaining: -1}
	slow := Check{IsUp: true, ResponseTime: time.Second, SSLDaysRemaining: -1}

	if d := tr.Evaluate(down, now); d.Event != EventNone || d.ConsecutiveFailures != 1 {
		t.Fatalf("unexpected %+v", d)
	}
	if d := tr.Evaluate(down, now); d.Event != EventTargetDown || d.State != StateDown || d.Reason == "" {
		t.Fatalf("unexpected %+v", d)
	}
	tr.Evaluate(up, now)
	if d := tr.Evaluate(up, now); d.Event != EventTargetRecovered || d.PreviousState != StateDown {
		t.Fatalf("unexpected %+v", d)
	}
	if d := tr.Evaluate(slow, now.Add(time.Second)); d.Event != EventTargetDegraded || !d.Suppressed || d.State != StateDegraded {
		t.Fatalf("unexpected %+v", d)
	}
	if d := tr.Evaluate(up, now); d.Event != EventTargetHealthy || d.Suppressed {
		t.Fatalf("unexpected %+v", d)
	}
	ssl := Check{IsUp: true, SSLDaysRemaining: 5}
	if d := tr.Evaluate(ssl, now.Add(2*time.Minute)); d.Event != EventSSLExpiring || d.Suppressed {
		t.Fatalf("unexpected %+v", d)
	}
	if d := tr.Evaluate(ssl, now.Add(4*time.Minute)); d.Event != EventNone {
		t.Fatalf("unexpected %+v", d)
	}
}
