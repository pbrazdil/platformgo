Title: Activate broker account read with the current Go summary and tenant authority

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/identity/e2e_tenant_isolation.rs::broker_access_is_scoped_to_its_tenant`
- `apps/app/src/core/accounts/queries/broker.rs::account_by_id_broker`
- `apps/app/src/core/access.rs::assert_account_tenant`
- frozen route inventory `GET /broker/v1/accounts/{accountId}`
- accepted current-Go `internal/edge/types.go::MyAccountView`

Conflict or ambiguity:
The pinned source freezes same-tenant success and treats absent and
cross-tenant accounts identically as `400 unknown account`. The source broker
view has fewer fields than the already accepted current-Go account summary.
The owner directed the project to preserve current Go behavior as the source.

Decision:
Activate the route with the ten-field current-Go `MyAccountView`:
`accountId`, `login`, `userId`, `baseCurrency`, `marginMode`, `omsMode`,
`marketVenue`, `permittedClasses`, `status`, and `createdAt`.

The exact gate order is:

1. authenticate the broker HMAC credential;
2. require exact `accounts:read` or wildcard scope;
3. parse the lowercase hyphenated UUID account URN; and
4. issue one parameterized PostgreSQL statement.

`Principal.Tenant` is tenant authority. `Principal.Subject` is not. The
statement first anchors authority on
`identity.user_accounts(account_id, broker_subject)` and only then left-joins
the tenant-constrained account profile and trading projection. Thus absent or
foreign ownership returns no row and the same `400 invalid_request / unknown
account`, while a tenant-owned but incomplete, inconsistent, or corrupt
projection remains distinguishable internally and fails closed as an opaque
`503`. Nullable joins never establish authority.

The complete projection is validated before the edge writes any response.
Only `USDC`, the accepted account status/margin/OMS enums,
`HYPERLIQUID`, and the single `CRYPTOCURRENCY` permitted class are accepted.
Rendering uses the existing deterministic lowercase current-Go enum mapping,
the fixed `["perps"]` slice, and error-returning UTC RFC3339-nanosecond time.
A finite PostgreSQL timestamp outside RFC3339's four-digit year range is
projection corruption and returns opaque `503`.

Economic/API impact:
This is a read-only authoritative point snapshot. It creates no command,
receipt, checkpoint, ledger entry, balance, position, order, fill, outbox
record, realtime event, or acknowledgment. It reads no amount and performs no
rounding or scale lookup. Rejecting a non-USDC base currency closes an existing
shared projection-validation gap without changing valid output.

PostgreSQL gives the single statement one MVCC snapshot. Unique account keys
make result cardinality zero or one, so no row ordering is required. Existing
least-privilege grants already permit the API role to select all three
relations; no migration or dependency change is required.

Compatibility:
- missing or invalid credential: `401 unauthorized`;
- insufficient scope: `403 forbidden`;
- malformed account ID: `400 invalid account id`;
- absent or foreign account: `400 unknown account`;
- authorized storage, scan, or projection failure: opaque `503`;
- success: exact ten-field `MyAccountView`.

The composite pinned source test also covers account listing, balance mutation
denial, and channel isolation. Activating this point read does not promote that
entire port-ledger row to reviewed or claim those other behaviors.

Required evidence:
- edge gate-order, exact-byte success, strict routing, generic unknown, and
  no-partial-response tests;
- real least-privilege PostgreSQL 19 Beta 2 HTTP tests for same-tenant,
  wildcard, absent, foreign, incomplete, conflicting, and corrupt authority
  shapes;
- repetition after pool, reader, authenticator, and server reconstruction;
- explicit relation digests proving every read is non-mutating;
- atomic OpenAPI, manifest/hash, runtime wiring, decision, and README updates.

Stop conditions:
- stop if a separate authorization query or post-read tenant filter appears;
- stop if nullable projection joins can establish authority;
- stop if foreign data can affect error selection or projection decoding;
- stop if an owned corrupt graph can return a partial account or success;
- stop if the route performs a durable write or requires a frozen migration
  edit.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-30
