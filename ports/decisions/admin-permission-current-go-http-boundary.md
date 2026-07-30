# Admin permission catalog uses the current Go HTTP boundary

Status: accepted by owner direction on 2026-07-30

## Context

The reviewed Go permission-catalog test is the maintained authority for the
ordered eleven-resource and four-action catalog and its application-level
audience gate. The frozen admin OpenAPI currently inventories
`GET /admin/v1/permissions`, but it does not yet freeze response schemas,
credential encoding, or status mappings. The pinned source implementation also
declares a separate durable `Roles:Read` dispatcher policy.

The owner directed that current accepted Go behavior remain the source while
Phase 3 continues. This decision fills only the missing Go HTTP/authentication
boundary; it does not reinterpret the accepted catalog.

## Decision

- A successful response uses the accepted ordered Go catalog with lowercase
  `resources`, `actions`, `id`, and `label` JSON names. Array order is
  contractual. JSON object-member byte order is not.
- Missing, invalid, expired, noncanonical, or wrong-audience credentials map
  to the current Go generic `401 unauthorized` response. Authorization and
  catalog code do not run.
- An authenticated admin without effective durable `roles/read` maps to the
  current Go generic `403 forbidden` response. Catalog code does not run.
- PostgreSQL authorization failure or catalog-service failure maps to the
  current Go opaque `503 unavailable` response. No valid catalog prefix or
  item may escape.
- PostgreSQL is the authorization authority. JWT `roles` and `scopes` are not
  copied into the permission decision and cannot grant access.
- The current Go HMAC access-token authority is extended to the strict
  `admin` audience and canonical `urn:xb:admin:<lowercase UUID>` subjects. This
  requires a separately configured admin key that is never equal to the client
  token key and fails closed when absent. This intentionally does not claim
  compatibility with the pinned source's EdDSA key format, `kid`, successful
  admin login, refresh sessions, or credential issuance.
- Unknown query parameters, GET bodies, unsupported methods, trailing slashes,
  `Allow` headers, and JSON object-member order remain unspecified.

## Activation boundary

The additive RBAC migration is empty and deny-by-default. It must not seed a
guessed production subject, development credential, or unaudited wildcard
assignment. The hardened PostgreSQL authority, adapter, admin verifier, and
testable edge may land first. The production runtime route remains inactive
until a separate reviewed slice provides an audited first-admin/bootstrap
assignment path and its operational recovery procedure.

This staged foundation is not an external-route completion claim. The OpenAPI
operation remains `source-route-inventory`, the compatibility manifest does
not list the route as implemented, and the existing source-test ledger row
continues to describe only its accepted application evidence.

## Compatibility impact

Client and broker authentication are unchanged. Economic state, engine
ordering, idempotency, messaging, and realtime behavior are unchanged. The new
PostgreSQL relations are additive, have no runtime DML grants, and expose only
one non-grantable `SECURITY DEFINER` authorization function to
`platformgo_api`.
