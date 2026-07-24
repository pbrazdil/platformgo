# Architecture

## 1. Architectural objective

The platform must turn an ordered stream of commands, market events, timers, and configuration changes into exact, auditable monetary state transitions.

The central equation is:

```text
(initial state, ordered inputs, config revisions, instrument revisions, code version)
    -> deterministic decisions, events, ledger entries, and next state
```

PostgreSQL owns authoritative business state. JetStream establishes durable delivery and the total live input order for each engine shard. Centrifugo delivers realtime updates to clients but is never authoritative.

## 2. Core principles

1. **Single writer per shard.** Economic state has one serialized mutation path.
2. **Pure decision core.** Domain decisions do not perform I/O or read implicit time/randomness.
3. **Exact decimal arithmetic.** No floating-point values in economic logic or storage.
4. **At-least-once delivery, exactly-once business effect.** Duplicate transport is expected and neutralized transactionally.
5. **PostgreSQL transaction is the commit point.** A decision and all of its durable consequences commit together.
6. **Input order is explicit.** A JetStream sequence defines live ordering within a shard.
7. **Fail closed.** Missing market data, sequence gaps, unknown schemas, inconsistent state, or unavailable authority stop risk-increasing execution.
8. **Compatibility at the edge, freedom inside.** External behavior is preserved; internal architecture and schema are not copied.

## 3. Logical components

```text
                           ┌───────────────────────┐
Clients / Partners ───────▶│ API edge              │
REST / gRPC                │ auth, shape, contract │
                           └───────────┬───────────┘
                                       │ PostgreSQL transaction
                                       ▼
                           ┌───────────────────────┐
                           │ Commands + idempotency│
                           │ + command outbox      │
                           └───────────┬───────────┘
                                       │ publish with stable message ID
                                       ▼
Hyperliquid WS ─▶ Market ─────────▶ JetStream ENGINE_INPUTS_<shard>
                 adapter              command | market | timer | config
                                             │ total stream order
                                             ▼
                                   ┌───────────────────────┐
                                   │ Engine shard          │
                                   │ one serial event loop │
                                   └───────────┬───────────┘
                                               │ one PostgreSQL transaction
                      ┌────────────────────────┼─────────────────────────┐
                      ▼                        ▼                         ▼
                Ledger/state             Command result            Outbox/events
                      │                        │                         │
                      └────────────────────────┴─────────────┬──────────┘
                                                           commit
                                                             │
                                                             ▼
                                                JetStream DOMAIN_EVENTS
                                                             │
                      ┌───────────────────────┬───────────────┴──────────────┐
                      ▼                       ▼                              ▼
                  projectors             schedulers                  realtime worker
                      │                       │                              │
                      ▼                       ▼                              ▼
                PostgreSQL reads      engine timer inputs              Centrifugo
                                                                            │
                                                                            ▼
                                                                          clients
```

## 4. Deployable roles

The implementation may share binaries but runs independent roles:

| Role | Responsibility | Scaling rule |
|---|---|---|
| `migrate` | Apply forward PostgreSQL migrations | one-shot, exclusive lock |
| `api` | REST/gRPC compatibility edge and reads | horizontal |
| `marketdata` | Hyperliquid connection, normalization, sequencing | one active per venue feed group |
| `engine` | Consume one shard input stream and mutate economic state | exactly one active per shard |
| `outbox` | Publish committed PostgreSQL outbox records to JetStream | active/standby or partitioned |
| `projector` | Consume domain events and update non-authoritative read projections | horizontal by subject/key |
| `scheduler` | Create idempotent timer/job inputs | one active per schedule partition |
| `realtime` | Publish committed realtime outbox entries to Centrifugo | horizontal, idempotent |
| `doctor` | Connectivity, schema, configuration and invariant diagnostics | on demand |

To support drop-in deployment, compatibility binaries/commands may retain the existing names and role syntax, including an `app` entrypoint and a `nautilus` compatibility entrypoint for the Go engine. The name does not imply Nautilus internals.

## 5. Deterministic engine

### 5.1 Input envelope

All engine inputs use a versioned envelope:

```go
type InputEnvelope struct {
    InputID              ID
    SchemaVersion        uint32
    ShardID              ShardID
    Kind                 InputKind
    SourceID             string
    SourceSequence       uint64
    StreamSequence       uint64
    LogicalTime          time.Time
    ConfigurationVersion uint64
    InstrumentVersion    uint64
    Payload              CanonicalPayload
}
```

`StreamSequence` comes from the shard’s physical
`ENGINE_INPUTS_<shard>` JetStream stream metadata in live execution. Tests
provide it explicitly. Multiple shards must not share one physical input stream
because its global sequence would create false gaps within each shard.

`LogicalTime` is the time used by business logic. Receipt and processing timestamps may exist for telemetry but cannot alter decisions.

### 5.2 Decision interface

The core is conceptually:

```go
type DecisionEngine interface {
    Apply(State, InputEnvelope) (Decision, error)
}

type Decision struct {
    CommandResult  *CommandResult
    StateChanges   []StateChange
    LedgerEntries  []LedgerEntry
    DomainEvents   []DomainEvent
    RealtimeEvents []RealtimeEvent
    ScheduledJobs  []ScheduledJob
    DecisionHash   Hash
    NextStateHash  Hash
}
```

The result depends only on explicit inputs and state. I/O adapters persist and publish it; they do not decide economics.

### 5.3 State ownership

- One goroutine owns each shard’s in-memory mutable engine state.
- Input is applied serially.
- Auxiliary goroutines may decode, fetch, or publish but return immutable messages to the owner.
- Domain state is never mutated from an HTTP handler, NATS callback, projector, or scheduler.

### 5.4 Canonicalization and hashes

Canonical hashes are used for replay and audit:

- objects have stable field ordering;
- maps are converted to sorted key/value sequences;
- decimals use canonical plain notation;
- timestamps use UTC RFC3339 nanoseconds or integer epoch nanoseconds as specified;
- IDs use canonical lowercase text;
- absent and null are distinct where the contract distinguishes them.

A hash mismatch after replay is a correctness incident.

## 6. Command path

### 6.1 API acceptance

The API performs:

- authentication and authorization;
- request shape and compatibility validation;
- request-size/rate checks;
- idempotency-key processing;
- durable command creation.

It must not independently reimplement monetary admission rules.

Within one PostgreSQL transaction, the API:

1. claims or reads the idempotency record;
2. rejects key reuse with a different canonical request hash;
3. creates the command with a stable command ID and account sequence;
4. creates a command-outbox entry;
5. commits.

After commit, a fast publish attempt may reduce latency, while the durable outbox remains the repair path. Both use the same message ID.

The API may wait for an authoritative engine result where the compatibility contract requires a synchronous verdict. Timeout does not create a new command; the request is resolved by idempotency/status lookup.

### 6.2 Engine processing

For every engine input, including market and timer inputs, the engine follows the same receipt/checkpoint discipline. For an input that causes economic effects, those effects and the checkpoint are atomic. For a market-only input, the correctness-first implementation persists the updated market state, input receipt and checkpoint before acknowledgment.

For each input, the engine:

1. parses the stable envelope identity needed for receipt lookup;
2. begins a PostgreSQL transaction;
3. checks `engine.input_receipts` by input ID and stream sequence;
4. returns an exact committed duplicate using the receipt's recorded fingerprint version, even if its schema is no longer accepted for new inputs;
5. rejects conflicting committed identity, then validates schema and sequence for a genuinely new input;
6. loads and locks required state in deterministic order;
7. applies the pure decision;
8. inserts immutable fills and ledger entries;
9. updates market state, orders, positions, balances, margin state and any command result;
10. inserts domain/realtime outbox entries;
11. inserts the input receipt and checkpoint;
12. commits;
13. acknowledges the JetStream input synchronously.

If the process crashes before commit, redelivery repeats the decision against unchanged durable state. If it crashes after commit and before acknowledgment, the input receipt turns redelivery into a no-op that returns the prior result.

Batching consecutive market inputs is a later optimization requiring an ADR. Any batch design must persist one unambiguous checkpoint and all triggered economic effects before acknowledging the covered sequence range.

## 7. Market-data path

### 7.1 Hyperliquid adapter

The adapter:

- owns WebSocket lifecycle and reconnect policy;
- captures raw frames for protocol diagnostics when configured;
- parses exact decimal strings;
- normalizes instrument IDs and event types;
- assigns a monotonically increasing source sequence inside a connection epoch;
- emits explicit epoch, snapshot, gap, reconnect, heartbeat and data events;
- never invents missing prices;
- publishes versioned normalized events to the appropriate engine input stream.

Frame receipt order is assigned before parallel parsing or fanout can reorder data.

### 7.2 Freshness and gaps

Each instrument market state records:

- connection epoch;
- source sequence;
- engine stream sequence;
- exchange timestamp where present;
- logical receipt time;
- last complete snapshot;
- freshness/gap status.

A gap, failed resynchronization, unknown instrument revision, or stale price makes the instrument unavailable for risk-increasing commands. Close/reduce behavior follows explicit tested policy; it never silently substitutes zero or an unrelated price.

### 7.3 Input ordering

Commands and market events for one shard share the same JetStream stream. JetStream assigns their total live order. The engine uses the last market state it has applied when it reaches a command.

This deliberately makes ordering observable and replayable. Wall-clock timestamps alone never establish cross-source order.

## 8. PostgreSQL data model

PostgreSQL contains these logical schemas:

| Schema | Purpose |
|---|---|
| `identity` | users, admins, sessions, API keys and auth metadata |
| `trading` | commands, orders, fills, positions, brackets and instrument configuration |
| `ledger` | immutable transactions/entries and balance projections |
| `engine` | input receipts, shard leases, checkpoints and state metadata |
| `market` | instrument revisions, feed status and durable snapshots needed for recovery/audit |
| `messaging` | outbox and consumer inbox records |
| `realtime` | durable publication outbox and per-channel sequence |
| `ops` | runtime settings, audit, reconciliation and operational controls |

See `DATABASE.md` for transaction and migration rules.

## 9. Domain events and projections

The engine writes domain events to the PostgreSQL outbox in the same transaction as the economic state change.

The outbox publisher sends them to a versioned JetStream stream. Consumers:

1. receive a message;
2. begin a PostgreSQL transaction;
3. insert an inbox record keyed by consumer and message ID;
4. return success without reapplying if the inbox row already exists;
5. apply the projection/effect;
6. commit;
7. acknowledge synchronously.

Read projections may be rebuilt. Ledger and authoritative engine state may not be reconstructed from ephemeral realtime messages.

## 10. Realtime with Centrifugo

Centrifugo owns client connections and fanout. The application owns content, authorization, ordering and recovery semantics.

Realtime messages include at minimum:

```text
eventId
schemaVersion
channelSequence
aggregateId
aggregateVersion
eventType
occurredAt
payload
```

The baseline no-Redis design may use Centrifugo’s NATS broker for distributed live fanout. That mode is treated as at-most-once and without authoritative history. Therefore:

- PostgreSQL remains the state source;
- clients load an initial snapshot;
- clients apply only events after the snapshot version;
- duplicates are ignored by event ID/sequence;
- gaps or reconnects that cannot prove continuity cause a snapshot reload.

Centrifugo PostgreSQL broker/history may be evaluated only through a separate ADR and production load testing. Monetary correctness may never depend on its availability.

## 11. Scheduling and time

Scheduled economic actions are not in-process timers that mutate state directly.

The scheduler creates a stable job/input ID and publishes a timer input into the engine shard stream. The engine evaluates it at the supplied logical due time. Duplicate scheduler runs are neutralized by the stable input ID and database receipt.

Funding boundaries, expirations, lockouts, and retention policies are tested with a manual clock.

## 12. Sharding and future scale

Initial deployment uses one engine shard.

A future shard owns a disjoint set of accounts and has:

- one input stream;
- one active engine writer;
- one checkpoint sequence;
- a complete market event feed for instruments relevant to its accounts.

Cross-shard monetary operations are forbidden until an ADR defines a durable protocol. Scaling by running two writers against the same account set is never allowed.

A second market integration may be introduced only after Hyperliquid parity and production gates are satisfied. The market abstraction is limited to concrete seams proven by Hyperliquid tests.

## 13. Failure behavior

| Failure | Required behavior |
|---|---|
| API loses reply after commit | retry with same idempotency key returns prior command/result |
| Outbox publish fails | row remains unpublished and is retried |
| Duplicate publish | stable message ID + engine/inbox receipt prevents duplicate effect |
| Engine crashes before DB commit | JetStream redelivers and decision re-runs against unchanged state |
| Engine crashes after DB commit before ack | receipt makes redelivery a no-op |
| PostgreSQL unavailable | money path stops; no in-memory success response |
| NATS unavailable | new engine inputs stop; accepted commands remain durable in PostgreSQL outbox |
| Hyperliquid feed gap | affected instruments fail closed for risk increase |
| Unknown input schema | shard halts and readiness fails |
| Centrifugo unavailable | economic commit succeeds; realtime outbox retries; clients can read snapshots |
| Duplicate realtime event | client ignores by sequence/event ID |
| Migration failure | deployment stops before new code starts |

## 14. Target repository layout

```text
cmd/
  app/                  compatibility API/worker/migrate/doctor entrypoint
  nautilus/             compatibility name for the Go engine entrypoint

internal/
  domain/               exact pure domain types and rules
  engine/               deterministic state transition and shard loop
  application/          commands, queries, ports and orchestration
  edge/http/            REST compatibility
  edge/grpc/            gRPC compatibility
  adapters/postgres/    persistence and migrations
  adapters/nats/        JetStream/Core NATS
  adapters/hyperliquid/ protocol and market data
  adapters/centrifugo/  realtime delivery
  scheduler/            durable timer creation
  observability/        logs, metrics and traces

testkit/                deterministic clocks, IDs, markets, faults and fixtures
tests/
  integration/
  contract/
  live/
ports/                   source-test mapping and decisions
migrations/              immutable forward migrations
docs/adr/                architectural decisions
```

## 15. Architectural fitness functions

CI enforces the architecture through:

- forbidden-import and forbidden-call checks in deterministic packages;
- no floating-point economic types;
- migration immutability;
- dependency allowlisting;
- source-test port-map validation;
- formatting, lint, unit, race and repeat runs;
- integration tests for PostgreSQL and NATS;
- fault/restart tests for every durable boundary.

See `scripts/`, `.github/workflows/ci.yml`, and `.golangci.yml`.
