# AI Coding Assistant Handoff

Use this message as the initial instruction for Claude Code, Codex, or another repository-aware coding agent.

---

## Initial handoff prompt

You are working on an existing repository for the **Fault-Tolerant Webhook Delivery Engine**.

Do not implement the project immediately.

### Step 1 — Read the repository instructions

Read:

- `AGENTS.md`
- `CLAUDE.md`
- `README.md`

Then read the project specification and architecture documents listed in `README.md`.

### Step 2 — Audit the repository

Inspect the current repository state, Git history, language/tooling, and any existing files.

Report:

1. Current repository state.
2. What is already implemented, if anything.
3. Any contradictions between files.
4. Any missing information that could block a correct implementation.
5. The implementation risks you expect to encounter.

Do not invent benchmark results or claim that an untested design is correct.

### Step 3 — Produce an execution plan

Map the repository to the milestones in `IMPLEMENTATION_PLAN.md`.

For each milestone identify:

- files/modules to create or modify;
- dependencies;
- tests;
- validation commands;
- likely failure modes.

Do not expand scope beyond the specification.

### Step 4 — Wait before major implementation

If there is a blocking correctness ambiguity, ask about it. Otherwise proceed only after the repository audit and plan are clear.

### Step 5 — Implementation rules

Once implementation starts:

- execute milestones in order;
- make small, coherent changes;
- keep PostgreSQL as the durable source of truth;
- use Redis Streams as work transport;
- preserve at-least-once + idempotency semantics;
- never claim exactly-once delivery;
- test failure paths, not only happy paths;
- do not introduce AI/ML or unnecessary infrastructure;
- do not silently change the API/database/queue contract;
- update documentation after architecture changes;
- run validation after every milestone.

### Final report for each milestone

Return:

1. What changed.
2. Why it was designed that way.
3. Files changed.
4. Tests added.
5. Commands run and whether they passed.
6. Known limitations.
7. What the next milestone depends on.

---

## Agent usage note

The repository documents are the source of truth. The chat prompt is only the execution trigger.
