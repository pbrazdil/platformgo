## Behavior implemented

## Risk and adversarial preflight

- Risk classification:
- Preflight required/exempt and why:
- Preflight artifact:
- Migration/determinism/money reviewers:
- Blockers found before implementation:

## Source tests ported

- Source repository/revision/path/line/function:
- `ports/test-port-map.csv` updated: yes/no
- Decision records for conflicts/exclusions:

## Invariants affected

## Determinism

- Explicit clock/IDs/sequence/config/instrument revisions:
- Canonical repeat result:

## Idempotency and recovery

- Idempotency key/business key:
- Duplicate-delivery behavior:
- Transaction boundary:
- Ack boundary:
- Crash/restart cases:

## Compatibility impact

- HTTP:
- gRPC:
- realtime:
- CLI/deployment:
- intentional deviation decision:

## Database and messaging

- Migration added:
- Frozen protected/shared migration history unchanged:
- Shared/persistent migration application: none, or exact path/SHA-256/source commit/environment
- NATS subjects/streams/consumers changed:

## Dependencies

- New direct modules:
- Dependency review/ADR:

## Operations and security

- New metrics/alerts:
- Secret/PII/logging impact:
- Runbook impact:

## Agent runtime and prompt policy

- Model/prompt/AGENTS/.codex/API policy changed: yes/no
- Agent profile(s), effort, context, and standard/pro mode used:
- If yes, separate governance PR: yes/no
- Agent-eval report: <path or not applicable>
- All agents remained pinned to `gpt-5.6-sol`: yes/no

## Review checkpoints and exact evidence

- Scope/design/failure-matrix review:
- Failing-test review:
- Implementation-boundary review:
- Advisory closure tree:
- Full-validation SHA:
- Candidate SHA: <full 40-character commit>
- Candidate tree: <full 40-character tree>
- Base SHA: <full 40-character commit>
- Specialist review SHA:
- Final review SHA:
- Hosted CI SHA:

Advisory closure, full-validation evidence, specialist review, final GO, and
hosted CI belong only to the recorded tree. Evidence and GO must not transfer
to a changed tree. A changed tree or SHA invalidates advisory closure,
validation, specialist evidence, and final approval; rerun the affected
exact-candidate gates.

## Workflow metrics

- Blockers found before implementation:
- Blockers found after full validation:
- Exact-SHA candidate count:
- Implementation time:
- Review/test wait time:

## Commands run

- [ ] `make policy`
- [ ] `make fmt-check`
- [ ] `make lint`
- [ ] `make test`
- [ ] `make test-race`
- [ ] `make test-repeat`
- [ ] relevant integration/contract tests

## Known limitations or unresolved conflicts
