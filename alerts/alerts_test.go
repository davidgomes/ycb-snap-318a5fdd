package alerts

import (
	"testing"
	"time"
)

var _baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type step struct {
	check      Check
	at         time.Duration
	event      Event
	state      State
	suppressed bool
}

func up(ms int) Check {
	return Check{IsUp: true, ResponseTime: time.Duration(ms) * time.Millisecond, SSLDaysRemaining: -1}
}

func down() Check {
	return Check{IsUp: false, SSLDaysRemaining: -1}
}

func withSSL(c Check, days int) Check {
	c.SSLDaysRemaining = days
	return c
}

func runSteps(t *testing.T, policy Policy, steps []step) []Decision {
	t.Helper()
	tracker := NewTracker(policy)
	decisions := make([]Decision, 0, len(steps))
	for i, s := range steps {
		d := tracker.Evaluate(s.check, _baseTime.Add(s.at))
		if d.Event != s.event {
			t.Errorf("step %d: event = %q, want %q", i, d.Event, s.event)
		}
		if d.State != s.state {
			t.Errorf("step %d: state = %q, want %q", i, d.State, s.state)
		}
		if d.Suppressed != s.suppressed {
			t.Errorf("step %d: suppressed = %v, want %v", i, d.Suppressed, s.suppressed)
		}
		if d.Event != EventNone && d.Reason == "" {
			t.Errorf("step %d: event %q has empty reason", i, d.Event)
		}
		decisions = append(decisions, d)
	}
	return decisions
}

func TestDefaultPolicyDownAndRecovery(t *testing.T) {
	runSteps(t, Policy{}, []step{
		{check: up(100), event: EventNone, state: StateHealthy},
		{check: down(), event: EventTargetDown, state: StateDown},
		{check: down(), event: EventNone, state: StateDown},
		{check: up(100), event: EventTargetRecovered, state: StateHealthy},
		{check: up(100), event: EventNone, state: StateHealthy},
	})
}

func TestConsecutiveFailuresAndRecoveries(t *testing.T) {
	decisions := runSteps(t, Policy{ConsecutiveFailures: 3, ConsecutiveRecoveries: 2}, []step{
		{check: down(), event: EventNone, state: StateHealthy},
		{check: down(), event: EventNone, state: StateHealthy},
		{check: up(10), event: EventNone, state: StateHealthy},
		{check: down(), event: EventNone, state: StateHealthy},
		{check: down(), event: EventNone, state: StateHealthy},
		{check: down(), event: EventTargetDown, state: StateDown},
		{check: up(10), event: EventNone, state: StateDown},
		{check: down(), event: EventNone, state: StateDown},
		{check: up(10), event: EventNone, state: StateDown},
		{check: up(10), event: EventTargetRecovered, state: StateHealthy},
	})

	if got := decisions[5]; got.ConsecutiveFailures != 3 || got.PreviousState != StateHealthy {
		t.Errorf("down decision = %+v, want 3 failures from healthy", got)
	}
	if got := decisions[7]; got.ConsecutiveFailures != 1 || got.ConsecutiveRecoveries != 0 {
		t.Errorf("counters while down = %+v", got)
	}
	if got := decisions[9]; got.ConsecutiveRecoveries != 2 || got.PreviousState != StateDown {
		t.Errorf("recovery decision = %+v, want 2 recoveries from down", got)
	}
}

func TestLatencyDegradedAndHealthy(t *testing.T) {
	policy := Policy{LatencyThreshold: 500 * time.Millisecond, LatencyBreachCount: 2}
	decisions := runSteps(t, policy, []step{
		{check: up(600), event: EventNone, state: StateHealthy},
		{check: up(100), event: EventNone, state: StateHealthy},
		{check: up(600), event: EventNone, state: StateHealthy},
		{check: up(700), event: EventTargetDegraded, state: StateDegraded},
		{check: up(800), event: EventTargetDegraded, state: StateDegraded},
		{check: up(100), event: EventTargetHealthy, state: StateHealthy},
	})

	if got := decisions[0].LatencyBreaches; got != 1 {
		t.Errorf("breaches after first slow check = %d, want 1", got)
	}
	if got := decisions[4]; got.LatencyBreaches != 3 || got.PreviousState != StateHealthy {
		t.Errorf("repeated degraded decision = %+v", got)
	}
	if got := decisions[5]; got.LatencyBreaches != 0 || got.PreviousState != StateDegraded {
		t.Errorf("healthy decision = %+v", got)
	}
}

func TestLatencyDisabledAndBreachCountDefault(t *testing.T) {
	runSteps(t, Policy{}, []step{
		{check: up(10000), event: EventNone, state: StateHealthy},
	})
	runSteps(t, Policy{LatencyThreshold: 100 * time.Millisecond, LatencyBreachCount: -3}, []step{
		{check: up(200), event: EventTargetDegraded, state: StateDegraded},
	})
}

func TestLatencyBreachesResetOnFailureAndWhileDown(t *testing.T) {
	policy := Policy{LatencyThreshold: 100 * time.Millisecond, LatencyBreachCount: 2, ConsecutiveFailures: 2, ConsecutiveRecoveries: 2}
	decisions := runSteps(t, policy, []step{
		{check: up(200), event: EventNone, state: StateHealthy},
		{check: down(), event: EventNone, state: StateHealthy},
		{check: up(200), event: EventNone, state: StateHealthy},
		{check: down(), event: EventNone, state: StateHealthy},
		{check: down(), event: EventTargetDown, state: StateDown},
		{check: up(200), event: EventNone, state: StateDown},
		{check: up(200), event: EventTargetRecovered, state: StateHealthy},
		{check: up(200), event: EventNone, state: StateHealthy},
		{check: up(200), event: EventTargetDegraded, state: StateDegraded},
	})

	if got := decisions[1].LatencyBreaches; got != 0 {
		t.Errorf("breaches after failure = %d, want 0", got)
	}
	if got := decisions[2].LatencyBreaches; got != 1 {
		t.Errorf("breaches after failure then slow = %d, want 1", got)
	}
	for _, i := range []int{5, 6} {
		if got := decisions[i].LatencyBreaches; got != 0 {
			t.Errorf("step %d: breaches while down = %d, want 0", i, got)
		}
	}
	if got := decisions[7].LatencyBreaches; got != 1 {
		t.Errorf("breaches after recovery = %d, want 1", got)
	}
}

func TestDegradedToDown(t *testing.T) {
	policy := Policy{LatencyThreshold: 100 * time.Millisecond}
	decisions := runSteps(t, policy, []step{
		{check: up(200), event: EventTargetDegraded, state: StateDegraded},
		{check: down(), event: EventTargetDown, state: StateDown},
		{check: up(200), event: EventTargetRecovered, state: StateHealthy},
	})
	if got := decisions[1].PreviousState; got != StateDegraded {
		t.Errorf("previous state = %q, want %q", got, StateDegraded)
	}
}

func TestSSLExpiring(t *testing.T) {
	policy := Policy{SSLExpiryThresholdDays: 14}
	decisions := runSteps(t, policy, []step{
		{check: withSSL(up(10), 30), event: EventNone, state: StateHealthy},
		{check: withSSL(up(10), 14), event: EventSSLExpiring, state: StateHealthy},
		{check: withSSL(up(10), 13), event: EventNone, state: StateHealthy},
		{check: withSSL(up(10), -1), event: EventNone, state: StateHealthy},
		{check: withSSL(up(10), 12), event: EventNone, state: StateHealthy},
		{check: withSSL(up(10), 90), event: EventNone, state: StateHealthy},
		{check: withSSL(up(10), 5), event: EventSSLExpiring, state: StateHealthy},
	})
	if got := decisions[2].SSLDaysRemaining; got != 13 {
		t.Errorf("ssl days = %d, want 13", got)
	}
	if got := decisions[3].SSLDaysRemaining; got != -1 {
		t.Errorf("ssl days = %d, want -1", got)
	}
}

func TestSSLDisabledAndNotApplicable(t *testing.T) {
	runSteps(t, Policy{}, []step{
		{check: withSSL(up(10), 1), event: EventNone, state: StateHealthy},
	})
	runSteps(t, Policy{SSLExpiryThresholdDays: 30}, []step{
		{check: withSSL(up(10), -1), event: EventNone, state: StateHealthy},
	})
}

func TestSSLExpiringDeferredBehindStateEvent(t *testing.T) {
	runSteps(t, Policy{SSLExpiryThresholdDays: 30}, []step{
		{check: withSSL(down(), 10), event: EventTargetDown, state: StateDown},
		{check: withSSL(down(), 10), event: EventSSLExpiring, state: StateDown},
		{check: withSSL(down(), 10), event: EventNone, state: StateDown},
	})
}

func TestCooldownSuppression(t *testing.T) {
	policy := Policy{Cooldown: time.Minute, LatencyThreshold: 100 * time.Millisecond, SSLExpiryThresholdDays: 7}
	decisions := runSteps(t, policy, []step{
		{check: down(), at: 0, event: EventTargetDown, state: StateDown},
		{check: up(10), at: 10 * time.Second, event: EventTargetRecovered, state: StateHealthy},
		{check: up(200), at: 20 * time.Second, event: EventTargetDegraded, state: StateDegraded, suppressed: true},
		{check: up(10), at: 30 * time.Second, event: EventTargetHealthy, state: StateHealthy},
		{check: withSSL(up(10), 3), at: 40 * time.Second, event: EventSSLExpiring, state: StateHealthy, suppressed: true},
		{check: down(), at: 60 * time.Second, event: EventTargetDown, state: StateDown},
		{check: up(10), at: 70 * time.Second, event: EventTargetRecovered, state: StateHealthy},
		{check: down(), at: 100 * time.Second, event: EventTargetDown, state: StateDown, suppressed: true},
		{check: down(), at: 110 * time.Second, event: EventNone, state: StateDown},
	})

	if got := decisions[2]; got.PreviousState != StateHealthy || got.LatencyBreaches != 1 {
		t.Errorf("suppressed decision snapshot = %+v", got)
	}
	if got := decisions[7]; got.ConsecutiveFailures != 1 || got.PreviousState != StateHealthy {
		t.Errorf("suppressed down snapshot = %+v", got)
	}
}

func TestSnapshotOnNoEvent(t *testing.T) {
	tracker := NewTracker(Policy{ConsecutiveFailures: 2, LatencyThreshold: time.Second, LatencyBreachCount: 3})
	tracker.Evaluate(up(2000), _baseTime)
	d := tracker.Evaluate(Check{IsUp: true, ResponseTime: 2 * time.Second, SSLDaysRemaining: 42}, _baseTime)
	want := Decision{
		Event:                 EventNone,
		State:                 StateHealthy,
		PreviousState:         StateHealthy,
		ConsecutiveRecoveries: 2,
		LatencyBreaches:       2,
		SSLDaysRemaining:      42,
	}
	if d != want {
		t.Errorf("decision = %+v, want %+v", d, want)
	}

	d = tracker.Evaluate(down(), _baseTime)
	if d.Event != EventNone || d.ConsecutiveFailures != 1 || d.ConsecutiveRecoveries != 0 || d.LatencyBreaches != 0 {
		t.Errorf("decision after single failure = %+v", d)
	}
}
