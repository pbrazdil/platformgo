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
Use `docs/AGENT_TASK_TEMPLATE.md` for assignments,
`docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md` for high-risk preflight, and
`docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md` for independent high-risk review.
Add only task-specific facts instead of repeating standing rules.

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
2. Semantically accepted native Go tests (`review_status=reviewed` in the port
   ledger; wiring and evidence state still determine what implementation
   boundary they prove).
3. Explicit assertions in tests at the pinned source revisions.
4. Frozen external contract artifacts and contract tests.
5. Accepted ADRs.
6. Architecture and subsystem documentation.
7. Implementation code.

Do not silently resolve a conflict between normative sources. Record it under `ports/decisions/`, mark affected rows in `ports/test-port-map.csv` as `conflict`, and request an owner decision.

Faithful updates to implementation companion documentation, including
`DATABASE.md` and `OPERATIONS.md`, belong atomically with the implementation
they describe. This does not authorize changing an accepted economic behavior:
that still requires a separate ADR, updated tests, a compatibility impact
statement, and owner approval. Protected governance or invariant changes must
not be mixed with the implementation change that benefits from weakening them.

## Autonomy and authority boundary

- For review, diagnosis, explanation, or planning: inspect relevant materials and report findings. Do not edit unless the request includes a change.
- For build, fix, or port requests: make in-scope local changes and run relevant non-destructive checks without asking first.
- Project Codex runs with `approval_policy = "never"` and `sandbox_mode = "danger-full-access"`. Do not request runtime command or sandbox approvals, and do not weaken or override those settings.
- Full runtime access does not broaden task authority. External writes, production actions, destructive commands, secret access, purchases, force pushes, database resets, migration-history changes, or material scope expansion require explicit user direction or standing authorization in the active task.
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

A migration's path and bytes freeze when it first reaches a protected branch or
is applied to a shared or persistent database, whichever happens first.
Application only to an explicitly disposable local/test database does not
freeze an unpublished, unshared candidate. Before freeze that candidate may be
edited, renamed, reordered, deleted, or squashed. After freeze it is immutable;
every correction is a new forward migration. Production down migrations are
forbidden.

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

## Risk-first, test-driven workflow

Sequence:

```text
scope
→ parallel adversarial preflight when high-risk
→ failing tests
→ implementation plus advisory review
→ focused green
→ advisory blocker closure
→ one full local validation pass on a stable candidate
→ exact SHA specialist and final release review
→ push and hosted CI on that SHA
→ merge
```

Preflight is required for money, ledger, fills, margin,
funding, balances, durable PostgreSQL state, migrations, ordering,
single-writer ownership, concurrency, idempotency, duplicate delivery,
acknowledgment, restart, recovery, reconciliation, rollback, HTTP/gRPC/realtime
compatibility, authentication, authorization, ACL/security, or production
lifecycle, readiness, and shutdown. Use
`docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md`; applicable migration,
determinism, and money reviewers inspect in parallel before implementation.
docs-only, mechanical, and isolated test-only work
does not require preflight.

Checkpoints are **Scope, design, and failure matrix**; **Failing tests**;
**Implementation boundary** before docs/full suite; and
**Exact-SHA release candidate**. Advisory review inspects working diffs and
cannot grant release approval; blockers are immediate. Originating specialists
close correction deltas. The independent final reviewer audits the
candidate once, read-only.

P0/P1 findings block publish and merge and require the evidence in the critical
review template. P2/P3 findings are non-blocking only outside the protected-risk
boundary; a real money, durable-state, ordering, idempotency, compatibility,
security, or recoverability defect is P1.

Read complete source-test context, prove the representative failing test,
record provenance, implement minimally, and refactor only after green.

Tests must not run the old Rust system, depend on live market data for economics, use sleeps in model tests, weaken assertions, hide work behind permanent skips, or use `t.Parallel()` before harness approval.

## Multi-agent work

Independent work clearing its predecessor checkpoint may parallelize
design, tests, implementation, and contract/docs with non-overlapping
ownership. The primary owns decomposition, conflicts, integration, candidate,
and validation. Shared authority files have one editor; specialists are
read-only. Every subagent uses `gpt-5.6-sol`.

Prefer safe `main` layers: schema/upgrade proof, store semantics, runtime
activation, and source-port acceptance. Never create an unsafe intermediate
state. Above 2,000–3,000 added lines or multiple authority boundaries, justify
why safe separation is impossible.

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

Run the smallest test, affected package, boundary suite, policy, and
format check during implementation. After advisory blockers close, stabilize a
clean candidate commit and run one full local validation pass:

```bash
make policy
make fmt-check
make lint
make test
make test-race
make test-repeat
```

Run PostgreSQL, NATS, Centrifugo, compatibility, migration, recovery,
and vulnerability gates. Record full candidate/tree/base SHAs. Validation,
closure, and GO apply only to that tree and never transfer. A changed tree
invalidates evidence and repeats affected gates. Hosted CI tests the exact SHA;
base drift invalidates approval. A final-review design blocker becomes a
preflight failure and checklist update.

## Stop conditions

Stop and request an explicit decision when:

- normative tests conflict;
- an invariant would be violated;
- a money calculation lacks a rounding rule;
- writer ownership or ordering is unclear;
- a retry can duplicate an economic effect;
- a frozen migration would need modification;
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
