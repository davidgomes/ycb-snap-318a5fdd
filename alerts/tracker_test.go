package alerts

import (
	"testing"
	"time"
)

func TestDefaultsAndDownRecovery(t *testing.T) {
	tracker := NewTracker(Policy{})
	now := time.Unix(1_700_000_000, 0)

	d := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now)
	if d.State != StateHealthy || d.Event != EventNone || d.ConsecutiveRecoveries != 1 {
		t.Fatalf("first up: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now.Add(time.Second))
	if d.Event != EventTargetDown || d.State != StateDown || d.PreviousState != StateHealthy || d.Reason == "" {
		t.Fatalf("first fail: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now.Add(2*time.Second))
	if d.Event != EventTargetRecovered || d.State != StateHealthy || d.Suppressed {
		t.Fatalf("recover: %+v", d)
	}
}

func TestConsecutiveThresholds(t *testing.T) {
	tracker := NewTracker(Policy{ConsecutiveFailures: 2, ConsecutiveRecoveries: 2})
	now := time.Now()

	d := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now)
	if d.Event != EventNone || d.State != StateHealthy || d.ConsecutiveFailures != 1 {
		t.Fatalf("single fail should wait: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now.Add(time.Second))
	if d.Event != EventTargetDown || d.State != StateDown {
		t.Fatalf("second fail: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now.Add(2*time.Second))
	if d.Event != EventNone || d.State != StateDown || d.ConsecutiveRecoveries != 1 {
		t.Fatalf("single recovery should wait: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now.Add(3*time.Second))
	if d.Event != EventTargetRecovered || d.State != StateHealthy {
		t.Fatalf("second recovery: %+v", d)
	}
}

func TestLatencyDegradedAndHealthy(t *testing.T) {
	tracker := NewTracker(Policy{
		LatencyThreshold:   100 * time.Millisecond,
		LatencyBreachCount: 0,
	})
	now := time.Now()

	d := tracker.Evaluate(Check{IsUp: true, ResponseTime: 150 * time.Millisecond, SSLDaysRemaining: -1}, now)
	if d.Event != EventTargetDegraded || d.State != StateDegraded || d.LatencyBreaches != 1 {
		t.Fatalf("first slow check (count default 1): %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, ResponseTime: 200 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(time.Second))
	if d.Event != EventTargetDegraded || d.State != StateDegraded {
		t.Fatalf("stay degraded: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, ResponseTime: 50 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(2*time.Second))
	if d.Event != EventTargetHealthy || d.State != StateHealthy || d.LatencyBreaches != 0 {
		t.Fatalf("recover latency: %+v", d)
	}
}

func TestLatencyResetsOnFailure(t *testing.T) {
	tracker := NewTracker(Policy{
		LatencyThreshold:   100 * time.Millisecond,
		LatencyBreachCount: 2,
	})
	now := time.Now()

	d := tracker.Evaluate(Check{IsUp: true, ResponseTime: 150 * time.Millisecond, SSLDaysRemaining: -1}, now)
	if d.Event != EventNone || d.LatencyBreaches != 1 {
		t.Fatalf("first breach: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now.Add(time.Second))
	if d.LatencyBreaches != 0 || d.Event != EventTargetDown {
		t.Fatalf("fail should reset breaches: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, ResponseTime: 150 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(2*time.Second))
	if d.Event != EventTargetRecovered || d.LatencyBreaches != 1 || d.State != StateHealthy {
		t.Fatalf("recovery restarts latency count: %+v", d)
	}
}

func TestSSLExpiryOnce(t *testing.T) {
	tracker := NewTracker(Policy{SSLExpiryThresholdDays: 14})
	now := time.Now()

	d := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -3}, now)
	if d.Event != EventNone {
		t.Fatalf("negative SSL is N/A: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 10}, now.Add(time.Second))
	if d.Event != EventSSLExpiring || d.State != StateHealthy || d.Reason == "" {
		t.Fatalf("enter threshold: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 9}, now.Add(2*time.Second))
	if d.Event != EventNone || d.State != StateHealthy {
		t.Fatalf("stay in threshold: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 20}, now.Add(3*time.Second))
	if d.Event != EventNone {
		t.Fatalf("leave threshold: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 5}, now.Add(4*time.Second))
	if d.Event != EventSSLExpiring {
		t.Fatalf("re-enter threshold: %+v", d)
	}
}

func TestCooldownSuppressesNonRecovery(t *testing.T) {
	tracker := NewTracker(Policy{
		Cooldown:         10 * time.Second,
		LatencyThreshold: 50 * time.Millisecond,
	})
	now := time.Now()

	d := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now)
	if d.Event != EventTargetDown || d.Suppressed {
		t.Fatalf("first down: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, ResponseTime: 80 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(time.Second))
	if d.Event != EventTargetRecovered || d.Suppressed {
		t.Fatalf("recovery never suppressed: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, ResponseTime: 80 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(2*time.Second))
	if d.Event != EventTargetDegraded || !d.Suppressed || d.State != StateDegraded {
		t.Fatalf("degraded during cooldown should be suppressed: %+v", d)
	}

	d = tracker.Evaluate(Check{IsUp: true, ResponseTime: 10 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(3*time.Second))
	if d.Event != EventTargetHealthy || d.Suppressed {
		t.Fatalf("healthy never suppressed: %+v", d)
	}
}
