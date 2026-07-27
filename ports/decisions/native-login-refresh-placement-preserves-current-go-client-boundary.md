Title: Preserve current Go native-login refresh placement at the client boundary

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/identity/e2e_web_refresh_cookie.rs::native_login_keeps_refresh_in_body_and_sets_no_cookie`
- `ports/decisions/trader-profile-preserves-current-go-identity-boundary.md`
- `internal/edge/http.go::Server.handleLogin`
- `internal/application/identity.go::Identity.Login`

Conflict or ambiguity:
The pinned Rust test submits a native login to
`POST /admin/v1/auth/login`. It asserts `200`, no `Set-Cookie` header, and
non-empty `accessToken` and `refreshToken` response fields. Current Go has no
production admin login route, admin credential authority, or admin session
issuer. The owner-approved trader-profile decision intentionally preserves that
absent admin plane.

Current Go does implement the same native token-placement behavior at its
production client boundary, `POST /v1/auth/login`: a valid password login
returns the access and refresh credentials in the JSON body and does not set a
refresh cookie. PostgreSQL stores only the refresh-token hash. Treating a
synthetic admin handler or fixture as proof would invent the missing admin
authority; treating the current route's `404` as the source's native-login
behavior would drop every positive source assertion.

Economic/API impact:
This is an intentional route and principal adaptation for one token-placement
source behavior. It changes no runtime route or response. Existing Go clients
retain the current client login contract, and the admin login plane remains
absent. No account, monetary, ordering, idempotency, migration, cookie, CORS,
refresh-rotation, revocation, or session-family behavior changes.

A consumer requiring the pinned admin login route is not compatible with this
acceptance. The decision does not label that unimplemented route as supported;
it limits the accepted behavior to the already implemented current-Go client
boundary.

Options considered:
1. Add a synthetic admin login and session authority solely to preserve the
   source route in this narrow token-placement test.
2. Keep the placeholder until the complete admin credential, login, MFA, and
   session plane is implemented.
3. Preserve the current Go client login boundary as authoritative and accept
   only the source's native token-placement assertions there, with the route
   and principal deviation explicit.

Decision:
Choose option 3 under the owner's standing instruction to preserve current Go
behavior as the source. The follow-on acceptance must prove that production
client password login:
- returns `200`;
- emits no `Set-Cookie` header;
- returns a non-empty `accessToken` in the JSON body; and
- returns a non-empty `refreshToken` in the JSON body.

It may additionally strengthen those source assertions by proving that
PostgreSQL persists only the refresh-token hash. The acceptance must not claim
successful admin login, an implemented admin credential/session authority,
web-origin branching, positive web-cookie issuance or attributes, CORS,
refresh redemption or rotation, token-family replay protection, logout,
revocation, restart, retry, or lost-acknowledgment behavior. Those remain
separate source behaviors.

Tests added/changed:
- This decision layer changes no runtime or source-port ledger row.
- A separate compatibility-manifest layer must record
  `native-login-refresh-placement-preserves-current-go-client-boundary`.
- The follow-on source acceptance must use the production HTTP edge, HMAC
  authentication, Argon2id verification, least-privilege API database role,
  and PostgreSQL 19 Beta 2.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-27
