# Migration Instructions

These rules apply to every file under `migrations/` and add to the repository-root `AGENTS.md`.

- Read `DATABASE.md`, `INVARIANTS.md`, and the migration ADRs before editing.
- Applied migration files are immutable. Never edit, rename, reorder, delete, or squash them.
- Add one new timestamped forward migration for every schema correction.
- Production down migrations are forbidden.
- State the expected lock level, table rewrite risk, transaction behavior, compatibility window, and failure/retry behavior in the PR.
- Use expand/backfill/contract sequencing for destructive changes. The contract phase occurs only after old code and data dependencies are gone.
- Backfills are bounded, resumable, observable, and idempotent. Do not hide an unbounded data rewrite inside DDL.
- Do not perform network calls or external writes from migrations.
- Economic columns do not use SQL floating-point types.
- Use explicit columns; `SELECT *` is forbidden.
- Add an upgrade test from the previous released schema and representative data.
- Run `make policy` and the relevant migration/upgrade test before completion.

Stop for owner review when a migration can lose data, require prolonged blocking locks, break one-release-back compatibility, or cannot be made safe through a forward sequence.
