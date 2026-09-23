package alerts

import (
	"fmt"
	"time"
)

type Event string

const (
	EventNone            Event = ""
	EventTargetDown      Event = "target_down"
	EventTargetRecovered Event = "target_recovered"
	EventTargetDegraded  Event = "target_degraded"
	EventTargetHealthy   Event = "target_healthy"
	EventSSLExpiring     Event = "ssl_expiring"
)

type State string

const (
	StateHealthy  State = "healthy"
	StateDegraded State = "degraded"
	StateDown     State = "down"
)

type Policy struct {
	ConsecutiveFailures    int
	ConsecutiveRecoveries  int
	Cooldown               time.Duration
	LatencyThreshold       time.Duration
	LatencyBreachCount     int
	SSLExpiryThresholdDays int
}

type Check struct {
	IsUp         bool
	ResponseTime time.Duration
	// SSLDaysRemaining is negative when not applicable.
	SSLDaysRemaining int
}

type Decision struct {
	Event                 Event
	State                 State
	PreviousState         State
	Reason                string
	ConsecutiveFailures   int
	ConsecutiveRecoveries int
	LatencyBreaches       int
	SSLDaysRemaining      int
	Suppressed            bool
}

type Tracker struct {
	policy       Policy
	state        State
	failures     int
	recoveries   int
	breaches     int
	sslAlerted   bool
	lastNotified time.Time
	hasNotified  bool
}

func NewTracker(p Policy) *Tracker {
	if p.ConsecutiveFailures <= 0 {
		p.ConsecutiveFailures = 1
	}
	if p.ConsecutiveRecoveries <= 0 {
		p.ConsecutiveRecoveries = 1
	}
	if p.LatencyThreshold > 0 && p.LatencyBreachCount <= 0 {
		p.LatencyBreachCount = 1
	}
	return &Tracker{policy: p, state: StateHealthy}
}

func (t *Tracker) Evaluate(c Check, now time.Time) Decision {
	prev := t.state
	event := EventNone
	reason := ""

	if !c.IsUp {
		t.failures++
		t.recoveries = 0
		t.breaches = 0
		if t.state != StateDown && t.failures >= t.policy.ConsecutiveFailures {
			t.state = StateDown
			event = EventTargetDown
			reason = fmt.Sprintf("target failed %d consecutive checks", t.failures)
		}
	} else {
		t.failures = 0
		t.recoveries++
		if t.state == StateDown {
			if t.recoveries >= t.policy.ConsecutiveRecoveries {
				t.state = StateHealthy
				event = EventTargetRecovered
				reason = fmt.Sprintf("target succeeded %d consecutive checks", t.recoveries)
			}
		} else if t.policy.LatencyThreshold > 0 && c.ResponseTime > t.policy.LatencyThreshold {
			t.breaches++
			if t.breaches >= t.policy.LatencyBreachCount {
				t.state = StateDegraded
				event = EventTargetDegraded
				reason = fmt.Sprintf("response time %dms exceeded threshold %dms for %d consecutive checks",
					c.ResponseTime.Milliseconds(), t.policy.LatencyThreshold.Milliseconds(), t.breaches)
			}
		} else {
			t.breaches = 0
			if t.state == StateDegraded {
				t.state = StateHealthy
				event = EventTargetHealthy
				reason = fmt.Sprintf("response time %dms back below threshold %dms",
					c.ResponseTime.Milliseconds(), t.policy.LatencyThreshold.Milliseconds())
			}
		}
	}

	threshold := t.policy.SSLExpiryThresholdDays
	inSSLWindow := threshold > 0 && c.SSLDaysRemaining >= 0 && c.SSLDaysRemaining <= threshold
	if !inSSLWindow {
		t.sslAlerted = false
	} else if !t.sslAlerted && event == EventNone {
		t.sslAlerted = true
		event = EventSSLExpiring
		reason = fmt.Sprintf("SSL certificate expires in %d days (threshold %d)", c.SSLDaysRemaining, threshold)
	}

	suppressed := false
	if event != EventNone && event != EventTargetRecovered && event != EventTargetHealthy {
		if t.policy.Cooldown > 0 && t.hasNotified && now.Sub(t.lastNotified) < t.policy.Cooldown {
			suppressed = true
		} else {
			t.lastNotified = now
			t.hasNotified = true
		}
	}

	return Decision{
		Event:                 event,
		State:                 t.state,
		PreviousState:         prev,
		Reason:                reason,
		ConsecutiveFailures:   t.failures,
		ConsecutiveRecoveries: t.recoveries,
		LatencyBreaches:       t.breaches,
		SSLDaysRemaining:      c.SSLDaysRemaining,
		Suppressed:            suppressed,
	}
}
