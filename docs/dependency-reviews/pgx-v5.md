# `github.com/jackc/pgx/v5` dependency review

Problem not solved adequately by standard library/current dependencies:
The standard library defines `database/sql` but does not implement the
PostgreSQL wire protocol, PostgreSQL-native types, connection pooling,
transaction options, advisory-lock queries, or structured PostgreSQL errors.
The project requires those boundaries without an ORM or SQL builder.

API surface used:
`pgxpool.New`, `pgxpool.Pool`, dedicated pooled connections, `pgx.Tx`,
`pgx.TxOptions`, explicit `Exec`, `Query`, and `QueryRow` calls,
`pgx.ErrNoRows`, `pgconn.PgError`, and PostgreSQL transaction/error metadata.

Maintenance/security posture:
Actively maintained PostgreSQL driver in the established `jackc/pgx` project.
The repository pins v5.10.0 and retains module-consistency and vulnerability
checks as mandatory release gates.

License:
MIT.

Transitive dependency impact:
Adds `pgpassfile`, `pgservicefile`, `puddle/v2`, and required Go `x/*`
modules. No ORM, reflection mapper, retry framework, or alternate database
abstraction is introduced.

Determinism impact:
None inside the deterministic engine. PostgreSQL access is confined to the
infrastructure adapter. Database scheduling is not used to establish economic
order; the durable single-shard binding, shard ownership capability, explicit
input sequence, and transaction checks establish authority.

Money-path impact:
Material and intentional. `pgx` carries exact decimal strings and integer
logical times into PostgreSQL and owns the atomic transaction containing
receipts, commands, fills, ledger entries, projections, checkpoints, and
outbox rows. Application-controlled transactions and stable business keys
remain responsible for idempotency and exactly-once business effects.

Removal strategy:
Replace the PostgreSQL adapter implementation behind its application-facing
boundaries. Domain and deterministic engine packages do not import `pgx`, so a
driver replacement does not change economic types or engine behavior.
