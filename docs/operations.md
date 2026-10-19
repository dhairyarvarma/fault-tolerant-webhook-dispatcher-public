# Operations and release checklist

The isolated local release rehearsal exercises container startup and backup
restore; evidence is linked below. Production VM/TLS deployment is still a
prepared procedure, not an executed claim. Use a dedicated test stack before
applying these procedures to real event data.

## Install and verify locally

1. Configure .env from .env.example with a generated 32-byte encryption key.
2. Run docker compose config --quiet, then docker compose up -d --build.
3. Verify /healthz, /readyz, Prometheus up, and the provisioned dashboard.
4. Run go test ./... with TEST_DATABASE_URL/TEST_REDIS_URL configured, then
   go run ./cmd/acceptance. The latter creates an isolated authenticated API.
5. Run go test -race ./... on a supported 64-bit cgo toolchain and retain
   the log/CI URL. Run the dependency vulnerability checker and record its
   version/database timestamp and results before release.

Default published Compose ports now bind only to 127.0.0.1. From another
machine, use an authorized tunnel/reverse proxy instead of exposing the
database, Redis, simulator, Prometheus, or Grafana directly.

## Production configuration

History cleanup is OFF by default. HISTORY_RETENTION_AGE=720h opts into a
30-day minimum audit/idempotency window; enabled age must be >=24h. Set
HISTORY_RETENTION_INTERVAL (default 1h, minimum 1s) and
HISTORY_RETENTION_BATCH (default 100, range 1..1000). Cleanup waits one interval
after startup and runs one five-second, bounded-parent batch per interval.
Only old SUCCEEDED/CANCELED events without leases/unpublished work are deleted,
atomically with their attempts and published outbox history. DLQ/targets stay.
Deleting event rows ends ingestion deduplication and makes old GETs return 404.
Back up and confirm replay/audit obligations before enabling; a smaller batch
may be needed for large per-event histories. This does not prune Redis or DLQ
history, nor immediately return PostgreSQL disk space to the OS. No cleanup
is automatically enabled by deployment examples or these rehearsals. Budget
batch/interval throughput above the terminal-event arrival rate; defaults are
conservative, not a capacity guarantee. Batches never overlap within one process;
a slow batch can coalesce timer ticks. Cleanup uses three bulk deletes per batch.

Optional WORKER_TARGET_CONCURRENCY defaults to 0 (disabled). A value such as
2 with WORKER_COUNT=4 limits one target's active processing in this process.
Saturated target events enter RETRY_WAIT for 1–2 jittered seconds without
an HTTP attempt; the scheduler/outbox recover them. This improves isolation
at the cost of more database/publication work and a longer hot-target drain.
It is not a cluster-wide cap or strict fairness guarantee: replica budgets
add together. Configure through .env and recreate the API to apply it.

- Set a strong API_KEY, preserve SECRET_ENCRYPTION_KEY securely, and use
  production environment/auth with private destinations disabled.
- Keep clocks synchronized; HMAC timestamp verification needs a tolerance.
- Keep dependency credentials in the host/provider's secret mechanism.
- The overlay requires API_KEY, SECRET_ENCRYPTION_KEY, DATABASE_URL, and
  POSTGRES_PASSWORD. DATABASE_URL must match the actual database credentials.
  Changing POSTGRES_PASSWORD in Compose does not rotate an existing database
  role on a persisted volume; plan and test rotation separately. If using a
  managed database, omit the bundled database service from the deployment.
- Change Grafana's initial administrator password and keep dependency/admin
  ports private; use authenticated tunnels for operator access.
- Pin WEBHOOK_IMAGE to the tested SHA-tagged image. Do not assume a tag
  was published merely because the workflow exists.
- For a security refresh, rebuild with docker compose build --pull --no-cache
  and scan the resulting image. Normal cached builds do not refresh installed
  packages merely because an upstream package repository changed. Retain
  image IDs, scanner/database versions and findings, not just an exit code.
- Review max pending work, workers, request/backoff/circuit timeouts,
  resource limits, storage retention, and capacity from measured evidence.
- Each store.Open pool permits 32 open/16 idle PostgreSQL connections.
  Budget all API replicas and administrative/benchmark clients together;
  leave headroom below PostgreSQL max_connections. This is not a global cap.
- Back up before migrations. Database migrations are forward-only; an
  application image rollback is safe only if schema compatibility was tested.

Prepared overlay (does not deploy anything by itself):

```bash
export WEBHOOK_IMAGE=ghcr.io/your-org/your-repo:tested-commit-sha
docker compose -f docker-compose.yml -f deploy/compose.production.yml config --quiet
docker compose -f docker-compose.yml -f deploy/compose.production.yml up -d --no-build
```

For public TLS, replace your-domain.example in deploy/Caddyfile, point a
real domain at the host, then add --profile tls. The proxy hides /metrics
and /readyz from public traffic. Verify certificate issuance/renewal and
auth from a separate machine before sending real traffic. TLS is untested
until that verification is recorded.

## Backup and restore rehearsal

Use Bash for the binary redirection below; older Windows PowerShell can
transcode native binary output. Store backups outside Git, with restrictive
permissions and encryption/access control appropriate to event payloads.
Back up SECRET_ENCRYPTION_KEY separately: database ciphertext is unusable
without the matching key. Do not rotate/discard it without a migration plan.

```bash
umask 077
mkdir -p backups
docker compose exec -T postgres pg_dump -U webhook -d webhook -Fc > backups/webhook.dump
```

Restore only into a new, uniquely named rehearsal database, never over the
live database. Substitute a name you have verified does not already exist:

```bash
docker compose exec -T postgres createdb -U webhook webhook_restore_run123
docker compose exec -T postgres pg_restore -U webhook -d webhook_restore_run123 < backups/webhook.dump
docker compose exec -T postgres psql -U webhook -d webhook_restore_run123 -c 'SELECT count(*) FROM events; SELECT count(*) FROM delivery_attempts; SELECT count(*) FROM outbox_messages;'
```

Compare IDs, statuses, attempt history, and table counts with the captured
source snapshot. Start a separate API against the restored database with
its own Redis stream and the matching encryption key. Confirm reads and
new delivery before marking backup/restore verified. Remove only the
specifically named rehearsal resources after retaining the evidence.

## Restart, lost transport, and rollback

For a dedicated test stack, create a target/event, retain its ID and attempt
history, restart api, and verify old history plus a new delivery. An in-flight
crash can repeat HTTP with the same idempotency key. A full Redis stream loss
is recovered through stale-work reconciliation after its grace period;
future RETRY_WAIT events must retain their deadline. Never use FLUSHALL on
a shared Redis instance to test this; the regression suite deletes only
its dedicated test stream.

For rollback, first verify schema compatibility and preserve a backup. Pin
WEBHOOK_IMAGE to the prior tested SHA, run the same overlay with --no-build,
then verify readiness, preserved event history, and new delivery. If the
schema is incompatible, perform a planned restore/migration in a separate
database rather than blindly rolling the binary backward.

## Retention and capacity limits

History accumulates unless the opt-in retention job is enabled. Even then,
DLQ, targets and Redis stream history are retained; cleanup must keep pace
with terminal-event creation. Stable process memory during a soak does not
prove bounded database storage. Keep demo payloads synthetic and define a
retention/privacy policy before accepting real data.

Monitor pg_database_size(current_database()), table sizes via
pg_total_relation_size, durable pending work, and disk free space. Estimate
growth from accepted events and attempts at the actual retry mix, not only
HTTP request rate. Admission control bounds unfinished work, not history.

Do not deploy an unreviewed DELETE/cron cleanup. The built-in job has explicit
eligibility, locking and rollback regressions, but operators must first
rehearse backup/restore and agree on the audit/idempotency window. No retention
job has been enabled on the user's existing data or a production deployment.

## Remaining release evidence

The native correctness/HTTP suite and patched image build are verified.
The Go vulnerability rescan reports no findings after the documented updates.
See docs/validation/2026-10-17-access-and-security.md for race/tool evidence.
The isolated fresh-checkout rehearsal now verifies live Prometheus scraping,
Grafana provisioning, backup/restore plus encrypted-secret delivery, and
Redis/worker recovery. Three current load runs and resource samples are retained
in docs/benchmarks/2026-10-17/README.md, including the observed delivery backlog.
The pool follow-up in docs/benchmarks/2026-10-17-pool/README.md retains a
ten-minute, 15,000-event soak, a same-schema local image rollback, and all
15 dashboard query responses. Hosted tests/race/HTTP acceptance and SHA-tagged
image publishing now pass; see validation/2026-10-18-github-publication.md.
Broader capacity/failure envelopes, an independent
human walkthrough, and any cloud/TLS deployment remain separate
checks. Query/provisioning checks do not prove visual review of dashboard layout.
