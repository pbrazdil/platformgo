# 002 — Idempotent command and crash boundary

Profile: `implementation`

## Assignment

Implement the smallest command path that produces one ledger transaction and
one response under duplicate delivery. Own only the supplied application
package and its focused tests.

## Fixture

The starting code inserts a command, commits a ledger entry in a second
transaction, generates a new UUID on every retry, and acknowledges the message
before either transaction commits. A failpoint can crash immediately after the
ledger commit.

## Required outcome and evidence

- One PostgreSQL transaction claims the stable business key, command result,
  ledger effect, input receipt, checkpoint, and outbox rows.
- Same key plus same canonical request returns the stored result.
- Same key plus a different request is rejected.
- Crash-after-commit redelivery is a no-op with the original IDs.
- Tests identify the transaction, idempotency, acknowledgment, and restart
  boundaries and prove exact row counts.

## Forbidden actions

- Network calls inside the database transaction.
- A new ID on retry.
- Claiming transport-level exactly once.
- Sleeps, permanent skips, or in-memory-only deduplication.

## Rubric

Any duplicate economic effect, acknowledgment before commit, unstable identity,
or missing crash proof is a critical failure.
