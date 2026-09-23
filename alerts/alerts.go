package alerts

import (
	"fmt"
	"time"
)

type Event = string

const (
	EventNone            Event = ""
	EventTargetDown      Event = "target_down"
	EventTargetRecovered Event = "target_recovered"
	EventTargetDegraded  Event = "target_degraded"
	EventTargetHealthy   Event = "target_healthy"
	EventSSLExpiring     Event = "ssl_expiring"
)

type State = string

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

// Check is a single observation. A negative SSLDaysRemaining means SSL expiry
// is not applicable to this check.
type Check struct {
	IsUp             bool
	ResponseTime     time.Duration
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
	policy Policy

	state                 State
	consecutiveFailures   int
	consecutiveRecoveries int
	latencyBreaches       int
	sslAlerted            bool

	lastNotified time.Time
	hasNotified  bool
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

func (t *Tracker) Evaluate(check Check, now time.Time) Decision {
	previous := t.state
	event := EventNone
	reason := ""

	if !check.IsUp {
		t.consecutiveFailures++
		t.consecutiveRecoveries = 0
		t.latencyBreaches = 0
		if t.state != StateDown && t.consecutiveFailures >= t.policy.ConsecutiveFailures {
			t.state = StateDown
			event = EventTargetDown
			reason = fmt.Sprintf("%d consecutive failed check(s)", t.consecutiveFailures)
		}
	} else {
		t.consecutiveFailures = 0
		t.consecutiveRecoveries++
		if t.state == StateDown {
			if t.consecutiveRecoveries >= t.policy.ConsecutiveRecoveries {
				t.state = StateHealthy
				event = EventTargetRecovered
				reason = fmt.Sprintf("%d consecutive successful check(s)", t.consecutiveRecoveries)
			}
		} else if t.policy.LatencyThreshold > 0 {
			event, reason = t.evaluateLatency(check.ResponseTime)
		}
	}

	if event == EventNone {
		event, reason = t.evaluateSSL(check.SSLDaysRemaining)
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
		PreviousState:         previous,
		Reason:                reason,
		ConsecutiveFailures:   t.consecutiveFailures,
		ConsecutiveRecoveries: t.consecutiveRecoveries,
		LatencyBreaches:       t.latencyBreaches,
		SSLDaysRemaining:      check.SSLDaysRemaining,
		Suppressed:            suppressed,
	}
}

func (t *Tracker) evaluateLatency(responseTime time.Duration) (Event, string) {
	threshold := t.policy.LatencyThreshold
	if responseTime > threshold {
		t.latencyBreaches++
		if t.latencyBreaches >= t.policy.LatencyBreachCount {
			t.state = StateDegraded
			return EventTargetDegraded, fmt.Sprintf("response time %dms exceeded %dms threshold for %d consecutive check(s)",
				responseTime.Milliseconds(), threshold.Milliseconds(), t.latencyBreaches)
		}
		return EventNone, ""
	}

	t.latencyBreaches = 0
	if t.state == StateDegraded {
		t.state = StateHealthy
		return EventTargetHealthy, fmt.Sprintf("response time %dms is within %dms threshold",
			responseTime.Milliseconds(), threshold.Milliseconds())
	}
	return EventNone, ""
}

// evaluateSSL runs only when no other event was emitted for the check, so an
// expiring certificate that coincides with another event is reported on the
// next check instead of being lost.
func (t *Tracker) evaluateSSL(daysRemaining int) (Event, string) {
	threshold := t.policy.SSLExpiryThresholdDays
	if threshold <= 0 || daysRemaining < 0 {
		return EventNone, ""
	}
	if daysRemaining > threshold {
		t.sslAlerted = false
		return EventNone, ""
	}
	if t.sslAlerted {
		return EventNone, ""
	}
	t.sslAlerted = true
	return EventSSLExpiring, fmt.Sprintf("SSL certificate expires in %d day(s), threshold is %d day(s)", daysRemaining, threshold)
}
