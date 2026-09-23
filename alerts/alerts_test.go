package alerts

import (
	"testing"
	"time"
)

func TestConsecutiveFailureAndRecovery(t *testing.T) {
	tr := NewTracker(Policy{ConsecutiveFailures: 2, ConsecutiveRecoveries: 2})
	now := time.Now()

	d := tr.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now)
	if d.Event != EventNone || d.State != StateHealthy || d.ConsecutiveFailures != 1 {
		t.Fatalf("first failure: %+v", d)
	}

	d = tr.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now.Add(time.Second))
	if d.Event != EventTargetDown || d.State != StateDown || d.PreviousState != StateHealthy || d.Reason == "" {
		t.Fatalf("second failure: %+v", d)
	}

	d = tr.Evaluate(Check{IsUp: true, ResponseTime: time.Millisecond, SSLDaysRemaining: -1}, now.Add(2*time.Second))
	if d.Event != EventNone || d.State != StateDown || d.LatencyBreaches != 0 {
		t.Fatalf("partial recovery: %+v", d)
	}

	d = tr.Evaluate(Check{IsUp: true, ResponseTime: time.Millisecond, SSLDaysRemaining: -1}, now.Add(3*time.Second))
	if d.Event != EventTargetRecovered || d.State != StateHealthy || d.Suppressed {
		t.Fatalf("recovery: %+v", d)
	}
}

func TestLatencyAndCooldown(t *testing.T) {
	tr := NewTracker(Policy{
		LatencyThreshold:   100 * time.Millisecond,
		LatencyBreachCount: 0,
		Cooldown:           10 * time.Second,
	})
	now := time.Now()
	slow := Check{IsUp: true, ResponseTime: 150 * time.Millisecond, SSLDaysRemaining: -1}
	fast := Check{IsUp: true, ResponseTime: 10 * time.Millisecond, SSLDaysRemaining: -1}

	d := tr.Evaluate(slow, now)
	if d.Event != EventTargetDegraded || d.State != StateDegraded || d.LatencyBreaches != 1 {
		t.Fatalf("degraded: %+v", d)
	}

	d = tr.Evaluate(slow, now.Add(time.Second))
	if d.Event != EventTargetDegraded || !d.Suppressed || d.State != StateDegraded {
		t.Fatalf("cooldown: %+v", d)
	}

	d = tr.Evaluate(fast, now.Add(2*time.Second))
	if d.Event != EventTargetHealthy || d.Suppressed || d.State != StateHealthy || d.LatencyBreaches != 0 {
		t.Fatalf("healthy: %+v", d)
	}
}

func TestSSLOnce(t *testing.T) {
	tr := NewTracker(Policy{SSLExpiryThresholdDays: 14})
	now := time.Now()

	d := tr.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now)
	if d.Event != EventNone {
		t.Fatalf("negative ssl: %+v", d)
	}

	d = tr.Evaluate(Check{IsUp: true, SSLDaysRemaining: 10}, now)
	if d.Event != EventSSLExpiring || d.State != StateHealthy || d.Reason == "" {
		t.Fatalf("ssl: %+v", d)
	}

	d = tr.Evaluate(Check{IsUp: true, SSLDaysRemaining: 9}, now.Add(time.Minute))
	if d.Event != EventNone || d.SSLDaysRemaining != 9 {
		t.Fatalf("ssl repeat: %+v", d)
	}

	d = tr.Evaluate(Check{IsUp: true, SSLDaysRemaining: 30}, now.Add(2*time.Minute))
	if d.Event != EventNone {
		t.Fatalf("ssl clear: %+v", d)
	}

	d = tr.Evaluate(Check{IsUp: true, SSLDaysRemaining: 7}, now.Add(3*time.Minute))
	if d.Event != EventSSLExpiring {
		t.Fatalf("ssl reenter: %+v", d)
	}
}

func TestFailureResetsLatency(t *testing.T) {
	tr := NewTracker(Policy{LatencyThreshold: time.Second, LatencyBreachCount: 3})
	now := time.Now()
	slow := Check{IsUp: true, ResponseTime: 2 * time.Second, SSLDaysRemaining: -1}

	d := tr.Evaluate(slow, now)
	if d.LatencyBreaches != 1 || d.Event != EventNone {
		t.Fatalf("breach: %+v", d)
	}
	d = tr.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now)
	if d.LatencyBreaches != 0 || d.Event != EventTargetDown || d.State != StateDown {
		t.Fatalf("down reset: %+v", d)
	}
	d = tr.Evaluate(Check{IsUp: false, ResponseTime: 2 * time.Second, SSLDaysRemaining: -1}, now)
	if d.State != StateDown || d.LatencyBreaches != 0 || d.Event != EventNone {
		t.Fatalf("still down: %+v", d)
	}
}
