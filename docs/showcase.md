# Five-minute project showcase

This is a presentation route through existing evidence, not a claim that a
new video or a human interview rehearsal has been completed.

## Watch or inspect the behavior

The [terminal recording](benchmarks/2026-10-17-pool/demo/showcase.cast) and
[readable transcript](benchmarks/2026-10-17-pool/demo/showcase.txt) show a real
isolated API, PostgreSQL and Redis. The recording is asciicast v2, not MP4;
GitHub's file viewer does not provide a terminal player. The transcript needs
no player. Demo retry/circuit timings are deliberately shortened.

## Record a narrated screen demo

Use synthetic test data only. Close notifications and unrelated windows; do
not show .env, shell history, credentials, or your personal filesystem paths.
Rehearse first. Follow the README's test dependency setup, then run
`go run ./cmd/acceptance -demo`. The command builds its own isolated server;
it removes its temporary schema and stream afterward. PowerShell users can
also run `./scripts/record-demo.ps1` for an output-only terminal recording.

Use this five-minute sequence, pausing on the relevant output:

| Time | Show | Explain |
|---|---|---|
| 0:00–0:40 | README architecture | PostgreSQL owns durable work; Redis is transport. Acceptance is not delivery. |
| 0:40–1:20 | Showcase steps 1–2 | Event succeeds; duplicate creation returns 409 and the original event ID. |
| 1:20–2:10 | Showcase steps 3–6 | Three HTTP 500 attempts lead to DLQ; open circuit defers work without attempts; probe recovery and replay retain history. |
| 2:10–3:10 | Recovery evidence below | Actual SIGKILL recovery is distinct from the exact HTTP-success/commit-failure regression. |
| 3:10–4:15 | Pool experiment | Diagnose connection churn, explain the bounded warm pool, show the paired results and limitations. |
| 4:15–5:00 | Code and honest limits | Trace CreateWithOutbox to worker completion. Explain at-least-once, receiver deduplication and why no extra broker was needed. |

The short showcase does not kill a worker. For that segment, clearly label
the [retained release record](benchmarks/2026-10-17-pool/runtime-patched/release.json)
as a previous run: the observed-claim SIGKILL check recovered to success in
61.625 seconds, including restart and polling. Redis restart recovery was
4.247 seconds in that run. Do not present those as live output or SLAs.
To produce a new live recovery run, use `./scripts/verify-release.ps1` from
PowerShell 7 with Go and Docker available. Allow several minutes, including
image builds and lease expiry; it is longer than the short HTTP showcase.

For the harder crash window, show the
[case study](case-study-delivery-crash.md): the regression deliberately loses
the database after receiver success and verifies two HTTP calls with one
committed attempt and a stable key. A random process kill does not prove
that exact boundary.

## One engineering improvement to defend

Use the [connection-pool experiment](benchmarks/2026-10-17-pool/README.md).
The old pool kept two idle connections and had no open cap. A 25/s, 30-second
baseline opened 905 database sessions across load and drain; the paired run
with 16 idle and 32 maximum open connections observed 20. Session counts
include probes and operator/collector connections. This was a sequential
shared-laptop experiment, not a randomized causal trial or capacity ceiling.

The ten-minute local soak accepted and delivered 15,000 events at 25/s,
with no failed HTTP requests, dropped arrivals, or final unfinished events.
Four workers still cannot sustain 20/s against a 500ms receiver: the ideal
service-rate bound is only 8/s. The fix removes connection churn, not that
receiver-latency bottleneck. Original machine/configuration and raw results
remain linked in the report; do not relabel them as measurements of a new run.

## Resume: choose two bullets

- Built a Go webhook dispatcher using PostgreSQL transactional outbox and
  Redis Streams, with jittered retries, circuit breaking, DLQ/replay and
  token-fenced delivery leases; regression-tested duplicate work and recovery.
- Diagnosed PostgreSQL connection churn and reduced observed database session
  creation from 905 to 20 in paired local load runs; validated 15,000 deliveries
  in a separate ten-minute local soak at 25 events/s with retained raw evidence.
- Hardened webhook delivery with registration/dial-time SSRF checks,
  encrypted target secrets and timestamped HMAC signatures; verified Linux
  race tests and authenticated HTTP acceptance in GitHub Actions.

Use these only if you can explain and modify the relevant code. Link the repo
and eventual video from your resume; a private repo is not anonymously visible.
Do not claim production deployment, exactly-once effects, peak throughput,
or that assistant-prepared material proves your personal understanding.

## Personal rehearsal

Complete the [ownership worksheet](ownership-rehearsal.md) without assistance.
Have a friend ask: where is the event/outbox commit; when is Redis acknowledged;
what survives Redis loss; why can HTTP repeat; what fences a late worker;
what does the pool cap cost across replicas? Add and run the proposed HTTP 409
classifier regression live in a disposable checkout. Do not mark that task
complete just because the existing suite passes.
