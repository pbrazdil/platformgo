# Companion documentation policy evaluation

## Candidate

- Baseline: `origin/main` at
  `1f96c757331c41e721d98c6c369951c0038533d8`.
- Candidate instruction change: clarify in `AGENTS.md` that faithful
  implementation companion documentation belongs atomically with the
  implementation it describes.
- Preserved boundary: an accepted economic behavior change still requires a
  separate ADR, updated tests, a compatibility impact statement, and owner
  approval. Protected governance and invariant changes remain isolated.
- Unchanged inputs: model pin, profiles, reasoning settings, fixture prompts,
  fixture ownership, expected evidence, forbidden actions, and economic rules.

## Method and limits

An independent exact-model reviewer performed a static, read-only comparison
of the baseline and candidate instruction semantics against all eight fixed
fixtures under `testdata/agent-evals/tasks/`. The comparison used
`gpt-5.6-sol`. It inspected each assignment, required outcome, forbidden
action, and rubric.

This was not an empirical execution rerun. The fixture actions were not
performed, no fixture implementation or review artifacts were produced, and
the results below establish instruction-semantic equivalence only.

## Profile configuration

| Fixtures | Profile | Effort | Context | Mode | Verbosity |
|---|---|---|---|---|---|
| 001, 002, 005 | `implementation` | `high` | `all_turns` | standard | medium |
| 003, 004, 006, 007, 008 | `critical-review` | `xhigh` | `current_turn` | pro | high |

The baseline and candidate used the same configuration. No fixture prompt or
profile was changed for the comparison.

## Results

| Fixture | Baseline | Candidate | Static evidence checked |
|---|---|---|---|
| 001 exact-decimal port | PASS | PASS | Exact strings, half-even rounding, currency assertion, provenance, assigned test/ledger scope, and the prohibition on Rust execution or floats remain required. |
| 002 idempotent command | PASS | PASS | Atomic transaction, stable business identity, duplicate handling, acknowledgment-after-commit, exact row counts, and crash-after-commit replay remain required. |
| 003 migration review | PASS | PASS | Read-only scope, immutable applied history, data-loss and lock analysis, staged backfill, compatibility, and measured upgrade evidence remain required. |
| 004 determinism review | PASS | PASS | Wall time, scheduling, map order, shared mutation, sequence, dual-writer, and stale-fencing hazards remain mandatory findings. |
| 005 HTTP contract port | PASS | PASS | Status, decimal scale, nullability, stored retry response, conflict code, provenance, and assigned contract-test/ledger scope remain required. |
| 006 normative conflict | PASS | PASS | The agent must record the conflict and stop for an owner decision; the candidate does not authorize choosing or weakening an economic rule. |
| 007 NATS acknowledgment review | PASS | PASS | Read-only scope, stable effect identity, inbox/effect transactionality, acknowledgment after commit, and duplicate-debit crash sequences remain required. |
| 008 realtime gap review | PASS | PASS | Stable event identity and sequence, duplicate tolerance, committed publication, PostgreSQL authority, and snapshot recovery on unproven continuity remain required. |

Static corpus result: baseline PASS 8/8; candidate PASS 8/8.

## Measurements

- Model and profile: exact model `gpt-5.6-sol`; fixture profiles shown above.
- Reasoning effort/context/mode: unchanged between baseline and candidate;
  values shown above.
- Text verbosity: unchanged between baseline and candidate; values shown above.
- Task success: baseline PASS 8/8; candidate PASS 8/8 for the static
  instruction-semantic comparison.
- Required evidence completeness: all eight fixture assignments, expected
  outcomes, forbidden actions, and rubrics were checked statically. Empirical
  fixture-output completeness was not measured because the tasks were not run.
- Critical rule violations: baseline 0; candidate 0 in the static comparison.
- Runtime approval prompts or unnecessary authority questions: baseline 0;
  candidate 0 in the static comparison. No runtime prompt behavior was
  empirically exercised.
- Files changed outside scope: 0 scope changes in the compared instructions;
  fixture file ownership remains unchanged.
- Tests/checks run: static read-only corpus comparison of fixtures 001–008;
  `bash -n scripts/check-governance-change.sh
  scripts/test-check-governance-change.sh`;
  `./scripts/test-check-governance-change.sh`; `make policy`;
  `make fmt-check`; `make lint` with a fresh isolated cache; `make test`;
  `make test-race`; `make test-repeat`; and `git diff --check`. These
  repository checks passed. Fixture actions were not run empirically.
- Input/output tokens: unavailable.
- Cached and cache-write tokens: unavailable.
- Latency: unavailable.
- Tool calls, retries, and subagents: exact metrics were not supplied for the
  independent static comparison.
- Programmatic Tool Calling: not reported for the independent comparison.
- Final response completeness: the independent result reported all eight
  baseline/candidate outcomes and the non-empirical limitation.
- Approvals: no approval boundary changed; no approval prompt was reported.

## Human review state

Independent exact-model static review: **GO for instruction semantics**.

This GO is limited to the static baseline/candidate corpus comparison. It is
not evidence that the eight fixture tasks were executed successfully, and it
does not replace repository policy tests or final owner acceptance of the
complete checker change.
