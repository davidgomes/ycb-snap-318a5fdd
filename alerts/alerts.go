package alerts

import (
	"fmt"
	"sync"
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

func (e Event) String() string {
	return string(e)
}

// IsRecovery reports whether the event signals a return to normal. Recovery
// events are never suppressed by the cooldown.
func (e Event) IsRecovery() bool {
	return e == EventTargetRecovered || e == EventTargetHealthy
}

type State string

const (
	StateHealthy  State = "healthy"
	StateDegraded State = "degraded"
	StateDown     State = "down"
)

func (s State) String() string {
	return string(s)
}

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
	// SSLDaysRemaining is negative when no certificate lifetime is available.
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
	mu     sync.Mutex
	policy Policy

	state                 State
	consecutiveFailures   int
	consecutiveRecoveries int
	latencyBreaches       int
	sslDaysRemaining      int
	sslAlerted            bool

	lastNotified time.Time
	hasNotified  bool
}

func NewTracker(policy Policy) *Tracker {
	return &Tracker{
		policy:           normalizePolicy(policy),
		state:            StateHealthy,
		sslDaysRemaining: -1,
	}
}

func normalizePolicy(p Policy) Policy {
	if p.ConsecutiveFailures <= 0 {
		p.ConsecutiveFailures = 1
	}
	if p.ConsecutiveRecoveries <= 0 {
		p.ConsecutiveRecoveries = 1
	}
	if p.Cooldown < 0 {
		p.Cooldown = 0
	}
	if p.LatencyThreshold <= 0 {
		p.LatencyThreshold = 0
		p.LatencyBreachCount = 0
	} else if p.LatencyBreachCount <= 0 {
		p.LatencyBreachCount = 1
	}
	if p.SSLExpiryThresholdDays < 0 {
		p.SSLExpiryThresholdDays = 0
	}
	return p
}

func (t *Tracker) Evaluate(check Check, now time.Time) Decision {
	t.mu.Lock()
	defer t.mu.Unlock()

	previous := t.state
	t.sslDaysRemaining = check.SSLDaysRemaining

	var event Event
	var reason string
	if check.IsUp {
		event, reason = t.observeSuccess(check.ResponseTime)
	} else {
		event, reason = t.observeFailure()
	}

	// A single decision carries one event, so an SSL alert that coincides with
	// an availability or latency event is deferred to the next evaluation.
	if sslEvent, sslReason := t.observeSSL(check.SSLDaysRemaining, event == EventNone); sslEvent != EventNone {
		event, reason = sslEvent, sslReason
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
		Suppressed:            t.suppress(event, now),
	}
}

func (t *Tracker) observeFailure() (Event, string) {
	t.consecutiveFailures++
	t.consecutiveRecoveries = 0
	t.latencyBreaches = 0

	if t.state == StateDown || t.consecutiveFailures < t.policy.ConsecutiveFailures {
		return EventNone, ""
	}

	t.state = StateDown
	return EventTargetDown, fmt.Sprintf("failed %s in a row", pluralize(t.consecutiveFailures, "check"))
}

func (t *Tracker) observeSuccess(responseTime time.Duration) (Event, string) {
	t.consecutiveFailures = 0
	t.consecutiveRecoveries++

	if t.state == StateDown {
		if t.consecutiveRecoveries < t.policy.ConsecutiveRecoveries {
			return EventNone, ""
		}
		t.state = StateHealthy
		return EventTargetRecovered, fmt.Sprintf("succeeded %s in a row", pluralize(t.consecutiveRecoveries, "check"))
	}

	if t.policy.LatencyThreshold <= 0 {
		return EventNone, ""
	}

	if responseTime <= t.policy.LatencyThreshold {
		t.latencyBreaches = 0
		if t.state != StateDegraded {
			return EventNone, ""
		}
		t.state = StateHealthy
		return EventTargetHealthy, fmt.Sprintf("response time %dms is within latency threshold %dms",
			responseTime.Milliseconds(), t.policy.LatencyThreshold.Milliseconds())
	}

	t.latencyBreaches++
	if t.state != StateDegraded && t.latencyBreaches < t.policy.LatencyBreachCount {
		return EventNone, ""
	}

	t.state = StateDegraded
	return EventTargetDegraded, fmt.Sprintf("response time %dms exceeded latency threshold %dms for %s in a row",
		responseTime.Milliseconds(), t.policy.LatencyThreshold.Milliseconds(), pluralize(t.latencyBreaches, "check"))
}

func (t *Tracker) observeSSL(daysRemaining int, canEmit bool) (Event, string) {
	if t.policy.SSLExpiryThresholdDays <= 0 || daysRemaining < 0 {
		return EventNone, ""
	}

	if daysRemaining > t.policy.SSLExpiryThresholdDays {
		t.sslAlerted = false
		return EventNone, ""
	}

	if t.sslAlerted || !canEmit {
		return EventNone, ""
	}

	t.sslAlerted = true
	return EventSSLExpiring, fmt.Sprintf("SSL certificate expires in %s (threshold %s)",
		pluralize(daysRemaining, "day"), pluralize(t.policy.SSLExpiryThresholdDays, "day"))
}

func (t *Tracker) suppress(event Event, now time.Time) bool {
	if event == EventNone || event.IsRecovery() {
		return false
	}

	if t.policy.Cooldown > 0 && t.hasNotified && now.Sub(t.lastNotified) < t.policy.Cooldown {
		return true
	}

	t.lastNotified = now
	t.hasNotified = true
	return false
}

func pluralize(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
