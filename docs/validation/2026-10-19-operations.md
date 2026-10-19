# Observability, retention and operations follow-through — 2026-10-19

Implemented Decisions 41–42: committed-capacity-deferral metrics with no
target/event labels, congestion and retention dashboard panels, opt-in
terminal-history cleanup and explicit audit/idempotency expiry semantics.
Cleanup is disabled by default and requires age >=24h. It uses locked parent
batches, three bulk child/parent deletes in one transaction and a five-second
background deadline; pending/DLQ/leased/unpublished work is excluded. A caller
key racing with purge retries its creation transaction once. Target records,
Redis history and DLQ remain outside automatic cleanup.

New tests cover metric exposition/counting, retention configuration, disabled
and canceled job behavior, actual enabled cleanup/shutdown, eligibility and
batch bounds, retained identity before cleanup and key reuse after it, row
locking, child-delete rollback and concurrent expiry/acceptance. Fixtures use
isolated schemas, not the user's data. The operational harness self-test
checks strict metric parsing; its live run checks actual admission recovery,
two instances, owner-loss recovery and a ten-minute capped soak. Exact results
and limitations are in the [operational report](../benchmarks/2026-10-19-operations/README.md).

An initial verification target-tests-a66bf12e ended with a vet import-resolution
error while test source was being edited. Its log is retained rather than
reported as a pass. Final verification target-tests-f6f5ad84 runs the frozen
source with real PostgreSQL/Redis in a disposable Linux stack:

```sh
go test -race -count=1 -timeout 3m ./... &&
go vet ./... && go build ./... && go run ./cmd/acceptance
```

Ordinary black-box HTTP tests skip without TEST_BASE_URL; the separate acceptance
command builds its own API. Optional native-k6 CLI tests skip inside this Go-only
container; actual k6 execution is evidenced by the operational rehearsal.
The frozen-source run completed successfully: full Linux race tests with
real PostgreSQL/Redis, vet/build and all eight HTTP acceptance cases including
signature verification. Logs are retained in the operational bundle. No
source files were edited during this final verification run.
No new cloud deployment, production deletion, human walkthrough or personal
interview rehearsal is implied by agent-run checks.
