package alerts

import (
	"testing"
	"time"
)

func TestDefaultsAndFailureRecovery(t *testing.T) {
	tracker := NewTracker(Policy{})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	up := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now)
	if up.Event != EventNone || up.State != StateHealthy || up.PreviousState != StateHealthy {
		t.Fatalf("healthy check = %+v", up)
	}
	if up.ConsecutiveFailures != 0 || up.ConsecutiveRecoveries != 1 || up.LatencyBreaches != 0 {
		t.Fatalf("healthy counters = %+v", up)
	}
	if up.SSLDaysRemaining != -1 || up.Suppressed || up.Reason != "" {
		t.Fatalf("healthy snapshot = %+v", up)
	}

	down := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now)
	if down.Event != EventTargetDown || down.State != StateDown || down.PreviousState != StateHealthy {
		t.Fatalf("first failure = %+v", down)
	}
	if down.Reason == "" || down.ConsecutiveFailures != 1 || down.ConsecutiveRecoveries != 0 {
		t.Fatalf("down snapshot = %+v", down)
	}

	still := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now)
	if still.Event != EventNone || still.State != StateDown || still.PreviousState != StateDown {
		t.Fatalf("still down = %+v", still)
	}
	if still.ConsecutiveFailures != 2 || still.Suppressed {
		t.Fatalf("still down counters = %+v", still)
	}

	recovered := tracker.Evaluate(Check{IsUp: true, ResponseTime: time.Second, SSLDaysRemaining: -1}, now)
	if recovered.Event != EventTargetRecovered || recovered.State != StateHealthy || recovered.PreviousState != StateDown {
		t.Fatalf("recovery = %+v", recovered)
	}
	if recovered.Reason == "" || recovered.ConsecutiveRecoveries != 1 || recovered.LatencyBreaches != 0 {
		t.Fatalf("recovery snapshot = %+v", recovered)
	}
}

func TestConsecutiveThresholds(t *testing.T) {
	tracker := NewTracker(Policy{ConsecutiveFailures: 3, ConsecutiveRecoveries: 2})
	now := time.Now()

	for i := 1; i <= 2; i++ {
		decision := tracker.Evaluate(Check{IsUp: false}, now)
		if decision.Event != EventNone || decision.State != StateHealthy || decision.ConsecutiveFailures != i {
			t.Fatalf("failure %d = %+v", i, decision)
		}
	}
	down := tracker.Evaluate(Check{IsUp: false}, now)
	if down.Event != EventTargetDown || down.State != StateDown || down.PreviousState != StateHealthy || down.ConsecutiveFailures != 3 {
		t.Fatalf("third failure = %+v", down)
	}
	if down.Reason == "" {
		t.Fatal("expected a down reason")
	}

	partial := tracker.Evaluate(Check{IsUp: true}, now)
	if partial.Event != EventNone || partial.State != StateDown || partial.ConsecutiveRecoveries != 1 || partial.ConsecutiveFailures != 0 {
		t.Fatalf("partial recovery = %+v", partial)
	}
	recovered := tracker.Evaluate(Check{IsUp: true}, now)
	if recovered.Event != EventTargetRecovered || recovered.State != StateHealthy || recovered.PreviousState != StateDown {
		t.Fatalf("full recovery = %+v", recovered)
	}
	if recovered.ConsecutiveRecoveries != 2 || recovered.Reason == "" {
		t.Fatalf("recovery snapshot = %+v", recovered)
	}
}

func TestLatencyDegradeAndHealthy(t *testing.T) {
	tracker := NewTracker(Policy{
		LatencyThreshold:   100 * time.Millisecond,
		LatencyBreachCount: 2,
	})
	now := time.Now()
	slow := Check{IsUp: true, ResponseTime: 150 * time.Millisecond, SSLDaysRemaining: -1}
	fast := Check{IsUp: true, ResponseTime: 90 * time.Millisecond, SSLDaysRemaining: -1}
	equal := Check{IsUp: true, ResponseTime: 100 * time.Millisecond, SSLDaysRemaining: -1}

	first := tracker.Evaluate(slow, now)
	if first.Event != EventNone || first.State != StateHealthy || first.LatencyBreaches != 1 {
		t.Fatalf("first slow = %+v", first)
	}
	second := tracker.Evaluate(slow, now)
	if second.Event != EventTargetDegraded || second.State != StateDegraded || second.PreviousState != StateHealthy {
		t.Fatalf("second slow = %+v", second)
	}
	if second.LatencyBreaches != 2 || second.Reason == "" {
		t.Fatalf("degraded snapshot = %+v", second)
	}
	third := tracker.Evaluate(slow, now)
	if third.Event != EventTargetDegraded || third.State != StateDegraded || third.PreviousState != StateDegraded || third.LatencyBreaches != 3 {
		t.Fatalf("repeat degraded = %+v", third)
	}
	healthy := tracker.Evaluate(fast, now)
	if healthy.Event != EventTargetHealthy || healthy.State != StateHealthy || healthy.PreviousState != StateDegraded {
		t.Fatalf("healthy = %+v", healthy)
	}
	if healthy.LatencyBreaches != 0 || healthy.Reason == "" {
		t.Fatalf("healthy snapshot = %+v", healthy)
	}

	// Equality with the threshold is not a breach. It also does not clear degradation.
	reset := NewTracker(Policy{LatencyThreshold: 100 * time.Millisecond, LatencyBreachCount: 3})
	if d := reset.Evaluate(slow, now); d.LatencyBreaches != 1 {
		t.Fatalf("breach start = %+v", d)
	}
	if d := reset.Evaluate(equal, now); d.Event != EventNone || d.LatencyBreaches != 0 || d.State != StateHealthy {
		t.Fatalf("equal resets streak = %+v", d)
	}
	if d := reset.Evaluate(slow, now); d.LatencyBreaches != 1 || d.Event != EventNone {
		t.Fatalf("streak restarted = %+v", d)
	}
	if d := reset.Evaluate(slow, now); d.LatencyBreaches != 2 || d.Event != EventNone {
		t.Fatalf("second breach = %+v", d)
	}
	if d := reset.Evaluate(slow, now); d.Event != EventTargetDegraded || d.State != StateDegraded {
		t.Fatalf("third breach = %+v", d)
	}
	if d := reset.Evaluate(equal, now); d.Event != EventNone || d.State != StateDegraded || d.LatencyBreaches != 0 {
		t.Fatalf("equal while degraded = %+v", d)
	}
	if d := reset.Evaluate(fast, now); d.Event != EventTargetHealthy || d.State != StateHealthy {
		t.Fatalf("below threshold = %+v", d)
	}
}

func TestLatencyDisabledAndBreachDefault(t *testing.T) {
	disabled := NewTracker(Policy{})
	slow := Check{IsUp: true, ResponseTime: 5 * time.Second}
	for i := 0; i < 3; i++ {
		decision := disabled.Evaluate(slow, time.Now())
		if decision.Event != EventNone || decision.State != StateHealthy || decision.LatencyBreaches != 0 {
			t.Fatalf("disabled latency = %+v", decision)
		}
	}

	implicit := NewTracker(Policy{LatencyThreshold: 10 * time.Millisecond, LatencyBreachCount: 0})
	decision := implicit.Evaluate(Check{IsUp: true, ResponseTime: 11 * time.Millisecond}, time.Now())
	if decision.Event != EventTargetDegraded || decision.State != StateDegraded || decision.LatencyBreaches != 1 {
		t.Fatalf("default breach count = %+v", decision)
	}
}

func TestLatencyResetWhileDown(t *testing.T) {
	tracker := NewTracker(Policy{
		ConsecutiveFailures:   2,
		ConsecutiveRecoveries: 2,
		LatencyThreshold:      100 * time.Millisecond,
		LatencyBreachCount:    3,
	})
	now := time.Now()
	slow := Check{IsUp: true, ResponseTime: 200 * time.Millisecond}

	if d := tracker.Evaluate(slow, now); d.LatencyBreaches != 1 || d.State != StateHealthy {
		t.Fatalf("first slow = %+v", d)
	}
	if d := tracker.Evaluate(slow, now); d.LatencyBreaches != 2 {
		t.Fatalf("second slow = %+v", d)
	}
	failed := tracker.Evaluate(Check{IsUp: false, ResponseTime: time.Second}, now)
	if failed.Event != EventNone || failed.State != StateHealthy || failed.LatencyBreaches != 0 || failed.ConsecutiveFailures != 1 {
		t.Fatalf("failure resets breaches = %+v", failed)
	}
	down := tracker.Evaluate(Check{IsUp: false}, now)
	if down.Event != EventTargetDown || down.State != StateDown || down.LatencyBreaches != 0 {
		t.Fatalf("down = %+v", down)
	}

	// Still down: a slow success does not restart breach counting.
	partial := tracker.Evaluate(slow, now)
	if partial.State != StateDown || partial.Event != EventNone || partial.LatencyBreaches != 0 || partial.ConsecutiveRecoveries != 1 {
		t.Fatalf("partial recovery = %+v", partial)
	}
	recovered := tracker.Evaluate(slow, now)
	if recovered.Event != EventTargetRecovered || recovered.State != StateHealthy || recovered.LatencyBreaches != 1 {
		t.Fatalf("recovery restarts breaches = %+v", recovered)
	}
	if d := tracker.Evaluate(slow, now); d.LatencyBreaches != 2 || d.Event != EventNone {
		t.Fatalf("second up breach = %+v", d)
	}
	degraded := tracker.Evaluate(slow, now)
	if degraded.Event != EventTargetDegraded || degraded.State != StateDegraded || degraded.LatencyBreaches != 3 {
		t.Fatalf("degraded after recovery = %+v", degraded)
	}
}

func TestRecoveryIntoImmediateDegrade(t *testing.T) {
	tracker := NewTracker(Policy{
		ConsecutiveFailures: 1,
		LatencyThreshold:    50 * time.Millisecond,
		LatencyBreachCount:  1,
	})
	now := time.Now()
	if d := tracker.Evaluate(Check{IsUp: false}, now); d.Event != EventTargetDown {
		t.Fatalf("down = %+v", d)
	}
	decision := tracker.Evaluate(Check{IsUp: true, ResponseTime: 80 * time.Millisecond}, now)
	if decision.Event != EventTargetDegraded || decision.State != StateDegraded || decision.PreviousState != StateDown {
		t.Fatalf("slow recovery = %+v", decision)
	}
	if decision.LatencyBreaches != 1 || decision.Reason == "" {
		t.Fatalf("slow recovery snapshot = %+v", decision)
	}
}

func TestSSLExpiry(t *testing.T) {
	tracker := NewTracker(Policy{SSLExpiryThresholdDays: 14})
	now := time.Now()

	outside := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 15}, now)
	if outside.Event != EventNone || outside.State != StateHealthy || outside.SSLDaysRemaining != 15 {
		t.Fatalf("outside window = %+v", outside)
	}
	first := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 14}, now)
	if first.Event != EventSSLExpiring || first.State != StateHealthy || first.PreviousState != StateHealthy || first.Reason == "" {
		t.Fatalf("entering window = %+v", first)
	}
	again := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 10}, now)
	if again.Event != EventNone || again.State != StateHealthy || again.SSLDaysRemaining != 10 {
		t.Fatalf("still inside = %+v", again)
	}
	na := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now)
	if na.Event != EventNone || na.SSLDaysRemaining != -1 {
		t.Fatalf("negative ssl = %+v", na)
	}
	// Negative remaining is not above the threshold, so the latch stays set.
	if still := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 7}, now); still.Event != EventNone {
		t.Fatalf("latched after n/a = %+v", still)
	}
	if cleared := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 20}, now); cleared.Event != EventNone {
		t.Fatalf("above threshold = %+v", cleared)
	}
	reentry := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 0}, now)
	if reentry.Event != EventSSLExpiring || reentry.State != StateHealthy || reentry.SSLDaysRemaining != 0 || reentry.Reason == "" {
		t.Fatalf("re-entry = %+v", reentry)
	}

	disabled := NewTracker(Policy{})
	if d := disabled.Evaluate(Check{IsUp: true, SSLDaysRemaining: 1}, now); d.Event != EventNone {
		t.Fatalf("ssl disabled = %+v", d)
	}
	negative := NewTracker(Policy{SSLExpiryThresholdDays: 30})
	if d := negative.Evaluate(Check{IsUp: true, SSLDaysRemaining: -5}, now); d.Event != EventNone || d.SSLDaysRemaining != -5 {
		t.Fatalf("negative never alerts = %+v", d)
	}
}

func TestSSLDoesNotOverrideStateEvent(t *testing.T) {
	tracker := NewTracker(Policy{SSLExpiryThresholdDays: 30})
	now := time.Now()
	down := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 3}, now)
	if down.Event != EventTargetDown || down.State != StateDown || down.SSLDaysRemaining != 3 {
		t.Fatalf("down wins = %+v", down)
	}
	ssl := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 3}, now)
	if ssl.Event != EventSSLExpiring || ssl.State != StateDown || ssl.PreviousState != StateDown {
		t.Fatalf("ssl while down = %+v", ssl)
	}
}

func TestCooldown(t *testing.T) {
	start := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tracker := NewTracker(Policy{
		ConsecutiveFailures:    1,
		ConsecutiveRecoveries:  1,
		Cooldown:               time.Minute,
		LatencyThreshold:       100 * time.Millisecond,
		LatencyBreachCount:     1,
		SSLExpiryThresholdDays: 30,
	})

	down := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 40}, start)
	if down.Event != EventTargetDown || down.Suppressed || down.State != StateDown {
		t.Fatalf("initial down = %+v", down)
	}
	recovered := tracker.Evaluate(Check{IsUp: true, ResponseTime: 10 * time.Millisecond, SSLDaysRemaining: 40}, start.Add(10*time.Second))
	if recovered.Event != EventTargetRecovered || recovered.Suppressed || recovered.State != StateHealthy {
		t.Fatalf("recovery during cooldown = %+v", recovered)
	}
	downAgain := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 40}, start.Add(20*time.Second))
	if downAgain.Event != EventTargetDown || !downAgain.Suppressed || downAgain.State != StateDown || downAgain.PreviousState != StateHealthy {
		t.Fatalf("suppressed down = %+v", downAgain)
	}
	if downAgain.Reason == "" || downAgain.ConsecutiveFailures != 1 {
		t.Fatalf("suppressed snapshot = %+v", downAgain)
	}

	// Window is still anchored at the first down, so a different event is suppressed too.
	ssl := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 5}, start.Add(30*time.Second))
	if ssl.Event != EventSSLExpiring || !ssl.Suppressed || ssl.State != StateDown {
		t.Fatalf("suppressed ssl = %+v", ssl)
	}

	later := tracker.Evaluate(Check{IsUp: true, ResponseTime: 250 * time.Millisecond, SSLDaysRemaining: 40}, start.Add(2*time.Minute))
	// One success recovers (threshold 1) and the same slow check degrades.
	if later.Event != EventTargetDegraded || later.Suppressed || later.State != StateDegraded || later.PreviousState != StateDown {
		t.Fatalf("degraded after cooldown = %+v", later)
	}
	repeat := tracker.Evaluate(Check{IsUp: true, ResponseTime: 250 * time.Millisecond, SSLDaysRemaining: 40}, start.Add(2*time.Minute+10*time.Second))
	if repeat.Event != EventTargetDegraded || !repeat.Suppressed || repeat.State != StateDegraded {
		t.Fatalf("repeat degraded suppressed = %+v", repeat)
	}
	healthy := tracker.Evaluate(Check{IsUp: true, ResponseTime: 10 * time.Millisecond, SSLDaysRemaining: 40}, start.Add(2*time.Minute+20*time.Second))
	if healthy.Event != EventTargetHealthy || healthy.Suppressed || healthy.State != StateHealthy {
		t.Fatalf("healthy during cooldown = %+v", healthy)
	}
	// Healthy does not move the cooldown anchor, so the next degrade is still inside the window.
	again := tracker.Evaluate(Check{IsUp: true, ResponseTime: 250 * time.Millisecond, SSLDaysRemaining: 40}, start.Add(2*time.Minute+30*time.Second))
	if again.Event != EventTargetDegraded || !again.Suppressed || again.PreviousState != StateHealthy {
		t.Fatalf("degrade still cooling down = %+v", again)
	}
}
