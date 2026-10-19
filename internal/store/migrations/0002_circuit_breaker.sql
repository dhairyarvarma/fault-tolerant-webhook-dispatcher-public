ALTER TABLE targets
    ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN circuit_opened_at TIMESTAMPTZ;
