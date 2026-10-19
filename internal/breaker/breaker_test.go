package breaker

import (
	"testing"
	"time"
)

func TestAllow_ClosedAlwaysAllows(t *testing.T) {
	state, allow := Allow(Closed, nil, time.Minute, time.Now())
	if state != Closed || !allow {
		t.Errorf("Allow(Closed) = (%q, %v), want (CLOSED, true)", state, allow)
	}
}

func TestAllow_HalfOpenDeniesConcurrentProbes(t *testing.T) {
	state, allow := Allow(HalfOpen, nil, time.Minute, time.Now())
	if state != HalfOpen || allow {
		t.Errorf("Allow(HalfOpen) = (%q, %v), want (HALF_OPEN, false)", state, allow)
	}
}

func TestAllow_OpenDeniesBeforeCooldown(t *testing.T) {
	openedAt := time.Now().Add(-5 * time.Second)
	state, allow := Allow(Open, &openedAt, time.Minute, time.Now())
	if state != Open || allow {
		t.Errorf("Allow(Open, 5s ago, 1m cooldown) = (%q, %v), want (OPEN, false)", state, allow)
	}
}

func TestAllow_OpenClaimsHalfOpenAfterCooldown(t *testing.T) {
	openedAt := time.Now().Add(-2 * time.Minute)
	state, allow := Allow(Open, &openedAt, time.Minute, time.Now())
	if state != HalfOpen || !allow {
		t.Errorf("Allow(Open, 2m ago, 1m cooldown) = (%q, %v), want (HALF_OPEN, true)", state, allow)
	}
}

func TestAllow_OpenWithNilOpenedAtIsTreatedAsElapsed(t *testing.T) {
	// Defensive: a nil circuit_opened_at on an OPEN row shouldn't happen by
	// construction, but must not wedge the circuit open forever if it does.
	state, allow := Allow(Open, nil, time.Minute, time.Now())
	if state != HalfOpen || !allow {
		t.Errorf("Allow(Open, nil openedAt) = (%q, %v), want (HALF_OPEN, true)", state, allow)
	}
}

func TestOutcome_SuccessAlwaysCloses(t *testing.T) {
	state, failures := Outcome(Open, 10, 5, true)
	if state != Closed || failures != 0 {
		t.Errorf("Outcome(Open, failures=10, success) = (%q, %d), want (CLOSED, 0)", state, failures)
	}
}

func TestOutcome_FailureBelowThresholdStaysClosed(t *testing.T) {
	state, failures := Outcome(Closed, 2, 5, false)
	if state != Closed || failures != 3 {
		t.Errorf("Outcome(Closed, failures=2, threshold=5, failure) = (%q, %d), want (CLOSED, 3)", state, failures)
	}
}

func TestOutcome_FailureAtThresholdOpens(t *testing.T) {
	state, failures := Outcome(Closed, 4, 5, false)
	if state != Open || failures != 5 {
		t.Errorf("Outcome(Closed, failures=4, threshold=5, failure) = (%q, %d), want (OPEN, 5)", state, failures)
	}
}

func TestOutcome_FailedProbeReopensImmediately(t *testing.T) {
	// A HALF_OPEN probe failing must reopen regardless of the failure
	// threshold - there's only ever one probe in flight, so "repeated
	// failures over threshold" doesn't apply here.
	state, failures := Outcome(HalfOpen, 0, 5, false)
	if state != Open || failures != 0 {
		t.Errorf("Outcome(HalfOpen, failure) = (%q, %d), want (OPEN, 0)", state, failures)
	}
}

func TestOutcome_SuccessfulProbeCloses(t *testing.T) {
	state, failures := Outcome(HalfOpen, 0, 5, true)
	if state != Closed || failures != 0 {
		t.Errorf("Outcome(HalfOpen, success) = (%q, %d), want (CLOSED, 0)", state, failures)
	}
}
