# ADR-0005: Exact decimal arithmetic is mandatory

Status: Accepted

## Context

Floating-point conversion caused correctness defects in the source platform and is unsuitable for money, quantity and risk calculations.

## Decision

Use one reviewed arbitrary-precision decimal implementation behind internal immutable domain types. Economic inputs and outputs use exact strings or integer units. Every division/quantization names its rounding policy.

## Consequences

- Floating point is banned in economic packages and SQL.
- Canonical decimal formatting is part of deterministic hashing and API compatibility.
- Decimal context and rounding rules receive dedicated tests.

## Enforcement

Policy scripts, linters, database checks and numeric property tests.
