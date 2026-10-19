# Resume/showcase follow-through — 2026-10-18

## Changes

Added a README-linked five-minute showcase with a recording/transcript route,
narration timeline, exact recovery-evidence references, measured pool-change
explanation and defensible resume bullets. Added an independent student's
setup/feedback checklist. The ownership worksheet remains a human exercise;
the proposed HTTP 409 test case has not been added on the user's behalf.

Added a public-release privacy gate documenting personal email metadata and
personal filesystem paths in history/evidence, without repeating the email.
The original private history and frozen benchmark bundles are unchanged.
Public visibility, sanitized export and history rewriting were not performed.

## Fresh-checkout rehearsal

Created a disposable local clone at .tmp/resume-rehearsal-20261018, with no
copied .env or ignored tools. Git clone initially failed under the restricted
shell; an approved local clone using the explicit source path succeeded.
This is a local source clone, not a test of another student's GitHub access.

Ran `pwsh -NoProfile -File scripts/verify-release.ps1` in that clean clone at
06887da6635bc2d208e74b4dcd801456a3fc980c. Its release metadata records dirty=false
and status=passed. Run ID: verify-20261018071633-7fa70481.

Verified:

- Fresh container build, dependency health, readiness, non-root user and CA bundle.
- HTTP lifecycle, duplicate rejection, non-DLQ replay conflict, DLQ replay with
  history, disabled-target rejection, authentication/health and concurrent
  idempotency; narrated showcase also passed. The native signing-receiver
  test was explicitly skipped in this container-only setup, not passed.
- Live Prometheus scrape and Grafana provisioning (not browser-layout review).
- Matching fingerprints for all five restored tables and new delivery using
  the restored encrypted signing secret.
- Redis restart recovery to durable success: 3.8262029 seconds.
- SIGKILL after an observed database claim, restart and durable success:
  61.424451 seconds, including Docker commands and polling.
- Cleanup completed with exit 0; only the test project's resources were
  removed. Disposable images/build cache and local clone/evidence remain.

Raw local evidence is retained inside the ignored clone at
reports/generated/verify-20261018071633-7fa70481: release.json,
http-acceptance.txt, transcript.txt and containers.log. Those unredacted
logs are not added to Git because the privacy review identified personal
paths in this class of artifact. Recovery observations are single-run
measurements, not production SLAs; no new load/soak test was run.

Also ran the existing retry classifier table with
`go test -count=1 -v ./internal/retry -run '^TestClassify$'` in that clone: passed.
This verifies the existing policy, not the user's ability to add a test live.

All 131 frozen benchmark hashes matched. The retained asciicast v2 recording
validated as 14 ordered output events. Local Markdown links and diff whitespace
were checked for the new showcase material. No application code, runtime
behavior, dependency or API contract changed in this documentation follow-up.

## Still requires the owner or an independent person

- Record/upload a narrated screen video if desired; existing artifacts are
  terminal recordings and readable transcripts, not a newly created MP4.
- Complete the ownership worksheet and live regression without assistance.
- Have another student follow the README and return concrete feedback.
- Approve the public publication route after reviewing known disclosures.

These tasks remain open; an agent-run clean clone does not substitute for them.
