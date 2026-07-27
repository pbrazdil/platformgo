Title: Preserve the current Go trader-profile and audience boundary

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/identity/e2e_trader.rs::login_and_own_profile_with_cross_audience_rejection`
- `shared/api/src/auth.rs::UserProfile`
- `contracts/openapi/client-v1.json::UserProfile`
- `internal/edge/types.go::UserProfile`

Conflict or ambiguity:
The pinned Rust test requires `GET /v1/me` to return `email`, `kycStatus`,
`userId`, and `status`; the pinned Rust profile also exposes `login`. The
current Go runtime and its already frozen client OpenAPI contract expose
exactly `userId`, `login`, `email`, and `status`. They do not expose
`kycStatus`, and the current Go identity schema has no durable KYC-status
authority from which to populate it.

The tested active-user status is compatible: both runtimes return `active` for
the created user. Current Go hard-codes that status because its identity schema
also lacks the source status column, so this acceptance cannot claim a broader
status lifecycle. Adding a hard-coded KYC value would appear source-compatible
while inventing an identity authority that current Go does not have.

The pinned test also registers an admin, successfully logs in through the
admin route, and then proves that the resulting admin-audience token receives
`401` from the client `/v1/me` route. Current Go has no production admin login
route, admin credential store, or admin token issuer. It can prove the
cross-audience rejection with a correctly signed admin-audience token, but it
cannot honestly preserve the preceding successful admin login.

Economic/API impact:
These are intentional external identity-contract deviations. Existing Go
clients continue to receive the frozen four-field profile, and the absent admin
plane remains absent. No password, token, authorization, session, account,
monetary, ordering, idempotency, migration, or transaction behavior changes.
A client that requires the pinned Rust `kycStatus` field or admin login must
adapt; this decision claims neither KYC nor admin-plane compatibility.

Options considered:
1. Add `kycStatus: "none"` and a synthetic admin login solely to make the
   source test appear green.
2. Add new PostgreSQL KYC, user-status, admin-credential, and admin-session
   authorities as part of this source-test acceptance.
3. Preserve the current Go four-field wire contract and absent admin plane,
   then accept the remaining login, profile, and audience-isolation behavior
   with both deviations explicit.

Decision:
Choose option 3 under the owner's standing instruction to preserve current Go
behavior as the source. The follow-on source acceptance may prove successful
password login, the current four-field profile, invalid-password rejection,
anonymous rejection, and cross-audience rejection. It must assert that
`kycStatus` remains absent and must not claim the pinned Rust KYC projection,
successful admin login, admin credential/session authority, session rotation,
revocation, or account-status lifecycle. A correctly signed admin-audience
token may be used only to exercise the production client authenticator's
fail-closed audience boundary.

Tests added/changed:
- This decision-only layer changes no executable test.
- The follow-on acceptance must use the production HTTP edge, HMAC
  authentication, identity application service, least-privilege API database
  role, and PostgreSQL 19.
- It must replace the fixture-only placeholder, freeze the current four-field
  profile exactly, causally prove password and audience rejection, and record
  this intentional deviation in the compatibility manifest.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-27
