# Centrifugo adapter instructions

Centrifugo is the client connection and fanout edge, never the source of truth.

- Publish only from committed PostgreSQL outbox records.
- Every event has a stable event ID, schema version, and monotonic channel or aggregate sequence.
- Publication is idempotent from the application's perspective; clients tolerate duplicates.
- Gaps trigger an authoritative API snapshot reload.
- Monetary correctness must not depend on Centrifugo history, presence, offsets, or broker state.
- Preserve tested token claims, channel names, envelopes, ordering fields, and recovery behavior.
- Do not add Redis or another broker without an ADR.
- Tests cover duplicate publication, missing event detection, reconnect, token scope, unauthorized channels, snapshot fallback, and serialization compatibility.

Read `API_COMPATIBILITY.md`, `MESSAGING.md`, and `ARCHITECTURE.md`.
