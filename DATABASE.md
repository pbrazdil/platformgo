# PostgreSQL Design and Migration Rules

## 1. Authority

PostgreSQL is the authoritative durable store for all business state. NATS and Centrifugo may be rebuilt from PostgreSQL and source feeds; PostgreSQL monetary history may not be rebuilt from realtime delivery.

## 2. Driver and access

- Use `github.com/jackc/pgx/v5` directly.
- No ORM or generic repository framework.
- SQL is restricted to `internal/adapters/postgres/**` and `migrations/**`.
- Queries list explicit columns. `SELECT *` is forbidden.
- Generated SQL is allowed only when the generated source is reviewed and deterministic.

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

## 11. Retention and partitioning

High-volume append-only tables may be time partitioned:

- outbox history;
- inbox history;
- audit events;
- market diagnostics;
- realtime publication history.

Retention must never delete records required for unresolved commands, reconciliation, legal/audit obligations, or recovery. Cleanup is a durable idempotent job with metrics.
The API-key replay cleanup reports every bounded batch and exposes a
least-privilege per-encryption-key live count plus oldest expiry through
`identity.api_key_replay_coverage()`. Missing live decryption keys make
readiness false; zero-count evidence is required before key removal.

## 12. Backup and restore

Production requires:

- continuous WAL archiving/PITR;
- encrypted backups;
- documented RPO/RTO;
- regular restore drills;
- reconciliation after restore;
- backup coverage for migration metadata and engine checkpoints.

A backup that has never been restored is not considered a recovery capability.
