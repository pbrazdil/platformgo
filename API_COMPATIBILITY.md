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
