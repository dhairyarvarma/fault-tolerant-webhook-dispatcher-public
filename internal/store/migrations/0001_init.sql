CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE targets (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name              TEXT NOT NULL,
    url               TEXT NOT NULL,
    secret_ciphertext BYTEA,
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    circuit_state     TEXT NOT NULL DEFAULT 'CLOSED'
                          CHECK (circuit_state IN ('CLOSED', 'OPEN', 'HALF_OPEN')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE events (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_id        UUID NOT NULL REFERENCES targets (id),
    idempotency_key  TEXT NOT NULL,
    payload_json     JSONB NOT NULL,
    payload_hash     TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'PENDING'
                         CHECK (status IN ('PENDING', 'QUEUED', 'DELIVERING', 'RETRY_WAIT',
                                            'SUCCEEDED', 'DLQ', 'CANCELED')),
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ,
    UNIQUE (target_id, idempotency_key)
);

CREATE INDEX idx_events_status ON events (status);
CREATE INDEX idx_events_target_id ON events (target_id);
CREATE INDEX idx_events_next_attempt_at ON events (next_attempt_at);

CREATE TABLE delivery_attempts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id        UUID NOT NULL REFERENCES events (id),
    attempt_number  INTEGER NOT NULL,
    started_at      TIMESTAMPTZ NOT NULL,
    finished_at     TIMESTAMPTZ,
    outcome         TEXT NOT NULL
                        CHECK (outcome IN ('SUCCESS', 'RETRYABLE_FAILURE', 'NON_RETRYABLE_FAILURE')),
    http_status     INTEGER,
    duration_ms     INTEGER,
    error_class     TEXT,
    error_message   TEXT
);

CREATE INDEX idx_delivery_attempts_event_id ON delivery_attempts (event_id);

CREATE TABLE outbox_messages (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id         UUID NOT NULL REFERENCES events (id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at     TIMESTAMPTZ,
    publish_attempts INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT
);

CREATE INDEX idx_outbox_messages_published_at ON outbox_messages (published_at);
CREATE INDEX idx_outbox_messages_created_at ON outbox_messages (created_at);
