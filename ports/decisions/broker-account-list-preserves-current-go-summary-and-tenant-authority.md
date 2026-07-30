Title: Activate broker account listing with the current Go summary and tenant authority

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/identity/e2e_tenant_isolation.rs::broker_access_is_scoped_to_its_tenant`
- `apps/app/src/api/broker/reads.rs::list_accounts`
- `apps/app/src/core/accounts/queries/broker.rs::BrokerAccountListHandler`
- `crates/persistence/src/accounts/mod.rs::list_by_tenant`
- `crates/persistence/src/accounts/mod.rs::list_by_user`
- frozen route inventory `GET /broker/v1/accounts`
- accepted current-Go `internal/edge/types.go::MyAccountView`
- accepted current-Go account and user URNs in
  `ports/decisions/broker-account-identifiers-preserve-current-go-urns.md`

Conflict or ambiguity:
The pinned source freezes an unpaged tenant account list, an optional `userId`
filter, and ascending account-login order. The source broker list returns
`MyAccountView`, while its identifier encoding differs from the already
accepted current-Go UUID URNs. Ordering only by login does not define a total
order if durable corruption or a future schema change permits duplicate logins.
The source implementation also filters tenant authority after a user-filtered
read. The owner directed the project to preserve current Go behavior as the
source; safety invariants prohibit reproducing that check-then-filter authority
shape.

Decision:
Activate the route with an unpaged JSON array of the ten-field current-Go
`MyAccountView`: `accountId`, `login`, `userId`, `baseCurrency`, `marginMode`,
`omsMode`, `marketVenue`, `permittedClasses`, `status`, and `createdAt`.

The exact gate order is:

1. authenticate the broker HMAC credential;
2. require exact `accounts:read` or wildcard scope;
3. parse an optional `userId` as one lowercase hyphenated UUID user URN; and
4. issue one parameterized PostgreSQL statement.

The query parser accepts zero or one `userId`. A blank, duplicate, malformed,
uppercase, or noncanonical `userId` returns `400 invalid_request / invalid user
id` before PostgreSQL. Unknown query keys remain ignored, matching the pinned
Serde extractor, which does not deny unknown fields.

`Principal.Tenant` is tenant authority. `Principal.Subject` and the optional
`userId` are not. The store selects one of two fixed parameterized statement
templates based only on filter presence. The unfiltered materialized ownership
query first constrains `identity.user_accounts` by `broker_subject`. The
filtered template performs a one-time tenant/user existence lookup through
`identity.users(user_id, broker_subject)`, then constrains ownership by both
`broker_subject` and `user_id`. Both templates use pgx unnamed
extended-protocol execution so PostgreSQL plans for the concrete authenticated
tenant instead of eventually reusing a tenant-agnostic generic plan. Only then
may the statement use tenant-constrained lateral account-profile and trading
projection probes. Nullable projections never establish authority. An absent
or foreign user filter therefore returns the same successful empty array and
cannot act as an existence oracle.

The complete result is scanned, validated, and buffered before the HTTP edge
writes headers or JSON. Any tenant-owned incomplete, inconsistent, or corrupt
row fails the whole request closed as an opaque `503`; no valid prefix or
partial account may be returned. Corruption outside the ownership anchor
cannot affect the response.

Successful rows are ordered by `profile.login`, then by
`ownership.account_id` under the PostgreSQL `C` collation. This preserves
source login ordering and adds a deterministic byte-total tie-breaker.
No rows serialize as exact `[]`, never `null`.

Every returned account must pass the shared current-Go projection validator:
positive login, `USDC`, accepted account status/margin/OMS enums,
`HYPERLIQUID`, the single `CRYPTOCURRENCY` permitted class, and an
RFC3339-representable UTC creation time. Identifier strings retain the
already-accepted current-Go account and user URN behavior; only the optional
list filter is newly constrained to a canonical UUID user URN.

The source contract is intentionally unpaged. Adding pagination or a response
cap would be a compatibility break and is not authorized. To prevent one
tenant request from scanning ownership rows belonging to every tenant, this
activation adds a new forward migration with a tenant-leading partial index on
`identity.user_accounts (broker_subject, user_id, account_id)` where
`broker_subject IS NOT NULL`. The store's per-request custom planning and
lateral indexed projection probes keep work outside final sorting and
rendering bounded by the authorized tenant/filter result rather than global
ownership or projection cardinality.

Economic/API impact:
This is one read-only PostgreSQL statement and one statement-level MVCC
snapshot. It creates no command, receipt, checkpoint, ledger entry, balance,
position, order, fill, outbox record, realtime event, acknowledgment, replay
record, or mutable process state. It reads no amount and performs no arithmetic,
rounding, or currency-scale lookup. `baseCurrency` is a validated identifier,
not an economic amount.

The new index changes no row or authority. Its forward migration, lock and
timeout behavior, retry boundary, least-privilege ACL state, current-main
upgrade, and rollback path require PostgreSQL 19 Beta 2 evidence. Existing
frozen migration bytes remain immutable.

Compatibility:
- missing or invalid credential: `401 unauthorized`;
- insufficient scope: `403 forbidden`;
- invalid `userId`: `400 invalid user id`;
- absent or foreign `userId`: `200 []`;
- authorized storage, scan, or any selected-row projection failure: opaque
  `503`;
- success: an exact non-null array of ten-field `MyAccountView` values in
  deterministic order.

The composite pinned source test also covers point read, balance mutation
denial, and channel isolation. Activating this list assertion does not promote
that entire port-ledger row to reviewed or claim those other behaviors.

Required evidence:
- edge gate-order, strict optional-filter, exact-byte list/empty/error, ignored
  unknown-key, deterministic-order, and no-partial-response tests;
- a representative failing native test before implementation;
- real least-privilege PostgreSQL 19 Beta 2 HTTP tests for exact and wildcard
  scope, unfiltered and user-filtered reads, empty/absent/foreign filters,
  foreign corruption isolation, owned incomplete/corrupt late rows, and
  canonical returned identifiers;
- exact statement inspection and unchanged relation digests covering
  `identity.users`, `identity.user_accounts`, `identity.account_profiles`, and
  `trading.accounts`;
- exactly zero SQL statements for authentication, scope, and parse failures,
  and exactly one statement for every authorized valid request;
- relation digests proving every request is non-mutating;
- repeated behavior after pool, reader, authenticator, and server
  reconstruction;
- representative tenant and cross-tenant
  `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` custom execution plans under
  normal planner settings, proving the fixed filtered template performs a
  one-time tenant/user key lookup with zero ownership loops and buffers for a
  large foreign user's range and the new index removes global ownership-scan
  amplification from the unfiltered template;
- production store evidence that both templates use pgx unnamed
  extended-protocol execution and cannot age into cached generic plans;
- current-main migration upgrade, retry/checksum, lock/timeout, ACL, and
  rollback evidence;
- atomic OpenAPI, manifest/hash, runtime wiring, companion documentation, and
  README updates.

Stop conditions:
- stop if pagination or a response cap is required without explicit owner
  approval for a compatibility break;
- stop if authorization uses a separate query or post-read tenant filter;
- stop if nullable projection joins can establish authority;
- stop if foreign data can affect error selection or projection validation;
- stop if a selected corrupt row can return a partial list or success;
- stop if ordering depends on locale, insertion order, map iteration, or
  goroutine scheduling;
- stop if the route performs a durable write or an existing frozen migration
  would need modification.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-30
