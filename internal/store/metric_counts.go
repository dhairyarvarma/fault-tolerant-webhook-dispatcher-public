package store

import "context"

func (s *EventStore) WorkCounts(ctx context.Context) (pending, dlq int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE status IN ('PENDING','QUEUED','RETRY_WAIT')),
	 count(*) FILTER(WHERE status='DLQ') FROM events`).Scan(&pending, &dlq)
	return
}

func (s *TargetStore) CircuitCounts(ctx context.Context) (closed, open, halfOpen int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE circuit_state='CLOSED'),
	 count(*) FILTER(WHERE circuit_state='OPEN'),count(*) FILTER(WHERE circuit_state='HALF_OPEN')
	 FROM targets WHERE enabled`).Scan(&closed, &open, &halfOpen)
	return
}
