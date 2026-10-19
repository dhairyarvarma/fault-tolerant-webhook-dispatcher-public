# Load testing

## Admission, two-process recovery and capped soak

```powershell
./scripts/verify-operations.ps1 -SelfTest
./scripts/verify-operations.ps1
```

PowerShell 7, Go, Docker Desktop and native k6 on PATH are required. The
rehearsal uses only a unique disposable project and synthetic credentials:
it tests admission while Redis is stopped, restart/drain/re-admission, two
API processes sharing state, survivor recovery after a known owner is killed,
and a 600s single-process cap-enabled soak (healthy 5/s, slow 3/s with 500ms
receiver delay). SoakSeconds accepts 120–1800. It retains raw k6/SQL/metric/
resource evidence under reports/generated/operations-* and removes only its
own test containers/network/volumes. Retention remains disabled. No production
data is purged. See the dated operational report for source provenance and
limitations; this does not test concurrent fresh migrations or prove an SLO.

## Mixed-target isolation experiment

From PowerShell 7 on Windows, with Go, Docker Desktop and native k6 on PATH:

```powershell
./scripts/measure-mixed-targets.ps1 -SelfTest
./scripts/measure-mixed-targets.ps1
# Repeat the same workloads with the opt-in process-local cap:
./scripts/measure-mixed-targets.ps1 -TargetConcurrency 2
```

The experiment creates a unique disposable four-service Compose project and
uses synthetic authenticated traffic. It does not read .env or truncate a
shared database. It compares a 30s healthy-only control (5/s), concurrent
healthy (5/s), slow HTTP 200 (10/s, 500ms), and failing HTTP 503 (2/s), then
another healthy-only control after disabling its own failed target. Shortened
retry/circuit settings and CPU/memory quotas are those of
deploy/compose.verification.yml, copied into each result directory.

Each target uses the existing benchmark runner with separate raw k6 output,
HTTP summary, metric samples and run-scoped SQL verification. Starts are
near-concurrent, not synchronized; inspect recorded load times for overlap.
The healthy/slow targets must drain successfully within 90s after load. The
failed target gets a 10s drain observation; pending/DLQ is expected, not a
delivery success. Setup/query/count/HTTP failures still fail the experiment.
The final SQL snapshot is later than these individual observations and may
show failing-target events CANCELED after the target was explicitly disabled.

`experiment.json` summarizes counts, success-only delivery p95, API p95,
workload timings, machine and image identities. `status=measured` means the
experiment completed with the documented outcome checks; it is not a pass
against a latency/fairness SLO. Read per-target results, not aggregate request
throughput. This overload test does not establish peak capacity or prove
isolation for every traffic mix. The default has no per-target cap;
`-TargetConcurrency 2` retains an override enabling the optional process-local
cap and records it in experiment.json. It is not a distributed fairness limit.

Artifacts stay under reports/generated/mixed-*; only this invocation's test
containers/network/volumes are removed. The harness retains failed-run
metadata and logs where available. Images, build cache and the benchmark
executable remain local. Review/redact personal paths before publishing raw
output. Run `-SelfTest` without Docker to test the expected-outcome rules.

## Single-target ingestion

Start the local stack, create a target, then run the ingestion scenario:

```powershell
$env:TARGET_ID = "<target UUID>"
k6 run .\load\k6\ingestion.js
```

Optional environment variables: `BASE_URL`, `API_KEY`, `RATE`, `DURATION`,
`PRE_ALLOCATED_VUS`, and `MAX_VUS`.

The scenario creates a distinct `Idempotency-Key` per iteration and measures
only API ingestion. Record the exact command, system configuration, and raw
output in `RESUME_EVIDENCE.md` before using any result as a claim.

## Evidence runner

Run from the repository root with native k6 installed. First build/deploy
the intended API commit and create a dedicated target for the run. Do not
truncate a shared database. Supply API_KEY in the environment when needed.
TEST_DATABASE_URL is optional and must point to the API's database/schema
to verify delivery; the runner never changes database rows.

```powershell
go run ./cmd/benchmark -validate-only -scenario soak -target-id <uuid>
go run ./cmd/benchmark -scenario ingestion -target-id <uuid> -rate 25 -duration 30s -api-commit <deployed-sha> -machine "API host and limits" -target-behavior "always 200, no latency"
go run ./cmd/benchmark -scenario ingestion -target-id <uuid> -rate 100 -duration 15s -api-commit <deployed-sha> -machine "API host and limits" -target-behavior "always 200, no latency"
go run ./cmd/benchmark -scenario ceiling -target-id <uuid> -api-commit <deployed-sha> -machine "API host and limits" -target-behavior "always 200, no latency"
go run ./cmd/benchmark -scenario soak -target-id <uuid> -duration 10m -api-commit <deployed-sha> -machine "API host and limits" -target-behavior "always 200, no latency"
```

Each executed run gets a new directory under docs/benchmarks containing:

- metadata.json: runner/API commit distinction, workload hash, reported API
  hardware/configuration, measured load-generator runtime, tool version,
  timestamps, effective VU settings, and completion status;
- script.js: the exact workload copy executed;
- k6-output.txt and k6-summary.json: raw console and machine-readable results;
- metrics.jsonl: timestamped API metrics sampled during load and draining;
- delivery.json when TEST_DATABASE_URL is set: counts and success-only
  end-to-end percentiles scoped by target and the unique run-key prefix.

The soak defaults to 10/s for 10m; ceiling uses CEILING_STAGES and records
per-stage submetrics. RATE/DURATION apply to ingestion/soak, not ceiling.
PRE_ALLOCATED_VUS and MAX_VUS override the documented script defaults.
Threshold failures return a nonzero status and retain the failed run's
artifacts. A capacity-search threshold failure is an observation, not by
itself a reliability defect. Read dropped iterations as well as HTTP errors.

With TEST_DATABASE_URL configured, exit zero additionally requires SQL's
run-scoped accepted count to match the named `event accepted` check in the
k6 summary, with every event SUCCEEDED. SQL/setup/summary/write errors,
DLQ/canceled outcomes, count mismatches and drain timeouts fail the command.
The last available delivery snapshot and final metadata remain available.
`-drain-wait 0s` takes one bounded snapshot; positive drain budgets include
query time and are cancellable. Use the API's primary database and schema.
Deliberately failing receivers can produce an expected nonzero result;
inspect outcome counts rather than treating that as a product defect.

Without TEST_DATABASE_URL, exit zero means ingestion-only: metadata status is
`ingestion_only` and delivery_verification is `not_configured`, not verified
delivery. With SQL enabled, successful metadata is `completed`/`verified`;
verification failures are `verification_failed` with a specific reason code.
HTTP failures retain status `load_failed` even if SQL verification also fails;
the separate delivery_verification field records that result. Never remove
the named acceptance check from workload scripts without updating the runner.

Validation-only performs no requests, writes no result directory, and needs
no k6 binary. It is not a benchmark. Reported API hardware/configuration is
operator-supplied, not remotely detected. Resource CPU/RSS sampling and fault
injection still require external tooling; retain those samples alongside
the run. Never use success-only latency without reporting pending/DLQ counts.
