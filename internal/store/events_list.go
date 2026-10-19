package store

import "context"

// Pending statuses: work accepted by the API that has not reached a
// terminal outcome and has not been dead-lettered. This is the set the
// admission check (EVENTS_MAX_PENDING_EVENTS, DECISIONS.md #28) counts:
// DLQ is deliberately excluded because dead-letter volume is not the
// runaway 2^attempt growth problem this limit guards against.
var pendingStatuses = []string{StatusPending, StatusQueued, StatusRetryWait}

// CountPending returns the number of events currently in a non-terminal,
// non-DLQ state (PENDING, QUEUED, RETRY_WAIT). Used by the API's
// admission-control check; not by the delivery path.
func (s *EventStore) CountPending(ctx context.Context) (int, error) {
	query := `SELECT COUNT(*) FROM events WHERE status = ANY($1)`
	row := s.db.QueryRowContext(ctx, query, pendingStatuses)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ListByStatus returns up to limit events with the given status, newest
// first. Status must be one of the documented event statuses; limit is
// clamped by the caller (service layer) before reaching here.
func (s *EventStore) ListByStatus(ctx context.Context, status string, limit int) ([]Event, error) {
	if !isValidStatus(status) {
		return nil, ErrNotFound
	}
	query := `SELECT ` + eventColumns + ` FROM events WHERE status = $1
		ORDER BY created_at DESC, id DESC LIMIT $2`
	rows, err := s.db.QueryContext(ctx, query, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(
			&ev.ID, &ev.TargetID, &ev.IdempotencyKey, &ev.PayloadJSON, &ev.PayloadHash, &ev.Status,
			&ev.AttemptCount, &ev.NextAttemptAt, &ev.CreatedAt, &ev.UpdatedAt, &ev.CompletedAt,
		); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func isValidStatus(status string) bool {
	switch status {
	case StatusPending, StatusQueued, StatusRetryWait, StatusSucceeded, StatusDLQ, StatusCanceled:
		return true
	}
	return false
}

// IsValidStatus reports whether status is one of the documented event
// statuses. Exported so the service layer can return a validation error
// (422) rather than a not-found (404) for an unknown status filter.
func IsValidStatus(status string) bool { return isValidStatus(status) }
