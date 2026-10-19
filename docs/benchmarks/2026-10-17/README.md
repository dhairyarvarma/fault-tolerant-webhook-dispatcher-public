Public derivative: personal identifiers are redacted and hashes regenerated. Historical commit IDs refer to private development history. See [provenance](../../../PUBLIC_RELEASE.md).

# Current implementation: release rehearsal and measured limits

## Outcome

The functional release rehearsal passes from a clean local clone at
a6bc0042b192db95322914365a16f30a6b3bb566. That includes container HTTP acceptance,
the narrated showcase, a real Prometheus scrape, Grafana dashboard provisioning,
backup/restore, post-restore delivery using an encrypted secret, Redis restart,
and recovery after an observed delivery claim followed by SIGKILL.

All 4,150 accepted events in the three main load runs eventually succeeded;
final DLQ, canceled, and pending counts are zero. This is finite-run evidence,
not a general zero-loss guarantee. Acceptance/dropped-arrival thresholds pass,
but delivery backlog grows at the tested rates. No latency SLO was configured.
The results do **not** establish sustainable low-latency delivery at 25/s.

## Conditions and reproduction

- Windows build 26200; i9-13900H, 14 physical/20 logical cores as reported by
  the OS; Docker Desktop 29.8.0 Linux VM with 20 reported CPUs and
  8,133,451,776 bytes of memory. Host and load generator are shared.
- Go 1.27.2; native k6 v1.8.2; 50 preallocated/100 maximum VUs.
- API: 2 CPU/512 MiB; PostgreSQL: 1 CPU/512 MiB; Redis: 0.5 CPU/128 MiB;
  simulator: 1 CPU/128 MiB. Prometheus and Grafana also run on the same VM.
- Four workers, 100ms relay/scheduler polling, three attempts, 200–400ms
  backoff cap and 5s circuit cooldown. These are explicit rehearsal settings,
  not the default production configuration.
- One target per scenario. Exact JSON workload bodies, scripts, tool metadata,
  timestamps, image IDs, and SQL delivery summaries are retained below.
- Application code is unchanged from the patched implementation at 1b4867a;
  intervening commits add rehearsal tooling and documentation. The clean
  clone has no developer .env; normal application containers are untouched.

From PowerShell 7 on Windows, with Docker Desktop, Go and native k6 on PATH:

```powershell
./scripts/verify-release.ps1 -Load
```

The script creates a unique synthetic-data project and random loopback ports,
retains reports/generated/<run>, then removes only its own containers/volumes.
It reads deploy/verification.env rather than the developer's .env. Docker
images/cache remain. This verifies a fresh checkout, not a cache-free machine
or another student's independent experience.

## Main load results

API durations are client-observed milliseconds. Delivery durations are seconds
from durable creation to successful attempt completion; they include queueing.
The SQL summaries are success-only percentiles, alongside all outcome counts.

| Workload | Scheduled rate / duration | Accepted / succeeded | API p50 / p95 / p99 (ms) | Delivery p50 / p95 / p99 (s) | Peak durable pending |
|---|---|---|---|---|---|
| Always-200 receiver | 25/s / 30s | 750 / 750 | 146.98 / 436.30 / 578.73 | 21.64 / 22.82 / 22.91 | 551 |
| Always-200 sustained | 25/s / 120s | 3,000 / 3,000 | 161.03 / 475.29 / 946.01 | 92.93 / 95.70 / 95.82 | 2,394 |
| Receiver sleeps 500ms | 20/s / 20s | 400 / 400 | 1,093.19 / 2,047.08 / 2,342.56 | 41.93 / 57.25 / 58.56 | 400 |

All three main runs have zero failed HTTP requests and zero dropped iterations.
Post-load draining took approximately 20.1s, 94.0s and 59.1s respectively
(including SQL/polling overhead). The 120s scenario is a two-minute sustained
check, **not** a ten-minute soak, capacity ceiling, or long-term reliability test.

## First demonstrated bottleneck

During the sustained run and drain, PostgreSQL averaged about 100.1% on Docker's
per-core CPU scale across 50 resource samples, against its one-CPU quota. The
API averaged about 35.2%; peak sampled container memory was 13.62 MiB for the
API and 93.15 MiB for PostgreSQL. These are container-memory readings, not RSS.
Sampling is discrete and can miss peaks; CPU readings can briefly exceed
quota-normalized expectations.

The timestamped [database snapshot](sustained-pg-activity.json) shows WALSync,
WALWrite and row/transaction lock waits around circuit-state operations.
Ingestion holds a shared lock on the target to serialize against disabling;
circuit permissions/outcomes lock that same target row. This is a plausible
hot-target contention path, not proof that one query alone explains all delay.

The combined evidence points first to database-side CPU/durable-write and
target-row coordination pressure in this constrained single-target setup,
before adding another broker or service. A controlled follow-up should vary
only PostgreSQL CPU/storage, inspect more wait samples/query profiles, and
compare multiple targets. Any lock/transaction optimization must preserve
disabled-target semantics and circuit-generation fencing.

The slow receiver adds a separate bound: four workers and a 500ms response
give an idealized ceiling of 8 deliveries/s before database/network overhead.
20 arrivals/s therefore cannot be sustained indefinitely. All four delivery
invocations were observed in flight in that run. Adding workers is not evidence
of a fix for the database bottleneck; it needs a separate measured experiment.

## Recovery and restore

- Redis restart command to durable success: 3.82s in the clean run.
- SIGKILL command after observing a database delivery claim, through restart
  to durable success: 61.67s in the clean run.
- A preceding run observed 3.87s and 61.65s respectively. These are two local
  observations, not statistical percentiles, an SLA, or isolated network time.
- Worker timing includes Docker commands and HTTP polling. It proves recovery
  of an abandoned claim; it does not hit an exact instruction after receiver
  success. The [case-study regression](../../case-study-delivery-crash.md)
  deliberately tests that separate boundary.
- pg_dump/pg_restore into a new database preserved fingerprints of targets,
  events, attempts, outbox and migration history. A restored API read prior
  success and delivered a new event with the matching encryption key.

## Failed run retained, not hidden

[baseline-10-vus](baseline-10-vus/release.json) records a preceding 25/s/30s run
with 10 initial VUs: 745 requests, zero HTTP failures, but six dropped scheduled
iterations and a failing threshold. All 745 accepted events eventually
succeeded; delivery p95 was 24.84s. Increasing initial VUs to 50 removed the
observed allocation-related drops in the main runs, but did not remove the
server-side backlog. This is not a code-performance comparison between the runs.

An earlier harness restart probe polled an obsolete random host port. Docker
had reassigned it; the API was healthy. The harness now rediscovers ports after
restart and cleans up the restore-profile container as well. That failed
rehearsal remains under reports/generated in the working environment.

## Evidence map

[sha256.json](sha256.json) records byte hashes for this bundle, excluding the
manifest itself. Git attributes disable line-ending conversion for the bundle.

- [Release checks, hardware, image IDs and recovery observations](fresh-50-vus/release.json)
- [Raw HTTP acceptance and showcase output](fresh-50-vus/http-acceptance.txt)
- [Container resource samples](fresh-50-vus/resources.jsonl), [derived analysis](analysis.json)
- [30s ingestion](fresh-50-vus/load/20261017T085645.791495100Z/metadata.json)
- [120s sustained](fresh-50-vus/load/20261017T085736.664279800Z/metadata.json)
- [500ms receiver](fresh-50-vus/load/20261017T090111.491010100Z/metadata.json)
- Each load directory contains script.js, k6-output.txt, k6-summary.json,
  metrics.jsonl and delivery.json. Unedited container logs and rehearsal
  transcript are retained alongside them.
- [Readable short demo](demo/showcase.txt) and [timed terminal demo](demo/showcase.cast)

Linux race/vulnerability verification is recorded separately in
[the security record](../../validation/2026-10-17-access-and-security.md).
Grafana provisioning/scraping checks do not prove every panel was visually
reviewed. No cloud/TLS deployment, hosted CI run, OS-package vulnerability
scan, exhaustive recovery benchmark, independent student walkthrough, or
personal interview rehearsal is claimed.
