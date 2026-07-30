Title: Activate broker fills with the current Go projection and tenant authority

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- frozen route inventory `GET /broker/v1/accounts/{accountId}/fills`
- `ports/decisions/fills-http-preserves-current-go-projection.md`
- `internal/edge/types.go`
- `internal/adapters/postgres/compatibility_store.go`
- `identity.user_accounts` and `identity.account_profiles`

Conflict or ambiguity:
The pinned broker artifact inventories the route but supplies no assertion for
its authorization order, tenant boundary, page shape, filters, or cursor
semantics. The accepted client fills route already freezes a narrower immutable
current-Go projection than the legacy enriched fill mirror. Broker credentials
carry two different identities: an API-key subject and the tenant whose
accounts the credential may access. An account-only reuse of the client
`Fills` reader would not prove tenant authority and could expose another
tenant's immutable economic history to a broker that learns its account ID.

Economic/API impact:
The route is read-only. It cannot create or mutate an order, fill, position,
ledger entry, balance, command, receipt, checkpoint, or outbox record. It
returns the same current-Go `FillExecutionPage` fields, exact decimal strings,
nullability, omission, filters, tuple order, totals, and cursors as the accepted
client route. It does not manufacture any field from the legacy enriched
mirror.

Each response is an ordinary committed history view, not replay or catch-up.
Items and the filter-wide total come from one PostgreSQL statement ordered
newest-first by `(logical_time, fill_id)`. A cursor resumes after one committed
tuple; it is not a cross-request snapshot or a continuity promise when later
fills commit.

Options considered:
1. Call the existing account-only `Fills` reader after broker authentication.
2. Recreate the legacy enriched mirror before exposing any broker fill history.
3. Preserve the accepted current-Go projection while adding a broker-specific
   same-statement tenant authorization and fill read.

Decision:
Choose option 3 under the owner's standing instruction to preserve current Go
behavior as the source.

The route requires a valid broker HMAC credential and exact
`accounts:read` or wildcard scope. The authorization tenant is
`Principal.Tenant`; the API-key `Principal.Subject` is never an account tenant.
The fail-closed evaluation order is exact:

1. authenticate the broker credential;
2. require `accounts:read` or wildcard scope;
3. strictly parse the current-Go account ID and fill query; and
4. execute the authorization-gated PostgreSQL page read.

Authentication dominates scope, parsing, and storage: an invalid credential
with malformed input returns `401` without a database call. Scope dominates
parsing and storage: a valid credential without the required scope returns
`403` without parsing-dependent or database-dependent behavior. Only a valid,
sufficiently scoped credential can receive `400` for malformed input.

After those gates, one PostgreSQL statement must require all of the following
before selecting, decoding, counting, or returning any page data:

- `identity.user_accounts.account_id` equals the requested account;
- `identity.user_accounts.broker_subject` equals the authenticated tenant;
- `identity.account_profiles.account_id` equals the requested account; and
- `identity.account_profiles.broker_subject` equals the authenticated tenant.

The same statement must produce the authorized page items and filter-wide
total. A separate authorization query followed by the existing account-only
reader is not accepted because revocation or corruption between statements can
cross the tenant boundary. Missing ownership, a missing profile, a mismatch in
either tenant column, or an unknown account returns the same generic `403`
without selecting or decoding that account's fills and without fill IDs,
totals, cursors, or row values. A foreign account's empty, valid, or corrupt
fill history cannot change that `403` status or logical error body. Invalid
account IDs or fill query values return `400`; invalid credentials return
`401`; invalid scope returns `403`; only authorized projection corruption or
PostgreSQL failure returns the existing opaque `503`.

The route remains inventory until the follow-on implementation and contract
tests are accepted. This decision does not activate it, does not change the
accepted client route, and does not authorize a broader broker account-list,
orders, positions, balances, funding, or realtime contract.

Tests required before implementation acceptance:
- Edge tests must first fail for the absent route, then freeze HMAC
  authentication, exact scope, strict account/query parsing, generic
  authorization errors, exact current-Go JSON, and opaque `503` mapping.
- The edge failure matrix must prove dominance without a store call:
  invalid credential plus malformed input returns `401`; valid credential with
  insufficient scope plus malformed input returns `403`; and only valid
  `accounts:read` or wildcard scope plus malformed input returns `400`.
- A PostgreSQL 19 Beta 2 HTTP test must prove same-tenant success and
  cross-tenant, ownership-only, profile-only, and conflicting-tenant denial
  through the least-privilege API role.
- Every unknown or unauthorized authority shape must be crossed with empty,
  valid, and corrupt fill history and return the same generic `403` status,
  code, message, and empty data surface. Unauthorized fill rows must not
  participate in decoding, totals, cursor construction, or error selection.
- The PostgreSQL test must prove the authorization and page read share one SQL
  statement, including after reconstructing the API pool and HTTP server.
- A real PostgreSQL-to-HTTP test, not a stubbed reader, must independently
  derive and compare the complete response bytes for `fillId`, correlatable
  `orderId`, `positionId`, `side`, all classified `tradeType` values, `reason`,
  exact canonical `realizedPnl`, paired `settlementCurrency`, exact canonical
  `leverage`, logical-time `filledAt`, non-null empty `items`, total, cursors,
  and every required `null` versus omitted field.
- Authorized late-row corruption must be tested independently for over-scale
  realized PnL, mismatched realized-PnL/currency presence, invalid or
  non-finite/non-positive leverage at a compatible historical schema boundary,
  invalid trade type, invalid reason/order/intent authority, and a terminal row
  stream error after at least one valid row. Every case must return the opaque
  `503` with a zero page and no fill ID, value, total, or cursor.
- Successful exact output and every material corruption denial must be repeated
  after reconstructing the least-privilege API pool, authenticator, reader, and
  HTTP server. Denial and corrupt-read paths must leave immutable fill rows and
  all economic projections unchanged.
- Fixed-history tests must preserve exact filtering, totals, forward/backward
  cursor traversal, equal-time tie-breaking, and valid `NULL` leverage.
- A moving-history test must document that new commits may change later pages
  and must not describe the cursor as replay, snapshot, or catch-up authority.
- The implementation PR must atomically update and test the frozen broker
  OpenAPI, compatibility manifest, generated artifacts, and hashes. It must
  bind the exact method/path, lowercase hyphenated current-Go account UUID URN,
  `side`, `tradeId`, `limit`, `cursor`, and `direction` parameters, broker
  API-key security, accepted statuses `200/400/401/403/503`,
  `FillExecutionPage`/`FillExecutionView` schemas, nullability and omission,
  `x-platformgo-contract-status = phase3-accepted-runtime`, the implemented
  route list, and this decision's intentional-deviation entry. Runtime
  activation while any frozen artifact still labels the route as inventory is
  forbidden.

Stop conditions:
- Stop if a pinned source assertion establishes a different broker response,
  scope, status, tenant authority, or cursor contract.
- Stop if one PostgreSQL statement cannot both prove tenant authority and
  return the page without weakening least privilege or exact-value validation.
- Stop if any path can expose a partial page, distinguish foreign account
  existence through row data, or mutate immutable economic history.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-30
