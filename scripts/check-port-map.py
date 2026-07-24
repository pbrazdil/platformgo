#!/usr/bin/env python3
from __future__ import annotations

import argparse
from collections import Counter
import csv
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
PATH = ROOT / "ports" / "test-port-map.csv"
SOURCE_REVISIONS = ROOT / "ports" / "SOURCE_REVISIONS.md"

REQUIRED = [
    "source_repo", "source_revision", "source_file", "source_test",
    "source_line", "go_file", "go_test", "category", "status", "owner", "notes",
]
STATUSES = {
    "discovered", "reserved", "in-progress", "ported-failing",
    "ported-green", "conflict", "deferred-live", "not-applicable",
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
PORTED = {"ported-failing", "ported-green"}
DECISION_REQUIRED = {"conflict", "not-applicable"}
NON_TERMINAL = {
    "discovered", "reserved", "in-progress", "ported-failing", "deferred-live",
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

    status = row["status"].strip()
    if status not in STATUSES:
        errors.append(f"line {line}: invalid status {status!r}")
    elif args.complete and status in NON_TERMINAL:
        errors.append(f"line {line}: non-terminal status {status!r} in complete inventory")
    if (status == "not-applicable") != (category == "not-applicable"):
        errors.append(
            f"line {line}: not-applicable status and category must be used together"
        )
    if status == "deferred-live" and category != "live-canary":
        errors.append(f"line {line}: deferred-live requires category 'live-canary'")

    owner = row["owner"].strip()
    if status and status != "discovered" and not owner:
        errors.append(f"line {line}: status {status!r} requires an owner")

    notes = row["notes"].strip()
    if status in DECISION_REQUIRED:
        references = re.findall(r"ports/decisions/[A-Za-z0-9._/-]+\.md", notes)
        if not references:
            errors.append(
                f"line {line}: status {status!r} requires a ports/decisions/*.md reference"
            )
        for reference in references:
            relative_reference = Path(reference)
            if relative_reference.is_absolute() or ".." in relative_reference.parts:
                errors.append(f"line {line}: invalid decision record path {reference}")
            elif not (ROOT / relative_reference).is_file():
                errors.append(f"line {line}: missing decision record {reference}")
    elif status == "deferred-live" and not notes:
        errors.append(f"line {line}: deferred-live requires a reason in notes")

    if status in PORTED:
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
        text = target.read_text(encoding="utf-8")
        repository_reference = None
        if policy is not None:
            provenance_patterns = [
                rf"{re.escape(str(policy['repository']))}@{re.escape(revision)}\b"
            ]
            legacy_label = {
                "platform": "platform",
                "nautilus": "NautilusTrader",
            }.get(source_repo)
            if legacy_label is not None:
                provenance_patterns.append(
                    rf"{re.escape(legacy_label)}:\s*{re.escape(revision)}\b"
                )
            repository_reference = re.compile("|".join(provenance_patterns))
        source_reference = re.compile(
            rf"source:\s*{re.escape(source_file)}:{re.escape(source_line)}\b"
        )
        test_reference = re.compile(
            rf"test:\s*{re.escape(row['source_test'].strip())}\b"
        )
        if (
            repository_reference is None
            or not repository_reference.search(text)
            or not source_reference.search(text)
            or not test_reference.search(text)
        ):
            errors.append(f"line {line}: Go file lacks exact source provenance")
        go_declaration = re.compile(
            rf"\bfunc\s+{re.escape(go_test)}\s*\(\s*"
            rf"(?:[A-Za-z_][A-Za-z0-9_]*\s+)?\*testing\.T\s*\)"
        )
        if not go_declaration.search(text):
            errors.append(f"line {line}: Go file lacks test function {go_test}")

if args.complete:
    counts = Counter(row["source_repo"].strip() for row in rows)
    for source_repo, policy in source_policy.items():
        expected = int(policy["expected_count"])
        actual = counts[source_repo]
        if actual != expected:
            errors.append(
                f"{source_repo}: inventory has {actual} rows; expected {expected}"
            )

if errors:
    print("test-port-map validation failed:", file=sys.stderr)
    for error in errors:
        print(f"- {error}", file=sys.stderr)
    raise SystemExit(1)

suffix = "; complete pinned inventory" if args.complete else ""
print(f"test-port-map valid: {len(rows)} rows{suffix}")
