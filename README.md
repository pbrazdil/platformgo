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

The fill-history foundation is also landed: its indexed newest-execution read
preserves the immutable engine execution time exactly, and its first
source-derived behavior has separate port-ledger acceptance. The external
fills route remains inventory. The landed filtering slice adds account-scoped,
case-insensitive side filtering and exact fill-ID filtering over immutable
fills, with a filtered total from the same PostgreSQL statement and separate
port-ledger acceptance. Each fill's immutable order reference now also projects
through the same `urn:xb:order:<UUID>` identity as the existing order surface,
with separate source-port acceptance preserving that current Go representation
as the authoritative cross-surface contract. Every durable fill's source-cased
side and required classified position effect now also project as its exact
lowercase trade type, with separate source-port acceptance preserving current
Go classification as authoritative.
The next fill-adjacent implementation slice ports durable order-rejection
reason persistence through the real engine transaction and PostgreSQL recovery
path. It also closes the exposed durable balance-projection gap: every
computable used/free/equity change now accompanies its accepted decision,
including working-order reservation, release, fill, and market-only projection
changes. Decision hash v3 separates those effects from
historical v2 replay. A guarded forward migration refuses pre-v3 order receipts
whose omitted projection cannot be repaired by guessing, while allowing safe
non-order v2 history and preventing an old binary from writing another v2
decision. The current Go lifecycle remains authoritative: a working
order rejected for `slippage_exceeded` stays terminal, and later market inputs
cannot rewrite its reason or version. Separate source-ledger acceptance is
still pending.

The client account-summary slice now implements authenticated
`GET /v1/me/accounts`. It reads only the caller's complete durable account
graph through the least-privilege API role, exposes the frozen camel-case
`MyAccountView`, preserves source enum spellings at the wire boundary, and
fails closed rather than omitting an incomplete account. Its additive
PostgreSQL 17 migration sequence records account status and default margin mode
without rewriting existing rows, commits metadata locks before constraint
validation, and has bounded-lock rollback/retry plus concurrent read/write
coverage. Its pinned source behavior now has separate port-ledger acceptance.

The landed user API-key slice implements authenticated
`POST /v1/me/api-keys` as a single PostgreSQL transaction. It returns the
opaque `xbk_` credential once, persists only its SHA-256 digest, serializes the
PostgreSQL-authoritative per-owner cap under the durable user-row lock, and
appends the attributed audit fact atomically. Explicit `Idempotency-Key`
retries replay an encrypted exact HTTP envelope before new entropy or rate
capacity is consumed, including after a post-commit unknown outcome; only its
fixed SHA-256 digest reaches PostgreSQL or replay authentication. A shared
PostgreSQL limiter covers the complete protected-client surface, rate-accounts
invalid new requests exactly once, and admits a new credential in the same
transaction only after replay/conflict resolution. Legacy source policy inputs
retain their complete numeric domains and fail readiness closed when they do
not match the durable singleton. New credentials also fail closed whenever
runtime command readiness is false, while committed replay remains available.
Replay-key rotation uses an explicit active key and distribute-then-promote
procedure; startup and readiness verify every live replay key, and expired
ciphertext is removed by a bounded owned cleanup loop with per-key backlog
evidence. The additive migration grants the API role only execution of bounded
authority functions, not direct table mutation, and explicitly requires
forward repair or full pre-migration restore rather than old-binary rollback.
The shown-once endpoint requires a client-stable `Idempotency-Key`; this
owner-approved safety deviation prevents an active credential whose only
plaintext response was lost. The landed replay-order decision also requires
exact committed replay and deterministic conflict resolution before new-work
rate rejection. Its implementation review and four source-port acceptance rows
are landed as separate gates.

Phase 3 is not complete. The runtime must still:

- implement and semantically review the remaining in-scope frozen HTTP, admin,
  broker, gRPC, realtime, and deployment-role contracts;
- continue the remaining fill-history behaviors through complete, independently
  reviewed compatibility slices;
- obtain separate port-ledger acceptance for each additional source behavior
  proven by subsequent implementation slices.

The source ledger contains all 2,748 pinned tests. It now records 58
independently reviewed green source tests; 2,593 remain explicitly unreviewed
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
| 3 — Compatibility edges | In progress | The runtime/contract slice includes reviewed JWT handling, trusted-proxy derivation, least-privilege role enforcement, identity cutover protection, a durable realtime projection, exact client funding history, authenticated caller-scoped account summaries, and hash-only self-service API-key creation with encrypted durable replay, a PostgreSQL-authoritative owner cap, protected-client rate limiting, and an atomic audit fact. Funding, account-summary, and API-key implementation plus their separate port-ledger acceptance are landed. The fill-history foundation and its first four separately accepted source behaviors add an indexed newest-execution read with exact engine-time fidelity, immutable side/fill-ID filtering, same-statement filtered totals, a correlatable fill-to-order identity using the current Go `urn:xb:order:<UUID>` representation, and exact classified side/trade-type projection. The next implementation candidate proves durable rejection-reason persistence, exact atomic reservation/release projections, version-aware v2/v3 recovery, and terminal no-rewrite behavior; its ledger gate remains separate. The external fills route remains inventory until its complete source contract is implemented and reviewed. |
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
The landed filtering slice additionally has live PostgreSQL 17 proof for
lowercase side and exact fill-ID filters, same-statement totals, the exact
production query plan over a representative 100,000-fill multi-account
dataset, bounded migration rollback/retry, and separate source-port acceptance.
The landed fill-order correlation slice additionally proves on PostgreSQL 17
that a fill references the same stable `urn:xb:order:<UUID>` identity exposed
by the existing order surface. Its separately accepted source row explicitly
preserves that current Go representation as authoritative; the slice does not
activate the external fills route. The landed side/trade-type slice proves all
five current Go position effects through the exact production query and
least-privilege API role. Its separate source acceptance records the
owner-approved decision to preserve the required classified effect instead of
importing an unclassified legacy mirror row.
The current order-rejection candidate additionally proves on PostgreSQL 17
that the engine atomically persists `slippage_exceeded`; commits exact
`0/1000 → 1.1/998.9 → 0/1000` used/free projections with the order lifecycle;
rolls back a pre-commit fault; replays duplicate delivery without a second
reservation; reconciles and recovers the working checkpoint; recovers mixed
decision-hash v2/v3 history; and leaves the terminal reason and order version
unchanged under later market inputs. Migration contention rolls back within a
bounded lock window and retries cleanly. Pre-v3 order history fails the
migration without mutation and requires owner-reviewed forward repair or
restore/reset; binary rollback after v3 order traffic is prohibited. The slice
does not add an unapproved field to the frozen external order contract.
The client account-summary slice additionally has live PostgreSQL 17 HTTP
proof through the least-privilege API role, cross-user isolation, exact full
wire-shape assertions, and a contended additive-schema upgrade that rolls back
within the bounded lock window and retries without data loss. Its pinned source
behavior has separate port-ledger acceptance.
The landed API-key slice has PostgreSQL 17 proof for exact encrypted
status/header/body replay after an injected post-commit unknown outcome,
recovery and conflict without entropy, concurrent duplicates with one rate
token, cross-route/cross-replica limiting with source-compatible
`429`/`Retry-After`, additive-field and known-field compatibility, fail-closed
legacy-policy reconciliation, two-stage key rotation, and bounded idempotent
ciphertext cleanup. Its exact implementation candidate passed hosted CI 7/7
and four independent reviews before merge. Its four pinned source behaviors
have separate ledger acceptance.
These results do not activate the external fills route or complete the
remaining Phase 3 scope.

## Next milestone

Land the separate rejection-reason source-ledger acceptance, then continue the
remaining fill-history behaviors and frozen compatibility operations through
vertically narrow implementation and ledger-acceptance changes. Phase 4 begins
only after the Phase 3 charter and release gates are fully satisfied.

The authoritative scope and completion criteria are in
`PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
