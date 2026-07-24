# Domain Glossary

- **Authoritative state** — PostgreSQL state whose mutation is owned by the engine and ledger transaction.
- **Business effect** — a monetary or lifecycle consequence such as a fill, ledger entry, order transition or balance change.
- **Command** — an idempotent requested mutation with a stable identity and account sequence.
- **Decision** — the pure engine output for one state and one ordered input.
- **Engine input** — command, market event, timer, configuration change or operator control applied in shard stream order.
- **Engine shard** — disjoint account ownership domain with one input stream and one active writer.
- **Input receipt** — durable record proving an engine input has already committed and identifying its result hashes.
- **Logical time** — explicit business time supplied in the input; not process wall clock.
- **Market epoch** — connection/resynchronization generation used to distinguish source sequence resets.
- **Outbox** — PostgreSQL records committed with business state and published asynchronously.
- **Inbox** — PostgreSQL deduplication record committed with a consumer side effect.
- **Projection** — rebuildable read or realtime representation derived from authoritative events/state.
- **Realtime sequence** — monotonic sequence allowing a client to detect duplicates and gaps.
- **Source sequence** — sequence assigned by an input source before fanout.
- **Stream sequence** — JetStream-assigned total order within one engine shard input stream.
