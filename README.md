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
flows; reviewed JWT signing and verification; trusted-proxy client-address
derivation; least-privilege runtime PostgreSQL role enforcement; exact command
replay; runtime readiness/drain behavior; a forward-only identity authority
cutover guard; and direct Centrifugo contract proof.

Phase 3 is not complete. The runtime must still:

- add a durable committed-event-to-Centrifugo projection with recovery,
  ordering, deduplication, and gap behavior;
- implement and semantically review the remaining in-scope frozen HTTP, admin,
  broker, gRPC, realtime, and deployment-role contracts;
- obtain green hosted exact-SHA PostgreSQL 17, NATS, Centrifugo,
  compatibility, and race evidence for this foundation candidate;
- obtain separate independent port-ledger acceptance for the source behaviors
  proven by the implementation.

The source ledger contains all 2,748 pinned tests. The current Phase 3
governance candidate raises independently reviewed green source tests from 41
to 47; 2,604 remain explicitly unreviewed placeholder ports, and 97
implementation-only tests are reviewed and excluded with decision records.
This ledger acceptance is separate from implementation, as required by
repository governance.

This repository is not yet a production-capable replacement.

## Delivery progress

| Phase | Status | Evidence and remaining work |
|---|---|---|
| 0 — Policy and test harness | Complete | Machine-readable policy, exact decimals, deterministic time and IDs, test fixture, full source inventory, provenance ledger, and protected hosted checks. |
| 1 — Pure engine | Complete | Deterministic order lifecycle, matching, fills, positions, PnL, margin, funding, liquidation, brackets, triggers, and fees with exact-value and replay coverage. |
| 2 — Durable execution | Complete | PostgreSQL 17 authority, immutable checksum-verified migrations, atomic economic commits, command/idempotency journal, durable ownership and ordering, JetStream outbox/inbox, recovery, and reconciliation. |
| 3 — Compatibility edges | In progress | The first runtime/contract slice includes reviewed JWT handling, trusted-proxy derivation, least-privilege role enforcement, identity cutover protection, and focused live evidence. Durable realtime projection, full frozen surface coverage, independent ledger acceptance, and exact-SHA hosted evidence remain. |
| 4 — Hyperliquid production integration | Not started | Protocol fixtures, controlled live canaries, reconnect and gap handling, capacity/soak validation, and incident drills. |
| 5 — Replacement rehearsal | Not started | Production-like data import, close-only/drain/cutover rehearsal, rollback and reconciliation plan, and audited go-live decision. |

## Validation snapshot

The first Phase 3 slice has passed repository-wide formatting, lint, unit,
race, repeat, vulnerability, module-consistency, and policy checks in local
development, plus focused live tests against disposable PostgreSQL 17, NATS,
and Centrifugo. The first hosted candidate exposed missing compatibility-test
role provisioning and a runner-dependent connection-pool budget; both are
corrected locally but still require a new immutable candidate SHA with all
hosted checks green. These results are foundation evidence only and do not
complete the remaining Phase 3 scope.

## Next milestone

Land the independently reviewed security and runtime-authority foundation,
then complete durable realtime delivery and the remaining frozen compatibility
operations and worker roles through vertically narrow changes. Phase 4 begins
only after the Phase 3 charter and release gates are fully satisfied.

The authoritative scope and completion criteria are in
`PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
