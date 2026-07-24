# platformgo

Clean-room Go replacement for `upcomers-org/platform`, using pinned source tests as the executable specification. The intended production stack is Go, PostgreSQL, NATS with JetStream, Centrifugo, and Hyperliquid first.

## Current status

Last updated: 2026-07-24

Current delivery stage: **Phase 0 — numeric implementation candidate complete**.

The pinned source inventory is complete: all 2,748 tests are recorded in
`ports/test-port-map.csv`. Of those, 2,651 have native Go representations but
remain `unreviewed/placeholder/spec-fixture`; 97 implementation-only tests are
reviewed and excluded with decision records. The numeric implementation
candidate includes a corrected five-test source cohort, but its ledger
promotion is intentionally deferred to a governance-only follow-up after the
production functions exist on `main`.

This repository is not yet a production-capable replacement. It has no executable `cmd` services, production PostgreSQL schema or adapter, NATS/JetStream adapter, Centrifugo adapter, or production Hyperliquid adapter. Current integration tests use deterministic in-memory fixtures and do not prove those runtime boundaries.

## Delivery progress

| Phase | Status | Evidence and remaining work |
|---|---|---|
| 0 — Policy and test harness | Numeric implementation candidate complete; evidence promotion and deterministic kernel remain | Machine-readable package scope, AST policy checks, split port/review/wiring evidence, exact function provenance, canonical source authorities, pinned Go 1.26.5, CODEOWNERS, immutable CI actions, complete-port and tidy gates, and the initial agent-evaluation corpus exist. `main` is protected and all seven required checks are enforced. The numeric candidate adds the sole `apd/v3`-backed production decimal, strict canonical parsing, explicit one-boundary rounding, immutable unit-bearing values, parser/arithmetic fuzzing, and a corrected five-test source cohort. A separate governance-only PR must promote that cohort to reviewed green. Deterministic clock, IDs, and the engine fixture remain before Phase 0 completes. |
| 1 — Pure engine | Not started under the reviewed-evidence definition | Extensive native compatibility models and placeholder tests exist, but no implementation cohort is semantically reviewed and wired to the intended deterministic engine boundary. |
| 2 — Durable execution | Not started | PostgreSQL migrations and persistence, idempotency journal, transactional ledger/state, NATS/JetStream transport, outbox/inbox, and durable recovery are not implemented. |
| 3 — Compatibility edges | Not started | Production REST, gRPC, authentication, realtime/Centrifugo, health, CLI, and deployment-compatible services are not implemented. |
| 4 — Hyperliquid production integration | Not started | No production adapter, reconnect/resynchronization path, controlled live canary, soak test, or incident drill exists. |
| 5 — Replacement rehearsal | Not started | Data import, cutover, rollback, reconciliation, and audited go-live rehearsal remain. |

## Validation snapshot

Verified on 2026-07-24:

- `make policy`, `make port-map-complete`, `make fmt-check`, `make lint`,
  `make test`, `make test-race`, `make test-repeat`, and `make vuln` pass in
  the current working tree.
- `go mod tidy -diff` is clean.
- The strict lint profile covers production, infrastructure, non-economic, and
  tooling classifications. Ported compatibility and placeholder packages
  remain quarantined but still compile, test, vet, and pass AST safety checks.
- All 2,748 ledger rows pass structural validation and exact Go `FuncDecl`
  provenance binding.
- Both numeric-boundary governance prerequisites passed all seven required
  checks before merge. Hosted validation for this numeric candidate is pending.

## Next milestone

Land the numeric implementation candidate and its separate evidence-promotion
PR, then implement the deterministic engine kernel: explicit logical time and
IDs, strict input sequencing, duplicate-input receipt behavior, canonical
state/decision hashes, and the minimal engine fixture. PostgreSQL and NATS
remain out of that slice.

The authoritative scope, phase definitions, and completion criteria are in `PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
