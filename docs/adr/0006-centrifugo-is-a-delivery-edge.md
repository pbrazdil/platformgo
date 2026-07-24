# ADR-0006: Centrifugo is a non-authoritative realtime edge

Status: Accepted

## Context

The runtime stack should avoid Redis while retaining the existing realtime client boundary.

## Decision

Retain Centrifugo for client connections and live fanout. Publications originate from PostgreSQL outbox. Monetary correctness never depends on Centrifugo history. With NATS broker mode, clients detect duplicates/gaps by stable sequence and reload authoritative snapshots when continuity is not proven.

## Consequences

- Realtime may be temporarily unavailable without rolling back economic commits.
- Client snapshot/gap recovery is mandatory.
- PostgreSQL broker/history requires a separate ADR and load validation before production use.
- Adding Redis requires a new ADR.

## Enforcement

Realtime contract tests, sequence fields, snapshot recovery tests and deployment configuration.
