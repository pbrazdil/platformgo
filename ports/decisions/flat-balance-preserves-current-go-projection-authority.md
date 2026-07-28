Title: Preserve the current Go flat-balance projection authority

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_balances.rs::flat_account_balance_is_pure_cash_and_scale_stripped`
- `apps/app/tests/common/mod.rs::seed_engine_cash`
- `contracts/openapi/client-v1.json::BalanceView`
- `internal/edge/types.go::BalanceView`
- `internal/adapters/postgres/compatibility_store.go::Balances`

Conflict or ambiguity:
The pinned Rust test seeds `total=1000.000000000000000`,
`locked=999.999`, and `free=0.001`, then deliberately ignores the stored
`locked` and `free` values. Its query derives a nine-field flat-account view:
`currency`, `total`, `locked`, `free`, `equity`, `crossEquity`,
`unrealizedPnl`, `marginRatio`, and `maintenanceMargin`.

Current Go has a different authority boundary. The engine computes and
atomically commits the complete `total`, `used`, `free`, and `equity`
projection with its receipt, checkpoint, and balanced ledger transaction.
The production read path maps committed `used` to wire `locked`; it does not
recompute economic values at the HTTP edge or ignore stored projection
columns. The frozen current Go client contract exposes exactly five fields:
`currency`, `total`, `locked`, `free`, and `equity`. It omits
`crossEquity`, `unrealizedPnl`, `marginRatio`, and `maintenanceMargin`.

The source literal cannot be reused as an engine input at the normal USDC
scale of two. Current Go correctly rejects its lexical scale of fifteen before
canonicalization. The equivalent exact engine input is `1000`; PostgreSQL
stores it in `NUMERIC(38,18)` and the production reader canonicalizes it to
wire `1000`.

Economic/API impact:
This is an intentional external-contract and projection-authority deviation.
Current Go clients retain the already frozen five-field response and receive
only committed PostgreSQL projection values. A source client requiring the
four additional fields, or expecting the read edge to discard stored
`locked/free` values and derive a flat view from `total`, must adapt.

The decision changes no economic formula, rounding rule, transaction, schema,
ledger effect, writer ownership, or identifier. It requires one additive
least-privilege grant allowing the API role to read the existing append-only
`trading.currency_scales` authority. The production reader must bind each
balance to that registered scale, validate every exact decimal at the scale,
discard the whole result on any invalid row, and return only the generic
unavailable contract. The API role remains read-only for both
`ledger.balances` and `trading.currency_scales`.

Options considered:
1. Recreate the nine-field Rust query and recompute balances at the HTTP edge.
2. Add the four source-only fields while populating synthetic or zero values.
3. Preserve the current Go five-field contract and complete PostgreSQL
   projection authority, with strict fail-closed validation at the read
   boundary.

Decision:
Choose option 3 under the owner's standing instruction to preserve current Go
behavior as the source. The accepted behavior is limited to a legitimate flat
balance committed through the deterministic engine and PostgreSQL transaction,
then read through the authenticated, ownership-gated production HTTP path.
The exact current Go response is `currency=USDC`, `total=1000`, `locked=0`,
`free=1000`, and `equity=1000`, with all economic values represented as
canonical JSON strings.

The acceptance must prove rollback, stable retry, same-sequence replay,
later-sequence duplicate delivery, restart recovery, reconciliation,
least-privilege reads, anonymous and foreign-account denial, exact field
absence, the source's one-row response, and whole-response failure for malformed
durable currency or decimal values. It must not claim source-compatible
query-time derivation, the adjacent working-order reservation behavior,
margin ratio, maintenance margin, unrealized PnL, cross equity, realtime
delivery, or arbitrary balance repair.

Tests added/changed:
- Replace the fixture-only
  `TestFlatAccountBalanceIsPureCashAndScaleStripped` with production HTTP and
  PostgreSQL 19 evidence in
  `tests/integration/compatibility/flat_balance_test.go`.
- Strengthen `CompatibilityStore.Balances` to validate and canonicalize every
  currency and economic decimal against the durable scale before returning any
  row.
- Add a forward least-privilege migration granting the API role read-only
  access to the append-only currency-scale registry.
- Keep the frozen five-field OpenAPI schema unchanged.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-28
