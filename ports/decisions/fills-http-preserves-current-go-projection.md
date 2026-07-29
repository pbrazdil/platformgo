Title: Activate the client fills route with the current Go projection

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_fills.rs`
- `apps/app/tests/it/trading/e2e_rest.rs`
- `internal/edge/types.go`
- `internal/adapters/postgres/compatibility_store.go`

Conflict or ambiguity:
The pinned source inventory contains
`GET /v1/accounts/{accountId}/fills`, while its fill-query tests exercise a
legacy enriched mirror row. The reviewed Go fill-history slices deliberately
preserve a narrower immutable execution projection and do not manufacture
catalog, user, commission, liquidity, order-type, or exchange fields that are
not authoritative at that boundary. Their earlier decisions left HTTP route
activation for a separate complete compatibility review.

Economic/API impact:
This route is read-only. It cannot create or mutate orders, fills, positions,
ledger entries, balances, commands, or outbox records. It exposes the existing
account-scoped PostgreSQL projection without recomputing money:
`realizedPnl` and `settlementCurrency` are the existing paired durable values,
and `leverage` is the frozen execution-time value. Absent realized values are
encoded as JSON `null`; absent leverage and cursors are omitted. Decimal values
remain exact strings.

The page is a committed keyset view ordered newest-first by the existing
`(logical_time, fill_id)` tuple. Each response obtains its items and
filter-wide total from one PostgreSQL statement. A cursor continues from that
committed tuple; it is not a cross-request database snapshot or a promise that
concurrent later commits cannot change subsequent pages. The accepted pinned
test proves exactly-once traversal for a fixed input set, and this route makes
no stronger concurrency claim.

Options considered:
1. Recreate the legacy mirror and add fields whose authority is not present in
   the current Go projection.
2. Keep the source route permanently unavailable even though its reviewed
   current-Go projection, filters, ordering, and PostgreSQL role already exist.
3. Freeze and activate the existing narrow Go projection as its own complete
   Phase 3 HTTP contract.

Decision:
Choose option 3. The authenticated owner-scoped route accepts the existing
`side`, `tradeId`, `limit`, `cursor`, and `direction` inputs. Invalid,
ambiguous, repeated, or out-of-range inputs fail closed with `400`; foreign
accounts fail before the fill-history read with `403`, after the existing
caller-scoped rate claim; projection corruption and database failure return
`503`. The route exposes only the current Go fields and current Go identities.
It does not claim the unimplemented legacy enriched row.

Tests added/changed:
- Edge tests freeze authentication, ownership, exact JSON field names,
  nullability/omission, filters, pagination parsing, and error mapping.
- A real PostgreSQL 19 Beta 2 HTTP test proves the least-privilege API role,
  account isolation, exact durable values, filtering, and cursor continuation.
- Frozen OpenAPI and compatibility-manifest tests mark only this route as an
  accepted runtime contract.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-29
