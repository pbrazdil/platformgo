# Dependency Policy

## 1. Goal

Keep the runtime dependency graph small, explicit and auditable. Minimal dependencies means fewer hidden semantics and smaller supply-chain risk; it does not justify reimplementing cryptography or database protocols.

## 2. Baseline choices

Expected foundational modules:

- PostgreSQL: `github.com/jackc/pgx/v5`
- NATS: `github.com/nats-io/nats.go`
- exact decimal: `github.com/cockroachdb/apd/v3`
- UUID compatibility: `github.com/google/uuid`
- gRPC/protobuf where required: `google.golang.org/grpc`, `google.golang.org/protobuf`
- reviewed standard extensions where needed: `golang.org/x/crypto`, `golang.org/x/sync`
- observability only when implemented: OpenTelemetry and/or Prometheus modules listed in the allowlist

The authoritative allowlist is `policy/allowed-direct-modules.txt` and is checked in CI.

## 3. Default to standard library

Prefer:

- `net/http`;
- `encoding/json`;
- `log/slog`;
- `context`;
- `crypto/*`;
- `testing`;
- standard profiling and diagnostics.

## 4. Disallowed without ADR

- web frameworks;
- ORMs and SQL builders;
- dependency-injection frameworks;
- workflow/saga frameworks;
- event-sourcing frameworks;
- generic repository frameworks;
- alternate brokers/databases;
- alternate decimal libraries;
- assertion frameworks;
- background-job frameworks;
- reflection-heavy validation frameworks;
- libraries that hide retries or transactions.

## 5. Adding a dependency

A PR adding a direct module must state:

```text
Problem not solved adequately by standard library/current dependencies:
API surface used:
Maintenance/security posture:
License:
Transitive dependency impact:
Determinism impact:
Money-path impact:
Removal strategy:
```

The dependency is then added to the allowlist and, for material architecture, an ADR.

## 6. Reproducibility and supply chain

- Pin exact versions in `go.mod`/`go.sum`.
- No unreviewed `replace` directives.
- CI runs `go mod tidy` consistency and `govulncheck`.
- Release builds use `-trimpath` and include VCS metadata.
- Produce an SBOM and sign release artifacts before production.
- Container base images are digest-pinned in production deployment manifests.
