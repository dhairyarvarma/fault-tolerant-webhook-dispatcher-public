# Public release privacy gate

The existing repository remains private. No history rewrite or public
visibility change is authorized by this checklist.

## Review observed on 2026-10-18

Gitleaks 8.30.1 scanned all 34 commits, approximately 26.43 MB of patches.
Its 11 generic-api-key findings were each checked at the flagged revision:
the exact line was a reported_api_commit value, a 40-character hexadecimal
SHA resolving to a local Git commit. These are reviewed false positives.
No broad exclusion was added. The redacted report is retained locally in
reports/generated/2026-10-18-public-review-secrets.json, not committed.

The earlier tracked-file/history check found no real .env or private-key
files. This does not prove that arbitrary sensitive strings could never be
present; scanners have limited patterns and cannot assess all personal data.

Known non-secret disclosures:

- Commit author metadata contains a personal Gmail address.
- Retained transcripts and k6 output contain a Windows username and local
  absolute paths, including the workspace and runtime location.
- Benchmark evidence records CPU/OS, Docker limits, timestamps, synthetic
  event IDs, loopback ports and development/test configuration.
- Source and documents intentionally describe security controls, limitations,
  development-only defaults and the project's assisted-development origin.

Do not remove machine specifications just to make numbers look stronger:
measurement conditions are part of the evidence. Personal paths can instead
be redacted in a separately labelled public derivative.

## Recommended publication route, pending approval

Keep this private repository as the original provenance record. Prepare a
separate public snapshot repository only after the owner approves its name,
contents and visibility. This avoids force-pushing or rewriting the original
history, but loses the public commit-by-commit development narrative.

Before uploading the snapshot:

1. Export tracked files only, never .git, .env, ignored reports, tools or caches.
2. Replace personal filesystem paths in exported logs with a documented
   placeholder; do not alter counts, timings, statuses or workload settings.
3. Clearly label those logs as privacy-redacted derivatives. Retain original
   private bundles unchanged. Regenerate public manifests for the redacted
   bytes and explain that they do not match the private original hashes.
4. Configure a verified GitHub no-reply author address for the new repository
   before its first commit; this does not fix the original commit history.
5. Review all tracked content for addresses, credentials, payloads, personal
   paths and third-party rights. Rerun secret scanning on both files and the
   new history. Inspect any scanner findings rather than suppressing them.
6. Check README/evidence links and run the public checkout's verification.
   Explain references to original private commit SHAs rather than claiming
   those commits are present in the snapshot history.
7. Confirm whether Actions logs, artifacts and container-package visibility
   are acceptable. Never assume repository privacy and package privacy are
   interchangeable. Do not upload raw unreviewed recordings.
8. Obtain the owner's final approval before creating/publishing the repository.

Alternatively, the owner may explicitly accept the listed disclosures and
approve making the original public. Rewriting its history is another option,
but changes commit identities, disrupts provenance/references and needs a
separate approved backup/migration plan. Neither route has been executed.

Publishing source does not itself expose a running server or your computer.
It does make any included data copyable; later deletion cannot reliably
recall copies. Do not deploy the development Compose defaults publicly.
