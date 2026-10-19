ALTER TABLE targets
    ADD COLUMN circuit_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN circuit_probe_until TIMESTAMPTZ;
