# Engine package instructions

The engine is the deterministic, single-writer state-transition core.

- One goroutine owns mutable state for a shard; no other goroutine mutates it.
- Apply inputs strictly in explicit shard stream order.
- Time, IDs, configuration, instrument metadata, and market data arrive in the input envelope.
- No direct PostgreSQL, NATS, Centrifugo, Hyperliquid, HTTP, environment, wall-clock, or randomness access.
- Every input is replayable and either yields the same decision or a typed fail-closed error.
- Unknown schemas, gaps, impossible transitions, or missing market context are not skipped.
- A command's market context is defined by its ordering/fence, never by whichever quote a goroutine happens to see.
- Decision output must include all state changes, immutable facts, outbox events, scheduled work, and canonical hashes needed for atomic persistence.
- Tests cover repeat execution, duplicate input, restart replay, ordering boundaries, and failure before/after commit through the integration layer.

Read `ARCHITECTURE.md`, `INVARIANTS.md`, and `MESSAGING.md`.
