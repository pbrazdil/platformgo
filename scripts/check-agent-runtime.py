#!/usr/bin/env python3
"""Validate the GPT-5.6 Sol-only Codex and Responses API policy."""

from __future__ import annotations

import json
import pathlib
import re
import sys
import tomllib
from typing import Any

ROOT = pathlib.Path(__file__).resolve().parents[1]
PINNED_MODEL = "gpt-5.6-sol"
POLICY_PATH = ROOT / "policy" / "openai-agent-policy.json"
CODEX_CONFIG = ROOT / ".codex" / "config.toml"
AGENT_DIR = ROOT / ".codex" / "agents"

ALLOWED_API_EFFORTS = {"none", "low", "medium", "high", "xhigh", "max"}
ALLOWED_CODEX_EFFORTS = {"minimal", "low", "medium", "high", "xhigh"}
ALLOWED_CONTEXTS = {"auto", "all_turns", "current_turn"}
ALLOWED_VERBOSITY = {"low", "medium", "high"}
ALLOWED_SANDBOX = {"read-only", "danger-full-access"}

EXPECTED_API_PROFILES = {
    "inventory": ("medium", "current_turn", None, "low"),
    "implementation": ("high", "all_turns", None, "medium"),
    "critical-review": ("xhigh", "current_turn", "pro", "high"),
    "release-gate": ("max", "current_turn", "pro", "high"),
}

EXPECTED_CODEX_AGENTS = {
    "determinism_reviewer": ("xhigh", "high", "read-only"),
    "implementation_worker": ("high", "medium", "danger-full-access"),
    "inventory": ("medium", "low", "read-only"),
    "migration_reviewer": ("xhigh", "high", "read-only"),
    "money_reviewer": ("xhigh", "high", "read-only"),
    "release_reviewer": ("xhigh", "high", "read-only"),
    "test_porter": ("high", "medium", "danger-full-access"),
}

WORD_BUDGETS = {
    "AGENTS.md": 1800,
    "MODEL_POLICY.md": 1300,
    "docs/AGENT_TASK_TEMPLATE.md": 400,
    "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md": 450,
    "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md": 450,
}

STALE_REFERENCES = {
    "agent/TASK_TEMPLATE.md",
    "agent/CRITICAL_REVIEW_TEMPLATE.md",
    "agent/profiles/",
}


def fail(message: str) -> None:
    print(f"AGENT RUNTIME POLICY ERROR: {message}", file=sys.stderr)
    raise SystemExit(1)


def load_json(path: pathlib.Path) -> dict[str, Any]:
    if not path.is_file():
        fail(f"missing required file: {path.relative_to(ROOT)}")
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"cannot parse {path.relative_to(ROOT)}: {exc}")
    if not isinstance(value, dict):
        fail(f"{path.relative_to(ROOT)} must contain a JSON object")
    return value


def load_toml(path: pathlib.Path) -> dict[str, Any]:
    if not path.is_file():
        fail(f"missing required file: {path.relative_to(ROOT)}")
    try:
        value = tomllib.loads(path.read_text(encoding="utf-8"))
    except (OSError, tomllib.TOMLDecodeError) as exc:
        fail(f"cannot parse {path.relative_to(ROOT)}: {exc}")
    if not isinstance(value, dict):
        fail(f"{path.relative_to(ROOT)} must contain a TOML table")
    return value


def require_fragments(path: pathlib.Path, fragments: list[str]) -> None:
    if not path.is_file():
        fail(f"missing required file: {path.relative_to(ROOT)}")
    text = path.read_text(encoding="utf-8")
    for fragment in fragments:
        if fragment not in text:
            fail(f"{path.relative_to(ROOT)} must contain {fragment!r}")


def contains_fallback_key(value: Any) -> bool:
    if isinstance(value, dict):
        return any(
            "fallback" in str(key).lower() or contains_fallback_key(child)
            for key, child in value.items()
        )
    if isinstance(value, list):
        return any(contains_fallback_key(child) for child in value)
    return False


def validate_api_policy() -> None:
    policy = load_json(POLICY_PATH)
    expected_scalars = {
        "schema_version": 2,
        "provider": "openai",
        "api": "responses",
        "model": PINNED_MODEL,
        "allowed_models": [PINNED_MODEL],
        "allow_aliases": False,
        "allow_model_fallback": False,
        "fail_on_model_unavailable": True,
        "verify_response_model": True,
    }
    for key, expected in expected_scalars.items():
        if policy.get(key) != expected:
            fail(f"{POLICY_PATH.relative_to(ROOT)}: {key} must be {expected!r}")

    if policy.get("codex_runtime") != {
        "approval_policy": "never",
        "default_sandbox_mode": "danger-full-access",
        "editing_agent_sandbox_mode": "danger-full-access",
        "read_only_agent_sandbox_mode": "read-only",
    }:
        fail("Codex runtime must enforce never approvals and full access for editing agents")

    profiles = policy.get("profiles")
    if not isinstance(profiles, dict) or set(profiles) != set(EXPECTED_API_PROFILES):
        fail("Responses API profile set does not match the approved policy")

    for name, expected in EXPECTED_API_PROFILES.items():
        profile = profiles.get(name)
        if not isinstance(profile, dict):
            fail(f"missing or invalid Responses API profile: {name}")
        reasoning = profile.get("reasoning")
        text = profile.get("text")
        if not isinstance(reasoning, dict) or not isinstance(text, dict):
            fail(f"profile {name} must contain reasoning and text objects")
        actual = (
            reasoning.get("effort"),
            reasoning.get("context"),
            reasoning.get("mode"),
            text.get("verbosity"),
        )
        if actual != expected:
            fail(f"invalid {name} profile: expected {expected}, got {actual}")
        if actual[0] not in ALLOWED_API_EFFORTS:
            fail(f"unsupported reasoning effort in profile {name}")
        if actual[1] not in ALLOWED_CONTEXTS:
            fail(f"unsupported reasoning context in profile {name}")
        if actual[2] not in {None, "pro"}:
            fail(f"unsupported reasoning mode in profile {name}")
        if actual[3] not in ALLOWED_VERBOSITY:
            fail(f"unsupported text verbosity in profile {name}")
        if "model" in profile:
            fail(f"profile {name} must inherit the single top-level model pin")

    if profiles["release-gate"].get("requires_explicit_owner_authorization") is not True:
        fail("release-gate profile must require explicit owner authorization")

    if policy.get("prompt_cache") != {
        "mode": "implicit",
        "stable_prefix": ["AGENTS.md", "MODEL_POLICY.md"],
    }:
        fail("prompt_cache policy must use the approved implicit stable prefix")

    if policy.get("templates") != {
        "task": "docs/AGENT_TASK_TEMPLATE.md",
        "adversarial_preflight": "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md",
        "critical_review": "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md",
    }:
        fail("canonical prompt template paths are not pinned")

    if policy.get("evaluation") != {
        "policy": "AGENT_EVALS.md",
        "fixtures": "testdata/agent-evals",
        "reports": "docs/agent-evals",
    }:
        fail("agent evaluation paths are not pinned")

    ptc = policy.get("programmatic_tool_calling")
    if ptc != {
        "policy": "bounded-only",
        "prohibited_for": [
            "edits",
            "approvals",
            "semantic-conflict-resolution",
            "final-validation",
        ],
    }:
        fail("Programmatic Tool Calling policy does not match the approved bounded profile")


def validate_codex_config() -> None:
    config = load_toml(CODEX_CONFIG)
    expected = {
        "model": PINNED_MODEL,
        "review_model": PINNED_MODEL,
        "model_reasoning_effort": "high",
        "model_verbosity": "medium",
        "approval_policy": "never",
        "sandbox_mode": "danger-full-access",
        "web_search": "cached",
        "personality": "pragmatic",
    }
    for key, value in expected.items():
        if config.get(key) != value:
            fail(f".codex/config.toml must set {key} = {value!r}")

    if contains_fallback_key(config):
        fail(".codex/config.toml contains a prohibited fallback setting")

    agents = config.get("agents")
    if not isinstance(agents, dict):
        fail(".codex/config.toml must define [agents]")
    expected_agents = {
        "enabled": True,
        "max_concurrent_threads_per_session": 4,
        "default_subagent_model": PINNED_MODEL,
        "default_subagent_reasoning_effort": "high",
    }
    for key, value in expected_agents.items():
        if agents.get(key) != value:
            fail(f".codex/config.toml [agents] must set {key} = {value!r}")


def validate_custom_agents() -> None:
    if not AGENT_DIR.is_dir():
        fail("missing .codex/agents directory")

    found: dict[str, pathlib.Path] = {}
    for path in sorted(AGENT_DIR.glob("*.toml")):
        data = load_toml(path)
        name = data.get("name")
        if not isinstance(name, str) or not name:
            fail(f"{path.relative_to(ROOT)} must define a non-empty name")
        if name in found:
            fail(f"duplicate custom agent name {name!r}")
        found[name] = path

        for key in ("description", "developer_instructions"):
            value = data.get(key)
            if not isinstance(value, str) or not value.strip():
                fail(f"{path.relative_to(ROOT)} must define {key}")
        if data.get("model") != PINNED_MODEL:
            fail(f"{path.relative_to(ROOT)} must use {PINNED_MODEL}")
        if data.get("model_reasoning_effort") not in ALLOWED_CODEX_EFFORTS:
            fail(f"{path.relative_to(ROOT)} has unsupported Codex reasoning effort")
        if data.get("model_verbosity") not in ALLOWED_VERBOSITY:
            fail(f"{path.relative_to(ROOT)} has unsupported model_verbosity")
        if data.get("sandbox_mode") not in ALLOWED_SANDBOX:
            fail(f"{path.relative_to(ROOT)} has unsupported sandbox_mode")
        if contains_fallback_key(data):
            fail(f"{path.relative_to(ROOT)} contains a prohibited fallback setting")

        instruction_words = len(str(data["developer_instructions"]).split())
        if instruction_words > 120:
            fail(
                f"{path.relative_to(ROOT)} developer_instructions are too long "
                f"({instruction_words} words; max 120)"
            )

    if set(found) != set(EXPECTED_CODEX_AGENTS):
        fail(
            "custom agent set mismatch: expected "
            f"{sorted(EXPECTED_CODEX_AGENTS)}, got {sorted(found)}"
        )

    for name, expected in EXPECTED_CODEX_AGENTS.items():
        data = load_toml(found[name])
        actual = (
            data.get("model_reasoning_effort"),
            data.get("model_verbosity"),
            data.get("sandbox_mode"),
        )
        if actual != expected:
            fail(f"{name} must use effort/verbosity/sandbox {expected}, got {actual}")


def validate_no_unapproved_model_assignments() -> None:
    assignment = re.compile(
        r"(?m)^\s*(?:model|review_model|default_subagent_model)\s*=\s*[\"']([^\"']+)[\"']"
    )
    for path in [CODEX_CONFIG, *sorted(AGENT_DIR.glob("*.toml"))]:
        text = path.read_text(encoding="utf-8")
        for match in assignment.finditer(text):
            if match.group(1) != PINNED_MODEL:
                fail(
                    f"unapproved model assignment {match.group(1)!r} "
                    f"in {path.relative_to(ROOT)}"
                )

    # Machine-readable configuration may mention only the exact model slug.
    model_slug = re.compile(r"gpt-[A-Za-z0-9._-]+")
    config_paths = [
        POLICY_PATH,
        CODEX_CONFIG,
        *sorted(AGENT_DIR.glob("*.toml")),
    ]
    for path in config_paths:
        for slug in model_slug.findall(path.read_text(encoding="utf-8")):
            if slug != PINNED_MODEL:
                fail(f"unapproved model slug {slug!r} in {path.relative_to(ROOT)}")



EXECUTABLE_CONFIG_SUFFIXES = {
    ".go", ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx",
    ".toml", ".json", ".yaml", ".yml", ".sh",
}
OPENAI_MODEL_LITERAL = re.compile(
    r"[\"'](gpt-[A-Za-z0-9._-]+|o[1-9](?:-[A-Za-z0-9._-]+)?|codex-[A-Za-z0-9._-]+)[\"']"
)


def validate_executable_model_literals() -> None:
    """Reject alternate OpenAI model IDs in executable code and configuration."""
    ignored_parts = {".git", "vendor", "node_modules"}
    for path in ROOT.rglob("*"):
        if not path.is_file() or path.suffix not in EXECUTABLE_CONFIG_SUFFIXES:
            continue
        if any(part in ignored_parts for part in path.parts):
            continue
        text = path.read_text(encoding="utf-8", errors="ignore")
        for match in OPENAI_MODEL_LITERAL.finditer(text):
            slug = match.group(1)
            if slug != PINNED_MODEL:
                fail(
                    f"unapproved OpenAI model literal {slug!r} "
                    f"in {path.relative_to(ROOT)}"
                )

def validate_docs() -> None:
    required = {
        "MODEL_POLICY.md": [
            PINNED_MODEL,
            "Responses API",
            "reasoning.context",
            "text.verbosity",
            "reasoning.mode: \"pro\"",
            "Lean prompt policy",
            "Autonomy and authority boundaries",
            'approval_policy = "never"',
            '"danger-full-access"',
            "Programmatic Tool Calling",
            "docs/AGENT_TASK_TEMPLATE.md",
            "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md",
            "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md",
            "AGENT_EVALS.md",
            "https://developers.openai.com/api/docs/guides/latest-model",
            "https://developers.openai.com/api/docs/models/gpt-5.6-sol",
        ],
        "AGENTS.md": [
            PINNED_MODEL,
            "MODEL_POLICY.md",
            "docs/AGENT_TASK_TEMPLATE.md",
            "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md",
            "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md",
        ],
        "docs/AGENT_TASK_TEMPLATE.md": [
            "Goal:",
            "Scope and ownership:",
            "Required evidence:",
            "Success criteria:",
            "Authority boundary:",
            "Validation:",
            "Deliverables:",
        ],
        "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md": [
            "durable PostgreSQL",
            "Authority and transaction boundary:",
            "Lock order and writer ownership:",
            "Duplicate delivery and lost acknowledgment:",
            "Representative current-main upgrade:",
            "Rollback and unknown commit:",
            "Hostile default privileges",
            "Tests that must fail first:",
            "Stop conditions and owner decisions:",
            "docs-only",
        ],
        "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md": [
            PINNED_MODEL,
            "Required evidence:",
            "Executable failure sequence",
            "P0/P1",
            "P2",
            "P3",
            "Minimum fix property",
            "Exact candidate evidence",
            "go/no-go",
            "Read-only",
        ],
        "AGENT_EVALS.md": [
            PINNED_MODEL,
            "testdata/agent-evals/",
            "docs/agent-evals/",
            "Pass criteria",
            "Measurements",
            "Programmatic Tool Calling",
        ],
        "docs/TEST_PORTING_PLAYBOOK.md": [
            PINNED_MODEL,
            "docs/AGENT_TASK_TEMPLATE.md",
            ".codex/agents/test-porter.toml",
        ],
        "REFERENCES.md": [
            "https://developers.openai.com/api/docs/guides/latest-model",
            "https://developers.openai.com/api/docs/models/gpt-5.6-sol",
            "https://developers.openai.com/codex/config-file/config-reference",
            "https://developers.openai.com/codex/agent-configuration/subagents",
        ],
    }
    for relative, fragments in required.items():
        require_fragments(ROOT / relative, fragments)

    runtime_policy_docs = [
        ROOT / "AGENTS.md",
        ROOT / "MODEL_POLICY.md",
        ROOT / "README_POLICY_PACK.md",
        ROOT / "docs" / "AGENT_TASK_TEMPLATE.md",
        ROOT / "docs" / "AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md",
        ROOT / "docs" / "AGENT_CRITICAL_REVIEW_TEMPLATE.md",
    ]
    for path in runtime_policy_docs:
        text = path.read_text(encoding="utf-8")
        for prohibited in ("on-request", "workspace-write"):
            if prohibited in text:
                fail(
                    f"stale restricted runtime policy {prohibited!r} "
                    f"in {path.relative_to(ROOT)}"
                )

    require_fragments(
        ROOT / "testdata" / "agent-evals" / "README.md",
        ["tasks/", "docs/agent-evals/", PINNED_MODEL],
    )
    require_fragments(
        ROOT / "docs" / "agent-evals" / "README.md",
        [PINNED_MODEL, "reasoning effort", "standard/pro mode"],
    )

    docs_to_scan = [
        ROOT / "AGENTS.md",
        ROOT / "MODEL_POLICY.md",
        ROOT / "README_POLICY_PACK.md",
        ROOT / "CONTRIBUTING.md",
        ROOT / "docs" / "TEST_PORTING_PLAYBOOK.md",
    ]
    for path in docs_to_scan:
        text = path.read_text(encoding="utf-8")
        for stale in STALE_REFERENCES:
            if stale in text:
                fail(f"stale agent-policy reference {stale!r} in {path.relative_to(ROOT)}")

    playbook = (ROOT / "docs" / "TEST_PORTING_PLAYBOOK.md").read_text(encoding="utf-8")
    if "## Task brief template" in playbook or "## Master prompt" in playbook:
        fail("test-porting playbook must use the canonical task template, not duplicate it")


def validate_instruction_budgets() -> None:
    for relative, maximum in WORD_BUDGETS.items():
        path = ROOT / relative
        if not path.is_file():
            fail(f"missing required file: {relative}")
        count = len(path.read_text(encoding="utf-8").split())
        if count > maximum:
            fail(f"{relative} is {count} words; lean-prompt budget is {maximum}")

    for path in sorted(ROOT.rglob("AGENTS.md")):
        if path == ROOT / "AGENTS.md":
            continue
        count = len(path.read_text(encoding="utf-8").split())
        if count > 600:
            fail(
                f"{path.relative_to(ROOT)} is {count} words; nested AGENTS.md budget is 600"
            )


def main() -> None:
    validate_api_policy()
    validate_codex_config()
    validate_custom_agents()
    validate_no_unapproved_model_assignments()
    validate_executable_model_literals()
    validate_docs()
    validate_instruction_budgets()
    print("agent runtime policy checks passed")


if __name__ == "__main__":
    main()
