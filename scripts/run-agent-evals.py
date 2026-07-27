#!/usr/bin/env python3
"""Run the fixed hidden-rubric corpus against baseline and candidate prompts."""

from __future__ import annotations

import argparse
import concurrent.futures
import datetime
import hashlib
import json
import pathlib
import subprocess
import tempfile
import time
from typing import Any

import agent_eval_lib as evals


def git_value(root: pathlib.Path, *args: str) -> str:
    result = subprocess.run(
        ["git", *args],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout.strip()


def repo_agent_paths(root: pathlib.Path) -> tuple[str, ...]:
    return tuple(
        sorted(
            path.relative_to(root).as_posix()
            for path in root.rglob("AGENTS.md")
            if ".git" not in path.parts
        )
    )


def repo_named_agent_paths(root: pathlib.Path) -> set[str]:
    return {
        path.relative_to(root).as_posix()
        for path in (root / ".codex" / "agents").glob("*.toml")
    }


def validate_named_agent_coverage(
    baseline_root: pathlib.Path,
    candidate_root: pathlib.Path,
) -> None:
    discovered = repo_named_agent_paths(baseline_root) | repo_named_agent_paths(
        candidate_root
    )
    mapped = {
        relative
        for value in evals.FIXTURE_SPECS.values()
        for relative in value["agents"]
    }
    if discovered != mapped:
        raise RuntimeError(
            "every named agent must map to at least one behavioral fixture; "
            f"unmapped={sorted(discovered - mapped)}, "
            f"missing={sorted(mapped - discovered)}"
        )


def cli_command(
    root: pathlib.Path,
    effort: str,
    verbosity: str,
) -> list[str]:
    return [
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
        str(root),
        "-",
    ]


def sanitized_invocation(
    command: list[str],
    root: pathlib.Path,
    prompt: bytes,
) -> dict[str, Any]:
    return {
        "argv": ["<EVAL_ROOT>" if value == str(root) else value for value in command],
        "stdin_sha256": evals.sha256_bytes(prompt),
    }


def find_session(thread_id: str) -> pathlib.Path:
    if not thread_id:
        raise RuntimeError("codex run did not return a thread identifier")
    sessions = pathlib.Path.home() / ".codex" / "sessions"
    candidates = sorted(sessions.glob(f"**/*{thread_id}.jsonl"))
    if len(candidates) != 1:
        raise RuntimeError(
            f"expected one local session for {thread_id}, found {len(candidates)}"
        )
    return candidates[0]


def session_evidence(thread_id: str) -> dict[str, Any]:
    session_path = find_session(thread_id)
    session_meta: dict[str, Any] | None = None
    turn_context: dict[str, Any] | None = None
    for raw_line in session_path.read_text(encoding="utf-8").splitlines():
        event = json.loads(raw_line)
        payload = event.get("payload")
        if not isinstance(payload, dict):
            continue
        if event.get("type") == "session_meta":
            session_meta = {
                field: payload.get(field)
                for field in (
                    "id",
                    "session_id",
                    "source",
                    "cli_version",
                    "model_provider",
                )
            }
        if event.get("type") == "turn_context":
            turn_context = {
                field: payload.get(field)
                for field in (
                    "turn_id",
                    "model",
                    "approval_policy",
                    "sandbox_policy",
                    "effort",
                )
            }
    if session_meta is None or turn_context is None:
        raise RuntimeError(f"incomplete local session evidence for {thread_id}")
    return {
        "schema_version": 1,
        "thread_id": thread_id,
        "source_rollout_sha256": evals.sha256_bytes(session_path.read_bytes()),
        "session_meta": session_meta,
        "turn_context": turn_context,
    }


def write_json(path: pathlib.Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(value, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def run_cli(
    root: pathlib.Path,
    prompt: bytes,
    effort: str,
    verbosity: str,
    events_path: pathlib.Path,
    session_path: pathlib.Path,
) -> tuple[subprocess.CompletedProcess[str], dict[str, Any], dict[str, Any], float]:
    command = cli_command(root, effort, verbosity)
    started = time.monotonic()
    result = subprocess.run(
        command,
        input=prompt.decode(),
        capture_output=True,
        text=True,
        timeout=900,
    )
    duration = time.monotonic() - started
    events_path.parent.mkdir(parents=True, exist_ok=True)
    events_path.write_text(result.stdout, encoding="utf-8")
    parsed = evals.parse_cli_events(result.stdout.encode())
    session = session_evidence(str(parsed["thread_id"]))
    write_json(session_path, session)
    return result, parsed, session, duration


def run_fixture(
    side: str,
    root: pathlib.Path,
    fixture_name: str,
    fixture_bytes: bytes,
    output_dir: pathlib.Path,
) -> dict[str, Any]:
    identifier = evals.fixture_id(fixture_name)
    spec = evals.FIXTURE_SPECS[identifier]
    prompt = evals.compose_subject_prompt(
        evals.filesystem_reader(root),
        fixture_name,
        fixture_bytes,
    )
    destination = output_dir / side / f"{identifier}.json"
    events_path = output_dir / side / f"{identifier}.events.jsonl"
    session_path = output_dir / side / f"{identifier}.session.json"
    started_at = datetime.datetime.now(datetime.timezone.utc)
    result, parsed, session, duration = run_cli(
        root,
        prompt,
        str(spec["effort"]),
        str(spec["verbosity"]),
        events_path,
        session_path,
    )
    visible, hidden = evals.split_fixture(fixture_bytes)
    artifact = {
        "schema_version": 2,
        "side": side,
        "fixture": fixture_name,
        "fixture_sha256": evals.sha256_bytes(fixture_bytes),
        "visible_stimulus_sha256": evals.sha256_bytes(visible),
        "hidden_rubric_sha256": evals.sha256_bytes(hidden),
        "prompt_sha256": evals.sha256_bytes(prompt),
        "profile": spec["profile"],
        "reasoning_effort": spec["effort"],
        "runtime": "codex-cli",
        "text_verbosity": spec["verbosity"],
        "requested_model": evals.MODEL,
        "verified_session_model": session["turn_context"]["model"],
        "approval_policy": session["turn_context"]["approval_policy"],
        "sandbox_mode": session["turn_context"]["sandbox_policy"]["type"],
        "started_at": started_at.isoformat(),
        "latency_seconds": round(duration, 3),
        "return_code": result.returncode,
        "thread_id": parsed["thread_id"],
        "usage": parsed["usage"],
        "item_counts": parsed["item_counts"],
        "event_types": parsed["event_types"],
        "event_errors": parsed["errors"],
        "stderr": result.stderr,
        "invocation": sanitized_invocation(
            cli_command(root, str(spec["effort"]), str(spec["verbosity"])),
            root,
            prompt,
        ),
        "events": f"{side}/{identifier}.events.jsonl",
        "events_sha256": evals.sha256_bytes(events_path.read_bytes()),
        "session": f"{side}/{identifier}.session.json",
        "session_sha256": evals.sha256_bytes(session_path.read_bytes()),
        "final_response": parsed["final_response"],
    }
    write_json(destination, artifact)
    return {
        "side": side,
        "fixture": fixture_name,
        "artifact": f"{side}/{identifier}.json",
        "artifact_sha256": evals.sha256_bytes(destination.read_bytes()),
        "events": artifact["events"],
        "events_sha256": artifact["events_sha256"],
        "session": artifact["session"],
        "session_sha256": artifact["session_sha256"],
        "return_code": result.returncode,
        "thread_id": parsed["thread_id"],
        "requested_model": evals.MODEL,
        "verified_session_model": session["turn_context"]["model"],
        "latency_seconds": round(duration, 3),
        "usage": parsed["usage"],
        "item_counts": parsed["item_counts"],
        "event_errors": parsed["errors"],
        "final_response_present": bool(str(parsed["final_response"]).strip()),
        "prompt_sha256": evals.sha256_bytes(prompt),
    }


def scorer_items(
    fixture_root: pathlib.Path,
    output_dir: pathlib.Path,
    runs: list[dict[str, Any]],
    side: str,
) -> list[dict[str, str]]:
    items: list[dict[str, str]] = []
    for run in sorted(
        (candidate for candidate in runs if candidate["side"] == side),
        key=lambda candidate: candidate["fixture"],
    ):
        fixture_path = fixture_root / run["fixture"]
        fixture_bytes = fixture_path.read_bytes()
        visible, hidden = evals.split_fixture(fixture_bytes)
        artifact_path = output_dir / run["artifact"]
        artifact = json.loads(artifact_path.read_text(encoding="utf-8"))
        items.append(
            {
                "fixture": run["fixture"],
                "artifact_sha256": run["artifact_sha256"],
                "fixture_sha256": evals.sha256_bytes(fixture_bytes),
                "hidden_rubric_sha256": evals.sha256_bytes(hidden),
                "visible": visible.decode(),
                "hidden": hidden.decode(),
                "final_response": str(artifact["final_response"]),
            }
        )
    return items


def validate_review_values(
    parsed: dict[str, Any],
    expected_fixtures: set[str],
) -> list[dict[str, Any]]:
    reviews = parsed.get("reviews")
    if not isinstance(reviews, list) or len(reviews) != len(expected_fixtures):
        raise RuntimeError("reviewer did not return the complete reviews array")
    seen: set[str] = set()
    for review in reviews:
        if not isinstance(review, dict):
            raise RuntimeError("reviewer returned a non-object review")
        fixture = str(review.get("fixture"))
        if fixture not in expected_fixtures or fixture in seen:
            raise RuntimeError(f"reviewer returned invalid fixture {fixture!r}")
        seen.add(fixture)
        if review.get("status") not in {"PASS", "FAIL"}:
            raise RuntimeError(f"reviewer returned invalid status for {fixture}")
        if not str(review.get("human_review_notes", "")).strip():
            raise RuntimeError(f"reviewer omitted notes for {fixture}")
    return reviews


def validate_calibration(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise RuntimeError("reviewer omitted the blinded calibration item")
    if (
        value.get("fixture") != "009-7f3a.md"
        or value.get("status") != "FAIL"
        or value.get("required_evidence_complete") is not False
        or not str(value.get("human_review_notes", "")).strip()
    ):
        raise RuntimeError("reviewer did not reject the blinded deficient item")
    return value


def run_scorer(
    side: str,
    fixture_root: pathlib.Path,
    output_dir: pathlib.Path,
    runs: list[dict[str, Any]],
    protected_policy: bytes,
) -> dict[str, Any]:
    items = scorer_items(fixture_root, output_dir, runs, side)
    prompt = evals.compose_scorer_prompt(protected_policy, side, items)
    events_path = output_dir / f"review-{side}.events.jsonl"
    session_path = output_dir / f"review-{side}.session.json"
    with tempfile.TemporaryDirectory(prefix="platformgo-agent-eval-scorer-") as temporary:
        isolated_root = pathlib.Path(temporary)
        subprocess.run(
            ["git", "init", "-q", "-b", "main"],
            cwd=isolated_root,
            check=True,
            capture_output=True,
        )
        result, parsed_events, session, duration = run_cli(
            isolated_root,
            prompt,
            "xhigh",
            "high",
            events_path,
            session_path,
        )
    parsed_response = evals.parse_json_response(str(parsed_events["final_response"]))
    all_reviews = validate_review_values(
        parsed_response,
        {item["fixture"] for item in items} | {"009-7f3a.md"},
    )
    calibration = validate_calibration(
        next(
            (
                review
                for review in all_reviews
                if review.get("fixture") == "009-7f3a.md"
            ),
            None,
        )
    )
    reviews = [
        review
        for review in all_reviews
        if review.get("fixture") != "009-7f3a.md"
    ]
    artifact_by_fixture = {item["fixture"]: item for item in items}
    for review in reviews:
        item = artifact_by_fixture[str(review["fixture"])]
        review.update(
            {
                "side": side,
                "reviewer_thread_id": parsed_events["thread_id"],
                "review_prompt_sha256": evals.sha256_bytes(prompt),
                "reviewed_artifact_sha256": item["artifact_sha256"],
                "fixture_sha256": item["fixture_sha256"],
                "hidden_rubric_sha256": item["hidden_rubric_sha256"],
            }
        )
    return {
        "side": side,
        "thread_id": parsed_events["thread_id"],
        "requested_model": evals.MODEL,
        "verified_session_model": session["turn_context"]["model"],
        "approval_policy": session["turn_context"]["approval_policy"],
        "sandbox_mode": session["turn_context"]["sandbox_policy"]["type"],
        "return_code": result.returncode,
        "latency_seconds": round(duration, 3),
        "prompt_sha256": evals.sha256_bytes(prompt),
        "events": events_path.relative_to(output_dir).as_posix(),
        "events_sha256": evals.sha256_bytes(events_path.read_bytes()),
        "session": session_path.relative_to(output_dir).as_posix(),
        "session_sha256": evals.sha256_bytes(session_path.read_bytes()),
        "invocation": sanitized_invocation(
            cli_command(pathlib.Path("<ISOLATED_SCORER_ROOT>"), "xhigh", "high"),
            pathlib.Path("<ISOLATED_SCORER_ROOT>"),
            prompt,
        ),
        "event_errors": parsed_events["errors"],
        "item_counts": parsed_events["item_counts"],
        "calibration": calibration,
        "reviews": reviews,
    }


def validate_corpus(
    baseline_root: pathlib.Path,
    candidate_root: pathlib.Path,
) -> dict[str, bytes]:
    expected = set(evals.FIXTURE_NAMES)
    baseline_paths = {
        path.name: path
        for path in (baseline_root / evals.FIXTURE_ROOT).glob("*.md")
    }
    candidate_paths = {
        path.name: path
        for path in (candidate_root / evals.FIXTURE_ROOT).glob("*.md")
    }
    if set(baseline_paths) != expected or set(candidate_paths) != expected:
        raise RuntimeError("baseline and candidate must contain the exact fixed corpus")
    corpus: dict[str, bytes] = {}
    for name in evals.FIXTURE_NAMES:
        baseline = baseline_paths[name].read_bytes()
        candidate = candidate_paths[name].read_bytes()
        if baseline != candidate:
            raise RuntimeError(f"candidate changed fixed corpus fixture: {name}")
        evals.split_fixture(baseline)
        corpus[name] = baseline
    return corpus


def corpus_digest(corpus: dict[str, bytes]) -> str:
    digest = hashlib.sha256()
    for name in evals.FIXTURE_NAMES:
        digest.update(name.encode())
        digest.update(b"\0")
        digest.update(corpus[name])
        digest.update(b"\0")
    return digest.hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline-root", type=pathlib.Path, required=True)
    parser.add_argument("--candidate-root", type=pathlib.Path, required=True)
    parser.add_argument("--output-dir", type=pathlib.Path, required=True)
    args = parser.parse_args()

    baseline_root = args.baseline_root.resolve()
    candidate_root = args.candidate_root.resolve()
    output_dir = args.output_dir.resolve()
    validate_named_agent_coverage(baseline_root, candidate_root)
    evals.validate_policy_config(
        evals.filesystem_reader(baseline_root),
        require_preflight=False,
    )
    evals.validate_policy_config(
        evals.filesystem_reader(candidate_root),
        require_preflight=True,
    )
    corpus = validate_corpus(baseline_root, candidate_root)
    paths = evals.all_prompt_paths(
        (*repo_agent_paths(baseline_root), *repo_agent_paths(candidate_root))
    )
    contexts = {
        "baseline": {
            "root": baseline_root,
            "head": git_value(baseline_root, "rev-parse", "HEAD"),
            "tree": git_value(baseline_root, "rev-parse", "HEAD^{tree}"),
            "prompt_surface_sha256": evals.prompt_surface_digest(
                evals.filesystem_reader(baseline_root),
                paths,
            ),
        },
        "candidate": {
            "root": candidate_root,
            "head": git_value(candidate_root, "rev-parse", "HEAD"),
            "tree": git_value(candidate_root, "rev-parse", "HEAD^{tree}"),
            "working_diff_sha256": evals.sha256_bytes(
                subprocess.run(
                    ["git", "diff", "--binary", "HEAD"],
                    cwd=candidate_root,
                    check=True,
                    capture_output=True,
                ).stdout
            ),
            "prompt_surface_sha256": evals.prompt_surface_digest(
                evals.filesystem_reader(candidate_root),
                paths,
            ),
        },
    }
    jobs = [
        (side, context["root"], name, corpus[name], output_dir)
        for side, context in contexts.items()
        for name in evals.FIXTURE_NAMES
    ]
    runs: list[dict[str, Any]] = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=evals.MAX_WORKERS) as pool:
        futures = [pool.submit(run_fixture, *job) for job in jobs]
        for future in concurrent.futures.as_completed(futures):
            run = future.result()
            runs.append(run)
            print(
                f"{run['side']} {run['fixture']}: rc={run['return_code']} "
                f"model={run['verified_session_model'] or 'unverified'} "
                f"latency={run['latency_seconds']}s",
                flush=True,
            )
    runs.sort(key=lambda run: (run["side"], run["fixture"]))
    failed = [
        run
        for run in runs
        if run["return_code"] != 0
        or not run["final_response_present"]
        or run["verified_session_model"] != evals.MODEL
        or run["event_errors"]
        or run["item_counts"] != {"agent_message": 1}
    ]
    if failed:
        for run in failed:
            print(f"FAILED: {run['side']} {run['fixture']}", flush=True)
        return 1

    protected_policy = (
        baseline_root / "AGENT_EVALS.md"
    ).read_bytes()
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        reviewer_futures = [
            pool.submit(
                run_scorer,
                side,
                baseline_root / evals.FIXTURE_ROOT,
                output_dir,
                runs,
                protected_policy,
            )
            for side in ("baseline", "candidate")
        ]
        reviewer_runs = [future.result() for future in reviewer_futures]
    reviewer_runs.sort(key=lambda run: run["side"])
    if any(
        run["return_code"] != 0
        or run["verified_session_model"] != evals.MODEL
        or run["event_errors"]
        or run["item_counts"] != {"agent_message": 1}
        for run in reviewer_runs
    ):
        raise RuntimeError("independent scorer runtime evidence failed")
    reviews = [
        review
        for reviewer_run in reviewer_runs
        for review in reviewer_run.pop("reviews")
    ]
    write_json(
        output_dir / "reviews.json",
        {
            "schema_version": 2,
            "protected_scorer_policy_sha256": evals.sha256_bytes(protected_policy),
            "reviewer_runs": reviewer_runs,
            "reviews": reviews,
        },
    )

    manifest = {
        "schema_version": 2,
        "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "runner": "scripts/run-agent-evals.py",
        "runner_sha256": evals.sha256_bytes(pathlib.Path(__file__).read_bytes()),
        "library": "scripts/agent_eval_lib.py",
        "library_sha256": evals.sha256_bytes(pathlib.Path(evals.__file__).read_bytes()),
        "codex_cli_version": subprocess.run(
            ["codex", "--version"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip(),
        "report": f"docs/agent-evals/{output_dir.name}.md",
        "model": evals.MODEL,
        "fallback_allowed": False,
        "max_concurrency": evals.MAX_WORKERS,
        "corpus_sha256": corpus_digest(corpus),
        "prompt_surface_paths": list(paths),
        "contexts": {
            side: {key: value for key, value in context.items() if key != "root"}
            for side, context in contexts.items()
        },
        "runs": runs,
    }
    write_json(output_dir / "manifest.json", manifest)
    if any(
        review["side"] == "candidate" and review["status"] != "PASS"
        for review in reviews
    ):
        print("FAILED: one or more candidate rubric reviews did not pass", flush=True)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
