# API edge instructions

The edge preserves the external compatibility contract and delegates economics to the application/engine.

- Preserve tested paths, methods, status codes, headers, cookies, JSON field names, nullability, decimal formatting, protobuf field numbers, enums, and gRPC status mapping.
- Authenticate, authorize, validate shape, enforce request limits, and claim idempotency keys at the edge.
- Do not reimplement margin, fill, fee, funding, liquidation, or other monetary rules in handlers.
- A timeout does not create a second command; resolve through the same idempotency record or command status.
- Canonical request hashes distinguish safe retries from key reuse with different input.
- Error responses are typed, stable, and free of secrets.
- Contract tests are black-box where practical and cover duplicate requests, timeout/unknown outcome, malformed decimals, null/omitted fields, and auth boundaries.

Read `API_COMPATIBILITY.md`, `SECURITY.md`, and `CONFIGURATION.md`.
