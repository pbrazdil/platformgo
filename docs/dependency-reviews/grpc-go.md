# Dependency review: `google.golang.org/grpc`

Version: `v1.81.1`

Problem not solved adequately by standard library/current dependencies:
The Phase 3 charter requires a real gRPC surface, status mapping, cancellation,
loopback tests, and `bufconn` tests. The standard library does not implement
gRPC over HTTP/2.

API surface used:

- `grpc.Server`, generated unary registration, and generated clients;
- metadata, canonical status codes, and status errors;
- insecure credentials only in local in-memory tests;
- `bufconn` only in tests.

Maintenance/security posture:
Official Go implementation maintained by the gRPC project. The version was the
latest stable grpc-go release when reviewed on 2026-07-25. CI runs
`govulncheck`.

License:
Apache-2.0.

Transitive dependency impact:
Adds the gRPC transport and Google RPC status modules plus their networking
support. `go mod tidy` and the direct-dependency allowlist remain enforced.

Determinism impact:
None in the deterministic engine. gRPC exists only at the edge and passes
explicit request data into the application command boundary.

Money-path impact:
No arithmetic or economic rules. The service shares the same durable command
submission path as REST and maps transport errors only.

Removal strategy:
Remove the gRPC server, generated stubs, protobuf contract, and direct module if
the external gRPC contract is retired through a versioned compatibility change.
