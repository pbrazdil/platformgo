# Operations and Production Safety

## PostgreSQL 19 qualification and production boundary

The current migrator and every runtime schema verifier require PostgreSQL 19 or
newer, with PostgreSQL 19 Beta 2 as the exact prerelease floor. Beta 2 is
qualified only for development and CI. It is not approved for production.
Production requires PostgreSQL 19 GA plus a production-like rehearsal from the
then-supported deployed major using the chosen `pg_upgrade`, dump/restore, or
logical-replication procedure. The release evidence must include a complete
pre-upgrade backup and restore drill, immutable artifact and database version
identity, application of every migration through the current tip, recovery, and
zero reconciliation mismatch. A clean-database Beta 2 CI run is not evidence of
a safe production major upgrade.

## 1. Deployment order

1. Provision PostgreSQL, NATS/JetStream and Centrifugo.
2. Validate backups, credentials, TLS and stream capacity.
3. Run the one-shot migrator with the new image.
4. Start stateless API and worker roles.
5. Start marketdata and establish fresh synchronized feed state.
6. Start exactly one engine for each shard using no-overlap deployment.
7. Verify readiness, input lag, invariant checks and reconciliation before enabling trading.

Schema migration failure blocks deployment.

### No-overlap migration protocol

For every no-overlap schema cutover, `halt` is only the first step. Withdraw
API and gRPC traffic, stop the engine, API background loops, outbox and event
publishers, projectors, marketdata roles, realtime workers, consumers, and
cleanup workers, then prove their leases, PostgreSQL sessions, and transactions
have ended. The runtime schema verifier runs when a process opens its pool; it
rejects a new process at the wrong migration tip but does not evict or
continuously reverify an already-open pool. Keep every old runtime process
stopped until the complete target migration set has committed and the new
artifact passes schema verification.

Classify migration errors by durable outcome:

- A PostgreSQL migration-statement error, `lock_timeout`, or
  `statement_timeout` raised before `COMMIT` aborts that transaction and leaves
  the prior recorded tip.
- A connection loss, client deadline, failover, or other error during or after
  `COMMIT` acknowledgment has an unknown outcome. It does not prove rollback.

For an unknown outcome, keep every runtime stopped and reconnect with the
migrator identity. Compare the exact filename and checksum in
`engine.schema_migrations` with the expected column, constraint, trigger, and
constraint-validation catalog state. Classify the database at an exact known
tip before retrying or choosing a binary. A journal/catalog disagreement stays
halted for a reviewed forward fix; never guess, edit migration history, or
restore selectively.

### Phase 3 broker-echo exact-replay upgrade

Migration `20260727000100_phase3_broker_echo_exact_replay.up.sql` is a
no-overlap cutover from the legacy broker-echo replay function to the dedicated
exact-response store:

1. Withdraw broker traffic. Stop and drain every old API instance and replay
   cleanup loop, then prove its leases, PostgreSQL sessions, and transactions
   have ended. No old process may overlap the migration or the new binary.
2. Measure and record the live legacy broker-echo row count, total response
   bytes, and complete legacy relation size including indexes and TOAST.
   Compare all three with the exact fixed bounds in the migration and retain
   the query output as release evidence. An excess or an unreconstructable live
   response blocks the cutover.
3. Record the candidate artifact digest and take a complete, restore-verified
   database backup/PITR boundary containing the legacy replay data and
   `engine.schema_migrations`.
4. Run the new-image migrator. Under bounded lock and statement timeouts it
   takes `SHARE` on `identity.idempotency_responses`, validates
   the live subset, and backfills the dedicated table atomically. A definite
   pre-commit error leaves the previous tip authoritative; drain the blocker or
   correct the source data through a reviewed process before retrying.
5. Treat connection loss, client deadline, failover, or missing `COMMIT`
   acknowledgment as an unknown outcome. Keep all API processes stopped.
   Compare the exact migration filename and checksum in
   `engine.schema_migrations` with catalog evidence for
   `identity.broker_echo_replays`, its primary key, expiry index, immutability
   trigger, claim/purge functions, and grants. Classify one exact tip before
   retrying or selecting a binary; a journal/catalog disagreement requires a
   reviewed forward fix.
6. After commit, run strict schema verification and permission probes. Prove
   that `platformgo_api` can execute the new claim and bounded purge functions,
   cannot directly read or mutate the dedicated table, and cannot use the
   legacy claim or replay table.
7. Start only the new API artifact. Verify same-key replay returns the exact
   stored status, logical required headers, and body bytes; different-request
   conflict, concurrent duplicate claim, lost HTTP acknowledgment, 24-hour
   PostgreSQL-time expiry, and expired-only bounded purge must all pass before
   restoring traffic.

Before any new traffic is accepted, an owner-authorized rollback is a complete
restore of the verified pre-migration boundary followed by the prior binary and
normal recovery checks. Never down-migrate or selectively restore replay
tables. After new traffic is accepted, rollback is a reviewed forward fix with
traffic withdrawn; restoring the old boundary would discard accepted durable
facts.

This cutover is qualified only on PostgreSQL 19 Beta 2 for development and CI
because its claim path uses PostgreSQL 19
`INSERT ... ON CONFLICT DO SELECT FOR UPDATE`. Rerun the complete policy,
format, lint, test, race, deterministic-repeat, PostgreSQL integration,
migration, recovery, and security gate on PostgreSQL 19 GA. Production remains
NO-GO before GA and remains blocked afterward until the production upgrade,
restore, recovery, and reconciliation evidence is complete.

### Phase 3 realtime schema upgrade

Migration `20260725001100_phase3_committed_realtime_outbox.up.sql` has an
enforced no-overlap compatibility boundary:

1. As the PostgreSQL security owner, pre-provision
   `platformgo_realtime` and `platformgo_realtime_repair` as `NOLOGIN`,
   `NOSUPERUSER`, `NOCREATEDB`, `NOCREATEROLE`, `NOREPLICATION`,
   `NOBYPASSRLS` roles with no memberships. Provision separate login roles
   that belong to exactly one of those roles.
2. Enter halt, drain the old engine singleton, and prove its ownership and
   transaction have ended.
3. Take the rollback backup and run the new-image migrator. A missing or unsafe
   role, lock timeout, invalid existing user channel, or failed constraint
   validation aborts the migration without partial application.
4. Start only the new API/workers/engine image. Every runtime verifies the
   exact immutable migration set. The database rejects economic receipts from
   an old engine that lacks the new runtime-schema binding.
5. Verify reconciliation and authoritative client snapshots before leaving
   halt.

Migration 011 runs in one transaction under the migrator's bounded
`lock_timeout`. Its existing-user channel validation scans `identity.users`;
the deployment stays halted for that validation. It adds new realtime tables
and indexes without rewriting existing economic tables. Any lock timeout or
validation failure rolls the migration back completely and is retried only
after the blocking transaction or invalid identity data is resolved.

After migration 011 commits, binary rollback to the prior engine is forbidden:
use forward-fix, keep trading halted, reconcile committed state, and require
clients to reload authoritative snapshots. Database rollback means restoring
the pre-migration backup during the halted window, not editing or reversing an
applied migration.

### Phase 3 balance-projection hash v3 upgrade

Migration `20260726000800_phase3_balance_projection_hash_v3.up.sql` introduces
a no-overlap engine boundary:

1. Enter halt, drain the old engine singleton, and prove its ownership and
   current transaction have ended.
2. Record the candidate artifact digest and take a complete, restore-verified
   backup/PITR boundary containing application state, immutable receipts,
   checkpoints, and `schema_migrations`.
3. Run the new-image migrator. It locks `trading.instruments`,
   `market.books`, `engine.input_receipts`, and
   `engine.duplicate_delivery_receipts` in writer-compatible order under a
   two-second lock timeout and a thirty-second statement timeout.
4. Treat SQLSTATE `55000`, a PostgreSQL statement/lock timeout, or another
   definite pre-commit migration error as a failed cutover. Keep writers halted.
   The transaction leaves the old schema and history intact; preserve the
   database and resolve malformed or incomplete history through an
   owner-reviewed forward repair or a complete restore. Resolve an unknown
   commit outcome through the no-overlap protocol above before taking any
   further action.
5. After a successful commit, start only the new engine image. New business
   and duplicate receipts are fenced to decision-hash v3, so an old engine
   must not be restarted against the upgraded database.
6. Verify engine recovery, checkpoint and decision/state hashes, the
   append-only currency-scale registry, exact balance projections, order
   reservations, nullable markless books, and zero reconciliation mismatches
   before leaving halt.

`trading.currency_scales` retains every historically accepted currency
code/scale binding even after the current instrument catalog changes. Update,
delete, and truncate are forbidden, including for the table owner; runtime
roles have no mutation grant. A scale conflict, malformed registry/history,
or disagreement between replay and durable projections makes readiness false.
Do not repair these facts in place.

After the migration commits, operational rollback is a reviewed forward fix
with trading halted. If an owner authorizes full rollback, stop every writer
and restore the complete database to the recorded pre-migration boundary
before deploying the prior binary; then run full reconciliation. Never
selectively restore tables, edit an applied migration, or run an old binary
against the v3 schema. Any facts accepted after the boundary are lost by a
full restore and require the normal disaster-recovery decision.

### Phase 3 frozen-effective-leverage hash v4 upgrade

Migrations `20260726000900_phase3_fill_effective_leverage_hash_v4.up.sql` and
`20260726001000_phase3_validate_fill_effective_leverage.up.sql` introduce the
next no-overlap engine boundary:

1. At the v3 tip, scan for account/instrument risk leverage above the
   instrument maximum. If any exists, abort the cutover. Submit an
   owner-approved normal v3 configuration input through the single writer so
   the correction commits one immutable receipt, decision/state hashes,
   checkpoint, and matching projection. If open orders or positions prevent
   that action, unwind them only through separately approved normal economic
   inputs or stop for an explicit owner decision. Direct SQL, projection-only
   repair migrations, implicit clamping, and reuse of a pre-correction backup
   as the rollback boundary are forbidden.
2. After a correction, acquire fresh v3 ownership, prove exact recovery and zero
   reconciliation mismatch, then stop it again. Rescan the authority. A
   mismatch still blocks the cutover.
3. Apply the complete no-overlap protocol above: withdraw traffic; terminate
   the v3 engine and every API, publisher, projector, marketdata, realtime,
   consumer, and cleanup process; release their leases; and prove no
   application-role transaction remains.
4. Record the candidate artifact digest and take a new complete,
   restore-verified backup/PITR boundary containing the corrected configuration,
   fills, receipts, checkpoints, and `schema_migrations`.
5. Inspect blockers before running migration 009. Its column and constraint
   change takes `ACCESS EXCLUSIVE` on `trading.fills`, which conflicts even with
   ordinary reads. Its trigger creation takes `SHARE ROW EXCLUSIVE` on
   `engine.input_receipts`, `engine.duplicate_delivery_receipts`,
   `engine.shard_faults`, and `engine.shard_checkpoints`. It also takes the
   engine-owner advisory lock and uses a two-second lock timeout and
   thirty-second statement timeout.
6. SQLSTATE `55000`, `55P03`, or a PostgreSQL statement timeout raised by a
   migration statement before `COMMIT` is a definite rollback to the v3 tip.
   Preserve evidence. For `55000`, return to step 1; any correction must use the
   normal v3 writer and requires another recovery/reconciliation proof and new
   backup. For a lock or statement timeout, drain the blocker and retry from the
   stopped state. A connection, deadline, failover, or commit-acknowledgment
   error has an unknown outcome; inspect the migration journal and catalog under
   the no-overlap protocol before retrying or selecting a binary.
7. Migration 009 adds the nullable/no-default leverage column without
   backfilling historical fills and fences new business, duplicate, fault, and
   checkpoint writes to runtime decision-hash v4. Never restart a v3 engine
   after it commits.
8. Migration 010 validates the already-enforced positive-value constraint in a
   separate transaction under a `SHARE UPDATE EXCLUSIVE` lock with the same
   time bounds. A PostgreSQL statement timeout before commit leaves the database
   at migration 009 for a clean retry; an unknown commit outcome requires the
   same journal/catalog inspection. Keep every runtime stopped at migration 009.
   The startup verifier rejects a newly opened runtime pool there, but does not
   evict an old process—which is why step 3 is mandatory.
9. Only after migration 010 is proven committed, start the v4 artifact and
   require its exact schema verification. Verify v2/v3 replay, v4 decision and
   state hashes,
   exact immutable fill leverage, restart recovery, duplicate delivery,
   risk-versus-instrument authority, and zero reconciliation mismatch before
   leaving halt.

Operational rollback after migration 009 is a reviewed forward fix with all
runtimes halted. First resolve any unknown commit outcome. An owner-authorized
database rollback means a complete restore of the valid post-correction,
pre-009 boundary followed by the prior binary, fresh recovery, and full
reconciliation. Never edit applied migrations, selectively restore economic
tables, directly update a receipt-backed projection, backfill historical
leverage, or run a v3 writer against the v4 schema.

### Realtime quarantine repair

A permanent error or ten transient/ambiguous failures quarantines the channel
head, keeps readiness false, and blocks later sequence numbers. After fixing
the cause, the repair login calls only:

```sql
SELECT realtime.requeue_publication(
    '<stable-request-uuid>',
    '<channel>',
    '<stable-event-uuid>',
    '<claimed-operator-identity>',
    '<verified-repair-reason>'
);
```

The function records both the authenticated database login and the claimed
operator identity. It atomically appends the immutable repair audit and opens
a fresh bounded retry cycle without changing event identity or sequence.
Retrying the same request and payload returns success; reusing its request ID
with different data fails. Raw publication updates are forbidden. Keep trading
halted if reconciliation detects a sequence gap, allocator mismatch, or
publication identity corruption.

Realtime readiness is deliberately false while any publication is claimed but
not yet durably acknowledged. This includes the bounded interval of a healthy
Centrifugo request: the database cannot safely distinguish that in-flight
claim from an orphan left by a process failure. Liveness remains true; use
readiness for traffic and replacement decisions, not as a restart trigger for
this short delivery window.

## 2. Health semantics

### Liveness

The process event loop and health server are alive. Liveness must not claim the service is safe to receive money-path traffic.

### Readiness

API readiness requires:

- PostgreSQL connectivity;
- schema compatibility;
- command outbox health;
- current engine heartbeat/status for trading routes.

Engine readiness requires:

- exclusive shard ownership;
- PostgreSQL and JetStream connectivity;
- input sequence continuity;
- checkpoint loaded;
- feed synchronized and freshness within policy;
- no invariant or schema failure;
- no unreconciled fatal error.

Marketdata readiness requires synchronized subscriptions/snapshots and no unresolved sequence gap.

## 3. Fail-closed operating modes

- `normal`: policy permits risk-increasing and reducing actions.
- `close_only`: no new/increased exposure; tested close/reduce operations remain available.
- `halt`: trading commands are rejected except explicitly tested recovery/admin actions.

Feed gaps, engine ambiguity, ledger mismatch or sequence corruption automatically remove readiness and may force close-only/halt according to tested policy.

## 4. Required metrics and alerts

### Engine

- last applied input sequence and age;
- stream lag;
- command latency by stage;
- duplicate input count;
- decision/invariant failures;
- checkpoint age;
- shard lease/ownership;
- market freshness and gaps;
- open order/position counts.

### PostgreSQL

- transaction latency and errors;
- pool usage;
- locks/deadlocks/serialization retries;
- outbox oldest unpublished age/count;
- inbox duplicate count;
- table/index growth;
- backup/WAL health.

### NATS

- stream bytes/messages and capacity percentage;
- consumer lag/redelivery/ack pending;
- publish errors and timeouts;
- cluster/replica health.

### Realtime

- publication backlog and failures;
- Centrifugo connectivity;
- client connection/reconnect rates;
- sequence-gap/snapshot-reload rate.

### Economic reconciliation

- unbalanced ledger transactions: always zero;
- balance projection mismatch: always zero;
- order fill quantity mismatch: always zero;
- orphan brackets/protection: always zero;
- duplicate fills/events: always zero.

## 5. Backups and recovery

- PostgreSQL PITR with encrypted backups.
- NATS JetStream replicated storage and documented backup/recovery where required.
- Restore drills at a regular cadence.
- After restore, reconcile ledger, balances, commands, engine checkpoints and stream positions before trading.
- If input history required after a checkpoint is unavailable, do not guess; enter halt and follow manual recovery/reconciliation.

## 6. Engine deployment

- Exactly one replica per shard.
- Recreate/no-overlap strategy.
- Graceful drain: stop intake, finish current transaction, persist checkpoint, acknowledge committed input, release ownership.
- Bounded termination grace with forced-shutdown alert.
- Startup refuses ownership if another valid lease/lock exists.

## 7. Runbooks required before real money

- enter/exit close-only;
- enter/exit halt;
- engine restart;
- NATS outage/recovery;
- PostgreSQL failover/restore;
- Hyperliquid disconnect/gap/resync;
- outbox backlog;
- poison engine input;
- reconciliation mismatch;
- Centrifugo outage;
- migration failure;
- compromised secret/API key;
- cutover and rollback.

### Phase 3 API-key migration rollback boundary

Migration `20260726000700_phase3_user_api_keys.up.sql` is additive but not
binary-backward-compatible with the strict schema verifier. A binary that does
not embed the applied migration rejects the database as schema-ahead; it does
not ignore the new objects.

Before applying the migration:

1. halt API-key mutation traffic;
2. take and restore-verify a complete database backup containing all
   then-existing application state and `schema_migrations`; migration 007
   introduces its API-key audit state;
3. record the backup/PITR boundary and candidate artifact digest;
4. verify the release candidate can apply from the previous released schema.

After the migration is applied, never edit, remove, or down-migrate it. For an
incident, keep writers halted and deploy a reviewed forward code or forward
migration fix verified from a database with migration 007 already applied. If
an owner-authorized full rollback is unavoidable, stop every writer, restore
the complete database to the recorded pre-migration boundary, deploy the prior
binary, and run reconciliation before reopening traffic. A selective schema or
identity-table restore is forbidden because it can split credential, audit,
command, and monetary history. Once post-migration traffic has been accepted,
prefer forward repair; restoring the old boundary discards all later durable
facts and requires the normal disaster-recovery decision.

## 8. Release gates

Continuous and release reconciliation follows `RECONCILIATION.md`.

A production release requires:

- green deterministic, race, integration, recovery and contract suites;
- no unreviewed test-port conflicts;
- immutable migrations tested from previous release;
- load and soak results within SLO;
- reconciliation clean;
- backup restore verified within policy cadence;
- dependency/security scans clean or explicitly accepted;
- signed immutable artifact digest;
- rollback/forward-fix plan.

For the first PostgreSQL 19 production release, these gates additionally
require a PostgreSQL 19 GA build, a production-like major-upgrade rehearsal
from the currently deployed major, a complete pre-upgrade restore drill,
successful immutable migration through the current schema tip, and clean
post-upgrade recovery and reconciliation. PostgreSQL 19 Beta 2 development/CI
qualification does not satisfy this production gate.

The rehearsal must additionally prove:

- invalid pre-v4 risk authority is corrected only by an audited v3 input, with
  fresh-owner recovery and reconciliation before a new rollback backup;
- the corrected rollback backup restores to the v3 tip with the same receipt,
  decision/state hash, checkpoint, projection, and clean reconciliation;
- a configuration blocked by open orders or positions stops the cutover rather
  than falling back to a projection-only repair;
- connection loss after PostgreSQL accepts `COMMIT` for migrations 009 and 010
  is resolved from exact journal/checksum and catalog evidence without assuming
  rollback or applying a migration twice;
- every old API, engine, publisher, projector, marketdata, realtime, consumer,
  and cleanup process, lease, pool, and transaction is gone before the final
  backup, and no application write or external acknowledgment crosses the
  migration window;
- production-scale lock and validation timing is measured after draining
  blockers—including a long old-runtime fill read—on every table touched by
  migrations 009 and 010.

Until those upgrade tests and orchestration proofs exist, production remains
blocked even after PostgreSQL 19 GA.
