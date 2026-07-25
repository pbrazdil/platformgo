# Dependency review: `google.golang.org/protobuf`

Version: `v1.36.11`

Problem not solved adequately by standard library/current dependencies:
The Phase 3 gRPC contract requires protobuf wire compatibility, descriptors,
field presence, stable field numbers, and generated Go messages.

API surface used:

- generated message implementations;
- protobuf reflection and descriptors required by generated code.

Maintenance/security posture:
Official Go protobuf implementation maintained by the Protocol Buffers
project. The version was the latest stable Go module release found when
reviewed on 2026-07-25. CI runs `govulncheck`.

License:
BSD-3-Clause.

Transitive dependency impact:
Small runtime support used by generated messages. The compiler and generators
are build tools and are not runtime dependencies.

Determinism impact:
The descriptor and generated source are checked in. Deterministic engine state
does not serialize protobuf; the edge converts messages into canonical engine
JSON.

Money-path impact:
Economic decimals remain strings. Protobuf messages do not use floating-point
fields for prices, quantities, rates, or amounts.

Removal strategy:
Remove generated messages, descriptors, `.proto` files, and the module after a
versioned retirement of the gRPC contract.
