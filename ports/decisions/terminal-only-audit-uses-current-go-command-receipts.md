Title: Preserve current Go terminal audit authority

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_fills.rs::terminal_only_audit_skips_running_history_but_keeps_recovery_and_terminal_row`
- `tests/integration/postgres/terminal_audit_test.go::TestTerminalOnlyAuditSkipsRunningHistoryButKeepsRecoveryAndTerminalRow`

Conflict or ambiguity:
The pinned source test stores running recovery state in a legacy saga instance,
does not append history for a nonterminal transition, and appends one history
row when the saga becomes terminal. The current Go runtime has no saga storage,
scheduler, or intermediate saga-transition contract. It persists a pending
command and in-progress idempotency record for recovery, then atomically commits
the terminal command result, completed idempotency response, immutable engine
input receipt, deterministic projection, and shard checkpoint.

Economic/API impact:
The accepted behavior preserves one recoverable running identity and exactly one
terminal business fact. The terminal action creates only one NETTING account
projection; it cannot create a balance, ledger transaction, funding settlement,
book, order, fill, position, domain event, or realtime publication. Retry and
both duplicate-delivery forms cannot add another terminal business receipt or
account version. No external HTTP, gRPC, realtime payload, identifier, decimal,
or deployment contract changes.

Options considered:
1. Add the legacy saga tables, scheduler, and history model.
2. Add a synthetic saga-history projection used only to mirror source storage.
3. Preserve current Go authority: use the durable command and idempotency state
   as the running recovery record and the immutable business input receipt as
   the terminal audit fact.

Decision:
Choose option 3. The observable requirements are a stable recoverable running
identity, no terminal audit fact before completion, one terminal fact after
completion, and completed durable recovery state. Legacy saga schema names,
intermediate runtime mechanics, and scheduler plumbing are not compatibility
contracts.

Tests added/changed:
- `TestTerminalOnlyAuditSkipsRunningHistoryButKeepsRecoveryAndTerminalRow`
  begins one pending command through the API role and recovers the same
  in-progress identity through independent Begin and Replay readers.
- A post-persist/pre-commit failpoint proves atomic rollback of command,
  idempotency, account, receipt, checkpoint, and every direct or triggered
  economic, event, funding, market, ledger, identity, and realtime projection.
- A fresh engine owner recovers the original state and commits exactly one
  accepted terminal decision with one NETTING account effect and no other
  economic or event effect.
- Same-sequence replay returns the complete stored business decision without
  advancing state. Later-sequence redelivery advances only the audit chain and
  records one no-effect duplicate-delivery receipt. Exact replay of that
  delivery returns the complete stored duplicate decision.
- Final restart recovery reproduces the exact state hash and sequence.
  Reconciliation is ready with one business receipt, one duplicate-delivery
  receipt, and no delivery, command, configuration, or messaging mismatch.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-26
