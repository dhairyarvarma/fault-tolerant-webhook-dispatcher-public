# Target isolation verification — 2026-10-18

## Scope

Implemented Decision 40 after the retained mixed-target run showed healthy
delivery delay behind slow successful HTTP. WORKER_TARGET_CONCURRENCY defaults
to zero; a positive value no greater than WORKER_COUNT bounds active work for
one target inside one process. Default behavior is unchanged. Run callers,
configuration parsing/example, architecture, queue semantics, operator guide
and benchmark documentation were updated together. No schema/dependency/API
contract was added. Replicas do not share the limit.

The saturation path uses the existing delivery ownership token and DeferRetry
transaction. It schedules 1–2 seconds of jittered waiting without HTTP or an
attempt, then the normal scheduler/outbox republishes work. Active target map
entries are removed when the last holder releases. More SQL/scheduled work
and a longer hot-target drain are intentional costs, not hidden performance
improvements. No cluster-wide fairness guarantee is claimed.

## Regressions

- Concurrent target-slot acquisition stays within two, releases entries and
  does not retain idle target identities. Disabled limits allocate no entries.
- Real PostgreSQL regression holds one slow receiver call open, durably
  defers another event for that target with no recorded attempt/extra HTTP,
  and lets another target finish. A duplicate RETRY_WAIT hint cannot bypass
  deferral. Due scheduling later recovers the deferred event with one attempt.
- Configuration rejects negative, malformed and above-worker-count limits;
  accepts zero, two and four with four workers.
- PowerShell harness self-tests reject failed ingestion, unfinished healthy
  work and unrelated verification errors instead of accepting every nonzero
  failing-target runner result.

## Executed evidence

Both 30-second-control / concurrent-mixed / control experiments completed
their outcome checks; raw results, measured tradeoffs, shortened settings,
image identities, source patch and privacy transformations are in
[the mixed-target report](../benchmarks/2026-10-18-mixed/README.md).
All 131 preceding benchmark hashes remained unchanged. There was no new
ten-minute soak, production deployment, public upload or hosted CI run.

Targeted native config/worker/outbox/server tests, harness self-tests, Go
formatting and changed documentation links passed. Native runs without
dependency URLs skip integration cases; those are not the database evidence.

Linux verification uses a read-only source mount and a unique disposable
PostgreSQL/Redis stack. Command inside golang:1.27.2:

```sh
go test -race -count=1 -timeout 3m ./... &&
go vet ./... && go build ./... && go run ./cmd/acceptance
```

The first run (target-tests-b1ff9c88) failed because an archived .go test copy
under docs was compiled as an unintended package. All actual application
packages passed, including worker race tests. The archive was renamed to
.go.txt without changing runtime code; the failed log is retained, not erased.
The full rerun uses target-tests-3ef62385. Its raw log is retained locally
under reports/generated and copied into the benchmark bundle. It passed the
full race suite with real PostgreSQL/Redis, vet, build and eight HTTP acceptance
cases including signed delivery. The optional narrated showcase was skipped
in ordinary acceptance; the earlier load/release showcases are separate.
Ordinary race tests have no TEST_BASE_URL and therefore skip black-box HTTP
cases; the separate acceptance command is what exercises that built API.
Optional native-k6 CLI cases skip inside this Go-only verification container.

The verification stacks use synthetic credentials and isolated schemas/streams.
They do not truncate shared tables or stop the user's ordinary stack. Source
and evidence remain local changes; the older sanitized publication snapshot
does not yet contain this implementation.
