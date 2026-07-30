# platformgo

Clean-room Go replacement for `upcomers-org/platform`, using pinned source
tests as the executable specification. The intended production stack is Go,
PostgreSQL 19 or newer, NATS with JetStream, Centrifugo, and Hyperliquid first.
The current pre-release qualification is PostgreSQL 19 Beta 2 for development
and CI only; production requires PostgreSQL 19 GA and a separately reviewed
major-upgrade, backup-restore, recovery, and reconciliation rehearsal.

## Current status

Last updated: 2026-07-30

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
The pinned direct-outbox drain behavior is also separately accepted through
the production PostgreSQL store and real JetStream: one direct batch publishes
one pending operational message with its exact subject and stable outbox ID,
records the JetStream acknowledgment sequence, and permits a synchronously
confirmed durable-consumer acknowledgment.
The pinned inbox-deduplication behavior is separately accepted at its narrow
non-economic boundary. Three real JetStream deliveries carrying application
IDs `dup`, `dup`, and `other` pass through one production PostgreSQL
`MessagingStore.ApplyInbox` consumer scope, commit exactly two test projection
effects with two inbox receipts, and are synchronously acknowledged only after
the inbox transaction returns. This does not claim a generic production
projector, transport-level exactly-once delivery, restart recovery, conflicting
payload detection, or idempotency of any economic effect.
The pinned bus roundtrip and confirmed-batch behaviors are separately accepted
only at a non-economic JetStream adapter boundary. One test-constructed
operational message round-trips through the production Publisher, while three
distinct operational messages are published sequentially, receive positive
broker acknowledgments, deliver the exact expected IDs, subjects, and payloads,
and are synchronously acknowledged. The directly constructed `OutboxMessage`
values are test fixtures; this acceptance neither proves nor authorizes
production publication without a committed PostgreSQL outbox. It does not
establish an atomic batch API, delivery ordering, outbox transactions or
marking, partial-failure or retry semantics, unknown-outcome handling,
redelivery, inbox or business effects, restart recovery, exactly-once
transport, runtime composition, or economic idempotency.
The pinned redelivery and dead-letter behavior is separately accepted at a
test-owned, non-economic JetStream adapter boundary. One operational message
is delivered once, explicitly negatively acknowledged, and redelivered with
the same identity, subject, payload, and stream sequence. A distinct,
positively acknowledged test quarantine record preserves its provenance and
exact failure reason before the source delivery is synchronously acknowledged;
the same durable source consumer has no third delivery, while a fresh repair
consumer can read the record. This is not a production consumer, automatic
retry cap, generic quarantine policy, PostgreSQL outbox/inbox or
commit-before-ack proof, atomic move, crash/restart or unknown-outcome
recovery, operator repair/replay procedure, exactly-once guarantee,
engine-input poison policy, source-record deletion, or economic-effect claim.
The durable outbox table now also has an explicit hostile-default-resistant
PostgreSQL ACL boundary. A forward-only, writer-fenced migration removes every
unlisted table and column grant, grant option, and dependent chain while
preserving exactly the existing API producer, engine producer, and outbox
publisher privileges. It changes no outbox row, index, trigger, producer
authority, claim order, retry, acknowledgment, worker polling, or publication
identity. The pinned coalesced-doorbell row is accepted separately under the
owner-approved current-Go adaptation: a row committed after production-worker
readiness reaches real JetStream through the fixed 100ms PostgreSQL-authoritative
poll inside the source bounds with exact identity and a durable published mark.
This does not claim `LISTEN`/`NOTIFY`, 50ms coalescing, a configurable 30-second
fallback, notification-loss recovery, or listener reconnect behavior.
Unfunded opening-order rejection is separately accepted at the current Go
durable engine boundary. An absent or exact-zero USDC balance produces the
atomic PostgreSQL result `rejected / insufficient_funds` without an order,
reservation, fill, position, ledger, domain-event, or realtime effect, and the
result remains stable through rollback, retry, duplicate delivery, restart,
and reconciliation. No additional engine/domain/realtime delivery effect is
created beyond command admission and duplicate audit. This intentionally does
not claim the pinned Rust
synchronous application error, its `free margin` message, or an HTTP status.
The landed finite fill-leverage hardening preserves valid and
historical `NULL` fill behavior while making both compatibility readers fail
the complete read closed on a non-finite or non-positive durable leverage.
Separate forward migrations add an immediately enforced `NOT VALID`
finite-positive check behind the configured-shard engine-owner fence, without
scanning or rewriting fill pages, then validate it in a bounded scan. The
stopped-runtime intermediate and final schema tips are exactly 35 and 36 files.
A preexisting numeric `NaN` leaves the database at the 35-file tip after
SQLSTATE `23514`; operators must not retry or repair that history and must
remain halted for a reviewed forward or owner decision. Only an explicit owner
authorization permits a complete verified pre-constraint restore followed by
the exact prior artifact, fresh recovery, and full reconciliation.
The broker account-fills route now preserves the accepted exact current-Go
fill page while authorizing `Principal.Tenant` through both durable ownership
and account-profile authorities in the same PostgreSQL statement that returns
the page and filter-wide total. API-key subject is not tenant authority.
Authentication and exact scope dominate strict parsing, foreign empty, valid,
or corrupt histories return the same generic denial, and authorized projection
corruption returns no partial page. Both reverse cursor directions are covered
through the real least-privilege PostgreSQL 19 Beta 2 HTTP boundary. The frozen
broker OpenAPI, compatibility manifest, generated artifact hash, runtime
wiring, and intentional-deviation record activate atomically. Moving keyset
cursors remain ordinary committed history rather than replay or catch-up.
The broker account-balances route now returns the deterministic
five-field exact balance projection while authorizing `Principal.Tenant`
through both durable account authorities in the same PostgreSQL statement that
returns balances and currency-scale authority. API-key subject is not tenant
authority, unauthorized corrupt rows cannot influence error selection,
authorized corruption fails the complete response closed, and bytewise `C`
ordering makes response bytes independent of database locale. The runtime,
frozen broker OpenAPI, compatibility manifest and hash, and intentional
deviation landed atomically. Full local PostgreSQL 19 Beta 2 validation,
exact-SHA money, determinism, security, and independent final review, and all
seven hosted CI jobs passed on the accepted tree before merge. Focused edge,
adapter, runtime, contract, and PostgreSQL exact-output, 18-case authority,
hostile-collation, corruption, snapshot-transition, cancellation, restart, and
ledger-fold tests are green.
The broker account-funding route now preserves the accepted full current-Go
funding page, including broker `accountLogin`, exact decimal strings,
nanosecond times, moving tuple cursors, and a first-page total. Authentication
and `accounts:read` scope dominate parsing; `Principal.Tenant` must match both
durable account authorities before the same PostgreSQL statement can invoke
the funding reader or counter. Foreign and missing accounts fail identically,
while any late identifier, currency-scale, non-finite, or non-positive-oracle
corruption rejects the whole response without a valid prefix. Forward
migration 40 reconstructs immutable receipt-derived instrument provenance,
validates historical price/quantity values against their exact revision,
rejects orphan or inconsistent history, and advances the engine ownership
revision fence. It also removes hostile funding table, column, and function
grants, restores only the engine/API allowlist, and leaves both constraint
helpers uncallable by runtime roles. The client funding route and its accepted
projection remain unchanged.
The landed broker account point read preserves the accepted
ten-field current-Go account summary while retaining the pinned source's
generic `400 unknown account` for both absent and foreign ownership.
Authentication and exact scope dominate canonical account parsing; one
PostgreSQL statement anchors `Principal.Tenant` on durable ownership and uses
nullable projection joins only to make tenant-owned incomplete or corrupt
graphs fail closed as opaque `503`. The route performs no write, migration,
scale lookup, or rounding. Focused application, edge, contract, and real
least-privilege PostgreSQL 19 Beta 2 HTTP tests are green, including exact and
wildcard scope, exact wire bytes, zero pre-read SQL on gate failures, one SQL
statement per valid authorized request, absent/foreign equivalence, foreign
corruption non-disclosure, owned corruption and incompleteness, restart, and
unchanged relation digests. Red-first corrections also reject unsupported base
currency and finite PostgreSQL timestamps outside RFC3339's wire range. The
full exact-tree local gate, money, determinism, security, migration, and
independent release reviews, and all seven hosted CI jobs passed before PR
#140 landed as `1b7474d574a517104e6802f7fbf6c241c65cd5e0`. The composite
pinned source ledger row remains
unreviewed because its account-list, balance-mutation, and channel-isolation
assertions are outside this narrow route.
The active broker account-list slice preserves the pinned unpaged tenant list
with the accepted ten-field current-Go account summary and optional canonical
`userId` filter. Two fixed one-statement PostgreSQL queries anchor
`Principal.Tenant` before any nullable projection join, return absent and
foreign filters as exact `[]`, validate the complete list before writing HTTP
bytes, and sort by login plus bytewise account ID. A new forward-only
tenant-leading ownership index plus a one-time tenant/user filter check removes
cross-tenant and foreign zero-result scan amplification without adding
pagination or a compatibility-breaking cap.
Real PostgreSQL 19 Beta 2 red-first coverage now proves route activation,
auth/scope/parser zero-SQL dominance, exact/wildcard scope, filtering, tenant
isolation, deterministic ties, foreign corruption isolation, late owned
corruption as opaque whole-response `503`, reconstruction, one statement per
valid request, and unchanged relation digests. Migration upgrade/lock/plan
evidence and full replacement-candidate validation are green after the
final-review account-identifier correction. Fresh exact-SHA migration,
security, money, determinism, and independent release reviews reported no
P0-P3 finding; all seven hosted CI jobs passed on the reviewed SHA; and PR
#143 landed the route as `35389a19fc7265743dc0e6930e4be443307eb63e`.
Its forward-only, catalog-only ACL migration scrubs hostile table, column,
grant-option, and dependent grant-chain privileges from both tenant
authorities and `ledger.balances`, then restores only the exact non-grantable
API/engine allowlist. All three bounded `SHARE` locks precede the first ACL
change in production writer order. PostgreSQL 19 Beta2 tests prove hostile
current-main upgrade, unchanged rows/filenodes/defaults and prior checksums, a
transaction-wide rollback when the final balance lock times out, safe retry,
and a production-order writer completing without deadlock.
The landed currency-scale authority fence addresses the release-blocking
defect discovered during the next Phase 3 route preflight. A
single forward-only migration fences the engine and all registry writers,
validates the append-only registry bidirectionally against current instruments
and accepted historical instrument changes, neutralizes previously created or
already-running hostile definer triggers with an `ENABLE ALWAYS` table guard,
scrubs every named table/column/function grant, and advances the runtime schema
revision. It never edits frozen migrations or repairs registry facts. Focused
PostgreSQL 19 Beta 2 exploit, rollback, upgrade, recovery, and exact-catalog
evidence and the complete stable-candidate local validation gate are green.
Exact-SHA migration, determinism, exact-money, and independent final reviews
reported no P0-P3 finding. Hosted CI passed all seven jobs on the reviewed SHA,
and PR #137 landed the correction on `main`.
Command-admission replay authority is now also hardened by a landed
forward-only PostgreSQL 19 Beta 2 migration. It fences engine and API writers,
removes hostile inherited table and column privileges from commands,
idempotency records, and immutable replay responses, and adds statement-level
truncate guards without changing rows or relation storage. Focused hostile
upgrade, rollback/retry, executable replay-protection, and runtime-role tests,
the full local gate, four exact-SHA reviews, and all seven hosted checks passed
before merge.
The current source-port candidate separately accepts the pinned reused-intent
behavior at the owner-approved current Go boundary. Intent `idem-1` maps to the
transport-derived key `intent:idem-1`; concurrent identical submissions admit
one exact PostgreSQL command/idempotency/replay/order-intent/outbox graph, a
fresh journal returns its byte-exact stored `202`, and a changed payload under
that key conflicts. This acceptance does not impose global intent uniqueness
across arbitrary explicit keys or claim synchronous engine, risk, or
materialized-order behavior.
Exact client funding history is also landed with immutable PostgreSQL
projection integrity, nanosecond keyset pagination, exact aggregation,
least-privilege reads, populated-schema upgrade coverage, and separate
source-port acceptance.

The fill-history foundation is also landed: its indexed newest-execution read
preserves the immutable engine execution time exactly, and its first
source-derived behavior has separate port-ledger acceptance. The authenticated
external fills route now separately exposes the accepted narrow current-Go
projection. The landed filtering slice adds account-scoped,
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

The admin fleet-positions foundation now provides the corresponding narrow
internal fresh-state boundary. Authorization precedes the database read, and
one PostgreSQL snapshot observes both the mutable position projection and
immutable fills through the least-privilege API role. A trusted typed admin
receives non-null empty items and a present exact total of zero only when both
relations are empty; any durable position or fill fails closed without a
partial page. A forward ACL-only migration scrubs hostile direct, column,
grant-option, and dependent grants from `trading.positions`, fences
pre-revocation writers, and preserves only API `SELECT` plus engine
`SELECT, INSERT, UPDATE`; the already-frozen fills ACL and both relations'
durable data remain unchanged. The external positions route, admin credential
transport, filters, ordering, cursors, pagination, positive totals, and
non-empty DTO remain inventory. Its mapped pinned source row is now separately
accepted against the complete pinned test context and the real production
PostgreSQL boundary.

The admin risk-monitor foundation now preserves the next source-proven
fresh-state boundary without inventing risk values. A trusted typed admin
executes one narrow PostgreSQL-authoritative boolean function and receives a
non-null empty account slice only when no durable account, command, shard,
balance, ledger transaction, or ledger entry exists. A client is rejected
before the reader, and any detected durable state fails closed without a
partial result. The `SECURITY DEFINER` function exposes no rows or economic
values, grants execution only to the API role, and scrubs hostile function
defaults and dependent grant chains. Its forward migration also restores the
existing exact account-lifecycle table authorities under the deadlock-safe
`accounts` → `account_shards` → `account_provisioning_intents` lock order.
The external risk route, admin credential transport, thresholds, formulas,
ordering, pagination, populated DTO, and every non-empty risk semantic remain
inventory. Its mapped pinned source row is now separately accepted against the
complete pinned test context and the real production PostgreSQL boundary.

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
behavior is separately accepted. The authenticated external route now
separately exposes that same fail-closed reason projection.

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
reservation ledger entries. Its mapped pinned source row is now separately
accepted under the owner-approved current-Go economic authority decision. This
evidence does not claim venue freshness, source-sequence gap recovery, or
authoritative snapshot reload;
those fail-closed Hyperliquid lifecycle controls remain Phase 4 work.

Phase 3 is not complete. The runtime must still:

- implement and semantically review the remaining in-scope frozen HTTP, admin,
  broker, gRPC, realtime, and deployment-role contracts;
- establish a complete refresh lifecycle before the cookie-refresh source rows
  can leave placeholder state; current Go has no production refresh handler,
  and route `404` must not be mislabeled as implemented compatibility;
- continue the remaining frozen compatibility operations through complete,
  independently reviewed vertical slices;
- obtain separate port-ledger acceptance for each additional source behavior
  proven by subsequent implementation slices.

The source ledger contains all 2,748 pinned tests. It records 91 independently
reviewed green source tests; 2,560 remain explicitly unreviewed placeholder
ports, and 97 implementation-only tests are reviewed and excluded with
decision records. The production permission-catalog test freezes the exact
static 11-resource and four-action source catalog plus client denial and proves
fresh return slices. Its application handler returns that exact ordered catalog
to admin-audience principals and fails every other audience closed before
returning data. It is not runtime- or HTTP-wired, and does not claim the source
`Roles:Read` dispatcher policy, production admin credentials, or the external
admin HTTP route. Ledger acceptance remains separate from implementation, as
required by repository governance.

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
| 2 — Durable execution | Complete | PostgreSQL authority, immutable checksum-verified migrations, atomic economic commits, command/idempotency journal, durable ownership and ordering, JetStream outbox/inbox, recovery, and reconciliation. Historical slices first passed on PostgreSQL 17; the supported development/CI minimum and all current qualification are PostgreSQL 19 Beta 2. |
| 3 — Compatibility edges | In progress | The runtime/contract slice includes reviewed JWT handling, trusted-proxy derivation, least-privilege role enforcement, identity cutover protection, a durable realtime projection, exact client and broker funding history, authenticated caller-scoped account summaries, an exact flat-balance read through the PostgreSQL-authoritative engine projection, and hash-only self-service API-key creation with encrypted durable replay, a PostgreSQL-authoritative owner cap, protected-client rate limiting, and an atomic audit fact. Funding, account-summary, flat-balance, API-key, realtime gateway, and authenticated account-fill HTTP implementations plus their applicable separate port-ledger acceptance are landed. The separately accepted working-order source behavior preserves current Go maximum-price and prospective-fee reservation authority, exact PostgreSQL balance/realtime/recovery semantics, and production JetStream market-watermark binding under the owner-approved economic deviation. The fill-history foundation and its first nine separately accepted source behaviors add an indexed newest-execution read with exact engine-time fidelity, immutable side/fill-ID filtering, same-statement filtered totals, a correlatable fill-to-order identity using the current Go `urn:xb:order:<UUID>` representation, exact classified side/trade-type and durable reason projection, strict deterministic tuple pagination with stable filter-wide totals, per-position exact realized PnL/currency that keeps hedged legs isolated, atomic fee-bearing order/fill settlement across rollback, retry, duplicate delivery, restart, and fail-closed reconciliation, and decision-hash v4 frozen execution-time leverage shared by decisions, PostgreSQL, recovery, and reconciliation while retaining v2/v3 history. Its client HTTP route freezes the narrow current-Go projection, authenticated account ownership, strict external query parsing, moving committed keyset semantics, exact JSON nullability, and registered-currency validation of realized PnL without rounding. The first admin fleet-fills source behavior is separately accepted through an internal admin-before-PostgreSQL gate and an empty-only existence projection with hostile-ACL cleanup; the admin external route and all non-empty semantics remain inventory. The first admin fleet-orders source behavior is separately accepted through an internal admin-before-PostgreSQL gate, a single-snapshot empty-only predicate over materialized orders plus immutable intents, exact hostile-ACL cleanup on both relations, and a pre-revocation writer fence; all external/non-empty order semantics remain inventory. The first admin fleet-positions source behavior is separately accepted through an internal admin-before-PostgreSQL gate, a single-snapshot empty-only predicate over positions plus immutable fills, exact hostile-ACL cleanup on positions, and a pre-revocation writer fence; every external/non-empty position semantic remains inventory. The first admin risk-monitor source behavior is separately accepted through an admin-before-PostgreSQL empty-only boolean over all current durable account/economic roots, exact hostile `SECURITY DEFINER` ACL cleanup, and deadlock-safe lifecycle-table ACL fencing; the external route and every non-empty risk semantic remain inventory. Terminal-only audit recovery is separately accepted through the current Go command/idempotency/receipt authority, including transaction rollback, duplicate delivery, restart, exact permitted-effect, and full durable-projection evidence. Rejected-order durability is also landed and separately accepted using the deterministic current-Go transition: exact terminal reason, reservation release, duplicate/restart/reconciliation stability, historical decision-hash v3 balance authority, markless recovery, and monotonic currency scales. Unfunded opening-order rejection is separately accepted at the current Go durable engine boundary for both absent and exact-zero USDC balances, with atomic `insufficient_funds`, no additional economic/domain/realtime delivery effect beyond command admission and duplicate audit, and rollback/duplicate/restart/reconciliation stability; the Rust synchronous application error remains outside the accepted contract. The pinned coalesced-doorbell behavior is separately accepted only as a real post-readiness PostgreSQL commit reaching JetStream through current Go's fixed 100ms authoritative poll; no PostgreSQL notification or coalescing mechanism is claimed. |
| 4 — Hyperliquid production integration | Not started | Protocol fixtures, controlled live canaries, reconnect and gap handling, capacity/soak validation, and incident drills. |
| 5 — Replacement rehearsal | Not started | Production-like data import, close-only/drain/cutover rehearsal, rollback and reconciliation plan, and audited go-live decision. |

## Validation snapshot

The currency-scale authority fence has completed migration,
determinism, and exact-money adversarial preflight. A PostgreSQL 19 Beta 2
red-first test failed on the missing forward migration before implementation,
then passed once the atomic candidate and runtime revision fence existed. Its
focused PG19 suite now covers hostile source grants, accepted-receipt
provenance, full latest-snapshot/version equality, canonical exact-value
domains, non-accepted history, committed pre-revocation writers, a previously
loaded definer body, exact trigger/ACL restoration, bounded same-scale writes,
rollback, and retry. The complete PostgreSQL integration package is green on
local PostgreSQL 19 Beta 2, and the migration, determinism, and exact-money
advisory correction reviews report no remaining P0-P3 finding. The stable
candidate completed policy, format, lint, full test, race, repeat, and
vulnerability validation. Exact-SHA migration, determinism, exact-money, and
independent final reviews reported no P0-P3 finding, and all seven hosted CI
jobs passed on the reviewed SHA. PR #137 merged the correction as
`d380c12ccb7614db7be9ac779ed08f61aa364830`. The broker account point-read
route is now landed after red-first application, edge, and PostgreSQL 19 Beta
2 validation; a full stable-candidate gate; exact-SHA money, determinism,
security, PostgreSQL/migration, and independent release reviews with no P0-P3
findings; and all seven hosted checks on the reviewed candidate SHA.
The broker account-list slice is landed. It includes the 39th forward
migration, fixed filtered/unfiltered query templates, per-request custom
planning, executed-plan amplification evidence, and list-specific canonical
account-URN validation without tightening accepted current-Go user URNs.
Policy, format, lint, full test, race, deterministic repeat, vulnerability,
complete PostgreSQL 19 Beta 2, and complete compatibility validation passed on
the replacement candidate. Fresh exact-SHA migration, security, money,
determinism, and independent release reviews reported no P0-P3 finding, all
seven hosted CI jobs passed, and PR #143 merged as
`35389a19fc7265743dc0e6930e4be443307eb63e`.

The broker account-funding candidate now has PostgreSQL 19 Beta 2 proof for
tenant-safe one-statement reads, exact historical instrument provenance,
first/forward/backward whole-page corruption refusal, deterministic
same-receipt instrument revision selection, transactional backfill, hostile
trigger/function/ACL refusal, old-runtime fencing, duplicate/restart behavior,
and fail-closed bidirectional reconciliation. Its documented stable candidate
passed policy, format, lint, complete test, race, deterministic repeat, and
vulnerability gates locally. Implementation-boundary money, migration, and
determinism reviews report no remaining P0-P3 finding. Fresh exact-SHA
specialist and independent release review, hosted CI, and merge remain pending.

The landed finite fill-leverage hardening has PostgreSQL 19 Beta 2
tests defining its corruption-read, no-partial-page, restart, exact 35/36-tip,
no-rewrite, finite-write, bounded lock timeout/retry, representative
100,000-row bounded validation, and preexisting-`NaN` refusal boundaries.
Its exact implementation candidate passed the full local policy, format, lint,
test, race, repeat, and vulnerability gates; independent money, migration,
determinism, and release reviews found no P0-P3 issue; and all seven hosted
checks passed before merge.
The command-admission ACL repair is landed after focused PostgreSQL 19 Beta 2
hostile-default, populated-upgrade, timeout/rollback/retry, executable
replay-destruction, and least-privilege tests; full policy, format, lint,
vulnerability, test, race, repeat, four exact-SHA reviews, and all seven hosted
checks passed on its frozen candidate.
The reused-intent source port is landed and green on its focused
PostgreSQL 19 Beta 2 least-privilege test, including concurrent convergence,
fresh-journal exact replay, changed-payload conflict, readiness-drained replay,
and proof that admission leaves every economic and realtime projection
unchanged; its full stable-candidate, exact-SHA, hosted-CI, and merge gates
passed.
The client fills HTTP slice is focused-green on PostgreSQL 19 Beta 2. It proves
exact owner-scoped wire output and query parsing, equal-time tuple pagination,
same-statement filtered totals, least-privilege reads, foreign-account
isolation, and fail-closed rejection of realized PnL beyond the registered
currency scale. Its full stable-candidate, money, determinism, final
exact-SHA, seven-check hosted CI, merge, and separate port-ledger
reconciliation gates passed.

The landed Phase 3 slices have passed repository-wide formatting, lint, unit,
race, repeat, vulnerability, module-consistency, and policy checks. Historical
slices were originally validated against disposable PostgreSQL 17; current CI
and every newly qualified database slice use PostgreSQL 19 Beta 2 alongside
NATS and Centrifugo. Their exact SHAs are green in hosted CI. The frozen
deployment manifest also covers the worker's production
`UZO_HTTP_HEALTH_ADDR` bootstrap key, whose typed parsing and real
`/healthz`/`/readyz` listener wiring are exercised by native Go tests. The
direct-outbox source acceptance additionally has live PostgreSQL 19 Beta 2 and
JetStream proof for exact batch count, subject, stable `Nats-Msg-Id`, persisted
publish sequence, and synchronous durable-consumer acknowledgment. The
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
The landed admin risk-monitor slice has focused PostgreSQL 19 Beta 2 proof for its
fresh and market-only results, every selected durable authority root,
commit/rollback visibility, hostile table and function defaults, exact
function/table ACLs, and all three bounded lock rollback/retry paths. Full
stable-candidate gates, exact-SHA specialist review, hosted CI, and merge are
green; its separate source-ledger acceptance is recorded above.
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
These results do not complete the remaining Phase 3 scope.

## Next milestone

Continue the remaining frozen compatibility operations through vertically
narrow implementation and ledger-acceptance changes. Phase 4 begins only after
the Phase 3 charter and release gates are fully satisfied.

The authoritative scope and completion criteria are in
`PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
