# Repository Policy Pack

This directory contains the governing documents, Codex configuration, custom agent profiles, and enforcement templates for the Go rewrite.

## Start here

1. `AGENTS.md` — repository-wide execution rules and task-authority boundary
2. `MODEL_POLICY.md` — mandatory `gpt-5.6-sol` configuration and prompting policy
3. `PROJECT_CHARTER.md`
4. `ARCHITECTURE.md`
5. `INVARIANTS.md`
6. `TESTING.md`
7. The subsystem document and nested `AGENTS.md` for the files being changed

The project pins the exact flagship model slug `gpt-5.6-sol` for primary agents and all subagents. `.codex/config.toml`, `.codex/agents/*.toml`, and the Responses API policy in `policy/openai-agent-policy.json` are checked by `scripts/check-agent-runtime.py`; the movable `gpt-5.6` alias, other variants, and fallback routing are not accepted.

## Before development begins

1. Copy the complete pack, including hidden `.codex/` and `.github/` directories, to the repository root.
2. Mark the project trusted so project-scoped Codex configuration is loaded.
3. Keep the `go.mod` `go` directive pinned to the reviewed exact patch release
   and keep the module tidy. Add a separate `toolchain` directive only when it
   intentionally differs from that minimum; Go removes a redundant identical
   directive during `go mod tidy`.
4. Keep `.github/CODEOWNERS` populated with actual owners.
5. Confirm the pinned source revisions, inventory roots, and expected test counts in `ports/SOURCE_REVISIONS.md`.
6. Enable branch protection requiring all CI jobs.
7. Protect `main` from direct pushes.
8. Require money, engine, database, test-harness, architecture, and agent-policy owners for their paths.
9. Make model-policy, migration-immutability, and general policy checks required status checks.
10. Create the initial fixed agent-evaluation corpus described in `AGENT_EVALS.md`.
11. Use `docs/AGENT_TASK_TEMPLATE.md` for assignments and `docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md` for independent high-risk review.

## Enforcement

Run:

```bash
make policy
```

This validates the exact GPT-5.6 Sol pin, custom-agent configuration, AGENTS.md instruction budget, economic code policy, migration history, dependencies, source-test accounting, and governance-change isolation.

After the entire pinned test inventory has been adjudicated and ported, run `make port-map-complete`. This stricter closure gate rejects incomplete counts and non-terminal ledger statuses.

Agent prompt, model, reasoning, `.codex`, or `AGENTS.md` changes are governance changes. They must be reviewed separately from implementation and evaluated according to `AGENT_EVALS.md`.

The scripts are intentionally strict. Change a rule only through a reviewed policy/ADR change, never by weakening enforcement to make a patch pass.
