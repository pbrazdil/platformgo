# Contract Instructions

These rules apply to external API, gRPC, realtime, CLI, health, and deployment contract artifacts.

- Read `API_COMPATIBILITY.md`, `SECURITY.md`, and the applicable frozen contract tests.
- Preserve exact methods, paths, status codes, field names, field numbers, enums, nullability, headers, cookies, decimal formatting, token claims, channel names, event envelopes, and idempotency behavior.
- Internal package layout, broker choice, and database schema are not compatibility contracts unless explicitly exposed.
- Contract changes require explicit owner approval, an impact statement, versioning where applicable, and tests for old and new behavior.
- Never regenerate or normalize a frozen contract artifact without reviewing the semantic diff.
- Economic JSON values remain exact strings where the existing contract requires them; never route through floating point.
