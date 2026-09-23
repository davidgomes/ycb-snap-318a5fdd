package alerts

import (
	"testing"
	"time"
)

var _base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func up(ms int) Check {
	return Check{IsUp: true, ResponseTime: time.Duration(ms) * time.Millisecond, SSLDaysRemaining: -1}
}

func down() Check {
	return Check{IsUp: false, SSLDaysRemaining: -1}
}

type step struct {
	check      Check
	offset     time.Duration
	event      Event
	state      State
	suppressed bool
}

func run(t *testing.T, policy Policy, steps []step) []Decision {
	t.Helper()
	tracker := NewTracker(policy)
	decisions := make([]Decision, 0, len(steps))
	for i, s := range steps {
		d := tracker.Evaluate(s.check, _base.Add(s.offset))
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
			t.Errorf("step %d: missing reason for %q", i, d.Event)
		}
		decisions = append(decisions, d)
	}
	return decisions
}

func TestDefaultsAlertOnFirstFailureAndRecovery(t *testing.T) {
	run(t, Policy{}, []step{
		{check: up(10), event: EventNone, state: StateHealthy},
		{check: down(), event: EventTargetDown, state: StateDown},
		{check: down(), event: EventNone, state: StateDown},
		{check: up(10), event: EventTargetRecovered, state: StateHealthy},
		{check: up(10000), event: EventNone, state: StateHealthy},
	})
}

func TestConsecutiveFailuresAndRecoveries(t *testing.T) {
	ds := run(t, Policy{ConsecutiveFailures: 3, ConsecutiveRecoveries: 2}, []step{
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
	if ds[5].ConsecutiveFailures != 3 || ds[5].PreviousState != StateHealthy {
		t.Errorf("down decision snapshot = %+v", ds[5])
	}
	if ds[6].ConsecutiveRecoveries != 1 || ds[6].ConsecutiveFailures != 0 {
		t.Errorf("recovering snapshot = %+v", ds[6])
	}
	if ds[9].ConsecutiveRecoveries != 2 || ds[9].PreviousState != StateDown {
		t.Errorf("recovered snapshot = %+v", ds[9])
	}
}

func TestLatencyDegradation(t *testing.T) {
	ds := run(t, Policy{LatencyThreshold: 100 * time.Millisecond, LatencyBreachCount: 2}, []step{
		{check: up(150), event: EventNone, state: StateHealthy},
		{check: up(50), event: EventNone, state: StateHealthy},
		{check: up(150), event: EventNone, state: StateHealthy},
		{check: up(150), event: EventTargetDegraded, state: StateDegraded},
		{check: up(200), event: EventTargetDegraded, state: StateDegraded},
		{check: up(100), event: EventTargetHealthy, state: StateHealthy},
	})
	if ds[4].LatencyBreaches != 3 || ds[4].PreviousState != StateDegraded {
		t.Errorf("degraded snapshot = %+v", ds[4])
	}
	if ds[5].LatencyBreaches != 0 {
		t.Errorf("healthy snapshot = %+v", ds[5])
	}
}

func TestLatencyBreachCountDefaultsToOne(t *testing.T) {
	run(t, Policy{LatencyThreshold: 100 * time.Millisecond}, []step{
		{check: up(150), event: EventTargetDegraded, state: StateDegraded},
	})
}

func TestLatencyResetsOnFailureAndWhileDown(t *testing.T) {
	ds := run(t, Policy{LatencyThreshold: 100 * time.Millisecond, LatencyBreachCount: 2, ConsecutiveRecoveries: 2}, []step{
		{check: up(150), event: EventNone, state: StateHealthy},
		{check: down(), event: EventTargetDown, state: StateDown},
		{check: up(150), event: EventNone, state: StateDown},
		{check: up(150), event: EventTargetRecovered, state: StateHealthy},
		{check: up(150), event: EventNone, state: StateHealthy},
		{check: up(150), event: EventTargetDegraded, state: StateDegraded},
	})
	for i := 1; i <= 3; i++ {
		if ds[i].LatencyBreaches != 0 {
			t.Errorf("step %d: latency breaches = %d, want 0", i, ds[i].LatencyBreaches)
		}
	}
}

func TestSSLExpiring(t *testing.T) {
	ssl := func(days int) Check {
		c := up(10)
		c.SSLDaysRemaining = days
		return c
	}
	ds := run(t, Policy{SSLExpiryThresholdDays: 14}, []step{
		{check: ssl(30), event: EventNone, state: StateHealthy},
		{check: ssl(14), event: EventSSLExpiring, state: StateHealthy},
		{check: ssl(13), event: EventNone, state: StateHealthy},
		{check: ssl(-1), event: EventNone, state: StateHealthy},
		{check: ssl(90), event: EventNone, state: StateHealthy},
		{check: ssl(5), event: EventSSLExpiring, state: StateHealthy},
	})
	if ds[1].SSLDaysRemaining != 14 || ds[3].SSLDaysRemaining != -1 {
		t.Errorf("ssl snapshots = %+v / %+v", ds[1], ds[3])
	}

	run(t, Policy{}, []step{
		{check: ssl(1), event: EventNone, state: StateHealthy},
	})
	run(t, Policy{SSLExpiryThresholdDays: 14}, []step{
		{check: ssl(-5), event: EventNone, state: StateHealthy},
	})
}

func TestCooldownSuppressesNonRecoveryEvents(t *testing.T) {
	policy := Policy{Cooldown: time.Minute, LatencyThreshold: 100 * time.Millisecond}
	run(t, policy, []step{
		{check: down(), offset: 0, event: EventTargetDown, state: StateDown},
		{check: up(10), offset: 10 * time.Second, event: EventTargetRecovered, state: StateHealthy},
		{check: up(500), offset: 20 * time.Second, event: EventTargetDegraded, state: StateDegraded, suppressed: true},
		{check: up(10), offset: 30 * time.Second, event: EventTargetHealthy, state: StateHealthy},
		{check: down(), offset: 50 * time.Second, event: EventTargetDown, state: StateDown, suppressed: true},
		{check: up(10), offset: 55 * time.Second, event: EventTargetRecovered, state: StateHealthy},
		{check: up(500), offset: 61 * time.Second, event: EventTargetDegraded, state: StateDegraded},
		{check: up(500), offset: 90 * time.Second, event: EventTargetDegraded, state: StateDegraded, suppressed: true},
		{check: up(500), offset: 121 * time.Second, event: EventTargetDegraded, state: StateDegraded},
	})
}

func TestSnapshotOnSilentChecks(t *testing.T) {
	tracker := NewTracker(Policy{ConsecutiveFailures: 3})
	tracker.Evaluate(down(), _base)
	d := tracker.Evaluate(down(), _base)
	if d.Event != EventNone || d.State != StateHealthy || d.PreviousState != StateHealthy || d.ConsecutiveFailures != 2 {
		t.Errorf("silent snapshot = %+v", d)
	}
}
