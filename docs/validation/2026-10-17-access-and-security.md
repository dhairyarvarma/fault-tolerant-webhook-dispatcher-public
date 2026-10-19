# Access, security updates, and demo verification — 2026-10-17

Reviewed escalation now reaches Docker Engine 29.8.0 and permits official
Go tool downloads. The restricted sandbox alone still denies Docker access.
No broad filesystem permissions or unauthenticated Docker TCP socket were enabled.

Installed tools in ignored .tmp/tools (not committed; no global PATH change):

- k6 v1.8.2, compiled with Go 1.27.0 for Windows amd64.
- govulncheck v1.9.0; database https://vuln.go.dev, updated
  2026-10-08 22:31:09 UTC as reported by the tool.

The first source scan reported 11 reachable vulnerability reports in Go
1.27.0 and golang.org/x/text v0.29.0. The project now requires Go 1.27.2 and
x/text v0.41.0; its required x/sync version resolves to v0.22.0. Docker uses
the patched Go tag, CI reads go.mod for its toolchain, and a pinned scanner
step blocks future reported reachable vulnerabilities. The native rescan
on the updated tree reports:

```text
No vulnerabilities found.
```

This is the scanner result against its database at execution time, not proof
that the application or runtime image has no possible security issues.
No separate OS-package/image vulnerability scan was performed.

The updated Dockerfile builds webhook-dispatcher:local-verification locally.
The image is based on a working tree above 2726374, including the dependency
updates and narrated demo. It is not a published release image. Before the
dependency update, runtime inspection confirmed UID/GID 65532 and a nonempty
/etc/ssl/certs/ca-certificates.crt; those image instructions are unchanged.

On Windows with Go 1.27.2 and real local PostgreSQL/Redis:

- go test -count=1 -timeout 2m ./... passes.
- go vet ./... passes.
- govulncheck ./... passes with the result above.
- go run ./cmd/acceptance passes all eight HTTP cases.
- go run ./cmd/acceptance -demo passes the narrated showcase (6.27s test body).
- go mod verify reports all modules verified.

An initial complete Linux race run passes against the same real dependencies
through host.docker.internal. The source was mounted read-only, the container
was disposable, and fixtures used isolated schemas/streams. A fresh complete
run with Go 1.27.2 and the updated dependencies also passes:

```text
go test -race -count=1 -timeout 3m ./...
# All packages pass; HTTP e2e cases skip without TEST_BASE_URL in this command.
# The native acceptance/demo commands above execute those HTTP cases separately.
```

The narrated showcase is invoked with go run ./cmd/acceptance -demo. It asserts
success, duplicate identity, three failed attempts to DLQ, open-circuit
deferral with zero attempts, successful probe recovery, and replay retaining
four historical attempts. Its three-attempt/five-second settings are demo-only,
not production defaults or performance/recovery measurements.

No k6 load run, full-stack dashboard validation, backup restore, hosted CI,
or cloud/TLS deployment is claimed by this record.
