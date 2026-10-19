// Package breaker implements the per-target circuit breaker state machine
// described in QUEUE_AND_DELIVERY_SEMANTICS.md: CLOSED -> OPEN on repeated
// failures, OPEN -> HALF_OPEN after a cooldown, then CLOSED on a successful
// probe or back to OPEN on a failed one. The functions here are pure state
// transitions; internal/store persists the result.
package breaker

import "time"

// Circuit states, matching the targets.circuit_state CHECK constraint.
const (
	Closed   = "CLOSED"
	Open     = "OPEN"
	HalfOpen = "HALF_OPEN"
)

// Allow decides whether a delivery attempt to the target should proceed,
// given its current circuit state. If the circuit is OPEN and cooldown has
// elapsed, it claims the single HALF_OPEN probe slot by returning that as
// the new state - callers must persist it before running the probe, so a
// concurrent caller serialized behind the same row lock sees HALF_OPEN and
// is correctly denied a second simultaneous probe.
func Allow(state string, openedAt *time.Time, cooldown time.Duration, now time.Time) (newState string, allow bool) {
	switch state {
	case Closed:
		return Closed, true
	case HalfOpen:
		return HalfOpen, false
	default: // Open
		// A nil openedAt on an OPEN circuit shouldn't happen by construction
		// (RecordCircuitOutcome always sets it when opening), but treating
		// it as "cooldown already elapsed" is the safer default: it allows a
		// recovery probe instead of wedging the circuit open forever.
		if openedAt != nil && now.Sub(*openedAt) < cooldown {
			return Open, false
		}
		return HalfOpen, true
	}
}

// Outcome decides the circuit's resulting state and failure count after one
// delivery attempt actually ran (i.e. Allow returned true for it).
func Outcome(state string, consecutiveFailures, failureThreshold int, success bool) (newState string, newFailures int) {
	if success {
		return Closed, 0
	}
	if state == HalfOpen {
		return Open, consecutiveFailures // a failed probe reopens immediately
	}
	newFailures = consecutiveFailures + 1
	if newFailures >= failureThreshold {
		return Open, newFailures
	}
	return state, newFailures
}
