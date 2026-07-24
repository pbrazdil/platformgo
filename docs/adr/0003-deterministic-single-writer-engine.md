# ADR-0003: One deterministic writer per engine shard

Status: Accepted

## Context

Concurrent mutation and implicit arrival timing make monetary execution difficult to reproduce and recover safely.

## Decision

Each shard has one total ordered JetStream input stream and exactly one active engine writer. The deterministic core applies inputs serially and has no I/O, wall clock or randomness.

## Consequences

- Live ordering is explicit and replayable.
- Horizontal scale is by disjoint account shards, not multiple writers for the same state.
- Engine deployment must prevent overlap.
- Throughput optimizations may not weaken ordering or replay semantics.

## Enforcement

Shard lease/lock, consumer configuration, deployment strategy, deterministic tests and race checks.
