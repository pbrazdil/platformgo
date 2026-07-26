Title: Preserve current Go fill realization and position identity

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_fills.rs::fill_realized_isolates_hedged_legs_by_position`
- `tests/integration/postgres/fill_history_test.go::TestFillRealizedIsolatesHedgedLegsByPosition`

Conflict or ambiguity:
The pinned source test writes four legacy mirror rows directly. It stores
explicit realized PnL zeroes on both opening fills and free-form position
tokens that its external projection converts to public position identifiers.
The current Go engine records realized PnL only when a fill reduces or closes
exposure; opening fills therefore persist neither a realized amount nor its
settlement currency. Current durable positions and fills use deterministic
UUID identities, and the existing internal Go position projection exposes the
raw UUID text. The external `/fills` route remains inventory.

Economic/API impact:
Each realized amount remains an exact settlement-currency value. The
deterministic engine computes `50 USDC` only for the targeted long close and
`30 USDC` only for the targeted short close. PostgreSQL stores the amount and
currency together on the immutable fill, enforces their nullability pair, and
the compatibility reader fails closed if that pair is incomplete. Opening
fills retain the truthful absence of realization rather than manufacturing a
zero-valued economic event. This decision does not activate an external route
or freeze a new public position-ID representation.

Options considered:
1. Manufacture `0 USDC` realization records on opening fills and introduce the
   legacy public position-token transformation solely to match the mirror.
2. Return a realized decimal without its currency.
3. Preserve current deterministic Go behavior: absent amount and currency on
   opens, exact paired amount and currency on closes, and durable UUID
   position identity.

Decision:
Choose option 3. Current Go fill semantics and durable identity are
authoritative. The accepted source requirements are that each closing fill
reports its own exact realized PnL, opening fills cannot acquire another leg's
profit, and long and short fills remain correlated to distinct positions.
Source-mirror `Some("0")` and its public/free-form position representation are
not imported. Any external identifier transformation or `/fills` field
activation requires a separate complete compatibility decision and contract
review.

Tests added/changed:
- `TestFillRealizedIsolatesHedgedLegsByPosition` drives real deterministic
  engine decisions through PostgreSQL 17: one long opens at `101` and closes
  at `151` for exact `50 USDC`; one short opens at `100` and closes at `70`
  for exact `30 USDC`.
- The test proves distinct stable position UUIDs, targeted reduce-only closes,
  `(nil, nil)` realized amount/currency on opening fills, exact paired values
  on closing fills, four immutable account-scoped rows, and least-privilege
  reads.
- Exact production forward, backward, and side-filtered query plans remain
  indexed over a representative 100,000-fill PostgreSQL 17 dataset.
- The test exercises only the current internal fill projection. It does not
  prove or activate an external HTTP fills response.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-26
