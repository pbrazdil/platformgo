# ADR-0007: Use a reviewed JWT library at compatibility edges

Status: Accepted

## Context

Client access tokens and Centrifugo connection tokens are externally visible
security contracts. The standard library supplies HMAC primitives but not JWT
parsing, registered-claim validation, or algorithm-confusion safeguards.
Maintaining those semantics locally would violate `SECURITY.md`.

## Decision

Use `github.com/golang-jwt/jwt/v5` for JWT signing and verification. Pin
accepted algorithms and required registered claims at every parser. Keep
client and Centrifugo claim types and signing secrets separate, reject
ambiguous claim JSON before verification, and preserve injected time at the
adapter boundary.

## Consequences

- JWT signature construction and verification are no longer maintained in
  platform code.
- Token acceptance is intentionally narrower than the library defaults.
- The direct dependency is pinned, allowlisted, reviewed, scanned, and covered
  by golden interoperability and negative tests.
- JWT remains outside the deterministic engine and money arithmetic.

## Enforcement

`SECURITY.md`, the direct-module allowlist, dependency review,
authentication/realtime contract tests, and `govulncheck`.
