# PostgreSQL adapter instructions

PostgreSQL is the authoritative monetary store.

- Use `pgx` directly; no ORM or generic repository framework.
- SQL stays in this adapter or migrations and selects explicit columns.
- Economic columns use exact `NUMERIC`, integers, UUID, text, timestamp, or binary types; no SQL floating point.
- One decision transaction atomically persists receipts/checkpoints, fills, ledger entries, state, command results, and outbox records.
- Lock rows in documented deterministic order.
- Do not perform network calls while a transaction is open.
- Retry only classified serialization/deadlock failures, with bounded attempts and stable idempotency keys.
- API roles cannot mutate economic tables; enforce privileges, not comments.
- Historical facts are append-only. Corrections use compensating records.
- Tests cover duplicate requests, crash after commit before acknowledgment, transaction rollback, lock ordering, migration from the previous release, and reconciliation queries.

Read `DATABASE.md`, `INVARIANTS.md`, and `migrations/AGENTS.md`.
