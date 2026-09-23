package notifications

import (
	"testing"

	"github.com/Owloops/updo/alerts"
)

func TestDecisionMessage(t *testing.T) {
	tests := []struct {
		decision alerts.Decision
		want     string
	}{
		{decision: alerts.Decision{Event: alerts.EventTargetDown}, want: "API is down!"},
		{decision: alerts.Decision{Event: alerts.EventTargetRecovered}, want: "API is back up!"},
		{decision: alerts.Decision{Event: alerts.EventTargetDegraded, Reason: "slow"}, want: "API is degraded: slow"},
		{decision: alerts.Decision{Event: alerts.EventTargetHealthy}, want: "API is healthy again"},
		{decision: alerts.Decision{Event: alerts.EventSSLExpiring, Reason: "expires soon"}, want: "API: expires soon"},
	}

	for _, tc := range tests {
		t.Run(tc.decision.Event.String(), func(t *testing.T) {
			if got := decisionMessage(tc.decision, "API"); got != tc.want {
				t.Errorf("decisionMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleAlertDecisionSkipsUndeliverable(t *testing.T) {
	for _, decision := range []alerts.Decision{
		{State: alerts.StateDown},
		{Event: alerts.EventTargetDown, State: alerts.StateDown, Suppressed: true},
	} {
		if err := HandleAlertDecision(decision, "API", _testAPIURL); err != nil {
			t.Errorf("HandleAlertDecision(%+v) = %v, want nil", decision, err)
		}
	}
}

func TestHandleAlertsLogic(t *testing.T) {
	tests := []struct {
		name         string
		isUp         bool
		initialSent  bool
		expectedSent bool
	}{
		{
			name:         "Site goes down for the first time",
			isUp:         false,
			initialSent:  false,
			expectedSent: true,
		},
		{
			name:         "Site is still down",
			isUp:         false,
			initialSent:  true,
			expectedSent: true,
		},
		{
			name:         "Site comes back up",
			isUp:         true,
			initialSent:  true,
			expectedSent: false,
		},
		{
			name:         "Site is still up",
			isUp:         true,
			initialSent:  false,
			expectedSent: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			alertSent := tc.initialSent

			_ = HandleAlerts(tc.isUp, &alertSent, "Test Site", "https://example.com")

			if alertSent != tc.expectedSent {
				t.Errorf("Expected alertSent to be: %v, got: %v", tc.expectedSent, alertSent)
			}
		})
	}
}
