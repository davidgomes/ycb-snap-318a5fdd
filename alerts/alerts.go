package alerts

import (
	"fmt"
	"time"
)

type Event string

type State string

const (
	EventNone            Event = ""
	EventTargetDown      Event = "target_down"
	EventTargetRecovered Event = "target_recovered"
	EventTargetDegraded  Event = "target_degraded"
	EventTargetHealthy   Event = "target_healthy"
	EventSSLExpiring     Event = "ssl_expiring"
)

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
	policy                Policy
	state                 State
	consecutiveFailures   int
	consecutiveRecoveries int
	latencyBreaches       int
	sslDaysRemaining      int
	sslAlerted            bool
	lastNonRecovery       time.Time
}

func NewTracker(policy Policy) *Tracker {
	return &Tracker{
		policy: normalizePolicy(policy),
		state:  StateHealthy,
	}
}

func (t *Tracker) Evaluate(check Check, now time.Time) Decision {
	previous := t.state
	t.sslDaysRemaining = check.SSLDaysRemaining

	event, reason := t.evaluateAvailability(check)
	if event == EventNone {
		if sslEvent, sslReason, ok := t.takeSSLEvent(check); ok {
			event = sslEvent
			reason = sslReason
		}
	} else {
		t.trackSSLWindow(check)
	}

	suppressed := false
	if event != EventNone && !isNeverSuppressed(event) {
		if t.policy.Cooldown > 0 && !t.lastNonRecovery.IsZero() && now.Sub(t.lastNonRecovery) < t.policy.Cooldown {
			suppressed = true
		} else {
			t.lastNonRecovery = now
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
		SSLDaysRemaining:      t.sslDaysRemaining,
		Suppressed:            suppressed,
	}
}

func (t *Tracker) evaluateAvailability(check Check) (Event, string) {
	if !check.IsUp {
		t.consecutiveFailures++
		t.consecutiveRecoveries = 0
		t.latencyBreaches = 0
		if t.state != StateDown && t.consecutiveFailures >= t.policy.ConsecutiveFailures {
			t.state = StateDown
			return EventTargetDown, fmt.Sprintf("target failed %d consecutive checks", t.consecutiveFailures)
		}
		return EventNone, ""
	}

	t.consecutiveFailures = 0
	t.consecutiveRecoveries++

	if t.state == StateDown {
		if t.consecutiveRecoveries >= t.policy.ConsecutiveRecoveries {
			t.state = StateHealthy
			t.latencyBreaches = 0
			return EventTargetRecovered, fmt.Sprintf("target recovered after %d consecutive successful checks", t.consecutiveRecoveries)
		}
		return EventNone, ""
	}

	if t.policy.LatencyThreshold <= 0 {
		return EventNone, ""
	}

	if check.ResponseTime > t.policy.LatencyThreshold {
		t.latencyBreaches++
		if t.latencyBreaches >= t.policy.LatencyBreachCount {
			t.state = StateDegraded
			return EventTargetDegraded, fmt.Sprintf("response time %dms exceeded %dms latency threshold", check.ResponseTime.Milliseconds(), t.policy.LatencyThreshold.Milliseconds())
		}
		return EventNone, ""
	}

	t.latencyBreaches = 0
	if t.state == StateDegraded {
		t.state = StateHealthy
		return EventTargetHealthy, fmt.Sprintf("response time %dms is within %dms latency threshold", check.ResponseTime.Milliseconds(), t.policy.LatencyThreshold.Milliseconds())
	}
	return EventNone, ""
}

func (t *Tracker) trackSSLWindow(check Check) {
	if check.SSLDaysRemaining >= 0 && t.policy.SSLExpiryThresholdDays > 0 && check.SSLDaysRemaining > t.policy.SSLExpiryThresholdDays {
		t.sslAlerted = false
	}
}

func (t *Tracker) takeSSLEvent(check Check) (Event, string, bool) {
	t.trackSSLWindow(check)
	if !t.sslEligible(check) || t.sslAlerted {
		return EventNone, "", false
	}
	t.sslAlerted = true
	return EventSSLExpiring, fmt.Sprintf("SSL certificate expires in %d days", check.SSLDaysRemaining), true
}

func (t *Tracker) sslEligible(check Check) bool {
	if check.SSLDaysRemaining < 0 || t.policy.SSLExpiryThresholdDays <= 0 {
		return false
	}
	return check.SSLDaysRemaining <= t.policy.SSLExpiryThresholdDays
}

func isNeverSuppressed(event Event) bool {
	return event == EventTargetRecovered || event == EventTargetHealthy
}

func normalizePolicy(policy Policy) Policy {
	if policy.ConsecutiveFailures <= 0 {
		policy.ConsecutiveFailures = 1
	}
	if policy.ConsecutiveRecoveries <= 0 {
		policy.ConsecutiveRecoveries = 1
	}
	if policy.LatencyThreshold > 0 && policy.LatencyBreachCount <= 0 {
		policy.LatencyBreachCount = 1
	}
	if policy.SSLExpiryThresholdDays < 0 {
		policy.SSLExpiryThresholdDays = 0
	}
	return policy
}
