# Migration Instructions

These rules apply to every file under `migrations/` and add to the repository-root `AGENTS.md`.

- Read `DATABASE.md`, `INVARIANTS.md`, and the migration ADRs before editing.
- A migration's path and bytes freeze when it first reaches a protected branch
  or is applied to a shared or persistent database, whichever occurs first.
- Application only to an explicitly disposable local/test database does not
  freeze an unpublished, unshared candidate. Reset or recreate that database
  before testing changed bytes.
- Before freeze an unpublished, unshared candidate may be edited, renamed,
  reordered, deleted, amended, or squashed. No branch parking or cherry-pick
  ceremony is required merely because the candidate was locally committed.
- After freeze never edit, rename, reorder, delete, or squash the migration.
  Add one new timestamped forward migration for every correction.
- If a pre-merge candidate is applied to a shared or persistent database,
  record its path, SHA-256, source commit, and environment in the PR immediately
  and treat it as frozen. Git policy cannot infer this external fact.
- Production down migrations are forbidden.
- State the expected lock level, table rewrite risk, transaction behavior, compatibility window, and failure/retry behavior in the PR.
- Use expand/backfill/contract sequencing for destructive changes. The contract phase occurs only after old code and data dependencies are gone.
- Backfills are bounded, resumable, observable, and idempotent. Do not hide an unbounded data rewrite inside DDL.
- Do not perform network calls or external writes from migrations.
- Economic columns do not use SQL floating-point types.
- Use explicit columns; `SELECT *` is forbidden.
- Add an upgrade test from the previous released schema and representative data.
- Run `make policy` and the relevant migration/upgrade test before completion.

Stop for owner review when shared/persistent application or freeze state is
uncertain, or when a migration can lose data, require prolonged blocking
locks, break one-release-back compatibility, or cannot be made safe through a
forward sequence.
