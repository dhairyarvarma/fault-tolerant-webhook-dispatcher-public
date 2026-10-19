# Deployment Plan

Current prepared operations and production overlay are documented in
docs/operations.md and deploy/compose.production.yml. Default published ports
bind to loopback. The older execution records below predate release hardening;
rebuild/rehearse the current version before claiming deployment validation.

## Stage 1 — Local Docker Compose

Run:

- API
- PostgreSQL
- Redis
- Prometheus
- Grafana
- target simulator

This is the primary development environment.

**Verified 2026-10-17** by actually running `docker compose up -d` from a
stopped state (no prior containers) and confirming all six services reach a
healthy state:

```bash
cp .env.example .env
# edit .env: set SECRET_ENCRYPTION_KEY (openssl rand -base64 32)
docker compose up -d
```

Two real bugs were found and fixed during this first genuine full-stack
run (previously each service had only ever been started individually, never
all six together) - see `DECISIONS.md`:

- `target-simulator` and `prometheus` both defaulted to host port 9090;
  Docker silently dropped Prometheus's port publish rather than erroring,
  so Prometheus was running but unreachable from the host. Fixed by
  moving Prometheus's host-side default to 9091 (`PROMETHEUS_PORT`); the
  container-to-container `prometheus:9090` hostname Grafana's datasource
  uses is unaffected.
- `deploy/grafana/provisioning/datasources/prometheus.yml` had a literal
  tab character on one indentation level, which is invalid YAML; Grafana
  refused to start at all. Fixed by replacing it with spaces.

After the fix, a clean `docker compose up -d` brings up all six services
with no manual intervention; confirmed `/healthz` (API), `/sim/status/200`
(target-simulator), `/-/healthy` (Prometheus), `/api/health` (Grafana), the
Prometheus datasource, and the "Webhook Dispatcher" dashboard all respond
correctly.

## Stage 2 — CI

GitHub Actions should at minimum run:

- formatting check
- static analysis/vet
- unit tests
- race tests where practical
- integration tests with service containers or an equivalent reproducible setup
- application build
- container image build

Implemented in `.github/workflows/ci.yml`: `build-and-test` runs
gofmt/vet/build/test/race against Postgres+Redis service containers. A
second job, `build-image`, builds and pushes the API image to
`ghcr.io/<repo>` tagged with both `latest` and the commit SHA, gated to
pushes on `main` only (not PRs) using the automatic `GITHUB_TOKEN` - no
registry secrets to manage. Hosted formatting/vet/scan/build, PostgreSQL/Redis
tests, Linux race checks and HTTP acceptance now pass at ced4cbe in
[the first hosted run](https://github.com/dhairyarvarma/fault-tolerant-webhook-dispatcher/actions/runs/39094548569).
See docs/validation/2026-10-18-github-publication.md for the separate image
publication outcome. The repository is private; cloud/TLS deployment remains
unexecuted.

## Stage 3 — Single-VM cloud deployment

Use one small Linux VM with Docker Compose.

Recommended architecture:

```text
Internet
   ↓
TLS / reverse proxy
   ↓
API container
   ├── PostgreSQL container or managed PostgreSQL
   ├── Redis container or managed Redis
   └── metrics stack as appropriate
```

Do not move to Kubernetes for the baseline project.

**Not done**: no cloud VM or account is available in this environment, so
this stage is documented but not executed. To actually deploy:

1. Provision a small Linux VM (1-2 vCPU / 1-2 GB RAM is enough at this
   scale - see `RESUME_EVIDENCE.md` for the measured resource footprint).
2. Install Docker + Compose; clone the repository (or just copy
   `docker-compose.yml`, `deploy/`, and `.env`).
3. Create `.env` from `.env.example` with real values. Set `AUTH_ENABLED=true`
   and a real `API_KEY` (required outside `APP_ENV=development` -
   `config.Load()` refuses to start otherwise). Set `ALLOW_PRIVATE_TARGETS=false`
   (the production default; leave it unset).
4. Point `DATABASE_URL`/`REDIS_URL` at managed services, or run the
   `postgres`/`redis` Compose services with their volumes on persistent disk.
5. Instead of `build: .`, pull the image built by CI:
   `ghcr.io/<repo>:<commit-sha>`, and set `api.image` instead of
   `api.build` in a local Compose override.
6. `docker compose up -d`.

### TLS / reverse proxy (only if the API needs public access)

A minimal Caddy reverse proxy (automatic Let's Encrypt, no manual cert
handling) added to the Compose stack:

```yaml
caddy:
  image: caddy:2-alpine
  ports:
    - "80:80"
    - "443:443"
  volumes:
    - ./deploy/Caddyfile:/etc/caddy/Caddyfile:ro
    - caddy-data:/data
  depends_on:
    - api
```

`deploy/Caddyfile`:

```text
your-domain.example {
    reverse_proxy api:8080
}
```

Not live-tested: this requires a real public domain pointed at the VM,
which this environment doesn't have. The config pattern itself (Caddy
automatic HTTPS) is standard and requires no certificate management, which
is why it was chosen over nginx/certbot for a single-VM deployment this
size.

## Cloud safety

- Store secrets in the provider's secret mechanism or environment configuration, never in Git.
- Restrict inbound ports.
- Use TLS for public API access.
- Restrict outbound access if the deployment environment allows it.
- Back up durable PostgreSQL data.

## Recovery test

After deployment:

1. Create a webhook event.
2. Confirm delivery.
3. Restart the application.
4. Confirm durable historical state remains.
5. Create a new event.
6. Confirm asynchronous delivery still works.

**Verified locally 2026-10-17** against the full Docker Compose stack (not
a cloud VM, but the same containers/images/compose file a VM would run),
twice: once restarting only the `api` container, once tearing down and
recreating the entire stack (`docker compose down` without `-v`, then
`docker compose up -d` - every container recreated from scratch). Both
times: the event created before the restart remained queryable with its
full history and reached `SUCCEEDED`, and a new event created after the
restart was delivered successfully. PostgreSQL's data survives because
`postgres-data` is a named volume; Redis has no volume (by design - it is
transport, not the source of truth per `ARCHITECTURE.md`) and its consumer
group is recreated automatically on API startup.

## Rollback

Every deployment should point to a known container image/version so rollback does not depend on rebuilding code live on the server.

In practice (once `build-image` has pushed at least one tag): change the
Compose override's `image:` tag from the current commit SHA to a prior
known-good one and `docker compose up -d api` - no rebuild, no source
checkout needed on the server. This is why CI tags images by commit SHA
rather than only `latest`.

## When to add more infrastructure

Only after a measured requirement. Do not add Kubernetes, multiple cloud regions, service meshes, or multiple brokers because they look impressive on a resume.
