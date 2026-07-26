Title: Require idempotency for shown-once client API-key creation

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/src/api/middleware.rs::idempotency`
- `apps/app/tests/it/identity/e2e_trader.rs::client_creates_own_api_key`

Conflict or ambiguity:
The pinned client mutation middleware accepts a missing `Idempotency-Key`.
API-key creation returns a plaintext credential that is not retained in the
key or audit tables. If the durable creation commits and the first HTTP `201`
is lost, a request without a stable key cannot recover that credential or
distinguish the committed outcome from a failed request. This conflicts with
the project requirements that an unknown outcome is resolved by lookup and
that an accepted idempotent retry returns the stored response.

Economic/API impact:
The replacement intentionally rejects authenticated
`POST /v1/me/api-keys` requests without a nonempty `Idempotency-Key` with
`400 invalid_request`. Existing callers must supply a stable key for each
logical credential creation. The stricter boundary prevents an active trading
credential whose only plaintext response was lost and prevents unsafe
create-again recovery.

Options considered:
1. Preserve the optional header and accept unrecoverable committed
   credentials.
2. Add a new two-phase pending/activation protocol.
3. Require a stable idempotency key and atomically persist an encrypted exact
   HTTP response envelope with the key and audit fact.

Decision:
Choose option 3. It is the smallest fail-closed change, preserves one durable
credential per logical operation, and provides exact recovery after timeout,
disconnect, process restart, or post-commit uncertainty. The implementation
acceptance change must freeze the deviation as
`client-api-key-creation-requires-idempotency-key` in the compatibility
manifest.

Required tests before implementation or port acceptance:
- Preserve the source-compatible protected-client ordering: authenticate the
  principal, claim the shared rate bucket, then require the idempotency key.
- Missing header returns `400` before any credential entropy, key, audit, or
  replay effect, while consuming the authenticated principal's rate allowance.
- Same key and canonical request replays the exact stored status, required
  headers, and body.
- Same key with a changed effective request returns deterministic `409`.
- Post-commit unknown outcome recovers after restart without new entropy.
- Concurrent duplicate first deliveries converge on one durable effect.

Approver: Petr Brazdil, through the active owner-authorized task directing the
agent to continue without further confirmations and permitting rollback.

Date: 2026-07-26
