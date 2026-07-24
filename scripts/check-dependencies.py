#!/usr/bin/env python3
from __future__ import annotations

import json
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
GO_MOD = ROOT / "go.mod"
ALLOW = ROOT / "policy" / "allowed-direct-modules.txt"

if not GO_MOD.exists():
    print("go.mod not present yet; dependency check skipped")
    raise SystemExit(0)

allowed = {
    line.strip()
    for line in ALLOW.read_text(encoding="utf-8").splitlines()
    if line.strip() and not line.lstrip().startswith("#")
}

proc = subprocess.run(
    ["go", "mod", "edit", "-json"],
    cwd=ROOT,
    check=True,
    stdout=subprocess.PIPE,
    text=True,
)
data = json.loads(proc.stdout)

direct = {
    item["Path"]
    for item in (data.get("Require") or [])
    if not item.get("Indirect", False)
}
unknown = sorted(direct - allowed)
if unknown:
    print("direct dependencies not in policy/allowed-direct-modules.txt:", file=sys.stderr)
    for module in unknown:
        print(f"- {module}", file=sys.stderr)
    raise SystemExit(1)

print(f"direct dependency allowlist valid: {len(direct)} modules")
