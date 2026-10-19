# Development Workflow

## Branching

Use a simple model:

```text
main
└── feature/<milestone-or-task>
```

## Commit style

Prefer small, descriptive commits such as:

```text
feat(api): add durable event creation
feat(queue): add redis stream relay
feat(delivery): add retry classifier
fix(worker): handle duplicate queue delivery safely
test(reliability): cover worker crash recovery
chore(ci): add go test and vet checks
```

## AI agent workflow

### Before coding

- Read instructions.
- Inspect repository.
- Summarize architecture.
- Identify blocking ambiguities.

### During coding

- One coherent task at a time.
- Tests with behavior changes.
- Avoid unrelated refactors.
- Keep diffs reviewable.

### Before commit

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
```

Add integration/load commands as the relevant milestone requires.

## Human review checklist

Before accepting agent changes, inspect:

- database transaction boundaries;
- goroutine lifetimes;
- error handling;
- retry decisions;
- state transitions;
- queue acknowledgment behavior;
- secret handling;
- SSRF policy;
- tests for the new behavior.

## Definition of done

A task is done only when the behavior exists, is tested, documented if necessary, and validated by the required commands.
