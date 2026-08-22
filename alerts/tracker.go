package alerts

import (
	"fmt"
	"time"
)

type Tracker struct {
	policy          Policy
	state           State
	failures        int
	recoveries      int
	latencyBreaches int
	sslAlerted      bool
	lastAlert       time.Time
}

func NewTracker(policy Policy) *Tracker {
	if policy.ConsecutiveFailures <= 0 {
		policy.ConsecutiveFailures = 1
	}
	if policy.ConsecutiveRecoveries <= 0 {
		policy.ConsecutiveRecoveries = 1
	}
	if policy.LatencyThreshold > 0 && policy.LatencyBreachCount <= 0 {
		policy.LatencyBreachCount = 1
	}
	return &Tracker{
		policy: policy,
		state:  StateHealthy,
	}
}

func (t *Tracker) latencyEnabled() bool {
	return t.policy.LatencyThreshold > 0
}

func (t *Tracker) sslEnabled() bool {
	return t.policy.SSLExpiryThresholdDays > 0
}

func (t *Tracker) Evaluate(check Check, now time.Time) Decision {
	previous := t.state
	event := EventNone
	reason := ""

	if !check.IsUp {
		t.failures++
		t.recoveries = 0
		t.latencyBreaches = 0
		if t.state != StateDown && t.failures >= t.policy.ConsecutiveFailures {
			t.state = StateDown
			event = EventTargetDown
			reason = fmt.Sprintf("target failed %d consecutive check(s)", t.failures)
		}
	} else {
		t.failures = 0
		t.recoveries++

		if t.state == StateDown {
			if t.recoveries >= t.policy.ConsecutiveRecoveries {
				t.state = StateHealthy
				event = EventTargetRecovered
				reason = fmt.Sprintf("target recovered after %d consecutive successful check(s)", t.recoveries)
				t.latencyBreaches = 0
				if t.latencyEnabled() && check.ResponseTime > t.policy.LatencyThreshold {
					t.latencyBreaches = 1
				}
			}
		} else if t.latencyEnabled() {
			if check.ResponseTime > t.policy.LatencyThreshold {
				t.latencyBreaches++
				if t.latencyBreaches >= t.policy.LatencyBreachCount {
					t.state = StateDegraded
					event = EventTargetDegraded
					reason = fmt.Sprintf("latency %dms exceeded threshold %dms for %d consecutive check(s)",
						check.ResponseTime.Milliseconds(), t.policy.LatencyThreshold.Milliseconds(), t.latencyBreaches)
				}
			} else {
				t.latencyBreaches = 0
				if t.state == StateDegraded {
					t.state = StateHealthy
					event = EventTargetHealthy
					reason = "latency returned below threshold"
				}
			}
		} else {
			t.latencyBreaches = 0
		}
	}

	if t.sslEnabled() {
		if check.SSLDaysRemaining < 0 {
			t.sslAlerted = false
		} else if check.SSLDaysRemaining <= t.policy.SSLExpiryThresholdDays {
			if !t.sslAlerted && event == EventNone {
				event = EventSSLExpiring
				reason = fmt.Sprintf("SSL certificate expires in %d day(s)", check.SSLDaysRemaining)
				t.sslAlerted = true
			}
		} else {
			t.sslAlerted = false
		}
	}

	suppressed := false
	if event != EventNone && event != EventTargetRecovered && event != EventTargetHealthy {
		if t.policy.Cooldown > 0 && !t.lastAlert.IsZero() && now.Sub(t.lastAlert) < t.policy.Cooldown {
			suppressed = true
		} else {
			t.lastAlert = now
		}
	}

	return Decision{
		Event:                 event,
		State:                 t.state,
		PreviousState:         previous,
		Reason:                reason,
		ConsecutiveFailures:   t.failures,
		ConsecutiveRecoveries: t.recoveries,
		LatencyBreaches:       t.latencyBreaches,
		SSLDaysRemaining:      check.SSLDaysRemaining,
		Suppressed:            suppressed,
	}
}
