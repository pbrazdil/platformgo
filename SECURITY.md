# Security Rules

## 1. Security posture

Security failures in this system can become money or account-integrity failures. Security controls are part of correctness, not an optional hardening phase.

## 2. Trust boundaries

Untrusted inputs include:

- public/client/admin/broker HTTP and gRPC;
- Hyperliquid frames and metadata;
- NATS payloads, even on authenticated subjects;
- database contents after partial/older deployments;
- realtime client claims;
- configuration and secrets supplied by deployment.

Validate at each boundary. Internal origin does not make a payload valid.

## 3. Secrets

- Secrets come from the deployment secret manager/environment, never source control.
- Production startup fails closed on missing, empty or known-development secrets.
- Never log credentials, private keys, password hashes, session tokens, API keys, TOTP seeds, raw authorization headers or full DSNs.
- Secret rotation procedures are documented before a secret type ships.

## 4. Cryptography

- Use standard or reviewed cryptographic libraries.
- Never implement password hashing, JWT signatures, TOTP, encryption or random generation ad hoc.
- Passwords use Argon2id parameters documented and benchmarked for production hardware.
- Sensitive factors/keys stored in PostgreSQL are encrypted with externally supplied key material where required.
- Randomness is injected into deterministic core; adapters use `crypto/rand`.
- User API-key idempotency responses use AES-256-GCM with an externally supplied
  keyring and explicit active key ID. The key table and audit trail retain only
  the authentication digest and non-secret metadata; the bounded replay table
  retains authenticated ciphertext, nonce and key ID.
- Shown-once credential creation requires a stable idempotency key. The exact
  encrypted HTTP response commits atomically with the credential and audit fact
  so a lost success response cannot leave an unrecoverable active credential.
- Existing shown-once credential replay or request-hash conflict is resolved
  before new-work rate rejection. Invalid new requests consume shared rate
  capacity exactly once; replay and conflict consume no new-work capacity.
- Replay-key rotation distributes future decryption material to every replica
  before activation and retains old keys through the replay TTL and cleanup
  horizon.

## 5. Authorization

- All mutations pass one authorization choke point before command creation.
- Authorization identity and scopes become part of the immutable command/audit record.
- Database roles prevent API code from writing monetary tables.
- `platformgo_api` is a trusted authenticated-principal authority for identity
  mutations. Its ability to execute a credential-minting definer function is an
  explicit trust boundary; compromise of that role triggers credential
  reconciliation and is not contained as an ordinary read-only database leak.
- NATS permissions prevent cross-role subject publication/subscription.
- Privileged risk settings, kill switch and account operations require explicit permissions and audit.

## 6. Input hardening

- Enforce body/message size limits.
- Reject unknown/invalid enums and malformed decimals.
- Bound arrays, book levels, strings and decompressed data.
- Use strict JSON decoding where compatibility allows.
- Validate Hyperliquid instrument identity before applying data.
- Do not construct NATS subjects or SQL identifiers directly from untrusted strings.

## 7. Network

- TLS at public edges and for non-local PostgreSQL/NATS/Centrifugo connections.
- Least-privilege network policies.
- Only API/gRPC and Centrifugo are publicly reachable.
- Marketdata has restricted outbound venue access.
- Administrative diagnostics are private.

## 8. Logging and audit

- Structured logs with request, command, input, account and correlation IDs.
- Redaction occurs before serialization.
- Audit records capture actor, action, target, before/after, request ID, logical time and result.
- Audit records are append-only and separately retained.

## 9. Dependency and build security

- Dependency allowlist and review.
- `govulncheck`, static analysis, secret scanning and container scanning in CI.
- Signed release artifacts and provenance before production.
- No build step downloads executable code from an unpinned source.

## 10. Security test requirements

- authn/authz negative tests;
- token expiry/revocation;
- idempotency abuse and request-hash mismatch;
- rate-limit and lockout behavior;
- malformed decimal/protobuf/JSON/NATS/Hyperliquid frames;
- cross-account/cross-tenant isolation;
- SQL and subject injection;
- log-redaction tests;
- permission tests for database and NATS roles.
