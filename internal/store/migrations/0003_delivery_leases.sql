ALTER TABLE events
    ADD COLUMN delivery_token UUID,
    ADD COLUMN delivery_lease_until TIMESTAMPTZ;

CREATE INDEX idx_events_recovery ON events (updated_at)
    WHERE status IN ('PENDING', 'QUEUED');
