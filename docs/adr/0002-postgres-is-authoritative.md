# ADR-0002: PostgreSQL is the authoritative monetary store

Status: Accepted

## Context

The system needs atomic monetary state, idempotency, audit, recovery and migration discipline with minimal infrastructure.

## Decision

PostgreSQL owns commands, ledger, balances, orders, positions, fills, engine receipts/checkpoints, outbox, inbox, configuration and audit. NATS and Centrifugo are delivery/projection systems.

## Consequences

- One PostgreSQL transaction is the business commit point.
- Network publication follows commit through outbox.
- Database roles enforce mutation ownership.
- Recovery and reconciliation are PostgreSQL-centered.

## Enforcement

`DATABASE.md`, schema grants, transaction tests and architecture policy checks.
