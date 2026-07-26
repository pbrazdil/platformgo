Title: Preserve the deterministic Go rejection transition and reason

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_fills.rs::rejected_order_persists_reason`
- `tests/integration/postgres/order_rejection_test.go::TestRejectedOrderPersistsReason`

Conflict or ambiguity:
The pinned source test creates a pending mirror row and calls an unrestricted
`mark_rejected(order_id, reason)` helper with
`MARGIN_EXCEEDS_FREE_BALANCE`. It then proves that a second call with `OTHER`
cannot rewrite the terminal reason. The accepted Go engine has no arbitrary
rejection-reason writer: an admitted order is `working`, and its terminal
reason is produced by the ordered deterministic transition that actually
rejects it. The implemented reachable path activates a stop-market order and
rejects it as `slippage_exceeded`.

Economic/API impact:
The Go transition atomically releases the order's exact margin reservation,
persists the terminal order and reason, advances the receipt/checkpoint chain,
and creates no ledger entry because the funded balance does not change. Retry,
duplicate delivery, restart, and later market inputs cannot duplicate the
transition or rewrite the reason. This decision does not add a rejection field
to the frozen external order contract; external field activation remains a
separate compatibility decision.

Options considered:
1. Add an unrestricted persistence helper solely to reproduce the source
   fixture's arbitrary reason.
2. Force the deterministic engine to manufacture the source fixture's margin
   reason even though that is not the transition under test.
3. Preserve the current Go transition as authority while retaining the source
   assertions that the reason is absent before rejection, exact and durable
   after rejection, and immutable once terminal.

Decision:
Choose option 3. Current accepted Go behavior is the source for reachable
order state and rejection classification. The source fixture's arbitrary
writer and literal reason are implementation details; its observable
durability and no-rewrite requirements remain preserved.

Tests added/changed:
- `TestRejectedOrderPersistsReason` proves the working order initially has no
  reason, the ordered stop activation persists exact `slippage_exceeded`, and
  a later market input cannot re-reject the order or change its reason/version.
- The same PostgreSQL 17 test proves exact reservation and release, injected
  pre-commit rollback, stable duplicate replay, restart recovery,
  reconciliation, and an unchanged frozen external contract.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-26
