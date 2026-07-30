Title: Activate broker funding with the current Go projection and tenant authority

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/src/api/broker/reads.rs::funding`
- `apps/app/src/core/trading/queries/broker.rs::BrokerAccountFundingHandler`
- `apps/app/tests/it/trading/e2e_funding.rs::funding_history_reads_paginates_and_aggregates`
- frozen route inventory `GET /broker/v1/accounts/{accountId}/funding`
- accepted current-Go `internal/edge/types.go::FundingPage`
- accepted PostgreSQL reader evidence in
  `tests/integration/postgres/funding_history_test.go`

Conflict or ambiguity:
The frozen broker OpenAPI inventories the route but its legacy two-field
`FundingView` does not match the full funding page returned by the pinned
broker handler or the already accepted current-Go client funding projection.
The existing Go `Funding` reader is account-only, executes the page and
cursorless total as two PostgreSQL statements, omits broker `accountLogin`,
and does not validate every stored economic value before serialization.
Calling it after a separate authorization query would permit another tenant's
history to cross the broker boundary and would mix page and total snapshots.

Decision:
Activate the route with the accepted current-Go `FundingPage` and
`FundingView`: `fundingId`, `symbol`, `positionId`,
`positionSignedQty`, `oraclePrice`, `fundingRate`, `fundingAmount`,
`currency`, `fundingTime`, and broker-visible `accountLogin`.
This follows the owner's standing instruction to preserve current Go behavior
as the source. The client funding route and its reader remain unchanged.

The fail-closed edge order is:

1. authenticate the broker HMAC credential;
2. require exact `accounts:read` or wildcard scope;
3. strictly parse the lowercase hyphenated current-Go account UUID URN;
4. parse the current-Go funding `limit`, `cursor`, and `direction` query; and
5. execute one broker-specific PostgreSQL statement.

`Principal.Tenant`, never the API-key subject, is tenant authority. The one
statement must materialize matching rows from both
`identity.user_accounts` and `identity.account_profiles` before invoking the
account funding functions. It returns one authority sentinel, the complete
page window, and the optional cursorless total from one MVCC snapshot.
Missing ownership, missing profile, either tenant mismatch, or an unknown
account returns the same generic `403` without executing or decoding the
foreign account's funding function. A separate authorization statement is
forbidden.

The broker statement uses the accepted newest-first
`(logical_time, funding_id)` tuple order and current cursor encoding. Cursors
are moving committed-history positions, not replay, catch-up, or a
cross-request snapshot. The cursorless page includes `total`; later cursor
pages retain the accepted omission of `total`. Empty authorized pages contain
non-null `items: []`.

Every selected row is buffered and validated before HTTP headers or bytes are
written. Funding IDs and durable position IDs remain PostgreSQL UUIDs.
Signed quantity, strictly positive oracle price, and funding rate must be
finite exact decimals and are canonicalized without rounding. Funding amount
must be valid exact money under the append-only
`trading.currency_scales` authority. Invalid currency, missing or invalid
scale, non-finite values, excess amount scale, incomplete rows, inconsistent
authority sentinels, scan failure, or terminal row-stream failure rejects the
whole response as opaque `503`; no valid prefix, row identifier, total, or
cursor is returned.

The route is read-only. It creates no command, input, receipt, checkpoint,
ledger entry, balance, funding settlement, projection, order, fill, position,
outbox record, realtime event, acknowledgment, or replay record. It performs
no economic calculation or rounding.

Migration and ACL boundary:
Frozen migration
`20260726000100_phase3_funding_history_read_model.up.sql` must not change.
Its `PUBLIC` revocations do not remove hostile named/default table, column, or
function grants or dependent grant chains. Before route activation, a new
forward migration must:

- take bounded `SHARE` locks on `trading.funding_settlements` and
  `trading.funding_history_projection` in engine insert order before changing
  any ACL;
- remove every non-owner table and column grant, grant option, and dependent
  chain from both relations;
- remove every non-owner function grant and dependent chain from the funding
  trigger/read/count/aggregate functions;
- restore only non-grantable engine `SELECT, INSERT` on both relations and API
  `EXECUTE` on the five existing read/count/aggregate functions;
- leave the trigger function uncallable by runtime roles;
- preserve owners' default privileges, function bodies/search paths, rows,
  indexes, relation files, owners, and prior migration bytes/checksums.

A definite lock or statement timeout rolls back every ACL change and the
migration journal for safe retry after the writer drains. A missing commit
acknowledgment is an unknown outcome: runtimes remain stopped while operators
compare the exact filename/checksum, raw table/column/function ACLs, function
definitions, row digests, owners, indexes, and relation files before retry or
binary selection.

Failure matrix and required red-first evidence:
- invalid credentials plus malformed path/query: `401`, zero SQL;
- valid insufficient scope plus malformed path/query: `403`, zero SQL;
- valid required/wildcard scope plus malformed account ID: `400`, zero SQL;
- valid account ID plus malformed cursor/limit: `400`, zero SQL;
- absent, foreign, ownership-only, profile-only, or conflicting authority:
  identical `403` for empty, valid, and corrupt foreign histories;
- authorized empty history: exact `200 {"items":[],"total":0}`;
- authorized fixed history: exact current-Go bytes, account login, total,
  newest-first order, forward/backward traversal, and equal-time tie break;
- one statement per valid store request, including cursorless total;
- late authorized corruption: opaque whole-response `503` before and after
  pool/store/authenticator/server reconstruction;
- request cancellation and terminal row errors: no partial page;
- unchanged digests for funding, authority, scale, and other economic
  relations on every read/error path;
- hostile exact-39 upgrade, exact ACL allowlist, function search paths,
  unchanged rows/filenodes/indexes/defaults/history, later-lock rollback,
  retry, and production-order writer completion on PostgreSQL 19 Beta 2;
- representative authorized and foreign query plans under normal planner
  settings, including zero funding-function loops for foreign authority;
- atomic runtime, OpenAPI, manifest/hash, intentional-deviation, operations,
  database, and README updates.

Stop conditions:
- stop if a pinned or accepted Go test establishes a different broker page,
  field, nullability, status, tenant authority, or cursor contract;
- stop if one statement cannot bind authority, page, and cursorless total
  without direct API access to monetary tables;
- stop if foreign data can influence error selection or invoke a funding
  reader;
- stop if any corrupt selected row can produce partial output or be repaired,
  defaulted, rounded, or silently omitted;
- stop if the ACL correction needs a heap rewrite, economic mutation, frozen
  migration edit, unbounded lock, or weakened engine authority.

Approver: Petr Brazdil, through the active owner instruction:
`Zachovej soucasne chovani jako zdroj.`

Date: 2026-07-30
