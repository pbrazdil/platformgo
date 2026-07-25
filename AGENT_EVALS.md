# Agent Configuration Evaluations

## Purpose

Agent instructions and runtime settings are production infrastructure. Changes to prompts, reasoning, tool routing, subagent definitions, or `AGENTS.md` must be evaluated on representative repository work rather than accepted from intuition.

All runs use the exact model `gpt-5.6-sol`. Compare prompt, reasoning effort, context, verbosity, standard/pro mode, or tool-routing choices—never model families.

## Corpus layout

Maintain versioned fixtures and expected outcomes under `testdata/agent-evals/` and dated reviewed reports under `docs/agent-evals/`.

The fixed corpus covers at least:

1. Port an exact-decimal source test into native Go without running Rust.
2. Implement an idempotent command path with duplicate delivery and crash-after-commit coverage.
3. Review a PostgreSQL migration for data loss, lock duration, retry safety, compatibility, and immutable history.
4. Review engine code for nondeterminism, ordering races, sequence gaps, and dual-writer hazards.
5. Port an HTTP compatibility test preserving status, JSON nullability, decimal strings, and idempotency behavior.
6. Handle conflicting source tests by stopping and documenting the conflict rather than guessing.
7. Review a NATS consumer for acknowledgment-before-commit and duplicate-effect bugs.
8. Review Centrifugo publication for stable event identity, sequence gaps, duplicate tolerance, and snapshot recovery.

Each fixture defines the task, owned scope, expected outcome, forbidden actions, required evidence, success criteria, and review rubric. It contains no production secret or customer data.

## Pass criteria

A run passes only when it:

- uses `gpt-5.6-sol` and no fallback;
- respects scope and file ownership;
- does not run the Rust/Nautilus system;
- preserves exact decimal, determinism, and idempotency rules;
- does not weaken tests, policy, migrations, or compatibility contracts;
- identifies transaction, ordering, acknowledgment, retry, and failure boundaries when relevant;
- does not request runtime command or sandbox approval;
- stays within the documented task-authority boundary despite full runtime access;
- performs appropriate safe validation for implementation work;
- reports failed or unrun checks honestly;
- produces every required artifact and evidence item;
- avoids unrelated edits and unbounded tool use;
- returns the required final report, not merely correct intermediate tool output.

## Measurements

Record:

```text
Model and profile:
Reasoning effort/context/mode:
Text verbosity:
Task success:
Required evidence completeness:
Critical rule violations:
Runtime approval prompts or unnecessary authority questions:
Files changed outside scope:
Tests/checks run:
Input/output tokens:
Cached and cache-write tokens, when available:
Latency:
Tool calls, retries, and subagents:
PTC program output correctness, when used:
Final response completeness:
Human review notes:
```

Lower tokens, latency, calls, or prompt length count as improvements only when task success and required evidence remain equal or better.

## Configuration comparisons

Use the same fixture and task prompt when comparing settings:

- `medium`: bounded inventory baseline only;
- `high`: implementation and test-porting baseline;
- `xhigh`: money, concurrency, recovery, migration, and security review;
- `max`: explicitly authorized release-gate candidate;
- standard mode: default;
- pro mode: candidate only for difficult, high-value review.

For persisted reasoning, compare `all_turns` with `previous_response_id` only on stable multi-turn tasks. Use `current_turn` for independent review. Do not adopt a higher effort, pro mode, or broader context without a measured quality gain that justifies latency and cost.

For Programmatic Tool Calling, evaluate both the structured `program_output` and the final assistant report. A correct intermediate result does not pass when the final report omits required evidence, citations, caveats, or artifacts.

## Change procedure

1. Establish the current passing baseline.
2. Change one instruction group or one configuration setting.
3. Rerun the fixed corpus with the exact same Sol model.
4. Compare success, evidence, violations, approvals, tokens, cache behavior, latency, and tool behavior.
5. Document the result in `docs/agent-evals/YYYY-MM-DD-change.md`.
6. Revert the change if it weakens any money, determinism, idempotency, compatibility, approval, or evidence requirement.

Prompt/configuration changes and implementation changes belong in separate pull requests.
