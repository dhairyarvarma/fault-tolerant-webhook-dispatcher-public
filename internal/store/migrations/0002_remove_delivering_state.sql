-- Tighten the events.status constraint: DELIVERING was defined in 0001 but
-- never written by any code path (see DECISIONS.md #27). Excluding it here
-- prevents any future writer from silently reintroducing a stale state.
ALTER TABLE events DROP CONSTRAINT events_status_check;
ALTER TABLE events ADD CONSTRAINT events_status_check
    CHECK (status IN ('PENDING', 'QUEUED', 'RETRY_WAIT', 'SUCCEEDED', 'DLQ', 'CANCELED'));
