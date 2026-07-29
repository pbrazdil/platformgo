# platformgo

Clean-room Go replacement for `upcomers-org/platform`, using pinned source
tests as the executable specification. The intended production stack is Go,
PostgreSQL 19 or newer, NATS with JetStream, Centrifugo, and Hyperliquid first.
The current pre-release qualification is PostgreSQL 19 Beta 2 for development
and CI only; production requires PostgreSQL 19 GA and a separately reviewed
major-upgrade, backup-restore, recovery, and reconciliation rehearsal.

## Current status

Last updated: 2026-07-29

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
replay; runtime readiness/drain behavior; a frozen deployment-visible worker
health-listener address; a forward-only identity authority cutover guard;
direct Centrifugo contract proof; and a committed
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
Go classification as authoritative. Deterministic fill-history pagination is
now also landed and separately accepted: opaque strict
`(logical_time, fill_id)` cursors preserve newest-first order across identical
execution times, forward and backward traversal cannot repeat a boundary row,
and the filter-wide total remains stable even for an empty cursor window. This
acceptance intentionally preserves the current narrow internal Go projection;
it does not import the legacy mirror shape or activate the external fills
route. Per-fill position identity and realized PnL are now also landed and
separately accepted. Real engine decisions prove that hedged legs remain
distinct and expose only their own exact close realization, paired with its
settlement currency. Opening fills preserve the current Go absence of a
realization instead of manufacturing zero-valued money, and durable position
UUIDs remain authoritative.
Atomic fill settlement is now also landed and separately accepted. A real
fee-bearing execution proves that order, fill, position, exact balance,
balanced ledger, receipt, checkpoint, and outbox facts either commit together
or roll back together. Fresh-store recovery, retry, same-sequence replay, and
later-sequence redelivery cannot duplicate the fill or fee. Reconciliation
fails closed on an injected orphan projection without repairing immutable
history. Operational realtime lease time remains part of worker readiness, not
an implicit wall-clock input to a durable engine fault.

The first admin fleet-fills behavior is now separately accepted at an internal
production boundary. A trusted typed admin principal reads only whether
committed fills exist through the least-privilege PostgreSQL API role; on a
fresh database it receives non-null empty items and a present exact total of
zero. A client principal is rejected before any database read. Until a separate
source slice establishes non-empty fields and ordering, any durable fill fails
closed without exposing a partial page. The external `GET /admin/v1/fills`
route, admin credential transport, filters, pagination, and non-empty DTO
remain inventory. A forward ACL-only migration removes hostile direct table
and column grants—including grants inherited when `trading.fills` was created
under owner defaults—and dependent grant chains, while preserving only API
`SELECT` and engine `SELECT, INSERT`.

The admin fleet-orders foundation now exposes the same narrow internal
fresh-state boundary without inventing non-empty compatibility semantics. One
PostgreSQL statement observes both materialized orders and immutable admitted
order intents in one snapshot through the least-privilege API role. A trusted
typed admin receives non-null empty items and a present exact total of zero
only when both durable projections are empty; a client is rejected before the
reader, and any order or intent fails closed. A forward ACL-only migration
scrubs hostile direct, column, grant-option, and dependent grants from both
relations while preserving the existing API admission and engine writer
authority. The external orders route, admin credential transport, filters,
pagination, ordering, positive totals, and non-empty DTO remain inventory. The
mapped pinned source row is now separately accepted against the complete
pinned test context and the real production PostgreSQL boundary.

Terminal-only audit recovery is now also landed and separately accepted. A
pending command and its in-progress idempotency record remain recoverable
without manufacturing a terminal business receipt. The terminal retry commits
the accepted command, completed idempotency state, one immutable receipt, one
NETTING account projection, and its checkpoint atomically. Pre-commit rollback,
fresh ownership recovery, same-sequence replay, later-sequence redelivery, and
restart preserve exactly one terminal business fact and one account version;
every other economic, event, funding, market, ledger, and realtime projection
remains empty. The current Go command and receipt model is authoritative, and
the legacy saga schema and scheduler are not imported.

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

The landed rejected-order durability slice preserves the current deterministic
Go transition as authority. An admitted stop order moves from `working` without
a reason to terminal `rejected` with exact `slippage_exceeded`; its margin
reservation is released atomically without creating ledger money. The reason
and order version remain stable across duplicate delivery, restart,
reconciliation, and later market inputs. Decision-hash v3 binds complete exact
balance projections, recovery remains compatible with recorded v2 history,
markless books persist as SQL `NULL`, and the append-only currency-scale
registry prevents one currency code from acquiring conflicting scales. The
pinned rejection behavior now has separate owner-approved port-ledger
acceptance. The frozen external order contract is unchanged.

The landed frozen-effective-leverage slice preserves the current deterministic
Go execution-time risk authority. Every new decision-hash v4 fill freezes the
unique positive account/instrument risk leverage, or the instrument maximum
when no explicit risk exists, before hashing and atomic persistence. Canonical
`5.00` therefore becomes `5`; later configuration cannot rewrite an earlier
fill. Historical v2/v3 decisions remain verifiable with absent/SQL `NULL`
leverage. Decision, state, PostgreSQL projection, compatibility read, recovery,
duplicate delivery, and reconciliation must agree on the immutable value and
fail closed on disagreement. The pinned leverage behavior now has separate
owner-approved port-ledger acceptance; it does not activate the external fills
route.

The landed fill-reason slice derives each compatibility projection only from
durable canonical provenance. Exact `stop_loss` and `take_profit` bracket legs
take precedence over intent prefixes; exact `stopout:` and `flatten:` intents
map to liquidation and flatten; entry, absent, and ordinary intent provenance
map to manual. Both fill readers fail closed with no partial output when the
fill, order, intent, and command accounts disagree or when a stored bracket leg
is empty, padded, case-shifted, or unknown. Fresh least-privilege API-role
connections return the same immutable reasons without changing any durable
economic, audit, checkpoint, receipt, or outbox state. Its pinned source
behavior is separately accepted; the external fills route remains inventory.

The landed idempotency scope-gate slice proves the pinned broker behavior
through the production HTTP handler and PostgreSQL 19 Beta 2: an exact
`accounts:write` principal receives one committed account response, while an
exact `accounts:read` principal reusing the same body and idempotency key gets
`403` before body parsing, application entry, replay lookup, or durable
mutation. The result remains stable after API-pool and server reconstruction
under least-privilege database roles. Its direct committed-outbox delivery
evidence does not claim NATS-real or full-process restart coverage.

The current broker API-key echo slice now preserves the pinned authentication
and idempotency behavior through the production HTTP handler and PostgreSQL 19
Beta 2. Missing and invalid keys fail before durable work; repeated keys replay
the exact committed status, logical headers, and body bytes, including after a
renderer change, API reconstruction, concurrent cross-instance delivery, and a
lost post-commit HTTP outcome. PostgreSQL statement time owns the 24-hour
lifetime, raw external keys are reduced to fixed SHA-256 digests before
persistence, and the dedicated least-privilege authority permits only targeted
expired-key replacement or bounded expired-row purge. The API runtime owns that
retention path at startup and on one cancellation-owned periodic loop. An
immutable PostgreSQL policy caps the physical authority at 1,000 total rows and
100 rows per principal. Exact replay and deterministic hash conflict resolve
before capacity admission; genuinely new work at capacity receives typed
`429` with a PostgreSQL-derived `Retry-After`, while expired same-key
replacement remains net-zero. One policy-driven cleanup cycle drains at most
ten ordered 100-row batches under an overall deadline, and startup fails before
serving when authoritative coverage still contains expired, malformed, or
overlong live rows. A versioned time-authority discriminator preserves every
legacy creation and expiry timestamp while requiring all new
PostgreSQL-authoritative claims to retain for exactly 24 hours. A post-backfill
insert guard commits atomically with the discriminator so no intermediate tip
allows new rows to claim the legacy exemption, and a statement trigger prevents
owner `TRUNCATE`. The validated constraint and
aggregate integrity counters catch malformed responses, non-exact
current-authority lifetimes, and future-dated rows before readiness; older data
is restored only through an isolated full upgrade and reconciliation. PostgreSQL
19 Beta 2 proves a single cycle drains
`100 + 100 + 5` expired rows while live exact responses remain byte-identical,
including through the real `Serve` composition, injected periodic work,
integrity rejection, cleanup failure shutdown, and cancellation. Its four
forward migrations form an explicit no-overlap cutover with hard live-data and
physical-relation bounds, exact legacy and intermediate authority validation,
bounded documented `SHARE`, `ACCESS EXCLUSIVE`, and
`SHARE UPDATE EXCLUSIVE` locks, complete hostile-default-ACL normalization, an
immutable policy/ACL boundary, exact catalog classification, and atomic
intermediate-tip rollback/retry evidence.
The exact implementation passed independent migration, determinism, and release
review plus hosted CI before merge. Its pinned authentication and idempotency
behavior now also has separate semantic port-ledger acceptance through the real
HTTP and PostgreSQL boundary. PostgreSQL 19 Beta 2 remains development/CI-only;
production remains NO-GO until PostgreSQL 19 GA and the full upgrade, restore,
recovery, and reconciliation gate.

The realtime gateway source behavior is now also separately accepted through
the production PostgreSQL publication store, realtime worker, real Centrifugo
history, and production HTTP identity edge. One focused test proves a committed
`order.updated` envelope reaches the exact user channel at a positive history
offset without internal execution names, while password login and the scoped
token endpoint return one channel and anonymous access returns `401`. Additive
stable event identity and sequence remain required for retry and recovery; this
does not claim network-level exactly-once delivery.

The broker delegated-token source behavior is separately accepted through the
production HTTP identity edge, least-privilege API role, and PostgreSQL 19 Beta
2. One focused test proves that two independent requests from the same scoped
broker converge case-insensitive email to one tenant-owned identity, a requested
120-second client token authenticates the original profile, a broker without
`tokens:mint` receives `403`, and a well-formed unknown user receives `400`.
This slice does not claim broker-user exact-response replay, token-expiry
behavior, or account-claim propagation beyond the pinned source assertions.

Principal-scoped broker idempotency is separately accepted through production
HMAC authentication, the HTTP broker echo edge, the least-privilege API role,
and PostgreSQL 19 Beta 2. Two broker subjects in the same tenant may use one
identical idempotency key without cross-principal replay, while the first
subject's retry preserves its own exact durable status, logical headers, body,
creation time, and expiry. This acceptance reuses the landed broker-echo
authority and does not broaden its existing capacity, restart, or
unknown-outcome claims.

Broker account scope gating is separately accepted through production HMAC
authentication, HTTP, least-privilege API and engine roles, and PostgreSQL 19
Beta 2. A valid `accounts:read` credential may use scope-free broker ping but
receives `403` before account-request business work, while a distinct
wildcard-only credential uses internally generated request identity and
receives one complete current-Go `201` response backed by the committed
account, ownership, profile, command, and provisioning graph. The accepted
identifier deviation is stated below. This source acceptance does not add
replay, restart, concurrency, cross-tenant, or network-delivery claims.

Broker credential expiry is separately accepted through the production
configured-HMAC authority and HTTP edge. The same explicitly configured key is
valid before its one-second expiry and receives `401` after two seconds without
using a wall-clock sleep. The current Go fail-closed equality boundary is also
covered: a key is unauthorized at the exact expiry instant. This acceptance
does not claim dynamic broker-key creation, PostgreSQL broker-key authority,
revocation, restart, multi-replica clock consistency, or monotonic expiry
across a backward system-clock correction.

Trader password login and the caller profile are separately accepted through
the production HTTP edge, HMAC authentication, Argon2id verification,
least-privilege API role, and PostgreSQL 19 Beta 2. The successful client sees
the exact current Go `userId`, `login`, `email`, and `status` profile; a wrong
password, anonymous request, and correctly signed admin-audience token each
receive the generic `401` without an unauthorized session or client-rate
effect. Under the owner-approved current-Go identity decision, `kycStatus`
remains absent and the admin login plane remains unimplemented. This acceptance
does not claim KYC or durable status lifecycle, successful admin login, admin
credential/session authority, password-creation policy, refresh rotation or
revocation, restart, retry, or lost-acknowledgment behavior.

Native token placement is separately accepted through the production client
`POST /v1/auth/login` boundary, deterministic Argon2id verification, HMAC
access-token issuance, the least-privilege API role, and PostgreSQL 19 Beta 2.
A successful login returns the exact current Go `200`, emits no `Set-Cookie`
header, and places nonempty access and refresh tokens in the response body;
exactly one session stores only the SHA-256 refresh-token hash. The
owner-approved decision adapts the source's absent admin route and principal to
the existing client route without simulating an admin plane. This acceptance
does not claim successful admin login, admin credentials or sessions,
access-token validation, web-origin behavior, positive cookie behavior, CORS,
password-creation compatibility, refresh redemption or rotation, token-family
replay, logout or revocation, restart, retry, or lost-acknowledgment behavior.

Origin-bearing web-shaped login is separately accepted at that same production
client `POST /v1/auth/login` boundary with the least-privilege API role and
PostgreSQL 19 Beta 2. The response carries the exact statically configured
`Access-Control-Allow-Origin`, emits no `Access-Control-Allow-Credentials` or
`Set-Cookie`, returns both nonempty tokens in the body, and commits exactly one
session containing only the SHA-256 refresh-token hash. The owner-approved
decision preserves this current Go client behavior rather than simulating the
absent source-compatible admin/browser-login plane. This acceptance does not
claim dynamic origin reflection or allowlisting, successful admin login,
credentialed CORS, cookies, CSRF protection, access-token validation,
password-creation compatibility, refresh redemption or rotation, token-family
replay, logout or revocation, restart, retry, or lost-acknowledgment behavior.

Cross-account order ownership is separately accepted through production
password login, HMAC-authenticated HTTP, the least-privilege API role, and
PostgreSQL 19 Beta 2. A token whose PostgreSQL-derived account claims do not
contain the target receives the generic `403`; the observed foreign-request
effects are one caller-scoped rate claim and zero rows in the enumerated
command, shard, and economic graph. The same source-shaped request from the
durable owner receives the current Go `202` only after one atomic
idempotency/command/replay/order-intent/command-outbox admission. This
acceptance proves the current transport ownership boundary, not a second
application-layer ownership check, dynamic revocation of issued token claims,
engine or risk acceptance, order execution, fills, economic mutation, NATS
publication, replay, restart, or lost-acknowledgment behavior.

The flat-balance implementation and separately accepted source-port evidence cross
the deterministic engine,
PostgreSQL-authoritative ledger and balance projection, least-privilege API
role, authenticated ownership gate, and production HTTP reader. A canonical
`1000` USDC adjustment commits atomically as one balanced ledger effect and
the exact current Go five-field response remains stable through rollback,
retry, both duplicate-delivery forms, restart, and reconciliation. Malformed
durable currency or decimal values fail the whole read closed instead of
leaking a partial or non-contract response. Under the owner-approved current
Go projection decision, the edge returns committed `total`, `used` as
`locked`, `free`, and `equity`; it does not recompute the source's nine-field
flat view or claim `crossEquity`, `unrealizedPnl`, `marginRatio`, or
`maintenanceMargin`.

The working-order reservation implementation preserves the owner-approved
current Go authority: the maximum authoritative price, exact initial margin,
and worst non-negative prospective fee produce `locked=45/free=9955` for the
pinned source-shaped order and `54/9946` when a higher mark controls. The
production command path binds its mandatory unresolved sentinel to the
committed shard-wide JetStream market-state watermark inside the serialized
engine owner; restart and redelivery retain the original command fence even
after later market events. The migration's runtime revision fence rejects a
previous engine's ownership-epoch write before it can publish readiness when it
verified the old schema before cutover and resumed only after commit.
PostgreSQL 19 Beta 2 evidence covers atomic rollback/retry, both duplicate
forms, exact half-even rounding, cancel release exactly once, durable
recovery/reconciliation, realtime effects, authenticated ownership, and zero
reservation ledger entries. The mapped pinned row remains unreviewed until its
separate source-acceptance layer lands. This evidence does not claim venue
freshness, source-sequence gap recovery, or authoritative snapshot reload;
those fail-closed Hyperliquid lifecycle controls remain Phase 4 work.

Phase 3 is not complete. The runtime must still:

- implement and semantically review the remaining in-scope frozen HTTP, admin,
  broker, gRPC, realtime, and deployment-role contracts;
- establish a complete refresh lifecycle before the cookie-refresh source rows
  can leave placeholder state; current Go has no production refresh handler,
  and route `404` must not be mislabeled as implemented compatibility;
- continue the remaining fill-history behaviors through complete, independently
  reviewed compatibility slices;
- obtain separate port-ledger acceptance for each additional source behavior
  proven by subsequent implementation slices.

The source ledger contains all 2,748 pinned tests. It records 79 independently
reviewed green source tests; 2,572 remain explicitly unreviewed
placeholder ports, and 97 implementation-only tests are reviewed and excluded
with decision records. Ledger acceptance remains separate from implementation,
as required by repository governance.

The broker scope-gate acceptance preserves the pinned authorization sequence:
an `accounts:read` credential may probe ping but cannot create an account, while
a distinct wildcard credential can create one committed account. Its response
uses the owner-approved current Go UUID account URN and current user URN,
matched field-for-field to PostgreSQL intent, ownership, and profile state.
This is an explicit deviation from the pinned Rust base62 identifier
deserializers, not a claim of source-typed `AccountView` compatibility.

This repository is not yet a production-capable replacement.

## Delivery progress

| Phase | Status | Evidence and remaining work |
|---|---|---|
| 0 — Policy and test harness | Complete | Machine-readable policy, exact decimals, deterministic time and IDs, test fixture, full source inventory, provenance ledger, and protected hosted checks. |
| 1 — Pure engine | Complete | Deterministic order lifecycle, matching, fills, positions, PnL, margin, funding, liquidation, brackets, triggers, and fees with exact-value and replay coverage. |
| 2 — Durable execution | Complete | PostgreSQL authority, historically completed and validated on PostgreSQL 17; immutable checksum-verified migrations, atomic economic commits, command/idempotency journal, durable ownership and ordering, JetStream outbox/inbox, recovery, and reconciliation. |
| 3 — Compatibility edges | In progress | The runtime/contract slice includes reviewed JWT handling, trusted-proxy derivation, least-privilege role enforcement, identity cutover protection, a durable realtime projection, exact client funding history, authenticated caller-scoped account summaries, an exact flat-balance read through the PostgreSQL-authoritative engine projection, and hash-only self-service API-key creation with encrypted durable replay, a PostgreSQL-authoritative owner cap, protected-client rate limiting, and an atomic audit fact. Funding, account-summary, flat-balance, API-key, and realtime gateway implementation plus their separate port-ledger acceptance are landed. The working-order implementation preserves current Go maximum-price and prospective-fee reservation authority, exact PostgreSQL balance/realtime/recovery semantics, and production JetStream market-watermark binding; its separate source acceptance remains pending. The fill-history foundation and its first nine separately accepted source behaviors add an indexed newest-execution read with exact engine-time fidelity, immutable side/fill-ID filtering, same-statement filtered totals, a correlatable fill-to-order identity using the current Go `urn:xb:order:<UUID>` representation, exact classified side/trade-type and durable reason projection, strict deterministic tuple pagination with stable filter-wide totals, per-position exact realized PnL/currency that keeps hedged legs isolated, atomic fee-bearing order/fill settlement across rollback, retry, duplicate delivery, restart, and fail-closed reconciliation, and decision-hash v4 frozen execution-time leverage shared by decisions, durable state, PostgreSQL, recovery, and reconciliation while retaining v2/v3 history. The first admin fleet-fills source behavior is separately accepted through an internal admin-before-PostgreSQL gate and an empty-only existence projection with hostile-ACL cleanup; the external route and all non-empty semantics remain inventory. The first admin fleet-orders source behavior is separately accepted through an internal admin-before-PostgreSQL gate, a single-snapshot empty-only predicate over materialized orders plus immutable intents, exact hostile-ACL cleanup on both relations, and a pre-revocation writer fence; all external/non-empty order semantics remain inventory. Terminal-only audit recovery is separately accepted through the current Go command/idempotency/receipt authority, including transaction rollback, duplicate delivery, restart, exact permitted-effect, and full durable-projection evidence. Rejected-order durability is also landed and separately accepted using the deterministic current-Go transition: exact terminal reason, reservation release, duplicate/restart/reconciliation stability, historical decision-hash v3 balance authority, markless recovery, and monotonic currency scales. Current Go fill semantics and durable identities remain authoritative, and the external fills route remains inventory until its complete source contract is implemented and reviewed. |
| 4 — Hyperliquid production integration | Not started | Protocol fixtures, controlled live canaries, reconnect and gap handling, capacity/soak validation, and incident drills. |
| 5 — Replacement rehearsal | Not started | Production-like data import, close-only/drain/cutover rehearsal, rollback and reconciliation plan, and audited go-live decision. |

## Validation snapshot

The landed Phase 3 slices have passed repository-wide formatting, lint, unit,
race, repeat, vulnerability, module-consistency, and policy checks. Historical
slices were originally validated against disposable PostgreSQL 17; current CI
and every newly qualified database slice use PostgreSQL 19 Beta 2 alongside
NATS and Centrifugo. Their exact SHAs are green in hosted CI. The frozen
deployment manifest also covers the worker's production
`UZO_HTTP_HEALTH_ADDR` bootstrap key, whose typed parsing and real
`/healthz`/`/readyz` listener wiring are exercised by native Go tests. The
realtime path additionally has live PostgreSQL 19 Beta 2
commit/order/retry evidence, a simulated Centrifugo outage proof, real
Centrifugo history and scoped-token contract proof, independent determinism and
money review, an indexed representative-backlog claim plan, and migration 011
bounded-lock rollback/retry proof. Exact funding history and its source-port
evidence are green and landed through hosted CI. The current fill-history
foundation has
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
The landed pagination slice additionally proves on PostgreSQL 17 that strict
same-time tuple cursors traverse each immutable fill exactly once in both
directions, malformed cursors fail closed, and the filter-wide total survives
empty cursor windows. Exact production forward, backward, and side-filtered
plans over 100,000 fills use the existing account-history indexes and reject a
sequential scan. Its separate source acceptance preserves the current narrow
internal Go projection and does not activate an external route.
The landed hedged-realization slice additionally drives real deterministic
engine decisions through PostgreSQL 17. A long leg realizes exactly `50 USDC`
and a separate short leg realizes exactly `30 USDC`; each close retains its
own durable position UUID, while opening fills retain absent realized amount
and currency. PostgreSQL and both readers enforce the amount/currency pair,
and the exact 100,000-fill production plans remain indexed. Its separate
source acceptance records the owner-approved current-Go semantics and does not
activate an external route.
The landed atomic-settlement slice additionally proves on PostgreSQL 17 that a
fee-bearing fill and its order, position, exact balance, balanced ledger,
receipt, checkpoint, and outbox effects share one transaction. A fresh store
recovers the pre-commit rollback state before the exact retry; same-sequence
replay and later-sequence redelivery add no second fill or fee, and restart
replay reproduces the final state hash. Injected orphan projection corruption
fails readiness closed without repair. Its separate source acceptance records
the owner-approved current-Go authority, while realtime lease timestamps remain
operational readiness metadata rather than durable reconciliation input.
The landed terminal-only audit slice additionally proves on PostgreSQL 17 that
the recoverable pending command has no terminal business receipt, a
post-persist/pre-commit fault leaves every terminal and economic projection
absent, and the exact retry commits one accepted command, completed idempotency
record, immutable receipt, NETTING account version, and checkpoint atomically.
Same-sequence replay, later-sequence duplicate delivery, exact duplicate replay,
and restart retain one terminal business fact while every forbidden economic,
event, ledger, funding, market, and realtime projection remains empty. Its
separate source acceptance records the owner-approved current-Go command and
receipt authority without importing legacy saga storage.
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
The rejected-order durability slice has PostgreSQL 17 proof for pre-commit
rollback, exact duplicate replay, working and terminal restart recovery,
reconciliation, immutable rejection reason/version, exact reservation release,
mixed v2/v3 history, markless restoration without manufactured ledger money,
malformed-history rollback, old-writer fences, and append-only currency-scale
authority. Its exact implementation candidate passed hosted CI 7/7 plus
independent determinism, money, PostgreSQL-foundation, and release reviews
before merge. The owner-approved source adaptation is separately accepted in
the port ledger.
The frozen-effective-leverage slice has PostgreSQL 19 Beta 2 proof for exact
execution-time values `10` and canonical `5`, the instrument-maximum default,
immutable v2/v3 absence, v4 decision/state hashes, atomic fill persistence,
same-sequence replay, later-sequence duplicate delivery, fresh-owner recovery,
and fail-closed reconciliation. Its additive migrations 009 and 010 preserve
old fill pages without backfill, reject invalid preexisting risk authority,
fence old engine receipt/fault/checkpoint writers, and validate the
positive-value constraint under bounded locks. The exact implementation
candidate passed hosted CI 7/7 plus independent
determinism, money, migration, and release reviews before merge. PostgreSQL 19
Beta 2 remains a nonproduction qualification; this evidence is not a
production major-upgrade rehearsal. The owner-approved source adaptation is
separately accepted in the port ledger.
The landed fill-reason slice has PostgreSQL 19 Beta 2 proof through both
production compatibility readers and distinct least-privilege API-role
connections. It preserves exact bracket precedence, liquidation, flatten, and
manual mappings; rejects corrupt bracket or cross-account authority with no
partial page or view; and leaves exact orders, fills, intents, commands,
ledger, receipts, checkpoints, and outbox state unchanged. The strengthened
native source port was first proven red against the pre-implementation commit,
then passed race and repeated deterministic execution. Its independent
semantic review and separate ledger acceptance are landed.
The current upgrade-rehearsal candidate additionally uses the exact final Go
decision-hash v3 artifact to create the invalid pre-009 authority through
the durable command journal, outbox, and audited single-writer engine inputs on
PostgreSQL 19 Beta 2. Migration 009 rejects that state atomically; an active
order blocks the first correction; normal v3 cancellation and
risk-configuration inputs repair it; and fresh v3 plus current v4 processes
recover the same corrected immutable history across the successful 009/010
retry. The current production messaging store then claims all six pending v3
outbox messages and records deterministic publisher acknowledgments. Its four
command redeliveries become decision-hash v4 duplicate receipts at later
sequences, with no new balance, ledger, risk, order, fill, position, or event
effect; the two domain events are acknowledged, every immutable publication
row retains its exact sequence and attempt metadata, the pending outbox reaches
zero, and a fresh current recovery and reconciliation remain exact. No direct
SQL economic repair or production historical-writer switch is introduced.
Live readiness, JetStream consumer acknowledgment, and process-drain
orchestration remain outside this database upgrade test.
Production additionally remains blocked on PostgreSQL 19 GA, a production-like
post-correction backup/restore proof, commit-acknowledgment outcome resolution
for migrations 009/010, production-scale lock timing, proof that every deployed
shard ID fits migration 009's signed-int32 advisory-lock key domain (or a
separately reviewed forward compatibility path), and proof that every old
runtime role and database pool is gone throughout the cutover.
These results do not activate the external fills route or complete the
remaining Phase 3 scope.

## Next milestone

Continue the remaining fill-history behaviors and frozen compatibility
operations through vertically narrow implementation and ledger-acceptance
changes. Phase 4 begins only after the Phase 3 charter and release gates are
fully satisfied.

The authoritative scope and completion criteria are in
`PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
