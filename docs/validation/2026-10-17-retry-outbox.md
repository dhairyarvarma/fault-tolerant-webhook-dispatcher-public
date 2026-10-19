# Retry outbox regression verification — 2026-10-17

Tested working tree based on `edbc2c8`; this record is committed with the
retry-outbox fix. Runtime: Go 1.27.0, Windows amd64. PostgreSQL and Redis
were accessed directly on localhost using TEST_DATABASE_URL and
TEST_REDIS_URL. New tests create isolated PostgreSQL schemas and dedicated
Redis streams; cleanup removes only those resources.

## Scope and limits

- Due retry state changes and outbox inserts commit atomically.
- An injected outbox constraint failure rolls back the entire batch.
- Eight simultaneous schedulers schedule twelve retries once each.
- Batch limits, future deadlines, and non-retryable states are respected.
- Restart is represented by reconstructing the scheduler/relay at the
  relevant commit/publication boundary, not killing a process.
- The relay test injects a connection failure, then an unmarked successful
  publish. The restarted relay republishes the row, and one worker processes
  both messages with one successful HTTP call and the unchanged idempotency
  key. This does not prove concurrent duplicate execution is prevented.
- Complete loss of already-published Redis state remains unresolved.
- These are correctness checks; elapsed test times are not benchmarks.

## Commands

With TEST_DATABASE_URL and TEST_REDIS_URL configured for local dependencies:

```powershell
go test -count=3 -v ./internal/store ./internal/retry ./internal/outbox -run 'TestScheduleDueRetries|TestScheduler_RequeueDue_|TestRelay_RetrySurvives'
```

Repository checks also passed:

```powershell
# TEST_DATABASE_URL, TEST_REDIS_URL, and TEST_BASE_URL unset:
go test -count=1 ./...
go vet ./...
go build -buildvcs=false ./...
gofmt -l cmd internal test
git diff --check
```

The broad test run skips dependency-backed and HTTP acceptance tests without
their environment settings. The targeted run below executes the new
integration tests against real dependencies. VCS stamping was disabled for
the build because of sandbox repository ownership restrictions. Race tests
and the full live acceptance suite were not rerun for this change.

## Captured integration output

The Redis connection-refused lines below are expected fault injection.

```text
=== RUN   TestScheduleDueRetries_OutboxFailureRollsBackStateAndBatch
--- PASS: TestScheduleDueRetries_OutboxFailureRollsBackStateAndBatch (0.63s)
=== RUN   TestScheduleDueRetries_ConcurrentSchedulersCreateOneRowPerRetry
--- PASS: TestScheduleDueRetries_ConcurrentSchedulersCreateOneRowPerRetry (0.86s)
=== RUN   TestScheduleDueRetries_BatchLimitAndEligibility
--- PASS: TestScheduleDueRetries_BatchLimitAndEligibility (0.55s)
=== RUN   TestScheduleDueRetries_OutboxFailureRollsBackStateAndBatch
--- PASS: TestScheduleDueRetries_OutboxFailureRollsBackStateAndBatch (0.47s)
=== RUN   TestScheduleDueRetries_ConcurrentSchedulersCreateOneRowPerRetry
--- PASS: TestScheduleDueRetries_ConcurrentSchedulersCreateOneRowPerRetry (1.20s)
=== RUN   TestScheduleDueRetries_BatchLimitAndEligibility
--- PASS: TestScheduleDueRetries_BatchLimitAndEligibility (0.91s)
=== RUN   TestScheduleDueRetries_OutboxFailureRollsBackStateAndBatch
--- PASS: TestScheduleDueRetries_OutboxFailureRollsBackStateAndBatch (0.66s)
=== RUN   TestScheduleDueRetries_ConcurrentSchedulersCreateOneRowPerRetry
--- PASS: TestScheduleDueRetries_ConcurrentSchedulersCreateOneRowPerRetry (1.38s)
=== RUN   TestScheduleDueRetries_BatchLimitAndEligibility
--- PASS: TestScheduleDueRetries_BatchLimitAndEligibility (0.85s)
PASS
ok  	github.com/dhairyarajvarma/webhook-dispatcher/internal/store	14.468s
=== RUN   TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis
=== RUN   TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/0s
=== RUN   TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/1h0m0s
--- PASS: TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis (1.32s)
    --- PASS: TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/0s (0.83s)
    --- PASS: TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/1h0m0s (0.49s)
=== RUN   TestScheduler_RequeueDue_ReportsDatabaseFailure
--- PASS: TestScheduler_RequeueDue_ReportsDatabaseFailure (0.00s)
=== RUN   TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis
=== RUN   TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/0s
=== RUN   TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/1h0m0s
--- PASS: TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis (1.01s)
    --- PASS: TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/0s (0.51s)
    --- PASS: TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/1h0m0s (0.50s)
=== RUN   TestScheduler_RequeueDue_ReportsDatabaseFailure
--- PASS: TestScheduler_RequeueDue_ReportsDatabaseFailure (0.00s)
=== RUN   TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis
=== RUN   TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/0s
=== RUN   TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/1h0m0s
--- PASS: TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis (0.98s)
    --- PASS: TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/0s (0.44s)
    --- PASS: TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis/1h0m0s (0.54s)
=== RUN   TestScheduler_RequeueDue_ReportsDatabaseFailure
--- PASS: TestScheduler_RequeueDue_ReportsDatabaseFailure (0.00s)
PASS
ok  	github.com/dhairyarajvarma/webhook-dispatcher/internal/retry	9.599s
=== RUN   TestRelay_RetrySurvivesSchedulerExitPublishFailureAndDuplicatePublication
redis: 2026/10/17 12:35:50 pool.go:770: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connectex: No connection could be made because the target machine actively refused it.
--- PASS: TestRelay_RetrySurvivesSchedulerExitPublishFailureAndDuplicatePublication (3.25s)
=== RUN   TestRelay_RetrySurvivesSchedulerExitPublishFailureAndDuplicatePublication
redis: 2026/10/17 12:35:53 pool.go:770: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connectex: No connection could be made because the target machine actively refused it.
--- PASS: TestRelay_RetrySurvivesSchedulerExitPublishFailureAndDuplicatePublication (3.24s)
=== RUN   TestRelay_RetrySurvivesSchedulerExitPublishFailureAndDuplicatePublication
redis: 2026/10/17 12:35:56 pool.go:770: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connectex: No connection could be made because the target machine actively refused it.
--- PASS: TestRelay_RetrySurvivesSchedulerExitPublishFailureAndDuplicatePublication (3.06s)
PASS
ok  	github.com/dhairyarajvarma/webhook-dispatcher/internal/outbox	18.459s
```
