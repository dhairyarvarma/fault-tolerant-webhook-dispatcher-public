package store

import "errors"

var (
	// ErrNotFound is returned when a requested row does not exist.
	ErrNotFound       = errors.New("not found")
	ErrOverloaded     = errors.New("pending event limit reached")
	ErrTargetDisabled = errors.New("target is disabled")
)
