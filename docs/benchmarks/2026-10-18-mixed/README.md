Public derivative: personal identifiers are redacted and hashes regenerated. Historical commit IDs refer to private development history. See [provenance](../../../PUBLIC_RELEASE.md).

# Mixed-target overload isolation experiment — 2026-10-18

## Question

Does a slow or failing target delay otherwise healthy deliveries sharing four
workers? Can a small opt-in per-process cap improve healthy latency without
changing durable delivery, ownership, acknowledgement or retry budgets?

## Workload and provenance

Each experiment runs a 30s healthy-only control at 5 arrivals/s, then concurrent
healthy HTTP 200 (5/s), slow HTTP 200 (10/s with 500ms delay), and HTTP 503
(2/s), followed by a 30s healthy-only control. The failing target is disabled
before the last control; all slow/healthy work has drained first. There is
about 30.18s/30.13s of overlap in the runner-recorded load-process intervals.
These timestamps include k6 startup/teardown, not exact first/last arrivals.

The slow receiver's offered demand alone is five worker-seconds per second,
above four-worker capacity. This is intentional overload, not a normal-load
throughput test. Each stream uses 20 preallocated/50 maximum k6 VUs and its
own target/run-key SQL verification. Payload/workload source is retained
beside every run. Boundary scheduling produces 150/151 or 300/301 events;
reported counts are observed, not rounded rate-times-duration assumptions.

Host: shared i9-13900H Windows machine, Docker Desktop Linux VM with 20 reported
CPUs and 8,133,451,776 bytes memory, Go 1.27.2, k6 1.8.2. Rehearsal quotas:
API 2 CPU/512MiB, PostgreSQL 1 CPU/512MiB, Redis 0.5 CPU/128MiB, simulator
1 CPU/128MiB. Three attempts, 200–400ms retry bounds, threshold 3 and 5s
circuit cooldown are shortened test settings, not production defaults.
Exact configuration, versions and image identities are in each experiment.json
and compose.yml. No additional CPU/memory utilization sampler was used.

The uncapped run uses application commit 06887da with documentation/harness
changes only; Go/application configuration matched that commit. The capped
run adds the uncommitted Decision 40 implementation, enabling a limit of two
with target-cap.yml. Both report the original HEAD plus dirty=true; neither
should be described as a pristine measured release commit. application.patch
retains the runtime change; capped-source-sha256.json identifies final Go
source files including tests. The additional regression test was refined
after the capped image was built, without changing runtime code. These
results do not belong to the earlier sanitized public snapshot.

## Results

| Case | Target / phase | Accepted / succeeded | API p95 ms | Successful delivery p95 s |
|---|---|---|---|---|
| Uncapped | Healthy before | 151 / 151 | 17.67 | 0.094 |
| Uncapped | Healthy mixed | 151 / 151 | 16.55 | 9.965 |
| Uncapped | Slow mixed | 301 / 301 | 14.89 | 10.413 |
| Uncapped | Healthy after | 151 / 151 | 17.26 | 0.087 |
| Cap 2/process | Healthy before | 151 / 151 | 37.33 | 0.074 |
| Cap 2/process | Healthy mixed | 151 / 151 | 13.16 | 0.265 |
| Cap 2/process | Slow mixed | 300 / 300 | 16.80 | 65.460 |
| Cap 2/process | Healthy after | 151 / 151 | 36.34 | 0.101 |

All workloads, including the deliberately failing receiver's ingestion, have
zero HTTP request failures and dropped arrivals. Run-scoped SQL counts match
the named k6 acceptance checks. All healthy/slow events eventually succeeded,
with zero final pending/DLQ/canceled events for those workloads. Success
latency measures durable acceptance to the committed successful attempt's
finished_at, including queueing. It is not API latency or HTTP service time.

The HTTP-503 target accepted 60 events in each mixed phase. Its bounded
post-load snapshot showed 1 DLQ/59 pending uncapped and 2 DLQ/58 pending
capped, with zero success. Both runners correctly exited nonzero with
delivery_verification=drain_timeout. Those are expected failure observations,
not verified delivery or discarded errors. The later final SQL snapshots,
after explicit target disable and the last control, show 1 DLQ/59 canceled
and 5 DLQ/55 canceled respectively. Different snapshot times explain the
different outcomes; do not mix them into successful-delivery totals.

The slow runner's one-second metric samples observed peak durable pending
172 uncapped versus 253 capped; peak unread Redis work was 166 versus 30,
and peak sampled HTTP in-flight was 4 versus 3. These are sampled maxima,
not continuous peaks. The cap moves pressure into durable deferred work:
a lower unread queue is not proof of a smaller overall backlog.

## Interpretation and tradeoff

The shared worker pool allows slow successful HTTP to delay healthy traffic;
the target circuit breaker does not address slow successes. Healthy API
ingestion stayed fast even while end-to-end delivery approached ten seconds.
With two slots per target, healthy mixed p95 was 265ms instead of 9.97s in
these runs. The price is material: slow-target p95 rose from 10.41s to 65.46s
and durable pending work increased. Its theoretical receiver service limit
falls from roughly eight to four calls/s, below the offered ten arrivals/s.

This is workload isolation, not increased overall capacity or a universal
speedup. Deferral adds SQL writes, scheduling and outbox publications; that
cost was not separately quantified here. The setting stays disabled by
default. It is process-local, not a distributed cap, FIFO guarantee or SLO;
N replicas can allow N times the configured target concurrency.

These are sequential shared-machine runs, not repeated randomized trials.
The healthy-only API p95 variation is visible in the table. There is no
slow-only or failing-only factorial control, and three concurrent collectors
scrape metrics during mixed traffic. Do not attribute every difference to
one component, claim statistical significance, or extrapolate peak capacity.
Longer repeated runs and multiple-replica tests remain future work.

## Reproduce and inspect

```powershell
./scripts/measure-mixed-targets.ps1 -SelfTest
./scripts/measure-mixed-targets.ps1
./scripts/measure-mixed-targets.ps1 -TargetConcurrency 2
```

PowerShell 7, Go, Docker Desktop and native k6 are required. These commands
use unique disposable projects, random loopback ports and synthetic data;
they do not read .env, truncate shared tables, or stop the normal application.
Both measured stacks were removed after collecting evidence. Only their
own test containers/network/volumes were removed; images/build cache remain.

uncapped/ and capped/ retain each experiment's report, SQL outcome snapshots,
configuration, image IDs, console output, k6 summaries, executed workload
copies and timestamped metric samples. Capped additionally retains the exact
harness version executed; the root harness reproduces either setting. The
baseline predates addition of the override parameter and uses the original
base Compose configuration. No generated executable is included.

Personal home-directory components in retained text are replaced with
PUBLIC_USER. These are privacy-redacted derivatives, not untouched original
bytes; original reports remain locally in reports/generated under the run
IDs in experiment.json. Measurements and timestamps are unchanged. The
sha256.json manifest covers these retained bytes, not the original reports.

## Correctness verification

linux-verification.txt retains the successful full Linux race suite with
real PostgreSQL/Redis, vet, build and eight HTTP acceptance cases (including
signature verification). linux-initial-artifact-failure.txt retains the first
failed attempt: the archived test file was mistakenly named .go and discovered
as an unintended package. It is now .go.txt. Actual application packages passed
in that first run too; the complete second run exited zero. See the
[verification record](../../validation/2026-10-18-target-isolation.md) for test
scope/skips. verify-target-cap.ps1.txt retains the verification procedure with
the home-directory component redacted. It is a record, not a portable script.
