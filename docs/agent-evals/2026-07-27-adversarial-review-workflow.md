# Adversarial review workflow evaluation

## Candidate

- Baseline: `origin/main` at
  `f912cbf80607878fea7b426df02e42cb975f3a7f`.
- Candidate: the uncommitted governance-only workflow diff on
  `feat/adversarial-review-workflow`.
- Changed instruction group: risk-first preflight, advisory/exact-SHA review,
  validation ladder, severity evidence, parallel ownership, and the precise
  migration freeze boundary.
- Preserved boundaries: exact money, determinism, single-writer ordering,
  idempotency, PostgreSQL authority, compatibility, security, recovery,
  `gpt-5.6-sol`, never approvals, and full access for editing agents.

## Method and limits

An independent exact-model reviewer performed a static, read-only
baseline/candidate comparison against the unchanged eight fixed fixtures under
`testdata/agent-evals/tasks/`. The comparison used `gpt-5.6-sol` and inspected
each assignment, required outcome, forbidden action, and rubric.

All eight fixture files are byte-for-byte unchanged from `origin/main`. This
was not an empirical execution of their production actions; no fixture
implementation or review artifacts were produced.

## Profile configuration

| Fixtures | Profile | Effort | Context | Mode | Verbosity |
|---|---|---|---|---|---|
| 001, 002, 005 | `implementation` | `high` | `all_turns` | standard | medium |
| 003, 004, 006, 007, 008 | `critical-review` | `xhigh` | `current_turn` | pro | high |

The baseline and candidate use the same profiles and exact model.

## Fixed-corpus results

| Fixture | Baseline | Candidate | Static evidence preserved |
|---|---|---|---|
| 001 exact-decimal port | PASS | PASS | Exact strings, half-even rounding, currency assertion, provenance, ownership, and prohibitions on Rust execution and floats. |
| 002 idempotent command | PASS | PASS | Atomic PostgreSQL transaction, stable business identity, duplicate handling, acknowledgment after commit, and crash replay. |
| 003 migration review | PASS | PASS | Read-only scope, data/lock/rewrite and compatibility analysis, staged backfill, retry/restart, and a forward correction. An unspecified environment is not presumed disposable; uncertain freeze state stops for owner review. |
| 004 determinism review | PASS | PASS | Wall time, scheduling, map order, sequence, shared mutation, dual writers, and stale fencing remain mandatory findings. |
| 005 HTTP contract port | PASS | PASS | Status, decimal scale, nullability, byte-stable replay, conflict mapping, provenance, and exact assigned scope. |
| 006 normative conflict | PASS | PASS | The agent records the conflict and stops; it cannot choose rounding, add tolerance, skip the test, or weaken policy. |
| 007 NATS acknowledgment | PASS | PASS | Stable effect identity, inbox/effect transactionality, acknowledgment after commit, and duplicate-debit crash sequences. |
| 008 realtime gap | PASS | PASS | Stable event identity/sequence, duplicate tolerance, committed publication, PostgreSQL authority, and snapshot recovery. |

Static corpus result: baseline PASS 8/8; candidate PASS 8/8; critical
violations 0.

## Focused workflow regressions

`scripts/test-agent-workflow-policy.py` proves:

- high-risk preflight and low-risk docs/mechanical/test-only exemption;
- all required P0/P1 evidence and no protected-correctness laundering to P2/P3;
- P3 non-blocking treatment without stale evidence after an implemented edit;
- advisory working-diff review, delta closure, and no advisory release approval;
- one stable full validation pass before exact-SHA specialist and independent
  final review, followed by push and hosted CI on that head SHA;
- evidence non-transfer after any tree change;
- protected/shared migration freeze plus the disposable-only exception;
- exact `gpt-5.6-sol`, expected agents, and never/full-access runtime settings.

`scripts/test-check-migrations.sh` uses isolated temporary Git repositories to
prove exact frozen path/byte identity in committed, staged, unstaged, and
hosted-CI states; rename/delete and index/worktree cancellation resistance;
support-document scope; mutable unshared candidates; pre-freeze squash; no
insertion before the frozen tip; trusted local base selection; and exact push
predecessor behavior.

## Measurements

- Model: exact `gpt-5.6-sol`; profiles shown above.
- Task success: static baseline 8/8; candidate 8/8.
- Required evidence completeness: all fixed assignments, required outcomes,
  forbidden actions, and rubrics inspected.
- Critical rule violations: 0 after advisory closure.
- Runtime approval prompts or unnecessary authority questions: 0.
- Files changed outside governance/workflow scope: 0.
- Focused checks: workflow regressions, migration regressions, agent runtime
  policy, shell/Python/YAML syntax, `make policy`, and `git diff --check` pass.
- Input/output and cache tokens: unavailable.
- Exact latency split: unavailable; implementation and review/test wait are
  reported manually in the PR.
- Programmatic tool calling: no edits, approvals, normative decisions, or final
  validation were delegated programmatically.

## Explicit limitations

- Static fixture comparison does not prove future agents will behave correctly.
- Migration fixtures do not run PostgreSQL or a real shared/persistent upgrade.
- Git cannot infer an omitted pre-merge shared/persistent application. The PR
  must record path, SHA-256, source commit, and environment; uncertainty is a
  stop condition.
- This report covers the working-diff checkpoint. It is not exact-SHA release
  approval and does not replace full local validation, final independent
  review, or hosted CI.

## Human review state

Independent exact-model static review: **GO for instruction semantics and
focused regression design**. Release approval remains pending the stable
exact-SHA candidate gates.
