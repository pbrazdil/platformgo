#!/usr/bin/env python3
"""Focused regressions for the risk-first agent and review workflow."""

from __future__ import annotations

import json
import pathlib
import re
import sys
import tomllib

ROOT = pathlib.Path(__file__).resolve().parents[1]
PINNED_MODEL = "gpt-5.6-sol"
failures: list[str] = []


def read(relative: str) -> str:
    path = ROOT / relative
    if not path.is_file():
        failures.append(f"{relative}: missing required file")
        return ""
    return path.read_text(encoding="utf-8")


def require(relative: str, *fragments: str) -> None:
    text = read(relative)
    for fragment in fragments:
        if fragment not in text:
            failures.append(f"{relative}: missing {fragment!r}")


def require_regex(relative: str, pattern: str, description: str) -> None:
    if not re.search(pattern, read(relative), flags=re.IGNORECASE | re.DOTALL):
        failures.append(f"{relative}: missing {description}")


# A high-risk PostgreSQL migration is routed through preflight, while low-risk
# docs-only, mechanical, and isolated-test-only work is explicitly exempt.
require(
    "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md",
    "durable PostgreSQL",
    "migrations",
    "Authority and transaction boundary:",
    "Lock order and writer ownership:",
    "Duplicate delivery and lost acknowledgment:",
    "Restart and recovery:",
    "Representative current-main upgrade:",
    "Rollback and unknown commit:",
    "Hostile default privileges",
    "Fail-closed conditions:",
    "Tests that must fail first:",
    "Stop conditions and owner decisions:",
    "docs-only",
    "mechanical",
    "isolated test-only",
)
require_regex(
    "AGENTS.md",
    r"preflight.{0,500}(money|ledger).{0,500}durable PostgreSQL.{0,500}security",
    "complete high-risk preflight routing",
)
require_regex(
    "AGENTS.md",
    r"(docs-only|documentation-only).{0,160}(exempt|does not require)",
    "low-risk docs-only exemption",
)

# A critical blocker cannot be invented without an executable failure case,
# and a real protected-correctness defect cannot be laundered into P2/P3.
critical = "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md"
require(
    critical,
    "P0/P1",
    "Violated invariant or contract",
    "Executable failure sequence",
    "Representative fixture",
    "Failing regression test",
    "Minimum fix property",
    "Exact candidate evidence",
    "P2",
    "P3",
    "non-blocking",
)
require_regex(
    critical,
    r"without.{0,160}(executable|concrete)\s+failure\s+sequence.{0,160}(must not|cannot).{0,80}P0/P1",
    "P0/P1 concrete-failure prohibition",
)
require_regex(
    critical,
    r"(money|durable state).{0,240}(ordering|idempotency).{0,240}(compatibility|security).{0,240}recoverability.{0,240}P1",
    "protected-correctness P1 promotion",
)
require_regex(
    critical,
    r"P2.{0,180}non-blocking.{0,180}(outside|cannot affect).{0,220}(protected|money|durable state)",
    "P2 non-blocking defect outside protected correctness",
)
require_regex(
    "AGENTS.md",
    r"P2/P3.{0,120}non-blocking.{0,120}(outside|cannot affect).{0,160}protected",
    "root P2/P3 non-blocking only outside protected correctness",
)
require_regex(
    critical,
    r"P3.{0,180}(cleanup|style|nit).{0,180}(no|without).{0,120}(correctness|exact-SHA)",
    "P3 cosmetic-only boundary",
)
require_regex(
    critical,
    r"P3.{0,260}(if|when).{0,80}(implemented|applied).{0,160}(changed|new).{0,80}tree.{0,160}invalidates",
    "implemented P3 change invalidates exact-tree evidence",
)
require_regex(
    critical,
    r"advisory.{0,180}(exact candidate evidence).{0,120}pending.{0,180}(mandatory|required).{0,100}(closure|GO)",
    "advisory candidate evidence pending until mandatory closure",
)

# Named agents retain the concrete evidence boundaries exercised by the
# behavioral corpus rather than relying on generic review wording.
require(
    ".codex/agents/test-porter.toml",
    "immediately preceding and attached to the target FuncDecl",
    "inside the function body is not function-attached evidence",
)
require(
    ".codex/agents/implementation-worker.toml",
    "exact row counts",
    "stored command result or response",
    "balanced ledger transaction and entries",
    "checkpoint",
    "outbox",
    "same-key/different-request conflict",
)
require(
    ".codex/agents/determinism-reviewer.toml",
    "committed PostgreSQL outbox records",
    "after the economic transaction commits",
    "initial authoritative snapshot",
    "durable watermark",
    "every unprovable gap",
)

# Advisory review is iterative and non-approving; exact-SHA evidence is bound
# to one tree and old GO decisions cannot move to a changed tree.
require(
    "AGENTS.md",
    "Scope, design, and failure matrix",
    "Implementation boundary",
    "Exact-SHA release candidate",
    "advisory review",
    "release approval",
)
require_regex("AGENTS.md", r"Failing\s+tests", "failing-tests checkpoint")
require_regex(
    "AGENTS.md",
    r"advisory review.{0,180}(working|current).{0,80}diff.{0,220}(not|cannot|may not).{0,80}release approval",
    "iterative advisory review over the working diff without release GO",
)
require_regex(
    "AGENTS.md",
    r"predecessor checkpoint.{0,120}(may parallelize|optional)",
    "parallel work only after its predecessor checkpoint",
)
require_regex(
    "AGENTS.md",
    r"applicable.{0,120}(migration|determinism|money).{0,160}reviewers",
    "only applicable specialist preflight reviewers",
)
require_regex(
    "AGENTS.md",
    r"full local validation.{0,180}exact SHA.{0,180}(specialist).{0,180}(final release).{0,180}(push).{0,180}(hosted CI)",
    "required validation, specialist, final, push, and CI order",
)
for relative in (
    "AGENTS.md",
    "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md",
    ".github/pull_request_template.md",
):
    require_regex(
        relative,
        r"(evidence|GO).{0,220}(must not|cannot|never).{0,120}(transfer|carry).{0,100}(changed|different).{0,60}(tree|SHA)",
        "exact-tree evidence non-transfer rule",
    )
require_regex(
    ".codex/agents/release-reviewer.toml",
    r"(independent).{0,160}(read-only).{0,220}(exact|stable).{0,80}(SHA|candidate)",
    "independent read-only exact-candidate release review",
)
require(
    ".github/pull_request_template.md",
    "Advisory closure tree:",
    "Full-validation SHA:",
    "Specialist review SHA:",
    "Final review SHA:",
    "Hosted CI SHA:",
    "40-character",
)
require_regex(
    ".github/pull_request_template.md",
    r"changed.{0,80}(tree|SHA).{0,220}invalidates.{0,240}advisory.{0,120}validation.{0,120}specialist.{0,120}final",
    "all review and validation evidence invalidated by a changed tree",
)

# Full validation happens once after advisory blockers close on a stable
# candidate, not on every implementation edit.
require_regex(
    "AGENTS.md",
    r"(once|one).{0,180}full.{0,80}validation.{0,220}(stable|stabilized).{0,80}(candidate|tree)",
    "one full validation pass on the stable candidate",
)
require_regex(
    "AGENTS.md",
    r"advisory blockers.{0,120}(close|closed).{0,220}(one|once).{0,140}full.{0,80}validation",
    "advisory blocker closure before full validation",
)
require_regex(
    "AGENTS.md",
    r"(changed|different).{0,80}(candidate|tree).{0,180}(invalidates).{0,120}(all).{0,120}(evidence).{0,160}(repeat|rerun).{0,120}(every|required|complete).{0,120}full-validation.{0,160}(specialist|final)",
    "complete full validation and exact-SHA review repetition for a changed candidate tree",
)
require_regex(
    ".github/pull_request_template.md",
    r"Before full validation.{0,160}focused affected checks.{0,220}After full validation.{0,220}(changed tree|tree or SHA).{0,160}invalidates.{0,160}advisory.{0,100}validation.{0,100}specialist.{0,100}final.{0,160}(complete|required).{0,80}full-validation.{0,180}fresh exact-SHA",
    "focused pre-candidate checks cannot replace complete post-validation reruns",
)
require_regex(
    "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md",
    r"After full validation.{0,220}(changed tree|tree).{0,180}(complete|required).{0,80}full-validation.{0,180}fresh.{0,80}exact-SHA",
    "critical review requires fresh full evidence after a stable-tree change",
)
require_regex(
    "AGENTS.md",
    r"(smallest|narrowest).{0,120}(test|validation).{0,260}(during|while).{0,80}implementation",
    "focused implementation validation",
)

# The migration boundary distinguishes an unshared candidate and disposable
# test database from frozen protected/shared history.
for relative in ("AGENTS.md", "migrations/AGENTS.md", "migrations/README.md", "DATABASE.md"):
    require_regex(relative, r"protected branch", "protected-branch freeze")
    require_regex(relative, r"shared or persistent", "shared/persistent freeze")
    require_regex(relative, r"disposable", "disposable database exception")
    require_regex(relative, r"forward\s+migration", "forward migration correction")
require_regex(
    "migrations/AGENTS.md",
    r"(unpublished|unlanded).{0,160}(edit|rename).{0,160}(squash)",
    "mutable unpublished migration candidate",
)
require(
    "migrations/AGENTS.md",
    "Git policy cannot infer this external fact",
)

# The PR captures the lightweight workflow metrics and exact candidate state.
require(
    ".github/pull_request_template.md",
    "Blockers found before implementation:",
    "Blockers found after full validation:",
    "Exact-SHA candidate count:",
    "Implementation time:",
    "Review/test wait time:",
    "Candidate SHA:",
    "Candidate tree:",
    "Base SHA:",
    "Shared/persistent migration application:",
)

# Machine-readable policy, templates, and every custom agent retain the exact
# Sol pin and the established full-access runtime for editing agents.
policy_path = ROOT / "policy/openai-agent-policy.json"
if policy_path.is_file():
    try:
        policy = json.loads(policy_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        failures.append(f"policy/openai-agent-policy.json: invalid JSON: {exc}")
    else:
        if policy.get("model") != PINNED_MODEL:
            failures.append("policy/openai-agent-policy.json: wrong model pin")
        templates = policy.get("templates", {})
        if templates.get("adversarial_preflight") != (
            "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md"
        ):
            failures.append(
                "policy/openai-agent-policy.json: preflight template is not canonical"
            )

expected_agents = {
    "determinism_reviewer",
    "implementation_worker",
    "inventory",
    "migration_reviewer",
    "money_reviewer",
    "release_reviewer",
    "test_porter",
}
found_agents: set[str] = set()
for path in sorted((ROOT / ".codex/agents").glob("*.toml")):
    try:
        agent = tomllib.loads(path.read_text(encoding="utf-8"))
    except tomllib.TOMLDecodeError as exc:
        failures.append(f"{path.relative_to(ROOT)}: invalid TOML: {exc}")
        continue
    if agent.get("model") != PINNED_MODEL:
        failures.append(f"{path.relative_to(ROOT)}: wrong model pin")
    name = agent.get("name")
    if isinstance(name, str):
        found_agents.add(name)
if found_agents != expected_agents:
    failures.append(
        f".codex/agents: expected {sorted(expected_agents)}, got {sorted(found_agents)}"
    )

require(
    ".codex/config.toml",
    f'model = "{PINNED_MODEL}"',
    f'review_model = "{PINNED_MODEL}"',
    f'default_subagent_model = "{PINNED_MODEL}"',
    'approval_policy = "never"',
    'sandbox_mode = "danger-full-access"',
)
require(
    ".github/workflows/ci.yml",
    "github.event.before",
    "github.event.pull_request.base.sha",
    "github.event.pull_request.head.sha",
)
ci_workflow = read(".github/workflows/ci.yml")
checkout_count = ci_workflow.count("uses: actions/checkout@")
head_checkout_count = ci_workflow.count("github.event.pull_request.head.sha")
if checkout_count == 0 or head_checkout_count < checkout_count:
    failures.append(
        ".github/workflows/ci.yml: every checkout must select the exact PR head SHA"
    )

if failures:
    for failure in failures:
        print(f"WORKFLOW POLICY REGRESSION: {failure}", file=sys.stderr)
    raise SystemExit(1)

print("agent workflow policy regressions passed")
