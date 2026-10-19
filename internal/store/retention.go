package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PruneCompleted permanently deletes eligible terminal history in one transaction.
func (s *EventStore) PruneCompleted(ctx context.Context, age time.Duration, limit int) (int64, error) {
	if age < 24*time.Hour || limit < 1 || limit > 1000 {
		return 0, fmt.Errorf("retention requires age >=24h and batch between 1 and 1000")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT e.id FROM events e
 WHERE e.status IN ('SUCCEEDED','CANCELED')
 AND e.completed_at < now()-$1*interval '1 microsecond'
 AND e.created_at < now()-$1*interval '1 microsecond'
 AND e.delivery_token IS NULL AND e.delivery_lease_until IS NULL
 AND NOT EXISTS (SELECT 1 FROM outbox_messages o WHERE o.event_id=e.id AND o.published_at IS NULL)
 ORDER BY e.completed_at,e.id LIMIT $2 FOR UPDATE OF e SKIP LOCKED`, age.Microseconds(), limit)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(ids) > 0 {
		idsArray := "{" + strings.Join(ids, ",") + "}"
		if _, err = tx.ExecContext(ctx, `DELETE FROM delivery_attempts WHERE event_id=ANY($1::uuid[])`, idsArray); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM outbox_messages WHERE event_id=ANY($1::uuid[]) AND published_at IS NOT NULL`, idsArray); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM events WHERE id=ANY($1::uuid[])`, idsArray); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}
