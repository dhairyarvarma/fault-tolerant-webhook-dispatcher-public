CREATE INDEX idx_events_retention ON events(completed_at,id)
    WHERE status IN ('SUCCEEDED','CANCELED');
CREATE INDEX idx_outbox_event_id ON outbox_messages(event_id);
