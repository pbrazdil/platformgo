# External API Compatibility

## 1. Objective

The Go implementation must be deployable as a replacement without requiring existing client, admin, broker, realtime or infrastructure consumers to change their contract.

Compatibility is proven by tests and frozen artifacts, not by matching internal code.

## 2. Contract sources

In authority order:

1. accepted Go contract tests;
2. explicit assertions in pinned platform tests;
3. frozen OpenAPI documents;
4. frozen protobuf definitions and descriptors;
5. frozen realtime token/channel/envelope fixtures;
6. accepted intentional-deviation decisions.

Store frozen artifacts under `contracts/` once imported.

## 3. HTTP compatibility

Preserve as tested:

- methods and paths;
- query and path parameter parsing;
- request size and content type behavior;
- authentication and authorization;
- status codes;
- error codes, messages and field structure;
- JSON field names and casing;
- omitted versus `null` fields;
- array ordering where observable;
- timestamps;
- decimal string formatting;
- headers, cookies, cache and CORS behavior;
- idempotency semantics;
- pagination and cursors.

Economic decimals are never JSON numbers if the source contract uses strings.

## 4. Idempotency contract

For every mutation requiring idempotency:

- scope and key rules match the source contract;
- same key and canonical request returns the stored response exactly;
- same key with a different request is rejected deterministically;
- retry after timeout never creates a new command;
- response body, status and required headers are persisted.

### Broker echo exact replay

Broker echo binds an idempotency key to the authenticated broker principal and
canonical request hash. The first accepted request commits the exact HTTP
status, logical required headers, and response body bytes in PostgreSQL. Every
same-key/same-request retry returns those stored values without re-rendering;
transport correlation headers such as the current attempt's request ID are not
part of the stored logical response. Same-key/different-request remains a
deterministic conflict.

The guarantee includes concurrent duplicates and a retry after PostgreSQL
commits but the HTTP success acknowledgment is lost. PostgreSQL statement time,
not an API process clock, owns the 24-hour replay lifetime. After expiry, a new
claim may replace the old row; cleanup is bounded and may delete expired rows
only.

### Broker account point read

`GET /broker/v1/accounts/{accountId}` returns the accepted ten-field
`MyAccountView`. Broker HMAC authentication dominates exact
`accounts:read`/wildcard scope, which dominates strict lowercase UUID account
URN parsing, which dominates storage access.

The authenticated tenant and account ID are bound inside one PostgreSQL
statement. Absent and foreign ownership are intentionally indistinguishable as
`400 invalid_request / unknown account`. A tenant-owned incomplete, inconsistent,
or corrupt projection returns opaque `503` without any partial account fields.
Valid output preserves the current-Go enum, venue, class, currency, and UTC
timestamp rendering. A finite PostgreSQL timestamp outside RFC3339's
representable year range is rejected as projection corruption. The point read
is non-mutating and has no replay, acknowledgment, or realtime boundary.

### Broker account list

`GET /broker/v1/accounts` returns the accepted non-null array of ten-field
`MyAccountView` values. Broker HMAC authentication dominates exact
`accounts:read`/wildcard scope, which dominates strict parsing of the optional
lowercase UUID `userId` URN, which dominates storage access. Unknown query keys
remain ignored to preserve the pinned source extractor; blank, duplicate, or
noncanonical `userId` values return `400 invalid_request / invalid user id`.

The store chooses one of two fixed parameterized statements and executes each
through pgx's unnamed extended protocol so PostgreSQL plans for the concrete
tenant instead of aging into a cached generic plan. The unfiltered plan uses
the tenant-leading ownership index. The filtered statement first performs a
one-time lookup of the user/tenant pair, then uses the user-key ownership
index; an absent or foreign filter never scans that user's account range.
`Principal.Tenant` remains authority inside the materialized ownership query,
while the optional user ID is only an additional filter. Foreign and absent
filters both return exact `[]`. Tenant-constrained lateral profile and trading
probes use their account-ID indexes; nullable projections never authorize.

Every selected row is scanned and validated before the edge writes a response.
One owned incomplete or corrupt row fails the whole list as opaque `503`; no
valid prefix is exposed. Valid rows sort by login and then account ID under
bytewise `C` collation. The source-compatible route remains unpaged, so sorting,
buffering, and serialization are proportional only to the authorized output.
The list is non-mutating and has no replay, acknowledgment, or realtime
boundary.

### Broker account funding

`GET /broker/v1/accounts/{accountId}/funding` returns the accepted current-Go
`FundingPage` with the full exact `FundingView` and required broker
`accountLogin`. Broker HMAC authentication dominates exact
`accounts:read`/wildcard scope, which dominates canonical account-URN and
funding-page parsing, which dominates storage access. `Principal.Tenant`,
never the API-key subject, is tenant authority.

One unnamed custom-plan PostgreSQL statement materializes matching
`identity.user_accounts` and `identity.account_profiles` authority before
calling the funding reader. It returns the ordered page and optional
cursorless total from one MVCC snapshot. Absent, foreign, partial, and
conflicting authority all return the same generic `403`; no denied account
invokes or decodes the funding function. Empty authorized pages preserve
`items: []` and first-page `total: 0`.

The newest-first `(logical_time, funding_id)` cursor remains a moving committed
history position, not a replay snapshot. Each settlement carries immutable
receipt-derived instrument revision, price-scale, and quantity-scale
provenance. The reader reconstructs the exact historical `InstrumentRevision`
and validates quantity and oracle price through its step and tick rules; it
does not reinterpret old rows through the instrument's current revision.
Every row is buffered before HTTP output. Missing or mismatched provenance,
off-step quantity, off-tick or non-positive price, non-finite values, invalid
currency scale, a late corrupt row, or a stream failure rejects the whole
response as opaque `503`, without identifiers, cursors, totals, or a valid
prefix. This applies equally to first, forward-cursor, and backward-cursor
pages. The read performs no durable write, acknowledgment, outbox publication,
or realtime effect.

### Admin permission catalog foundation

The internal `GET /admin/v1/permissions` composition preserves the accepted
current-Go response fields and ordered catalog values: eleven resources and
four actions, each represented by lowercase `id` and `label`. Authentication
requires the separately configured current-Go HMAC admin audience and a
canonical admin subject. Its key must be distinct from the client-token key and
an absent admin key leaves the route uncomposed. Durable PostgreSQL
`roles/read`, not token role
claims, authorizes the read; missing credentials, denial, and unavailable
authority fail as `401`, `403`, and `503` before catalog data is returned.

This does not yet freeze or activate an external admin contract. The imported
OpenAPI entry remains source-route inventory because the source does not
currently determine additional query, body, method, trailing-slash, `Allow`
header, or JSON object-member-order behavior. The production runtime injects
none of the admin catalog dependencies until the separately reviewed
first-admin/bootstrap boundary exists.

## 5. gRPC compatibility

Preserve:

- proto package names;
- service and method names;
- field numbers and wire types;
- enum numeric values;
- optional/presence semantics;
- status codes and details;
- decimal representation;
- deadlines and cancellation behavior required by tests.

Never reuse a removed protobuf field number.

## 6. Realtime compatibility

Preserve as established by tests:

- token issuer/subject/audience and expiry semantics;
- channel names and authorization;
- event envelope shape;
- event type names;
- payload field names and decimal formats;
- aggregate/channel sequence behavior;
- initial snapshot and reconnect behavior.

Internal delivery changes from Redis/RabbitMQ to NATS are invisible to clients.

Every new implementation event includes stable `eventId` and sequence information. If the historical surface omitted those fields, introduce them only in an additive compatible location or keep them internal until a versioned contract allows exposure.

## 7. CLI and deployment compatibility

Where existing deployment tooling depends on it, retain:

```text
app serve
app worker --handlers=<role>
app migrate
app doctor
nautilus
```

The Go binaries may route these commands to a new implementation. Environment variable names, ports and health endpoints required by deployment compatibility are frozen in tests/manifest.

Implemented worker handlers include `outbox-publisher`,
`realtime-publisher`, and `event-consumer` (including its compatible pattern
form). The realtime publisher consumes only committed PostgreSQL publications
and retries with the same event and channel sequence.

## 8. Compatibility manifest

Create `contracts/compatibility-manifest.json` containing:

```text
platform source revision
OpenAPI artifact hashes
protobuf descriptor hash
realtime fixture hash
supported role commands
environment key list
intentional deviations
```

CI verifies artifact hashes and contract tests.

## 9. Intentional deviations

A deviation requires:

- a test demonstrating old and desired behavior from source assertions/documents;
- safety/business rationale;
- migration and client impact;
- ADR or `ports/decisions/` record;
- owner approval.

Do not label an unimplemented behavior as an intentional deviation.

## 10. Contract-test approach

- HTTP: `httptest` plus full-process loopback tests.
- gRPC: `bufconn` plus loopback tests.
- Realtime: token/envelope unit tests and Centrifugo integration tests.
- Golden wire fixtures are reviewed and generated from the specification, not from executing the old system.
- Fuzz request decoders and invalid enums/precision.
