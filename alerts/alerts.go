package alerts

import (
	"fmt"
	"sync"
	"time"
)

// Event and State are aliases of string so they can be assigned to and
// compared with the plain string fields of notification payloads.
type (
	Event = string
	State = string
)

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

// Check is a single probe result. A negative SSLDaysRemaining means SSL
// expiry does not apply to the check.
type Check struct {
	IsUp             bool
	ResponseTime     time.Duration
	SSLDaysRemaining int
}

// Decision is the tracker snapshot after evaluating a check. Suppressed
// events are still reported so callers can observe state changes, but
// they must not be delivered.
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
	mu     sync.Mutex
	policy Policy

	state           State
	previousState   State
	failures        int
	recoveries      int
	latencyBreaches int
	sslDays         int
	sslAlerted      bool

	lastNotified time.Time
	hasNotified  bool
}

func NewTracker(policy Policy) *Tracker {
	return &Tracker{
		policy:        normalize(policy),
		state:         StateHealthy,
		previousState: StateHealthy,
		sslDays:       -1,
	}
}

func normalize(p Policy) Policy {
	if p.ConsecutiveFailures <= 0 {
		p.ConsecutiveFailures = 1
	}
	if p.ConsecutiveRecoveries <= 0 {
		p.ConsecutiveRecoveries = 1
	}
	if p.Cooldown < 0 {
		p.Cooldown = 0
	}
	if p.LatencyThreshold > 0 && p.LatencyBreachCount <= 0 {
		p.LatencyBreachCount = 1
	}
	return p
}

func (t *Tracker) Evaluate(check Check, now time.Time) Decision {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.sslDays = check.SSLDaysRemaining

	event, reason := t.evaluateHealth(check)
	sslEvent, sslReason := t.evaluateSSL(check.SSLDaysRemaining, event == EventNone)
	if sslEvent != EventNone {
		event, reason = sslEvent, sslReason
	}

	decision := Decision{
		Event:                 event,
		State:                 t.state,
		PreviousState:         t.previousState,
		Reason:                reason,
		ConsecutiveFailures:   t.failures,
		ConsecutiveRecoveries: t.recoveries,
		LatencyBreaches:       t.latencyBreaches,
		SSLDaysRemaining:      t.sslDays,
	}

	if isCooldownEligible(event) {
		if t.hasNotified && now.Sub(t.lastNotified) < t.policy.Cooldown {
			decision.Suppressed = true
		} else {
			t.lastNotified = now
			t.hasNotified = true
		}
	}

	return decision
}

func (t *Tracker) evaluateHealth(check Check) (Event, string) {
	if !check.IsUp {
		t.failures++
		t.recoveries = 0
		t.latencyBreaches = 0
		if t.state == StateDown || t.failures < t.policy.ConsecutiveFailures {
			return EventNone, ""
		}
		t.transition(StateDown)
		return EventTargetDown, fmt.Sprintf("%d consecutive failed checks", t.failures)
	}

	t.failures = 0
	t.recoveries++

	if t.state == StateDown {
		if t.recoveries < t.policy.ConsecutiveRecoveries {
			return EventNone, ""
		}
		t.transition(StateHealthy)
		return EventTargetRecovered, fmt.Sprintf("%d consecutive successful checks", t.recoveries)
	}

	threshold := t.policy.LatencyThreshold
	if threshold <= 0 {
		return EventNone, ""
	}

	if check.ResponseTime > threshold {
		t.latencyBreaches++
		if t.state != StateDegraded && t.latencyBreaches < t.policy.LatencyBreachCount {
			return EventNone, ""
		}
		if t.state != StateDegraded {
			t.transition(StateDegraded)
		}
		return EventTargetDegraded, fmt.Sprintf("response time %dms exceeded latency threshold %dms (%d consecutive slow checks)",
			check.ResponseTime.Milliseconds(), threshold.Milliseconds(), t.latencyBreaches)
	}

	t.latencyBreaches = 0
	if t.state != StateDegraded {
		return EventNone, ""
	}
	t.transition(StateHealthy)
	return EventTargetHealthy, fmt.Sprintf("response time %dms is back below latency threshold %dms",
		check.ResponseTime.Milliseconds(), threshold.Milliseconds())
}

// evaluateSSL only emits when no health event was produced for the same
// check; a pending expiry alert is emitted on the next quiet check.
func (t *Tracker) evaluateSSL(days int, canEmit bool) (Event, string) {
	threshold := t.policy.SSLExpiryThresholdDays
	if threshold <= 0 || days < 0 {
		return EventNone, ""
	}
	if days > threshold {
		t.sslAlerted = false
		return EventNone, ""
	}
	if t.sslAlerted || !canEmit {
		return EventNone, ""
	}
	t.sslAlerted = true
	return EventSSLExpiring, fmt.Sprintf("SSL certificate expires in %d days (threshold %d days)", days, threshold)
}

func (t *Tracker) transition(next State) {
	t.previousState = t.state
	t.state = next
}

func isCooldownEligible(event Event) bool {
	switch event {
	case EventTargetDown, EventTargetDegraded, EventSSLExpiring:
		return true
	default:
		return false
	}
}
