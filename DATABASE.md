# PostgreSQL Design and Migration Rules

## 1. Authority

PostgreSQL is the authoritative durable store for all business state. NATS and Centrifugo may be rebuilt from PostgreSQL and source feeds; PostgreSQL monetary history may not be rebuilt from realtime delivery.

## 2. Driver and access

- Use `github.com/jackc/pgx/v5` directly.
- No ORM or generic repository framework.
- SQL is restricted to `internal/adapters/postgres/**` and `migrations/**`.
- Queries list explicit columns. `SELECT *` is forbidden.
- Generated SQL is allowed only when the generated source is reviewed and deterministic.

### Supported PostgreSQL version

The migrator and runtime schema verifier require PostgreSQL 19 or newer. While
PostgreSQL 19 is prerelease software, the exact minimum accepted build is
PostgreSQL 19 Beta 2; development snapshots, Beta 1, older majors, and
noncanonical version strings fail closed. PostgreSQL 19 Beta 2 is qualified
only for development and CI. Production requires PostgreSQL 19 GA and the
major-upgrade, backup-restore, recovery, and reconciliation gates in
`OPERATIONS.md`. The broker-echo replay claim additionally depends on the
PostgreSQL 19 `INSERT ... ON CONFLICT DO SELECT FOR UPDATE` behavior and must
be requalified by the full gate on the released PostgreSQL 19 GA build.

## 3. Logical schemas

```text
identity     users, admins, sessions, API keys
trading      commands, orders, fills, positions, brackets, instruments
ledger       immutable transactions and entries, balance projection
engine       shard lease, input receipts, checkpoints
market       instrument revisions, feed status, snapshots
messaging    outbox and inbox
realtime     publication outbox and channel sequence
ops          settings, controls, audit, reconciliation
```

## 4. Numeric storage

- Economic values use `NUMERIC(38,18)` unless a narrower integer representation is proven and documented.
- SQL `REAL`, `FLOAT`, and `DOUBLE PRECISION` are forbidden for economic data.
- Application code validates domain bounds before persistence.
- Database constraints reject invalid sign, scale, status and quantity relationships where practical.
- Serialization to/from PostgreSQL never passes through floating point.

## 5. Transaction ownership

### API role

May:

- create idempotency records;
- insert commands and command outbox rows;
- read allowed projections.

May not directly mutate:

- balances;
- ledger;
- fills;
- positions;
- order economic state;
- margin/funding/liquidation state.

### Engine role

Owns economic state mutations for its shard.

### Worker roles

Own only their explicit projection, inbox, outbox or operational tables.
`platformgo_realtime` claims and acknowledges committed realtime
publications; it cannot alter publication identity or economic state.
`platformgo_realtime_repair` can only inspect realtime delivery state and call
the audited, idempotent quarantine-requeue function; it cannot update
publication or economic rows directly.

### Migrator role

Owns DDL and is not used by running services.

Use distinct PostgreSQL roles and grants to enforce these boundaries.

## 6. Engine transaction

The engine transaction for an input includes, as applicable:

- duplicate/input receipt check;
- deterministic locks;
- command result;
- orders and positions;
- fills;
- ledger entries and balance projection;
- margin/reservations;
- domain event outbox;
- per-user realtime publication and monotonic channel sequence;
- realtime outbox;
- scheduled jobs;
- engine checkpoint and receipt.

Network calls are outside the transaction.

### Balance projection and decision-hash authority

- The authoritative funded balance is the raw account/currency total. Ledger
  derivation compares only that total before and after an input; changes to
  margin reservation, unrealized PnL, `free`, `used`, or `equity` cannot create
  ledger money.
- When the required market mark exists, the engine completes the exact
  `total`, `used`, `free`, and `equity` projection after applying the state
  transition and before hashing or persistence. Order-only and market-only
  inputs therefore bind their final derived balances.
- A missing mark is represented by SQL `NULL` in `market.books.mark_price`.
  The engine preserves the last durable projection instead of inventing a
  price or zeroing an economic value. Restoring the mark recomputes the exact
  projection and emits no ledger entries unless the funded total changed.
- Decision-hash version 3 introduced complete derived balance projections.
  Valid v2/v3 receipts remain verifiable at their recorded version.
- Current decision-hash version 4 preserves the v3 balance authority and also
  binds every new fill's exact positive execution-time effective leverage into
  its effects hash. The value comes from the unique account/instrument risk
  authority, or the instrument maximum when no explicit risk exists, and is
  persisted as an immutable fill fact. Every new business and
  duplicate-delivery receipt uses v4.
- Recovery replays each immutable business and duplicate-delivery receipt at
  its recorded v2, v3, or v4 decision-hash version. Historical v2/v3 fills may
  have absent/SQL `NULL` leverage; every current v4 fill must have one canonical
  positive value and must match the stored decision and projection exactly.
- `trading.currency_scales` is the append-only durable authority for the one
  scale assigned to each currency code. Recovery seeds the deterministic state
  from the registry before replay, then independently verifies historical
  instrument decisions, decision hashes, state hashes, and the final
  bidirectional registry match. Runtime roles cannot mutate the registry;
  instrument writes register or verify a scale inside the engine transaction.

## 7. Locking and isolation

- One engine writer per shard removes most write contention but does not replace database constraints.
- Default engine transactions may use `READ COMMITTED` with explicit row locks because the single-writer invariant serializes economic mutation.
- Transactions involving state writable by more than one role require a documented strategy and may require `SERIALIZABLE` with bounded retry.
- Multi-row locks are acquired in sorted stable ID order.
- Advisory locks may enforce singleton roles and migrations, but monetary idempotency also uses durable unique constraints.
- Deadlock and serialization retries retain the same command/input ID.

## 8. Required durable records

### Idempotency

Keyed by a stable scope and idempotency key. Stores:

```text
request_hash
command_id
state: in_progress | completed
response_status
response_headers
response_body
created_at
expires_at
```

Same key/different hash is rejected.

### Commands

Stores stable ID, account and account sequence, type, canonical payload, status, result, creation time and completion time.

### Engine input receipts

Unique by shard and input ID; also records stream sequence, decision hash and resulting state hash.

### Duplicate delivery receipts

Unique by shard and later stream sequence. Records a re-published stable input,
the original decision hash, the no-effect delivery decision hash, and resulting
state hash. It never replaces or duplicates the unique business receipt.

### Outbox

Stores stable message ID, subject/topic, schema version, payload, attempts, next attempt and publication acknowledgment metadata.

### Inbox

Unique by consumer and message ID. The inbox insert and consumer side effect commit together.

### Ledger

Immutable transaction and entry tables with constraints ensuring stable IDs and balanced currency legs.

## 9. Migrations

- Forward-only SQL files under `migrations/`.
- Recommended name: `YYYYMMDDHHMMSS_description.up.sql`.
- Applied migration filenames and SHA-256 checksums are stored in the database.
- The migrator takes a global advisory lock.
- Running services verify the expected schema version but never apply DDL.
- Existing migration files are immutable.
- Destructive changes use expand/backfill/contract across releases.
- New code remains compatible with the prior release during rolling components where overlap is permitted.
- The singleton engine uses no-overlap deployment; schema still follows forward-compatible practice.

## 10. Migration testing

CI must test:

1. clean database migration;
2. migration from the previous released schema with representative data;
3. idempotent rerun where intended;
4. constraints and indexes;
5. downgrade rejection rather than destructive down migration;
6. checksum mismatch refusal;
7. query plans for hot outbox, inbox, command and account paths.

### Phase 3 balance-projection migration boundary

Migration `20260726000800_phase3_balance_projection_hash_v3.up.sql` is a
halted, forward-only cutover. It takes bounded locks in writer-compatible
instrument, book, business-receipt, duplicate-receipt order before reading
legacy history. Before any DDL it rejects a non-object decision, malformed
instrument or order collections, invalid currency identities, conflicting
scales, and nonempty pre-v3 order history whose balance projection cannot be
proven complete. Every refusal is atomic and leaves the prior immutable schema
and receipts authoritative.

The migration backfills the monotonic currency registry from both the current
instrument catalog and accepted historical instrument changes, permits
markless books, and fences every new business or duplicate receipt to
decision-hash v3. A prior binary is therefore not compatible after the
migration commits. Applied migration history is never edited: recover with a
reviewed forward fix, or restore the complete pre-migration database while all
writers remain stopped.

### Phase 3 frozen-effective-leverage migration boundary

Migration `20260726000900_phase3_fill_effective_leverage_hash_v4.up.sql` is the
halted v4 cutover. Before DDL it acquires the configured shard's engine-owner
advisory lock and, under a two-second lock timeout and thirty-second statement
timeout, rejects any existing account/instrument risk leverage above the
instrument maximum with SQLSTATE `55000`. The refusal is atomic and blocks the
cutover. Correct that authority only through a normal audited v3 input whose
receipt, decision/state hashes, checkpoint, projection, fresh-owner recovery,
and reconciliation all agree. Then stop every runtime, rescan, and record a new
restore-verified rollback boundary before retrying. Direct SQL,
projection-only repair, implicit clamping, and a pre-correction backup are not
valid remediation or rollback boundaries.

Migration 009 adds nullable, no-default `numeric(38,18)`
`trading.fills.effective_leverage` without rewriting or backfilling historical
fill pages. Its positive-value check is installed `NOT VALID`, so it protects
new writes immediately while preserving valid v2/v3 `NULL` history. Runtime
version fences cover new business receipts, duplicate receipts, shard faults,
and checkpoints, preventing a v3 engine from writing after the cutover.

Migration `20260726001000_phase3_validate_fill_effective_leverage.up.sql`
validates that constraint in a separate transaction under PostgreSQL's
`SHARE UPDATE EXCLUSIVE` lock with the same two-second and thirty-second bounds.
Ordinary DML is lock-compatible, but the no-overlap cutover keeps every runtime
stopped. A PostgreSQL statement timeout before `COMMIT` leaves the constraint
enforced but unvalidated and migration 010 unapplied for a clean retry. A
connection, client-deadline, failover, or commit-acknowledgment error has an
unknown outcome: inspect the exact migration journal/checksum and catalog state
before retrying. The strict schema verifier rejects a newly started runtime at
migration 009; it does not evict an already-open pool. The current immutable
schema tip is migration 010. Recover only with a reviewed forward fix or a
complete valid post-correction/pre-009 restore while every runtime remains
stopped.

### Phase 3 broker-echo exact-replay migration boundary

Migration `20260727000100_phase3_broker_echo_exact_replay.up.sql` is a
forward-only, no-overlap API cutover. It creates the dedicated
`identity.broker_echo_replays` authority and claims one `(principal, key hash)`
with PostgreSQL 19's `INSERT ... ON CONFLICT DO SELECT FOR UPDATE`. The row
stores the exact HTTP status, logical required headers, and body bytes; replay
does not re-render them. The external idempotency key is SHA-256 hashed before
persistence, and the raw key is never stored in this table.

PostgreSQL `statement_timestamp()` is the only creation and expiry authority.
Each claim has a 24-hour lifetime. Rows are immutable while live; only expired
rows may be deleted, through
`identity.purge_expired_broker_echo_replays(integer)` or the targeted
expired-key replacement inside the claim function. Unrelated cleanup is not in
the claim's correctness path. Runtime roles receive no direct table access.
`platformgo_api` may execute only the definer claim and bounded purge functions,
and loses access to the legacy broker-echo claim and legacy replay table.

The migration locks `identity.idempotency_responses` in
`SHARE` mode before validating and copying its live broker-echo
subset. Its fixed live-row and response-byte bounds are part of the reviewed
migration. A fixed total-relation-size ceiling also bounds the physical work of
the legacy scan. Before cutover, operations records all measured values and
proves they are within those exact bounds; an excess or a response that cannot
be reconstructed byte-for-byte aborts atomically. Old and new API binaries
must never overlap this migration. The stop, backup, journal/catalog
classification, schema verification, start, and rollback protocol is in
`OPERATIONS.md`.

## 11. Retention and partitioning

High-volume append-only tables may be time partitioned:

- outbox history;
- inbox history;
- audit events;
- market diagnostics;
- realtime publication history.

Retention must never delete records required for unresolved commands, reconciliation, legal/audit obligations, or recovery. Cleanup is a durable idempotent job with metrics.
The periodic API-key replay cleanup reports every bounded batch and exposes a
least-privilege per-encryption-key live count plus oldest expiry through
`identity.api_key_replay_coverage()`. Missing live decryption keys make
readiness false; zero-count evidence is required before key removal.
Broker-echo exact responses retain for 24 hours from PostgreSQL statement time.
Cleanup deletes expired rows only, in bounded batches, and reports its deleted
count; it must not update or delete a live replay.

## 12. Backup and restore

Production requires:

- continuous WAL archiving/PITR;
- encrypted backups;
- documented RPO/RTO;
- regular restore drills;
- reconciliation after restore;
- backup coverage for migration metadata and engine checkpoints.

A backup that has never been restored is not considered a recovery capability.
