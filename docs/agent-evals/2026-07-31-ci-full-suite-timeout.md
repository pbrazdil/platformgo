# Hosted full-suite timeout evaluation

## Candidate

- Baseline: `origin/main` at
  `6f00c6a054e67e8db7c2fdf729e183ad0853b4a5`, tree
  `a69f0c11b43d22541f49118136379bbd84317475`.
- Candidate change: run the complete serial hosted `test` and `race` suites
  with an explicit 30-minute Go package timeout and document the exact hosted
  commands in `TESTING.md`.
- Preserved behavior: every package, test, assertion, PostgreSQL 19 Beta 2
  service, NATS service, Centrifugo service, serial package order, race
  instrumentation, and single execution count remains unchanged.
- Reason: both hosted suites reproducibly exhausted Go's implicit 10-minute
  package timeout while the same complete suites passed locally. The new bound
  remains finite and does not turn a timeout into a skip or ignored failure.

## Method and trust boundary

The fixed eight-task corpus ran once against the clean baseline and once
against the candidate through `codex-cli 0.146.0`. All 16 read-only subject
runs explicitly requested and verified `gpt-5.6-sol`, used
`approval_policy=never`, allowed no fallback, and returned exactly one final
response without tool calls.

Two additional isolated `gpt-5.6-sol` xhigh sessions scored the baseline and
candidate responses against the protected baseline `AGENT_EVALS.md` and hidden
rubrics. Both scorers rejected the opaque deficient calibration item. The
evidence is locally generated and hash-bound but not provider-attested;
repository policy, exact-SHA review, hosted CI, and owner acceptance remain
separate gates.

Evidence:
`docs/agent-evals/artifacts/2026-07-31-ci-full-suite-timeout/manifest.json`
and the adjacent `reviews.json`, raw event streams, and session snapshots.

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
prompts, and out-of-scope changes were zero.

The timeout guidance changes the prompt-surface digest because `TESTING.md` is
an applicable companion document. The behavioral evidence found no weakening
of money, determinism, idempotency, compatibility, migration, acknowledgment,
or evidence requirements.

## Measurements

- Model: exact `gpt-5.6-sol`; no fallback.
- Subject runs: 16, all successful, one final response each, zero tool calls.
- Scorer runs: 2, both successful, one final response each, zero tool calls.
- Baseline subject usage: 287,212 input tokens, 0 cached input tokens,
  42,313 output tokens, and 20,798 reasoning output tokens.
- Candidate subject usage: 287,464 input tokens, 26,112 cached input tokens,
  39,856 output tokens, and 18,320 reasoning output tokens.
- Cache-write tokens: zero on both sides.
- Baseline subject latency: 926.514 seconds total, 122.165 seconds median.
- Candidate subject latency: 795.379 seconds total, 89.576 seconds median.
- Scorer latency: 76.539 seconds baseline and 155.247 seconds candidate.
- Corpus SHA-256:
  `bc24163189e87b61ed721578dc77a777ef247e89125a588ea8817009e27a258d`.
- Runner SHA-256:
  `ed3b77aea974fcd001ab926b90963a74c60d578411ee0cdc4e98302da00b3085`.
- Library SHA-256:
  `777549fb9e548970e16f2671f1a32b101d7b4cf5ebe51eb0085817ff3862ced3`.

## Limits and disposition

The response corpus evaluates agent behavior and evidence quality; it does not
itself execute the hosted Go suites or qualify production. The timeout remains
a bounded hosted-runner allowance, not a test deletion or success override.
Behavioral evaluation is **GO** for this governance-only candidate. Repository
policy, format, lint, push, and hosted CI still must pass on its exact SHA.

<!-- agent-eval-summary
{
  "baseline_pass": 8,
  "candidate_pass": 8,
  "corpus_sha256": "bc24163189e87b61ed721578dc77a777ef247e89125a588ea8817009e27a258d",
  "critical_rule_violations": 0,
  "files_changed_outside_scope": 0,
  "final_response_complete": 16,
  "manifest": "docs/agent-evals/artifacts/2026-07-31-ci-full-suite-timeout/manifest.json",
  "model": "gpt-5.6-sol",
  "prompt_surface_sha256": {
    "baseline": "76b192038e3a8fad52f0341361257a2eebae378c4bb64f3291a7493d0bcd3f36",
    "candidate": "ff55853fe8843499339451fa8e81fa67973e2316747a624494571829af94c400"
  },
  "required_evidence_complete": 16,
  "reviewer_tool_calls": 0,
  "runtime_approval_prompts": 0,
  "schema_version": 2,
  "subject_tool_calls": 0
}
-->
