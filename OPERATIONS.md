# Operations and Production Safety

## 1. Deployment order

1. Provision PostgreSQL, NATS/JetStream and Centrifugo.
2. Validate backups, credentials, TLS and stream capacity.
3. Run the one-shot migrator with the new image and explicitly provision the
   configured initial engine shard.
4. Start stateless API and worker roles.
5. Start marketdata and establish fresh synchronized feed state.
6. Start exactly one engine for each shard using no-overlap deployment.
7. Verify readiness, input lag, invariant checks and reconciliation before enabling trading.

Schema migration failure blocks deployment.

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
