Title: Preserve atomic Go order and fill settlement

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_fills.rs::saga_reconcile_settles_orphaned_order_from_fills`
- `tests/integration/postgres/engine_store_test.go::TestSagaReconcileSettlesOrphanedOrderFromFills`

Conflict or ambiguity:
The pinned source test separately commits a pending order and a fill, then
invokes a repair saga that derives the order's terminal state from the orphan
fill. The current Go runtime does not admit that intermediate state. One
deterministic decision atomically commits its order transition, fill, position,
exact balance and ledger effects, receipt, checkpoint, and outbox records.
Reconciliation verifies those facts and fails closed on disagreement; it does
not rewrite immutable economic history.

Economic/API impact:
The accepted behavior prevents a fill or fee from existing without its matching
order, position, balance, ledger, and audit facts. Retry and both duplicate
delivery forms cannot charge the fee or create the fill twice. A corrupted
order/fill projection makes readiness false while leaving the immutable fill,
position, balance, and balanced ledger unchanged. Operational realtime lease
time remains visible through worker readiness, but it is not an implicit
wall-clock input to a durable economic reconciliation fault. No external HTTP,
gRPC, realtime payload, identifier, or decimal contract changes.

Options considered:
1. Add the legacy repair saga and permit separately committed orphan fills.
2. Repair an inconsistent order projection during reconciliation.
3. Preserve current Go authority: atomically commit the complete economic
   effect and fail closed without rewriting facts if durable projections
   disagree.

Decision:
Choose option 3. Current Go transaction and reconciliation semantics are
authoritative. The accepted source requirements are that an order with no fill
remains non-terminal, one committed fill records the exact filled quantity,
and repetition produces no second settlement. The legacy separately committed
orphan state and repair mechanism are not imported.

Tests added/changed:
- `TestSagaReconcileSettlesOrphanedOrderFromFills` drives a resting order into
  one exact `0.01 @ 100` maker fill with a `0.5 USDC` fee through PostgreSQL 17.
- The post-persist/pre-commit failpoint proves rollback of fill, position,
  balance, balanced ledger, receipt, checkpoint, and outbox effects.
- A fresh store recovers the working zero-fill state before retrying the exact
  envelope. The retry commits once; same-sequence replay and later-sequence
  redelivery leave the economic projection unchanged.
- Restart recovery reproduces the receipt chain and exact state hash.
  Reconciliation accepts the valid state, then detects an injected orphan
  projection, makes readiness false, and does not repair or alter immutable
  economic facts.
- Realtime reconciliation no longer compares operational `claimed_at` lease
  metadata with wall time. A future lease remains operationally not-ready but
  cannot create a timing-dependent durable shard fault; structural realtime
  corruption still fails closed.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-26
