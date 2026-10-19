package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

const (
	minIdempotencyKeyLen = 1
	maxIdempotencyKeyLen = 256
)

type EventService struct {
	events     *store.EventStore
	targets    *store.TargetStore
	maxPending int // EVENTS_MAX_PENDING_EVENTS; 0 disables admission control
}

func NewEventService(events *store.EventStore, targets *store.TargetStore, maxPending int) *EventService {
	return &EventService{events: events, targets: targets, maxPending: maxPending}
}

// Create validates and persists a new webhook event for target, atomically
// with its outbox entry. If the (target, idempotencyKey) pair already has an
// event, the existing event is returned with created=false and no new state
// is written, per QUEUE_AND_DELIVERY_SEMANTICS.md.
func (s *EventService) Create(
	ctx context.Context, targetID, idempotencyKey string, payload json.RawMessage,
) (event store.Event, created bool, err error) {
	if err := validateID(targetID); err != nil {
		return store.Event{}, false, err
	}
	if len(idempotencyKey) < minIdempotencyKeyLen || len(idempotencyKey) > maxIdempotencyKeyLen {
		return store.Event{}, false, validationErrorf(
			"Idempotency-Key must be between %d and %d characters", minIdempotencyKeyLen, maxIdempotencyKeyLen)
	}
	if len(payload) == 0 || string(payload) == "null" {
		return store.Event{}, false, validationErrorf("payload is required")
	}
	if !json.Valid(payload) {
		return store.Event{}, false, validationErrorf("payload must be valid JSON")
	}

	target, err := s.targets.Get(ctx, targetID)
	if err != nil {
		return store.Event{}, false, err
	}
	if !target.Enabled {
		return store.Event{}, false, ErrTargetDisabled
	}

	sum := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(sum[:])

	return s.events.CreateWithOutbox(ctx, targetID, idempotencyKey, payload, payloadHash, s.maxPending)
}

// Get returns an event by ID. Returns store.ErrNotFound if it does not exist.
func (s *EventService) Get(ctx context.Context, id string) (store.Event, error) {
	if err := validateID(id); err != nil {
		return store.Event{}, err
	}
	return s.events.GetByID(ctx, id)
}

const (
	defaultEventListLimit = 50
	maxEventListLimit     = 100
)

// ListEvents returns up to limit events with the given status, newest first.
// status must be a documented event status; limit is clamped to
// [1, 100] with a default of 50. Returns validationErrorf for a bad status.
func (s *EventService) ListEvents(ctx context.Context, status string, limit int) ([]store.Event, error) {
	if !store.IsValidStatus(status) {
		return nil, validationErrorf("unknown status %q", status)
	}
	if limit <= 0 {
		limit = defaultEventListLimit
	}
	if limit > maxEventListLimit {
		limit = maxEventListLimit
	}
	events, err := s.events.ListByStatus(ctx, status, limit)
	if err != nil {
		return nil, err
	}
	if events == nil {
		events = []store.Event{}
	}
	return events, err
}

// ListAttempts returns the bounded delivery attempt history for an event.
// Returns store.ErrNotFound if the event does not exist.
func (s *EventService) ListAttempts(ctx context.Context, eventID string, limit int) ([]store.DeliveryAttempt, error) {
	if err := validateID(eventID); err != nil {
		return nil, err
	}
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return nil, err
	}
	return s.events.ListAttempts(ctx, eventID, limit)
}

// Replay resets an eligible (DLQ) event for a fresh delivery attempt. Returns
// store.ErrNotFound if the event doesn't exist, or ErrNotEligibleForReplay if
// it exists but isn't currently DLQ.
func (s *EventService) Replay(ctx context.Context, eventID string) (store.Event, error) {
	if err := validateID(eventID); err != nil {
		return store.Event{}, err
	}
	event, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return store.Event{}, err
	}
	target, err := s.targets.Get(ctx, event.TargetID)
	if err != nil {
		return store.Event{}, err
	}
	if !target.Enabled {
		return store.Event{}, ErrTargetDisabled
	}
	applied, err := s.events.ReplayFromDLQ(ctx, eventID)
	if err != nil {
		return store.Event{}, err
	}
	if !applied {
		return store.Event{}, ErrNotEligibleForReplay
	}
	return s.events.GetByID(ctx, eventID)
}
