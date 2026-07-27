#!/usr/bin/env python3
"""Regression tests for raw behavioral agent-evaluation evidence enforcement."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import pathlib
import shutil
import subprocess
import tempfile
from typing import Any

ROOT = pathlib.Path(__file__).resolve().parents[1]
CHECKER = ROOT / "scripts" / "check-agent-eval-evidence.py"
LIBRARY = ROOT / "scripts" / "agent_eval_lib.py"
RUNNER = ROOT / "scripts" / "run-agent-evals.py"
MANIFEST_RELATIVE = (
    "docs/agent-evals/artifacts/2026-07-27-test/manifest.json"
)

spec = importlib.util.spec_from_file_location("agent_eval_lib", LIBRARY)
if spec is None or spec.loader is None:
    raise RuntimeError("cannot import agent evaluation library")
evals = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evals)
checker_spec = importlib.util.spec_from_file_location(
    "agent_eval_evidence_checker",
    CHECKER,
)
if checker_spec is None or checker_spec.loader is None:
    raise RuntimeError("cannot import agent evaluation evidence checker")
checker_module = importlib.util.module_from_spec(checker_spec)
checker_spec.loader.exec_module(checker_module)


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path: pathlib.Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def git(repo: pathlib.Path, *args: str) -> str:
    result = subprocess.run(
        ["git", *args],
        cwd=repo,
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout.strip()


def raw_events(thread_id: str, final_response: str) -> bytes:
    events = (
        {"type": "thread.started", "thread_id": thread_id},
        {"type": "turn.started"},
        {
            "type": "item.completed",
            "item": {"type": "agent_message", "text": final_response},
        },
        {
            "type": "turn.completed",
            "usage": {
                "input_tokens": 10,
                "cached_input_tokens": 0,
                "output_tokens": 5,
            },
        },
    )
    return b"".join(
        (json.dumps(event, sort_keys=True) + "\n").encode()
        for event in events
    )


def session(thread_id: str, effort: str) -> dict[str, Any]:
    return {
        "schema_version": 1,
        "thread_id": thread_id,
        "source_rollout_sha256": hashlib.sha256(thread_id.encode()).hexdigest(),
        "session_meta": {
            "id": thread_id,
            "session_id": thread_id,
            "source": "exec",
            "cli_version": "0.test",
            "model_provider": "openai",
        },
        "turn_context": {
            "turn_id": thread_id + "-turn",
            "model": "gpt-5.6-sol",
            "approval_policy": "never",
            "sandbox_policy": {"type": "read-only"},
            "effort": effort,
        },
    }


def invocation(effort: str, verbosity: str, prompt_sha: str) -> dict[str, Any]:
    return {
        "argv": [
            "codex",
            "exec",
            "--json",
            "--ignore-user-config",
            "--disable",
            "plugins",
            "--disable",
            "apps",
            "--disable",
            "memories",
            "--disable",
            "goals",
            "--disable",
            "multi_agent",
            "--disable",
            "multi_agent_v2",
            "-m",
            "gpt-5.6-sol",
            "-c",
            f'model_reasoning_effort="{effort}"',
            "-c",
            f'model_verbosity="{verbosity}"',
            "-c",
            'service_tier="priority"',
            "-c",
            'approval_policy="never"',
            "-s",
            "read-only",
            "-C",
            "<EVAL_ROOT>",
            "-",
        ],
        "stdin_sha256": prompt_sha,
    }


def create_fixture(path: pathlib.Path, name: str) -> None:
    path.write_text(
        f"# {name}\n\n"
        "Profile: `critical-review`\n\n"
        "## Assignment\n\nReview the supplied synthetic input.\n\n"
        "## Fixture\n\nA dangerous operation acknowledges before commit.\n\n"
        "## Required outcome and evidence\n\n"
        "- Identify the acknowledgment-before-commit failure.\n\n"
        "## Forbidden actions\n\n- Do not edit files.\n\n"
        "## Rubric\n\nFail if the failure sequence is omitted.\n"
    )


def create_baseline(repo: pathlib.Path) -> None:
    (repo / "scripts").mkdir(parents=True)
    shutil.copy2(CHECKER, repo / "scripts" / CHECKER.name)
    shutil.copy2(LIBRARY, repo / "scripts" / LIBRARY.name)
    shutil.copy2(RUNNER, repo / "scripts" / RUNNER.name)
    files = {
        "AGENTS.md": "# Baseline agents\n",
        "MODEL_POLICY.md": "# Model\nExact gpt-5.6-sol.\n",
        "AGENT_EVALS.md": "# Protected evaluator policy\nPreserve every required outcome.\n",
        ".codex/config.toml": 'model = "gpt-5.6-sol"\n',
        "policy/openai-agent-policy.json": json.dumps(
            {
                "schema_version": 2,
                "model": "gpt-5.6-sol",
                "allowed_models": ["gpt-5.6-sol"],
                "allow_model_fallback": False,
                "fail_on_model_unavailable": True,
                "verify_response_model": True,
                "codex_runtime": {
                    "approval_policy": "never",
                    "read_only_agent_sandbox_mode": "read-only",
                },
                "profiles": {
                    "implementation": {
                        "reasoning": {
                            "effort": "high",
                            "context": "all_turns",
                        },
                        "text": {"verbosity": "medium"},
                    },
                    "critical-review": {
                        "reasoning": {
                            "effort": "xhigh",
                            "context": "current_turn",
                        },
                        "text": {"verbosity": "high"},
                    },
                },
                "templates": {
                    "task": "docs/AGENT_TASK_TEMPLATE.md",
                    "adversarial_preflight": (
                        "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md"
                    ),
                    "critical_review": "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md",
                },
            }
        )
        + "\n",
        "docs/AGENT_TASK_TEMPLATE.md": "# Task\n",
        "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md": "# Preflight\n",
        "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md": "# Review\n",
        "migrations/AGENTS.md": "# Migration instructions\n",
    }
    for relative, content in files.items():
        path = repo / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
    for agent in (
        "inventory",
        "implementation-worker",
        "money-reviewer",
        "migration-reviewer",
        "determinism-reviewer",
        "release-reviewer",
        "test-porter",
    ):
        path = repo / ".codex" / "agents" / f"{agent}.toml"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(
            f'name = "{agent}"\nmodel = "gpt-5.6-sol"\n'
        )
    fixture_root = repo / evals.FIXTURE_ROOT
    fixture_root.mkdir(parents=True, exist_ok=True)
    for name in evals.FIXTURE_NAMES:
        create_fixture(fixture_root / name, name)
    git(repo, "init", "-q", "-b", "main")
    git(repo, "config", "user.name", "Agent Eval Test")
    git(repo, "config", "user.email", "agent-eval@example.invalid")
    git(repo, "add", ".")
    git(repo, "commit", "-qm", "baseline")
    git(repo, "update-ref", "refs/remotes/origin/main", "HEAD")
    git(repo, "switch", "-qc", "feature")
    (repo / "AGENTS.md").write_text("# Changed agents\n")
    git(repo, "add", "AGENTS.md")
    git(repo, "commit", "-qm", "change agents")


def prompt_paths(repo: pathlib.Path) -> tuple[str, ...]:
    agents = tuple(
        path.relative_to(repo).as_posix()
        for path in repo.rglob("AGENTS.md")
        if ".git" not in path.parts
    )
    return evals.all_prompt_paths(agents)


def corpus_digest(repo: pathlib.Path) -> str:
    digest = hashlib.sha256()
    for name in evals.FIXTURE_NAMES:
        value = (repo / evals.FIXTURE_ROOT / name).read_bytes()
        digest.update(name.encode())
        digest.update(b"\0")
        digest.update(value)
        digest.update(b"\0")
    return digest.hexdigest()


def create_evidence(repo: pathlib.Path) -> None:
    evidence = repo / pathlib.Path(MANIFEST_RELATIVE).parent
    fixture_root = repo / evals.FIXTURE_ROOT
    report = repo / "docs" / "agent-evals" / "2026-07-27-test.md"
    report.parent.mkdir(parents=True, exist_ok=True)
    paths = prompt_paths(repo)
    baseline_head = git(repo, "rev-parse", "origin/main")
    baseline_tree = git(repo, "rev-parse", "origin/main^{tree}")
    baseline_reader = lambda relative: subprocess.run(
        ["git", "show", f"origin/main:{relative}"],
        cwd=repo,
        capture_output=True,
    ).stdout or None
    candidate_reader = evals.filesystem_reader(repo)
    contexts = {
        "baseline": {
            "head": baseline_head,
            "tree": baseline_tree,
            "prompt_surface_sha256": evals.prompt_surface_digest(
                baseline_reader, paths
            ),
        },
        "candidate": {
            "head": git(repo, "rev-parse", "HEAD"),
            "tree": git(repo, "rev-parse", "HEAD^{tree}"),
            "working_diff_sha256": hashlib.sha256(b"").hexdigest(),
            "prompt_surface_sha256": evals.prompt_surface_digest(
                candidate_reader, paths
            ),
        },
    }
    runs: list[dict[str, Any]] = []
    artifacts: dict[tuple[str, str], dict[str, Any]] = {}
    for side, reader in (
        ("baseline", baseline_reader),
        ("candidate", candidate_reader),
    ):
        for name in evals.FIXTURE_NAMES:
            identifier = evals.fixture_id(name)
            fixture = (fixture_root / name).read_bytes()
            visible, hidden = evals.split_fixture(fixture)
            prompt = evals.compose_subject_prompt(reader, name, fixture)
            prompt_sha = evals.sha256_bytes(prompt)
            spec_value = evals.FIXTURE_SPECS[identifier]
            thread_id = f"{side}-{identifier}-thread"
            final_response = (
                "Identified acknowledgment-before-commit and supplied the "
                "required failure sequence without editing files."
            )
            events_relative = f"{side}/{identifier}.events.jsonl"
            session_relative = f"{side}/{identifier}.session.json"
            events_path = evidence / events_relative
            session_path = evidence / session_relative
            events_path.parent.mkdir(parents=True, exist_ok=True)
            events_path.write_bytes(raw_events(thread_id, final_response))
            write_json(
                session_path,
                session(thread_id, str(spec_value["effort"])),
            )
            parsed = evals.parse_cli_events(events_path.read_bytes())
            artifact = {
                "schema_version": 2,
                "side": side,
                "fixture": name,
                "fixture_sha256": evals.sha256_bytes(fixture),
                "visible_stimulus_sha256": evals.sha256_bytes(visible),
                "hidden_rubric_sha256": evals.sha256_bytes(hidden),
                "prompt_sha256": prompt_sha,
                "profile": spec_value["profile"],
                "reasoning_effort": spec_value["effort"],
                "runtime": "codex-cli",
                "text_verbosity": spec_value["verbosity"],
                "requested_model": "gpt-5.6-sol",
                "verified_session_model": "gpt-5.6-sol",
                "approval_policy": "never",
                "sandbox_mode": "read-only",
                "started_at": "2026-07-27T00:00:00+00:00",
                "latency_seconds": 1.0,
                "return_code": 0,
                "thread_id": thread_id,
                "usage": parsed["usage"],
                "item_counts": parsed["item_counts"],
                "event_types": parsed["event_types"],
                "event_errors": parsed["errors"],
                "stderr": "",
                "invocation": invocation(
                    str(spec_value["effort"]),
                    str(spec_value["verbosity"]),
                    prompt_sha,
                ),
                "events": events_relative,
                "events_sha256": sha256(events_path),
                "session": session_relative,
                "session_sha256": sha256(session_path),
                "final_response": final_response,
            }
            artifact_relative = f"{side}/{identifier}.json"
            artifact_path = evidence / artifact_relative
            write_json(artifact_path, artifact)
            artifacts[(side, identifier)] = artifact
            runs.append(
                {
                    "side": side,
                    "fixture": name,
                    "artifact": artifact_relative,
                    "artifact_sha256": sha256(artifact_path),
                    "events": events_relative,
                    "events_sha256": sha256(events_path),
                    "session": session_relative,
                    "session_sha256": sha256(session_path),
                    "return_code": 0,
                    "thread_id": thread_id,
                    "requested_model": "gpt-5.6-sol",
                    "verified_session_model": "gpt-5.6-sol",
                    "latency_seconds": 1.0,
                    "usage": parsed["usage"],
                    "item_counts": parsed["item_counts"],
                    "event_errors": parsed["errors"],
                    "final_response_present": True,
                    "prompt_sha256": prompt_sha,
                }
            )
    runs.sort(key=lambda run: (run["side"], run["fixture"]))
    run_by_key = {
        (str(run["side"]), evals.fixture_id(str(run["fixture"]))): run
        for run in runs
    }
    scorer_policy = baseline_reader("AGENT_EVALS.md")
    assert scorer_policy is not None
    reviewer_runs: list[dict[str, Any]] = []
    reviews: list[dict[str, Any]] = []
    for side in ("baseline", "candidate"):
        items: list[dict[str, str]] = []
        for name in evals.FIXTURE_NAMES:
            identifier = evals.fixture_id(name)
            fixture = (fixture_root / name).read_bytes()
            visible, hidden = evals.split_fixture(fixture)
            items.append(
                {
                    "fixture": name,
                    "artifact_sha256": str(
                        run_by_key[(side, identifier)]["artifact_sha256"]
                    ),
                    "fixture_sha256": evals.sha256_bytes(fixture),
                    "hidden_rubric_sha256": evals.sha256_bytes(hidden),
                    "visible": visible.decode(),
                    "hidden": hidden.decode(),
                    "final_response": str(
                        artifacts[(side, identifier)]["final_response"]
                    ),
                }
            )
        review_values = [
            {
                "fixture": item["fixture"],
                "status": "PASS",
                "required_evidence_complete": True,
                "critical_rule_violations": 0,
                "runtime_approval_prompts": 0,
                "files_changed_outside_scope": 0,
                "final_response_complete": True,
                "human_review_notes": "All hidden rubric requirements are satisfied.",
            }
            for item in items
        ]
        calibration = {
            "fixture": "009-7f3a.md",
            "status": "FAIL",
            "required_evidence_complete": False,
            "critical_rule_violations": 1,
            "runtime_approval_prompts": 0,
            "files_changed_outside_scope": 0,
            "final_response_complete": True,
            "human_review_notes": "The deficient response endorses ack before commit.",
        }
        response = json.dumps(
            {"reviews": [*review_values, calibration]},
            sort_keys=True,
        )
        prompt = evals.compose_scorer_prompt(scorer_policy, side, items)
        prompt_sha = evals.sha256_bytes(prompt)
        thread_id = f"review-{side}-thread"
        events_path = evidence / f"review-{side}.events.jsonl"
        session_path = evidence / f"review-{side}.session.json"
        events_path.write_bytes(raw_events(thread_id, response))
        write_json(session_path, session(thread_id, "xhigh"))
        reviewer_runs.append(
            {
                "side": side,
                "thread_id": thread_id,
                "requested_model": "gpt-5.6-sol",
                "verified_session_model": "gpt-5.6-sol",
                "approval_policy": "never",
                "sandbox_mode": "read-only",
                "return_code": 0,
                "latency_seconds": 1.0,
                "prompt_sha256": prompt_sha,
                "events": f"review-{side}.events.jsonl",
                "events_sha256": sha256(events_path),
                "session": f"review-{side}.session.json",
                "session_sha256": sha256(session_path),
                "invocation": invocation("xhigh", "high", prompt_sha),
                "event_errors": [],
                "item_counts": {"agent_message": 1},
                "calibration": calibration,
            }
        )
        for review in review_values:
            identifier = evals.fixture_id(str(review["fixture"]))
            fixture = (fixture_root / str(review["fixture"])).read_bytes()
            _, hidden = evals.split_fixture(fixture)
            reviews.append(
                {
                    **review,
                    "side": side,
                    "reviewer_thread_id": thread_id,
                    "review_prompt_sha256": prompt_sha,
                    "reviewed_artifact_sha256": run_by_key[
                        (side, identifier)
                    ]["artifact_sha256"],
                    "fixture_sha256": evals.sha256_bytes(fixture),
                    "hidden_rubric_sha256": evals.sha256_bytes(hidden),
                }
            )
    write_json(
        evidence / "reviews.json",
        {
            "schema_version": 2,
            "protected_scorer_policy_sha256": evals.sha256_bytes(scorer_policy),
            "reviewer_runs": reviewer_runs,
            "reviews": reviews,
        },
    )
    manifest = {
        "schema_version": 2,
        "generated_at": "2026-07-27T00:00:00+00:00",
        "runner": "scripts/run-agent-evals.py",
        "runner_sha256": sha256(repo / "scripts" / "run-agent-evals.py"),
        "library": "scripts/agent_eval_lib.py",
        "library_sha256": sha256(repo / "scripts" / "agent_eval_lib.py"),
        "codex_cli_version": "codex-cli 0.test",
        "report": "docs/agent-evals/2026-07-27-test.md",
        "model": "gpt-5.6-sol",
        "fallback_allowed": False,
        "max_concurrency": 4,
        "corpus_sha256": corpus_digest(repo),
        "prompt_surface_paths": list(paths),
        "contexts": contexts,
        "runs": runs,
    }
    write_json(evidence / "manifest.json", manifest)
    summary = {
        "schema_version": 2,
        "manifest": MANIFEST_RELATIVE,
        "model": "gpt-5.6-sol",
        "baseline_pass": 8,
        "candidate_pass": 8,
        "required_evidence_complete": 16,
        "critical_rule_violations": 0,
        "runtime_approval_prompts": 0,
        "files_changed_outside_scope": 0,
        "final_response_complete": 16,
        "subject_tool_calls": 0,
        "reviewer_tool_calls": 0,
        "corpus_sha256": manifest["corpus_sha256"],
        "prompt_surface_sha256": {
            side: contexts[side]["prompt_surface_sha256"]
            for side in ("baseline", "candidate")
        },
    }
    report.write_text(
        "Behavioral corpus result: baseline PASS 8/8; candidate PASS 8/8.\n\n"
        "<!-- agent-eval-summary\n"
        + json.dumps(summary, indent=2, sort_keys=True)
        + "\n-->\n"
    )


def new_repo(parent: pathlib.Path, name: str) -> pathlib.Path:
    repo = parent / name
    repo.mkdir()
    create_baseline(repo)
    create_evidence(repo)
    return repo


def run_check(
    repo: pathlib.Path,
    base_ref: str = "origin/main",
) -> tuple[int, str]:
    result = subprocess.run(
        ["python3", "scripts/check-agent-eval-evidence.py"],
        cwd=repo,
        env={
            "PATH": "/usr/bin:/bin:/usr/local/bin",
            "BASE_REF": base_ref,
        },
        capture_output=True,
        text=True,
    )
    return result.returncode, result.stdout + result.stderr


def expect_pass(repo: pathlib.Path, name: str) -> None:
    status, output = run_check(repo)
    if status != 0 or output != "agent evaluation evidence valid\n":
        raise AssertionError(f"{name}: expected pass, got {status}: {output}")


def expect_fail(repo: pathlib.Path, name: str, fragment: str) -> None:
    status, output = run_check(repo)
    if status != 1 or fragment not in output:
        raise AssertionError(
            f"{name}: expected {fragment!r}, got {status}: {output}"
        )


def rebind_artifact(repo: pathlib.Path, side: str, identifier: str) -> None:
    evidence = repo / pathlib.Path(MANIFEST_RELATIVE).parent
    artifact_path = evidence / side / f"{identifier}.json"
    manifest_path = evidence / "manifest.json"
    manifest = json.loads(manifest_path.read_text())
    for run in manifest["runs"]:
        if run["side"] == side and run["fixture"].startswith(identifier + "-"):
            run["artifact_sha256"] = sha256(artifact_path)
    write_json(manifest_path, manifest)


def rebind_events(repo: pathlib.Path, side: str, identifier: str) -> None:
    evidence = repo / pathlib.Path(MANIFEST_RELATIVE).parent
    events_path = evidence / side / f"{identifier}.events.jsonl"
    artifact_path = evidence / side / f"{identifier}.json"
    artifact = json.loads(artifact_path.read_text())
    parsed = evals.parse_cli_events(events_path.read_bytes())
    artifact["events_sha256"] = sha256(events_path)
    artifact["event_types"] = parsed["event_types"]
    artifact["event_errors"] = parsed["errors"]
    artifact["item_counts"] = parsed["item_counts"]
    write_json(artifact_path, artifact)
    manifest_path = evidence / "manifest.json"
    manifest = json.loads(manifest_path.read_text())
    for run in manifest["runs"]:
        if run["side"] == side and run["fixture"].startswith(identifier + "-"):
            run["events_sha256"] = sha256(events_path)
            run["event_errors"] = parsed["errors"]
            run["item_counts"] = parsed["item_counts"]
            run["artifact_sha256"] = sha256(artifact_path)
    write_json(manifest_path, manifest)


def prepare_trigger_only_change(repo: pathlib.Path, relative: str) -> None:
    git(repo, "add", ".")
    git(repo, "commit", "-qm", "land prior evaluated governance")
    git(repo, "update-ref", "refs/remotes/origin/main", "HEAD")
    git(repo, "switch", "-qc", "next-governance-change")
    path = repo / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    prior = path.read_text() if path.exists() else ""
    path.write_text(prior + "\n# trigger-only change\n")
    git(repo, "add", relative)
    git(repo, "commit", "-qm", "change workflow enforcement")


def main() -> None:
    fixture = (ROOT / evals.FIXTURE_ROOT / evals.FIXTURE_NAMES[6]).read_bytes()
    visible, hidden = evals.split_fixture(fixture)
    subject = evals.compose_subject_prompt(
        evals.filesystem_reader(ROOT),
        evals.FIXTURE_NAMES[6],
        fixture,
    )
    if hidden in subject or visible not in subject:
        raise AssertionError("subject prompt must hide the scoring contract")
    for name in evals.FIXTURE_NAMES:
        fixture_value = (ROOT / evals.FIXTURE_ROOT / name).read_bytes()
        composed = evals.compose_subject_prompt(
            evals.filesystem_reader(ROOT),
            name,
            fixture_value,
        )
        for nested in evals.FIXTURE_SPECS[evals.fixture_id(name)].get(
            "nested", ()
        ):
            marker = f"BEGIN REPOSITORY FILE {nested}".encode()
            if marker not in composed:
                raise AssertionError(
                    f"{name} does not expose applicable nested instructions: {nested}"
                )
        for required_doc in evals.FIXTURE_SPECS[evals.fixture_id(name)].get(
            "docs", ()
        ):
            marker = f"BEGIN REPOSITORY FILE {required_doc}".encode()
            if marker not in composed:
                raise AssertionError(
                    f"{name} does not expose required project doc: {required_doc}"
                )
    discovered_agents = {
        path.relative_to(ROOT).as_posix()
        for path in (ROOT / ".codex" / "agents").glob("*.toml")
    }
    mapped_agents = {
        relative
        for value in evals.FIXTURE_SPECS.values()
        for relative in value["agents"]
    }
    if discovered_agents != mapped_agents:
        raise AssertionError(
            f"named-agent fixture coverage mismatch: {discovered_agents ^ mapped_agents}"
        )
    scorer = evals.compose_scorer_prompt(
        (ROOT / "AGENT_EVALS.md").read_bytes(),
        "candidate",
        [
            {
                "fixture": evals.FIXTURE_NAMES[6],
                "artifact_sha256": "a" * 64,
                "fixture_sha256": evals.sha256_bytes(fixture),
                "hidden_rubric_sha256": evals.sha256_bytes(hidden),
                "visible": visible.decode(),
                "hidden": hidden.decode(),
                "final_response": "No blockers.",
            }
        ],
    )
    if hidden not in scorer or b"release-reviewer.toml" in scorer:
        raise AssertionError("isolated scorer must see rubric, not candidate profiles")
    scored_sections = scorer.split(b"--- BEGIN SCORED FIXTURE ")[1:]
    if (
        len(scored_sections) != 2
        or any(b"artifact_sha256:" not in section for section in scored_sections)
        or b"calibration" in scorer.lower()
        or b"negative control" in scorer.lower()
    ):
        raise AssertionError("blinded scorer items must use one uniform envelope")
    for relative in (
        set(evals.all_prompt_paths(()))
        - checker_module.CONTEXT_ONLY_PROMPT_PATHS
    ):
        if not checker_module.triggers_evaluation(relative):
            raise AssertionError(
                f"canonical prompt input is not an evaluation trigger: {relative}"
            )
    for relative in checker_module.CONTEXT_ONLY_PROMPT_PATHS:
        if checker_module.triggers_evaluation(relative):
            raise AssertionError(
                f"companion context doc independently triggers evaluation: {relative}"
            )

    with tempfile.TemporaryDirectory() as temporary:
        parent = pathlib.Path(temporary)

        repo = new_repo(parent, "valid")
        expect_pass(repo, "valid raw behavioral corpus")

        repo = new_repo(parent, "changed-fixture")
        fixture_path = repo / evals.FIXTURE_ROOT / evals.FIXTURE_NAMES[1]
        fixture_path.write_text(fixture_path.read_text() + "\nweakened\n")
        expect_fail(
            repo,
            "candidate corpus mutation",
            "changed protected fixed corpus fixture",
        )

        repo = new_repo(parent, "missing-events")
        (
            repo
            / pathlib.Path(MANIFEST_RELATIVE).parent
            / "candidate"
            / "008.events.jsonl"
        ).unlink()
        expect_fail(repo, "missing raw events", "missing raw events evidence")

        repo = new_repo(parent, "wrong-side")
        artifact_path = (
            repo
            / pathlib.Path(MANIFEST_RELATIVE).parent
            / "candidate"
            / "008.json"
        )
        artifact = json.loads(artifact_path.read_text())
        artifact["side"] = "baseline"
        write_json(artifact_path, artifact)
        rebind_artifact(repo, "candidate", "008")
        expect_fail(repo, "wrong artifact side", "side='baseline'")

        repo = new_repo(parent, "started-tool")
        events_path = (
            repo
            / pathlib.Path(MANIFEST_RELATIVE).parent
            / "candidate"
            / "008.events.jsonl"
        )
        lines = events_path.read_text().splitlines()
        lines.insert(
            2,
            json.dumps(
                {
                    "type": "item.started",
                    "item": {
                        "id": "tool-1",
                        "type": "command_execution",
                        "command": "true",
                    },
                },
                sort_keys=True,
            ),
        )
        events_path.write_text("\n".join(lines) + "\n")
        rebind_events(repo, "candidate", "008")
        expect_fail(
            repo,
            "started-only tool item",
            "contains non-response activity",
        )

        repo = new_repo(parent, "wrong-model")
        session_path = (
            repo
            / pathlib.Path(MANIFEST_RELATIVE).parent
            / "candidate"
            / "008.session.json"
        )
        value = json.loads(session_path.read_text())
        value["turn_context"]["model"] = "wrong-model"
        write_json(session_path, value)
        artifact_path = session_path.with_name("008.json")
        artifact = json.loads(artifact_path.read_text())
        artifact["session_sha256"] = sha256(session_path)
        write_json(artifact_path, artifact)
        manifest_path = repo / MANIFEST_RELATIVE
        manifest = json.loads(manifest_path.read_text())
        for run in manifest["runs"]:
            if run["side"] == "candidate" and run["fixture"].startswith("008-"):
                run["session_sha256"] = sha256(session_path)
        write_json(manifest_path, manifest)
        rebind_artifact(repo, "candidate", "008")
        expect_fail(repo, "wrong returned model", "runtime context mismatch")

        repo = new_repo(parent, "review-unbound")
        reviews_path = (
            repo / pathlib.Path(MANIFEST_RELATIVE).parent / "reviews.json"
        )
        value = json.loads(reviews_path.read_text())
        value["reviews"][0]["reviewed_artifact_sha256"] = "0" * 64
        write_json(reviews_path, value)
        expect_fail(repo, "unbound review", "unbound review")

        repo = new_repo(parent, "scorer-always-pass")
        reviews_path = (
            repo / pathlib.Path(MANIFEST_RELATIVE).parent / "reviews.json"
        )
        value = json.loads(reviews_path.read_text())
        value["reviewer_runs"][0]["calibration"]["status"] = "PASS"
        write_json(reviews_path, value)
        expect_fail(
            repo,
            "compromised scorer",
            "scorer sensitivity control failed",
        )

        repo = new_repo(parent, "untrusted-base")
        status, output = run_check(repo, "HEAD")
        if (
            status != 1
            or "local evaluation BASE_REF must resolve to trusted origin/main"
            not in output
        ):
            raise AssertionError(f"untrusted local base: got {status}: {output}")

        repo = new_repo(parent, "stale-prompt")
        (repo / "migrations" / "AGENTS.md").write_text("# weakened migration\n")
        expect_fail(
            repo,
            "stale nested prompt",
            "candidate prompt-surface identity mismatch",
        )

        repo = new_repo(parent, "contradictory-report")
        report = repo / "docs" / "agent-evals" / "2026-07-27-test.md"
        report.write_text(report.read_text().replace('"candidate_pass": 8', '"candidate_pass": 7'))
        expect_fail(
            repo,
            "contradictory report",
            "behavioral summary contradicts evidence",
        )

        trigger_paths = (
            "PROJECT_CHARTER.md",
            "INVARIANTS.md",
            "DECIMAL.md",
            "docs/TEST_PORTING_PLAYBOOK.md",
            "scripts/policy-check.sh",
            "scripts/check-agent-runtime.py",
            "scripts/check-governance-change.sh",
            "scripts/check-migrations.sh",
            "scripts/test-agent-workflow-policy.py",
            "scripts/test-check-agent-eval-evidence.py",
            "scripts/test-check-governance-change.sh",
            "scripts/test-check-migrations.sh",
            "scripts/agent_eval_lib.py",
            "scripts/run-agent-evals.py",
            "scripts/check-agent-eval-evidence.py",
        )
        for index, relative in enumerate(trigger_paths):
            repo = new_repo(parent, f"trigger-{index}")
            prepare_trigger_only_change(repo, relative)
            expect_fail(
                repo,
                f"behavioral evidence trigger {relative}",
                "requires changed behavioral evaluation evidence",
            )

    print("agent evaluation evidence checker tests passed")


if __name__ == "__main__":
    main()
