package alerts

import (
	"fmt"
	"time"
)

// Tracker remembers alert state for a single target.
type Tracker struct {
	policy Policy

	state         State
	previousState State

	consecutiveFailures   int
	consecutiveRecoveries int
	latencyBreaches       int
	sslDaysRemaining      int

	// sslLatched is true after ssl_expiring has been produced for the current
	// stay inside the expiry threshold. It clears only when remaining days
	// rise above the threshold.
	sslLatched bool

	lastNonRecovery time.Time
	hasNonRecovery  bool
}

// NewTracker returns a tracker that starts healthy.
func NewTracker(policy Policy) *Tracker {
	policy = normalizePolicy(policy)
	return &Tracker{
		policy:        policy,
		state:         StateHealthy,
		previousState: StateHealthy,
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
	if policy.LatencyThreshold > 0 && policy.LatencyBreachCount <= 0 {
		policy.LatencyBreachCount = 1
	}
	return policy
}

// Evaluate applies one check and returns the resulting snapshot.
// now is the time used for cooldown comparisons.
func (t *Tracker) Evaluate(check Check, now time.Time) Decision {
	previous := t.state
	t.sslDaysRemaining = check.SSLDaysRemaining
	t.noteSSL(check.SSLDaysRemaining)

	event := EventNone
	reason := ""

	if !check.IsUp {
		t.consecutiveFailures++
		t.consecutiveRecoveries = 0
		t.latencyBreaches = 0
		if t.consecutiveFailures >= t.policy.ConsecutiveFailures && t.state != StateDown {
			t.state = StateDown
			event = EventTargetDown
			reason = fmt.Sprintf("target failed %d consecutive checks", t.consecutiveFailures)
		}
	} else {
		t.consecutiveRecoveries++
		t.consecutiveFailures = 0

		if previous == StateDown {
			// Stay down until enough successful checks arrive. Latency counting
			// stays reset for the whole outage, including the recovering check,
			// and starts again on later up checks.
			t.latencyBreaches = 0
			if t.consecutiveRecoveries >= t.policy.ConsecutiveRecoveries {
				t.state = StateHealthy
				event = EventTargetRecovered
				reason = fmt.Sprintf("target recovered after %d consecutive successful checks", t.consecutiveRecoveries)
			}
		} else if t.slow(check) {
			t.latencyBreaches++
			if previous == StateDegraded || t.latencyBreaches >= t.policy.LatencyBreachCount {
				t.state = StateDegraded
				event = EventTargetDegraded
				reason = fmt.Sprintf(
					"response time %dms exceeded %dms latency threshold for %d consecutive checks",
					check.ResponseTime.Milliseconds(),
					t.policy.LatencyThreshold.Milliseconds(),
					t.latencyBreaches,
				)
			}
		} else {
			t.latencyBreaches = 0
			if previous == StateDegraded && t.belowLatency(check) {
				t.state = StateHealthy
				event = EventTargetHealthy
				reason = fmt.Sprintf(
					"response time %dms is below %dms latency threshold",
					check.ResponseTime.Milliseconds(),
					t.policy.LatencyThreshold.Milliseconds(),
				)
			}
		}
	}

	if event == EventNone {
		if sslReason, ok := t.takeSSL(check.SSLDaysRemaining); ok {
			event = EventSSLExpiring
			reason = sslReason
		}
	}

	suppressed := false
	if event != EventNone && !neverSuppressed(event) {
		if t.withinCooldown(now) {
			suppressed = true
		} else {
			t.lastNonRecovery = now
			t.hasNonRecovery = true
		}
	}

	if event != EventNone && reason == "" {
		reason = event.String()
	}

	t.previousState = previous

	return Decision{
		Event:                 event,
		State:                 t.state,
		PreviousState:         t.previousState,
		Reason:                reason,
		ConsecutiveFailures:   t.consecutiveFailures,
		ConsecutiveRecoveries: t.consecutiveRecoveries,
		LatencyBreaches:       t.latencyBreaches,
		SSLDaysRemaining:      t.sslDaysRemaining,
		Suppressed:            suppressed,
	}
}

func (t *Tracker) slow(check Check) bool {
	return t.policy.LatencyThreshold > 0 && check.ResponseTime > t.policy.LatencyThreshold
}

func (t *Tracker) belowLatency(check Check) bool {
	if t.policy.LatencyThreshold <= 0 {
		return true
	}
	return check.ResponseTime < t.policy.LatencyThreshold
}

func (t *Tracker) withinCooldown(now time.Time) bool {
	if t.policy.Cooldown <= 0 || !t.hasNonRecovery {
		return false
	}
	return now.Sub(t.lastNonRecovery) < t.policy.Cooldown
}

func neverSuppressed(event Event) bool {
	return event == EventTargetRecovered || event == EventTargetHealthy
}

// noteSSL re-arms expiry alerting once remaining life rises above the threshold.
// Negative days are not applicable and do not trigger or re-arm.
func (t *Tracker) noteSSL(days int) {
	if t.policy.SSLExpiryThresholdDays <= 0 || days < 0 {
		return
	}
	if days > t.policy.SSLExpiryThresholdDays {
		t.sslLatched = false
	}
}

func (t *Tracker) takeSSL(days int) (string, bool) {
	if t.policy.SSLExpiryThresholdDays <= 0 || days < 0 {
		return "", false
	}
	if days > t.policy.SSLExpiryThresholdDays || t.sslLatched {
		return "", false
	}
	t.sslLatched = true
	return fmt.Sprintf(
		"SSL certificate expires in %d days (threshold %d days)",
		days,
		t.policy.SSLExpiryThresholdDays,
	), true
}
