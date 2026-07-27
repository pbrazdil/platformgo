# Dependency review: `golang.org/x/crypto`

Version: `v0.50.0`

Problem not solved adequately by standard library/current dependencies:
The password-login compatibility surface requires a reviewed password hashing
algorithm. The Go standard library does not provide Argon2id.

API surface used:

- `argon2.IDKey` for versioned Argon2id password hashes.

Maintenance/security posture:
Official Go extended cryptography module maintained by the Go project. Hash
parameters are fixed, encoded with the stored hash, and verified in constant
time. The `v0.50.0` selection is required by `x/net v0.53.0` in the grpc-go
security update; the imported Argon2 and BLAKE2b implementations are unchanged
from `v0.49.0`. CI runs `govulncheck`.

License:
BSD-3-Clause.

Transitive dependency impact:
The module was already present transitively through `pgx`; Phase 3 makes it a
direct, allowlisted dependency because production identity code imports it.
The version update also selects `x/sys v0.43.0`; it introduces no new module.

Determinism impact:
None in the deterministic engine. Password salts use adapter-side
cryptographic entropy and identity tests inject deterministic bytes.

Money-path impact:
None. The module is used only for password verification at the authentication
edge.

Removal strategy:
Remove the Argon2id implementation and direct dependency only after migrating
all stored password hashes to another reviewed scheme through a versioned
identity migration.
