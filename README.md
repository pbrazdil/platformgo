# platformgo

Clean-room Go replacement for `upcomers-org/platform`, using pinned source tests as the executable specification. The intended production stack is Go, PostgreSQL 17 or newer, NATS with JetStream, Centrifugo, and Hyperliquid first.

## Current status

Last updated: 2026-07-25

Current delivery stage: **Phase 2 — durable execution release hardening**.

The pinned source inventory is complete: all 2,748 tests are recorded in
`ports/test-port-map.csv`. Forty-one source tests are independently reviewed
and green against production numeric and pure-engine boundaries, 2,610 native
Go representations remain `unreviewed/placeholder/spec-fixture`, and 97
implementation-only tests are reviewed and excluded with decision records.
Mechanical porting is not conflated with semantic acceptance or production
implementation.

The Phase 0 deterministic kernel supplies canonical IDs and logical time,
versioned input envelopes, strict shard-stream sequencing, recorded duplicate
results, fail-closed readiness, canonical decision/state hashes, and a
synchronous fixture with manual time and IDs. Its replay and duplicate
properties are policy-native evidence derived from the repository invariants;
they are not presented as reviewed ports of unrelated source tests.

Phase 1 supplies deterministic market, limit, stop, and favorable-touch order
execution; exact depth-weighted and deepest-level B-book pricing; admission
slippage limits; GTC, IOC, and FOK behavior; amend and cancel; cumulative fills;
and immutable economic decisions. It maintains account-isolated netting and
hedging positions, exact realized PnL, execution-time reduce-only clamping,
margin reservation, business-key-idempotent funding, cross and isolated
liquidation, and fail-closed stale-mark handling.

Atomic brackets provide held OTO activation, reduce-only OCO cleanup, ladder
resizing, hedged-position isolation, and protection cleanup after external
closes or reversals. Explicit maker/taker schedules support signed maker
rebates, one-boundary half-even rounding, fee-aware margin reservation, exact
fill attribution, atomic in-memory balance settlement, and duplicate-delivery
protection. Thirty-six pinned Phase 1 rows are reviewed, green, and wired to
this `model-real` boundary; five numeric rows provide the underlying
`unit-real` exact-value evidence.

Phase 2 hardens and persists the execution contracts. Economic
state now retains only its bounded latest receipt while an explicit O(1)
receipt index resolves historical identity before current-schema validation.
Versioned decision hashes bind previous state, input, and canonical effects;
fixed vectors pin the audit protocol. Canonical payloads are opaque values
created by deterministic encoders, production-economic dependency direction
is enforced, and the shared deterministic testkit now provides shard-scoped
IDs, manual time, receipt-aware engine execution, semantic failpoints, and
canonical hash extraction.

Forward-only checksum-verified PostgreSQL migrations create exact durable
engine, trading, ledger, market, messaging, command, idempotency, outbox, inbox,
and runtime-role boundaries. One engine transaction commits normalized state,
balanced core-authored ledger effects, command result, immutable receipt,
checkpoint, and versioned domain-event outbox. Recovery replays canonical
inputs and terminal faults, verifies every hash, and reconciliation detects
projection corruption without repair.

Phase 2 is not yet complete. The owner-approved pre-release migration rewrite
now provides one final checksum-verified baseline, populated PostgreSQL 17
fixtures, stale-development-history refusal, runtime-role membership
preflight, immutable durable account-to-shard ownership, and transaction-level
ownership epochs. The privileged one-shot migrator explicitly provisions the
documented initial shard before traffic; API and engine roles only validate
the immutable PostgreSQL binding, so request or database scheduling cannot
choose deployment authority and a second shard fails before it can become a
writer for globally keyed instrument or book projections. Engine logical
times persist as exact Unix nanoseconds rather than lossy PostgreSQL
timestamps. Every engine-input and domain-event publication now requires
database-established producer authority plus an explicit receipt foreign key;
command claims also validate the complete redundant envelope, and missing
predecessor outbox rows block later account commands. Unbound or receipt-rebound
events cannot receive a publication claim. Undecodable transport
poison remains durably halted across restart, and reconciliation acquires the
same lifetime shard ownership capability as execution. Exact
receipt-to-projection comparison covers configuration, books, balances,
orders, fills, positions, commands including outbox producer class, funding,
all ledger facts, complete pending command journal tuples, and receipt-bound
domain-event outbox rows without repairing evidence. Destructive PostgreSQL
test reset requires an explicit
opt-in and a verified disposable database name. Full PostgreSQL 17 and
JetStream integration suites and every repository-wide local release gate pass
on the current working tree; hosted validation and renewed independent approval
remain.

JetStream uses bounded file-backed streams with `DiscardNew`, one physical
input stream and lifetime PostgreSQL ownership lock per shard, stable
`Nats-Msg-Id` publication, pull delivery with one unacknowledged input, and
synchronous acknowledgment only after the PostgreSQL handler commits.
The live integration suite injects a failure after commit and before
acknowledgment, restarts the processor, and proves same-sequence redelivery is
a receipt-backed no-op before the acknowledgment floor advances.
Beyond-window duplicate publication advances a separate immutable delivery
receipt and state chain without repeating ledger, fill, balance, position, or
event effects.

This repository is not yet a production-capable replacement. It has no
executable `cmd` services, REST/gRPC/authentication compatibility edge,
Centrifugo adapter, or production Hyperliquid adapter. Phase 2 runtime
boundaries are production packages with live PostgreSQL and JetStream
integration proof; deployment packaging and later phase contracts remain.

## Delivery progress

| Phase | Status | Evidence and remaining work |
|---|---|---|
| 0 — Policy and test harness | Complete | Machine-readable package scope, AST policy checks, split port/review/wiring evidence, exact function provenance, canonical source authorities, pinned Go 1.26.5, CODEOWNERS, immutable CI actions, complete-port and tidy gates, and the initial agent-evaluation corpus exist. `main` is protected and all seven required checks are enforced. The numeric foundation provides the sole `apd/v3`-backed production decimal, strict canonical parsing, explicit one-boundary rounding, immutable unit-bearing values, parser/arithmetic fuzzing, and five reviewed green source rows. The deterministic kernel adds explicit logical time and IDs, strict input sequencing, idempotent duplicate receipts, fail-closed typed errors, canonical decision/state hashes, replay properties, and the minimal synchronous engine fixture. |
| 1 — Pure engine | Complete | Thirty-six pinned source rows are reviewed and green against the production `model-real` engine boundary. Coverage includes deterministic order lifecycle, depth/VWAP and slippage, netting and hedging positions, exact PnL, margin and reservation, idempotent funding, cross/isolated liquidation, stop/touch triggers, brackets and ladders, protection cleanup, and exact maker/taker fees. Policy-native invariant, fuzz, duplicate, replay, repeated, and race-enabled tests reinforce the source-test evidence. |
| 2 — Durable execution | In progress | Durable execution, complete command-envelope binding, contiguous account sequencing, explicit migrator provisioning of an immutable initial single-shard deployment binding with session and epoch fencing, exact nanosecond logical-time persistence, receipt-bound producer-authorized engine input and domain-event publication, restart-stable poison faults, exact global fail-closed reconciliation under lifetime shard ownership, guarded disposable-database test reset, one clean pre-release PostgreSQL baseline, populated/stale-history migration fixtures, live PostgreSQL/JetStream proof including commit-before-ack loss, and hosted PostgreSQL 17/NATS CI execution are implemented. Full live and repository-wide local release gates pass on the latest working tree; renewed hosted gates and exact-SHA independent approval remain. |
| 3 — Compatibility edges | Not started | Production REST, gRPC, authentication, realtime/Centrifugo, health, CLI, and deployment-compatible services are not implemented. |
| 4 — Hyperliquid production integration | Not started | No production adapter, reconnect/resynchronization path, controlled live canary, soak test, or incident drill exists. |
| 5 — Replacement rehearsal | Not started | Data import, cutover, rollback, reconciliation, and audited go-live rehearsal remain. |

## Validation snapshot

Verified on 2026-07-25:

- `make policy`, `make fmt-check`, `make lint`, serial `make test`, serial
  `make test-race`, `make test-repeat`, and `make vuln` pass on the current
  corrected candidate. `go mod tidy -diff` and `git diff --check` are clean.
- `go mod tidy -diff` is clean.
- The strict lint profile covers production, infrastructure, non-economic, and
  tooling classifications. Ported compatibility and placeholder packages
  remain quarantined but still compile, test, vet, and pass AST safety checks.
- All 2,748 ledger rows pass structural validation and exact Go `FuncDecl`
  provenance binding.
- The numeric implementation, deterministic kernel, and all Phase 1
  implementation and evidence changes passed all seven required hosted checks
  before merge.
- The deterministic kernel has focused duplicate, replay, ordering, immutable
  state, canonical hash, manual-time, deterministic-ID, fuzz/property, repeated,
  and race-enabled coverage. It does not claim durable transaction or transport
  behavior.
- All thirty-six Phase 1 rows are reviewed and green against the production
  engine. Their focused source, invariant, fuzz seed, 100-repeat, and
  race-enabled tests pass.
- The complete Phase 1 implementation passes repository-wide tests, race tests,
  deterministic repeats, policy, formatting, strict lint, complete source
  inventory, tidy-diff, and vulnerability checks.
- Phase 2 PostgreSQL tests pass against a temporary PostgreSQL 17 instance and
  cover the clean final baseline, populated idempotent rerun, stale-history
  refusal, immutable/checksum constraints, executable least-privilege roles,
  explicit shard provisioning and runtime validation-only roles, durable
  account-to-shard binding, atomic rollback/retry, normalized execution
  projections, idempotency replay/conflict, outbox unknown outcomes,
  transactional inbox dedupe, pending-journal and receipt-binding corruption,
  unexplained monetary rows, restart/fault replay, and reconciliation.
- Phase 2 JetStream tests pass against a temporary NATS Server 2.14.3 instance
  and cover versioned bounded streams, duplicate publish acknowledgments,
  handler redelivery, command-to-engine persistence, beyond-window duplicate
  publication, poison-message halt, stream-full rejection, singleton shard
  ownership, commit-succeeded/ack-lost same-sequence recovery, and server
  reconnect with retained stream sequence.
- Focused PostgreSQL 17 and JetStream tests cover runtime-role membership
  escalation, forged API engine inputs and domain events, backend-terminated
  shard ownership, live-owner reconciliation exclusion, concurrent
  readiness/state access, malformed transport restart/redelivery, exact
  nanosecond command/fill/ledger/trigger persistence, canonical slippage
  references, second-shard refusal, exact amended order persistence, valid
  rounded multi-fill/closed-position reconciliation, guarded destructive reset,
  unbound engine-event rejection, and durable reconciliation halts for
  corrupted configuration, books, balances, orders, fills, positions,
  commands, protection, funding, ledger facts, and domain events.

## Next milestone

Complete the Phase 2 hosted release gates and obtain final independent release
approval. Phase 3 then begins with production health/readiness and idempotent
command submission over HTTP.

The authoritative scope, phase definitions, and completion criteria are in `PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
