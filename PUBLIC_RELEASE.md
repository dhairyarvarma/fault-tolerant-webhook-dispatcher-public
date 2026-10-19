# Public snapshot and evidence provenance

This is a separately published, privacy-sanitized snapshot of the original
private project. Its new Git history starts here; this does not imply that
the project was developed in one commit. The original history remains private
and unchanged. This publication was explicitly approved by the owner.

## Source and verification

Source is private HEAD 06887da6635bc2d208e74b4dcd801456a3fc980c plus the
subsequent isolation, deferral-observability, retention and operational-test
implementation. No application logic or dependency was changed during export.
PUBLIC_EXPORT.json records inventory/redactions. The source was verified with
Linux race tests, real PostgreSQL/Redis, vet/build and signed HTTP acceptance;
dated records retain skips, failed attempts and final successful verification.
Use this repository's Actions tab for its own hosted verification.

Historical Git SHAs, image identities and private CI URLs refer to the original
development repository, not objects necessarily present in this new history.
Historical records saying there was no remote or that the repository was
private describe their execution state, not this public snapshot. Old benchmark
results retain their original implementation/image/workload, not this new Git
commit. The operational soak explicitly predates final retention hardening;
retention was disabled in that measurement and final code was tested separately.

## Privacy transformations

Only inventoried project files were exported. Original .git history, real .env,
credentials, ignored tools, binaries, caches and generated private reports were
not copied. Twenty older evidence files had personal home-directory and machine/
user identifiers replaced with PUBLIC_USER/PUBLIC_HOST. Newer bundles were
already labelled privacy-redacted derivatives. Measurement values and hardware
conditions were retained, not altered to improve results.

Benchmark notices and all four sha256.json manifests describe these public
derivative bytes; hashes were regenerated after redaction. They are not claims
of byte equality with the private originals. Older prose referring to raw/frozen
originals is superseded by this notice for redacted copies. Original evidence
remains private. New commits use the owner's GitHub no-reply identity, not a
personal email. Package-maintainer contacts in scanner metadata and synthetic
credentials/URL-userinfo in test fixtures are public source data, not owner secrets.

## Safety and honest limits

Secret scans reduce risk but cannot prove that every sensitive string or bug
has been found. Development and CI credentials are synthetic; never reuse them
in production. Publishing source does not start a server or expose your computer.
Do not publicly deploy development Compose defaults.

History retention is off by default. Enabling it permanently deletes eligible
old terminal events/attempts/outbox history and ends their audit/idempotency
window. Back up and read operator precautions first. It does not prune DLQ,
targets or Redis history. Target concurrency caps are per process, not a
distributed fairness guarantee. Delivery is at-least-once, not exactly-once.

There is no demonstrated cloud deployment, production SLA, independent human
walkthrough or personal interview rehearsal. Development used AI assistance
and a detailed specification handoff, as the original documents explain.
No new license grant is introduced by this export. The inherited Go module
path is unchanged; supported setup is clone/build, not a claim of go-install
distribution under the new repository URL.
