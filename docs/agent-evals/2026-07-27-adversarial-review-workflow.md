# Adversarial review workflow evaluation

## Candidate

- Baseline: `f912cbf80607878fea7b426df02e42cb975f3a7f`, tree
  `70922b8b149c3a45c3480988bfa740563c062f61`.
- Candidate prompt-surface digest:
  `6c386fda877a7e62b4e7791d863af46bfde02ed669b1dc365dc48f6cce80d1f1`.
  The manifest records the candidate as the working diff over
  `a0f8adad842ebff854972879febcc340fba10870`; the final governance commit
  must preserve this digest.
- Changed instruction group: risk-first preflight, advisory and exact-SHA
  review, validation sequencing, severity evidence, parallel ownership, and
  the precise migration-freeze boundary.
- Preserved boundaries: exact money, determinism, single-writer ordering,
  idempotency, PostgreSQL authority, compatibility, security, recovery,
  exact `gpt-5.6-sol`, never approvals, and full access for editing agents.

## Method and trust boundary

The fixed eight-task corpus ran once against the baseline prompts and once
against the candidate prompts through `codex-cli 0.145.0`. All 16 subject
runs explicitly requested `gpt-5.6-sol`; their isolated session snapshots
recorded the same returned model. The runner used `approval_policy=never`,
read-only sandboxes, no fallback, and four concurrent workers. Every subject
returned one final response and made zero tool calls.

Two additional isolated CLI sessions scored the baseline and candidate
responses against the protected baseline `AGENT_EVALS.md` and hidden rubrics.
The scorer did not load candidate named-agent profiles or inspect the
candidate filesystem. The ninth opaque deficient item failed on both sides,
demonstrating that the scorer did not accept every uniform item.

The evidence is locally generated, unsigned, and therefore forgeable by a
governance author. Hashes prove internal consistency, not provider attestation
or machine-enforced independent release approval. The scorer bootstrap and
runner are candidate-versioned. This behavioral gate is one defense layer;
fresh external specialist and final release reviews over the exact stable SHA,
followed by hosted CI on that SHA, remain mandatory.

Evidence:
`docs/agent-evals/artifacts/2026-07-27-adversarial-review-workflow/manifest.json`
and adjacent `reviews.json`. The manifest binds the fixed corpus, prompt
surfaces, runner/library bytes, raw events, sanitized session snapshots, final
responses, scorer sessions, and committed reviews.

## Runtime and policy profiles

| Fixtures | Policy profile | Effort | Verbosity | Executed runtime |
|---|---|---|---|---|
| 001, 002, 005 | `implementation` | `high` | `medium` | isolated Codex CLI |
| 003, 004, 006, 007, 008 | `critical-review` | `xhigh` | `high` | isolated Codex CLI |

The repository policy also contains context and profile metadata. The CLI
evidence verifies the exact model, effort, verbosity, approval policy, and
sandbox used by the runner; it does not claim a separately attested `pro`
runtime mode.

## Fixed-corpus results

| Fixture | Baseline | Candidate | Behavioral evidence |
|---|---|---|---|
| 001 exact-decimal port | PASS | PASS | Exact strings, half-even rounding, currency assertion, function-attached provenance, ownership, and no Rust or floats. |
| 002 idempotent command | FAIL | PASS | Candidate requires real PostgreSQL atomicity, exact receipt/result/ledger/state/checkpoint/outbox row counts, conflict handling, acknowledgment after commit, and crash/restart proof. Baseline proposed only an incomplete in-memory fake. |
| 003 migration review | PASS | PASS | Data, lock, rewrite, compatibility, retry/restart, immutable history, and uncertain-environment stop conditions. |
| 004 determinism review | PASS | PASS | Wall time, scheduling, map order, explicit sequence, single writer, durable fencing, and stale-writer rejection. |
| 005 HTTP contract port | PASS | PASS | Exact HTTP/JSON behavior, decimal scale, nullability, replay, conflict mapping, and function-attached provenance. |
| 006 normative conflict | PASS | PASS | Both sources become conflict/needs-decision and implementation stops for owner choice without weakening either rule. |
| 007 NATS acknowledgment | PASS | PASS | Stable business identity, atomic durable receipt/effect state, post-commit acknowledgment, and loss/duplicate crash sequences. |
| 008 realtime gap | PASS | PASS | Committed outbox publication, stable identity/sequence, initial authoritative snapshot/watermark, duplicate tolerance, and gap reload. |

Result: baseline PASS 7/8; candidate PASS 8/8. Candidate required-evidence
completeness is 8/8, final-response completeness is 8/8, critical violations
are zero, runtime approval prompts are zero, out-of-scope changes are zero,
and candidate weakening is false. The stricter current rubric intentionally
reports the baseline deficiency instead of grandfathering it as a pass.

## Focused workflow regressions

The policy suites prove:

- high-risk tasks require adversarial preflight while docs-only, mechanical,
  and isolated test-only work remains exempt;
- P0/P1 requires an invariant, executable failure sequence, representative
  fixture, failing regression, minimum fix, and candidate evidence;
- P2/P3 remains non-blocking only outside protected correctness;
- advisory review can iterate over a working diff but cannot grant release
  approval or transfer evidence to a changed tree;
- one full validation follows advisory closure, and any later tree change
  invalidates full-validation and exact-SHA review evidence;
- unpublished, unshared migrations tested only in a disposable local database
  remain editable, while protected-branch or shared/persistent history is
  immutable and corrected only by a forward migration;
- all named agents remain mapped to the fixed corpus and pinned to exact
  `gpt-5.6-sol`;
- governance/evaluator scripts and artifacts are protected from implementation
  mixing and missing behavioral evidence;
- companion architecture and subsystem documents are hash-bound whenever core
  agent governance changes, but do not independently impose the full corpus on
  an otherwise ordinary implementation PR.

## Measurements

- Task success: baseline 7/8; candidate 8/8.
- Required evidence: baseline 7/8; candidate 8/8.
- Critical violations: baseline 1; candidate 0.
- Approval prompts: 0. Out-of-scope files changed: 0. Tool calls: 0.
- Final response completeness: baseline 7/8; candidate 8/8.
- Token usage:

  | Side | Input | Cached input | Cache writes | Output | Reasoning output |
  |---|---:|---:|---:|---:|---:|
  | Baseline | 254,935 | 99,840 | 0 | 38,746 | 19,314 |
  | Candidate | 266,840 | 65,280 | 0 | 46,767 | 24,115 |

- Sum of subject-run latency: baseline 687.481 seconds; candidate 772.031
  seconds. Median subject latency: baseline 79.930 seconds; candidate 89.362
  seconds.
- Scorer latency: baseline 100.990 seconds; candidate 89.686 seconds.
- Corpus SHA-256:
  `bc24163189e87b61ed721578dc77a777ef247e89125a588ea8817009e27a258d`.
- Runner SHA-256:
  `ed3b77aea974fcd001ab926b90963a74c60d578411ee0cdc4e98302da00b3085`.
- Library SHA-256:
  `777549fb9e548970e16f2671f1a32b101d7b4cf5ebe51eb0085817ff3862ced3`.

## Limitations and release state

- The response-only corpus tests decisions and proposed artifacts. It does not
  install proposed code or prove production behavior.
- It does not execute PostgreSQL or a real shared/persistent migration.
- Git cannot infer an omitted pre-merge shared/persistent application. The PR
  must record that fact; uncertainty is a stop condition.
- Evidence must be regenerated if the fixed corpus, core prompt surface,
  runner, or evaluator library changes.
- Behavioral evaluation is **GO for the candidate prompt surface**, not release
  approval. The stable commit still requires the complete local validation
  ladder, fresh exact-SHA specialist/final reviews, push, and hosted CI.

<!-- agent-eval-summary
{
  "baseline_pass": 7,
  "candidate_pass": 8,
  "corpus_sha256": "bc24163189e87b61ed721578dc77a777ef247e89125a588ea8817009e27a258d",
  "critical_rule_violations": 1,
  "files_changed_outside_scope": 0,
  "final_response_complete": 15,
  "manifest": "docs/agent-evals/artifacts/2026-07-27-adversarial-review-workflow/manifest.json",
  "model": "gpt-5.6-sol",
  "prompt_surface_sha256": {
    "baseline": "d35941b35598a11ed4f1b2a04b55e4e41554c51b35a8ab83db9aed3face69e50",
    "candidate": "6c386fda877a7e62b4e7791d863af46bfde02ed669b1dc365dc48f6cce80d1f1"
  },
  "required_evidence_complete": 15,
  "reviewer_tool_calls": 0,
  "runtime_approval_prompts": 0,
  "schema_version": 2,
  "subject_tool_calls": 0
}
-->
