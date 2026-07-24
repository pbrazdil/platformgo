# Testkit instructions

The testkit must make deterministic behavior easy and nondeterministic behavior difficult.

- Provide manual clocks, deterministic IDs, explicit market events, bounded schedulers, semantic failpoints, isolated PostgreSQL schemas/databases, and isolated NATS resources.
- No hidden live network access, environment mutation, wall clock, sleeps, or process-global caches in model tests.
- Do not use `t.Parallel()` until isolation has been reviewed and policy explicitly permits it.
- Do not add broad helpers that hide economic inputs or assertions.
- Failpoints name semantic transaction boundaries, not approximate timing windows.
- Cleanup is mandatory and verified: goroutines, listeners, files, databases, schemas, streams, consumers, subscriptions, and processes.
- Shared harness changes require harness-owner review and must remain backward compatible with existing accepted tests unless intentionally migrated.

Read `TESTING.md` and `docs/TEST_PORTING_PLAYBOOK.md`.
