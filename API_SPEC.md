# API Specification

The API is intentionally small. The core project is the backend delivery engine, not a CRUD platform.

## Authentication

- Local development may run with auth disabled.
- Non-local deployment must require an API key supplied in `X-API-Key`.
- Never log credentials.

## Targets

### POST `/api/v1/targets`

Create a destination.

Request:

```json
{
  "name": "example-service",
  "url": "https://example.com/webhook",
  "secret": "shared-receiver-secret"
}
```

Response should include a stable `target_id`, configuration summary, and timestamps. Do not echo the secret.

### GET `/api/v1/targets/{target_id}`

Return target configuration summary and current circuit state.

### DELETE `/api/v1/targets/{target_id}`

Disable/remove a target according to the final persistence policy. Do not destroy historical delivery data automatically.

Disabling retains the target and history. Workers cancel pending delivery
when they observe the disabled target, without making another HTTP call;
an already in-flight call cannot be undone. New events and manual replay
against a disabled target return 403. Invalid resource UUIDs return 422.

## Events

### POST `/api/v1/targets/{target_id}/events`

Create a durable webhook event.

Headers:

```text
Idempotency-Key: <client supplied key>
```

Request:

```json
{
  "payload": {
    "type": "invoice.created",
    "id": "inv_123"
  }
}
```

Constraints:

- payload size is bounded by configuration;
- accepted payload content type is JSON for the initial version;
- client-supplied outbound headers are not accepted in the initial version; the
  delivery engine sets only its own allowed headers;
- target URL is validated against `SECURITY_MODEL.md`.

Response:

```json
{
  "event_id": "...",
  "target_id": "...",
  "status": "PENDING",
  "created_at": "..."
}
```

Repeated use of the same `Idempotency-Key` for the same target must not create a second logical event.

### GET `/api/v1/events?status=`

List newest events with the given status (one of the documented event
statuses). Optional bounded `limit` (default 50, max 100). Intended for
operator triage (e.g. DLQ inspection) without SQL access.

### GET `/api/v1/events/{event_id}`

Return current event status, attempt count, next retry time if applicable, and timestamps.

### GET `/api/v1/events/{event_id}/attempts`

Return bounded attempt history. Do not expose arbitrary secret material.

History is newest-first by started_at and ID. Attempt numbers are per replay
budget and can repeat after an explicit replay.

### POST `/api/v1/events/{event_id}/retry`

Request an explicit retry/replay of an eligible failed/DLQ event. The operation must be idempotent enough to avoid accidentally creating uncontrolled retry storms.

## Operational endpoints

### GET `/healthz`

Process liveness only. It should not fail merely because a dependency is temporarily unavailable.

### GET `/readyz`

Dependency readiness check. Should reflect whether the service can safely accept work.

### GET `/metrics`

Prometheus metrics.

## Error format

Use one stable error envelope:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "human-readable safe message",
    "request_id": "..."
  }
}
```

Do not expose database internals, stack traces, private URLs, secrets, or raw downstream response bodies through public errors.

## Status codes

Use ordinary HTTP semantics:

- `200` successful retrieval/operation
- `201` created
- `202` accepted for asynchronous event creation where appropriate
- `400` malformed request
- `401` unauthenticated
- `403` authenticated but forbidden
- `404` resource not found
- `409` idempotency/conflict state
- `413` payload too large
- `422` semantic validation failure
- `429` API rate-limit response; used by admission control when `EVENTS_MAX_PENDING_EVENTS` is configured and the durable pending row count is at or above the bound (includes `Retry-After`)
- `500` unexpected server error
- `503` not ready/unavailable where appropriate

The final OpenAPI contract is also provided in `contracts/openapi.yaml`.

## Optional history retention

Cleanup is disabled by default. When HISTORY_RETENTION_AGE is enabled (>=24h),
old completed SUCCEEDED/CANCELED events and their history may be deleted.
Event/attempt endpoints then return 404; duplicate acceptance is protected
only while the original event row is retained. A key reused after deletion
may create a new event. DLQ events are retained for replay. See Decision 42.
