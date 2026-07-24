#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

fail() {
  echo "POLICY ERROR: $*" >&2
  exit 1
}

existing_dirs=()
for d in internal/domain internal/engine internal/order internal/matching internal/position internal/bracket internal/margin internal/funding internal/liquidation internal/ledger; do
  [[ -d "$d" ]] && existing_dirs+=("$d")
done

if ((${#existing_dirs[@]})); then
  if grep -RInE --include='*.go' '\b(float32|float64)\b' "${existing_dirs[@]}"; then
    fail "floating-point types are forbidden in deterministic/economic packages"
  fi

  if grep -RInE --include='*.go' 'time\.Now\s*\(|time\.Sleep\s*\(' "${existing_dirs[@]}"; then
    fail "wall clock and sleeps are forbidden in deterministic/economic packages"
  fi

  if grep -RInE --include='*.go' '(crypto/rand|math/rand|rand\.|uuid\.New|os\.Getenv|os\.LookupEnv)' "${existing_dirs[@]}"; then
    fail "randomness/environment access is forbidden in deterministic/economic packages"
  fi

  if grep -RInE --include='*.go' '(github\.com/jackc/pgx|database/sql|github\.com/nats-io/nats\.go|net/http|centrifug)' "${existing_dirs[@]}"; then
    fail "infrastructure imports are forbidden in deterministic/economic packages"
  fi

  if grep -RInE --include='*.go' --exclude='*_test.go' '\bpanic\s*\(' "${existing_dirs[@]}"; then
    fail "panic is forbidden in deterministic/economic production paths"
  fi
fi

if grep -RInE --include='*.go' '"unsafe"' . --exclude-dir=.git --exclude-dir=vendor; then
  fail "unsafe package import is forbidden"
fi

if grep -RInE --include='*_test.go' 'time\.Sleep\s*\(' internal testkit 2>/dev/null; then
  fail "time.Sleep is forbidden in unit/model test packages"
fi

if grep -RInE --include='*_test.go' '\bt\.Parallel\s*\(' internal testkit tests 2>/dev/null; then
  fail "t.Parallel is forbidden until explicit harness approval and policy change"
fi

if grep -RInE --include='*_test.go' '\bt\.Skip(f|Now)?\s*\(' internal testkit 2>/dev/null; then
  fail "permanent skips are forbidden in deterministic/unit/model tests"
fi

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
./scripts/check-governance-change.sh

echo "policy checks passed"
