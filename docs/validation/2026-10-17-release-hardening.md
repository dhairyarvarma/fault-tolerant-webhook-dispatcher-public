# Release-hardening verification — 2026-10-17

Working tree based on d764da3, verified with Go 1.27.0 on Windows amd64,
using real localhost PostgreSQL and Redis. Database fixtures use disposable
schemas, not application tables. Recovery time is advanced explicitly in
fixtures; these are deterministic correctness tests, not recovery benchmarks.

Commands executed successfully:

```powershell
# TEST_DATABASE_URL and TEST_REDIS_URL configured; TEST_BASE_URL unset.
go test -count=1 ./...
go vet ./...
go build -buildvcs=false ./...
go run ./cmd/acceptance
go mod verify
```

All Go packages passed; module verification reported `all modules verified`.
The direct package run skips black-box tests without TEST_BASE_URL. The
acceptance command separately builds the application, starts it on a temporary
local port with auth enabled, and runs the HTTP tests against real dependencies
and a local target simulator. It removes only its own schemas/streams/process.

Captured acceptance test outcomes from the final run:

```text
--- PASS: TestE2E_DeliveryLifecycle (0.31s)
--- PASS: TestE2E_IdempotencyConflict (0.04s)
--- PASS: TestE2E_ReplayNonDLQIsConflict (0.30s)
--- PASS: TestE2E_DLQReplayRetainsHistory (0.58s)
--- PASS: TestE2E_DisabledTargetRejectsNewEvents (0.02s)
--- PASS: TestE2E_AuthenticationAndHealth (0.01s)
--- PASS: TestE2E_ConcurrentIdempotencyCreatesOneLogicalEvent (0.09s)
--- PASS: TestE2E_SignedDeliveryPassesReceiverVerification (0.28s)
PASS
```

Additional integration regressions exercise expiring delivery ownership,
stale completion/deferral fencing, bounded stale-work reconciliation, Redis
stream loss and live consumer-group recreation, reclaim scan pagination,
concurrent duplicate messages, expired circuit probes and late generations,
atomic admission at capacity, replay ordering, and disabled/future work.
Unit tests cover signatures/tampering, request bounds, malformed IDs,
configuration rejection, overflow-safe backoff, and histogram snapshot consistency.

The crash boundaries are represented by committed durable state and expired
claims, not by timing a process kill inside a microsecond window. Existing
historical throughput and recovery numbers are not remeasured by this run.

Limits: no supported local 64-bit cgo compiler; WSL enumeration is denied.
Docker engine access is denied, so the new runtime image has not been built
or deployed here. Compose syntax and Grafana dashboard JSON parse locally,
but updated live scraping/dashboard behavior still needs verification.
No native k6 or govulncheck is available. No hosted CI run, final load test,
cloud deployment, or TLS/backup-restore execution is claimed by this record.
