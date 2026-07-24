#!/usr/bin/env python3
from __future__ import annotations

import argparse
from collections import Counter
import csv
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
PATH = ROOT / "ports" / "test-port-map.csv"
SOURCE_REVISIONS = ROOT / "ports" / "SOURCE_REVISIONS.md"

REQUIRED = [
    "source_repo", "source_revision", "source_file", "source_test",
    "source_line", "go_file", "go_test", "category", "port_status",
    "review_status", "wiring_status", "evidence", "milestone", "port_owner",
    "implementation_owner", "notes",
]
PORT_STATUSES = {
    "discovered", "reserved", "in-progress", "ported", "conflict", "excluded",
}
REVIEW_STATUSES = {
    "unreviewed", "reviewed", "needs-decision",
}
WIRING_STATUSES = {
    "placeholder", "red", "green",
}
EVIDENCE = {
    "spec-fixture",
    "unit-real",
    "model-real",
    "postgres-real",
    "nats-real",
    "http-real",
    "grpc-real",
    "realtime-real",
    "adapter-real",
}
MILESTONES = {
    "hyperliquid-core",
    "durable-execution",
    "platform-compatibility",
    "future-market",
    "future-nautilus-model",
}
CATEGORIES = {
    "unit",
    "model",
    "integration-postgres",
    "integration-messaging",
    "contract-http",
    "contract-grpc",
    "contract-realtime",
    "adapter-hyperliquid",
    "live-canary",
    "not-applicable",
}
DECISION_REQUIRED = {"conflict", "excluded"}
NON_TERMINAL = {
    "discovered", "reserved", "in-progress", "conflict",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="validate the native test-port ledger")
    parser.add_argument(
        "--complete",
        action="store_true",
        help="also require the full pinned inventory and only terminal statuses",
    )
    return parser.parse_args()


def load_source_policy() -> dict[str, dict[str, object]]:
    if not SOURCE_REVISIONS.is_file():
        raise ValueError(f"missing {SOURCE_REVISIONS}")

    text = SOURCE_REVISIONS.read_text(encoding="utf-8")
    values = dict(re.findall(r"(?m)^([A-Z][A-Z0-9_]*)=([^\s]+)$", text))
    definitions = (
        ("platform", "PLATFORM", "PLATFORM_SOURCE_COMMIT"),
        ("nautilus", "NAUTILUS", "NAUTILUS_SOURCE_REVISION"),
    )
    policy: dict[str, dict[str, object]] = {}

    for alias, prefix, revision_key in definitions:
        required_keys = (
            f"{prefix}_SOURCE_REPOSITORY",
            revision_key,
            f"{prefix}_SOURCE_ROOTS",
            f"{prefix}_SOURCE_TEST_COUNT",
        )
        missing = [key for key in required_keys if key not in values]
        if missing:
            raise ValueError(
                f"{SOURCE_REVISIONS}: missing source policy values {', '.join(missing)}"
            )

        revision = values[revision_key]
        if not re.fullmatch(r"[0-9a-f]{40}", revision):
            raise ValueError(f"{SOURCE_REVISIONS}: {revision_key} must be a Git commit")

        roots = tuple(values[f"{prefix}_SOURCE_ROOTS"].split(","))
        for root in roots:
            relative_root = Path(root)
            if (
                not root
                or relative_root.is_absolute()
                or ".." in relative_root.parts
            ):
                raise ValueError(
                    f"{SOURCE_REVISIONS}: invalid repository-relative source root {root!r}"
                )

        try:
            expected_count = int(values[f"{prefix}_SOURCE_TEST_COUNT"])
            if expected_count <= 0:
                raise ValueError
        except ValueError as exc:
            raise ValueError(
                f"{SOURCE_REVISIONS}: {prefix}_SOURCE_TEST_COUNT must be positive"
            ) from exc

        policy[alias] = {
            "repository": values[f"{prefix}_SOURCE_REPOSITORY"],
            "revision": revision,
            "roots": roots,
            "expected_count": expected_count,
        }

    return policy


args = parse_args()
try:
    source_policy = load_source_policy()
except (OSError, ValueError) as exc:
    print(exc, file=sys.stderr)
    raise SystemExit(1)

if not PATH.exists():
    print(f"missing {PATH}", file=sys.stderr)
    raise SystemExit(1)

with PATH.open(newline="", encoding="utf-8") as fh:
    reader = csv.DictReader(fh)
    if reader.fieldnames != REQUIRED:
        print(f"unexpected columns: {reader.fieldnames}; required: {REQUIRED}", file=sys.stderr)
        raise SystemExit(1)
    rows = list(reader)

seen_source: set[tuple[str, ...]] = set()
seen_go: set[tuple[str, str]] = set()
errors: list[str] = []

for line, row in enumerate(rows, start=2):
    # Rust permits same-named tests in separate cfg-gated modules. The source
    # line is therefore part of the immutable source-test identity.
    key = tuple(row[k].strip() for k in REQUIRED[:5])
    if not all(key):
        errors.append(f"line {line}: source identity fields must be non-empty")
    if key in seen_source:
        errors.append(f"line {line}: duplicate source test {key}")
    seen_source.add(key)

    source_repo = row["source_repo"].strip()
    policy = source_policy.get(source_repo)
    revision = row["source_revision"].strip()
    if policy is None:
        errors.append(f"line {line}: unknown source_repo {source_repo!r}")
    elif revision != policy["revision"]:
        errors.append(f"line {line}: source revision does not match the pinned revision")

    source_file = row["source_file"].strip()
    relative_source = Path(source_file)
    if relative_source.is_absolute() or ".." in relative_source.parts:
        errors.append(f"line {line}: source_file must be a repository-relative path")
    elif policy is not None and not any(
        source_file == root or source_file.startswith(f"{root}/")
        for root in policy["roots"]
    ):
        errors.append(f"line {line}: source_file is outside the pinned source roots")

    source_line = row["source_line"].strip()
    try:
        if int(source_line) <= 0:
            raise ValueError
    except ValueError:
        errors.append(f"line {line}: source_line must be a positive integer")

    category = row["category"].strip()
    if category not in CATEGORIES:
        errors.append(f"line {line}: invalid category {category!r}")

    port_status = row["port_status"].strip()
    if port_status not in PORT_STATUSES:
        errors.append(f"line {line}: invalid port_status {port_status!r}")
    elif args.complete and port_status in NON_TERMINAL:
        errors.append(
            f"line {line}: non-terminal port_status {port_status!r} in complete inventory"
        )
    if (port_status == "excluded") != (category == "not-applicable"):
        errors.append(
            f"line {line}: excluded port_status and not-applicable category must be used together"
        )

    review_status = row["review_status"].strip()
    if review_status not in REVIEW_STATUSES:
        errors.append(f"line {line}: invalid review_status {review_status!r}")
    elif port_status == "conflict" and review_status != "needs-decision":
        errors.append(
            f"line {line}: conflict port_status requires review_status 'needs-decision'"
        )
    elif port_status == "excluded" and review_status != "reviewed":
        errors.append(
            f"line {line}: excluded port_status requires review_status 'reviewed'"
        )
    elif port_status not in {"conflict", "excluded"} and review_status == "needs-decision":
        errors.append(
            f"line {line}: review_status 'needs-decision' requires conflict port_status"
        )
    elif (
        port_status in {"discovered", "reserved", "in-progress"}
        and review_status != "unreviewed"
    ):
        errors.append(
            f"line {line}: incomplete port_status requires review_status 'unreviewed'"
        )

    wiring_status = row["wiring_status"].strip()
    if wiring_status not in WIRING_STATUSES:
        errors.append(f"line {line}: invalid wiring_status {wiring_status!r}")

    evidence = row["evidence"].strip()
    if evidence and evidence not in EVIDENCE:
        errors.append(f"line {line}: invalid evidence {evidence!r}")

    milestone = row["milestone"].strip()
    if milestone and milestone not in MILESTONES:
        errors.append(f"line {line}: invalid milestone {milestone!r}")

    port_owner = row["port_owner"].strip()
    if port_status and port_status != "discovered" and not port_owner:
        errors.append(
            f"line {line}: port_status {port_status!r} requires a port_owner"
        )
    implementation_owner = row["implementation_owner"].strip()

    if port_status == "ported":
        if wiring_status == "placeholder" and evidence != "spec-fixture":
            errors.append(
                f"line {line}: placeholder wiring requires evidence 'spec-fixture'"
            )
        if wiring_status in {"red", "green"}:
            if review_status != "reviewed":
                errors.append(
                    f"line {line}: real wiring requires review_status 'reviewed'"
                )
            if not evidence or evidence == "spec-fixture":
                errors.append(
                    f"line {line}: real wiring requires non-fixture evidence"
                )
            if not milestone:
                errors.append(f"line {line}: real wiring requires a milestone")
            if not implementation_owner:
                errors.append(
                    f"line {line}: real wiring requires an implementation_owner"
                )
    else:
        if wiring_status and wiring_status != "placeholder":
            errors.append(
                f"line {line}: non-ported row must use placeholder wiring"
            )
        if evidence:
            errors.append(f"line {line}: non-ported row must not claim evidence")
        if milestone:
            errors.append(f"line {line}: non-ported row must not claim a milestone")
        if implementation_owner:
            errors.append(
                f"line {line}: non-ported row must not have an implementation_owner"
            )

    notes = row["notes"].strip()
    if port_status in DECISION_REQUIRED:
        references = re.findall(r"ports/decisions/[A-Za-z0-9._/-]+\.md", notes)
        if not references:
            errors.append(
                f"line {line}: port_status {port_status!r} requires a ports/decisions/*.md reference"
            )
        for reference in references:
            relative_reference = Path(reference)
            if relative_reference.is_absolute() or ".." in relative_reference.parts:
                errors.append(f"line {line}: invalid decision record path {reference}")
            elif not (ROOT / relative_reference).is_file():
                errors.append(f"line {line}: missing decision record {reference}")

    if port_status == "ported":
        go_file = row["go_file"].strip()
        go_test = row["go_test"].strip()
        if not go_file or not go_test:
            errors.append(f"line {line}: ported row requires go_file and go_test")
            continue
        if not re.fullmatch(r"Test[A-Z0-9_].*", go_test):
            errors.append(f"line {line}: go_test must be a discoverable Go Test function")
        relative_target = Path(go_file)
        if relative_target.is_absolute() or ".." in relative_target.parts:
            errors.append(f"line {line}: go_file must be a repository-relative path")
            continue
        if not go_file.endswith("_test.go"):
            errors.append(f"line {line}: ported Go file must end in _test.go")
        go_key = (go_file, go_test)
        if go_key in seen_go:
            errors.append(f"line {line}: duplicate Go test mapping {go_key}")
        seen_go.add(go_key)
        target = ROOT / relative_target
        if not target.is_file():
            errors.append(f"line {line}: missing Go file {go_file}")
            continue
    elif row["go_file"].strip() or row["go_test"].strip():
        errors.append(
            f"line {line}: only ported rows may name go_file and go_test"
        )

if args.complete:
    counts = Counter(row["source_repo"].strip() for row in rows)
    for source_repo, policy in source_policy.items():
        expected = int(policy["expected_count"])
        actual = counts[source_repo]
        if actual != expected:
            errors.append(
                f"{source_repo}: inventory has {actual} rows; expected {expected}"
            )

if not errors:
    provenance = subprocess.run(
        [
            "go",
            "run",
            "./scripts/check-test-provenance.go",
            "-root",
            str(ROOT),
            "-ledger",
            str(PATH.relative_to(ROOT)),
            "-source-policy",
            str(SOURCE_REVISIONS.relative_to(ROOT)),
        ],
        cwd=ROOT,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if provenance.returncode != 0:
        errors.extend(
            line
            for line in provenance.stderr.splitlines()
            if line.strip()
        )

if errors:
    print("test-port-map validation failed:", file=sys.stderr)
    for error in errors:
        print(f"- {error}", file=sys.stderr)
    raise SystemExit(1)

suffix = "; complete pinned inventory" if args.complete else ""
print(f"test-port-map valid: {len(rows)} rows{suffix}")
