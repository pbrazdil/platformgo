# Contributing

## Branch and review policy

- `main` is protected; no direct pushes.
- All changes use pull requests and required CI checks.
- Money, engine, migration, test-harness, architecture, and agent-policy paths require their configured CODEOWNERS.
- Policy, invariant, prompt, model, or agent-runtime changes are submitted separately from implementation changes.

## Agent configuration

Repository agents use `gpt-5.6-sol` exclusively. Do not override `.codex/config.toml` or custom agents with the `gpt-5.6` alias, `terra`, `luna`, or another model. See `MODEL_POLICY.md`.

Changes to `AGENTS.md`, nested agent instructions, `.codex`, `policy/openai-agent-policy.json`, or prompt templates require the fixed evaluation procedure in `AGENT_EVALS.md`.

## Development sequence

Use `docs/AGENT_TASK_TEMPLATE.md` for agent assignments and
`docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md` for high-risk work. Do not
repeat the standing rules from `AGENTS.md` and `MODEL_POLICY.md` in each task.

1. Read root and applicable nested `AGENTS.md` files plus the relevant architecture documents; assign agent work with `docs/AGENT_TASK_TEMPLATE.md`.
2. Scope the slice and run parallel adversarial preflight when protected boundaries require it.
3. Create or port representative failing native Go tests and review their failure boundary.
4. Implement the smallest vertical slice while advisory reviewers report blockers immediately.
5. Reach focused green and close advisory blockers with their originating reviewers.
6. Stabilize a clean candidate, run one full local validation pass, and record its exact SHA/tree/base.
7. Run parallel specialist review and one independent final read-only release review.
8. Push that exact SHA, require hosted CI on it, recheck the base, and merge.

## Commit guidance

Prefer intentional commits:

```text
test: port deterministic limit modification behavior
feat: implement idempotent order modification
refactor: isolate order transition table
```

Do not hide test-first history through squash during review.

## Review standard

Review correctness and invariants before style. Money-path changes must explain transaction, ordering, idempotency, recovery, rounding, and compatibility boundaries.

See `REVIEW_CHECKLIST.md`.
