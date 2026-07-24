# platformgo

Clean-room Go replacement for `upcomers-org/platform`, using pinned source tests as the executable specification. The intended production stack is Go, PostgreSQL, NATS with JetStream, Centrifugo, and Hyperliquid first.

## Current status

Last updated: 2026-07-24

Current delivery stage: **Phase 0 — numeric foundation**.

The pinned source inventory is complete: all 2,748 tests are recorded in
`ports/test-port-map.csv`. Of those, 2,651 have native Go representations but
remain `unreviewed/placeholder/spec-fixture`; 97 implementation-only tests are
reviewed and excluded with decision records. Mechanical porting is no longer
conflated with semantic acceptance or production implementation.

This repository is not yet a production-capable replacement. It has no executable `cmd` services, production PostgreSQL schema or adapter, NATS/JetStream adapter, Centrifugo adapter, or production Hyperliquid adapter. Current integration tests use deterministic in-memory fixtures and do not prove those runtime boundaries.

## Delivery progress

| Phase | Status | Evidence and remaining work |
|---|---|---|
| 0 — Policy and test harness | Repository preflight complete; numeric foundation in progress | Machine-readable package scope, AST policy checks, split port/review/wiring evidence, exact function provenance, canonical source authorities, pinned Go 1.26.5, CODEOWNERS, immutable CI actions, complete-port and tidy gates, and the initial agent-evaluation corpus exist. `main` is protected, all seven required checks are enforced, and the current `main` passed the complete hosted workflow. The next narrow slice is the production exact-decimal and typed-value foundation. |
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
- The complete hosted workflow passed on current `main` commit
  `325c7231553059b81579bb0962f1959655931d11`.

## Next milestone

Complete the numeric foundation cohort: independently review its source tests,
settle the one exact economic representation and precision rules, wire the
reviewed tests red to real code, and implement them green without allowing the
quarantined compatibility decimal tree into production.

The authoritative scope, phase definitions, and completion criteria are in `PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
