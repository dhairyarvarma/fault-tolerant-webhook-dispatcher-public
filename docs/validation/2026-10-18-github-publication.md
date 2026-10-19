# Private GitHub publication — 2026-10-18

## Repository and scope

Created the approved private repository:
https://github.com/dhairyarvarma/fault-tolerant-webhook-dispatcher.
The API confirms private=true and default_branch=main. The existing local
master branch was renamed to main without rewriting history, pushed, and
configured to track origin/main. No force push or visibility change occurred.
The initial published application commit is
ced4cbe886596a9d6ef92e1199cf96ba7d35b01f.

The installed GitHub plugin did not expose operations in this chat. The
fallback is official GitHub CLI 2.102.0, downloaded to ignored .tmp/tools and
verified against the release's SHA256 checksum. The user approved GitHub's
browser/device flow; credentials are stored in the system keyring, not the
repository or chat. Push authentication used command-scoped Git configuration;
the existing global credential helper was not replaced.

## Pre-push review

Gitleaks 8.30.1 scanned all 33 existing commits (about 26.42MB of patches).
It reported 11 generic-api-key findings in retained benchmark metadata.
Every flagged line is the reported_api_commit field containing a 40-character
Git SHA, and every SHA resolves to a local commit object. These are reviewed
false positives, not credentials. No blanket ignore or history rewrite was
used. Tracked-file inspection found no real .env or private-key files.
This is a dated scanner/review result, not a guarantee of no possible secret.
Raw redacted output remains in reports/generated/2026-10-18-prepush-secrets.*.

## Hosted verification

[Initial CI run](https://github.com/dhairyarvarma/fault-tolerant-webhook-dispatcher/actions/runs/39094548569)
tests ced4cbe. Its build-and-test job (117578151101) succeeds, including:

- clean GitHub checkout and Go toolchain setup;
- formatting, vet, reachable Go vulnerability scan and build;
- unit/repository tests with PostgreSQL and Redis service containers;
- Linux race tests;
- HTTP acceptance against a newly built isolated authenticated server.

The optional native-k6 command-level tests require k6 on PATH. The workflow
does not install k6, so a green ordinary test step does not establish that
those optional cases executed. Their local execution is recorded separately
in 2026-10-18-benchmark-verification.md. A green CI run is not a new benchmark.

The complete workflow concludes success. Its build-image job (117578708650)
also succeeds and publishes:

```text
ghcr.io/dhairyarvarma/fault-tolerant-webhook-dispatcher:ced4cbe886596a9d6ef92e1199cf96ba7d35b01f
ghcr.io/dhairyarvarma/fault-tolerant-webhook-dispatcher:latest
digest: sha256:3adcf0d56a71dab9e116e1d362ff48c2999487344b3f7037af2dc418a44e15fb
```

The first job takes 3m15s and the image job 1m11s, as reported by Actions.
Raw workflow metadata and logs are retained locally under
reports/generated/2026-10-18-github-initial-ci.json and .log. Repository privacy
is rechecked after publishing; anonymous GHCR access is denied with HTTP 401.
This is publication verification, not an executed deployment or authenticated
pull/run of that published digest on a new host.

Actions emits non-failing maintenance warnings: older action releases target
Node 20 and are forced onto Node 24, and ubuntu-latest is scheduled to move
to Ubuntu 26. The observed run passes; action/runner updates need a separate
validated change, not a claim of a permanently fixed build environment.

## Remaining scope

The repository remains private. Public visibility, branch-protection settings,
new collaborators, cloud/TLS deployment and production traffic were not
configured. Personal ownership rehearsal and another student's independent
README walkthrough still require human participation. Historical validation
records stating no remote/hosted CI describe their earlier execution state;
this publication record supersedes that blocker, not their test results.
