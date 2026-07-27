#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

fail() {
  echo "POLICY ERROR: $*" >&2
  exit 1
}

go run ./tools/policycheck

if [[ -d migrations ]]; then
  if grep -RInE --include='*.sql' '\b(REAL|FLOAT|DOUBLE[[:space:]]+PRECISION)\b' migrations; then
    fail "SQL floating-point types are forbidden"
  fi
  if grep -RInE --include='*.sql' 'SELECT[[:space:]]+\*' migrations; then
    fail "SELECT * is forbidden"
  fi
  while IFS= read -r f; do
    base="$(basename "$f")"
    [[ "$base" =~ ^[0-9]{14}_[a-z0-9_]+\.up\.sql$ ]] || fail "invalid migration name: $f"
  done < <(find migrations -maxdepth 1 -type f -name '*.sql' | sort)
fi

./scripts/check-migrations.sh
./scripts/test-check-migrations.sh
python3 ./scripts/test-agent-workflow-policy.py
./scripts/test-check-governance-change.sh
./scripts/check-governance-change.sh
python3 ./scripts/test-check-agent-eval-evidence.py
python3 ./scripts/check-agent-eval-evidence.py

echo "policy checks passed"
