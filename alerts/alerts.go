package alerts

import "time"

// Event is an alert notification produced by a single check evaluation.
// The zero value is EventNone.
type Event string

const (
	EventNone            Event = ""
	EventTargetDown      Event = "target_down"
	EventTargetRecovered Event = "target_recovered"
	EventTargetDegraded  Event = "target_degraded"
	EventTargetHealthy   Event = "target_healthy"
	EventSSLExpiring     Event = "ssl_expiring"
)

// String returns the serialized event name.
func (e Event) String() string {
	return string(e)
}

// State is the alerting condition of a target.
type State string

const (
	StateHealthy  State = "healthy"
	StateDegraded State = "degraded"
	StateDown     State = "down"
)

// String returns the serialized state name.
func (s State) String() string {
	return string(s)
}

// Policy configures how a target moves between alert states.
// Zero values are normalized by NewTracker:
// consecutive failures and recoveries default to 1, latency alerting stays
// disabled unless LatencyThreshold is positive, and a non-positive
// LatencyBreachCount is treated as 1 when latency alerting is enabled.
type Policy struct {
	ConsecutiveFailures    int
	ConsecutiveRecoveries  int
	Cooldown               time.Duration
	LatencyThreshold       time.Duration
	LatencyBreachCount     int
	SSLExpiryThresholdDays int
}

// Check is one observation of a target.
// A negative SSLDaysRemaining means certificate lifetime is not applicable
// and never produces an SSL expiry alert.
type Check struct {
	IsUp             bool
	ResponseTime     time.Duration
	SSLDaysRemaining int
}

// Decision is the tracker snapshot after evaluating one check.
// Counters and states are reported even when Event is EventNone or the
// notification is suppressed.
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
