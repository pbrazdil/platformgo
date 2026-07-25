# platformgo

Clean-room Go replacement for `upcomers-org/platform`, using pinned source
tests as the executable specification. The intended production stack is Go,
PostgreSQL 17 or newer, NATS with JetStream, Centrifugo, and Hyperliquid first.

## Current status

Last updated: 2026-07-25

Current delivery stage: **Phase 3 — compatibility edges, in progress**.

Phases 0 through 2 are complete:

- Phase 0 established repository policy, exact decimal arithmetic, explicit
  logical time and deterministic IDs, the synchronous engine fixture, and the
  complete pinned source-test inventory.
- Phase 1 implemented the deterministic pure engine: orders, matching, fills,
  netting and hedging positions, exact PnL, margin, funding, liquidation,
  brackets, triggers, and maker/taker fees.
- Phase 2 made execution durable through PostgreSQL authority, immutable
  migrations, atomic command/state/ledger/checkpoint/outbox transactions,
  single-writer shard ownership, JetStream delivery, replay, reconciliation,
  and restart/fault coverage.

The current Phase 3 candidate establishes the first compatibility slice:
executable `app` and `nautilus` commands; frozen OpenAPI, protobuf, and
realtime inventories; selected REST, gRPC, client and broker authentication
flows; exact command replay; runtime readiness/drain behavior; and direct
Centrifugo contract proof.

Phase 3 is not complete. Release review found that the runtime must still:

- replace ad hoc JWT code with the separately approved reviewed library;
- enforce trusted-proxy source IP and least-privilege PostgreSQL role
  boundaries at startup;
- add a durable committed-event-to-Centrifugo projection with recovery,
  ordering, deduplication, and gap behavior;
- implement and semantically review the remaining in-scope frozen HTTP, admin,
  broker, gRPC, realtime, and deployment-role contracts;
- add a forward-only fail-closed correction for ambiguous pre-authority
  identity data;
- obtain green live PostgreSQL 17, NATS, Centrifugo, compatibility, and hosted
  exact-SHA release evidence.

The source ledger contains all 2,748 pinned tests. On current `main`, 41 source
tests are independently reviewed and green, 2,610 remain explicitly
unreviewed placeholder ports, and 97 implementation-only tests are reviewed
and excluded with decision records. Phase 3 ledger acceptance will be proposed
separately from implementation, as required by repository governance.

This repository is not yet a production-capable replacement.

## Delivery progress

| Phase | Status | Evidence and remaining work |
|---|---|---|
| 0 — Policy and test harness | Complete | Machine-readable policy, exact decimals, deterministic time and IDs, test fixture, full source inventory, provenance ledger, and protected hosted checks. |
| 1 — Pure engine | Complete | Deterministic order lifecycle, matching, fills, positions, PnL, margin, funding, liquidation, brackets, triggers, and fees with exact-value and replay coverage. |
| 2 — Durable execution | Complete | PostgreSQL 17 authority, immutable checksum-verified migrations, atomic economic commits, command/idempotency journal, durable ownership and ordering, JetStream outbox/inbox, recovery, and reconciliation. |
| 3 — Compatibility edges | In progress | The first runtime/contract slice exists and has focused live evidence. Security boundaries, durable realtime projection, full frozen surface coverage, role compatibility, independent ledger acceptance, and exact-SHA hosted evidence remain. |
| 4 — Hyperliquid production integration | Not started | Protocol fixtures, controlled live canaries, reconnect and gap handling, capacity/soak validation, and incident drills. |
| 5 — Replacement rehearsal | Not started | Production-like data import, close-only/drain/cutover rehearsal, rollback and reconciliation plan, and audited go-live decision. |

## Validation snapshot

The first Phase 3 slice has passed repository-wide formatting, lint, unit,
race, repeat, vulnerability, module-consistency, and policy checks in local
development, plus focused live tests against disposable PostgreSQL 17, NATS,
and Centrifugo. Those results are development evidence only: the final Phase 3
release gate remains open until the listed review blockers are fixed and the
immutable candidate SHA passes all hosted checks without required integration
skips.

## Next milestone

Complete Phase 3 through vertically narrow, independently reviewed changes:
security and runtime authority first, then durable realtime delivery, then the
remaining frozen compatibility operations and worker roles. Phase 4 begins
only after the Phase 3 charter and release gates are fully satisfied.

The authoritative scope and completion criteria are in
`PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
