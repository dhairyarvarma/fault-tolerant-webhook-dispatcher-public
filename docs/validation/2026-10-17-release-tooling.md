# Release tooling and failure-boundary verification — 2026-10-17

Working tree based on 79160be; Go 1.27.0, Windows amd64, real localhost
PostgreSQL and Redis. Disposable database schemas and Redis streams isolate
fixtures from application data. This supplements the release-hardening record;
it is correctness evidence, not a throughput or outage-recovery benchmark.

## Verified

- All packages pass: go test -count=1 -timeout 2m ./... with both dependency
  test URLs configured. Black-box tests are separately exercised by the
  acceptance runner rather than silently counted from their default skips.
- go vet ./..., go build -buildvcs=false ./..., and go mod verify pass.
- go run ./cmd/acceptance passes all eight authenticated HTTP cases: delivery,
  duplicate conflict, invalid replay, DLQ/replay history, disabled targets,
  authentication/health, concurrent duplicates, and signed receiver verification.
- The server cross-compiles for Linux amd64 with CGO_ENABLED=0. This proves
  compilation only, not Linux runtime or image behavior.
- All three k6 scripts pass Node's module syntax check. The production Compose
  merge passes config --quiet using dummy validation credentials. Docker prints
  a client-config access warning; no image or running stack is validated.
- The benchmark runner's validation-only mode reports validation_only_no_load.
  Its dependency-backed SQL tests verify run scoping, failure/cancellation
  counts, successful-delivery percentiles, and empty-run null percentiles.

New failure regressions cover DNS failure, injected connect timeout, malformed
HTTP response redaction, actual HTTP status/hang/reset persistence, safe API
responses during database unavailability, target-name bounds, conservative
special-purpose address rejection, production/private-target configuration
rejection, and static credential-safe connection-string parse errors.

The POST hang simulator now consumes its bounded request body before awaiting
cancellation; the cancellation regression and full failure matrix pass.

## Prepared, not executed

The benchmark runner archives its exact workload, configuration/tool metadata,
k6 output/summary, metrics samples, and optional run-scoped SQL delivery report.
No final-version ingestion, ceiling, soak, resource, or recovery measurements
were run. Old README/resume figures remain explicitly historical.

The production overlay, TLS template, backup/restore/rollback runbook, and
interview guide are prepared material, not deployment or restore evidence.

## External limits observed

- Docker engine access and WSL enumeration are denied.
- Installed MinGW cannot compile 64-bit cgo; the race probe fails in runtime/cgo
  with "64-bit mode not compiled in", before application race tests execute.
- Native k6 and govulncheck are absent. Workspace-local installation attempts
  fail because outbound proxy.golang.org sockets are forbidden by the host.
- No Git remote or observed hosted CI run; no authorized deployment VM/domain.

Remaining release gates therefore include supported race testing, vulnerability
scanning, image/runtime/dashboard checks, measured load/resources/recovery,
backup/restore rehearsal, and any authorized cloud/TLS deployment. No
production-readiness or complete-release claim follows from this record.
