# Independent fresh-checkout check

Status: not yet completed by an independent human. An automated or agent-run
rehearsal is separate evidence and cannot check whether these instructions
make sense to another student.

Ask a friend to use a new checkout and only the README, without coaching.
While the repository is private, they need their own authorized GitHub access;
never share an account token, .env, or your real credentials with them.

## Task to hand them

1. Record commit, OS, Docker/Compose versions and start time. Install the
   prerequisites stated in the README; use PowerShell 7 on Windows.
2. Clone the repository into a new directory. Do not copy your .env or tools.
3. Follow Quickstart: generate a fresh encryption key and start Compose.
   Local sample passwords are development-only, not deployment credentials.
4. Wait for readiness. Create a target, create an event, poll SUCCEEDED,
   and inspect its attempts. Repeat the same key: expect 409 and the original ID.
5. Find the architecture, recording/transcript, benchmark conditions,
   at-least-once limitation and crash-boundary case study from README links.
6. If Go is installed, follow the test dependency instructions and run the
   acceptance demo. Record skips/errors rather than treating skips as passes.
7. Stop only this checkout's Compose project with `docker compose down`.
   This preserves database volumes. Do not use `down --volumes` unless you
   deliberately want to discard this disposable project's data.

If they already run another copy, choose a unique Compose project name
(`COMPOSE_PROJECT_NAME` in the new .env) and unused host ports before starting.
Use the README's port overrides; all client URLs must match the new API port.
Do not stop an existing stack to make the walkthrough work.

## Return this feedback

- Commit / OS / prerequisites:
- Time to first successful delivery:
- Exact step where you got stuck:
- Expected behavior versus actual behavior:
- Confusing wording or missing prerequisite:
- Could you find the raw evidence and explain what was actually measured?
- Did anyone need to coach you? If yes, where?

Share redacted errors only: remove passwords, auth headers, personal paths and
real payloads. Submit each issue with a reproducible step and expected result.
The maintainer should fix each blocker and have the friend retry the failing
step before calling this independent check complete.
