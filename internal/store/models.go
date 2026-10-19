package store

import (
	"encoding/json"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/breaker"
)

// Event status values. QUEUE_AND_DELIVERY_SEMANTICS.md's recommended state
// list also includes DELIVERING; this implementation doesn't need it as a
// distinct persisted state - a worker holds an event's "in flight" status
// implicitly (claimed-but-unacked in the Redis consumer group) rather than
// writing an extra status transition for it, so there's no separate
// DELIVERING row to go stale if a worker crashes mid-call. See DECISIONS.md.
const (
	StatusPending   = "PENDING"
	StatusQueued    = "QUEUED"
	StatusRetryWait = "RETRY_WAIT"
	StatusSucceeded = "SUCCEEDED"
	StatusDLQ       = "DLQ"
	StatusCanceled  = "CANCELED"
)

// Circuit state values per QUEUE_AND_DELIVERY_SEMANTICS.md. Aliased from
// internal/breaker, the canonical owner of these names, so the two packages
// can't drift out of sync.
const (
	CircuitClosed   = breaker.Closed
	CircuitOpen     = breaker.Open
	CircuitHalfOpen = breaker.HalfOpen
)

// Delivery attempt outcomes. Matches the delivery_attempts.outcome CHECK
// constraint. NonRetryableFailure classification arrives in Phase 3; Phase 2
// records every failure as retryable.
const (
	OutcomeSuccess             = "SUCCESS"
	OutcomeRetryableFailure    = "RETRYABLE_FAILURE"
	OutcomeNonRetryableFailure = "NON_RETRYABLE_FAILURE"
)

type Target struct {
	ID               string
	Name             string
	URL              string
	SecretCiphertext []byte
	Enabled          bool
	CircuitState     string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Event struct {
	ID             string
	TargetID       string
	IdempotencyKey string
	PayloadJSON    json.RawMessage
	PayloadHash    string
	Status         string
	AttemptCount   int
	NextAttemptAt  *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
}

type DeliveryAttempt struct {
	ID            string
	EventID       string
	AttemptNumber int
	StartedAt     time.Time
	FinishedAt    *time.Time
	Outcome       string
	HTTPStatus    *int
	DurationMS    *int
	ErrorClass    *string
	ErrorMessage  *string
}

type OutboxMessage struct {
	ID              string
	EventID         string
	CreatedAt       time.Time
	PublishedAt     *time.Time
	PublishAttempts int
	LastError       *string
}
