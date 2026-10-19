package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/breaker"
)

// CircuitAllow reports whether a delivery attempt to target should proceed.
// When it denies work, retryAt is the earliest safe time to queue it again.
// If the circuit is OPEN and its cooldown has elapsed, this atomically claims
// the single HALF_OPEN probe slot (via a row lock), so concurrent workers
// can't both probe at once.
func (s *TargetStore) CircuitAllow(ctx context.Context, targetID string, cooldown time.Duration, probeDuration ...time.Duration) (allow bool, retryAt time.Time, generation int64, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, time.Time{}, 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var state string
	var openedAt *time.Time
	var probeUntil *time.Time
	if err := tx.QueryRowContext(ctx,
		`SELECT circuit_state, circuit_opened_at, circuit_probe_until, circuit_generation FROM targets WHERE id = $1 FOR UPDATE`, targetID,
	).Scan(&state, &openedAt, &probeUntil, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, time.Time{}, 0, ErrNotFound
		}
		return false, time.Time{}, 0, fmt.Errorf("lock target row: %w", err)
	}

	now := time.Now()
	newState, allow := breaker.Allow(state, openedAt, cooldown, now)
	if state == breaker.HalfOpen && (probeUntil == nil || !probeUntil.After(now)) {
		newState, allow = breaker.HalfOpen, true
	}
	if allow && newState == breaker.HalfOpen {
		duration := 30 * time.Second
		if len(probeDuration) > 0 {
			duration = probeDuration[0]
		}
		if err := tx.QueryRowContext(ctx,
			`UPDATE targets SET circuit_state=$2, circuit_generation=circuit_generation+1,
			 circuit_probe_until=now()+$3*interval '1 microsecond', updated_at=now()
			 WHERE id=$1 RETURNING circuit_generation`, targetID, newState, duration.Microseconds(),
		).Scan(&generation); err != nil {
			return false, time.Time{}, 0, fmt.Errorf("claim circuit probe: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, time.Time{}, 0, err
	}
	if allow {
		return true, time.Time{}, generation, nil
	}
	if state == breaker.HalfOpen && probeUntil != nil {
		return false, *probeUntil, generation, nil
	}
	if state == breaker.Open && openedAt != nil {
		return false, openedAt.Add(cooldown), generation, nil
	}
	return false, now.Add(cooldown), generation, nil
}

// RecordCircuitOutcome updates a target's circuit state after one delivery
// attempt actually ran: success closes the circuit and resets the failure
// count; a failure increments it, opening the circuit at failureThreshold
// (or immediately, if the failure was a HALF_OPEN probe).
func (s *TargetStore) RecordCircuitOutcome(ctx context.Context, targetID string, success bool, failureThreshold int, permitGeneration ...int64) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var state string
	var failures int
	var generation int64
	var probeUntil *time.Time
	if err := tx.QueryRowContext(ctx,
		`SELECT circuit_state, consecutive_failures, circuit_generation, circuit_probe_until FROM targets WHERE id = $1 FOR UPDATE`, targetID,
	).Scan(&state, &failures, &generation, &probeUntil); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("lock target row: %w", err)
	}
	if len(permitGeneration) > 0 && (permitGeneration[0] != generation || (state == breaker.HalfOpen && (probeUntil == nil || !probeUntil.After(time.Now())))) {
		return state, tx.Commit()
	}

	newState, newFailures := breaker.Outcome(state, failures, failureThreshold, success)

	var openedAt any
	if newState == breaker.Open {
		openedAt = time.Now()
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE targets
		SET circuit_state = $2, consecutive_failures = $3, circuit_opened_at = $4, updated_at = now(),
		 circuit_probe_until=NULL, circuit_generation=circuit_generation+CASE WHEN circuit_state<>$2 THEN 1 ELSE 0 END
		WHERE id = $1
	`, targetID, newState, newFailures, openedAt); err != nil {
		return "", fmt.Errorf("update circuit outcome: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return newState, nil
}
