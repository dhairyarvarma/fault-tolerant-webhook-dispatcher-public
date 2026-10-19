# Test Plan

## Test pyramid

### Unit tests

Cover deterministic business logic:

- retry classification
- exponential backoff
- jitter bounds
- circuit-breaker transitions
- event state transitions
- idempotency behavior
- URL validation/SSRF policy
- header filtering
- HMAC signing
- response classification

### Repository/database tests

- target creation
- event creation + outbox atomicity
- unique idempotency constraint
- concurrent state transitions
- attempt persistence

### Integration tests

Run PostgreSQL + Redis + target simulator in Docker.

Test the full path:

```text
API → DB → Outbox → Redis → Worker → Target → DB
```

### Failure tests

Every row in `FAILURE_MODES.md` should have either an automated integration test or a deterministic procedure.

### Concurrency tests

Test:

- multiple workers on the same queue
- duplicate queue messages
- same idempotency key submitted concurrently
- manual retry racing with an active delivery
- target circuit state under concurrent failures

### Load tests

Use k6 for the ingestion API and, where useful, for target-side load.

## Completion gate

A milestone is incomplete if tests fail or a new race is discovered without a documented resolution.

## Database isolation

Database test fixtures now use temporary schemas via `internal/testutil`.
They do not share tables with the running application or other test cases.
The original interference below motivated this change; UUID event names
alone did not isolate global relay/scheduler queries. Tests remove only
their own schemas/streams. Black-box tests deliberately exercise the live
application selected by TEST_BASE_URL and should use a dedicated app instance.

### Historical shared-table interference

Discovered while polishing Phase 9: the former scheduler publish-failure test
failed intermittently with the event ending up `DLQ` instead of the
expected `RETRY_WAIT`. Root cause was not a product bug - a live `api`
container (started earlier for the Phase 8 deployment work) was still
running against the same `TEST_DATABASE_URL`/`TEST_REDIS_URL` database,
and its own outbox relay and retry scheduler were actively claiming the
exact same globally-due rows the test was trying to claim and revert
(`ScheduleDueRetries` and `FetchUnpublished` are intentionally unscoped by
stream/caller - that's correct production behavior, but it means a live
instance and a test run race for the same work). Stopping the container
(`docker compose stop api`) made the failure disappear immediately and
reproducibly. Stop any live app instance before running `go test ./...`
against the shared dev Postgres/Redis.

New retry scheduling and publication-recovery tests use isolated PostgreSQL
schemas (`internal/testutil.OpenDB`) and dedicated Redis streams, with cleanup
restricted to those resources. They can run alongside a live application.
Scheduling tests require only `TEST_DATABASE_URL`; relay/worker recovery
tests also require `TEST_REDIS_URL`. See Decision 30 for the changed protocol.

## Race detector

Use Go's race detector for supported tests:

```bash
go test -race ./...
```

## Static checks

At minimum:

```bash
gofmt -w .
go vet ./...
go test ./...
```

Use the chosen linter once the final tooling configuration is created.
