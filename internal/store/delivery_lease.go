package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ClaimDelivery serializes HTTP attempts across duplicate queue messages.
func (s *EventStore) ClaimDelivery(ctx context.Context, id string, duration time.Duration) (string, bool, error) {
	var token string
	err := s.db.QueryRowContext(ctx, `UPDATE events
		SET delivery_token=gen_random_uuid(),
		    delivery_lease_until=now()+$2*interval '1 microsecond', updated_at=now()
		WHERE id=$1 AND status IN ('PENDING','QUEUED')
		  AND (delivery_lease_until IS NULL OR delivery_lease_until <= now())
		RETURNING delivery_token`, id, duration.Microseconds()).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("claim delivery: %w", err)
	}
	return token, true, nil
}

func (s *EventStore) ReleaseDelivery(ctx context.Context, id, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events
		SET delivery_token=NULL, delivery_lease_until=NULL
		WHERE id=$1 AND delivery_token=$2`, id, token)
	return err
}

func (s *EventStore) CancelDelivery(ctx context.Context, id, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events SET status='CANCELED',completed_at=now(),updated_at=now(),
	 delivery_token=NULL,delivery_lease_until=NULL
	 WHERE id=$1 AND delivery_token=$2 AND delivery_lease_until>now()`, id, token)
	return err
}

// RecoverStaleWork recreates transport hints without stealing active delivery.
func (s *EventStore) RecoverStaleWork(ctx context.Context, limit int, grace time.Duration) (int64, error) {
	result, err := s.db.ExecContext(ctx, `WITH stale AS (
		SELECT e.id FROM events e
		WHERE e.status IN ('PENDING','QUEUED')
		  AND e.updated_at <= now()-$2*interval '1 microsecond'
		  AND (e.delivery_lease_until IS NULL OR e.delivery_lease_until <= now())
		  AND NOT EXISTS (SELECT 1 FROM outbox_messages o WHERE o.event_id=e.id AND o.published_at IS NULL)
		ORDER BY e.updated_at,e.id LIMIT $1 FOR UPDATE OF e SKIP LOCKED
	), recovered AS (
		UPDATE events e SET updated_at=now(), delivery_token=NULL, delivery_lease_until=NULL
		FROM stale WHERE e.id=stale.id RETURNING e.id
	)
	INSERT INTO outbox_messages(event_id) SELECT id FROM recovered`, limit, grace.Microseconds())
	if err != nil {
		return 0, fmt.Errorf("recover stale work: %w", err)
	}
	return result.RowsAffected()
}
