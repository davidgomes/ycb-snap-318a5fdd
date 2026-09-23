// Package alerts evaluates per-target alert policy.
//
// A tracker turns check results into a state (healthy, degraded, down) and at
// most one alert event. Cooldown suppresses delivery of non-recovery events; it
// does not change the decision itself.
package alerts

import (
	"fmt"
	"time"
)

// Event is an alert emitted by a single evaluation.
type Event string

const (
	EventNone            Event = ""
	EventTargetDown      Event = "target_down"
	EventTargetRecovered Event = "target_recovered"
	EventTargetDegraded  Event = "target_degraded"
	EventTargetHealthy   Event = "target_healthy"
	EventSSLExpiring     Event = "ssl_expiring"
)

func (e Event) String() string { return string(e) }

// State is the target condition after an evaluation.
type State string

const (
	StateHealthy  State = "healthy"
	StateDegraded State = "degraded"
	StateDown     State = "down"
)

func (s State) String() string { return string(s) }

// Policy configures when a target alerts.
type Policy struct {
	ConsecutiveFailures    int
	ConsecutiveRecoveries  int
	Cooldown               time.Duration
	LatencyThreshold       time.Duration
	LatencyBreachCount     int
	SSLExpiryThresholdDays int
}

// Check is one probe result.
type Check struct {
	IsUp             bool
	ResponseTime     time.Duration
	SSLDaysRemaining int
}

// Decision is the outcome of evaluating one check.
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

// Tracker holds alert state for a single target.
type Tracker struct {
	policy                Policy
	state                 State
	consecutiveFailures   int
	consecutiveRecoveries int
	latencyBreaches       int
	sslDays               int
	sslAlerted            bool
	cooldownAnchor        time.Time
	cooldownAnchorSet     bool
}

// NewTracker returns a tracker starting in the healthy state.
// Zero consecutive-failure and consecutive-recovery counts default to 1.
// Latency alerting is disabled unless LatencyThreshold is positive; a
// non-positive LatencyBreachCount is then treated as 1. SSL expiry alerting
// is disabled unless SSLExpiryThresholdDays is positive.
func NewTracker(policy Policy) *Tracker {
	return &Tracker{
		policy: normalizePolicy(policy),
		state:  StateHealthy,
	}
}

func normalizePolicy(policy Policy) Policy {
	if policy.ConsecutiveFailures <= 0 {
		policy.ConsecutiveFailures = 1
	}
	if policy.ConsecutiveRecoveries <= 0 {
		policy.ConsecutiveRecoveries = 1
	}
	if policy.Cooldown < 0 {
		policy.Cooldown = 0
	}
	if policy.LatencyThreshold <= 0 {
		policy.LatencyThreshold = 0
	} else if policy.LatencyBreachCount <= 0 {
		policy.LatencyBreachCount = 1
	}
	if policy.SSLExpiryThresholdDays < 0 {
		policy.SSLExpiryThresholdDays = 0
	}
	return policy
}

// Evaluate applies check to the tracker and returns the resulting snapshot.
// now is the time used for cooldown. Recovery and healthy events are never
// suppressed. Other events are suppressed when they fall inside the cooldown
// window measured from the last non-suppressed non-recovery event.
func (t *Tracker) Evaluate(check Check, now time.Time) Decision {
	previous := t.state

	var event Event
	var reason string
	if check.IsUp {
		event, reason = t.evaluateUp(check)
	} else {
		event, reason = t.evaluateDown()
	}
	event, reason = t.evaluateSSL(check, event, reason)
	suppressed := t.applyCooldown(event, now)

	return Decision{
		Event:                 event,
		State:                 t.state,
		PreviousState:         previous,
		Reason:                reason,
		ConsecutiveFailures:   t.consecutiveFailures,
		ConsecutiveRecoveries: t.consecutiveRecoveries,
		LatencyBreaches:       t.latencyBreaches,
		SSLDaysRemaining:      t.sslDays,
		Suppressed:            suppressed,
	}
}

func (t *Tracker) evaluateDown() (Event, string) {
	t.consecutiveFailures++
	t.consecutiveRecoveries = 0
	t.latencyBreaches = 0

	if t.state != StateDown && t.consecutiveFailures >= t.policy.ConsecutiveFailures {
		t.state = StateDown
		return EventTargetDown, fmt.Sprintf("target is down after %d consecutive failed checks", t.consecutiveFailures)
	}
	return EventNone, ""
}

func (t *Tracker) evaluateUp(check Check) (Event, string) {
	t.consecutiveFailures = 0
	t.consecutiveRecoveries++

	event := EventNone
	reason := ""
	if t.state == StateDown && t.consecutiveRecoveries >= t.policy.ConsecutiveRecoveries {
		t.state = StateHealthy
		event = EventTargetRecovered
		reason = fmt.Sprintf("target recovered after %d consecutive successful checks", t.consecutiveRecoveries)
	}

	// Breach counting stays reset until the target is up again.
	if t.state == StateDown || t.policy.LatencyThreshold <= 0 {
		return event, reason
	}

	if check.ResponseTime > t.policy.LatencyThreshold {
		t.latencyBreaches++
		if t.state == StateDegraded || t.latencyBreaches >= t.policy.LatencyBreachCount {
			t.state = StateDegraded
			return EventTargetDegraded, fmt.Sprintf(
				"response time %dms exceeded latency threshold %dms for %d consecutive checks",
				check.ResponseTime.Milliseconds(),
				t.policy.LatencyThreshold.Milliseconds(),
				t.latencyBreaches,
			)
		}
		return event, reason
	}

	// Equal to the threshold is not a breach and is not below it, so a
	// degraded target stays degraded until a strictly faster check.
	t.latencyBreaches = 0
	if check.ResponseTime < t.policy.LatencyThreshold && t.state == StateDegraded {
		t.state = StateHealthy
		return EventTargetHealthy, fmt.Sprintf(
			"response time %dms is below latency threshold %dms",
			check.ResponseTime.Milliseconds(),
			t.policy.LatencyThreshold.Milliseconds(),
		)
	}
	return event, reason
}

func (t *Tracker) evaluateSSL(check Check, event Event, reason string) (Event, string) {
	t.sslDays = check.SSLDaysRemaining
	if t.policy.SSLExpiryThresholdDays <= 0 || check.SSLDaysRemaining < 0 {
		return event, reason
	}
	if check.SSLDaysRemaining > t.policy.SSLExpiryThresholdDays {
		t.sslAlerted = false
		return event, reason
	}
	// Already inside the window, or a higher-priority event owns this check.
	if t.sslAlerted || event != EventNone {
		return event, reason
	}
	t.sslAlerted = true
	return EventSSLExpiring, fmt.Sprintf(
		"SSL certificate expires in %d days (threshold %d days)",
		check.SSLDaysRemaining,
		t.policy.SSLExpiryThresholdDays,
	)
}

func (t *Tracker) applyCooldown(event Event, now time.Time) bool {
	if event == EventNone || event == EventTargetRecovered || event == EventTargetHealthy {
		return false
	}
	if t.policy.Cooldown > 0 && t.cooldownAnchorSet && now.Sub(t.cooldownAnchor) < t.policy.Cooldown {
		return true
	}
	t.cooldownAnchor = now
	t.cooldownAnchorSet = true
	return false
}
