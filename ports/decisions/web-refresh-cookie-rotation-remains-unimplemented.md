Title: Keep cookie refresh rotation unresolved at the current Go boundary

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/identity/e2e_web_refresh_cookie.rs::web_refresh_redeems_from_cookie_and_rotates`
- `ports/decisions/web-login-cookie-placement-preserves-current-go-client-boundary.md`
- `internal/edge/http.go::Server.route`
- `contracts/openapi/client-v1.json`
- `contracts/openapi/admin-v1.json`
- `API_COMPATIBILITY.md`

Conflict or ambiguity:
The pinned Rust test first obtains a non-empty `uzo_admin_refresh` cookie from
an Origin-bearing admin login. It then submits that cookie alone to
`POST /admin/v1/auth/refresh` and requires `200`, a different non-empty refresh
cookie, a non-empty body `accessToken`, no body `refreshToken`, and a second
successful redemption using the rotated cookie.

Current Go has no production refresh handler, admin session issuer,
cookie-sourced credential parser, refresh-token redemption, rotation, or token
family boundary. `POST /v1/auth/refresh` and
`POST /admin/v1/auth/refresh` exist only in the frozen OpenAPI source-route
inventory. The production router returns its generic `404` for both paths.
The already accepted current-Go web-login boundary emits no cookie and returns
the refresh credential in the JSON body.

The owner's standing instruction makes current Go behavior authoritative and
therefore forbids adding a partial cookie-refresh plane solely to satisfy this
source row. However, `API_COMPATIBILITY.md` also forbids labeling
unimplemented behavior as an intentional deviation. A `404` acceptance would
misrepresent route absence as implemented refresh compatibility and would
silently reject every positive source assertion.

Security and compatibility impact:
Cookie refresh cannot be introduced safely without the complete browser
session boundary: credentialed origin validation, CSRF protection, cookie
attributes and path, token hash lookup, one-time rotation, token-family replay
response, revocation, logout, concurrency, retry, lost-response, and restart
behavior. None of those capabilities may be inferred from the current login
acceptance or from an OpenAPI inventory entry.

A source-compatible web client cannot refresh against current Go. Existing Go
clients also have no supported refresh endpoint. This record changes no
runtime route, response, cookie, session, migration, manifest, or source-port
acceptance state.

Options considered:
1. Implement the source admin cookie-refresh plane in this narrow source-port
   slice.
2. Adapt the source test to the unimplemented client inventory route and mark
   the current `404` as an intentional deviation.
3. Preserve current Go as authority, record the missing boundary explicitly,
   and keep the source row unresolved until a complete refresh lifecycle is
   intentionally implemented and reviewed.

Decision:
Choose option 3 under the owner's standing instruction to preserve current Go
behavior as the source.

The source row remains `ported/unreviewed/placeholder`. This record must not be
added to `contracts/compatibility-manifest.json`, and no acceptance may claim:
- successful client or admin refresh;
- cookie-sourced refresh authentication;
- refresh-token body redemption;
- refresh-token rotation;
- one-time use or old-token rejection;
- token-family replay response;
- refresh-cookie issuance or attributes;
- credentialed CORS, browser safety, or CSRF protection;
- access-token validation;
- logout or revocation;
- retry, lost acknowledgment, restart, or recovery.

A future implementation slice must establish the complete intended current-Go
refresh contract first. It must then port the source assertions through
production HTTP and the least-privilege PostgreSQL role, with direct
concurrency, duplicate/replay, transaction, restart, and failure evidence
before this row can become reviewed and green.

Tests added/changed:
- This decision-only gate changes no runtime, compatibility manifest, source
  port ledger row, README progress count, or test. README records the missing
  refresh lifecycle as remaining Phase 3 work.
- The fixture placeholder remains non-authoritative and does not prove a
  production refresh capability.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-28
