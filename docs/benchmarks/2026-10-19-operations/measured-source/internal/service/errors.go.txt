package service

import (
	"errors"
	"fmt"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

// ValidationError indicates the caller supplied invalid input. API handlers
// map it to HTTP 422/400 depending on the field.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func validationErrorf(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// ErrTargetDisabled is returned when an event is created against a disabled target.
var ErrTargetDisabled = store.ErrTargetDisabled

// ErrNotEligibleForReplay is returned when a manual replay is requested for
// an event that is not currently in DLQ.
var ErrNotEligibleForReplay = errors.New("event is not eligible for replay")

// ErrOverloaded is returned when admission control (EVENTS_MAX_PENDING_EVENTS)
// rejects new event creation because the durable pending-work count is at or
// above the configured bound. Mapped to HTTP 429 by the API layer.
var ErrOverloaded = store.ErrOverloaded
