# ADR-0001: Native Go tests are the executable specification

Status: Accepted

## Context

The existing platform is unreliable as a continuously runnable reference and would add operational complexity to parallel development.

## Decision

Read tests at the pinned Rust/Nautilus revisions and port their observable assertions into native Go tests. Do not execute the old runtime or use differential production output as an oracle. Accepted Go tests become the maintained specification.

## Consequences

- Every source test is tracked with provenance.
- Ambiguity requires a decision record.
- Live dependencies are replaced by deterministic fixtures for economic tests.
- Implementation-specific tests may be replaced only with observable invariants.

## Enforcement

`TESTING.md`, `ports/test-port-map.csv`, policy checks and PR review.
