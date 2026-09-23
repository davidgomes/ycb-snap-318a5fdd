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
	policy           Policy
	state            State
	consecFailures   int
	consecRecoveries int
	latencyBreaches  int
	sslDays          int
	sslAlerted       bool
	lastNonRecovery  time.Time
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
	t.sslDays = check.SSLDaysRemaining

	event := EventNone
	reason := ""

	if !check.IsUp {
		t.consecFailures++
		t.consecRecoveries = 0
		t.latencyBreaches = 0
		if t.state != StateDown && t.consecFailures >= t.policy.ConsecutiveFailures {
			t.state = StateDown
			event = EventTargetDown
			reason = fmt.Sprintf("target failed %d consecutive checks", t.consecFailures)
		}
	} else {
		t.consecRecoveries++
		t.consecFailures = 0
		if t.state == StateDown {
			if t.consecRecoveries >= t.policy.ConsecutiveRecoveries {
				t.state = StateHealthy
				event = EventTargetRecovered
				reason = fmt.Sprintf("target recovered after %d consecutive successful checks", t.consecRecoveries)
			}
		} else if t.policy.LatencyThreshold > 0 {
			if check.ResponseTime > t.policy.LatencyThreshold {
				t.latencyBreaches++
				if t.latencyBreaches >= t.policy.LatencyBreachCount {
					t.state = StateDegraded
					event = EventTargetDegraded
					reason = fmt.Sprintf("response time %s exceeded threshold %s", check.ResponseTime, t.policy.LatencyThreshold)
				}
			} else {
				t.latencyBreaches = 0
				if t.state == StateDegraded {
					t.state = StateHealthy
					event = EventTargetHealthy
					reason = fmt.Sprintf("response time %s is within threshold %s", check.ResponseTime, t.policy.LatencyThreshold)
				}
			}
		}
	}

	if t.policy.SSLExpiryThresholdDays > 0 && check.SSLDaysRemaining >= 0 {
		if check.SSLDaysRemaining > t.policy.SSLExpiryThresholdDays {
			t.sslAlerted = false
		} else if event == EventNone && !t.sslAlerted {
			event = EventSSLExpiring
			reason = fmt.Sprintf("certificate expires in %d days", check.SSLDaysRemaining)
			t.sslAlerted = true
		}
	}

	suppressed := false
	if event != EventNone && event != EventTargetRecovered && event != EventTargetHealthy {
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
		ConsecutiveFailures:   t.consecFailures,
		ConsecutiveRecoveries: t.consecRecoveries,
		LatencyBreaches:       t.latencyBreaches,
		SSLDaysRemaining:      t.sslDays,
		Suppressed:            suppressed,
	}
}
