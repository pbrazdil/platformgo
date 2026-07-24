# Hyperliquid adapter instructions

This adapter converts Hyperliquid protocol input into versioned deterministic market events.

- Preserve raw decimal strings until exact parsing; never route through floating point.
- Assign explicit connection epoch and source sequence before concurrent processing can reorder frames.
- Preserve frame array order and identify snapshot, delta, heartbeat, reconnect, gap, and resync events.
- Do not infer missing prices or silently bridge sequence gaps.
- Reconnect performs an explicit resynchronization before risk-increasing trading resumes.
- Venue timestamps are data, not a global ordering guarantee.
- Protocol parsing and economic behavior are separate. Adapter tests use static raw fixtures; engine tests use normalized deterministic events.
- Live tests are minimal protocol canaries and never define fill, margin, or liquidation correctness.
- Additional markets must use a concrete adapter seam, not speculative generic abstractions.

Read `ARCHITECTURE.md`, `TESTING.md`, and `testdata/README.md`.
