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

Use `docs/AGENT_TASK_TEMPLATE.md` for agent assignments. Do not repeat the standing rules from `AGENTS.md` and `MODEL_POLICY.md` in each task.

1. Read root and applicable nested `AGENTS.md` files plus the relevant architecture documents; assign agent work with `docs/AGENT_TASK_TEMPLATE.md`.
2. Create or port failing native Go tests.
3. Implement the smallest vertical slice.
4. Run targeted checks, then the required full verification.
5. Submit the PR using the template.

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
