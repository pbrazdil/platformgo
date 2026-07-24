# NATS and JetStream adapter instructions

NATS is transport, not monetary authority.

- Use Core NATS only where loss is explicitly acceptable.
- Durable commands, engine inputs, domain events, and jobs use JetStream.
- Use stable message IDs and versioned envelopes.
- Durable consumers use explicit acknowledgment; money-path acknowledgment follows the committed PostgreSQL transaction.
- Treat redelivery as normal and prove exactly-once business effects through PostgreSQL receipts/inbox records.
- Pull consumers are the default for durable work unless an ADR says otherwise.
- Engine-shard inputs are processed one at a time in stream order.
- Publish waits for JetStream acknowledgment where durability is required.
- Stream limits and retention must not silently discard unprocessed money-path data.
- Tests cover duplicate publish, duplicate consume, lost acknowledgment, reconnect, consumer recreation, poison messages, and backpressure.

Read `MESSAGING.md` and `ARCHITECTURE.md`.
