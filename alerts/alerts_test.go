package alerts

import (
	"testing"
	"time"
)

func TestDefaultsRequireConsecutiveBeforeDownAndRecovery(t *testing.T) {
	tracker := NewTracker(Policy{})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	up := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now)
	if up.Event != EventNone || up.State != StateHealthy || up.PreviousState != StateHealthy {
		t.Fatalf("initial up = %+v", up)
	}
	if up.Reason != "" {
		t.Fatalf("EventNone should not set a reason, got %q", up.Reason)
	}

	down := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now.Add(time.Second))
	if down.Event != EventTargetDown || down.State != StateDown || down.PreviousState != StateHealthy {
		t.Fatalf("first failure = %+v", down)
	}
	if down.ConsecutiveFailures != 1 || down.Reason == "" || down.Suppressed {
		t.Fatalf("down decision = %+v", down)
	}

	still := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now.Add(2*time.Second))
	if still.Event != EventNone || still.State != StateDown || still.ConsecutiveFailures != 2 {
		t.Fatalf("continued failure = %+v", still)
	}

	recovered := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now.Add(3*time.Second))
	if recovered.Event != EventTargetRecovered || recovered.State != StateHealthy || recovered.PreviousState != StateDown {
		t.Fatalf("recovery = %+v", recovered)
	}
	if recovered.Suppressed || recovered.Reason == "" {
		t.Fatalf("recovery should always be delivered, got %+v", recovered)
	}
}

func TestConsecutiveThresholdsAndLatencyReset(t *testing.T) {
	tracker := NewTracker(Policy{
		ConsecutiveFailures:   3,
		ConsecutiveRecoveries: 2,
		LatencyThreshold:      100 * time.Millisecond,
		LatencyBreachCount:    2,
	})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	for i := 1; i <= 2; i++ {
		decision := tracker.Evaluate(Check{IsUp: false, ResponseTime: 500 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(time.Duration(i)*time.Second))
		if decision.Event != EventNone || decision.State != StateHealthy || decision.ConsecutiveFailures != i || decision.LatencyBreaches != 0 {
			t.Fatalf("failure %d = %+v", i, decision)
		}
	}

	down := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now.Add(3*time.Second))
	if down.Event != EventTargetDown || down.ConsecutiveFailures != 3 {
		t.Fatalf("down = %+v", down)
	}

	slowWhileDown := tracker.Evaluate(Check{IsUp: true, ResponseTime: 800 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(4*time.Second))
	if slowWhileDown.Event != EventNone || slowWhileDown.State != StateDown || slowWhileDown.LatencyBreaches != 0 || slowWhileDown.ConsecutiveRecoveries != 1 {
		t.Fatalf("success while down = %+v", slowWhileDown)
	}

	recovered := tracker.Evaluate(Check{IsUp: true, ResponseTime: 800 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(5*time.Second))
	if recovered.Event != EventTargetRecovered || recovered.State != StateHealthy || recovered.LatencyBreaches != 0 {
		t.Fatalf("recovered = %+v", recovered)
	}

	firstSlow := tracker.Evaluate(Check{IsUp: true, ResponseTime: 150 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(6*time.Second))
	if firstSlow.Event != EventNone || firstSlow.State != StateHealthy || firstSlow.LatencyBreaches != 1 {
		t.Fatalf("first slow = %+v", firstSlow)
	}

	failed := tracker.Evaluate(Check{IsUp: false, ResponseTime: 150 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(7*time.Second))
	if failed.LatencyBreaches != 0 || failed.ConsecutiveFailures != 1 || failed.State != StateHealthy {
		t.Fatalf("failure should reset latency = %+v", failed)
	}
}

func TestLatencyDegradedRepeatsUntilHealthy(t *testing.T) {
	tracker := NewTracker(Policy{
		LatencyThreshold:   50 * time.Millisecond,
		LatencyBreachCount: 0,
	})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	first := tracker.Evaluate(Check{IsUp: true, ResponseTime: 80 * time.Millisecond, SSLDaysRemaining: -1}, now)
	if first.Event != EventTargetDegraded || first.State != StateDegraded || first.PreviousState != StateHealthy || first.LatencyBreaches != 1 || first.Reason == "" {
		t.Fatalf("first breach = %+v", first)
	}

	second := tracker.Evaluate(Check{IsUp: true, ResponseTime: 90 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(time.Second))
	if second.Event != EventTargetDegraded || second.State != StateDegraded || second.PreviousState != StateDegraded || second.LatencyBreaches != 2 {
		t.Fatalf("continued breach = %+v", second)
	}

	healthy := tracker.Evaluate(Check{IsUp: true, ResponseTime: 40 * time.Millisecond, SSLDaysRemaining: -1}, now.Add(2*time.Second))
	if healthy.Event != EventTargetHealthy || healthy.State != StateHealthy || healthy.PreviousState != StateDegraded || healthy.LatencyBreaches != 0 || healthy.Suppressed {
		t.Fatalf("healthy = %+v", healthy)
	}
}

func TestLatencyDisabledAndEqualThreshold(t *testing.T) {
	tracker := NewTracker(Policy{})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	slow := tracker.Evaluate(Check{IsUp: true, ResponseTime: 5 * time.Second, SSLDaysRemaining: -1}, now)
	if slow.Event != EventNone || slow.State != StateHealthy || slow.LatencyBreaches != 0 {
		t.Fatalf("latency disabled = %+v", slow)
	}

	bounded := NewTracker(Policy{LatencyThreshold: 100 * time.Millisecond, LatencyBreachCount: 1})
	equal := bounded.Evaluate(Check{IsUp: true, ResponseTime: 100 * time.Millisecond, SSLDaysRemaining: -1}, now)
	if equal.Event != EventNone || equal.LatencyBreaches != 0 {
		t.Fatalf("equal to threshold = %+v", equal)
	}
}

func TestSSLExpiringIsOneShotAndDoesNotChangeState(t *testing.T) {
	tracker := NewTracker(Policy{SSLExpiryThresholdDays: 14})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	negative := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now)
	if negative.Event != EventNone || negative.SSLDaysRemaining != -1 {
		t.Fatalf("negative ssl = %+v", negative)
	}

	above := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 30}, now.Add(time.Second))
	if above.Event != EventNone {
		t.Fatalf("above threshold = %+v", above)
	}

	first := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 10}, now.Add(2*time.Second))
	if first.Event != EventSSLExpiring || first.State != StateHealthy || first.PreviousState != StateHealthy || first.Reason == "" {
		t.Fatalf("ssl = %+v", first)
	}

	again := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 9}, now.Add(3*time.Second))
	if again.Event != EventNone || again.State != StateHealthy {
		t.Fatalf("repeat ssl = %+v", again)
	}

	cleared := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 20}, now.Add(4*time.Second))
	if cleared.Event != EventNone {
		t.Fatalf("cleared ssl = %+v", cleared)
	}

	reentered := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 14}, now.Add(5*time.Second))
	if reentered.Event != EventSSLExpiring || reentered.State != StateHealthy {
		t.Fatalf("reentered ssl = %+v", reentered)
	}
}

func TestSSLDisabledAndNegativeNeverRearms(t *testing.T) {
	disabled := NewTracker(Policy{SSLExpiryThresholdDays: 0})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	decision := disabled.Evaluate(Check{IsUp: true, SSLDaysRemaining: 1}, now)
	if decision.Event != EventNone {
		t.Fatalf("disabled ssl = %+v", decision)
	}

	tracker := NewTracker(Policy{SSLExpiryThresholdDays: 7})
	first := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 3}, now)
	if first.Event != EventSSLExpiring {
		t.Fatalf("first = %+v", first)
	}
	na := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -5}, now.Add(time.Second))
	if na.Event != EventNone || na.SSLDaysRemaining != -5 {
		t.Fatalf("not applicable = %+v", na)
	}
	still := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 2}, now.Add(2*time.Second))
	if still.Event != EventNone {
		t.Fatalf("negative should not rearm ssl = %+v", still)
	}
}

func TestCooldownSuppressesNonRecoveryAcrossEventTypes(t *testing.T) {
	tracker := NewTracker(Policy{
		Cooldown:               30 * time.Second,
		LatencyThreshold:       10 * time.Millisecond,
		LatencyBreachCount:     1,
		SSLExpiryThresholdDays: 5,
	})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	down := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 30}, now)
	if down.Event != EventTargetDown || down.Suppressed || down.State != StateDown {
		t.Fatalf("down = %+v", down)
	}

	recovered := tracker.Evaluate(Check{IsUp: true, ResponseTime: time.Millisecond, SSLDaysRemaining: 30}, now.Add(5*time.Second))
	if recovered.Event != EventTargetRecovered || recovered.Suppressed || recovered.State != StateHealthy {
		t.Fatalf("recovered during cooldown = %+v", recovered)
	}

	degraded := tracker.Evaluate(Check{IsUp: true, ResponseTime: 40 * time.Millisecond, SSLDaysRemaining: 2}, now.Add(10*time.Second))
	if degraded.Event != EventTargetDegraded || !degraded.Suppressed || degraded.State != StateDegraded || degraded.PreviousState != StateHealthy || degraded.Reason == "" {
		t.Fatalf("degraded should be evaluated but suppressed = %+v", degraded)
	}

	healthy := tracker.Evaluate(Check{IsUp: true, ResponseTime: time.Millisecond, SSLDaysRemaining: 2}, now.Add(15*time.Second))
	if healthy.Event != EventTargetHealthy || healthy.Suppressed || healthy.State != StateHealthy {
		t.Fatalf("healthy = %+v", healthy)
	}

	ssl := tracker.Evaluate(Check{IsUp: true, ResponseTime: time.Millisecond, SSLDaysRemaining: 2}, now.Add(20*time.Second))
	if ssl.Event != EventSSLExpiring || !ssl.Suppressed || ssl.State != StateHealthy {
		t.Fatalf("ssl during cooldown = %+v", ssl)
	}

	after := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 30}, now.Add(50*time.Second))
	if after.Event != EventTargetDown || after.Suppressed || after.ConsecutiveFailures != 1 {
		t.Fatalf("after cooldown = %+v", after)
	}
}

func TestRepeatedDegradedWinsOverSSLUntilQuiet(t *testing.T) {
	tracker := NewTracker(Policy{
		LatencyThreshold:       10 * time.Millisecond,
		LatencyBreachCount:     1,
		SSLExpiryThresholdDays: 30,
	})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	slow := Check{IsUp: true, ResponseTime: 50 * time.Millisecond, SSLDaysRemaining: 5}

	first := tracker.Evaluate(slow, now)
	if first.Event != EventTargetDegraded || first.State != StateDegraded {
		t.Fatalf("first = %+v", first)
	}
	second := tracker.Evaluate(slow, now.Add(time.Second))
	if second.Event != EventTargetDegraded || second.State != StateDegraded || second.SSLDaysRemaining != 5 {
		t.Fatalf("second = %+v", second)
	}
	healthy := tracker.Evaluate(Check{IsUp: true, ResponseTime: time.Millisecond, SSLDaysRemaining: 5}, now.Add(2*time.Second))
	if healthy.Event != EventTargetHealthy || healthy.State != StateHealthy {
		t.Fatalf("healthy = %+v", healthy)
	}
	ssl := tracker.Evaluate(Check{IsUp: true, ResponseTime: time.Millisecond, SSLDaysRemaining: 5}, now.Add(3*time.Second))
	if ssl.Event != EventSSLExpiring || ssl.State != StateHealthy {
		t.Fatalf("ssl after quiet check = %+v", ssl)
	}
}

func TestSnapshotMatchesTrackerWhenQuiet(t *testing.T) {
	tracker := NewTracker(Policy{ConsecutiveFailures: 2})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	decision := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 12}, now)
	if decision.Event != EventNone || decision.State != StateHealthy || decision.PreviousState != StateHealthy {
		t.Fatalf("quiet = %+v", decision)
	}
	if decision.ConsecutiveFailures != 1 || decision.ConsecutiveRecoveries != 0 || decision.LatencyBreaches != 0 || decision.SSLDaysRemaining != 12 {
		t.Fatalf("snapshot = %+v", decision)
	}
}
