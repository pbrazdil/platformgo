# Project Charter

## Mission

Build a production-grade Go implementation of the brokerage platform that can replace the existing platform without requiring client, broker, administrative, or deployment consumers to change their external contract.

The rewrite exists as an independent contingency implementation. It must be safe enough to operate with real money even if it is never activated.

## Technology boundary

The intended runtime stack is:

- Go
- PostgreSQL 17 or newer
- NATS with JetStream
- Centrifugo
- Hyperliquid as the first market-data and market-rules integration

Redis and RabbitMQ are intentionally excluded from the baseline architecture.

## Product scope for the first production-capable release

The first release must support the behavior established by accepted tests for:

- identity and account access required by the existing surface;
- account funding and exact ledger effects;
- Hyperliquid instrument metadata and market data;
- market, limit, stop, amend, cancel, close, and reduce-only order behavior;
- netting and hedging modes where required by tests;
- brackets, stop loss, take profit, and protection cleanup;
- exact fills, fees, margin, funding, PnL, stop-out, and liquidation;
- restart recovery and duplicate delivery;
- REST, gRPC, realtime, health, and operational compatibility required for drop-in deployment.

## Explicit non-goals until the Hyperliquid replacement is proven

- a generic multi-venue framework;
- a second market or venue;
- A-book routing or LP execution;
- frontend redesign;
- API cleanup or renaming;
- speculative microservices;
- broad plugin systems;
- event-sourcing every administrative entity;
- reproducing Nautilus actors, caches, thread models, or internal database schema;
- optimizing before deterministic correctness and recovery are measured.

## Source specification

The source revisions are pinned in `ports/SOURCE_REVISIONS.md`.

The old runtime is not executed. Agents read and port source tests into native Go tests. Accepted Go tests become the maintained executable specification.

## Success criteria

The implementation is a credible replacement only when:

- all in-scope source tests are accounted for;
- accepted compatibility tests pass;
- repeated deterministic runs produce identical canonical results;
- duplicate command/message delivery produces one business effect;
- every crash failpoint recovers to the same valid state;
- ledger, fill, position, order, and balance invariants hold under fuzz, load, and recovery tests;
- migrations upgrade a production-like previous schema without reset;
- PostgreSQL restore is rehearsed;
- the engine singleton and shard ownership are enforced;
- NATS loss/reconnect and consumer replay are tested;
- realtime gaps force snapshot recovery rather than silent divergence;
- operational kill-switch, close-only mode, alerts, and reconciliation are proven.

## Delivery phases

### Phase 0 — policy and test harness

- repository policy files and CI;
- exact decimal package;
- deterministic clock and IDs;
- engine fixture;
- source test inventory and port map.

### Phase 1 — pure engine

- order state machine;
- matching and fills;
- positions and OMS behavior;
- margin, funding, liquidation;
- brackets and triggers;
- pure deterministic tests.

### Phase 2 — durable execution

- PostgreSQL schema and immutable migrations;
- command/idempotency journal;
- ledger and state transactions;
- NATS input stream, outbox, inbox, replay;
- fault and restart testing.

### Phase 3 — compatibility edges

- REST and gRPC surfaces;
- authentication and idempotency responses;
- realtime/Centrifugo contract;
- deployment-role compatibility.

### Phase 4 — Hyperliquid production integration

- protocol fixtures;
- controlled live canaries;
- reconnect/gap handling;
- capacity, soak, and incident drills.

### Phase 5 — replacement rehearsal

- production-like data import;
- close-only/drain/cutover rehearsal;
- rollback and reconciliation plan;
- audited go-live decision.
