Title: Resolve shown-once API-key replay before rate rejection

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/src/api/client/mod.rs`
- `apps/app/src/api/middleware.rs::idempotency`
- `apps/app/tests/it/trading/e2e_rate_limit.rs::protected_surface_is_per_principal_rate_limited`

Conflict or ambiguity:
The pinned client router effectively authenticates, claims per-principal rate
capacity, and only then resolves idempotency. An immediate retry after a
committed but lost API-key `201` can therefore receive `429` instead of the
stored credential response. That conflicts with the higher-authority project
requirements that the same scope, key and request return the same response and
that an unknown committed outcome is resolved by durable lookup.

Economic/API impact:
For shown-once client API-key creation, the replacement resolves an existing
same-request replay or changed-request conflict before rejecting for exhausted
new-work rate capacity. A replay returns the stored `201`; a changed request
returns deterministic `409`. Neither consumes credential entropy, creates a
new durable effect, nor consumes new-work rate capacity. A valid new operation
claims the shared rate bucket atomically with its durable transaction.
Authenticated malformed or otherwise invalid new requests still claim the
shared rate bucket exactly once before returning `400` or `429`.

Options considered:
1. Preserve source ordering and allow an exhausted rate bucket to suppress
   recovery of a committed shown-once credential.
2. Exempt the complete API-key route from rate limiting.
3. Classify canonical replay/conflict first, rate-account every terminal
   invalid new request once, and atomically rate-admit only genuinely new work.

Decision:
Choose option 3. The exactly-once and unknown-outcome invariants outrank pinned
source middleware order. The implementation acceptance change must freeze the
deviation as `api-key-idempotent-resolution-precedes-rate-rejection` in the
compatibility manifest.

Required tests before implementation or port acceptance:
- A committed same-request replay returns the exact stored status, headers and
  body while the principal's new-work bucket is exhausted.
- The same key with a changed canonical request returns deterministic `409`
  while that bucket is exhausted.
- Concurrent duplicate first deliveries consume one new-work rate admission
  and converge on one credential, audit fact and replay envelope.
- Malformed JSON, invalid known fields and invalid ancillary metadata consume
  rate exactly once, create no credential/audit/replay effect, and eventually
  return the pinned `429` wire response across replicas.
- A new valid request claims rate capacity in the same PostgreSQL transaction
  as its credential, audit fact and encrypted response.

Approver: Petr Brazdil, through the active owner-authorized task directing the
agent to continue all required Phase 3 work without further confirmations.

Date: 2026-07-26
