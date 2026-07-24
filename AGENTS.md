# AGENTS.md

## Mission

Build a clean-room Go replacement for the existing brokerage platform with the same externally observable API behavior and a smaller runtime stack:

- Go
- PostgreSQL
- NATS with JetStream
- Centrifugo
- Hyperliquid first

This system processes money. Correctness, determinism, idempotency, auditability, and recoverability outrank delivery speed, convenience, abstraction purity, and feature breadth.

The Rust platform and Nautilus runtime are never executed in development or CI. Their pinned test source is ported into native Go tests. Accepted Go tests become the maintained executable specification.

## Agent runtime

All primary agents and subagents use the exact model slug `gpt-5.6-sol`. Do not use the `gpt-5.6` alias or another GPT-5.6 variant. Project defaults and named agents are in `.codex/`; the machine-checkable policy is enforced by `scripts/check-agent-runtime.py`.

Read `MODEL_POLICY.md` before changing agent instructions, prompts, `.codex` configuration, or multi-agent workflows.
Use `docs/AGENT_TASK_TEMPLATE.md` for assignments and `docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md` for independent high-risk review. Add only task-specific goal, scope, inputs, evidence, success criteria, validation, and deliverables instead of repeating standing rules.

## Instruction scope

Codex loads this file at the repository root. More specific `AGENTS.md` files apply inside subsystem directories and may add stricter constraints. When instructions conflict, the closer file wins unless it would weaken an economic invariant.

## Project progress

Maintain the root `README.md` as the concise, current source of truth for delivery progress. After any material change that advances, completes, blocks, or reopens a phase in `PROJECT_CHARTER.md`, update the README's current stage, completed work, remaining work, known blockers, validation state, and last-updated date.

Base every status claim on live repository evidence. Do not describe deterministic fixtures or ported tests as production PostgreSQL, NATS, Centrifugo, Hyperliquid, API, or deployment integration. Mark a phase complete only when its charter deliverables exist and the required checks pass.

Read only the documents needed for the task:

| Work | Required documents |
|---|---|
| Any code change | `PROJECT_CHARTER.md`, `INVARIANTS.md`, relevant nested `AGENTS.md` |
| Architecture or boundaries | `ARCHITECTURE.md`, applicable ADRs |
| Money or numeric logic | `DECIMAL.md`, `INVARIANTS.md` |
| PostgreSQL or migrations | `DATABASE.md`, `migrations/AGENTS.md` |
| NATS or delivery | `MESSAGING.md`, adapter `AGENTS.md` |
| HTTP/gRPC/realtime compatibility | `API_COMPATIBILITY.md` |
| Test porting | `TESTING.md`, `docs/TEST_PORTING_PLAYBOOK.md`, `ports/SOURCE_REVISIONS.md` |
| Operations or release | `OPERATIONS.md`, `RECONCILIATION.md`, `SECURITY.md` |
| Dependency changes | `DEPENDENCIES.md` |

## Authority

Use this order when deciding behavior:

1. Economic and safety invariants in `INVARIANTS.md`.
2. Accepted native Go tests.
3. Explicit assertions in tests at the pinned source revisions.
4. Frozen external contract artifacts and contract tests.
5. Accepted ADRs.
6. Architecture and subsystem documentation.
7. Implementation code.

Do not silently resolve a conflict between normative sources. Record it under `ports/decisions/`, mark affected rows in `ports/test-port-map.csv` as `conflict`, and request an owner decision.

Changing an accepted economic behavior requires a separate ADR, updated tests, a compatibility impact statement, and owner approval. Governance or invariant changes must not be mixed with the implementation change that benefits from weakening them.

## Autonomy and approval boundary

- For review, diagnosis, explanation, or planning: inspect relevant materials and report findings. Do not edit unless the request includes a change.
- For build, fix, or port requests: make in-scope local changes and run relevant non-destructive checks without asking first.
- Ask before external writes, production actions, destructive commands, secret access, purchases, force pushes, database resets, migration-history changes, or material scope expansion.
- Do not ask about ordinary implementation details when the tests, invariants, and architecture determine the answer.
- Stop for a material ambiguity only when choosing incorrectly could alter money, ordering, idempotency, compatibility, security, or irreversible data.

## Non-negotiable invariants

### Deterministic execution

Given identical persisted state, ordered inputs, market events, logical time, configuration and instrument revisions, deterministic IDs, and code version, the engine must produce identical decisions, events, ledger entries, state, and canonical hashes.

The deterministic core must not depend on wall time, sleeps, randomness, environment variables, network calls, database timing, goroutine scheduling, map iteration order, or process-global mutable state. Time, IDs, sequence, configuration, and market data are explicit inputs.

### Exact money

`float32` and `float64` are forbidden for any economic value. Parse exact strings or typed integer units. Every rounding point names its rule. Amounts carry currency; quantities carry instrument. Canonical output uses plain decimal notation and normalizes negative zero to zero.

### Single writer and explicit order

Exactly one engine writer mutates each shard. One goroutine owns mutable shard state and applies inputs serially. The shard input sequence is explicit and durable. A second active writer is a fatal configuration error.

### Exactly-once business effects

Networks and brokers may duplicate delivery; fills, ledger entries, balances, positions, orders, jobs, and client events must not duplicate. Every retry uses a stable business key. Acknowledgment follows the committed durable transaction. Never claim network-level exactly once.

### PostgreSQL authority

PostgreSQL is authoritative for monetary and audit state. NATS and Centrifugo are delivery systems. One economic decision commits its command receipt, fills, ledger entries, state changes, checkpoints, and outbox records atomically. Historical facts are append-only; corrections use compensating actions.

### Fail closed

Missing or stale market data, unknown schemas, sequence gaps, impossible transitions, inconsistent state, or uncertain authority must block risk-increasing behavior and make readiness false. Do not invent prices, default corrupted values, or skip bad money-path inputs.

### Immutable migrations

Applied migrations are immutable. Every correction is a new forward migration. No production down migrations, squashing, renaming, reordering, or editing applied files.

### External compatibility

Preserve the tested HTTP, JSON, gRPC, authentication, idempotency, realtime, health, CLI, and deployment contract. Internal package layout and database schema are not compatibility contracts.

## Dependency direction

```text
cmd / edge / adapters
        ↓
application orchestration
        ↓
deterministic engine and domain
```

`internal/domain/**` and `internal/engine/**` must not import PostgreSQL, NATS, Centrifugo, HTTP, environment access, direct logging control flow, wall-clock APIs, randomness, or infrastructure-specific types. Interfaces belong with their consumers. Do not build generic abstractions for future markets before a second market requires them.

## Test-driven workflow

For each vertical slice:

1. Read the complete source-test context and relevant invariants.
2. Port or write a native Go test that expresses the observable requirement.
3. Add source provenance and update `ports/test-port-map.csv` when porting.
4. Confirm the test fails for the missing behavior rather than missing test plumbing.
5. Implement the smallest deterministic behavior.
6. Add integration or contract tests only at crossed boundaries.
7. Refactor after green without changing semantics.

Tests must not run the old Rust system, depend on live market data for economics, use sleeps in model tests, weaken assertions, hide work behind permanent skips, or use `t.Parallel()` before harness approval.

Preferred history:

```text
test: port <behavior> from pinned source
feat: implement <behavior>
refactor: simplify <behavior> without semantic change
```

## Multi-agent work

Use subagents only when work divides into independent, non-overlapping units. Prefer them for exploration, test inventory, review, and isolated source modules. Avoid parallel edits to shared harness, migrations, decimal primitives, or the same package.

The primary agent owns task decomposition, file ownership, conflict resolution, integration, and final validation. Every subagent must use `gpt-5.6-sol`; named profiles in `.codex/agents/` pin this explicitly.

## Editing discipline

- Keep the change vertically narrow.
- Do not perform unrelated cleanup.
- Do not weaken policy, tests, checks, or assertions to make a patch pass.
- Do not edit shared testkit files without the harness owner.
- Do not hold database transactions across network calls.
- Every goroutine has an owner, cancellation path, bounded queue, and shutdown coverage.
- Every error in a money path is handled or returned; expected rejection is typed, not a panic.
- Retries are bounded, classified, observable, and safe under idempotency.

## Validation

Run the smallest relevant tests while developing. Before declaring completion, run:

```bash
make policy
make fmt-check
make lint
make test
make test-race
make test-repeat
```

Also run the relevant PostgreSQL, NATS, Centrifugo, compatibility, migration, or recovery targets when those boundaries changed. Do not claim a check passed unless it was executed successfully in the current working tree.

## Stop conditions

Stop and request an explicit decision when:

- normative tests conflict;
- an invariant would be violated;
- a money calculation lacks a rounding rule;
- writer ownership or ordering is unclear;
- a retry can duplicate an economic effect;
- an applied migration would need modification;
- compatibility requires an intentional break;
- a venue gap would require guessing data;
- a dependency is outside the allowlist without approval;
- the source test asserts only an implementation detail and no observable requirement can be identified.

Do not hide these with a TODO, skip, tolerance, fallback value, or best-effort behavior.

## Completion report

Report only material information:

```text
Behavior implemented:
Source tests ported:
Invariants affected:
Transaction and idempotency boundary:
Ordering boundary:
Failure/restart coverage:
Compatibility impact:
Migrations/dependencies:
Validation run:
Known limitations or conflicts:
```

A behavior is complete only when its tests, deterministic repeats, race checks, duplicate-delivery behavior, restart boundaries, transaction/locking behavior, compatibility checks, migrations, observability, and resource cleanup are satisfactory for the affected scope.
