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

### Phase 3 flat-balance currency-scale read upgrade

Migration `20260728000100_phase3_flat_balance_currency_scale_read.up.sql` is an
ACL-only no-overlap cutover. Stop and drain every old API process before
applying it. The migration scrubs the existing currency-scale table ACL and
grants read-only access to the candidate API role; it does not rewrite or scan
economic data. Verify raw table and column ACLs after commit: the only
non-owner grants are non-grantable `SELECT` for `platformgo_engine` and
`platformgo_api`.

Deploy in this order: migration first, then the candidate API binary. A prior
binary started cold after the migration rejects the schema as newer; an
already-running prior API process remains query-compatible but must not be
left overlapping the cutover. Rollback therefore uses a schema-compatible
binary embedding the new migration but restoring the prior query behavior, or
a reviewed forward fix. Full database rollback is allowed only from the
complete verified pre-migration restore boundary while every runtime remains
stopped. Never remove the journal row, edit the migration, or selectively
restore ACL catalogs.

If the migration returns `lock_timeout`, the transaction has rolled back:
verify that its filename is absent from `engine.schema_migrations` and the
prior ACL remains intact before retrying. For an unknown commit acknowledgment,
inspect the exact filename/checksum and the raw table/column ACL together. A
journal/catalog disagreement remains stopped for a forward repair.

### Phase 3 admin fleet-fills ACL upgrade

Migration `20260728000300_phase3_admin_fleet_fills_acl.up.sql` is an ACL-only
no-overlap cutover for `trading.fills`. Stop and drain old API and engine
processes before applying it, and keep them stopped until the exact journal and
ACL state is classified. The migration performs no fill or ledger data change
and does not rewrite the relation. It removes every explicit non-owner table
and column grant and dependent grant chain from the existing table, then
atomically restores only non-grantable API `SELECT` and engine
`SELECT, INSERT`.

Run the migration under the migrator's five-second `lock_timeout`; its SQL also
sets a ten-second `statement_timeout`. Ordinary engine-like `ROW EXCLUSIVE`
DML is compatible with the ACL change. An `ACCESS EXCLUSIVE` blocker produces
a definite pre-commit rollback rather than an unbounded wait. Before retrying,
prove the migration filename is absent, the prior raw ACL is unchanged, and
fill data and relation identity are unchanged.

After commit, verify the exact filename/checksum and raw table plus column ACL:
the only explicit non-owner rows are API `SELECT` and engine `SELECT, INSERT`,
all without grant option. Also prove API mutation is denied and both intended
roles can read. A prior binary started cold rejects the newer migration tip.
For a connection loss or missing `COMMIT` acknowledgment, keep every runtime
stopped and inspect the journal/checksum and complete ACL together. A mismatch
requires a reviewed forward repair or complete verified pre-migration restore;
never edit the journal, edit frozen migration bytes, or restore ACL catalogs
selectively.

### Phase 3 admin fleet-orders ACL upgrade

Migration `20260728000400_phase3_admin_fleet_orders_acl.up.sql` is an ACL-only
no-overlap cutover for `trading.orders` and `trading.order_intents`. Stop and
drain old API and engine processes before applying it. Keep them stopped until
the exact journal and complete ACL state of both relations are classified.
The migration performs no row-data change, heap rewrite, backfill, or runtime
schema-revision bump.

Apply under the migrator's five-second `lock_timeout`; the SQL sets a
ten-second `statement_timeout`. It scrubs the two relations in deterministic
order. Up-front `SHARE` locks intentionally conflict with `ROW EXCLUSIVE`
writers so that a statement authorized before revocation cannot commit after
the ACL cutover. A pre-revocation writer or an `ACCESS EXCLUSIVE` blocker on
the second relation produces a definite
pre-commit rollback of both relations' ACL changes and the migration journal.
Before retrying, prove the filename is absent, the complete raw table and
column ACL of both relations is unchanged, and both data digests and relation
filenodes are unchanged. Drain or roll back the writer before retrying.

After commit, verify the exact filename/checksum and raw table plus column ACL.
The only explicit non-owner privileges are API `SELECT` on orders, API
`SELECT, INSERT` on order intents, engine `SELECT, INSERT, UPDATE` on orders,
and engine `SELECT` on order intents, all without grant option. Also execute
role-boundary probes: API cannot mutate orders; engine cannot mutate order
intents; neither runtime role can `DELETE` or `TRUNCATE` either relation. A
prior binary started cold must reject the newer migration tip.

A connection loss or missing `COMMIT` acknowledgment is an unknown outcome,
not authorization to retry blindly. Keep all runtimes stopped and inspect the
exact filename/checksum together with the complete raw ACL of both relations.
A journal/catalog mismatch requires a reviewed forward repair or complete
verified pre-migration restore. Never delete the journal row, edit frozen
migration bytes, reset a persistent database, or restore ACL catalogs
selectively. Before its first application, a candidate migration may be
changed only while every database that has received it is explicitly
disposable, local/test-only, unpublished, and unshared. Once protected or
shared, its path and bytes are immutable and every correction is forward-only.

### Phase 3 admin fleet-positions ACL upgrade

Migration `20260729000100_phase3_admin_fleet_positions_acl.up.sql` is the
forward-only ACL-only no-overlap cutover for `trading.positions`. Stop and
drain old API and engine processes before applying it. Keep them stopped until
the exact journal/checksum, complete position ACL, position relation state, and
unchanged fill state are classified. The migration performs no row-data
change, heap rewrite, scan, backfill, runtime schema-revision bump, or
`trading.fills` ACL change.

Apply under the migrator's five-second `lock_timeout`; the SQL sets a
ten-second `statement_timeout` and takes `SHARE` on `trading.positions` before
revocation. The lock intentionally conflicts with a pre-revocation
`ROW EXCLUSIVE` writer so a position update authorized before the cutover
cannot commit afterward. A `55P03` failure is a definite pre-commit rollback.
Before retrying, prove the filename is absent, the complete raw position table
and column ACL is unchanged, the canonical explicit-column position digest
and filenode are unchanged, and the fill digest, filenode, and raw ACL are
unchanged. Drain or roll back the writer, then retry the whole migration.

After commit, verify the exact filename/checksum and complete raw position
table plus column ACL. The only explicit non-owner privileges are
non-grantable API `SELECT` and engine `SELECT, INSERT, UPDATE`. Prove API
`INSERT`, `UPDATE`, `DELETE`, and `TRUNCATE` remain denied; engine `DELETE` and
`TRUNCATE` remain denied; and one API-role statement can read both positions
and fills. A prior binary started cold must reject the newer migration tip.

A connection loss or missing `COMMIT` acknowledgment is an unknown outcome,
not proof of rollback and not authorization to retry. Keep every runtime
stopped and reconnect with the migrator identity. Compare the exact
filename/checksum, complete raw position ACL, canonical position digest and
filenode, and unchanged fill digest, filenode, and ACL. A journal, ACL, or
relation-state disagreement remains halted for a reviewed forward repair or a
complete verified pre-migration restore. Never delete the journal row, edit
frozen migration bytes, reset a persistent database, or selectively restore
ACL catalogs.

### Phase 3 admin risk-monitor ACL upgrade

Migration `20260729000200_phase3_admin_risk_monitor_acl.up.sql` is a
forward-only, ACL/catalog-only no-overlap cutover from the exact 31-file
positions tip to the 32-file risk tip. Stop and drain old API and engine
processes before applying it. Keep them stopped until the exact
journal/checksum, three lifecycle-relation ACLs and states, narrow risk
function, unchanged raw ledger ACLs, owner defaults, and legacy provisioning
function are classified.

Apply under the migrator's five-second `lock_timeout`; the SQL sets a
ten-second `statement_timeout` and takes `SHARE` in exact order on
`trading.accounts`, `engine.account_shards`, and
`identity.account_provisioning_intents` before any ACL or function DDL. A
transaction paused after shard assignment therefore waits on the shard lock
without a lock-order inversion. A writer on the first or last relation, or
that paused admission, produces bounded SQLSTATE `55P03`, not `40P01`. This is
a definite pre-commit rollback: prove the migration filename is absent, all
three prior raw ACLs and canonical row digests/filenodes are unchanged, then
drain or roll back the blocker and retry the whole migration.

After commit, verify migration count 32 and this exact tip/checksum. The only
explicit non-owner table privileges must be:

- accounts: API `SELECT`; engine `SELECT, INSERT, UPDATE`;
- shards: API `SELECT, INSERT`; engine `SELECT`;
- provisioning intents: API `SELECT, INSERT`; engine `SELECT`.

All grants are non-grantable. Verify the only
`trading.admin_risk_state_exists` overload has no arguments, returns boolean,
is SQL/STABLE/SECURITY DEFINER, has exactly `search_path=pg_catalog`, is owned
by the migration owner—including after an unexpected same-signature
predecessor is atomically transferred—and contains the six fully qualified
existence predicates. Its only non-owner privilege is non-grantable API
`EXECUTE`.
`PUBLIC`, hostile/default-derived grantees, inheriting logins, and dependent
grant chains must receive SQLSTATE `42501`. The API role must still receive
`42501` for direct reads of raw ledger transactions or entries. Engine
provisioning privileges must remain usable, while API execution of the legacy
`identity.provision_broker_account` function remains denied.

The function is only a committed-state presence gate. It reads no economic
number and performs no arithmetic or rounding; it does not establish risk
amounts, thresholds, totals, filters, ordering, cursors, pagination, or an
external route. Market/instrument-only state remains empty. Any committed
account, command, shard, balance, ledger transaction, or ledger entry makes
the reader fail closed until separately specified non-empty risk behavior
exists.

A connection loss or missing `COMMIT` acknowledgment is an unknown outcome,
not proof of rollback and not authorization to retry. Keep every runtime
stopped and reconnect with the migrator identity. Compare the exact
filename/checksum and 32-file tip with all table/column ACLs, function
definition and ACL, preserved relation digests/filenodes, raw ledger ACLs,
owner defaults, and the legacy function. Any journal/catalog/state
disagreement requires a reviewed forward repair or complete verified
pre-migration restore. Never delete the journal row, edit frozen migration
bytes, reset a persistent database, or selectively restore ACL catalogs.

### Phase 3 durable-outbox ACL upgrade

Migration `20260729000300_phase3_outbox_acl.up.sql` is a forward-only,
ACL-only no-overlap cutover from the exact 32-file risk-monitor tip to the
33-file outbox-ACL tip. Stop and drain API admission, the engine, and the
outbox publisher before applying it. Keep every runtime stopped until the
exact journal/checksum, complete outbox table and column ACL, outbox relation
state, existing command-binding triggers, owner defaults, and neighboring
inbox ACL are classified.

Apply under the migrator's five-second `lock_timeout`; the SQL sets a
ten-second `statement_timeout` and takes `SHARE` on `messaging.outbox` before
revocation. That lock intentionally conflicts with a pre-revocation
`ROW EXCLUSIVE` writer. SQLSTATE `55P03` is a definite pre-commit rollback:
prove the new filename is absent and the prior ACL, canonical explicit-column
row digest, relation filenode, indexes, constraints, triggers, defaults, and
inbox ACL are unchanged. Drain or roll back the writer, then retry the whole
migration.

After commit, verify migration count 33 and the exact filename/checksum. The
only explicit non-owner privileges on `messaging.outbox` are:

- API table `SELECT` plus column `INSERT` on `message_id`, `subject`,
  `schema_version`, and `payload`;
- engine table `SELECT, INSERT`;
- outbox table `SELECT` plus column `UPDATE` on `attempts`,
  `next_attempt_at`, `claimed_at`, `published_at`, `publish_sequence`, and
  `last_error`.

Every grant is non-grantable. Prove all unlisted roles, `PUBLIC`, dependent
grantees, and unintended operations receive SQLSTATE `42501`; also prove each
intended role can still perform only its production operation. This migration
does not add a notification trigger, change the current 100ms worker poll, or
change producer, claim, ordering, retry, acknowledgment, or publication
semantics.

A connection loss or missing `COMMIT` acknowledgment is an unknown outcome.
Keep every runtime stopped and compare the exact filename/checksum and 33-file
tip with the complete raw ACL and preserved relation/catalog evidence above.
A mixed state requires a reviewed forward repair or a complete verified
pre-migration restore. Never delete the journal row, edit frozen migration
bytes, reset a persistent database, or selectively restore ACL catalogs.

### Phase 3 command-admission ACL and truncate upgrade

Migration `20260729000400_phase3_command_admission_acl.up.sql` is a
forward-only, ACL/trigger-only no-overlap cutover from the exact 33-file
outbox-ACL tip to the 34-file command-admission-ACL tip. Stop and drain API
admission and the engine before applying it; keep the outbox worker stopped
until the complete command ACL has been verified.

The migration runs under the migrator's five-second `lock_timeout` and its own
ten-second `statement_timeout`. It acquires the configured shard's engine-owner
advisory lock, then the exclusive command-admission gate, then `SHARE` on
`trading.commands`, `trading.idempotency_records`, and
`trading.command_replay_responses` in that order. SQLSTATE `55P03` is a
definite pre-commit rollback: prove the new journal row is absent and all three
relations retain their prior ACLs, triggers, explicit-column digests, and
filenodes. Drain or roll back the blocker, then retry the whole migration.

After commit, verify migration count 34 and the exact filename/checksum. Verify
all three enabled `BEFORE TRUNCATE FOR EACH STATEMENT` guards, owner
`TRUNCATE ... CASCADE` rejection with SQLSTATE `55000`, and the exact
non-grantable runtime allowlist documented in `DATABASE.md`. Prove `PUBLIC`,
unexpected roles, and delegated grant chains have no table or column
privileges, while API admission, engine completion, and outbox command reads
still succeed. Confirm rows, filenodes, neighboring order intents/outbox rows,
owner defaults, and role memberships are unchanged.

A connection loss or missing `COMMIT` acknowledgment is an unknown outcome,
not proof of rollback. Keep every runtime stopped and reconcile the exact
filename/checksum, 34-file tip, all raw ACLs and grant options, the three
truncate trigger definitions, relation digests/filenodes, neighboring
admission state, and owner defaults. A mixed state requires a reviewed forward
repair or complete verified pre-migration restore. Never edit the frozen
migration, delete its journal row, reset a persistent database, or selectively
restore ACL catalogs.

### Phase 3 finite fill-leverage constraint upgrade

Migrations
`20260729000500_phase3_fill_leverage_finite_constraint.up.sql` and
`20260729000600_phase3_validate_fill_leverage_finite.up.sql` form a
forward-only, stopped-runtime sequence from the exact 34-file
command-admission tip:

1. Stop and drain every API, engine, publisher, projector, marketdata,
   realtime, consumer, and cleanup runtime. Prove no application-role
   transaction remains. Record the exact 34-file journal and checksums, fill
   digest and relation filenode, and take a complete restore-verified pre-005
   database boundary.
2. Apply migration 005 under its two-second `lock_timeout` and thirty-second
   `statement_timeout`. It first acquires the engine-owner transaction advisory
   lock for the configured shard, so a live owner fails the cutover within the
   bound. Only after that fence does it take `ACCESS EXCLUSIVE` on
   `trading.fills`, which conflicts with reads and writes. Adding the `NOT
   VALID` constraint performs no historical scan, heap rewrite, row write,
   backfill, or repair.
3. Verify the exact 35-file tip and checksum, the present but unvalidated
   `fills_effective_leverage_finite_positive` constraint, the unchanged
   existing positive constraint, and unchanged fill digest and relation
   filenode. Keep all runtimes stopped at this intermediate tip.
4. Apply migration 006 with the same bounds. `VALIDATE CONSTRAINT` scans
   `trading.fills` under `SHARE UPDATE EXCLUSIVE` and performs no rewrite or row
   mutation.
5. Verify the exact 36-file tip and checksum, both constraints validated, and
   the unchanged fill digest and relation filenode. Only then start the exact
   candidate artifact and prove valid finite leverage remains canonical while
   corrupt non-finite leverage fails the whole read closed without exposing a
   partial page or mutating history.

If preexisting numeric `NaN` exists, migration 005 commits and migration 006
fails with SQLSTATE `23514`. The database remains at the exact 35-file tip with
the new constraint enforced but unvalidated and the `NaN` row unchanged. Keep
every runtime stopped. Do not retry migration 006, directly update or delete
the immutable fill, add a repair migration, or remove a constraint. Remain
halted for a reviewed forward or owner decision. Only an explicit owner
authorization permits restoring the complete verified pre-005 database
boundary, followed by the exact prior artifact, fresh recovery, and full
reconciliation. Never reset or recreate a persistent database or selectively
restore fill history.

SQLSTATE `55P03` or a statement timeout definitely raised before `COMMIT`
rolls back only the active migration; prove whether the journal remains at 34
or 35 before a clean retry. A connection loss, client deadline, failover, or
missing `COMMIT` acknowledgment is an unknown outcome. Keep every runtime
stopped and reconcile the exact filename/checksum, 34-, 35-, or 36-file journal
tip, constraint presence and validation state, fill digest, and relation
filenode before retrying or selecting a binary. Never infer rollback from the
client error, delete a journal row, edit an applied migration, reset a
persistent database, or selectively restore fill history.

### Phase 3 broker-balances ACL upgrade

Migration `20260730000100_phase3_broker_balances_acl.up.sql` is a
forward-only catalog correction from the exact 36-file finite-leverage tip.
Before applying it, drain engine balance/account-provisioning writers and API
account provisioning, record all 36 filenames/checksums, explicit-column
digests plus owners/filenodes for `identity.user_accounts`,
`identity.account_profiles`, and `ledger.balances`, complete raw table/column
ACLs, and owner default privileges.

The migration takes bounded `SHARE` locks on those three relations in that
exact production writer order before the first ACL change. A pre-revocation
writer that remains active beyond the migrator's five-second `lock_timeout`
causes SQLSTATE `55P03`; prove the journal, all three ACLs, rows, owners,
filenodes, and defaults remain at the complete prior state, drain or roll back
the writer, and retry the same bytes. A production-order writer that commits
within the bound may allow the migration to continue successfully; classify
that result through the normal exact journal/checksum, ACL, and preservation
verification rather than expecting a timeout. The migration's ten-second
`statement_timeout` also bounds catalog enumeration. Read-only traffic is
lock-compatible, but it does not authorize activating the broker-balances
route.

After commit, verify the exact 37-file tip and checksum and the complete raw
non-grantable allowlist documented in `DATABASE.md`. Prove there are no
`PUBLIC`, column, grant-option, unexpected-role, or dependent-chain privileges;
all 36 prior checksums and every preserved relation/default snapshot must
remain unchanged. Existing engine/API binaries remain ACL-compatible because
their legitimate privileges are restored and the runtime schema revision is
unchanged. The broker-balances route remains inactive until its separate
runtime and frozen-contract candidate is accepted.

A connection loss, failover, client deadline, or missing `COMMIT`
acknowledgment is an unknown outcome. Keep affected runtimes stopped. Classify
committed only when the exact journal row/checksum and complete final raw ACL
state agree; classify not committed only when the row is absent and the
complete prior ACL/data/owner/filenode/default snapshot agrees. Any mixed state
requires a reviewed forward repair or explicitly authorized complete verified
pre-migration restore. Never blindly retry, edit frozen bytes, delete a journal
row, or reset a persistent database.

### Phase 3 broker-funding ACL upgrade

Migration `20260730000400_phase3_broker_funding_acl.up.sql` is a forward-only
provenance, runtime-fence, and ACL cutover from the exact 39-file
broker-account-list tip. Stop API activation and drain the engine writer.
Record all 39 filenames/checksums, the configured shard ownership epoch,
funding/instrument/receipt digests, relation metadata, exact trigger catalogs,
the eight reused trigger-function definitions and metadata, raw ACLs, and owner
defaults.

The migration acquires the configured shard's engine advisory ownership key
before locking ownership epochs, instruments, settlements, history, and
receipts in production-compatible order. SQLSTATE `55P03` or `57014` is a
definite rollback only after proving the 40th journal row is absent and every
recorded authority/catalog value is unchanged. Drain the blocker and retry the
same bytes. SQLSTATE `55000` is a fail-closed authority or reconstruction
conflict; do not repair it by editing migration history or bypassing a trigger.

After commit, verify the exact 40-file tip and prior checksums; exact
settlement/history/provenance counts and provenance digest; the expected
immutable, truncate, deferred-constraint, ownership-revision, receipt, fault,
and checkpoint triggers; trusted function definitions; runtime revision; and
this complete non-grantable allowlist:

- engine `SELECT, INSERT` on settlements, history, and instrument provenance;
- API `EXECUTE` on the client and broker account readers, symbol reader,
  account and symbol counters, and account-position funding aggregate;
- no API direct table or column privilege;
- no runtime `EXECUTE` on either funding constraint helper;
- no `PUBLIC`, unexpected-role, grant-option, or dependent-chain privilege.

Only the runtime built for revision
`20260730000400_phase3_broker_funding_acl` may reacquire ownership after those
checks; the old runtime must fail with SQLSTATE `55000`. Then activate the
broker funding route. A valid request executes one custom-plan PostgreSQL
statement; absent or foreign authority must not execute the funding reader or
counter. Missing provenance, historical off-grid values, other corruption,
cancellation, and restart remain whole-response failures without identifiers,
cursors, totals, or valid prefixes.

A lost connection or missing `COMMIT` acknowledgment is an unknown outcome.
Keep runtimes stopped and classify the exact journal/checksum, runtime gate,
authority/provenance digests, trigger/function catalogs, ACLs, owners, and
defaults. Retry only when the journal row is absent and the complete prior state
matches. Never edit frozen bytes, delete journal history, or reset a persistent
database.

### Phase 3 command market-sequence binding upgrade

Migration
`20260728000200_phase3_command_market_sequence_binding.up.sql` is a
no-overlap engine/API cutover. Stop and drain the engine and API command
writers first. The migration then proves that no engine owner still holds the
configured shard advisory lock, takes `SHARE` on `trading.commands` and `SHARE
ROW EXCLUSIVE` on `engine.input_receipts`, and runs under a five-second lock
timeout plus ten-second statement timeout. A live old engine or table writer
therefore rolls the transaction back rather than pausing and resuming across
the cutover. A pending legacy command whose immutable API outbox already has a
nonzero market fence returns SQLSTATE `55000`; drain it to a terminal response
under the previous artifact before retrying.

The command-column/default change is metadata-only but briefly requires
`ACCESS EXCLUSIVE`; trigger installation takes `SHARE ROW EXCLUSIVE` on
`messaging.outbox`. Verify
that the nullable `market_sequence_binding` column, its `ordered` default, the
`NOT VALID` value constraint, both command/outbox guards, and the market
receipt guard exist and are enabled. There is no command backfill: null means
legacy history. Existing rejected market receipts are preserved and replay as
rejections without advancing market state. Existing accepted receipts with a
legacy zero or producer-supplied fence retain their bytes and hashes; recovery
reconstructs only the hidden watermark from their durable physical stream
sequence. New and previous command binaries can admit only a zero-sentinel
outbox; an explicit outbox fails within its admission transaction with
SQLSTATE `23514`. A post-cutover market receipt must contain a non-empty book
change and matching stream/market metadata. This boundary introduced runtime
revision `20260728000200_phase3_command_market_sequence_binding` on every
shard ownership-epoch insert/update, business receipt, duplicate receipt, shard
fault, and checkpoint insert/update. Later no-overlap migrations advance that
exact value; the current required revision is recorded by the latest such
boundary below. Each advance fences an engine that passed schema verification
before the upgrade but attempted shard ownership only after commit; its epoch
write fails with SQLSTATE `55000` before it can publish engine readiness.

On `55P03`, `57014`, `55000`, or another definite pre-commit error, verify that
the migration filename, column, functions, and triggers are all absent before
retrying at the prior exact tip. For an unknown commit result, keep every
runtime stopped and classify the exact checksum plus catalog state. After a
confirmed commit, an old engine cannot restart because its migration set is
schema-behind, and a previously verified old process cannot establish ownership
because its session revision is stale. Rollback is a schema-compatible forward
artifact or a complete verified pre-migration restore, never migration-history
editing or selective receipt removal.

### Phase 3 broker-echo exact-replay upgrade

Migrations `20260727000100_phase3_broker_echo_exact_replay.up.sql`,
`20260727000200_phase3_broker_echo_capacity_authority.up.sql`,
`20260727000300_phase3_broker_echo_coverage_integrity.up.sql`, and
`20260727000400_phase3_broker_echo_replay_guards.up.sql` form one no-overlap
operational cutover even though each migration and checksum journal record
commits in its own transaction:

1. Withdraw broker traffic. Stop and drain every API process, including every
   claim caller and replay-cleanup owner. Prove from PostgreSQL and the process
   supervisor that their pools, sessions, transactions, waits, and cleanup
   schedules have ended. Keep them stopped through all migrations and catalog
   classification; no old and new binary, cleanup process, session, or
   transaction may overlap.
2. Record the old and candidate artifact digests. Take a complete,
   restore-verified backup/PITR boundary containing both replay authorities and
   `engine.schema_migrations`.
3. Before 00100, measure and retain:
   `pg_total_relation_size('identity.idempotency_responses')`, the count of
   live scopes prefixed by `broker-echo` plus byte `0x1f`, and the sum of their
   `octet_length(response_body::text)`. The relation must be at most
   67,108,864 bytes, the live subset at most 1,000 rows and 46,000 bytes, and
   every live row must pass the migration's exact scope, 32-byte request-hash,
   status-200, one-id UUID-v4 JSON, finite-time, and increasing-time
   reconstruction predicate. Any excess or unreconstructable response blocks
   the cutover.
4. Apply 00100. It takes `SHARE` on
   `identity.idempotency_responses` with `lock_timeout=5s` and
   `statement_timeout=15s`, validates and copies only live broker-echo rows,
   and installs the dedicated exact-response catalog atomically. Do not start a
   binary at this intermediate tip.
5. Before 00200, record
   `pg_total_relation_size('identity.broker_echo_replays')`, total rows,
   rows grouped by scope, exact-response validity, and maximum remaining
   lifetime using PostgreSQL `statement_timestamp()`. The relation must be at
   most 67,108,864 bytes. After rows already expired by PostgreSQL time are
   removed, every response must pass the exact validator predicate, total rows
   must be at most 1,000, each scope at most 100, and no expiry may exceed
   statement time plus 24 hours. Preserve the query output as release
   evidence; do not delete or shorten a live response to make preflight pass.
   A legitimate legacy expiry derived from an application clock ahead of
   PostgreSQL can temporarily exceed this bound. Keep traffic stopped and wait
   until PostgreSQL statement time reaches the accepted boundary, then rerun
   the unchanged preflight; never rewrite that row or its expiry.
6. Apply 00200 while all API and cleanup processes remain stopped. It takes
   `SHARE` on the dedicated replay table with the same 5-second/15-second
   bounds, performs one transitional expired-only purge of up to 1,000 rows,
   validates the remaining authority, and installs the immutable capacity
   policy, validator, reduced purge, six-column claim, and aggregate coverage
   surface atomically.
7. Verify the immutable singleton row exactly:
   `max_total_rows=1000`, `max_rows_per_principal=100`,
   `purge_batch_size=100`, `max_batches_per_cycle=10`,
   `cleanup_interval_seconds=60`, `cleanup_cycle_timeout_seconds=10`,
   `expired_readiness_slo_seconds=120`, and
   `max_retry_after_seconds=86460`. Verify the equality and cross-column
   checks, the enabled update/delete and truncate guard triggers, and rejection
   of all three mutation forms.
8. Verify the claim result with `pg_get_function_result`: its columns are
   exactly `(outcome text, retry_after_seconds bigint, capacity_scope text,
   response_status integer, response_headers jsonb, response_body bytea)` in
   that order. This must be the dropped-and-recreated six-column result, not
   00100's three-column result. Verify purge accepts only 1 through 100.
9. Verify catalog security. Claim and purge are `SECURITY DEFINER`,
   `search_path=pg_catalog`, `lock_timeout=5s`; coverage is
   `SECURITY DEFINER`, `search_path=pg_catalog`; the validator is immutable with
   `search_path=pg_catalog`. Inspect complete `aclexplode(proacl)` and
   `aclexplode(relacl)` output. Every explicit non-owner grantee and `PUBLIC`
   must have been revoked from each broker-echo function and both new tables,
   including unlisted roles introduced through hostile default privileges.
   `platformgo_api` can execute exactly claim, purge, and
   `identity.broker_echo_replay_coverage()` and cannot directly read or mutate
   either replay or policy table. Other runtime roles have no broker-echo table
   or function access. The legacy claim and API select on
   `identity.idempotency_responses` remain revoked.
10. Verify coverage's exact intermediate migration-00200 result order: the
    eight policy columns, then `total_rows bigint`, `live_rows bigint`,
    `expired_rows bigint`, `maximum_principal_rows bigint`,
    `oldest_live_expires_at text`, `oldest_expired_at text`, and
    `oldest_expired_age_seconds bigint`. Do not start the current runtime at
    this intermediate tip.
11. Before 00300, prove every row passes the response predicate, including a
    valid JSON object, UUID-v4 `id`, final newline, and finite increasing
    timestamps. Do not require exact 24-hour lifetime from these rows: they
    include legacy expiry selected by the prior Go application clock. Repeat
    the maximum remaining-lifetime preflight using PostgreSQL statement time;
    no expiry may exceed statement time plus 24 hours. Apply 00300 while every
    API and cleanup process remains stopped. It first obtains `SHARE`; the
    constant-default column, default, and constraint DDL then require
    `ACCESS EXCLUSIVE`, and validation requires `SHARE UPDATE EXCLUSIVE`, all
    under the 5-second/15-second bounds. The constant default does not rewrite
    the capped heap. It adds `postgres_time_authority=false` to all existing
    rows, changes the default to `true` for future claims, and atomically
    installs the enabled insert fence that rejects any later `false` marker
    regardless of its caller-supplied timestamps. It rejects any invalid
    response or overlong expiry, adds and validates the conditional
    `broker_echo_replays_have_valid_exact_response`, then atomically replaces
    the aggregate coverage function. The validator remains the immutable
    finite/increasing response validator installed by 00200. A reader holding
    `ACCESS SHARE` can block the DDL escalation; a timeout must leave the exact
    00200 journal, data, catalog, and ACL state for a clean retry.
12. Verify coverage's 00300 result order exactly: the eight policy columns,
    `total_rows bigint`, `live_rows bigint`, `invalid_live_rows bigint`,
    `overlong_live_rows bigint`, `expired_rows bigint`,
    `maximum_principal_rows bigint`, `oldest_live_expires_at text`,
    `oldest_expired_at text`, and `oldest_expired_age_seconds bigint`. It
    remains `STABLE SECURITY DEFINER`, with `search_path=pg_catalog`, no
    `PUBLIC` execute grant, and an execute grant to `platformgo_api`. Verify
    `broker_echo_replays_have_valid_exact_response` is validated, uses the
    immutable response validator, and requires exact 24-hour lifetime only for
    rows with `postgres_time_authority=true`. Verify the discriminator is
    `NOT NULL`, every migrated row is `false`, and the column default is
    `true`. Verify backdated, equal-cutover, and future-dated owner inserts with
    `postgres_time_authority=false` all fail with SQLSTATE `55000`, while real
    migrated rows remain byte- and timestamp-identical. Do not start a runtime
    at this intermediate tip.
13. Apply 00400 while every runtime and non-migrator database session remains
    stopped. It takes `ACCESS EXCLUSIVE` before catalog work, performs no
    rewrite/backfill, verifies the 00300 insert fence's exact language,
    signature, return and execution attributes, configuration, source body,
    owner, function OID wiring, exact trigger definition, mode, and enabled
    state. The trigger must have no `WHEN` predicate, arguments, or transition
    tables, and the complete non-internal trigger catalog must contain only the
    expected immutable update/delete guard and insert fence. A same-named no-op
    function, selectively conditional trigger, or alphabetically later sidecar
    trigger must fail with SQLSTATE `55000` without changing the journal, data,
    or divergent catalog. It also fails if a false-marked row has the
    additional anomaly of a `created_at` later than the committed 00300 journal
    time. Verify the enabled statement-level truncate guard rejects owner
    `TRUNCATE` with SQLSTATE `55000` and preserves every row. Recheck complete
    raw ACLs, including an unlisted hostile default grantee.
14. Run the strict schema verifier against 00400. Exercise a fresh API-role
    claim and prove it stores `postgres_time_authority=true` with exact
    PostgreSQL-derived 24-hour lifetime. Prove bounded purge still removes only
    expired rows, coverage integrity counts are zero, and exact replay remains
    unchanged before selecting the current artifact.

The exact journal identity is the filename plus SHA-256 of the immutable file
bytes. For this sequence it is:

```text
20260727000100_phase3_broker_echo_exact_replay.up.sql
700a5581f30f32e9d3846d6c9c0d26227a96287d988ac839a3af806e364b493e
20260727000200_phase3_broker_echo_capacity_authority.up.sql
3da4abb62234d5ef2e1f0364390b90e9aba0f4523f5ef9e52f3b8b428ab1f0c7
20260727000300_phase3_broker_echo_coverage_integrity.up.sql
3335758c901a0667896ed6ed304c8c99338f0d749f4a67e24e9286f3231c78d4
20260727000400_phase3_broker_echo_replay_guards.up.sql
1ba1a4495213baee0773f677e6d36eef41f25f639501a355fc23e7e63957952e
```

Compare those values to `filename` and `encode(checksum, 'hex')` in
`engine.schema_migrations`; a filename alone is not evidence. Resolve a
connection loss, client deadline, failover, or missing `COMMIT`
acknowledgment using both the journal and the complete expected catalog:

- Before 00100: neither journal row nor any dedicated replay catalog exists.
  Only the pre-cutover binary matches, and all processes remain stopped before
  retrying 00100.
- Exact 00100 intermediate: the 00100 journal/checksum exists, 00200 is absent;
  the dedicated table, primary key, expiry index, live-row guard, definer
  claim/purge and ACL revocations exist; claim has the three-column result;
  policy, validator, and coverage do not exist. Do not run the capacity-aware
  binary. Keep runtimes stopped and advance with 00200.
- Exact 00200 intermediate capacity catalog: the 00100 and 00200
  journal/checksums exist and 00300 is absent; the policy row, all checks and
  guards, validator, reduced purge, six-column claim, 15-column coverage,
  function settings, table ACLs, and execute grants match every item above.
  Do not run the current integrity-aware binary. Keep runtimes stopped and
  advance with 00300.
- Exact 00300 intermediate integrity catalog: the first three
  journal/checksums exist and 00400 is absent; every 00200
  object still matches, `postgres_time_authority` is non-null with default
  true, the validated conditional exact-response constraint exists, the
  post-cutover insert fence exists and is enabled, and coverage has exactly the
  final 17-column result, security settings, and grants above. The validator
  remains the 00200 finite/increasing response predicate. Keep runtimes stopped
  and advance with 00400.
- Exact 00400 final catalog: all four journal/checksums exist; the exact 00300
  data, catalog, and post-cutover insert fence remain, the statement-level
  truncate guard exists and is enabled, full raw ACLs contain only owner plus
  the exact API function allowlist, and no post-00300 false-marker anomaly
  exists. Only this tip is compatible with the current artifact.
- Partial or divergent: any journal row with a wrong checksum, expected
  journal row with missing or mismatched catalog, catalog without its exact
  journal row, mixed three-/six-column claim state, a 00200/00300 coverage
  row-type mismatch, missing 00300 insert fence, missing 00400 truncate guard,
  a post-cutover false marker, extra grants, mutable or altered policy, or any
  otherwise unclassifiable state is not a retryable known tip. Keep all
  runtimes stopped and recover with a reviewed forward fix or a complete valid
  restore; never guess, edit the journal, selectively restore objects, or run
  any binary. Restore older data only into an isolated database, apply the
  ordered migrations, verify and reconcile there, then promote the complete
  database.

At the selected runtime tip, verify same-key replay returns exact stored status,
logical required headers, and body bytes. Verify different-request conflict,
concurrent duplicate claim, lost HTTP acknowledgment, 24-hour PostgreSQL-time
expiry, capacity limits and retry-after, bounded expired-only cleanup, coverage
readiness, and cleanup-owner shutdown before restoring traffic.

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

#### Currency-scale authority-fence forward correction

Migration `20260730000200_phase3_currency_scale_authority_fence.up.sql` is the
mandatory forward correction for the frozen registry ACL/function boundary.
Apply it with traffic withdrawn and the engine drained. The migration's
engine-owner advisory lock and transaction-held relation fences make a complete
all-session drain defense-in-depth rather than a correctness dependency:

- an old registry insert that already holds `ROW EXCLUSIVE` completes before
  the migration can validate, so its committed effect is observed and causes
  SQLSTATE `55000`;
- an old definer invocation that has not acquired the registry write lock
  cannot insert until the migration commits, after which the committed
  `ENABLE ALWAYS` registry guard rejects any pair without exact durable
  instrument authority.

The migration uses a five-second lock timeout and a thirty-second statement
timeout. Any failure rolls back every function, trigger, ACL,
runtime-revision, and journal change. A definite `55P03` or statement timeout
is retryable only after the writer or administrative blocker is identified
and drained and the prior 37-file state is proven intact. Every semantic
`55000` is non-retryable: unexpected source mutation ACL,
missing/malformed/extra authority trigger, impossible instrument-effect
cardinality, noncanonical or out-of-domain exact instrument value, full
current-instrument/latest-accepted-history projection mismatch, malformed or
non-accepted history, conflicting authority, and any missing, extra, or
mismatched registry row all require preserved evidence plus owner-reviewed
proof and forward recovery, or a complete restore. Merely revoking a hostile
grant or dropping/recreating a trigger does not classify facts written while
that path existed and must never authorize retry. Never mutate registry,
instrument, or receipt history in place.

If the migration `COMMIT` acknowledgment is lost, keep all runtimes and new
connection admission halted. Reconcile the exact filename and checksum, the
38-file journal tip, raw table/column/function ACLs, function owner/security
and `search_path`, exact trigger identities/timing/`ENABLE ALWAYS` state,
runtime-revision body, registry row digest, relation owner and filenode, and
bidirectional equality against historical accepted `InstrumentChanges`, plus
full latest-snapshot and projection-version equality for every current
instrument. Exact committed evidence selects the matching new binary; exact
absent journal and catalog deltas permit retry only when no semantic `55000`
evidence is present. Mixed or unproven evidence requires forensic owner review
or complete restore.

After a proven commit, start only the binary carrying runtime revision
`20260730000200_phase3_currency_scale_authority_fence`. Require exact schema
verification, fresh shard ownership, recovery, decision/state/checkpoint hash
verification, and zero instrument/registry reconciliation mismatches before
restoring traffic. Database rollback is a complete owner-authorized restore of
the verified pre-migration boundary followed by the prior binary; down
migration, selective restore, and frozen-migration edits are forbidden.

After the migration commits, operational rollback is a reviewed forward fix
with trading halted. If an owner authorizes full rollback, stop every writer
and restore the complete database to the recorded pre-migration boundary
before deploying the prior binary; then run full reconciliation. Never
selectively restore tables, edit an applied migration, or run an old binary
against the v3 schema. Any facts accepted after the boundary are lost by a
full restore and require the normal disaster-recovery decision.

#### Broker account-list index upgrade

Migration `20260730000300_phase3_broker_account_list_index.up.sql` is the
forward-only access-path correction from the exact 38-file currency-scale
authority-fence tip. Before applying it, withdraw broker account-provisioning
traffic, drain API and engine provisioning writers, and record:

- all 38 migration filenames and checksums;
- the explicit-column `identity.user_accounts` digest, owner, filenode, raw
  table/column ACLs, and owner default privileges;
- free disk and WAL headroom plus the measured populated index-build time.

The migration takes explicit `SHARE` on `identity.user_accounts`. Reads remain
compatible, but `INSERT`, `UPDATE`, and `DELETE` wait. The migrator's
five-second `lock_timeout` and the migration's fifteen-second
`statement_timeout` make a busy or unexpectedly large build fail closed. On a
definite pre-commit timeout or SQL error, prove the journal remains at 38, the
index is absent, and every recorded table property is unchanged; drain the
identified blocker and retry the identical bytes. Do not lengthen the bound,
run manual DDL, or switch to `CREATE INDEX CONCURRENTLY`: the current migrator
requires one transaction to commit the index and checksum journal atomically.

Do not activate `GET /broker/v1/accounts` during a mixed 38/39-file serving
overlap. An already-running 38-file process does not continuously re-run schema
verification and still returns `404` for that route. Keep the route withdrawn
until every instance receiving broker-list traffic is the verified 39-file
artifact, or route that path exclusively to a separately verified 39-file
pool. A newly started 38-file artifact fails schema-ahead verification and
must not receive traffic.

After commit, require the exact 39-file tip and checksum. In `pg_index`,
`user_accounts_broker_list_idx` must be both ready and valid, and
`pg_get_indexdef` plus the partial predicate must exactly match
`(broker_subject, user_id, account_id) WHERE broker_subject IS NOT NULL`.
Verify the ownership digest, table filenode, owner, ACLs, defaults, and all
prior checksums are unchanged. The broker-list store deliberately uses
unnamed extended-protocol execution so PostgreSQL makes a custom plan for the
concrete authenticated tenant on every request. With normal sequential-scan
settings and `plan_cache_mode = force_custom_plan`, run the exact statements
with `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`: prove the fixed filtered
statement uses a one-time `users_id_broker_subject_key` lookup and executes
zero ownership-scan loops for an absent or foreign user; prove the unfiltered
statement uses `user_accounts_broker_list_idx` to avoid foreign tenant rows
and projection joins use their account-ID indexes.

A connection loss, failover, deadline, or missing `COMMIT` acknowledgment is an
unknown outcome. Keep provisioning writers and the new route stopped until the
exact journal and index catalog either prove a complete commit or prove the
complete prior state. A mixed or invalid index state requires a reviewed
forward repair. Never delete the journal row, edit frozen bytes, or drop the
index in production.

There is no down migration. Because the 38-file binary rejects a schema-ahead
database, rollback after commit uses a reviewed code-revert artifact that still
contains migration 39; the additive index remains. Restore a complete
pre-migration database only under explicit owner disaster-recovery direction.

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
