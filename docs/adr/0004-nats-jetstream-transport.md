# ADR-0004: NATS JetStream is the durable transport

Status: Accepted

## Context

The rewrite should reduce infrastructure while retaining durable commands/events, replay and controlled fanout.

## Decision

Use JetStream for durable engine inputs, domain events and jobs. Use Core NATS only for explicitly ephemeral signals. Implement exactly-once business effects through PostgreSQL outbox/inbox/input receipts, not transport assumptions.

## Consequences

- Durable consumers use explicit acknowledgment after commit.
- Stable message IDs support deduplication.
- Stream limits fail closed.
- PostgreSQL remains authoritative.

## Enforcement

`MESSAGING.md`, NATS integration tests and permissions.
