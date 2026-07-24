# Economic and Execution Invariants

These invariants are stronger than implementation details. A violation is a correctness incident. The engine must reject or halt rather than commit a state that violates them.

## 1. Determinism

For a fixed code version:

```text
S1, O = Apply(S0, ordered inputs, config versions, instrument versions)
```

is a pure function.

- Repeating the same input history produces byte-equivalent canonical decisions, events, ledger entries and state hashes.
- Wall-clock time, scheduling, map order and network timing do not change the result after input ordering is assigned.
- Every economic decision records the input ID, stream sequence, configuration version, instrument revision, logical time and relevant market sequence.

## 2. Input sequencing

- Each engine shard applies input stream sequences strictly monotonically.
- An already processed input is a no-op with the same recorded result.
- A sequence gap, regression, duplicate with different payload, or unknown schema is fatal to shard readiness.
- Commands for one account have a monotonically increasing account sequence.
- Events for one aggregate have a monotonically increasing aggregate version.

## 3. Ledger

- Ledger entries are immutable.
- Every ledger transaction has a stable transaction ID and idempotency/business key.
- The algebraic sum of entries in each currency for a balanced transaction is zero.
- A command cannot create more than one ledger transaction for the same economic effect.
- Corrections use compensating transactions; historical entries are never edited or deleted.
- Balance projections equal the exact fold of ledger entries through the recorded ledger sequence.
- No balance is derived from floating-point arithmetic.

## 4. Commands and idempotency

- The same idempotency scope/key and same canonical request return the same response and command ID.
- The same idempotency scope/key with a different canonical request is rejected.
- A command is terminal exactly once: completed or rejected.
- A duplicate command/input cannot create an additional order, fill, ledger transaction, job, or event.
- Unknown outcome is resolved by command lookup; it is never retried with a new identity.

## 5. Orders

For every order:

```text
0 <= filled_quantity <= order_quantity
remaining_quantity = order_quantity - filled_quantity
```

- Filled quantity equals the exact sum of fill quantities linked to the order.
- Average fill price equals the exact weighted mean defined by matching tests and rounding policy.
- Terminal orders do not return to a non-terminal state.
- A cancellation or modification cannot affect another account’s order.
- Modification preserves order identity unless an explicit compatibility test specifies replace semantics.
- Invalid side, type, TIF, quantity, price, trigger or precision is rejected; no invalid value defaults to a buy, zero, or another valid value.
- FOK never partially fills or rests.
- IOC never remains working after its execution attempt.
- Reduce-only execution cannot increase absolute exposure or cross through flat into opposite exposure.

## 6. Matching and fills

- Matching uses the market state established by ordered engine inputs.
- Book levels are consumed in deterministic price-time order defined by tests.
- A fill has a stable ID and links to exactly one command/order/account/instrument.
- Duplicate market or command delivery cannot duplicate a fill.
- Fill price, quantity, fee and timestamps are exact and reproducible.
- Missing, stale, gapped or invalid market data cannot be silently replaced with zero, last-known data outside policy, or a different instrument’s data.

## 7. Positions

- Position quantity is the exact fold of fills under the account’s OMS mode.
- Netting mode has at most one net economic position per account/instrument.
- Hedging mode preserves independently addressable legs where required by tests.
- Reducing a position realizes PnL only for the reduced quantity.
- Reversal closes the existing exposure first and opens only the residual quantity in the opposite direction.
- A flat position has zero quantity and no active position-bound protective orders.
- Realized PnL, unrealized PnL and entry price use named exact formulas and rounding rules.

## 8. Brackets and protective orders

- Protective legs belong to one entry/position scope and account.
- Long exposure is protected by sell-side exits; short exposure by buy-side exits.
- Protective orders are reduce-only.
- Held legs become active exactly once when their activation condition is satisfied.
- Filling one OCO leg cancels all incompatible sibling legs exactly once.
- Closing or flattening a position cancels its remaining protection.
- Reversing exposure cannot leave protection from the previous direction active.
- Restart recovery restores valid protection without duplication or orphaning.

## 9. Margin, risk and liquidation

- All risk inputs are explicit: balance/equity, positions, orders, mark, leverage, fee schedule, margin mode and config revision.
- Risk-increasing orders require fresh, gap-free market data.
- Used margin and order reservations are not double counted.
- Free margin is derived from authoritative equity and reservations using the tested formula.
- Cross and isolated allocations cannot leak into one another.
- Stop-out and liquidation are idempotent.
- Liquidation can only reduce risk; it cannot increase exposure.
- Broker modes are monotonic in restriction: `normal` permits policy; `close_only` forbids risk increase; `halt` forbids trading actions except explicitly tested operational recovery.

## 10. Funding and scheduled effects

- A funding interval is applied at most once per account/position/instrument interval key.
- Funding uses the rate, position and logical boundary explicitly associated with the interval.
- A scheduler retry or restart cannot duplicate settlement.
- Timer order is part of the engine input order.

## 11. Persistence and transactionality

For one economic decision, these consequences are atomic where applicable:

- command status/result;
- orders and positions;
- fills;
- ledger entries and balance projections;
- margin/reservation state;
- domain events;
- realtime outbox entries;
- scheduled jobs;
- input receipt and engine checkpoint.

No external network call occurs inside that transaction.

## 12. Messaging

- PostgreSQL outbox row exists before durable publication.
- Published message ID equals the stable domain/outbox ID.
- Consumer side effect and inbox receipt commit in the same transaction.
- Acknowledgment occurs after commit.
- Redelivery after any crash produces the same final business state.
- Stream limit exhaustion is visible and fail-closed; messages are not silently discarded.

## 13. Realtime

- Realtime is a projection of committed state.
- A realtime event cannot precede its economic commit.
- Events include stable identity and sequence.
- Duplicate realtime events do not change client state twice.
- A detected gap causes snapshot reload; clients do not continue on an unproven partial history.
- Failure to publish realtime does not roll back an already committed economic transaction.

## 14. API compatibility

- Decimal values are serialized exactly as required by contract tests.
- Omitted and null fields are not interchangeable unless tests say so.
- Error codes and status mapping are stable.
- An accepted idempotent retry returns the stored response, not a newly rendered approximation.
- API reads never expose a partial transaction.

## 15. Security and audit

- No secret, token, password, private key, full credential DSN, or TOTP seed appears in logs or events.
- Every privileged risk/configuration change records actor, request ID, before/after values, logical time and configuration version.
- Application roles cannot bypass the engine to mutate monetary state.
- Failed authorization has no business side effect.

## 16. Required property tests

At minimum, fuzz/property suites must continuously verify:

- duplicate input is idempotent;
- replay produces the same state hash;
- order filled quantity equals fill sum;
- reduce-only never increases exposure;
- reversal equals close plus residual open;
- flat positions have no active protection;
- ledger transactions balance per currency;
- balance projection equals ledger fold;
- independent accounts do not influence one another;
- deterministic results are unchanged by repeated runs and race-enabled execution;
- crash at every failpoint converges to the same valid final state.
