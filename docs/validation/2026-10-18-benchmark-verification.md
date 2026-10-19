# Benchmark delivery verification — 2026-10-18

## Problem and scope

The runner previously returned k6's exit status even when its SQL query failed
or its drain deadline expired. It could also treat an empty/wrong schema or
DLQ/canceled outcomes as successful completion. This changes only the benchmark
and rehearsal tooling, not the HTTP API, database schema or worker protocol.
Decision 39 and load/README.md define the revised exit/metadata contract.

Configured verification now compares SQL accepted events with the named
`event accepted` check in k6's summary, requires every accepted event to
succeed, and propagates verification failures after retaining final metadata.
The verifier bounds queries, honors cancellation and includes query time in
positive drain budgets. A zero budget means one bounded snapshot. Diagnostic
database errors are not echoed because they may contain credentials.
Runs without TEST_DATABASE_URL remain supported as explicit ingestion_only
results; they do not claim verified delivery.

## Verification

Executed on Windows with Go 1.27.2 and native k6 from .tmp/tools, against real
local PostgreSQL/Redis. Repository fixtures create disposable isolated schemas;
the running application's rows are not modified.

```powershell
go test -count=1 -v ./cmd/benchmark
go test -count=1 -timeout 2m ./...
go vet ./...
go build -buildvcs=false ./...
```

The verifier unit cases cover success, unfinished work, DLQ, cancellation,
missing/partial counts, SQL and artifact errors, malformed acceptance summaries,
query deadlines, eventual success and cancellation during draining. The real
PostgreSQL summary test also exercises verified, terminal-failure and missing-
run results through the verifier.

TestBenchmarkCLI builds and executes the actual benchmark binary with native
k6, using a tiny HTTP fixture and isolated SQL rows. It verifies success,
drain timeout, DLQ, missing durable rows, SQL query errors, malformed database
configuration/redaction, ingestion-only mode and HTTP failure. Checks include
process status, final metadata and whether the last SQL snapshot was retained.
The fixture records controlled SQL outcomes itself: these are runner regression
tests, not real worker delivery tests, throughput measurements or new resume
numbers. This test skips if native k6 is absent; ordinary verifier tests still run.

Raw outputs are retained locally under reports/generated:
2026-10-18-benchmark-cli-tests.txt, 2026-10-18-benchmark-tests.txt and
2026-10-18-native-tests.txt. PowerShell parser and git diff --check also pass.
All earlier benchmark bundle bytes remain unchanged.

## Remaining checks and connection state

The Docker engine is denied in the current sandbox, so no new Linux race or
full container rehearsal is claimed for this change. Earlier race/container
evidence belongs to its recorded implementation. Metrics-sample read/write
errors remain outside this delivery-verification fix; sampling is not a complete
telemetry durability guarantee.

GitHub installation is confirmed after the user completed setup, but GitHub
operations are not available in the current chat and no gh executable is
installed. No repository has been created or pushed, and hosted CI remains
unverified. Do not mistake plugin installation for a successful repository
creation or CI run. Cloud deployment and human ownership rehearsal remain
separate tasks.

Publication follow-up: the later
[GitHub publication record](2026-10-18-github-publication.md) supersedes the
missing-remote/hosted-CI blocker above. The state described here was recorded
before that browser login and publication; benchmark results are unchanged.
