package alerts

import (
	"encoding/json"
	"testing"
	"time"
)

var _baseTime = time.Date(2025, 10, 7, 12, 0, 0, 0, time.UTC)

type step struct {
	check          Check
	offset         time.Duration
	wantEvent      Event
	wantState      State
	wantPrevious   State
	wantFailures   int
	wantRecoveries int
	wantBreaches   int
	wantSuppressed bool
}

func up(responseTime time.Duration) Check {
	return Check{IsUp: true, ResponseTime: responseTime, SSLDaysRemaining: -1}
}

func down() Check {
	return Check{IsUp: false, SSLDaysRemaining: -1}
}

func withSSL(check Check, days int) Check {
	check.SSLDaysRemaining = days
	return check
}

func runSteps(t *testing.T, policy Policy, steps []step) {
	t.Helper()
	tracker := NewTracker(policy)
	for i, s := range steps {
		d := tracker.Evaluate(s.check, _baseTime.Add(s.offset))

		if d.Event != s.wantEvent {
			t.Errorf("step %d: event = %q, want %q", i, d.Event, s.wantEvent)
		}
		if d.State != s.wantState {
			t.Errorf("step %d: state = %q, want %q", i, d.State, s.wantState)
		}
		if d.PreviousState != s.wantPrevious {
			t.Errorf("step %d: previous state = %q, want %q", i, d.PreviousState, s.wantPrevious)
		}
		if d.ConsecutiveFailures != s.wantFailures {
			t.Errorf("step %d: consecutive failures = %d, want %d", i, d.ConsecutiveFailures, s.wantFailures)
		}
		if d.ConsecutiveRecoveries != s.wantRecoveries {
			t.Errorf("step %d: consecutive recoveries = %d, want %d", i, d.ConsecutiveRecoveries, s.wantRecoveries)
		}
		if d.LatencyBreaches != s.wantBreaches {
			t.Errorf("step %d: latency breaches = %d, want %d", i, d.LatencyBreaches, s.wantBreaches)
		}
		if d.Suppressed != s.wantSuppressed {
			t.Errorf("step %d: suppressed = %v, want %v", i, d.Suppressed, s.wantSuppressed)
		}
		if d.SSLDaysRemaining != s.check.SSLDaysRemaining {
			t.Errorf("step %d: ssl days remaining = %d, want %d", i, d.SSLDaysRemaining, s.check.SSLDaysRemaining)
		}
		if d.Event != EventNone && d.Reason == "" {
			t.Errorf("step %d: expected a reason for event %q", i, d.Event)
		}
		if d.Event == EventNone && d.Reason != "" {
			t.Errorf("step %d: unexpected reason %q without an event", i, d.Reason)
		}
	}
}

func TestDefaultPolicyAlertsOnFirstFailureAndRecovery(t *testing.T) {
	runSteps(t, Policy{}, []step{
		{check: up(10 * time.Second), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 1},
		{check: down(), wantEvent: EventTargetDown, wantState: StateDown, wantPrevious: StateHealthy, wantFailures: 1},
		{check: down(), wantState: StateDown, wantPrevious: StateDown, wantFailures: 2},
		{check: up(0), wantEvent: EventTargetRecovered, wantState: StateHealthy, wantPrevious: StateDown, wantRecoveries: 1},
		{check: up(0), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 2},
	})
}

func TestConsecutiveFailuresAndRecoveries(t *testing.T) {
	runSteps(t, Policy{ConsecutiveFailures: 3, ConsecutiveRecoveries: 2}, []step{
		{check: down(), wantState: StateHealthy, wantPrevious: StateHealthy, wantFailures: 1},
		{check: down(), wantState: StateHealthy, wantPrevious: StateHealthy, wantFailures: 2},
		{check: up(0), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 1},
		{check: down(), wantState: StateHealthy, wantPrevious: StateHealthy, wantFailures: 1},
		{check: down(), wantState: StateHealthy, wantPrevious: StateHealthy, wantFailures: 2},
		{check: down(), wantEvent: EventTargetDown, wantState: StateDown, wantPrevious: StateHealthy, wantFailures: 3},
		{check: down(), wantState: StateDown, wantPrevious: StateDown, wantFailures: 4},
		{check: up(0), wantState: StateDown, wantPrevious: StateDown, wantRecoveries: 1},
		{check: down(), wantState: StateDown, wantPrevious: StateDown, wantFailures: 1},
		{check: up(0), wantState: StateDown, wantPrevious: StateDown, wantRecoveries: 1},
		{check: up(0), wantEvent: EventTargetRecovered, wantState: StateHealthy, wantPrevious: StateDown, wantRecoveries: 2},
	})
}

func TestLatencyDegradedAndHealthy(t *testing.T) {
	policy := Policy{LatencyThreshold: 200 * time.Millisecond, LatencyBreachCount: 2}
	runSteps(t, policy, []step{
		{check: up(300 * time.Millisecond), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 1, wantBreaches: 1},
		{check: up(100 * time.Millisecond), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 2},
		{check: up(300 * time.Millisecond), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 3, wantBreaches: 1},
		{check: up(300 * time.Millisecond), wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateHealthy, wantRecoveries: 4, wantBreaches: 2},
		{check: up(300 * time.Millisecond), wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateDegraded, wantRecoveries: 5, wantBreaches: 3},
		{check: up(100 * time.Millisecond), wantEvent: EventTargetHealthy, wantState: StateHealthy, wantPrevious: StateDegraded, wantRecoveries: 6},
		{check: up(100 * time.Millisecond), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 7},
	})
}

func TestLatencyBreachCountDefaultsToOne(t *testing.T) {
	runSteps(t, Policy{LatencyThreshold: 200 * time.Millisecond}, []step{
		{check: up(300 * time.Millisecond), wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateHealthy, wantRecoveries: 1, wantBreaches: 1},
	})
	runSteps(t, Policy{LatencyThreshold: 200 * time.Millisecond, LatencyBreachCount: -4}, []step{
		{check: up(300 * time.Millisecond), wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateHealthy, wantRecoveries: 1, wantBreaches: 1},
	})
}

func TestLatencyDisabledWithoutThreshold(t *testing.T) {
	runSteps(t, Policy{LatencyBreachCount: 1}, []step{
		{check: up(time.Minute), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 1},
		{check: up(time.Minute), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 2},
	})
}

func TestLatencyBreachesResetOnFailureAndWhileDown(t *testing.T) {
	policy := Policy{ConsecutiveRecoveries: 2, LatencyThreshold: 200 * time.Millisecond, LatencyBreachCount: 2}
	slow := up(300 * time.Millisecond)
	runSteps(t, policy, []step{
		{check: slow, wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 1, wantBreaches: 1},
		{check: down(), wantEvent: EventTargetDown, wantState: StateDown, wantPrevious: StateHealthy, wantFailures: 1},
		{check: slow, wantState: StateDown, wantPrevious: StateDown, wantRecoveries: 1},
		{check: slow, wantEvent: EventTargetRecovered, wantState: StateHealthy, wantPrevious: StateDown, wantRecoveries: 2},
		{check: slow, wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 3, wantBreaches: 1},
		{check: slow, wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateHealthy, wantRecoveries: 4, wantBreaches: 2},
	})
}

func TestDegradedTargetGoesDown(t *testing.T) {
	policy := Policy{ConsecutiveFailures: 2, LatencyThreshold: 200 * time.Millisecond}
	runSteps(t, policy, []step{
		{check: up(300 * time.Millisecond), wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateHealthy, wantRecoveries: 1, wantBreaches: 1},
		{check: down(), wantState: StateDegraded, wantPrevious: StateDegraded, wantFailures: 1},
		{check: down(), wantEvent: EventTargetDown, wantState: StateDown, wantPrevious: StateDegraded, wantFailures: 2},
		{check: up(300 * time.Millisecond), wantEvent: EventTargetRecovered, wantState: StateHealthy, wantPrevious: StateDown, wantRecoveries: 1},
	})
}

func TestSSLExpiringFiresOnceUntilRearmed(t *testing.T) {
	policy := Policy{SSLExpiryThresholdDays: 14}
	runSteps(t, policy, []step{
		{check: withSSL(up(0), 30), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 1},
		{check: withSSL(up(0), 14), wantEvent: EventSSLExpiring, wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 2},
		{check: withSSL(up(0), 10), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 3},
		{check: withSSL(up(0), -1), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 4},
		{check: withSSL(up(0), 9), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 5},
		{check: withSSL(up(0), 90), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 6},
		{check: withSSL(up(0), 0), wantEvent: EventSSLExpiring, wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 7},
	})
}

func TestSSLNotApplicableOrDisabled(t *testing.T) {
	runSteps(t, Policy{SSLExpiryThresholdDays: 14}, []step{
		{check: withSSL(up(0), -1), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 1},
		{check: withSSL(up(0), -30), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 2},
	})
	runSteps(t, Policy{}, []step{
		{check: withSSL(up(0), 0), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 1},
		{check: withSSL(up(0), 3), wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 2},
	})
}

func TestSSLExpiringDoesNotChangeState(t *testing.T) {
	policy := Policy{ConsecutiveFailures: 3, SSLExpiryThresholdDays: 7}
	runSteps(t, policy, []step{
		{check: withSSL(down(), 5), wantEvent: EventSSLExpiring, wantState: StateHealthy, wantPrevious: StateHealthy, wantFailures: 1},
		{check: withSSL(down(), 5), wantState: StateHealthy, wantPrevious: StateHealthy, wantFailures: 2},
	})
}

func TestSSLExpiringDeferredBehindAvailabilityEvent(t *testing.T) {
	runSteps(t, Policy{SSLExpiryThresholdDays: 7}, []step{
		{check: withSSL(down(), 5), wantEvent: EventTargetDown, wantState: StateDown, wantPrevious: StateHealthy, wantFailures: 1},
		{check: withSSL(down(), 5), wantEvent: EventSSLExpiring, wantState: StateDown, wantPrevious: StateDown, wantFailures: 2},
		{check: withSSL(down(), 5), wantState: StateDown, wantPrevious: StateDown, wantFailures: 3},
	})
}

func TestCooldownSuppressesNonRecoveryEvents(t *testing.T) {
	policy := Policy{
		Cooldown:               time.Minute,
		LatencyThreshold:       200 * time.Millisecond,
		SSLExpiryThresholdDays: 7,
	}
	slow := up(300 * time.Millisecond)
	runSteps(t, policy, []step{
		{check: down(), offset: 0, wantEvent: EventTargetDown, wantState: StateDown, wantPrevious: StateHealthy, wantFailures: 1},
		{check: slow, offset: 10 * time.Second, wantEvent: EventTargetRecovered, wantState: StateHealthy, wantPrevious: StateDown, wantRecoveries: 1},
		{check: slow, offset: 20 * time.Second, wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateHealthy, wantRecoveries: 2, wantBreaches: 1, wantSuppressed: true},
		{check: up(0), offset: 30 * time.Second, wantEvent: EventTargetHealthy, wantState: StateHealthy, wantPrevious: StateDegraded, wantRecoveries: 3},
		{check: down(), offset: 40 * time.Second, wantEvent: EventTargetDown, wantState: StateDown, wantPrevious: StateHealthy, wantFailures: 1, wantSuppressed: true},
		{check: withSSL(up(0), 3), offset: 50 * time.Second, wantEvent: EventTargetRecovered, wantState: StateHealthy, wantPrevious: StateDown, wantRecoveries: 1},
		{check: withSSL(up(0), 3), offset: 55 * time.Second, wantEvent: EventSSLExpiring, wantState: StateHealthy, wantPrevious: StateHealthy, wantRecoveries: 2, wantSuppressed: true},
		{check: slow, offset: 60 * time.Second, wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateHealthy, wantRecoveries: 3, wantBreaches: 1},
		{check: slow, offset: 90 * time.Second, wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateDegraded, wantRecoveries: 4, wantBreaches: 2, wantSuppressed: true},
		{check: slow, offset: 120 * time.Second, wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateDegraded, wantRecoveries: 5, wantBreaches: 3},
	})
}

func TestZeroCooldownNeverSuppresses(t *testing.T) {
	policy := Policy{LatencyThreshold: 200 * time.Millisecond}
	slow := up(300 * time.Millisecond)
	runSteps(t, policy, []step{
		{check: slow, wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateHealthy, wantRecoveries: 1, wantBreaches: 1},
		{check: slow, wantEvent: EventTargetDegraded, wantState: StateDegraded, wantPrevious: StateDegraded, wantRecoveries: 2, wantBreaches: 2},
		{check: down(), wantEvent: EventTargetDown, wantState: StateDown, wantPrevious: StateDegraded, wantFailures: 1},
	})
}

func TestNewTrackerStartsHealthy(t *testing.T) {
	d := NewTracker(Policy{ConsecutiveFailures: 5}).Evaluate(down(), _baseTime)
	if d.Event != EventNone || d.State != StateHealthy || d.PreviousState != StateHealthy || d.ConsecutiveFailures != 1 {
		t.Errorf("unexpected first decision: %+v", d)
	}
}

func TestSerializedValues(t *testing.T) {
	values := map[string]any{
		"healthy":          StateHealthy,
		"degraded":         StateDegraded,
		"down":             StateDown,
		"target_down":      EventTargetDown,
		"target_recovered": EventTargetRecovered,
		"target_degraded":  EventTargetDegraded,
		"target_healthy":   EventTargetHealthy,
		"ssl_expiring":     EventSSLExpiring,
	}
	for want, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %v: %v", value, err)
		}
		if string(data) != `"`+want+`"` {
			t.Errorf("marshal = %s, want %q", data, want)
		}
	}
}
