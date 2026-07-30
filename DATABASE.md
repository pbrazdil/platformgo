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
- A market input or API command admitted before JetStream ordering stores
  `marketSequence=0` in its producer or command outbox. This is the only legal
  API command representation; admission rejects nonzero values. Inside the
  serialized engine transaction, a market input resolves the sentinel to its
  assigned shard-stream sequence and every other unresolved input resolves it
  to the shard-wide committed market-state high-watermark. Both resolve before
  business hashing and receipt persistence. Command completion independently
  validates the
  resolved sequence against `max(market.books.stream_sequence)`; a later
  redelivery reuses the original receipt's resolved sequence rather than its
  new delivery position or the latest market state. New command rows carry the
  non-null `ordered` binding marker, and reconciliation derives their expected
  zero command outbox from that enforced producer contract. A nullable marker
  is reserved for pre-migration history: only those legacy rows derive whether
  their immutable outbox was ordered-zero or explicit-nonzero, while every
  other durable field is compared exactly.
- Migration
  `20260728000200_phase3_command_market_sequence_binding.up.sql` takes a
  five-second lock timeout and a strictly longer ten-second statement timeout
  around the engine-owner advisory lock, `SHARE` command lock, and `SHARE ROW
  EXCLUSIVE` receipt lock, so a live old engine fails the cutover with lock
  timeout and legacy command or market admission cannot cross it. The
  migration refuses any pending explicit legacy command with SQLSTATE `55000`.
  The metadata-only command-column/default work briefly upgrades to `ACCESS
  EXCLUSIVE`; installing the compatibility trigger takes `SHARE ROW EXCLUSIVE`
  on `messaging.outbox`. There is no row backfill or table rewrite. Existing
  completed explicit history remains readable through the legacy-null
  classification. Existing rejected market receipts remain immutable, replay
  as their original rejected decisions, advance input order, and do not
  advance the market watermark. Existing accepted receipts with a legacy zero
  or producer-supplied market fence also retain their exact historical bytes
  and hashes, while recovery reconstructs the hidden authoritative watermark
  from the receipt's durable physical stream sequence. The check constraint is
  `NOT VALID` to avoid an unbounded validation scan while still enforcing every
  new value. During the
  one-release compatibility window, an old binary receives the `ordered`
  default and its formerly legal explicit outbox is rejected inside the same
  admission transaction with SQLSTATE `23514`; zero-sentinel admission remains
  compatible. A durable receipt trigger likewise rejects a post-cutover market
  receipt without authoritative book state or with a market/stream fence
  mismatch. The migration advances
  `platformgo.runtime_schema_revision` to its own tip and enforces that value
  on every shard ownership-epoch insert/update, business receipt, duplicate
  receipt, shard fault, and checkpoint insert/update. This per-write fence
  stops an old engine before it can establish writer/readiness authority after
  verifying
  the previous tip before cutover but acquired shard ownership only after the
  migration committed. The migration and history record share one transaction,
  so active
  ownership, lock timeout, statement timeout, preflight refusal, or another
  definite pre-commit failure leaves no partial column, trigger, or history
  state and a later retry is safe. A connection loss or missing acknowledgment
  during/after `COMMIT` has an unknown outcome: keep runtimes stopped and
  compare the exact migration checksum with the column, constraint, functions,
  and enabled triggers before retrying.
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
  The API role has non-grantable `SELECT` only so authoritative balance reads
  can reject missing registry rows and values exceeding the registered scale.

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
- A migration path and its exact bytes become immutable when first merged to a
  protected branch or applied to a shared or persistent database, whichever
  occurs first. Disposable local/test application does not freeze an
  unpublished, unshared candidate. Before freeze it may be edited, renamed,
  reordered, deleted, amended, or squashed; after freeze every correction is a
  new forward migration.
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

### Phase 3 flat-balance currency-scale read boundary

Migration `20260728000100_phase3_flat_balance_currency_scale_read.up.sql`
changes only the ACL catalogs for the existing append-only
`trading.currency_scales` table. It scrubs `PUBLIC`, every direct non-owner
table grant, every column grant, and dependent same-object grant chains, then
grants non-grantable `SELECT` only to `platformgo_engine` and
`platformgo_api`. It performs no heap rewrite, scan, backfill, or economic
mutation.

The compatibility balance query uses a `LEFT JOIN` so a balance without scale
authority is an error rather than an omitted row. Every returned amount is
validated through `domain.NewMoney` at the registered scale before any result
is serialized. Missing scale authority, invalid currency, non-finite money,
or excess scale rejects the whole read.

### Phase 3 currency-scale authority fence

Migration `20260730000200_phase3_currency_scale_authority_fence.up.sql`
closes two preexisting authority paths without editing the frozen migrations
that created or first exposed the registry. It takes the configured shard's
engine-owner advisory lock, then `SHARE ROW EXCLUSIVE` locks
`trading.instruments`, `trading.currency_scales`, and
`engine.input_receipts` in production writer order. Those locks are acquired
before validation or catalog changes and remain held through the atomic
migration-journal commit.

The migration independently reconstructs the exact registry exclusively from
canonical accepted historical `InstrumentChanges` in committed business
receipts. Every current instrument currency pair must also have matching
accepted receipt provenance. Every historical instrument snapshot must use
canonical exact decimal strings within the runtime and PostgreSQL domains, and
the latest accepted snapshot fold plus change count must equal every field and
`version` in the current instrument projection in both directions. Before
trusting either source, the migration
requires the exact frozen trigger inventory on `trading.instruments`,
`trading.currency_scales`, and `engine.input_receipts`, and rejects any
unexpected pre-cutover mutation grant on `trading.instruments` or
`engine.input_receipts`. Revoking a grant or removing a trigger cannot prove
that authority was not forged while it existed. An invalid source,
missing/malformed/extra trigger, noncanonical or out-of-domain exact value,
non-accepted instrument effect, full projection mismatch, conflicting scale,
extra registry row, missing row, or scale mismatch raises SQLSTATE `55000`.
The migration never repairs, deletes, updates, backfills, or otherwise derives
an accepted economic fact from the registry being checked.

New registrations use an exact-origin `AFTER INSERT OR UPDATE` instrument
trigger. A separate `ENABLE ALWAYS BEFORE INSERT` registry trigger permits a
new pair only when the same single scale is already visible in the current
instrument catalog. An existing same-scale registry row takes a bounded fast
path and does not rescan immutable receipt history. The existing
update/delete/truncate guard is also `ENABLE ALWAYS`. All three trigger
functions have exact relation, trigger name, timing, level, operation, and
argument checks. All non-owner function, table, and column privileges are
removed from the registry and both authority sources, including named grants
and dependent grant chains, before restoring only the documented API, engine,
and outbox allowlists. Runtime roles receive no function `EXECUTE`; only API
and engine retain non-grantable registry `SELECT`.

The runtime schema revision advances to
`20260730000200_phase3_currency_scale_authority_fence`. A preverified old
engine therefore cannot resume durable writes after the cutover. This is a
catalog-and-authority migration with no table rewrite, but it is an enforced
no-overlap engine boundary.

### Phase 3 admin fleet-fills ACL boundary

Migration `20260728000300_phase3_admin_fleet_fills_acl.up.sql` changes only
the ACL catalogs for the existing append-only `trading.fills` table. It
removes `PUBLIC`, every direct non-owner table grant, every direct non-owner
column grant, and dependent same-object grant chains—including grants
inherited when the table was created under hostile owner defaults. It then
restores exactly non-grantable `SELECT` to `platformgo_api` and non-grantable
`SELECT, INSERT` to `platformgo_engine`. The migration does not alter the
owner's default-privilege template and performs no heap rewrite, scan,
backfill, or economic mutation.

Privilege changes and the migration journal commit in one transaction under
the migrator's five-second `lock_timeout` and the migration's ten-second
`statement_timeout`. A definite pre-commit timeout leaves the previous ACL and
journal intact for retry. A missing `COMMIT` acknowledgment is an unknown
outcome: keep runtimes stopped and compare the exact filename/checksum with the
complete raw table and column ACL before selecting a binary or retrying.

The internal application read performs one
`SELECT EXISTS (SELECT 1 FROM trading.fills)` through `platformgo_api`. It
returns the exact empty page only when that statement's PostgreSQL snapshot
contains no committed fill. Any committed fill or database error fails closed;
no fill value, total above zero, ordering, filter, cursor, or non-empty DTO is
projected by this boundary.

### Phase 3 admin fleet-orders ACL boundary

Migration `20260728000400_phase3_admin_fleet_orders_acl.up.sql` changes only
the ACL catalogs for the existing `trading.orders` and
`trading.order_intents` tables, in that deterministic relation order. It
removes `PUBLIC`, every direct non-owner table grant, every direct non-owner
column grant, grant options, and dependent same-object grant chains, including
grants inherited from hostile owner defaults. It then restores exactly:

- `platformgo_api`: `SELECT` on `trading.orders`; `SELECT, INSERT` on
  `trading.order_intents`.
- `platformgo_engine`: `SELECT, INSERT, UPDATE` on `trading.orders`; `SELECT`
  on `trading.order_intents`.

All restored grants are non-grantable. The migration does not alter the
owner's default-privilege template, the runtime schema revision, row data, or
relation files. It performs no heap rewrite, scan, backfill, or economic
mutation. Privilege changes and the migration journal commit in one
transaction under the migrator's five-second `lock_timeout` and the
migration's ten-second `statement_timeout`. A timeout on the second relation
therefore rolls back the first relation's ACL delta as well.
Before changing ACLs, deterministic `SHARE` locks fence transactions that
already executed DML under a privilege being revoked. An open
`ROW EXCLUSIVE` writer causes a bounded pre-commit rollback; after that writer
is drained or rolled back, the whole migration is retried.

The internal application reader executes one PostgreSQL statement:

```sql
SELECT
    EXISTS (SELECT 1 FROM trading.orders)
    OR EXISTS (SELECT 1 FROM trading.order_intents)
```

Both predicates share one MVCC snapshot. The exact empty page is returned only
when neither a materialized order nor an immutable admitted intent exists.
Every order or intent, including a rejected or not-yet-materialized intent,
fails closed with a typed unsupported-state error. This boundary reads no
economic value and does not establish non-empty DTO fields, totals above zero,
ordering, filters, cursors, or an external admin route.

### Phase 3 admin fleet-positions ACL boundary

Migration `20260729000100_phase3_admin_fleet_positions_acl.up.sql` is the
forward-only ACL correction for the existing `trading.positions` table. It
removes `PUBLIC`, every direct non-owner table grant, every direct non-owner
column grant, grant options, and dependent same-object grant chains, including
grants inherited from hostile owner defaults. It then restores exactly
non-grantable `SELECT` to `platformgo_api` and non-grantable
`SELECT, INSERT, UPDATE` to `platformgo_engine`.

The migration does not change `trading.fills`, owner default-privilege
templates, runtime schema revision, position or fill rows, or either relation
file. It performs no heap rewrite, scan, or backfill. Privilege changes and the
migration journal commit in one transaction under the migrator's five-second
`lock_timeout` and the migration's ten-second `statement_timeout`. An up-front
`SHARE` lock on `trading.positions` fences a transaction that already executed
position DML under a privilege being revoked. A pre-revocation
`ROW EXCLUSIVE` writer therefore produces a bounded definite pre-commit
rollback; after the writer is drained or rolled back, retry the complete
migration.

A missing `COMMIT` acknowledgment is an unknown outcome. Keep all runtimes
stopped and compare the exact filename/checksum with the complete raw
`trading.positions` table and column ACL before retrying or choosing a binary.
The only valid non-owner ACL is API `SELECT` plus engine
`SELECT, INSERT, UPDATE`, all without grant option. Also prove the canonical
explicit-column position digest and relation filenode, and the fill digest,
filenode, and ACL, match the pre-migration state. A journal, ACL, or relation
state disagreement requires a reviewed forward repair or complete verified
pre-migration restore; never edit frozen migration bytes or migration history.

### Phase 3 admin risk-monitor ACL boundary

Migration `20260729000200_phase3_admin_risk_monitor_acl.up.sql` advances the
immutable migration journal from the 31-file positions tip to the 32-file risk
tip. It takes bounded `SHARE` locks in exact order on `trading.accounts`,
`engine.account_shards`, and `identity.account_provisioning_intents` before
changing any ACL or function catalog. That order fences existing lifecycle DML
without deadlocking a provisioning transaction paused after shard assignment.
A conflicting writer returns SQLSTATE `55P03` under the migrator's five-second
`lock_timeout`; the migration's ten-second `statement_timeout` also bounds
catalog enumeration.

The migration scrubs `PUBLIC`, every explicit non-owner table and column grant,
grant options, and dependent same-object grant chains on those three
relations. It restores exactly these non-grantable privileges:

- `trading.accounts`: API `SELECT`; engine `SELECT, INSERT, UPDATE`.
- `engine.account_shards`: API `SELECT, INSERT`; engine `SELECT`.
- `identity.account_provisioning_intents`: API `SELECT, INSERT`; engine
  `SELECT`.

It also creates the zero-argument
`trading.admin_risk_state_exists()` authority as `LANGUAGE sql`, `STABLE`,
`SECURITY DEFINER`, with `search_path=pg_catalog`. One fully qualified
statement tests only existence in `trading.accounts`, `trading.commands`,
`engine.account_shards`, `ledger.balances`, `ledger.transactions`, and
`ledger.entries`. The migration removes `PUBLIC` and every explicit non-owner
function grant, including dependent grant chains, transfers any pre-existing
same-signature function to the migration owner inside the same transaction,
then grants non-grantable `EXECUTE` only to `platformgo_api`. Raw ledger ACLs
remain unchanged, so the API can evaluate the boolean authority but cannot read
raw ledger transactions or entries. Its pre-existing balance-projection read
remains unchanged.

The predicate reads no `NUMERIC` column, performs no arithmetic or rounding,
and defines no non-empty risk DTO, total, threshold, filter, ordering, cursor,
or external route. It returns false only when the same PostgreSQL snapshot has
no account, command, shard assignment, balance, ledger transaction, or ledger
entry. Any such committed durable state makes the application boundary fail
closed rather than inventing risk values.

This is an ACL/catalog-only migration: it performs no heap rewrite, backfill,
row mutation, runtime-schema revision change, raw-ledger privilege change, or
legacy provisioning-function change. The three relation states and filenodes,
owner default privileges, and legacy function remain unchanged. All effects
and the migration journal commit in one transaction. A definite pre-commit
failure leaves the 31-file schema and prior ACLs intact for a complete retry.
A missing `COMMIT` acknowledgment is an unknown outcome: keep runtimes stopped
and reconcile the exact filename/checksum, 32-file tip, complete table/column
and function ACLs, function definition, preserved relation state, raw ledger
ACLs, defaults, and legacy function before retrying or selecting a binary.

### Phase 3 durable-outbox ACL boundary

Migration `20260729000300_phase3_outbox_acl.up.sql` advances the immutable
journal from the 32-file risk-monitor tip to the 33-file outbox-ACL tip. It
takes `SHARE` on `messaging.outbox` under the migrator's five-second
`lock_timeout` and its own ten-second `statement_timeout`, fencing any API,
engine, or publisher transaction that already performed DML under a privilege
being revoked.

The migration removes `PUBLIC`, every explicit non-owner table and column
grant, grant options, and dependent same-object grant chains. It then restores
only these non-grantable privileges:

- `platformgo_api`: table `SELECT` and column `INSERT` on `message_id`,
  `subject`, `schema_version`, and `payload`;
- `platformgo_engine`: table `SELECT, INSERT`;
- `platformgo_outbox`: table `SELECT` and column `UPDATE` on `attempts`,
  `next_attempt_at`, `claimed_at`, `published_at`, `publish_sequence`, and
  `last_error`.

This is an ACL-catalog-only correction. It does not alter rows, indexes,
constraints, existing command-binding triggers, owner default privileges,
runtime schema revision, producer authority, claim order, publication
identity, retry behavior, or worker polling. A definite pre-commit timeout
leaves the 32-file journal, prior ACL, and outbox state intact for a complete
retry. A missing `COMMIT` acknowledgment is an unknown outcome: keep runtimes
stopped and reconcile the exact filename/checksum, 33-file tip, complete raw
table and column ACL, canonical explicit-column outbox digest, relation
filenode, indexes, constraints, triggers, owner defaults, and neighboring
inbox ACL before retrying or selecting a binary.

### Phase 3 command-admission ACL and truncate boundary

Migration `20260729000400_phase3_command_admission_acl.up.sql` advances the
immutable journal from the 33-file outbox-ACL tip to the 34-file
command-admission-ACL tip. It repairs inherited hostile default privileges on
`trading.commands`, `trading.idempotency_records`, and
`trading.command_replay_responses`, and adds statement-level `BEFORE TRUNCATE`
guards to all three durable authorities.

The no-overlap cutover first acquires the configured shard's engine-owner
advisory lock, then the exclusive command-admission gate, then `SHARE` relation
locks in the fixed order commands, idempotency records, replay responses. This
matches runtime drain and writer ownership: engine shutdown drains admission
before releasing ownership, and no pre-revocation writer can commit after the
ACL journal is published. The migrator's five-second `lock_timeout` is shorter
than the migration's ten-second `statement_timeout`.

The migration removes `PUBLIC`, every explicit non-owner table and column
grant, all grant options, and dependent same-object grant chains. It restores
only non-grantable production privileges:

- `platformgo_api`: table `SELECT, INSERT` on all three relations;
- `platformgo_engine`: table `SELECT` on all three relations, column `UPDATE`
  on `commands(status, result, completed_at)`, and column `UPDATE` on
  `idempotency_records(state, response_status, response_headers,
  response_body)`;
- `platformgo_outbox`: column `SELECT` on the seven immutable command-envelope
  fields it reads.

This is an ACL/trigger-catalog-only correction. It performs no row update,
backfill, heap rewrite, runtime-schema revision change, role-membership change,
or owner-default change. A definite pre-commit timeout leaves the 33-file
journal, data, filenodes, triggers, and prior ACLs intact for a complete retry.
A missing `COMMIT` acknowledgment is an unknown outcome: keep API, engine, and
outbox runtimes stopped and reconcile the exact filename/checksum, 34-file
tip, full raw table/column ACLs, all three truncate guards, explicit-column
digests, relation filenodes, neighboring intent/outbox state, and owner
defaults before retrying or selecting a binary.

### Phase 3 finite fill-leverage constraint boundary

Migration
`20260729000500_phase3_fill_leverage_finite_constraint.up.sql` advances the
immutable journal from the exact 34-file command-admission tip to the 35-file
finite-constraint tip. Under a two-second `lock_timeout` and thirty-second
`statement_timeout`, it first acquires the engine-owner transaction advisory
lock for the configured shard, then takes `ACCESS EXCLUSIVE` on
`trading.fills` and adds `fills_effective_leverage_finite_positive` as `NOT
VALID`. A live engine owner therefore times out before the DDL, and no
legitimate engine writer overlaps the constraint cutover. This is a
catalog-only change: it performs no historical scan, heap rewrite, row write,
backfill, or repair. The existing `fills_effective_leverage_positive`
constraint is not dropped or changed. The new constraint immediately rejects
every new non-`NULL` value unless it is both positive and distinct from numeric
`NaN`, `Infinity`, and `-Infinity`; historical `NULL` remains accepted.

Migration
`20260729000600_phase3_validate_fill_leverage_finite.up.sql` separately scans
`trading.fills` under `SHARE UPDATE EXCLUSIVE`, with the same two-second and
thirty-second bounds, and validates the new constraint without rewriting or
mutating the table. A successful transaction advances the immutable journal
to the exact 36-file validation tip.

A preexisting numeric `NaN` has an explicit stopped-runtime outcome: migration
005 commits at the 35-file tip with the constraint enforced but unvalidated,
then migration 006 fails with SQLSTATE `23514` and rolls back only its own
validation transaction. Keep every runtime stopped. Do not retry validation,
update or delete immutable fill history, introduce a repair, or remove either
constraint. Remain halted for a reviewed forward or owner decision. Only an
explicit owner authorization permits restoring the complete verified pre-005
database boundary, followed by the exact prior artifact, fresh recovery, and
full reconciliation. Never reset or recreate a persistent database or
selectively restore fill history.

A definite lock or statement timeout before `COMMIT` rolls back that migration
transaction. A connection, client-deadline, failover, or missing
`COMMIT`-acknowledgment error is an unknown outcome. Keep runtimes stopped and
classify the exact migration filename and checksum, 34-, 35-, or 36-file tip,
constraint presence and `convalidated` state, and preserved fill digest and
relation filenode before retrying or selecting a binary. The application
readers independently parse every non-`NULL` effective leverage through the
exact ratio domain, require a positive value, and canonicalize it; invalid
durable data fails the complete read closed without changing rows.

### Phase 3 broker-balances ACL boundary

Migration `20260730000100_phase3_broker_balances_acl.up.sql` advances the
immutable journal from the exact 36-file finite-leverage validation tip to the
37-file broker-balances ACL tip. It takes `SHARE` locks before any ACL change,
in production writer order: `identity.user_accounts`,
`identity.account_profiles`, then `ledger.balances`. Account provisioning
writes the two identity relations in that order before balance persistence; a
balance-only writer touches only the final relation. The migrator's five-second
`lock_timeout` is shorter than the migration's ten-second
`statement_timeout`, which bounds a conflicting pre-revocation writer. If the
writer does not finish or drain within five seconds, lock acquisition fails
with SQLSTATE `55P03` and the migration rolls back before any ACL change. If
the production-order writer commits within the bound, the migration acquires
all three locks and proceeds safely.

The migration removes `PUBLIC`, every explicit non-owner table and column
grant, all grant options, and dependent same-object grant chains inherited
from hostile owner defaults. It restores only these non-grantable privileges:

- `identity.user_accounts`: API `SELECT`; engine `SELECT, INSERT`;
- `identity.account_profiles`: API `SELECT`; engine `INSERT`;
- `ledger.balances`: API `SELECT`; engine `SELECT, INSERT, UPDATE`.

The migration changes only ACL catalogs and the migration journal. It does not
change owner default-privilege templates, relation owners, rows, relation
files, schemas, economic state, or the runtime schema revision. A lock,
statement, deadlock, SQL, or journal failure before `COMMIT` rolls back all
three relations' ACL changes and can be retried with the same bytes after the
cause is classified and drained.

A connection loss, failover, client deadline, or missing `COMMIT`
acknowledgment is an unknown outcome. Keep runtimes stopped and compare the
exact filename/checksum, complete raw `pg_class.relacl` and
`pg_attribute.attacl` allowlist, all prior migration checksums, explicit-column
row digests, relation owners and filenodes, and owner defaults. Retry only when
the new journal row is absent and the complete prior state matches. A mixed or
divergent state requires a reviewed forward repair or explicitly authorized
complete verified pre-migration restore; never edit frozen migration bytes.

### Phase 3 broker-account read boundary

The broker account point read requires no migration. The existing immutable
schema and broker-balances ACL tip already grant `platformgo_api` non-grantable
`SELECT` on `identity.user_accounts`, `identity.account_profiles`, and
`trading.accounts`.

One statement anchors authorization on the unique
`identity.user_accounts(account_id)` row with
`broker_subject = Principal.Tenant`. Tenant-constrained nullable joins then
detect, but never authorize through, a missing or inconsistent account profile
or trading projection. No ownership row means the generic unknown-account
result; an owned incomplete or invalid graph fails closed. The statement is a
single MVCC snapshot, returns at most one row, and performs no lock upgrade or
durable write.

### Phase 3 broker-account list index boundary

Migration `20260730000300_phase3_broker_account_list_index.up.sql` advances the
immutable journal from the exact 38-file currency-scale authority-fence tip to
the 39-file broker account-list tip. It takes an explicit `SHARE` lock on
`identity.user_accounts` under a five-second lock timeout, then builds
`user_accounts_broker_list_idx` under a fifteen-second statement timeout:

```text
(broker_subject, user_id, account_id)
WHERE broker_subject IS NOT NULL
```

The index prevents one authenticated tenant list from scanning ownership rows
belonging to every tenant. The unfiltered fixed statement uses the
`broker_subject` prefix. The filtered fixed statement performs a one-time
tenant/user lookup through `users_id_broker_subject_key`, then uses
`user_accounts_pkey`; an absent or foreign filter therefore never scans that
user's ownership range. Both statements use pgx unnamed extended-protocol
execution so PostgreSQL plans for the concrete tenant on every request rather
than reusing a tenant-agnostic generic plan. Lateral account-profile and
trading projections remain primary-key probes. Final sorting by profile login
and account ID processes only the authorized result. The existing
least-privilege API role already has `SELECT` on
`identity.users`, `identity.user_accounts`, `identity.account_profiles`, and
`trading.accounts`; the migration adds no privilege.

`CREATE INDEX` reads the existing relation and writes only the new index and
WAL; it does not rewrite or mutate ownership rows. `SHARE` permits reads but
blocks account-provisioning writes, so operators drain those writers before
the cutover. A lock, statement, cancellation, disk, or SQL failure before
commit rolls back both the index and checksum journal row. The migration adds
no grant and changes no table owner, table filenode, constraint, trigger,
default privilege, or economic fact.

No down migration or production `DROP INDEX` is allowed. After commit, an old
38-file binary fails exact schema verification; operational rollback is a
reviewed code-revert artifact that still embeds migration 39. A missing commit
acknowledgment is classified from the exact filename/checksum, valid and ready
index catalog state, exact index definition/predicate, unchanged ownership
digest/filenode/ACL/defaults, and all prior checksums before any retry or
binary selection.

### Phase 3 broker-funding ACL and read boundary

Migration `20260730000400_phase3_broker_funding_acl.up.sql` advances the
immutable journal from the exact 39-file broker-account-list tip to the
40-file broker-funding provenance and ACL tip. It first acquires the configured
shard's engine-owner advisory lock, then takes bounded `SHARE` locks on
`engine.shard_ownership_epochs`, `trading.instruments`,
`trading.funding_settlements`, `trading.funding_history_projection`, and
`engine.input_receipts` in the production writer's order. An active writer
therefore causes a bounded rollback instead of permitting an old and new
runtime to overlap.

Before DDL or backfill, the migration proves the exact prior trigger catalog
and the complete trusted metadata and bodies of all reused trigger functions.
It rejects missing, disabled, rebound, duplicated, immediate, malformed, or
sidecar triggers and hostile same-OID function replacement with SQLSTATE
`55000`. It then reconstructs each historical funding row from immutable
accepted receipts, including the last same-instrument change within a receipt,
and refuses orphan, malformed, out-of-bounds, or receipt/projection mismatch.

The migration creates append-only
`trading.funding_instrument_provenance`, backfills its exact instrument
revision and price/quantity scales, adds immutable and truncate guards, and
requires both history projection and provenance through genuine deferred
constraint triggers. Engine persistence writes settlement, history, and
provenance in the same transaction. The broker read function uses the
historical provenance; reconciliation compares it bidirectionally and also
counts either orphan direction.

The ACL scrub removes `PUBLIC` and every explicit non-owner table, column, and
function privilege, grant option, and dependent same-object grant chain. It
restores only non-grantable engine `SELECT, INSERT` on the three funding
relations and API `EXECUTE` on the six approved funding functions. API receives
no direct funding-table privilege and no `EXECUTE` on either constraint helper.
The migration advances the runtime revision gate to the 40-file tip, so an old
runtime cannot acquire or refresh a shard ownership epoch after cutover.

The broker reader authorizes `Principal.Tenant` through matching
`identity.user_accounts` and `identity.account_profiles` rows in a materialized
authority CTE. One unnamed custom-plan PostgreSQL statement returns the
authority sentinel, ordered funding window, registered currency scale, and
optional cursorless total from one MVCC snapshot. The funding and count
functions depend on the authority row, so an absent or foreign account cannot
invoke them. Every returned economic value and identifier is buffered and
validated through its historical instrument revision before exposure;
incomplete or mismatched provenance, off-grid quantity or price, non-finite,
non-positive-oracle, invalid-currency, scan, or terminal stream failure rejects
the whole page without rounding or partial output.

A missing commit acknowledgment is an unknown outcome. Keep runtimes stopped
while comparing the exact filename/checksum, all 40 journal rows, raw ACLs,
trusted function and trigger catalogs, preserved authority-row digests,
provenance count/digest, owners, defaults, and runtime revision. Retry only
when the new row is absent and the complete 39-file state matches. Never edit
the frozen funding read-model migration or applied migration history.

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

The broker-echo change is a forward-only, no-overlap sequence. Migration
`20260727000100_phase3_broker_echo_exact_replay.up.sql` is an intermediate,
stopped-runtime tip. It takes `SHARE` on
`identity.idempotency_responses`, with a five-second lock timeout and a
fifteen-second statement timeout, before validating and copying the live
broker-echo subset. The legacy relation, including indexes and TOAST, must be
at most 64 MiB (67,108,864 bytes); the live subset must contain at most 1,000
rows and at most 46,000 bytes of canonical JSON text. Every live row must be
reconstructable as the exact status, logical required headers, and body bytes.
An excess or invalid row aborts the migration atomically. The copy preserves
both legacy timestamps exactly. Those rows were created by the prior Go
application clock for expiry and PostgreSQL for creation, so their finite,
increasing lifetime is not required to equal exactly 24 hours.

Migration 00100 creates `identity.broker_echo_replays`, keyed by
`(scope, idempotency_key_hash)`, plus
`broker_echo_replays_expiry_idx` and the enabled
`broker_echo_replays_guard_mutation` trigger. PostgreSQL
`statement_timestamp()` is the creation and expiry authority and a new claim
retains the response for exactly 24 hours. The raw external idempotency key is
never stored; only its 32-byte SHA-256 digest is persisted. A live exact
response cannot be updated or deleted. Only an expired row can be removed by
the definer purge function or by the locked same-key replacement in the claim
function. At this intermediate tip, purge accepts a limit from 1 through 1,000
and `identity.claim_broker_echo_response(text, bytea, bytea, integer, jsonb,
bytea)` returns exactly `(response_status integer, response_headers jsonb,
response_body bytea)`.

Migration `20260727000200_phase3_broker_echo_capacity_authority.up.sql` is the
forward capacity companion. It again requires every runtime to remain stopped,
takes `SHARE` on `identity.broker_echo_replays`, and uses the same five-second
lock and fifteen-second statement bounds. Before adding catalog authority it
rejects a replay relation larger than 64 MiB, purges up to 1,000 rows already
expired by PostgreSQL time, and rejects any remaining state with:

- an invalid exact response according to
  `identity.valid_broker_echo_response(bytea, integer, jsonb, bytea,
  timestamptz, timestamptz)`;
- more than 1,000 rows in total or more than 100 rows for one `scope`; or
- any `expires_at` later than `statement_timestamp() + interval '24 hours'`.

The immutable singleton `identity.broker_echo_replay_policy` is the only
capacity and cleanup configuration authority. Its one row and equality checks
fix `max_total_rows=1000`, `max_rows_per_principal=100`,
`purge_batch_size=100`, `max_batches_per_cycle=10`,
`cleanup_interval_seconds=60`, `cleanup_cycle_timeout_seconds=10`,
`expired_readiness_slo_seconds=120`, and
`max_retry_after_seconds=86460`. Its cross-column checks require one cleanup
cycle to cover the total capacity, principal capacity not to exceed total
capacity, and maximum retry-after to cover 24 hours plus the cleanup interval.
The `broker_echo_replay_policy_is_immutable` row trigger rejects update and
delete; `broker_echo_replay_policy_rejects_truncate` rejects truncate.

Migration 00200 reduces the purge call limit to 1 through 100. It drops and
recreates, rather than replaces in place, the claim function because the
function result row type changes. The six result columns and their order are
exactly:

```text
outcome text
retry_after_seconds bigint
capacity_scope text
response_status integer
response_headers jsonb
response_body bytea
```

`outcome` is `stored` for a newly stored or exactly replayed response and
`capacity_limited` for a rejected new key. A capacity result names
`principal`, `global`, or `both` and supplies a bounded retry-after. A live
same-key replay or conflict is resolved before capacity admission; an expired
same-key replacement is net-zero capacity.

`identity.broker_echo_replay_coverage()` is the aggregate, least-privilege
catalog surface. At migration 00200 it returns, in order, the eight policy
values above followed by `total_rows bigint`, `live_rows bigint`,
`expired_rows bigint`, `maximum_principal_rows bigint`,
`oldest_live_expires_at text`, `oldest_expired_at text`, and
`oldest_expired_age_seconds bigint`. Migration 00200 is itself an
intermediate, stopped-runtime capacity tip: that coverage result does not
report invalid live rows or live rows whose remaining lifetime exceeds the
capacity retry bound.

Migration `20260727000300_phase3_broker_echo_coverage_integrity.up.sql` is the
forward integrity companion and remains an intermediate, stopped-runtime tip.
With every runtime still stopped, it first takes `SHARE` on the replay table,
then its column/default/constraint DDL requires `ACCESS EXCLUSIVE` and
constraint validation requires `SHARE UPDATE EXCLUSIVE`; every acquisition is
bounded by the same five-second lock and fifteen-second statement timeouts. The
constant boolean default does not rewrite the heap, and the validated scan is
bounded by the 1,000-row authority cap. It adds the immutable row discriminator
`postgres_time_authority`.
Rows already present at the start of 00300 receive `false`, preserving their
original legacy creation and expiry timestamps; the column default is then
changed to `true` for every later PostgreSQL-authoritative claim. In that same
transaction, 00300 installs an enabled insert guard that rejects any new
`false` marker with SQLSTATE `55000`, regardless of caller-supplied timestamps.
It aborts if any row fails response validation, including the valid JSON
object, UUID-v4 `id`, final newline, finite and increasing timestamps, or if
any row expires later than PostgreSQL statement time plus 24 hours. It adds
and validates `broker_echo_replays_have_valid_exact_response`, which
additionally requires `expires_at = created_at + interval '24 hours'` exactly
when `postgres_time_authority` is true, and then drops and recreates coverage
because its result row type changes. The final result order is the eight
policy columns, `total_rows bigint`, `live_rows bigint`,
`invalid_live_rows bigint`, `overlong_live_rows bigint`, `expired_rows bigint`,
`maximum_principal_rows bigint`, `oldest_live_expires_at text`,
`oldest_expired_at text`, and `oldest_expired_age_seconds bigint`. Invalid live
rows include malformed responses and current-authority rows with a non-exact
24-hour lifetime. Startup and readiness fail closed unless both integrity
counts are zero. Legacy rows retain their original bounded expiry and
disappear only through the normal expired-only purge.

Migration `20260727000400_phase3_broker_echo_replay_guards.up.sql` is the only
tip compatible with the current runtime. It takes `ACCESS EXCLUSIVE` up front
under the same timeout bounds and performs no heap rewrite or backfill. Before
catalog work, it verifies the exact language, signature, return and execution
attributes, configuration, source body, function OID wiring, trigger mode, and
enabled state of the 00300 insert fence. The exact trigger definition must have
no `WHEN` predicate, arguments, or transition tables, and the complete
non-internal trigger catalog must contain only the expected immutable
update/delete guard and insert fence. A same-named no-op function, selectively
conditional trigger, or sidecar trigger is a fail-closed divergent tip.
It then rejects the additional anomaly of a false-marked row whose `created_at`
is later than the committed 00300 journal time. It installs the statement
trigger that rejects every `TRUNCATE`;
update/delete immutability, the pre-existing insert fence, and expired-only
bounded purge remain unchanged. A normal data-only import cannot create legacy
exemptions at either committed tip. Older data is restored only into an
isolated database, advanced through the ordered migrations, and reconciled
before promotion.

The claim and purge functions are `SECURITY DEFINER` with
`search_path=pg_catalog` and `lock_timeout=5s`; coverage is
`SECURITY DEFINER` with `search_path=pg_catalog` from 00200 onward. The
validator is an immutable invoker function with `search_path=pg_catalog`.
At each intermediate and final tip, migrations inspect complete raw function
and table ACLs, revoke every explicit non-owner grantee plus `PUBLIC`, and then
grant the exact intended function allowlist. This neutralizes hostile default
privileges for known and unlisted roles.
`platformgo_api` has execute privilege on exactly claim, purge, and coverage,
but not the validator; it has no direct privilege on either replay or policy
table, no select privilege on the legacy replay table, and no execute privilege
on the legacy claim. Other runtime roles have neither table access nor execute
privilege on these broker-echo functions.

No intermediate tip is permission to overlap binaries or guess a runtime from
partial catalog state. The stop, preflight, exact journal/checksum and catalog
classification, final-tip binary selection, and recovery protocol is in
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
New broker-echo exact responses retain for 24 hours from PostgreSQL statement
time. Rows migrated from the prior application-clock contract preserve their
original finite expiry and are never extended or shortened during migration.
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
