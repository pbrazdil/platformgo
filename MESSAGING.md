# NATS and Messaging Architecture

## 1. Roles of NATS

NATS is the transport and durable stream system. It is not the source of monetary truth.

Use:

- **Core NATS** for ephemeral wake-ups, health hints and request/reply where loss is explicitly tolerated.
- **JetStream** for engine inputs, commands, domain events and durable jobs.

The system assumes at-least-once delivery and implements exactly-once business effects with PostgreSQL idempotency.

## 2. Streams

Initial stream set:

| Stream | Subjects | Purpose |
|---|---|---|
| `ENGINE_INPUTS_<shard>` | `engine.input.<shard>.>` | one physical total-ordered input history per shard |
| `DOMAIN_EVENTS` | `domain.<version>.<aggregate>.<event>` | committed domain events |
| `JOBS` | `jobs.<version>.<kind>` | non-engine durable work where appropriate |
| `OPS` | `ops.<version>.>` | operational events, never economic authority |

The engine input stream contains commands, market events, timers and config/control changes.

Production stream policy:

- file storage;
- replication appropriate to the failure domain;
- limits retention, not work-queue deletion, for replayable input/event streams;
- `DiscardNew` or equivalent fail-closed behavior when limits are reached;
- explicit maximum message size;
- capacity alerts well before limits;
- no silent discard of unprocessed money-path input.

Exact capacity and retention values are deployment parameters justified by load tests and required recovery window.

## 3. Subjects

Subject names are versioned and bounded:

```text
engine.input.0.command.v1
engine.input.0.market.hyperliquid.v1
engine.input.0.timer.v1
engine.input.0.config.v1

domain.v1.order.filled
domain.v1.position.changed
domain.v1.balance.changed
jobs.v1.realtime.publish
```

Do not place secrets, email addresses, login names or unbounded user data in subjects.

## 4. Message envelope

Every durable message contains:

```json
{
  "messageId": "stable-id",
  "schemaVersion": 1,
  "kind": "...",
  "correlationId": "...",
  "causationId": "...",
  "aggregateId": "...",
  "aggregateVersion": 12,
  "logicalTime": "...",
  "payload": {}
}
```

Engine inputs additionally carry shard, source sequence, configuration revision and instrument revision.

Payload schemas evolve additively within a version. Breaking changes use a new subject/envelope version and migration plan.

### Ordered market-fence binding

Producers cannot predict the physical JetStream sequence at which an input will
be consumed. Producers write `marketSequence=0` as the unresolved transport
representation; for API commands it is the only legal representation and
command admission rejects a nonzero value. After JetStream assigns the input's
shard sequence, the single serialized engine owner resolves a market input to
that assigned sequence and every other unresolved input to the shard-wide
committed market-state high-watermark before computing the business-input hash,
decision, receipt, or checkpoint.

The original producer or command outbox retains the zero sentinel. The
committed engine receipt retains the resolved market sequence and is
authoritative for redelivery: a market event republished at a later shard
sequence retains its original market sequence, and a command arriving after
later market events retains its original market-state fence. Reconciliation
reconstructs the required zero API representation from the non-null `ordered`
marker on every new command, not from the outbox row being audited. Only
pre-migration rows retain a null marker; completed legacy explicit envelopes
are classified from their immutable outbox. The schema upgrade refuses pending
legacy explicit envelopes and cannot overlap a live old engine owner.
Historical rejected market receipts remain immutable and replay without
advancing market state. Historical accepted receipts keep their exact bytes
and hashes while recovery derives the hidden market watermark from durable
physical stream order. Transactional command/outbox and market-receipt guards
keep previous binaries compatible only for the zero-sentinel and
authoritative-market producer contracts. The cutover also advances the engine
runtime-schema revision and checks it first on the shard ownership epoch, then
again on business receipts, duplicate receipts, faults, and checkpoints. An old
engine that verified the previous schema before the migration therefore fails
before it can establish writer/readiness authority instead of crossing the
cutover.

## 5. Publication

Durable publication originates from a PostgreSQL outbox transaction.

Publisher flow:

1. claim rows with bounded batch and `SKIP LOCKED`;
2. publish with `Nats-Msg-Id` equal to the stable outbox/message ID;
3. wait for JetStream publish acknowledgment;
4. mark the row published in PostgreSQL;
5. retry with the same ID after unknown outcome.

A direct post-commit fast publish is allowed to reduce latency only when the outbox remains the repair path and uses the same ID.

JetStream duplicate detection is not sufficient by itself because its deduplication window is finite. Consumers and engine inputs have PostgreSQL receipts.

If the same stable business input is published again after the server
deduplication window, it receives a new shard stream sequence. The engine
commits a separate immutable delivery receipt and advances the shard audit
chain without repeating command, ledger, fill, balance, position, order, or
event effects. The unique business receipt remains the exactly-once authority.

## 6. Consumers

Durable consumers use pull mode by default.

Consumer flow:

1. fetch a bounded batch;
2. validate envelope/schema;
3. begin PostgreSQL transaction;
4. insert inbox record;
5. if duplicate, commit without reapplying;
6. apply effect/projection;
7. commit;
8. acknowledge synchronously.

Never acknowledge before commit.

`AckWait` exceeds the maximum expected transaction time and is monitored. Long processing extends progress deliberately rather than relying on accidental timing.

## 7. Engine consumer

For each shard:

- one durable consumer;
- one physical `ENGINE_INPUTS_<shard>` stream;
- exactly one active process;
- `MaxAckPending = 1` or equivalent serialized delivery;
- strict stream sequence processing;
- no automatic skip/DLQ for malformed or unknown inputs;
- each acknowledged sequence is covered by a durable engine receipt/checkpoint;
- market-only inputs are not acknowledged while represented only in volatile memory;
- fatal readiness failure on gap, schema error or impossible state.

A business rejection is a successfully processed command and is committed/acknowledged. A corrupt envelope is not a business rejection.

## 8. Non-engine consumer failures

Projection and notification consumers may use bounded retry and quarantine/dead-letter policy, provided:

- the original message remains identifiable;
- retry count and last error are observable;
- skipping does not alter monetary truth;
- repair/replay procedures exist.

## 9. Ordering

NATS subject order alone is not a cross-subject business guarantee. The engine relies on its physical `ENGINE_INPUTS_<shard>` stream sequence for total input order. A shared multi-shard stream is forbidden because unrelated shard traffic would create gaps.

Domain-event consumers rely only on ordering explicitly stated by aggregate version or subject partitioning. They detect gaps and do not infer order from timestamps.

## 10. Market data

Normalized Hyperliquid events publish directly to the relevant shard input stream with stable source epoch/sequence.

A reconnect emits explicit control and snapshot events. Duplicate venue events are safe. Source gaps are recorded and trigger fail-closed market status.

## 11. Scheduler

Scheduled economic work becomes an engine timer input with a stable job ID and logical due time. Duplicate scheduler execution produces one engine input/business effect.

## 12. Security

- TLS for all non-local NATS connections.
- NKeys/JWT or equivalent least-privilege credentials.
- Subjects are permissioned by role.
- API cannot publish market or domain-event subjects.
- Marketdata cannot publish command subjects.
- Projectors cannot publish engine inputs except explicitly authorized scheduler/control subjects.
- Payload size and decompression limits are enforced.

## 13. Required messaging tests

- publish acknowledgment and unknown-outcome retry;
- duplicate publish beyond server dedup assumptions;
- crash after effect/commit and before ack;
- consumer restart/replay;
- sequence gap detection;
- stream full/discard-new failure;
- NATS disconnect and reconnect;
- singleton consumer enforcement;
- poison message fail-closed behavior for engine inputs;
- no duplicate ledger/fill/event under repeated redelivery.
