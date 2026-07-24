## Behavior implemented

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
- Existing migrations unchanged:
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

## Commands run

- [ ] `make policy`
- [ ] `make fmt-check`
- [ ] `make lint`
- [ ] `make test`
- [ ] `make test-race`
- [ ] `make test-repeat`
- [ ] relevant integration/contract tests

## Known limitations or unresolved conflicts
