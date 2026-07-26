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
port-ledger acceptance.

The client account-summary slice now implements authenticated
`GET /v1/me/accounts`. It reads only the caller's complete durable account
graph through the least-privilege API role, exposes the frozen camel-case
`MyAccountView`, preserves source enum spellings at the wire boundary, and
fails closed rather than omitting an incomplete account. Its additive
PostgreSQL 17 migration sequence records account status and default margin mode
without rewriting existing rows, commits metadata locks before constraint
validation, and has bounded-lock rollback/retry plus concurrent read/write
coverage. Its pinned source behavior now has separate port-ledger acceptance.

The current user API-key candidate implements authenticated
`POST /v1/me/api-keys` as a single PostgreSQL transaction. It returns the
opaque `xbk_` credential once, persists only its SHA-256 digest, serializes the
PostgreSQL-authoritative per-owner cap under the durable user-row lock, and
appends the attributed audit fact atomically. Optional `Idempotency-Key`
retries replay authenticated ciphertext under a rotatable external AES-256-GCM
keyring, including after a post-commit unknown outcome, without storing the
plaintext token. A shared PostgreSQL limiter enforces the pinned protected-
client rate policy before credential entropy or mutation work. The additive
migration grants the API role only execution of the bounded authority
functions, not direct table mutation. Its three source behaviors still require
replacement independent implementation review and separate port-ledger
acceptance.

Phase 3 is not complete. The runtime must still:

- implement and semantically review the remaining in-scope frozen HTTP, admin,
  broker, gRPC, realtime, and deployment-role contracts;
- continue the remaining fill-history behaviors through complete, independently
  reviewed compatibility slices;
- obtain separate port-ledger acceptance for each additional source behavior
  proven by subsequent implementation slices.

The source ledger contains all 2,748 pinned tests. It now records 52
independently reviewed green source tests; 2,599 remain explicitly unreviewed
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
| 3 — Compatibility edges | In progress | The runtime/contract slice includes reviewed JWT handling, trusted-proxy derivation, least-privilege role enforcement, identity cutover protection, a durable realtime projection, exact client funding history, and authenticated caller-scoped account summaries. Funding and account-summary implementation plus their separate port-ledger acceptance are landed. The fill-history foundation and its first two separately accepted source behaviors add an indexed newest-execution read with exact engine-time fidelity, immutable side/fill-ID filtering, and same-statement filtered totals; the external fills route remains inventory until its complete source contract is implemented and reviewed. A current candidate adds hash-only self-service API-key creation with encrypted durable replay, a PostgreSQL-authoritative owner cap, protected-client rate limiting, and an atomic audit fact. |
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
The client account-summary slice additionally has live PostgreSQL 17 HTTP
proof through the least-privilege API role, cross-user isolation, exact full
wire-shape assertions, and a contended additive-schema upgrade that rolls back
within the bounded lock window and retries without data loss. Its pinned source
behavior has separate port-ledger acceptance.
The current API-key candidate has focused PostgreSQL 17 HTTP proof for the
source `201`/`401` behavior, a concurrent race for the final owner slot,
hash-only secret persistence, exact actor/request audit attribution, direct
API-role mutation denial, and bounded migration rollback/retry. Review of its
first exact candidate rejected missing idempotent recovery, rate limiting,
durable policy authority, and source-compatible input behavior. The replacement
has focused live PostgreSQL 17 proof for encrypted replay after an injected
post-commit unknown outcome, concurrent duplicate delivery, retained-key
rotation, request-hash conflict, shared rate limiting, and source-compatible
names/scopes. The complete compatibility and PostgreSQL packages pass live
against disposable PostgreSQL 17, and repository-wide policy, formatting,
lint, unit, race, repeat, and vulnerability gates pass locally. Replacement
hosted validation and replacement independent review remain pending.
These results do not activate the external fills route or complete the
remaining Phase 3 scope.

## Next milestone

Review and land the user API-key implementation, accept its three source ports
in a separate governance change, then continue the remaining fill-history
behaviors and frozen compatibility operations through vertically narrow
implementation and ledger-acceptance changes. Phase 4 begins only after the
Phase 3 charter and release gates are fully satisfied.

The authoritative scope and completion criteria are in
`PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
