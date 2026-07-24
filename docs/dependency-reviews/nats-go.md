# `github.com/nats-io/nats.go` dependency review

Problem not solved adequately by standard library/current dependencies:
The standard library does not implement the NATS protocol, JetStream durable
publish acknowledgments, pull consumers, stream management, or NATS message-ID
deduplication.

API surface used:
`jetstream.New`, `JetStream.PublishMsg`, `Nats-Msg-Id`,
`CreateOrUpdateStream`, `CreateOrUpdateConsumer`, bounded `Fetch`, message
metadata, and `DoubleAck`.

Maintenance/security posture:
Official NATS Go client maintained by the NATS project. Dependency and
vulnerability checks remain mandatory release gates.

License:
Apache-2.0.

Transitive dependency impact:
Adds `nkeys`, `nuid`, `klauspost/compress`, and the required Go `x/*` modules.

Determinism impact:
None inside the deterministic engine. The client is confined to the NATS
infrastructure adapter.

Money-path impact:
JetStream provides at-least-once transport only. PostgreSQL receipts, inbox
records, and outbox rows remain authoritative. Consumer acknowledgment follows
the committed PostgreSQL effect.

Removal strategy:
Replace the infrastructure adapter behind the PostgreSQL durable-publisher and
committed-handler boundaries; no engine or domain type depends on NATS.
