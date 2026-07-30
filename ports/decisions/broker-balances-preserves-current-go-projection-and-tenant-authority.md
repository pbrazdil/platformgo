Title: Activate broker balances with the current Go projection and tenant authority

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- frozen route inventory `GET /broker/v1/accounts/{accountId}/balances`
- `ports/decisions/flat-balance-preserves-current-go-projection-authority.md`
- `internal/edge/types.go::BalanceView`
- `internal/adapters/postgres/compatibility_store.go::Balances`
- `identity.user_accounts` and `identity.account_profiles`

Conflict or ambiguity:
The pinned broker artifact inventories the route but supplies no assertion for
its authorization order, tenant boundary, response shape, decimal validation,
or row order. The accepted client balance route already freezes a five-field
current-Go projection backed by the complete committed PostgreSQL balance
projection rather than the legacy nine-field query-time-derived flat view.
Broker credentials carry two different identities: an API-key subject and the
tenant whose accounts the credential may access. Reusing the account-only
client `Balances` reader after a separate authorization query would permit an
authority change or inconsistent durable authority to cross the tenant
boundary.

Economic/API impact:
The route is read-only. It cannot create or mutate a ledger transaction,
ledger entry, balance, order, fill, position, command, receipt, checkpoint, or
outbox record. It returns the same current-Go `[]BalanceView` projection as the
accepted client route: `currency`, `total`, `locked`, `free`, and `equity`.
Every economic value is an exact canonical decimal string read from one
committed PostgreSQL projection. `locked` maps the durable `used` column.
`crossEquity`, `unrealizedPnl`, `marginRatio`, and `maintenanceMargin` remain
absent; the broker edge does not recompute, synthesize, or round them.

Rows are ordered by the bytewise currency comparator
`ORDER BY balance.currency COLLATE pg_catalog."C"` so database locale cannot
change response bytes for identical committed state. Currency is non-null and
unique per account. Every row is validated against the append-only
`trading.currency_scales` authority before any response is returned. A missing
scale, invalid currency, malformed or non-finite decimal, or value exceeding
the registered scale rejects the complete read; partial balance arrays are
forbidden.

The existing migrations do not fully protect every relation required by this
read from hostile owner defaults. Before route activation, one new immutable
forward migration must scrub `identity.user_accounts`,
`identity.account_profiles`, and `ledger.balances`. It must remove `PUBLIC`,
every explicit non-owner table and column grant, grant option, and dependent
same-object grant chain, then restore only this non-grantable allowlist:

- `identity.user_accounts`: API `SELECT`; engine `SELECT, INSERT`;
- `identity.account_profiles`: API `SELECT`; engine `INSERT`; and
- `ledger.balances`: API `SELECT`; engine `SELECT, INSERT, UPDATE`.

The migration changes only ACL catalogs and the migration journal. It preserves
owners' default-privilege templates, row bytes, relation files, schemas, and
economic state. Bounded `SHARE` locks are acquired in production writer order:
`identity.user_accounts`, `identity.account_profiles`, then
`ledger.balances`. This fences a provisioning or balance writer that already
used a privilege before it is revoked. A lock or statement timeout is a
definite pre-commit rollback and the whole migration may be retried after the
writer is drained. A missing `COMMIT` acknowledgment is an unknown outcome:
runtimes stay halted while the exact filename/checksum and complete raw
table/column ACLs are compared before any retry or binary selection.

Options considered:
1. Authenticate the broker, then call the account-only client `Balances`
   reader.
2. Recreate the legacy nine-field broker view and derive economic values at
   query or HTTP time.
3. Preserve the accepted current-Go five-field projection while adding a
   broker-specific same-statement tenant authorization and balance read.

Decision:
Choose option 3 under the owner's standing instruction to preserve current Go
behavior as the source.

The route requires a valid broker HMAC credential and exact `accounts:read` or
wildcard scope. The authorization tenant is `Principal.Tenant`; the API-key
`Principal.Subject` is never account tenant authority. The fail-closed
evaluation order is exact:

1. authenticate the broker credential;
2. require `accounts:read` or wildcard scope;
3. strictly parse the lowercase, hyphenated current-Go account UUID URN; and
4. execute the authorization-gated PostgreSQL balance read.

Authentication dominates scope, parsing, and storage: an invalid credential
with a malformed account ID returns `401` without a database call. Scope
dominates parsing and storage: a valid credential without the required scope
returns `403` without parsing-dependent or database-dependent behavior. Only a
valid, sufficiently scoped credential can receive `400` for a malformed
account ID.

One PostgreSQL statement must establish all of the following before selecting,
decoding, validating, or returning any balance row:

- `identity.user_accounts.account_id` equals the requested account;
- `identity.user_accounts.broker_subject` equals the authenticated tenant;
- `identity.account_profiles.account_id` equals the requested account; and
- `identity.account_profiles.broker_subject` equals the authenticated tenant.

The same statement must return an authority sentinel and every authorized
balance row joined to its registered currency scale. A separate authorization
query followed by the account-only reader is not accepted. Missing ownership,
a missing profile, a mismatch in either tenant column, or an unknown account
returns the same generic `403` without selecting or decoding that account's
balances. A foreign account's empty, valid, or corrupt balance projection
cannot change that status or logical error body. An authorized account with no
balance rows returns `200` and the non-null empty JSON array `[]`. Invalid
credentials return `401`, invalid scope returns `403`, invalid account IDs
return `400`, and only authorized projection corruption or PostgreSQL failure
returns the existing opaque `503`.

The route remains inventory until the follow-on implementation and contract
tests are accepted. This decision does not activate it, change the accepted
client route, authorize any balance mutation, or authorize broader broker
account-list, order, position, fill, funding, or realtime behavior.

Tests required before implementation acceptance:
- Edge tests must first fail for the absent route, then freeze HMAC
  authentication, exact scope, strict account parsing, generic authorization
  errors, exact current-Go JSON, non-null empty output, and opaque `503`
  mapping.
- The edge failure matrix must prove dominance without a store call: invalid
  credential plus malformed account ID returns `401`; valid credential with
  insufficient scope plus malformed account ID returns `403`; and only valid
  `accounts:read` or wildcard scope plus malformed input returns `400`.
- A PostgreSQL 19 Beta 2 HTTP test must prove same-tenant success and
  cross-tenant, ownership-only, profile-only, conflicting-tenant, and unknown
  account denial through the least-privilege API role.
- A representative-current-main hostile-upgrade test must first prove the
  vulnerable named/default, column, grant-option, and dependent grant chains,
  then apply the new forward migration and assert the exact three-relation
  allowlist. It must prove bounded lock timeout and retry, transaction-wide
  rollback when the later relation is blocked, unchanged owner defaults and
  relation bytes, immutable migration history, least-privilege API read
  success, and denied unauthorized DML.
- Every unknown or unauthorized authority shape must be crossed with empty,
  valid, and corrupt balance projections and return the same generic `403`
  status, code, message, and empty data surface. Unauthorized rows must not
  participate in decimal decoding, currency validation, ordering, or error
  selection.
- The PostgreSQL test must prove the authorization and balance read share one
  SQL statement, including after reconstructing the API pool, authenticator,
  reader, and HTTP server.
- A real PostgreSQL-to-HTTP test, not a stubbed reader, must independently
  derive and compare the complete ordered response bytes for at least two
  currencies and all five fields. It must prove exact canonical strings,
  durable `used` to wire `locked`, no additional fields, and deterministic
  currency ordering.
- A collation-hostile PostgreSQL test must prove exact bytewise currency order
  for codes whose order differs under a non-`C` locale, and repeat the exact
  response after reconstructing the pool and server. The test must either run
  against both collations or inspect the query and execute the hostile fixture
  under an available non-`C` collation; a default-locale-only assertion is
  insufficient.
- Authorized corruption must be tested independently for missing scale
  authority, invalid currency, malformed/non-finite values where a compatible
  disposable historical schema permits them, excess registered scale, and a
  terminal row-stream error after at least one valid row. Every case returns
  the opaque `503` with no partial array or row value.
- Successful exact output and every material corruption denial must be
  repeated after reconstructing the least-privilege API pool and server.
  Denial and corrupt-read paths must leave immutable balances, ledger history,
  currency scales, and all other economic projections unchanged.
- The implementation PR must atomically update and test the frozen broker
  OpenAPI, compatibility manifest, artifact hash, runtime wiring, and this
  decision's intentional-deviation entry. It must bind the exact method/path,
  lowercase hyphenated current-Go account UUID URN, broker API-key security,
  accepted statuses `200/400/401/403/503`, the five-field `BalanceView` schema,
  non-null array response, exact string fields,
  `x-platformgo-contract-status = phase3-accepted-runtime`, and the implemented
  route list. Runtime activation while any frozen artifact still labels the
  route as inventory is forbidden.

Stop conditions:
- Stop if a pinned source assertion establishes a different broker response,
  scope, status, tenant authority, projection, or decimal contract.
- Stop if one PostgreSQL statement cannot both prove tenant authority and
  return the complete projection without weakening least privilege or
  exact-value validation.
- Stop if any path can expose a partial array, distinguish foreign account
  existence through row data, recompute economic values, or mutate durable
  economic state.
- Stop if response order remains dependent on the database's default
  collation.
- Stop if the ACL correction cannot preserve the engine provisioning/write
  privileges above, requires a heap rewrite, or cannot fence pre-revocation
  writers with bounded deterministic locks.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-30
