Title: Preserve classified trade types for durable Go fills

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_fills.rs::fill_history_returns_side_and_trade_type`
- `internal/engine/trading_positions_source_test.go::TestTradingFillTypeIsClassifiedOpenIncreaseReduceFlipClose`

Conflict or ambiguity:
The pinned source test inserts an artificial legacy mirror row without an
`entry` classification and requires its projected `trade_type` to be `None`.
Current accepted Go behavior assigns every engine-produced fill exactly one of
`open`, `increase`, `reduce`, `flip`, or `close`. The authoritative durable
schema requires a non-null, nonempty `position_effect`, and the compatibility
reader fails closed when the stored value is not exactly one of those five
canonical classifications.

Economic/API impact:
The replacement does not create or project an unclassified durable fill.
Reachable current-Go fills retain their exact economic position effect across
persistence, restart, reconciliation, and compatibility reads. This prevents a
nullable legacy mirror artifact from weakening the deterministic classification
or hiding corrupt durable state. The external fills route remains inactive; its
eventual HTTP field name, omission/nullability behavior, and activation remain
unresolved until a separate contract gate.

Options considered:
1. Import the legacy nullable mirror behavior and allow a durable fill without a
   position effect.
2. Add a second nullable compatibility classification that can diverge from the
   authoritative engine effect.
3. Preserve the current Go classification as authority, expose the exact stored
   canonical effect, and reject missing, aliased, or unknown effects.

Decision:
Choose option 3. The current accepted Go engine behavior and durable fail-closed
invariants outrank the artificial nullable mirror fixture. This is an approved
semantic adaptation of the pinned test, not permission to normalize or repair
invalid immutable rows. External route activation remains a separate contract
gate.

Tests added/changed:
- `TestFillHistoryReturnsSideAndTradeType` proves exact `BUY`/`SELL` casing and
  all five canonical classifications through real PostgreSQL 17 and the
  least-privilege API role.
- The same test proves both fill readers reject uppercase, mixed-case,
  whitespace-padded, and unknown durable effects through current and recreated
  API pools, return zero projections, and preserve the raw immutable evidence.
- `TestTradingFillTypeIsClassifiedOpenIncreaseReduceFlipClose` remains the
  accepted engine authority for the five reachable position effects.

Approver: Petr Brazdil, through the active owner instruction: "Zachovej soucasne chovani jako zdroj."

Date: 2026-07-26
