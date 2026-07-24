#!/usr/bin/env python3
from __future__ import annotations

import csv
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

PLATFORM_REVISION = "50141367492be46ebf5623f6191a14b94af2f2bd"
NAUTILUS_REVISION = "116c9b5159ebeb6b578b737d72298cac8d723723"
HEADER = [
    "source_repo",
    "source_revision",
    "source_file",
    "source_test",
    "source_line",
    "go_file",
    "go_test",
    "category",
    "port_status",
    "review_status",
    "wiring_status",
    "evidence",
    "milestone",
    "port_owner",
    "implementation_owner",
    "notes",
]


class PortMapCheckerTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        (self.root / "scripts").mkdir()
        (self.root / "ports" / "decisions").mkdir(parents=True)
        (self.root / "internal" / "order").mkdir(parents=True)
        shutil.copy2(
            Path(__file__).resolve().with_name("check-port-map.py"),
            self.root / "scripts" / "check-port-map.py",
        )
        shutil.copy2(
            Path(__file__).resolve().with_name("check-test-provenance.go"),
            self.root / "scripts" / "check-test-provenance.go",
        )

        (self.root / "ports" / "SOURCE_REVISIONS.md").write_text(
            "\n".join(
                (
                    "PLATFORM_SOURCE_REPOSITORY=upcomers-org/platform",
                    f"PLATFORM_SOURCE_COMMIT={PLATFORM_REVISION}",
                    "NAUTILUS_SOURCE_REPOSITORY=nautechsystems/nautilus_trader",
                    f"NAUTILUS_SOURCE_REVISION={NAUTILUS_REVISION}",
                    "PLATFORM_SOURCE_ROOTS=apps/nautilus/tests,apps/app/tests",
                    "PLATFORM_SOURCE_TEST_COUNT=1",
                    "NAUTILUS_SOURCE_ROOTS=crates/model/src",
                    "NAUTILUS_SOURCE_TEST_COUNT=1",
                    "",
                )
            ),
            encoding="utf-8",
        )
        (self.root / "internal" / "order" / "order_test.go").write_text(
            "\n".join(
                (
                    "package order",
                    "",
                    'import "testing"',
                    "",
                    "// Ported from:",
                    f"//   repository: upcomers-org/platform@{PLATFORM_REVISION}",
                    "//   source: apps/app/tests/it/order.rs:12",
                    "//   test: test_submit_order",
                    "func TestSubmitOrder(t *testing.T) {}",
                    "",
                )
            ),
            encoding="utf-8",
        )
        (self.root / "ports" / "decisions" / "binding-only.md").write_text(
            "Title: Binding-only source test\nApprover: test owner\n",
            encoding="utf-8",
        )
        self.rows = [
            [
                "platform",
                PLATFORM_REVISION,
                "apps/app/tests/it/order.rs",
                "test_submit_order",
                "12",
                "internal/order/order_test.go",
                "TestSubmitOrder",
                "model",
                "ported",
                "unreviewed",
                "placeholder",
                "spec-fixture",
                "",
                "test_porter",
                "",
                "",
            ],
            [
                "nautilus",
                NAUTILUS_REVISION,
                "crates/model/src/python/binding.rs",
                "test_python_binding",
                "20",
                "",
                "",
                "not-applicable",
                "excluded",
                "reviewed",
                "placeholder",
                "",
                "",
                "test_porter",
                "",
                "ports/decisions/binding-only.md",
            ],
        ]
        self.write_ledger()

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def write_ledger(self) -> None:
        with (self.root / "ports" / "test-port-map.csv").open(
            "w", newline="", encoding="utf-8"
        ) as handle:
            writer = csv.writer(handle)
            writer.writerow(HEADER)
            writer.writerows(self.rows)

    def run_checker(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                sys.executable,
                str(self.root / "scripts" / "check-port-map.py"),
                *arguments,
            ],
            cwd=self.root,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def test_accepts_complete_pinned_inventory(self) -> None:
        result = self.run_checker("--complete")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("complete pinned inventory", result.stdout)

    def test_rejects_inexact_provenance(self) -> None:
        target = self.root / "internal" / "order" / "order_test.go"
        target.write_text(
            target.read_text(encoding="utf-8").replace(
                "apps/app/tests/it/order.rs:12",
                "apps/app/tests/it/order.rs:13",
            ),
            encoding="utf-8",
        )

        result = self.run_checker()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("lacks exact source provenance", result.stderr)

    def test_rejects_provenance_found_only_on_a_different_function(self) -> None:
        target = self.root / "internal" / "order" / "order_test.go"
        target.write_text(
            target.read_text(encoding="utf-8").replace(
                "apps/app/tests/it/order.rs:12",
                "apps/app/tests/it/order.rs:13",
            )
            + "\n".join(
                (
                    "// Ported from:",
                    f"//   repository: upcomers-org/platform@{PLATFORM_REVISION}",
                    "//   source: apps/app/tests/it/order.rs:12",
                    "//   test: test_submit_order",
                    "func TestDifferentFunction(t *testing.T) {}",
                    "",
                )
            ),
            encoding="utf-8",
        )

        result = self.run_checker()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("TestSubmitOrder lacks exact source provenance", result.stderr)

    def test_rejects_non_testing_test_signature(self) -> None:
        target = self.root / "internal" / "order" / "order_test.go"
        target.write_text(
            target.read_text(encoding="utf-8")
            .replace('import "testing"', 'import testing "example.com/not-testing"'),
            encoding="utf-8",
        )

        result = self.run_checker()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("is not a valid func(*testing.T) Go test", result.stderr)

    def test_accepts_legacy_exact_provenance_label(self) -> None:
        target = self.root / "internal" / "order" / "order_test.go"
        target.write_text(
            target.read_text(encoding="utf-8").replace(
                f"repository: upcomers-org/platform@{PLATFORM_REVISION}",
                f"platform: {PLATFORM_REVISION}",
            ),
            encoding="utf-8",
        )

        result = self.run_checker()

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_canonical_repository_comes_from_source_policy(self) -> None:
        policy = self.root / "ports" / "SOURCE_REVISIONS.md"
        policy.write_text(
            policy.read_text(encoding="utf-8").replace(
                "PLATFORM_SOURCE_REPOSITORY=upcomers-org/platform",
                "PLATFORM_SOURCE_REPOSITORY=example/platform",
            ),
            encoding="utf-8",
        )
        target = self.root / "internal" / "order" / "order_test.go"
        target.write_text(
            target.read_text(encoding="utf-8").replace(
                "repository: upcomers-org/platform@",
                "repository: example/platform@",
            ),
            encoding="utf-8",
        )

        result = self.run_checker()

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_source_line_distinguishes_same_named_rust_tests(self) -> None:
        target = self.root / "internal" / "order" / "order_test.go"
        target.write_text(
            target.read_text(encoding="utf-8")
            + "\n".join(
                (
                    "// Ported from:",
                    f"//   repository: upcomers-org/platform@{PLATFORM_REVISION}",
                    "//   source: apps/app/tests/it/order.rs:13",
                    "//   test: test_submit_order",
                    "func TestSubmitOrderVariant(t *testing.T) {}",
                    "",
                )
            ),
            encoding="utf-8",
        )
        variant = self.rows[0].copy()
        variant[4] = "13"
        variant[6] = "TestSubmitOrderVariant"
        self.rows.append(variant)
        self.write_ledger()

        result = self.run_checker()

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_unowned_non_discovered_row(self) -> None:
        self.rows[0][13] = ""
        self.write_ledger()

        result = self.run_checker()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("requires a port_owner", result.stderr)

    def test_rejects_unknown_category(self) -> None:
        self.rows[0][7] = "integration"
        self.write_ledger()

        result = self.run_checker()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid category", result.stderr)

    def test_rejects_missing_decision_record(self) -> None:
        (self.root / "ports" / "decisions" / "binding-only.md").unlink()

        result = self.run_checker()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("missing decision record", result.stderr)

    def test_complete_mode_rejects_non_terminal_status(self) -> None:
        self.rows[0][8] = "in-progress"
        self.write_ledger()

        result = self.run_checker("--complete")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("non-terminal port_status 'in-progress'", result.stderr)

    def test_rejects_real_wiring_before_semantic_review(self) -> None:
        self.rows[0][10] = "red"
        self.rows[0][11] = "unit-real"
        self.rows[0][12] = "hyperliquid-core"
        self.rows[0][14] = "implementation_worker"
        self.write_ledger()

        result = self.run_checker()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("real wiring requires review_status 'reviewed'", result.stderr)

    def test_accepts_reviewed_real_wiring(self) -> None:
        self.rows[0][9] = "reviewed"
        self.rows[0][10] = "red"
        self.rows[0][11] = "unit-real"
        self.rows[0][12] = "hyperliquid-core"
        self.rows[0][14] = "implementation_worker"
        self.write_ledger()

        result = self.run_checker()

        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
