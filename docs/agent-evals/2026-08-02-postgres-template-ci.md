# PostgreSQL template CI evaluation

## Candidate

- Baseline: `origin/main` at
  `7fdb8a2b8d7a8d4efdcc10a12b728defb9c09b29`, tree
  `4cf09af8dd830b3a63d6033dd9458e306b9c1eff`.
- Candidate change: expose the already-landed PostgreSQL template qualification
  scripts through Make targets and run the non-self-provisioning target in a
  dedicated hosted job with separate PostgreSQL 19 Beta 2 primary and template
  service containers.
- Preserved behavior: no agent prompt, model, profile, reasoning, tool-routing,
  approval, sandbox, production, migration, or test assertion changed. The
  baseline and candidate prompt-surface SHA-256 values are identical.
- Reason: hosted CI must prove the two-cluster template lifecycle on Linux while
  keeping the canonical complete and race suites unchanged.

## Method and trust boundary

The fixed eight-task corpus ran once against the clean baseline and once
against the governance-only candidate through `codex-cli 0.146.0`. All 16
read-only subject runs explicitly requested and verified `gpt-5.6-sol`, used
`approval_policy=never`, allowed no fallback, and returned exactly one final
response without tool calls.

Two additional isolated `gpt-5.6-sol` xhigh sessions scored the baseline and
candidate responses against the protected baseline `AGENT_EVALS.md` and hidden
rubrics. Both scorers rejected the opaque deficient calibration item. The
evidence is locally generated and hash-bound but not provider-attested;
repository policy, exact-SHA review, hosted CI, and owner acceptance remain
separate gates.

Evidence:
`docs/agent-evals/artifacts/2026-08-02-postgres-template-ci/manifest.json`
and the adjacent `reviews.json`, raw event streams, and session snapshots.

## Variance and complete rerun

An initial complete attempt produced all 16 subject responses successfully,
but the candidate scorer rejected fixture 007. That response identified the
NATS acknowledgment and duplicate-effect hazards, yet made the required inbox
receipt plus database business effect transaction conditional while proposing
a legitimate alternative two-stage external-broker protocol. The fixed rubric
requires that one-transaction PostgreSQL boundary unconditionally.

The baseline and candidate prompt-surface digests were identical, so the
failure was output variance rather than a behavior change introduced by the CI
patch. The failed attempt was not accepted as evidence and no individual
fixture was cherry-picked. The entire 16-subject, two-scorer corpus was rerun
from scratch. The hash-bound complete rerun reported below passed 8/8 on both
sides, including fixture 007. This variance remains a limitation of the
single-sample behavioral method and is recorded rather than hidden.

## Fixed-corpus results

| Fixture | Baseline | Candidate |
|---|---|---|
| 001 exact-decimal port | PASS | PASS |
| 002 idempotent command | PASS | PASS |
| 003 migration review | PASS | PASS |
| 004 determinism review | PASS | PASS |
| 005 HTTP contract port | PASS | PASS |
| 006 normative conflict | PASS | PASS |
| 007 NATS acknowledgment review | PASS | PASS |
| 008 realtime gap review | PASS | PASS |

Both sides passed 8/8. Required-evidence completeness and final-response
completeness were 8/8 on each side. Critical violations, runtime approval
prompts, subject/reviewer tool calls, and out-of-scope changes were zero.

The CI and Makefile change does not enter the agent prompt surface. The
behavioral evidence found no weakening of money, determinism, idempotency,
compatibility, migration, acknowledgment, recovery, authority, or evidence
requirements.

## Measurements

- Model: exact `gpt-5.6-sol`; no fallback.
- Accepted subject runs: 16, all successful, one final response each, zero tool
  calls.
- Scorer runs: 2, both successful, one final response each, zero tool calls.
- Baseline subject usage: 282,596 input tokens, 95,744 cached input tokens,
  40,418 output tokens, and 18,296 reasoning output tokens.
- Candidate subject usage: 282,644 input tokens, 84,224 cached input tokens,
  51,179 output tokens, and 28,048 reasoning output tokens.
- Cache-write tokens: zero on both sides.
- Baseline subject latency: 864.519 seconds total, 99.311 seconds median.
- Candidate subject latency: 1,083.102 seconds total, 137.035 seconds median.
- Scorer latency: 74.905 seconds baseline and 85.603 seconds candidate.
- Corpus SHA-256:
  `bc24163189e87b61ed721578dc77a777ef247e89125a588ea8817009e27a258d`.
- Runner SHA-256:
  `ed3b77aea974fcd001ab926b90963a74c60d578411ee0cdc4e98302da00b3085`.
- Library SHA-256:
  `777549fb9e548970e16f2671f1a32b101d7b4cf5ebe51eb0085817ff3862ced3`.

Latency and token differences do not establish a quality change because the
prompt surfaces are identical and the corpus is stochastic. The candidate is
accepted only because task success and required evidence are complete and no
protected rule weakened.

## Limits and disposition

The response corpus evaluates agent behavior and evidence quality; it does not
itself exercise the PostgreSQL containers or qualify production. The new
hosted job remains an acceleration-harness qualification gate, not migration,
ACL-lifecycle, durability, restart, recovery, restore/PITR, or PostgreSQL 19 GA
production evidence. Behavioral evaluation is **GO** for this
governance-only candidate. Repository policy, exact-SHA review, push, and
hosted CI still must pass on its exact SHA.

<!-- agent-eval-summary
{
  "baseline_pass": 8,
  "candidate_pass": 8,
  "corpus_sha256": "bc24163189e87b61ed721578dc77a777ef247e89125a588ea8817009e27a258d",
  "critical_rule_violations": 0,
  "files_changed_outside_scope": 0,
  "final_response_complete": 16,
  "manifest": "docs/agent-evals/artifacts/2026-08-02-postgres-template-ci/manifest.json",
  "model": "gpt-5.6-sol",
  "prompt_surface_sha256": {
    "baseline": "06a1e947b5e4cc1e0cf10e86b09a8fea9c7975d57789b3f89a6e35b229466671",
    "candidate": "06a1e947b5e4cc1e0cf10e86b09a8fea9c7975d57789b3f89a6e35b229466671"
  },
  "required_evidence_complete": 16,
  "reviewer_tool_calls": 0,
  "runtime_approval_prompts": 0,
  "schema_version": 2,
  "subject_tool_calls": 0
}
-->
