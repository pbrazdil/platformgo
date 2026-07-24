# platformgo

Clean-room Go replacement for `upcomers-org/platform`, using pinned source tests as the executable specification. The intended production stack is Go, PostgreSQL, NATS with JetStream, Centrifugo, and Hyperliquid first.

## Current status

Last updated: 2026-07-24

Current delivery stage: **Phase 1 — pure engine and model hardening**.

The source-test inventory is fully adjudicated: all 2,748 pinned tests are recorded in `ports/test-port-map.csv`, with 2,651 `ported-green` and 97 reviewed as `not-applicable`. Native Go domain, exact-value, order, matching, position, market, and deterministic fixture tests are present.

This repository is not yet a production-capable replacement. It has no executable `cmd` services, production PostgreSQL schema or adapter, NATS/JetStream adapter, Centrifugo adapter, or production Hyperliquid adapter. Current integration tests use deterministic in-memory fixtures and do not prove those runtime boundaries.

## Delivery progress

| Phase | Status | Evidence and remaining work |
|---|---|---|
| 0 — Policy and test harness | Implemented, validation debt remains | Policy pack, CI, exact-value primitives, deterministic IDs, fixtures, pinned revisions, and the complete port ledger exist. Policy, lint, and toolchain vulnerability gates are not yet green. |
| 1 — Pure engine | In progress | Native model behavior and ported tests are extensive. Remaining work includes policy-compliant error handling, clean quality gates, and consolidation behind the intended deterministic engine boundary. |
| 2 — Durable execution | Not started | PostgreSQL migrations and persistence, idempotency journal, transactional ledger/state, NATS/JetStream transport, outbox/inbox, and durable recovery are not implemented. |
| 3 — Compatibility edges | Not started | Production REST, gRPC, authentication, realtime/Centrifugo, health, CLI, and deployment-compatible services are not implemented. |
| 4 — Hyperliquid production integration | Not started | No production adapter, reconnect/resynchronization path, controlled live canary, soak test, or incident drill exists. |
| 5 — Replacement rehearsal | Not started | Data import, cutover, rollback, reconciliation, and audited go-live rehearsal remain. |

## Validation snapshot

Verified on 2026-07-24:

- `make test`, `make test-race`, and `make test-repeat` pass.
- Agent-runtime policy, the complete 2,748-row port ledger, dependency policy, and formatting checks pass.
- `make policy` is blocked by one `float64` use in the matching package; a static scan also finds 43 production `panic` sites in policy-scoped packages.
- The pinned golangci-lint v2 profile reports 651 existing findings.
- The Go 1.26.0 standard library reports three called vulnerabilities; the scanner identifies Go 1.26.4 as the fixed version.

## Next milestone

Finish Phase 1 hardening without weakening the accepted tests or economic invariants, then begin Phase 2 with PostgreSQL as the authoritative store and NATS/JetStream as transport.

The authoritative scope, phase definitions, and completion criteria are in `PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
