# Database Design

PostgreSQL is the durable system of record.

## Tables

### `targets`

Purpose: destination configuration.

Suggested fields:

- `id` UUID primary key
- `name`
- `url`
- `secret_ciphertext` or another safe secret representation appropriate for the final deployment
- `enabled`
- `created_at`
- `updated_at`
- circuit-breaker state fields or a separate state table if justified

Do not store plaintext secrets in application logs.

### `events`

Suggested fields:

- `id` UUID primary key
- `target_id` foreign key
- `idempotency_key`
- `payload_json`
- `payload_hash`
- `status`
- `attempt_count`
- `next_attempt_at`
- `created_at`
- `updated_at`
- `completed_at`
- `delivery_token` nullable UUID and `delivery_lease_until` nullable timestamp

Required uniqueness:

```text
UNIQUE(target_id, idempotency_key)
```

Targets also persist `circuit_generation` and `circuit_probe_until` for
expiring, fenced half-open probes. Delivery ownership and circuit ownership
are separate from the event's user-visible status.

### `delivery_attempts`

Suggested fields:

- `id` UUID primary key
- `event_id` foreign key
- `attempt_number`
- `started_at`
- `finished_at`
- `outcome`
- `http_status` nullable
- `duration_ms`
- `error_class` nullable
- `error_message` nullable

Never store unbounded downstream response bodies.

### `outbox_messages`

Suggested fields:

- `id` UUID primary key
- `event_id` foreign key
- `created_at`
- `published_at` nullable
- `publish_attempts`
- `last_error` nullable

## Indexes

At minimum evaluate indexes for:

- events by `status`
- events by `target_id`
- events by `next_attempt_at`
- delivery attempts by `event_id`
- outbox rows by `published_at` and creation time

Only add indexes justified by query patterns.

## Transaction rules

Event creation + outbox creation must be atomic.

Due retry scheduling + insertion of a new outbox row must also be atomic.
An event can have multiple historical outbox rows: one for acceptance and
one for each scheduled retry/replay. Each publication row has its own ID;
all reference the same event and stable outbound idempotency key (Decision 30).

Status transitions must use conditional updates or row-level locking where needed to prevent concurrent workers from corrupting state.

## Historical data

Historical attempts should remain queryable after an event reaches `SUCCEEDED` or `DLQ`.

Do not cascade-delete operational history merely because a target is disabled.

Explicit opt-in retention is separate from target disable. It removes only
old successful/canceled history in locked, bounded-parent batches, atomically
deleting child attempts/published outbox rows before the event. DLQ, leased
events and unpublished work are excluded. Deletion ends that event's audit
and ingestion-idempotency record. See Decision 42; default remains disabled.
