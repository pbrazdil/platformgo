Title: Preserve the current Go durable unfunded-order rejection boundary

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_order_caps.rs::submit_denies_open_on_unfunded_account`
- `internal/application/order_submission.go::OrderSubmission.SubmitOrder`
- `internal/engine/trading_risk.go::marginAdmissionReason`
- `internal/adapters/postgres/engine_store.go::EngineStore.ApplyTrading`

Conflict or ambiguity:
The pinned Rust test submits a valid risk-increasing `LIMIT BUY 1 @ 100` for an
unfunded account through its application command handler. It expects the
submission call itself to return a synchronous `AppError::Denied` with reason
`InsufficientFunds` and a message containing `free margin`.

Current Go separates durable command admission from the serialized economic
decision. This acceptance begins with an already durable pending command and
immutable replay response; it does not exercise an HTTP or application
admission path. The single engine writer processes the ordered input and
atomically persists the terminal command result, input receipt, checkpoint,
and replay lifecycle in PostgreSQL. For an absent or non-positive settlement
balance, that durable result is `rejected / insufficient_funds`.

The source synchronous error contract and current Go asynchronous durable
contract cannot both be the accepted behavior. Inventing a pre-admission
balance check would create a second economic authority outside the serialized
engine and could race the authoritative balance, market, configuration, and
instrument revisions.

Economic/API impact:
The economic outcome is preserved: an unfunded account cannot create or
increase exposure. This decision does not accept or describe how a client
submits the command, observes its result, or maps it to an HTTP response.

The accepted rejection creates no order, reservation, fill, position, ledger,
domain-event, or realtime publication effect beyond the durable
command-admission outbox and duplicate-delivery audit. It commits the
successfully processed rejection receipt and checkpoint so duplicate delivery,
restart, and reconciliation reproduce the same result.

Options considered:
1. Add a synchronous application-layer balance check and reproduce the Rust
   error response and message.
2. Return a synchronous error derived from a later engine decision while
   holding the HTTP request open.
3. Preserve the current Go asynchronous command boundary and accept the exact
   durable engine result `rejected / insufficient_funds`.

Decision:
Choose option 3 under the owner's instruction to preserve current Go behavior
as the source.

The source acceptance uses deterministic `LIMIT BUY 1 @ 100`, explicit ordered
market authority, and both current Go representations of an unfunded account:
an absent settlement-balance row and an explicit exact zero balance. Both must
produce the same durable result and no additional economic, domain-event, or
realtime publication effect beyond command admission and duplicate-delivery
audit.

Required implementation evidence:
- Prove the exact durable command and receipt result
  `rejected / insufficient_funds` on PostgreSQL 19.
- Prove the decision and state hash chain against explicit ordered input,
  logical time, configuration, instrument, and market revisions.
- Prove an injected pre-commit failure leaves the command pending and commits
  no receipt, checkpoint advance, order, balance, ledger, event, or outbox
  effect; retry with the same identity must then commit one rejection.
- Prove same-sequence replay returns the original immutable decision without a
  second effect.
- Prove later-sequence delivery of the same business input writes only its
  duplicate-delivery audit receipt and checkpoint advance.
- Prove fresh-store recovery and reconciliation reproduce the rejection and
  report no mismatch.
- Prove by a focused mutation that bypassing the non-positive-balance gate
  makes the acceptance test fail, then restore the production source
  byte-for-byte.

This decision does not accept or claim the Rust synchronous `AppError`, its
message text, an HTTP status transition, a NATS acknowledgment path, a funded
account's margin calculation, fill execution, or realtime continuity.

Tests added/changed:
- `tests/integration/postgres/unfunded_order_test.go`
- remove the placeholder implementation of
  `TestSubmitDeniesOpenOnUnfundedAccount`

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-29
