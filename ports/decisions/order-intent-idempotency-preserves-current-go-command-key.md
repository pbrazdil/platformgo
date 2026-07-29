Title: Preserve the current Go transport-derived order idempotency key

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_orders.rs::a_reused_intent_id_replays_only_an_identical_payload`
- `internal/edge/http.go::handleSubmitOrder`
- `internal/application/order_submission.go::OrderSubmission.SubmitOrder`
- `internal/adapters/postgres/command_journal.go::CommandJournal`

Conflict or ambiguity:
The pinned Rust test supplies `intent_id = "idem-1"` without a distinct HTTP
idempotency key. It expects an identical order payload to return the original
order identity and a changed payload using that intent to conflict.

Current Go maps a missing transport `Idempotency-Key` to
`"intent:" + intent_id`. Durable identity is keyed by the authenticated
principal/method/account scope plus that resulting key and the canonical
request hash. `trading.order_intents` does not impose a separate unique
`(account_id, intent_id)` constraint, so the same intent under two different
explicit transport keys is not the same current Go command identity.

The pinned synchronous Rust order result also crosses a different boundary.
Current Go commits an asynchronous admission graph—idempotency, pending
command, immutable replay response, order intent, account-shard assignment,
and API outbox—before the serialized engine performs risk and materializes an
order.

Economic/API impact:
The accepted boundary performs no economic calculation or rounding and does
not mutate balances, ledger, orders, fills, positions, funding, domain events,
or realtime publications. Exact decimals are canonicalized from `0.010` to
`0.01` and `0.020` to `0.02` before hashing, without notional, margin, fee, or
PnL calculation.

The first admission and its immutable response commit atomically in
PostgreSQL. A fresh application/journal instance must return the byte-exact
stored `202`, headers, body, order ID, and command identity. A changed request
under the same mapped key must return the deterministic idempotency conflict
without another durable graph.

Options considered:
- Add global `(account_id, intent_id)` uniqueness and conflict handling. This
  would create a second command identity that current Go does not have and
  would change behavior for callers supplying distinct explicit transport
  idempotency keys.
- Port the synchronous source handler boundary, including risk acceptance and
  materialized order creation. This would collapse the current Go
  API-admission and serialized-engine boundaries and expand the accepted
  economic contract beyond the observable behavior needed by this source
  test.
- Preserve the current Go scope/key identity and asynchronous admission
  boundary while exercising the exact lowercase source inputs and their
  canonical persisted representation. This keeps one durable authority and
  introduces no new economic behavior.

Decision:
Choose the current Go scope/key and asynchronous-admission option. Exercise
the source intent through the exact transport-derived key `intent:idem-1`.
Accept identical replay and changed-payload conflict only for that same
durable scope/key. Do not add a second intent-uniqueness rule or move engine
risk/order materialization into the API transaction.

Required evidence:
- PostgreSQL 19 Beta 2 and the least-privilege `platformgo_api` role.
- Concurrent identical first deliveries converge on one admission graph.
- A fresh journal with a different clock returns the exact persisted response.
- `SELL 0.020 @ 80000` under `intent:idem-1` conflicts after the accepted
  `BUY 0.010 @ 50000`.
- Account sequence remains one; canonical command/outbox identities and
  revision bindings remain exact.
- The funded baseline is unchanged and no order, fill, position, ledger,
  engine receipt, duplicate receipt, funding, domain, or realtime effect is
  created by admission.
- Hostile inherited ACLs and `TRUNCATE` cannot destroy commands,
  idempotency records, or replay responses.
- Mutation evidence proves the source acceptance fails when changed-request
  hash conflict detection is bypassed, followed by byte-for-byte production
  source restoration.

Compatibility limit:
This decision does not claim global intent uniqueness across different
explicit idempotency keys, synchronous engine acceptance, risk or margin
approval, a materialized order, HTTP routing/status mapping, NATS publication
or acknowledgment, ambiguous PostgreSQL commit fault injection, engine
restart, fills, balances, ledger, positions, or realtime continuity.

Tests added/changed:
- `tests/integration/postgres/order_intent_idempotency_test.go`
- remove the placeholder implementation of
  `TestAReusedIntentIDReplaysOnlyAnIdenticalPayload`

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-29
