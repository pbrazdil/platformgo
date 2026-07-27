# Dependency review: `google.golang.org/grpc`

Version: `v1.82.1`

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
Official Go implementation maintained by the gRPC project. Version `v1.82.1`
fixes `GO-2026-6061`, including the reachable HTTP/2 control-frame flood in the
server transport. The runtime does not configure xDS, but the same patch also
closes the advisory's xDS RBAC fail-open and panic cases. CI runs
`govulncheck`.

License:
Apache-2.0.

Transitive dependency impact:
The patch update advances the required `x/net` to `v0.53.0`, `x/sys` to
`v0.43.0`, and Google RPC status module to
`v0.0.0-20260414002931-afd174a4e478`. `x/net` requires
`x/crypto v0.50.0`. No new module is introduced; `go mod tidy` and the direct
dependency allowlist remain enforced.

Determinism impact:
None in the deterministic engine. gRPC exists only at the edge and passes
explicit request data into the application command boundary. The transport
frame throttle cannot become a business clock, ID, sequence, or ordering
authority.

Money-path impact:
No arithmetic or economic rules. The service shares the same durable command
submission path as REST and maps transport errors only.

Removal strategy:
Remove the gRPC server, generated stubs, protobuf contract, and direct module if
the external gRPC contract is retired through a versioned compatibility change.
