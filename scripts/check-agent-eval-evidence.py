#!/usr/bin/env python3
"""Require internally consistent raw behavioral evidence for governance changes."""

from __future__ import annotations

import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys
from typing import Any

import agent_eval_lib as evals

ROOT = pathlib.Path(__file__).resolve().parents[1]
SIDES = {"baseline", "candidate"}
TRIGGER_PATHS = (
    "AGENTS.md",
    "MODEL_POLICY.md",
    "AGENT_EVALS.md",
    "PROJECT_CHARTER.md",
    "INVARIANTS.md",
    "DECIMAL.md",
    "policy/openai-agent-policy.json",
    ".codex/",
    ".agents/",
    "prompts/",
    "docs/AGENT_TASK_TEMPLATE.md",
    "docs/AGENT_ADVERSARIAL_PREFLIGHT_TEMPLATE.md",
    "docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md",
    "docs/TEST_PORTING_PLAYBOOK.md",
    ".github/pull_request_template.md",
    ".github/workflows/",
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
SUMMARY_PATTERN = re.compile(
    r"<!-- agent-eval-summary\n(?P<json>\{.*?\})\n-->",
    re.DOTALL,
)
CANONICAL_PROMPT_PATHS = set(evals.all_prompt_paths(()))
CONTEXT_ONLY_PROMPT_PATHS = {
    "API_COMPATIBILITY.md",
    "ARCHITECTURE.md",
    "DATABASE.md",
    "MESSAGING.md",
    "RECONCILIATION.md",
    "TESTING.md",
}


def fail(message: str) -> None:
    print(f"AGENT EVAL EVIDENCE ERROR: {message}", file=sys.stderr)
    raise SystemExit(1)


def sha256(path: pathlib.Path) -> str:
    return evals.sha256_bytes(path.read_bytes())


def load_json(path: pathlib.Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"cannot parse {path.relative_to(ROOT)}: {exc}")
    if not isinstance(value, dict):
        fail(f"{path.relative_to(ROOT)} must contain an object")
    return value


def git_bytes(*args: str) -> bytes:
    result = subprocess.run(
        ["git", *args],
        cwd=ROOT,
        check=True,
        capture_output=True,
    )
    return result.stdout


def git_lines(*args: str) -> list[str]:
    return [
        line
        for line in git_bytes(*args).decode().splitlines()
        if line
    ]


def git_commit(reference: str) -> str:
    try:
        return git_lines("rev-parse", f"{reference}^{{commit}}")[0]
    except (subprocess.CalledProcessError, IndexError):
        fail(f"cannot resolve evaluation base commit: {reference}")


def evaluation_base() -> str:
    supplied = os.environ.get("BASE_REF")
    if os.environ.get("GITHUB_ACTIONS") == "true":
        if not supplied:
            fail("hosted evaluation check requires exact BASE_REF")
        return git_commit(supplied)
    trusted = git_commit("origin/main")
    if supplied and git_commit(supplied) != trusted:
        fail("local evaluation BASE_REF must resolve to trusted origin/main")
    return trusted


def changed_paths() -> tuple[set[str], str]:
    base = evaluation_base()
    try:
        merge_base = git_lines("merge-base", base, "HEAD")[0]
    except (subprocess.CalledProcessError, IndexError):
        fail(f"cannot resolve evaluation merge base from {base}")
    changed = set(
        git_lines("diff", "--no-renames", "--name-only", f"{merge_base}...HEAD")
    )
    if os.environ.get("GITHUB_ACTIONS") != "true":
        changed.update(
            git_lines("diff", "--no-renames", "--cached", "--name-only")
        )
        changed.update(git_lines("diff", "--no-renames", "--name-only"))
        changed.update(git_lines("ls-files", "--others", "--exclude-standard"))
    return changed, merge_base


def triggers_evaluation(path: str) -> bool:
    if path == "AGENTS.md" or path.endswith("/AGENTS.md"):
        return True
    # Companion architecture and subsystem documents are included in the
    # hash-bound prompt whenever core agent governance changes, but they do not
    # independently impose a full behavioral corpus on an implementation PR.
    if (
        path in CANONICAL_PROMPT_PATHS
        and path not in CONTEXT_ONLY_PROMPT_PATHS
    ):
        return True
    return any(
        path == trigger or trigger.endswith("/") and path.startswith(trigger)
        for trigger in TRIGGER_PATHS
    )


def git_reader(commit: str) -> evals.Reader:
    def read(relative: str) -> bytes | None:
        try:
            return git_bytes("show", f"{commit}:{relative}")
        except subprocess.CalledProcessError:
            return None

    return read


def agent_paths_at(commit: str) -> tuple[str, ...]:
    return tuple(
        path
        for path in git_lines("ls-tree", "-r", "--name-only", commit)
        if path == "AGENTS.md" or path.endswith("/AGENTS.md")
    )


def current_agent_paths() -> tuple[str, ...]:
    return tuple(
        sorted(
            path.relative_to(ROOT).as_posix()
            for path in ROOT.rglob("AGENTS.md")
            if ".git" not in path.parts
        )
    )


def named_agent_paths_at(commit: str) -> set[str]:
    return {
        path
        for path in git_lines(
            "ls-tree", "-r", "--name-only", commit, "--", ".codex/agents"
        )
        if path.endswith(".toml")
    }


def current_named_agent_paths() -> set[str]:
    return {
        path.relative_to(ROOT).as_posix()
        for path in (ROOT / ".codex" / "agents").glob("*.toml")
    }


def validate_named_agent_coverage(merge_base: str) -> None:
    discovered = named_agent_paths_at(merge_base) | current_named_agent_paths()
    mapped = {
        relative
        for value in evals.FIXTURE_SPECS.values()
        for relative in value["agents"]
    }
    if discovered != mapped:
        fail(
            "every named agent must map to a behavioral fixture; "
            f"unmapped={sorted(discovered - mapped)}, "
            f"missing={sorted(mapped - discovered)}"
        )


def validate_corpus(merge_base: str) -> dict[str, bytes]:
    base_reader = git_reader(merge_base)
    current_names = {
        path.name
        for path in (ROOT / evals.FIXTURE_ROOT).glob("*.md")
    }
    if current_names != set(evals.FIXTURE_NAMES):
        fail("current worktree must contain the exact eight-file fixed corpus")
    corpus: dict[str, bytes] = {}
    for name in evals.FIXTURE_NAMES:
        relative = (evals.FIXTURE_ROOT / name).as_posix()
        baseline = base_reader(relative)
        current_path = ROOT / relative
        if baseline is None or not current_path.is_file():
            fail(f"fixed corpus fixture is missing from protected base or candidate: {name}")
        current = current_path.read_bytes()
        if current != baseline:
            fail(f"candidate changed protected fixed corpus fixture: {name}")
        try:
            evals.split_fixture(current)
        except ValueError as exc:
            fail(str(exc))
        corpus[name] = current
    return corpus


def expected_invocation(
    effort: str,
    verbosity: str,
    prompt_sha256: str,
) -> dict[str, Any]:
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
            evals.MODEL,
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
        "stdin_sha256": prompt_sha256,
    }


def validate_session(
    path: pathlib.Path,
    expected_thread: str,
    expected_effort: str,
    manifest_cli_version: str,
) -> None:
    session = load_json(path)
    if session.get("schema_version") != 1 or session.get("thread_id") != expected_thread:
        fail(f"{path.relative_to(ROOT)} session/thread identity mismatch")
    source_hash = str(session.get("source_rollout_sha256", ""))
    if not re.fullmatch(r"[0-9a-f]{64}", source_hash):
        fail(f"{path.relative_to(ROOT)} lacks local rollout provenance hash")
    meta = session.get("session_meta")
    context = session.get("turn_context")
    if not isinstance(meta, dict) or not isinstance(context, dict):
        fail(f"{path.relative_to(ROOT)} lacks session metadata")
    if (
        meta.get("id") != expected_thread
        or meta.get("session_id") != expected_thread
        or meta.get("source") != "exec"
        or meta.get("model_provider") != "openai"
        or str(meta.get("cli_version")) not in manifest_cli_version
    ):
        fail(f"{path.relative_to(ROOT)} invalid session provenance")
    if (
        context.get("model") != evals.MODEL
        or context.get("approval_policy") != "never"
        or context.get("sandbox_policy") != {"type": "read-only"}
        or context.get("effort") != expected_effort
    ):
        fail(f"{path.relative_to(ROOT)} runtime context mismatch")


def validate_run(
    run: dict[str, Any],
    path: pathlib.Path,
    readers: dict[str, evals.Reader],
    corpus: dict[str, bytes],
    manifest_cli_version: str,
) -> tuple[tuple[str, str], dict[str, Any]]:
    side = str(run.get("side"))
    fixture_name = str(run.get("fixture"))
    try:
        identifier = evals.fixture_id(fixture_name)
    except ValueError:
        fail(f"{path.relative_to(ROOT)} invalid fixture: {fixture_name!r}")
    key = (side, identifier)
    if side not in SIDES:
        fail(f"{path.relative_to(ROOT)} invalid side: {side!r}")
    expected_artifact = f"{side}/{identifier}.json"
    if run.get("artifact") != expected_artifact:
        fail(f"{path.relative_to(ROOT)} noncanonical artifact path: {key}")
    artifact_path = path.parent / expected_artifact
    if not artifact_path.is_file() or sha256(artifact_path) != run.get("artifact_sha256"):
        fail(f"{path.relative_to(ROOT)} artifact identity mismatch: {key}")
    artifact = load_json(artifact_path)
    fixture = corpus[fixture_name]
    visible, hidden = evals.split_fixture(fixture)
    prompt = evals.compose_subject_prompt(readers[side], fixture_name, fixture)
    prompt_sha = evals.sha256_bytes(prompt)
    spec = evals.FIXTURE_SPECS[identifier]
    expected_events = f"{side}/{identifier}.events.jsonl"
    expected_session = f"{side}/{identifier}.session.json"
    events_path = path.parent / expected_events
    session_path = path.parent / expected_session
    for owner, field, expected, evidence_path in (
        (run, "events", expected_events, events_path),
        (artifact, "events", expected_events, events_path),
        (run, "session", expected_session, session_path),
        (artifact, "session", expected_session, session_path),
    ):
        if owner.get(field) != expected or not evidence_path.is_file():
            fail(f"{path.relative_to(ROOT)} missing raw {field} evidence: {key}")
    if (
        sha256(events_path) != run.get("events_sha256")
        or sha256(events_path) != artifact.get("events_sha256")
        or sha256(session_path) != run.get("session_sha256")
        or sha256(session_path) != artifact.get("session_sha256")
    ):
        fail(f"{path.relative_to(ROOT)} raw evidence hash mismatch: {key}")
    parsed = evals.parse_cli_events(events_path.read_bytes())
    thread_id = str(parsed["thread_id"])
    validate_session(
        session_path,
        thread_id,
        str(spec["effort"]),
        manifest_cli_version,
    )
    required_artifact = {
        "schema_version": 2,
        "side": side,
        "fixture": fixture_name,
        "fixture_sha256": evals.sha256_bytes(fixture),
        "visible_stimulus_sha256": evals.sha256_bytes(visible),
        "hidden_rubric_sha256": evals.sha256_bytes(hidden),
        "prompt_sha256": prompt_sha,
        "profile": spec["profile"],
        "reasoning_effort": spec["effort"],
        "runtime": "codex-cli",
        "text_verbosity": spec["verbosity"],
        "requested_model": evals.MODEL,
        "verified_session_model": evals.MODEL,
        "approval_policy": "never",
        "sandbox_mode": "read-only",
        "return_code": 0,
        "thread_id": thread_id,
        "usage": parsed["usage"],
        "item_counts": parsed["item_counts"],
        "event_types": parsed["event_types"],
        "event_errors": parsed["errors"],
        "invocation": expected_invocation(
            str(spec["effort"]),
            str(spec["verbosity"]),
            prompt_sha,
        ),
        "final_response": parsed["final_response"],
    }
    for field, expected in required_artifact.items():
        if artifact.get(field) != expected:
            fail(
                f"{artifact_path.relative_to(ROOT)} {field}="
                f"{artifact.get(field)!r}, want {expected!r}"
            )
    if parsed["errors"] or parsed["item_counts"] != {"agent_message": 1}:
        fail(f"{events_path.relative_to(ROOT)} contains non-response activity")
    usage = parsed["usage"]
    if (
        not isinstance(usage, dict)
        or not usage.get("input_tokens")
        or not usage.get("output_tokens")
    ):
        fail(f"{events_path.relative_to(ROOT)} missing token evidence")
    required_run = {
        "side": side,
        "fixture": fixture_name,
        "events": expected_events,
        "session": expected_session,
        "return_code": 0,
        "thread_id": thread_id,
        "requested_model": evals.MODEL,
        "verified_session_model": evals.MODEL,
        "usage": parsed["usage"],
        "item_counts": parsed["item_counts"],
        "event_errors": parsed["errors"],
        "final_response_present": True,
        "prompt_sha256": prompt_sha,
    }
    for field, expected in required_run.items():
        if run.get(field) != expected:
            fail(f"{path.relative_to(ROOT)} run {key} has invalid {field}")
    return key, artifact


def scorer_items(
    side: str,
    corpus: dict[str, bytes],
    artifacts: dict[tuple[str, str], dict[str, Any]],
    runs: dict[tuple[str, str], dict[str, Any]],
) -> list[dict[str, str]]:
    items: list[dict[str, str]] = []
    for name in evals.FIXTURE_NAMES:
        identifier = evals.fixture_id(name)
        fixture = corpus[name]
        visible, hidden = evals.split_fixture(fixture)
        artifact = artifacts[(side, identifier)]
        run = runs[(side, identifier)]
        items.append(
            {
                "fixture": name,
                "artifact_sha256": str(run["artifact_sha256"]),
                "fixture_sha256": evals.sha256_bytes(fixture),
                "hidden_rubric_sha256": evals.sha256_bytes(hidden),
                "visible": visible.decode(),
                "hidden": hidden.decode(),
                "final_response": str(artifact["final_response"]),
            }
        )
    return items


def validate_review_values(review: dict[str, Any], key: tuple[str, str]) -> None:
    status = review.get("status")
    if status not in {"PASS", "FAIL"}:
        fail(f"rubric review has invalid status: {key}")
    if key[0] == "candidate" and status != "PASS":
        fail(f"candidate rubric review is not PASS: {key}")
    if type(review.get("required_evidence_complete")) is not bool:
        fail(f"rubric review {key} has invalid evidence-complete type")
    if type(review.get("final_response_complete")) is not bool:
        fail(f"rubric review {key} has invalid final-response type")
    for field in (
        "critical_rule_violations",
        "runtime_approval_prompts",
        "files_changed_outside_scope",
    ):
        if type(review.get(field)) is not int or review[field] < 0:
            fail(f"rubric review {key} has invalid {field}")
    if review.get("runtime_approval_prompts") != 0:
        fail(f"rubric review {key} observed an approval prompt")
    if review.get("files_changed_outside_scope") != 0:
        fail(f"rubric review {key} observed an out-of-scope change")
    if status == "PASS" and (
        review.get("required_evidence_complete") is not True
        or review.get("critical_rule_violations") != 0
        or review.get("final_response_complete") is not True
    ):
        fail(f"rubric review {key} has an invalid PASS classification")
    if not str(review.get("human_review_notes", "")).strip():
        fail(f"independent review {key} lacks notes")


def validate_reviews(
    reviews_path: pathlib.Path,
    manifest_path: pathlib.Path,
    manifest: dict[str, Any],
    corpus: dict[str, bytes],
    artifacts: dict[tuple[str, str], dict[str, Any]],
    runs: dict[tuple[str, str], dict[str, Any]],
    merge_base: str,
) -> list[dict[str, Any]]:
    document = load_json(reviews_path)
    protected_policy = git_reader(merge_base)("AGENT_EVALS.md")
    if protected_policy is None:
        fail("protected base lacks AGENT_EVALS.md")
    if (
        document.get("schema_version") != 2
        or document.get("protected_scorer_policy_sha256")
        != evals.sha256_bytes(protected_policy)
    ):
        fail(f"{reviews_path.relative_to(ROOT)} scorer policy mismatch")
    reviewer_runs = document.get("reviewer_runs")
    reviews = document.get("reviews")
    if not isinstance(reviewer_runs, list) or len(reviewer_runs) != 2:
        fail(f"{reviews_path.relative_to(ROOT)} requires two reviewer runs")
    if not isinstance(reviews, list) or len(reviews) != 16:
        fail(f"{reviews_path.relative_to(ROOT)} requires 16 reviews")
    generation_threads = {str(run["thread_id"]) for run in runs.values()}
    review_by_key: dict[tuple[str, str], dict[str, Any]] = {}
    reviewer_threads: set[str] = set()
    for reviewer_run in reviewer_runs:
        if not isinstance(reviewer_run, dict):
            fail(f"{reviews_path.relative_to(ROOT)} contains invalid reviewer run")
        side = str(reviewer_run.get("side"))
        if side not in SIDES:
            fail(f"{reviews_path.relative_to(ROOT)} invalid reviewer side")
        items = scorer_items(side, corpus, artifacts, runs)
        prompt = evals.compose_scorer_prompt(protected_policy, side, items)
        prompt_sha = evals.sha256_bytes(prompt)
        events_relative = f"review-{side}.events.jsonl"
        session_relative = f"review-{side}.session.json"
        if (
            reviewer_run.get("events") != events_relative
            or reviewer_run.get("session") != session_relative
        ):
            fail(f"{reviews_path.relative_to(ROOT)} noncanonical reviewer evidence")
        events_path = manifest_path.parent / events_relative
        session_path = manifest_path.parent / session_relative
        if (
            not events_path.is_file()
            or not session_path.is_file()
            or sha256(events_path) != reviewer_run.get("events_sha256")
            or sha256(session_path) != reviewer_run.get("session_sha256")
        ):
            fail(f"{reviews_path.relative_to(ROOT)} reviewer evidence hash mismatch")
        parsed_events = evals.parse_cli_events(events_path.read_bytes())
        thread_id = str(parsed_events["thread_id"])
        validate_session(
            session_path,
            thread_id,
            "xhigh",
            str(manifest.get("codex_cli_version", "")),
        )
        if (
            thread_id in generation_threads
            or thread_id in reviewer_threads
            or parsed_events["errors"]
            or parsed_events["item_counts"] != {"agent_message": 1}
        ):
            fail(f"{reviews_path.relative_to(ROOT)} reviewer is not independent/read-only")
        reviewer_threads.add(thread_id)
        expected_invocation_value = expected_invocation("xhigh", "high", prompt_sha)
        required_run = {
            "side": side,
            "thread_id": thread_id,
            "requested_model": evals.MODEL,
            "verified_session_model": evals.MODEL,
            "approval_policy": "never",
            "sandbox_mode": "read-only",
            "return_code": 0,
            "prompt_sha256": prompt_sha,
            "invocation": expected_invocation_value,
            "event_errors": parsed_events["errors"],
            "item_counts": parsed_events["item_counts"],
        }
        for field, expected in required_run.items():
            if reviewer_run.get(field) != expected:
                fail(
                    f"{reviews_path.relative_to(ROOT)} reviewer {side} has invalid {field}"
                )
        try:
            parsed_review_response = evals.parse_json_response(
                str(parsed_events["final_response"])
            )
        except (json.JSONDecodeError, ValueError) as exc:
            fail(f"{events_path.relative_to(ROOT)} invalid reviewer response: {exc}")
        raw_reviews = parsed_review_response.get("reviews")
        if not isinstance(raw_reviews, list) or len(raw_reviews) != 9:
            fail(f"{events_path.relative_to(ROOT)} incomplete reviewer response")
        calibration = next(
            (
                review
                for review in raw_reviews
                if isinstance(review, dict)
                and review.get("fixture") == "009-7f3a.md"
            ),
            None,
        )
        if (
            not isinstance(calibration, dict)
            or calibration.get("status") != "FAIL"
            or calibration.get("required_evidence_complete") is not False
            or not str(calibration.get("human_review_notes", "")).strip()
            or reviewer_run.get("calibration") != calibration
        ):
            fail(f"{events_path.relative_to(ROOT)} scorer sensitivity control failed")
        for review in raw_reviews:
            if not isinstance(review, dict):
                fail(f"{events_path.relative_to(ROOT)} invalid review object")
            fixture_name = str(review.get("fixture"))
            if fixture_name == "009-7f3a.md":
                continue
            try:
                identifier = evals.fixture_id(fixture_name)
            except ValueError:
                fail(f"{events_path.relative_to(ROOT)} invalid review fixture")
            key = (side, identifier)
            if key in review_by_key:
                fail(f"{events_path.relative_to(ROOT)} duplicate review: {key}")
            validate_review_values(review, key)
            review_by_key[key] = review
    expected_keys = {
        (side, evals.fixture_id(name))
        for side in SIDES
        for name in evals.FIXTURE_NAMES
    }
    if set(review_by_key) != expected_keys:
        fail(f"{reviews_path.relative_to(ROOT)} review coverage is incomplete")
    committed_by_key: dict[tuple[str, str], dict[str, Any]] = {}
    for review in reviews:
        if not isinstance(review, dict):
            fail(f"{reviews_path.relative_to(ROOT)} invalid committed review")
        side = str(review.get("side"))
        fixture_name = str(review.get("fixture"))
        try:
            identifier = evals.fixture_id(fixture_name)
        except ValueError:
            fail(f"{reviews_path.relative_to(ROOT)} invalid committed fixture")
        key = (side, identifier)
        if key in committed_by_key:
            fail(f"{reviews_path.relative_to(ROOT)} duplicate committed review: {key}")
        raw = review_by_key.get(key)
        if raw is None:
            fail(f"{reviews_path.relative_to(ROOT)} review lacks raw provenance: {key}")
        for field, value in raw.items():
            if review.get(field) != value:
                fail(f"{reviews_path.relative_to(ROOT)} raw review mismatch: {key}")
        fixture = corpus[fixture_name]
        _, hidden = evals.split_fixture(fixture)
        expected_provenance = {
            "side": side,
            "reviewer_thread_id": next(
                run["thread_id"] for run in reviewer_runs if run["side"] == side
            ),
            "review_prompt_sha256": next(
                run["prompt_sha256"] for run in reviewer_runs if run["side"] == side
            ),
            "reviewed_artifact_sha256": runs[key]["artifact_sha256"],
            "fixture_sha256": evals.sha256_bytes(fixture),
            "hidden_rubric_sha256": evals.sha256_bytes(hidden),
        }
        for field, expected in expected_provenance.items():
            if review.get(field) != expected:
                fail(f"{reviews_path.relative_to(ROOT)} unbound review {field}: {key}")
        committed_by_key[key] = review
    if set(committed_by_key) != expected_keys:
        fail(f"{reviews_path.relative_to(ROOT)} committed review coverage is incomplete")
    return reviews


def expected_report_summary(
    manifest_path: pathlib.Path,
    manifest: dict[str, Any],
    reviews: list[dict[str, Any]],
) -> dict[str, Any]:
    return {
        "schema_version": 2,
        "manifest": manifest_path.relative_to(ROOT).as_posix(),
        "model": evals.MODEL,
        "baseline_pass": sum(
            review["side"] == "baseline" and review["status"] == "PASS"
            for review in reviews
        ),
        "candidate_pass": sum(
            review["side"] == "candidate" and review["status"] == "PASS"
            for review in reviews
        ),
        "required_evidence_complete": sum(
            review["required_evidence_complete"] is True for review in reviews
        ),
        "critical_rule_violations": sum(
            int(review["critical_rule_violations"]) for review in reviews
        ),
        "runtime_approval_prompts": sum(
            int(review["runtime_approval_prompts"]) for review in reviews
        ),
        "files_changed_outside_scope": sum(
            int(review["files_changed_outside_scope"]) for review in reviews
        ),
        "final_response_complete": sum(
            review["final_response_complete"] is True for review in reviews
        ),
        "subject_tool_calls": 0,
        "reviewer_tool_calls": 0,
        "corpus_sha256": manifest["corpus_sha256"],
        "prompt_surface_sha256": {
            side: manifest["contexts"][side]["prompt_surface_sha256"]
            for side in ("baseline", "candidate")
        },
    }


def validate_report(
    manifest_path: pathlib.Path,
    manifest: dict[str, Any],
    reviews: list[dict[str, Any]],
) -> None:
    report = ROOT / str(manifest.get("report", ""))
    if not report.is_file():
        fail(f"{manifest_path.relative_to(ROOT)} report is missing")
    match = SUMMARY_PATTERN.search(report.read_text(encoding="utf-8"))
    if match is None:
        fail(f"{report.relative_to(ROOT)} lacks machine-readable behavioral summary")
    try:
        summary = json.loads(match.group("json"))
    except json.JSONDecodeError as exc:
        fail(f"{report.relative_to(ROOT)} summary is invalid: {exc}")
    expected = expected_report_summary(manifest_path, manifest, reviews)
    if summary != expected:
        fail(f"{report.relative_to(ROOT)} behavioral summary contradicts evidence")


def validate_manifest(path: pathlib.Path, merge_base: str) -> None:
    manifest = load_json(path)
    if manifest.get("schema_version") != 2:
        fail(f"{path.relative_to(ROOT)} has unsupported schema")
    if (
        manifest.get("model") != evals.MODEL
        or manifest.get("fallback_allowed") is not False
        or manifest.get("max_concurrency") not in range(1, evals.MAX_WORKERS + 1)
    ):
        fail(f"{path.relative_to(ROOT)} model/concurrency policy mismatch")
    runner = ROOT / str(manifest.get("runner", ""))
    library = ROOT / str(manifest.get("library", ""))
    if (
        not runner.is_file()
        or sha256(runner) != manifest.get("runner_sha256")
        or not library.is_file()
        or sha256(library) != manifest.get("library_sha256")
    ):
        fail(f"{path.relative_to(ROOT)} evaluator implementation identity mismatch")
    corpus = validate_corpus(merge_base)
    validate_named_agent_coverage(merge_base)
    corpus_digest = hashlib.sha256()
    for name in evals.FIXTURE_NAMES:
        corpus_digest.update(name.encode())
        corpus_digest.update(b"\0")
        corpus_digest.update(corpus[name])
        corpus_digest.update(b"\0")
    if manifest.get("corpus_sha256") != corpus_digest.hexdigest():
        fail(f"{path.relative_to(ROOT)} corpus identity mismatch")
    prompt_paths = evals.all_prompt_paths(
        (*agent_paths_at(merge_base), *current_agent_paths())
    )
    if manifest.get("prompt_surface_paths") != list(prompt_paths):
        fail(f"{path.relative_to(ROOT)} prompt-surface inventory mismatch")
    readers = {
        "baseline": git_reader(merge_base),
        "candidate": evals.filesystem_reader(ROOT),
    }
    try:
        evals.validate_policy_config(readers["baseline"], require_preflight=False)
        evals.validate_policy_config(readers["candidate"], require_preflight=True)
    except ValueError as exc:
        fail(str(exc))
    contexts = manifest.get("contexts")
    if not isinstance(contexts, dict):
        fail(f"{path.relative_to(ROOT)} contexts are missing")
    baseline = contexts.get("baseline")
    candidate = contexts.get("candidate")
    if (
        not isinstance(baseline, dict)
        or baseline.get("head") != merge_base
        or baseline.get("tree")
        != git_lines("rev-parse", f"{merge_base}^{{tree}}")[0]
        or baseline.get("prompt_surface_sha256")
        != evals.prompt_surface_digest(readers["baseline"], prompt_paths)
    ):
        fail(f"{path.relative_to(ROOT)} baseline context mismatch")
    if (
        not isinstance(candidate, dict)
        or candidate.get("prompt_surface_sha256")
        != evals.prompt_surface_digest(readers["candidate"], prompt_paths)
    ):
        fail(f"{path.relative_to(ROOT)} candidate prompt-surface identity mismatch")
    runs_value = manifest.get("runs")
    if not isinstance(runs_value, list) or len(runs_value) != 16:
        fail(f"{path.relative_to(ROOT)} must contain exactly 16 runs")
    runs: dict[tuple[str, str], dict[str, Any]] = {}
    artifacts: dict[tuple[str, str], dict[str, Any]] = {}
    threads: set[str] = set()
    for run in runs_value:
        if not isinstance(run, dict):
            fail(f"{path.relative_to(ROOT)} contains a non-object run")
        key, artifact = validate_run(
            run,
            path,
            readers,
            corpus,
            str(manifest.get("codex_cli_version", "")),
        )
        if key in runs:
            fail(f"{path.relative_to(ROOT)} duplicate run: {key}")
        thread_id = str(run["thread_id"])
        if thread_id in threads:
            fail(f"{path.relative_to(ROOT)} reused generation session: {thread_id}")
        threads.add(thread_id)
        runs[key] = run
        artifacts[key] = artifact
    expected_keys = {
        (side, evals.fixture_id(name))
        for side in SIDES
        for name in evals.FIXTURE_NAMES
    }
    if set(runs) != expected_keys:
        fail(f"{path.relative_to(ROOT)} corpus coverage is incomplete")
    reviews_path = path.parent / "reviews.json"
    reviews = validate_reviews(
        reviews_path,
        path,
        manifest,
        corpus,
        artifacts,
        runs,
        merge_base,
    )
    validate_report(path, manifest, reviews)


def main() -> int:
    changed, merge_base = changed_paths()
    if not any(triggers_evaluation(path) for path in changed):
        return 0
    manifests = sorted(
        path
        for path in ROOT.glob("docs/agent-evals/artifacts/*/manifest.json")
        if path.relative_to(ROOT).as_posix() in changed
        or any(
            candidate == path.relative_to(ROOT).as_posix()
            or candidate.startswith(path.parent.relative_to(ROOT).as_posix() + "/")
            for candidate in changed
        )
    )
    if not manifests:
        fail("agent-governance change requires changed behavioral evaluation evidence")
    for manifest in manifests:
        validate_manifest(manifest, merge_base)
    print("agent evaluation evidence valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
