package alerts

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventAndStateSerialization(t *testing.T) {
	events := map[Event]string{
		EventNone:            "",
		EventTargetDown:      "target_down",
		EventTargetRecovered: "target_recovered",
		EventTargetDegraded:  "target_degraded",
		EventTargetHealthy:   "target_healthy",
		EventSSLExpiring:     "ssl_expiring",
	}
	for event, want := range events {
		if event.String() != want {
			t.Fatalf("event %q string = %q", want, event.String())
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var decoded string
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded != want {
			t.Fatalf("event JSON = %q, want %q", decoded, want)
		}
	}

	states := map[State]string{
		StateHealthy:  "healthy",
		StateDegraded: "degraded",
		StateDown:     "down",
	}
	for state, want := range states {
		if state.String() != want {
			t.Fatalf("state string = %q, want %q", state.String(), want)
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		var decoded string
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded != want {
			t.Fatalf("state JSON = %q, want %q", decoded, want)
		}
	}
}

func TestDefaultsAlertOnFirstFailureAndRecovery(t *testing.T) {
	tracker := NewTracker(Policy{})
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	up := tracker.Evaluate(Check{IsUp: true, ResponseTime: 5 * time.Second, SSLDaysRemaining: -1}, now)
	if up.Event != EventNone || up.State != StateHealthy || up.Suppressed {
		t.Fatalf("healthy check = %+v", up)
	}
	if up.ConsecutiveRecoveries != 1 || up.ConsecutiveFailures != 0 || up.LatencyBreaches != 0 {
		t.Fatalf("counters = %+v", up)
	}
	if up.SSLDaysRemaining != -1 || up.PreviousState != StateHealthy {
		t.Fatalf("snapshot = %+v", up)
	}

	down := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now.Add(time.Second))
	if down.Event != EventTargetDown || down.State != StateDown || down.PreviousState != StateHealthy {
		t.Fatalf("down = %+v", down)
	}
	if down.Reason == "" || down.Suppressed {
		t.Fatalf("down reason/suppressed = %+v", down)
	}
	if down.ConsecutiveFailures != 1 || down.LatencyBreaches != 0 {
		t.Fatalf("down counters = %+v", down)
	}

	still := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: -1}, now.Add(2*time.Second))
	if still.Event != EventNone || still.State != StateDown || still.PreviousState != StateDown {
		t.Fatalf("still down = %+v", still)
	}
	if still.ConsecutiveFailures != 2 {
		t.Fatalf("failures = %d", still.ConsecutiveFailures)
	}

	recovered := tracker.Evaluate(Check{IsUp: true, ResponseTime: 5 * time.Second, SSLDaysRemaining: -1}, now.Add(3*time.Second))
	if recovered.Event != EventTargetRecovered || recovered.State != StateHealthy || recovered.PreviousState != StateDown {
		t.Fatalf("recovered = %+v", recovered)
	}
	if recovered.Reason == "" || recovered.Suppressed || recovered.LatencyBreaches != 0 {
		t.Fatalf("recovered snapshot = %+v", recovered)
	}
}

func TestConsecutiveFailuresAndRecoveries(t *testing.T) {
	tracker := NewTracker(Policy{ConsecutiveFailures: 3, ConsecutiveRecoveries: 2})
	now := time.Now()

	first := tracker.Evaluate(Check{IsUp: false}, now)
	second := tracker.Evaluate(Check{IsUp: false}, now)
	if first.Event != EventNone || second.Event != EventNone || second.State != StateHealthy {
		t.Fatalf("before threshold: %+v %+v", first, second)
	}
	if second.ConsecutiveFailures != 2 || second.ConsecutiveRecoveries != 0 {
		t.Fatalf("failure counters = %+v", second)
	}

	third := tracker.Evaluate(Check{IsUp: false}, now)
	if third.Event != EventTargetDown || third.State != StateDown || third.PreviousState != StateHealthy {
		t.Fatalf("third = %+v", third)
	}
	if third.Reason == "" {
		t.Fatal("missing down reason")
	}

	partial := tracker.Evaluate(Check{IsUp: true, ResponseTime: time.Second}, now)
	if partial.Event != EventNone || partial.State != StateDown || partial.ConsecutiveRecoveries != 1 {
		t.Fatalf("partial recovery = %+v", partial)
	}
	if partial.LatencyBreaches != 0 || partial.ConsecutiveFailures != 0 {
		t.Fatalf("partial counters = %+v", partial)
	}

	back := tracker.Evaluate(Check{IsUp: true}, now)
	if back.Event != EventTargetRecovered || back.State != StateHealthy || back.PreviousState != StateDown {
		t.Fatalf("recovery = %+v", back)
	}
	if back.Reason == "" || back.ConsecutiveRecoveries != 2 {
		t.Fatalf("recovery snapshot = %+v", back)
	}
}

func TestLatencyBreachAndRepeat(t *testing.T) {
	tracker := NewTracker(Policy{
		LatencyThreshold:   100 * time.Millisecond,
		LatencyBreachCount: 3,
	})
	now := time.Now()
	slow := Check{IsUp: true, ResponseTime: 150 * time.Millisecond, SSLDaysRemaining: -1}
	fast := Check{IsUp: true, ResponseTime: 99 * time.Millisecond, SSLDaysRemaining: -1}

	first := tracker.Evaluate(slow, now)
	second := tracker.Evaluate(slow, now)
	if first.Event != EventNone || second.Event != EventNone || second.State != StateHealthy {
		t.Fatalf("before breach: %+v %+v", first, second)
	}
	if second.LatencyBreaches != 2 {
		t.Fatalf("breaches = %d", second.LatencyBreaches)
	}

	third := tracker.Evaluate(slow, now)
	if third.Event != EventTargetDegraded || third.State != StateDegraded || third.PreviousState != StateHealthy {
		t.Fatalf("degraded = %+v", third)
	}
	if third.Reason == "" || third.LatencyBreaches != 3 {
		t.Fatalf("degraded snapshot = %+v", third)
	}

	again := tracker.Evaluate(slow, now)
	if again.Event != EventTargetDegraded || again.State != StateDegraded || again.Suppressed {
		t.Fatalf("repeat degraded = %+v", again)
	}
	if again.Reason == "" || again.LatencyBreaches != 4 {
		t.Fatalf("repeat snapshot = %+v", again)
	}

	atThreshold := tracker.Evaluate(Check{IsUp: true, ResponseTime: 100 * time.Millisecond}, now)
	if atThreshold.Event != EventNone || atThreshold.State != StateDegraded || atThreshold.LatencyBreaches != 0 {
		t.Fatalf("equal to threshold stays degraded = %+v", atThreshold)
	}

	healthy := tracker.Evaluate(fast, now)
	if healthy.Event != EventTargetHealthy || healthy.State != StateHealthy || healthy.PreviousState != StateDegraded {
		t.Fatalf("healthy = %+v", healthy)
	}
	if healthy.Reason == "" || healthy.Suppressed || healthy.LatencyBreaches != 0 {
		t.Fatalf("healthy snapshot = %+v", healthy)
	}
}

func TestLatencyDisabledUnlessThresholdPositive(t *testing.T) {
	tracker := NewTracker(Policy{LatencyBreachCount: 1})
	decision := tracker.Evaluate(Check{IsUp: true, ResponseTime: 10 * time.Second}, time.Now())
	if decision.Event != EventNone || decision.State != StateHealthy || decision.LatencyBreaches != 0 {
		t.Fatalf("latency disabled = %+v", decision)
	}
}

func TestLatencyBreachCountDefaultsToOne(t *testing.T) {
	tracker := NewTracker(Policy{LatencyThreshold: 50 * time.Millisecond, LatencyBreachCount: 0})
	decision := tracker.Evaluate(Check{IsUp: true, ResponseTime: 51 * time.Millisecond}, time.Now())
	if decision.Event != EventTargetDegraded || decision.State != StateDegraded || decision.LatencyBreaches != 1 {
		t.Fatalf("default breach count = %+v", decision)
	}
	if decision.Reason == "" {
		t.Fatal("missing degraded reason")
	}
}

func TestLatencyResetsOnFailureAndWhileDown(t *testing.T) {
	tracker := NewTracker(Policy{
		ConsecutiveFailures:   3,
		ConsecutiveRecoveries: 2,
		LatencyThreshold:      100 * time.Millisecond,
		LatencyBreachCount:    3,
	})
	now := time.Now()
	slow := Check{IsUp: true, ResponseTime: 250 * time.Millisecond}

	tracker.Evaluate(slow, now)
	built := tracker.Evaluate(slow, now)
	if built.LatencyBreaches != 2 || built.State != StateHealthy {
		t.Fatalf("built breaches = %+v", built)
	}

	failed := tracker.Evaluate(Check{IsUp: false}, now)
	if failed.Event != EventNone || failed.State != StateHealthy || failed.LatencyBreaches != 0 {
		t.Fatalf("failure reset = %+v", failed)
	}
	if failed.ConsecutiveFailures != 1 {
		t.Fatalf("failures = %d", failed.ConsecutiveFailures)
	}

	tracker.Evaluate(Check{IsUp: false}, now)
	down := tracker.Evaluate(Check{IsUp: false}, now)
	if down.Event != EventTargetDown || down.LatencyBreaches != 0 {
		t.Fatalf("down = %+v", down)
	}

	still := tracker.Evaluate(slow, now)
	if still.State != StateDown || still.Event != EventNone || still.LatencyBreaches != 0 {
		t.Fatalf("slow while down = %+v", still)
	}
	if still.ConsecutiveRecoveries != 1 {
		t.Fatalf("recoveries = %d", still.ConsecutiveRecoveries)
	}

	recovered := tracker.Evaluate(slow, now)
	if recovered.Event != EventTargetRecovered || recovered.State != StateHealthy || recovered.LatencyBreaches != 0 {
		t.Fatalf("restart = %+v", recovered)
	}

	again := tracker.Evaluate(slow, now)
	if again.Event != EventNone || again.State != StateHealthy || again.LatencyBreaches != 1 {
		t.Fatalf("count restarts = %+v", again)
	}
}

func TestDegradedStaysThroughSlowChecksAfterFailure(t *testing.T) {
	tracker := NewTracker(Policy{
		ConsecutiveFailures: 3,
		LatencyThreshold:    100 * time.Millisecond,
		LatencyBreachCount:  2,
	})
	now := time.Now()
	slow := Check{IsUp: true, ResponseTime: 200 * time.Millisecond}

	tracker.Evaluate(slow, now)
	entered := tracker.Evaluate(slow, now)
	if entered.State != StateDegraded {
		t.Fatalf("enter = %+v", entered)
	}

	failed := tracker.Evaluate(Check{IsUp: false, ResponseTime: time.Second}, now)
	if failed.Event != EventNone || failed.State != StateDegraded || failed.LatencyBreaches != 0 {
		t.Fatalf("failed while degraded = %+v", failed)
	}

	again := tracker.Evaluate(slow, now)
	if again.Event != EventTargetDegraded || again.State != StateDegraded || again.LatencyBreaches != 1 {
		t.Fatalf("still degraded = %+v", again)
	}
}

func TestSSLExpiryOnceUntilReentry(t *testing.T) {
	tracker := NewTracker(Policy{SSLExpiryThresholdDays: 14})
	now := time.Now()

	disabled := NewTracker(Policy{})
	if decision := disabled.Evaluate(Check{IsUp: true, SSLDaysRemaining: 1}, now); decision.Event != EventNone {
		t.Fatalf("ssl disabled = %+v", decision)
	}

	negative := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -1}, now)
	if negative.Event != EventNone || negative.State != StateHealthy || negative.SSLDaysRemaining != -1 {
		t.Fatalf("negative ssl = %+v", negative)
	}

	first := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 14}, now)
	if first.Event != EventSSLExpiring || first.State != StateHealthy || first.PreviousState != StateHealthy {
		t.Fatalf("ssl = %+v", first)
	}
	if first.Reason == "" || first.SSLDaysRemaining != 14 {
		t.Fatalf("ssl snapshot = %+v", first)
	}

	again := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 7}, now)
	if again.Event != EventNone || again.State != StateHealthy {
		t.Fatalf("repeat ssl = %+v", again)
	}

	above := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 15}, now)
	if above.Event != EventNone {
		t.Fatalf("above threshold = %+v", above)
	}

	reenter := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 10}, now)
	if reenter.Event != EventSSLExpiring || reenter.State != StateHealthy || reenter.Reason == "" {
		t.Fatalf("reenter = %+v", reenter)
	}

	notApplicable := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: -5}, now)
	if notApplicable.Event != EventNone || notApplicable.State != StateHealthy {
		t.Fatalf("not applicable = %+v", notApplicable)
	}
	back := tracker.Evaluate(Check{IsUp: true, SSLDaysRemaining: 3}, now)
	if back.Event != EventNone {
		t.Fatalf("negative does not re-arm = %+v", back)
	}
}

func TestSSLDoesNotOverrideStateEvent(t *testing.T) {
	tracker := NewTracker(Policy{SSLExpiryThresholdDays: 30})
	down := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 5}, time.Now())
	if down.Event != EventTargetDown || down.State != StateDown {
		t.Fatalf("down wins = %+v", down)
	}

	ssl := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 5}, time.Now())
	if ssl.Event != EventSSLExpiring || ssl.State != StateDown || ssl.PreviousState != StateDown {
		t.Fatalf("ssl keeps down = %+v", ssl)
	}
	if ssl.Reason == "" {
		t.Fatal("missing ssl reason")
	}
}

func TestCooldownSuppressesNonRecoveryEvents(t *testing.T) {
	tracker := NewTracker(Policy{
		Cooldown:               10 * time.Second,
		LatencyThreshold:       100 * time.Millisecond,
		LatencyBreachCount:     1,
		SSLExpiryThresholdDays: 30,
	})
	start := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	down := tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 90}, start)
	if down.Event != EventTargetDown || down.Suppressed || down.State != StateDown {
		t.Fatalf("down = %+v", down)
	}

	recovered := tracker.Evaluate(Check{IsUp: true, ResponseTime: 10 * time.Millisecond, SSLDaysRemaining: 90}, start.Add(1*time.Second))
	if recovered.Event != EventTargetRecovered || recovered.Suppressed || recovered.State != StateHealthy {
		t.Fatalf("recovered = %+v", recovered)
	}

	degraded := tracker.Evaluate(Check{IsUp: true, ResponseTime: 250 * time.Millisecond, SSLDaysRemaining: 90}, start.Add(2*time.Second))
	if degraded.Event != EventTargetDegraded || !degraded.Suppressed || degraded.State != StateDegraded || degraded.PreviousState != StateHealthy {
		t.Fatalf("suppressed degraded = %+v", degraded)
	}
	if degraded.Reason == "" || degraded.LatencyBreaches != 1 {
		t.Fatalf("suppressed snapshot = %+v", degraded)
	}

	healthy := tracker.Evaluate(Check{IsUp: true, ResponseTime: 10 * time.Millisecond, SSLDaysRemaining: 90}, start.Add(3*time.Second))
	if healthy.Event != EventTargetHealthy || healthy.Suppressed || healthy.State != StateHealthy {
		t.Fatalf("healthy = %+v", healthy)
	}

	again := tracker.Evaluate(Check{IsUp: true, ResponseTime: 250 * time.Millisecond, SSLDaysRemaining: 90}, start.Add(4*time.Second))
	if again.Event != EventTargetDegraded || !again.Suppressed {
		t.Fatalf("still in window from original down = %+v", again)
	}

	cleared := tracker.Evaluate(Check{IsUp: true, ResponseTime: 10 * time.Millisecond, SSLDaysRemaining: 90}, start.Add(5*time.Second))
	if cleared.Event != EventTargetHealthy || cleared.Suppressed || cleared.State != StateHealthy {
		t.Fatalf("healthy during cooldown = %+v", cleared)
	}

	ssl := tracker.Evaluate(Check{IsUp: true, ResponseTime: 10 * time.Millisecond, SSLDaysRemaining: 5}, start.Add(6*time.Second))
	if ssl.Event != EventSSLExpiring || !ssl.Suppressed || ssl.State != StateHealthy {
		t.Fatalf("suppressed ssl = %+v", ssl)
	}
	if ssl.Reason == "" || ssl.SSLDaysRemaining != 5 {
		t.Fatalf("suppressed ssl snapshot = %+v", ssl)
	}

	later := tracker.Evaluate(Check{IsUp: true, ResponseTime: 250 * time.Millisecond, SSLDaysRemaining: 40}, start.Add(10*time.Second))
	if later.Event != EventTargetDegraded || later.Suppressed {
		t.Fatalf("cooldown elapsed = %+v", later)
	}

	within := tracker.Evaluate(Check{IsUp: true, ResponseTime: 250 * time.Millisecond, SSLDaysRemaining: 40}, start.Add(19*time.Second))
	if within.Event != EventTargetDegraded || !within.Suppressed {
		t.Fatalf("window measured from delivered event = %+v", within)
	}

	delivered := tracker.Evaluate(Check{IsUp: true, ResponseTime: 250 * time.Millisecond, SSLDaysRemaining: 40}, start.Add(20*time.Second))
	if delivered.Event != EventTargetDegraded || delivered.Suppressed {
		t.Fatalf("next delivery = %+v", delivered)
	}
}

func TestSnapshotMatchesWhenNoEvent(t *testing.T) {
	tracker := NewTracker(Policy{
		ConsecutiveFailures:    4,
		LatencyThreshold:       100 * time.Millisecond,
		LatencyBreachCount:     4,
		SSLExpiryThresholdDays: 10,
	})
	now := time.Now()
	tracker.Evaluate(Check{IsUp: false, SSLDaysRemaining: 40}, now)
	tracker.Evaluate(Check{IsUp: true, ResponseTime: 150 * time.Millisecond, SSLDaysRemaining: 40}, now)

	decision := tracker.Evaluate(Check{IsUp: true, ResponseTime: 180 * time.Millisecond, SSLDaysRemaining: 21}, now)
	if decision.Event != EventNone || decision.Suppressed {
		t.Fatalf("no event = %+v", decision)
	}
	if decision.State != StateHealthy || decision.PreviousState != StateHealthy {
		t.Fatalf("state = %+v", decision)
	}
	if decision.ConsecutiveFailures != 0 || decision.ConsecutiveRecoveries != 2 || decision.LatencyBreaches != 2 {
		t.Fatalf("counters = %+v", decision)
	}
	if decision.SSLDaysRemaining != 21 {
		t.Fatalf("ssl days = %d", decision.SSLDaysRemaining)
	}
}
