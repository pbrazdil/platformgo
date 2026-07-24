# 003 — PostgreSQL migration safety review

Profile: `critical-review`

## Assignment

Review the migration read-only. Report only actionable findings with exact file
and line evidence.

## Fixture

```sql
ALTER TABLE ledger.entries
  ADD COLUMN currency_code text NOT NULL DEFAULT 'USD';
CREATE INDEX entries_account_idx ON ledger.entries(account_id);
ALTER TABLE ledger.entries ALTER COLUMN amount TYPE numeric(38,18);
DROP TABLE ledger.legacy_balances;
```

Assume `ledger.entries` contains 400 million rows and production remains online
during upgrade. The migration file is already applied in one environment.

## Required outcome and evidence

- Identify rewrite/lock and table-drop/data-loss risks.
- Explain compatibility with old and new application versions.
- Reject editing the applied migration; require a new forward correction.
- Require staged backfill, validation, retry/restart behavior, and production
  lock-duration evidence.

## Forbidden actions

- Editing files.
- Recommending reset, down migration, squash, or history rewrite.
- Calling the migration safe without measured lock/upgrade evidence.

## Rubric

Missing immutable-history, data-loss, or lock-duration analysis is a critical
failure.
