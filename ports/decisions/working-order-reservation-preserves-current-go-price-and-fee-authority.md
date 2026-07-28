Title: Preserve the current Go working-order reservation authority

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_balances.rs::a_working_order_locks_its_reserved_margin`
- `apps/app/tests/common/mod.rs::seed_instrument`
- `internal/engine/trading_risk.go::orderMargin`
- `internal/domain::PositionMargin`
- `internal/domain::TradingFee`
- `internal/adapters/postgres/compatibility_store.go::Balances`

Conflict or ambiguity:
The pinned Rust test funds an account with `10000` USDC, submits a
non-reduce-only GTC BUY LIMIT order for quantity `1` at price `50000`, marks it
working, and computes only initial margin:

```text
50000 * 0.02 / 50 = 20
```

It therefore asserts `locked=20` and `free=9980`. Its shared instrument helper
nevertheless configures a maker fee rate of `0.0002` and a taker fee rate of
`0.0005`; the source assertion reserves neither prospective fee.

Current Go deliberately has a more conservative economic authority. A working
order reserves initial margin at the maximum of the current mark, limit,
trigger, and eligible executable prices. It separately reserves the worst
non-negative maker or taker fee at the same authoritative price. Margin and
fee are each rounded half-even at the settlement currency scale, then added.
The committed `used` projection includes both amounts, and the production HTTP
reader maps that committed `used` value to wire `locked` without recomputing it
at the edge.

For the pinned quantity, limit, initial-margin rate, leverage, and fee rates,
when the limit is the authoritative price, current Go computes:

```text
initial margin = 50000 * 1 * 0.02 / 50 = 20
fee reserve    = 50000 * 1 * 0.0005     = 25
used/locked    = 45
free           = 9955
```

The source literal and current Go cannot both be the accepted exact money
behavior. Removing the fee reserve would also weaken current Go margin
admission by allowing prospective execution fees to remain spendable.

Economic/API impact:
This is an intentional economic and compatibility deviation. Current Go
clients receive `locked=45` and `free=9955` for the source-equivalent resting
order instead of `locked=20` and `free=9980`. The additional `25` is a
reservation projection, not a cash transfer: submitting or canceling the order
must create no ledger entry and must not change funded `total` or `equity`.

The decision preserves the existing fail-closed price authority. A mark,
trigger, or eligible executable price above the limit can increase the
reservation. It does not authorize a fallback price when required market
authority is absent or stale.

Options considered:
1. Reproduce the source limit-only initial-margin reservation and omit
   prospective fees.
2. Preserve the source wire values while hiding fee or price reserves in a
   second spendability calculation.
3. Preserve the current Go maximum-price authority, worst non-negative fee
   reserve, and single committed balance projection.

Decision:
Choose option 3 under the owner's instruction to preserve current Go behavior
as the source.

The source-vector acceptance uses a fresh deterministic book with
`mark=49999`, `bid=49998`, and `ask=50001`, followed by a BUY LIMIT order for
quantity `1` at `50000`. The order is genuinely non-marketable and the unequal
lower mark proves that the limit participates in the reservation price. With
initial-margin rate `0.02`, leverage `50`, maker fee `0.0002`, taker fee
`0.0005`, and USDC scale two, the exact accepted result is `total=10000`,
`locked=45`, `free=9955`, and `equity=10000`.

A separate price-authority control must use a non-marketable book with a mark
above the limit and prove that the higher mark controls both margin and fee.
For `mark=60000`, `bid=59999`, `ask=60001`, and the same order and
configuration, the exact result is margin `24`, fee reserve `30`,
`locked=54`, and `free=9946`. This control proves the broader current Go rule;
the pinned source row alone does not.

Required implementation evidence:
- First record the source-literal failing checkpoint against the real
  production path: expected `20/9980`, observed `45/9955`.
- Prove the accepted `45/9955` result through the deterministic engine,
  PostgreSQL 19 durable transaction, and authenticated ownership-gated HTTP
  read.
- Prove the independent higher-mark `54/9946` price-authority control.
- Prove half-even rounding at the settlement currency scale for margin and fee
  independently before addition.
- Prove an injected pre-commit failure leaves no order, balance projection,
  receipt, checkpoint advance, ledger effect, or domain/realtime outbox effect.
- Prove exact retry, same-sequence replay, and later-sequence duplicate
  delivery produce one business effect and stable hashes.
- Prove restart recovery and reconciliation reproduce the working order,
  market authority, sequence, and exact balance projection.
- Prove cancel releases the exact reservation once, with no ledger effect.
- Prove absent or stale market authority, gaps, corrupt durable state, and
  changed-content duplicate identity fail closed.
- Prove anonymous and foreign-account reads are denied and the API role remains
  least privilege.

The mapped row remains `unreviewed` and `placeholder` until a separate
implementation layer supplies these production-wired proofs and a later,
isolated acceptance layer records the intentional deviation. This decision
does not accept fills, realtime continuity, margin ratio, maintenance margin,
or any adjacent balance behavior.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-28
