# platformgo

Clean-room Go replacement for `upcomers-org/platform`, using pinned source
tests as the executable specification. The intended production stack is Go,
PostgreSQL 17 or newer, NATS with JetStream, Centrifugo, and Hyperliquid first.

## Current status

Last updated: 2026-07-26

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

The landed Phase 3 work establishes the first compatibility slice:
executable `app` and `nautilus` commands; frozen OpenAPI, protobuf, and
realtime inventories; selected REST, gRPC, client and broker authentication
flows; reviewed JWT signing and verification; trusted-proxy client-address
derivation; least-privilege runtime PostgreSQL role enforcement; exact command
replay; runtime readiness/drain behavior; a forward-only identity authority
cutover guard; direct Centrifugo contract proof; and a committed
PostgreSQL-to-Centrifugo realtime publication path with stable event identity,
per-channel ordering, bounded claims, retry, and crash recovery.
Exact client funding history is also landed with immutable PostgreSQL
projection integrity, nanosecond keyset pagination, exact aggregation,
least-privilege reads, populated-schema upgrade coverage, and separate
source-port acceptance.

Phase 3 is not complete. The runtime must still:

- implement and semantically review the remaining in-scope frozen HTTP, admin,
  broker, gRPC, realtime, and deployment-role contracts;
- complete, independently review, and land the current exact fill-history
  compatibility slice;
- obtain separate port-ledger acceptance for each source behavior proven by
  subsequent implementation slices.

The source ledger contains all 2,748 pinned tests. It now records 49
independently reviewed green source tests; 2,602 remain explicitly unreviewed
placeholder ports, and 97 implementation-only tests are reviewed and excluded
with decision records. Ledger acceptance remains separate from implementation,
as required by repository governance.

This repository is not yet a production-capable replacement.

## Delivery progress

| Phase | Status | Evidence and remaining work |
|---|---|---|
| 0 — Policy and test harness | Complete | Machine-readable policy, exact decimals, deterministic time and IDs, test fixture, full source inventory, provenance ledger, and protected hosted checks. |
| 1 — Pure engine | Complete | Deterministic order lifecycle, matching, fills, positions, PnL, margin, funding, liquidation, brackets, triggers, and fees with exact-value and replay coverage. |
| 2 — Durable execution | Complete | PostgreSQL 17 authority, immutable checksum-verified migrations, atomic economic commits, command/idempotency journal, durable ownership and ordering, JetStream outbox/inbox, recovery, and reconciliation. |
| 3 — Compatibility edges | In progress | The runtime/contract slice includes reviewed JWT handling, trusted-proxy derivation, least-privilege role enforcement, identity cutover protection, a durable realtime projection, and exact client funding history with PostgreSQL 17 query-plan and migration evidence. Funding implementation and separate port-ledger acceptance are landed. A locally green candidate now starts the fill-history foundation with an indexed newest-execution read and exact engine-time fidelity; the external fills route remains inventory until its complete source contract is implemented and reviewed. |
| 4 — Hyperliquid production integration | Not started | Protocol fixtures, controlled live canaries, reconnect and gap handling, capacity/soak validation, and incident drills. |
| 5 — Replacement rehearsal | Not started | Production-like data import, close-only/drain/cutover rehearsal, rollback and reconciliation plan, and audited go-live decision. |

## Validation snapshot

The landed Phase 3 slices have passed repository-wide formatting, lint, unit,
race, repeat, vulnerability, module-consistency, and policy checks, plus live
tests against disposable PostgreSQL 17, NATS, and Centrifugo. Their exact SHAs
are green in hosted CI. The realtime path additionally has live PostgreSQL 17
commit/order/retry evidence, a simulated Centrifugo outage proof, a real
Centrifugo history proof, independent determinism and money review, an indexed
representative-backlog claim plan, and migration 011 bounded-lock
rollback/retry proof. Exact funding history and its source-port evidence are
green and landed through hosted CI. The current fill-history foundation has
live PostgreSQL 17 proof that `filledAt` comes from the persisted engine
execution time, its newest-execution read uses the intended account-history
index, and a contended populated-schema upgrade rolls back and retries cleanly.
These results do not activate the external fills route or complete the
remaining Phase 3 scope.

## Next milestone

Complete and land the exact fill-history vertical slice and its separate ledger
acceptance, then continue the remaining frozen compatibility operations and
worker roles through vertically narrow changes. Phase 4 begins only after the
Phase 3 charter and release gates are fully satisfied.

The authoritative scope and completion criteria are in
`PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
