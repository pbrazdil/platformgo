Title: Preserve current Go token placement for Origin-bearing client login

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/identity/e2e_web_refresh_cookie.rs::web_login_sets_httponly_cookie_and_omits_body_refresh`
- `ports/decisions/native-login-refresh-placement-preserves-current-go-client-boundary.md`
- `internal/edge/http.go::Server.handleLogin`
- `internal/edge/http.go::Server.harden`
- `internal/edge/types.go::LoginResponse`
- `contracts/openapi/client-v1.json`

Conflict or ambiguity:
The pinned Rust test submits an Origin-bearing login to
`POST /admin/v1/auth/login` for a registered admin. It requires `200`, the
configured origin in `Access-Control-Allow-Origin`,
`Access-Control-Allow-Credentials: true`, a non-empty
`uzo_admin_refresh` cookie with `HttpOnly` and
`Path=/admin/v1/auth`, a non-empty body `accessToken`, and no
`refreshToken` field in the response body.

Current Go has no production admin login route, admin credential authority, or
admin session issuer. Its frozen client boundary is `POST /v1/auth/login`, and
its `LoginResponse` requires both `accessToken` and `refreshToken`. The login
handler does not branch on `Origin`, emits no cookie, and does not enable
credentialed CORS. The hardening wrapper emits one statically configured
`Access-Control-Allow-Origin` value independently of the request origin; this
is not dynamic origin reflection or allowlist validation.

The source setup also selects `Secure=false` and `SameSite=Lax`, but this test
does not explicitly assert those cookie attributes. They are not additional
requirements of this decision.

Security and API rationale:
Adding a synthetic admin or browser-cookie branch solely for this source row
would fabricate an absent admin credential/session authority and introduce
cookie authentication without the complete CSRF, origin-validation, refresh
redemption, rotation, token-family replay, revocation, and logout boundary.
Preserving the existing client token-in-body contract avoids claiming a partial
browser security plane.

This choice is not a claim that the current Go response is browser-safe or
compatible with the pinned admin web client. A source-compatible consumer
cannot rely on `/admin/v1/auth/login`, credentialed CORS,
`uzo_admin_refresh`, cookie-only refresh placement, or omission of the refresh
credential from the JSON body. Existing current Go clients continue receiving
both credentials in the JSON body. No runtime route, response schema,
migration, or production behavior changes.

Options considered:
1. Implement the source admin login, credentialed CORS, cookie issuance, and
   body omission in this narrow source-port slice.
2. Leave the row unresolved until the complete admin credential, MFA, browser
   session, CSRF, refresh, rotation, revocation, and logout plane exists.
3. Preserve the current Go client login contract as authority and record every
   rejected browser-specific source assertion as an intentional deviation.

Decision:
Choose option 3 under the owner's standing instruction to preserve current Go
behavior as the source. The follow-on acceptance must send the source-shaped
`Origin: https://admin.web.test` header to production client
`POST /v1/auth/login`, with the server's configured allow-origin value set to
the same string, and prove:
- the response status is `200`;
- `Access-Control-Allow-Origin` is exactly
  `https://admin.web.test`;
- no `Access-Control-Allow-Credentials` header is emitted;
- no `Set-Cookie` header is emitted;
- the JSON body contains a non-empty `accessToken`; and
- the JSON body contains a non-empty `refreshToken`.

The exact configured allow-origin assertion does not prove request-origin
reflection, origin allowlist membership, rejection of an unconfigured origin,
multi-origin selection, or credentialed CORS.

The following pinned source assertions are intentionally rejected at the
current Go boundary:
- successful `POST /admin/v1/auth/login`;
- `Access-Control-Allow-Credentials: true`;
- refresh-cookie issuance;
- the `uzo_admin_refresh` cookie name and a non-empty cookie token;
- `HttpOnly`;
- `Path=/admin/v1/auth`; and
- omission of `refreshToken` from the response body.

The acceptance may additionally prove that exactly one PostgreSQL session is
committed before `200` and that it stores only the SHA-256 hash of the returned
refresh token. Any such strengthening needs its own direct assertions and
controlled failure evidence.

The decision and follow-on acceptance must not claim successful admin login,
admin credentials or sessions, admin MFA, browser-origin detection, dynamic
origin allowlisting or reflection, credentialed browser CORS, positive cookie
issuance or attributes, browser safety, CSRF protection, cookie-sourced
refresh, refresh redemption or rotation, token-family replay protection,
logout, revocation, access-token validation, password-creation compatibility,
retry, lost acknowledgment, restart, or recovery.

Tests added/changed:
- This decision-only layer changes no runtime, compatibility manifest, source
  port ledger row, README status, or test.
- A separate compatibility-manifest layer must record
  `web-login-cookie-placement-preserves-current-go-client-boundary`.
- Only after that layer lands may a separate source-acceptance layer replace
  the fixture placeholder with production HTTP and PostgreSQL evidence.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-28
