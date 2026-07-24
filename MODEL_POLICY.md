# GPT-5.6 Sol Agent Policy

## Non-negotiable model pin

Every repository agent uses the exact model slug:

```text
gpt-5.6-sol
```

This includes the primary Codex session, `/review`, custom agents, spawned subagents, CI-driven agents, and all Responses API orchestration. Do not use the movable `gpt-5.6` alias, another GPT-5.6 variant, another model family, or a fallback route. If Sol is unavailable, fail explicitly rather than rerouting.

Repository configuration can be overridden by command-line or spawn-time settings. Such overrides are prohibited for project work. Do not pass a different `--model`, `-c model=...`, review model, or explicit subagent model. API orchestration must set `model: "gpt-5.6-sol"`, verify the returned model identifier, and treat a mismatch as an error.

The machine-readable source of truth is `policy/openai-agent-policy.json`. `scripts/check-agent-runtime.py` validates it together with `.codex/`, templates, and instruction budgets.

## Codex configuration

The project-scoped defaults are in `.codex/config.toml` and load only when the project is trusted. They pin:

- `model` and `review_model` to `gpt-5.6-sol`;
- default reasoning effort to `high`;
- default response verbosity to `medium`;
- approvals to `on-request`;
- the sandbox to `workspace-write`;
- every default subagent to `gpt-5.6-sol`.

Every file in `.codex/agents/` repeats the exact model pin because explicit spawn settings otherwise take precedence. Agent roles vary only in reasoning effort, verbosity, and sandbox permissions:

- `inventory`: `medium`, low verbosity, read-only;
- implementation and test porting: `high`, medium verbosity, workspace-write;
- money, migration, and determinism review: `xhigh`, high verbosity, read-only;
- release review in Codex: `xhigh`, high verbosity, read-only; `max` is reserved for the Responses API release-gate profile.

Do not use a lower-cost model for exploration. Sol exclusivity applies to all workstreams.

## Responses API profiles

Use the Responses API for reasoning, tool-calling, and multi-turn orchestration. Approved request profiles are defined once in `policy/openai-agent-policy.json`:

| Profile | Reasoning | Context | Mode | Verbosity | Use |
|---|---|---|---|---|---|
| `inventory` | `medium` | `current_turn` | standard | low | bounded read-only classification |
| `implementation` | `high` | `all_turns` | standard | medium | implementation and test porting |
| `critical-review` | `xhigh` | `current_turn` | `pro` | high | independent money, migration, recovery, or concurrency review |
| `release-gate` | `max` | `current_turn` | `pro` | high | explicitly authorized final release gate |

Reasoning effort and reasoning mode are independent. Standard mode is the default; `reasoning.mode: "pro"` is reserved for difficult, high-value reviews where representative evaluations demonstrate a material gain. Pro mode is not a different model slug.

Use `reasoning.context: "all_turns"` with `previous_response_id` only while the same task goals, assumptions, and priorities remain active. Use `current_turn` for independent review or after a material task switch. Do not carry stale reasoning into a new decision.

Use `text.verbosity` in Responses API requests and `model_verbosity` in Codex configuration. Prompts specify required fields and evidence; they do not repeatedly ask the model to be brief or detailed.

## Lean prompt policy

Stable repository rules belong in `AGENTS.md` and this file. Use:

- `docs/AGENT_TASK_TEMPLATE.md` for implementation, porting, investigation, and planning assignments;
- `docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md` for independent high-risk review.

A task prompt adds only information unique to that task: goal, scope and ownership, relevant inputs, task-specific constraints, required evidence, success criteria, validation, deliverables, and any approval boundary narrower than `AGENTS.md`.

State each requirement once. Do not paste repository policies into every assignment. Expose only tools relevant to the task. Keep examples only when they encode a compatibility requirement or correct a measured failure. Ask for decisions, evidence, file references, tests, and failure sequences—not hidden chain-of-thought.

Do not instruct the model to “think harder,” “use pro mode,” “use maximum reasoning,” or reveal private reasoning. Select effort, mode, context, and verbosity in configuration.

## Autonomy and approval boundaries

The single repository-wide action boundary is in `AGENTS.md`:

- review, explanation, diagnosis, and planning authorize inspection and reporting, not edits;
- build, fix, change, and port requests authorize in-scope local edits and non-destructive validation;
- external writes, destructive or costly actions, production operations, secrets, migration-history changes, and material scope expansion require confirmation.

Task prompts do not repeat or contradict this boundary. They may only narrow it.

## Tool orchestration

Direct tool calls are the default for coding because each result often changes the next decision, approval must remain visible, and final validation must preserve native artifacts.

Programmatic Tool Calling is allowed only for a bounded, predictable, non-side-effecting stage such as filtering, joining, ranking, deduplication, aggregation, inventory, or validation. The assignment must name the eligible tools, exact output schema, concurrency and retry limits, stopping condition, and handoff back to direct judgment. Do not use it for edits, approvals, normative conflict resolution, or final validation.

## Multi-agent use

Use subagents only when work divides cleanly into independent, non-overlapping workstreams. The primary agent owns decomposition, file ownership, integration, conflict resolution, waiting for all requested results, and final validation. Avoid concurrent edits to shared harnesses, decimal primitives, migrations, or the same package.

All agents remain on `gpt-5.6-sol`; only approved effort, verbosity, and permission profiles vary.

## Prompt caching

Use implicit prompt caching by default. Keep the stable prefix limited to `AGENTS.md`, this policy, and stable orchestration instructions. Put source tests, diffs, logs, and mutable worktree context afterward. Adopt explicit cache breakpoints only after measuring task success, cache writes, cache reads, latency, and total cost on representative work.

## Evaluation and governance

Changes to model settings, `.codex/`, prompt templates, tool routing, multi-agent rules, `AGENTS.md`, or this policy are governance changes. They must be isolated from implementation changes and evaluated under `AGENT_EVALS.md` using the same Sol model. Compare prompt, effort, context, verbosity, standard/pro mode, or tool-routing changes—not model families.

Lower token use, latency, or tool count is an improvement only when task success, evidence, approval behavior, and money-safety requirements remain equal or better.

Run:

```bash
make policy
```

before accepting any agent-policy change.

## Maintenance

This policy was checked against the official GPT-5.6 model guidance and Codex configuration documentation on 2026-07-24. Re-run the agent evaluation corpus and review those references before changing the model pin, reasoning profiles, Responses API fields, prompt-caching strategy, or Codex agent configuration. Do not adopt an automatic model fallback or alias during maintenance.

## Official references

- GPT-5.6 guidance: https://developers.openai.com/api/docs/guides/latest-model
- GPT-5.6 Sol model: https://developers.openai.com/api/docs/models/gpt-5.6-sol
- Codex configuration basics: https://developers.openai.com/codex/config-file/config-basic
- Codex configuration reference: https://developers.openai.com/codex/config-file/config-reference
- Codex subagents: https://developers.openai.com/codex/agent-configuration/subagents
