#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKER="$ROOT/scripts/check-governance-change.sh"
EXPECTED_ERROR="Protected governance or invariant changes must be reviewed separately from implementation changes."
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

repo=""
check_status=0
check_output=""

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

new_repo() {
  local name="$1"
  repo="$TMP_ROOT/$name"
  mkdir -p "$repo/scripts" "$repo/internal"
  cp "$CHECKER" "$repo/scripts/check-governance-change.sh"
  chmod +x "$repo/scripts/check-governance-change.sh"

  git -C "$repo" init -q -b main
  git -C "$repo" config user.name "Policy Test"
  git -C "$repo" config user.email "policy-test@example.invalid"

  printf 'module example.com/policytest\n\ngo 1.24.0\n' >"$repo/go.mod"
  printf 'package internal\n\nconst baseline = true\n' >"$repo/internal/service.go"
  printf '# Invariants\n\nBaseline.\n' >"$repo/INVARIANTS.md"
  printf '# Agents\n\nBaseline.\n' >"$repo/AGENTS.md"
  printf '# Model policy\n\nBaseline.\n' >"$repo/MODEL_POLICY.md"
  printf '# Database\n\nBaseline.\n' >"$repo/DATABASE.md"

  git -C "$repo" add .
  git -C "$repo" commit -qm "baseline"
  git -C "$repo" update-ref refs/remotes/origin/main HEAD
  git -C "$repo" switch -qc feature
}

append_line() {
  local path="$1"
  local line="$2"
  mkdir -p "$(dirname "$repo/$path")"
  printf '%s\n' "$line" >>"$repo/$path"
}

write_file() {
  local path="$1"
  local content="$2"
  mkdir -p "$(dirname "$repo/$path")"
  printf '%s\n' "$content" >"$repo/$path"
}

run_local_check() {
  set +e
  check_output="$(
    cd "$repo"
    env -u CI -u GITHUB_ACTIONS -u GITHUB_BASE_REF \
      BASE_REF=main ./scripts/check-governance-change.sh 2>&1
  )"
  check_status=$?
  set -e
}

run_ci_check() {
  set +e
  check_output="$(
    cd "$repo"
    env -u BASE_REF CI=true GITHUB_ACTIONS=true GITHUB_BASE_REF=main \
      ./scripts/check-governance-change.sh 2>&1
  )"
  check_status=$?
  set -e
}

run_generic_ci_local_check() {
  set +e
  check_output="$(
    cd "$repo"
    env -u GITHUB_ACTIONS -u GITHUB_BASE_REF \
      CI=true BASE_REF=main ./scripts/check-governance-change.sh 2>&1
  )"
  check_status=$?
  set -e
}

run_local_origin_check() {
  set +e
  check_output="$(
    cd "$repo"
    env -u CI -u GITHUB_ACTIONS -u GITHUB_BASE_REF \
      BASE_REF=origin/main ./scripts/check-governance-change.sh 2>&1
  )"
  check_status=$?
  set -e
}

expect_pass_result() {
  local name="$1"
  ((check_status == 0)) || fail "$name: expected pass, got status $check_status: $check_output"
  [[ -z "$check_output" ]] || fail "$name: expected no output, got: $check_output"
}

expect_error_result() {
  local name="$1"
  local expected="$2"
  ((check_status == 1)) || fail "$name: expected status 1, got $check_status: $check_output"
  [[ "$check_output" == "$expected" ]] || fail "$name: unexpected error: $check_output"
}

expect_pass() {
  local name="$1"
  run_local_check
  expect_pass_result "$name"
}

expect_protected_failure() {
  local name="$1"
  run_local_check
  expect_error_result "$name" "$EXPECTED_ERROR"
}

# 1. Implementation and faithful DATABASE.md companion documentation.
new_repo "implementation-database"
append_line "internal/service.go" "const databaseCompanion = true"
append_line "DATABASE.md" "Implementation-specific schema behavior."
git -C "$repo" add internal/service.go DATABASE.md
git -C "$repo" commit -qm "implementation with database docs"
expect_pass "implementation + DATABASE.md"

# 2. Migration and faithful OPERATIONS.md companion documentation.
new_repo "migration-operations"
write_file "migrations/20260727000000_example.up.sql" "CREATE TABLE example (id BIGINT PRIMARY KEY);"
write_file "OPERATIONS.md" "# Operations"
git -C "$repo" add migrations/20260727000000_example.up.sql OPERATIONS.md
git -C "$repo" commit -qm "migration with operations docs"
expect_pass "migration + OPERATIONS.md"

# 3. HTTP implementation and API_COMPATIBILITY.md companion documentation.
new_repo "http-api-compatibility"
write_file "internal/edge/http.go" $'package edge\n\nconst route = "/v1/example"'
write_file "API_COMPATIBILITY.md" "# API compatibility"
git -C "$repo" add internal/edge/http.go API_COMPATIBILITY.md
git -C "$repo" commit -qm "HTTP implementation with compatibility docs"
expect_pass "HTTP implementation + API_COMPATIBILITY.md"

# 4. Implementation mixed with invariant changes.
new_repo "implementation-invariants"
append_line "internal/service.go" "const invariantConflict = true"
append_line "INVARIANTS.md" "Changed invariant."
git -C "$repo" add internal/service.go INVARIANTS.md
git -C "$repo" commit -qm "implementation with invariant"
expect_protected_failure "implementation + INVARIANTS.md"

# 5. Implementation mixed with root agent-instruction changes.
new_repo "implementation-agents"
append_line "internal/service.go" "const agentConflict = true"
append_line "AGENTS.md" "Changed instruction."
git -C "$repo" add internal/service.go AGENTS.md
git -C "$repo" commit -qm "implementation with agent instructions"
expect_protected_failure "implementation + AGENTS.md"

# 6. Implementation mixed with model-policy changes.
new_repo "implementation-model-policy"
append_line "internal/service.go" "const modelPolicyConflict = true"
append_line "MODEL_POLICY.md" "Changed model policy."
git -C "$repo" add internal/service.go MODEL_POLICY.md
git -C "$repo" commit -qm "implementation with model policy"
expect_protected_failure "implementation + MODEL_POLICY.md"

# 7. Implementation mixed with policy-checker changes.
new_repo "implementation-policy-checker"
append_line "internal/service.go" "const checkerConflict = true"
write_file "scripts/check-dependencies.py" "print('changed policy checker')"
git -C "$repo" add internal/service.go scripts/check-dependencies.py
git -C "$repo" commit -qm "implementation with policy checker"
expect_protected_failure "implementation + policy checker"

# 8. Protected governance changes without implementation.
new_repo "governance-only"
append_line "INVARIANTS.md" "Governance-only change."
git -C "$repo" add INVARIANTS.md
git -C "$repo" commit -qm "governance only"
expect_pass "governance only"

# 9. Implementation without protected governance changes.
new_repo "implementation-only"
append_line "internal/service.go" "const implementationOnly = true"
git -C "$repo" add internal/service.go
git -C "$repo" commit -qm "implementation only"
expect_pass "implementation only"

# 10. A forbidden combination present only in the unstaged worktree.
new_repo "unstaged-forbidden"
append_line "go.mod" "require example.com/dependency v1.0.0"
append_line "INVARIANTS.md" "Unstaged invariant change."
expect_protected_failure "unstaged forbidden combination"

# 11. The same forbidden combination present only in the staged index.
new_repo "staged-forbidden"
append_line "go.mod" "require example.com/dependency v1.0.0"
append_line "INVARIANTS.md" "Staged invariant change."
git -C "$repo" add go.mod INVARIANTS.md
expect_protected_failure "staged forbidden combination"

# 12. Companion docs plus implementation classify identically staged or unstaged.
new_repo "companion-stage-independent"
append_line "go.mod" "require example.com/dependency v1.0.0"
companion_docs=(
  README.md
  ARCHITECTURE.md
  DATABASE.md
  MESSAGING.md
  TESTING.md
  API_COMPATIBILITY.md
  DEPENDENCIES.md
  OPERATIONS.md
  SECURITY.md
  RECONCILIATION.md
)
for companion_doc in "${companion_docs[@]}"; do
  append_line "$companion_doc" "Faithful implementation companion detail."
done
run_local_check
unstaged_status=$check_status
unstaged_output=$check_output
git -C "$repo" add go.mod "${companion_docs[@]}"
run_local_check
((unstaged_status == 0 && check_status == 0)) ||
  fail "companion stage independence: staged or unstaged check failed"
[[ "$unstaged_output" == "$check_output" ]] ||
  fail "companion stage independence: output changed after staging"

# 13. A clean committed PR diff classifies identically in local and CI modes.
new_repo "committed-local-ci"
append_line "go.mod" "require example.com/dependency v1.0.0"
append_line "INVARIANTS.md" "Committed invariant change."
git -C "$repo" add go.mod INVARIANTS.md
git -C "$repo" commit -qm "forbidden combination"
[[ -z "$(git -C "$repo" status --porcelain)" ]] ||
  fail "committed local/CI: fixture is not clean"
run_local_check
local_status=$check_status
local_output=$check_output
run_ci_check
((local_status == 1 && check_status == 1)) ||
  fail "committed local/CI: expected both checks to fail"
[[ "$local_output" == "$EXPECTED_ERROR" && "$check_output" == "$local_output" ]] ||
  fail "committed local/CI: classification output differs"

# Relevant untracked implementation and protected-governance paths are included
# in the local effective diff.
new_repo "untracked-protected"
write_file "internal/untracked.go" $'package internal\n\nconst untracked = true'
write_file "policy/untracked-policy.md" "Protected policy change."
expect_protected_failure "untracked protected path"

# Rename detection must expose both sides of committed, staged, and unstaged
# renames so a protected source path cannot be hidden under an unclassified
# destination.
new_repo "committed-protected-rename"
mkdir -p "$repo/notes"
git -C "$repo" mv INVARIANTS.md notes/invariants.txt
append_line "internal/service.go" "const committedProtectedRename = true"
git -C "$repo" add internal/service.go
git -C "$repo" commit -qm "rename protected path with implementation"
git -C "$repo" diff --name-status -M main...HEAD |
  grep -Fqx $'R100\tINVARIANTS.md\tnotes/invariants.txt' ||
  fail "committed protected rename: fixture is not detected as a rename"
run_ci_check
expect_error_result "committed protected rename" "$EXPECTED_ERROR"

new_repo "staged-protected-rename"
mkdir -p "$repo/notes"
git -C "$repo" mv INVARIANTS.md notes/invariants.txt
append_line "internal/service.go" "const stagedProtectedRename = true"
git -C "$repo" add internal/service.go
git -C "$repo" diff --cached --name-status -M |
  grep -Fqx $'R100\tINVARIANTS.md\tnotes/invariants.txt' ||
  fail "staged protected rename: fixture is not detected as a rename"
expect_protected_failure "staged protected rename"

new_repo "unstaged-protected-rename"
mkdir -p "$repo/notes"
mv "$repo/INVARIANTS.md" "$repo/notes/invariants.txt"
git -C "$repo" add -N notes/invariants.txt
append_line "internal/service.go" "const unstagedProtectedRename = true"
git -C "$repo" diff --name-status -M |
  grep -Fqx $'R100\tINVARIANTS.md\tnotes/invariants.txt' ||
  fail "unstaged protected rename: fixture is not detected as a rename"
expect_protected_failure "unstaged protected rename"

# The inverse rename must retain the old implementation path.
new_repo "committed-implementation-rename"
mkdir -p "$repo/notes"
git -C "$repo" mv internal/service.go notes/service.txt
append_line "INVARIANTS.md" "Protected change with renamed implementation."
git -C "$repo" add INVARIANTS.md
git -C "$repo" commit -qm "rename implementation with protected governance"
git -C "$repo" diff --name-status -M main...HEAD |
  grep -Fqx $'R100\tinternal/service.go\tnotes/service.txt' ||
  fail "committed implementation rename: fixture is not detected as a rename"
run_ci_check
expect_error_result "committed implementation rename" "$EXPECTED_ERROR"

# Generic CI environment variables do not discard local state. Only the
# repository's explicit GitHub Actions mode checks committed PR state alone.
new_repo "explicit-github-mode"
append_line "go.mod" "require example.com/dependency v1.0.0"
append_line "INVARIANTS.md" "Local protected change."
run_generic_ci_local_check
expect_error_result "generic CI local check" "$EXPECTED_ERROR"
run_ci_check
expect_pass_result "GitHub mode ignores local-only changes"

# Every production compatibility and migration Go surface participates in
# implementation classification.
new_repo "contracts-production-go-blocker"
write_file "contracts/handler.go" $'package contracts\n\nconst handler = true'
append_line "INVARIANTS.md" "Contract Go blocker."
expect_protected_failure "contracts production Go + protected governance"

new_repo "contracts-generated-go-blocker"
write_file "contracts/gen/platform/v1/model.go" $'package platformv1\n\nconst generated = true'
append_line "AGENTS.md" "Generated contract blocker."
expect_protected_failure "contracts generated Go + protected governance"

new_repo "contracts-json-blocker"
write_file "contracts/openapi/client-v1.json" '{"openapi":"3.1.0"}'
append_line "MODEL_POLICY.md" "Contract JSON blocker."
expect_protected_failure "contract JSON + protected governance"

new_repo "contracts-proto-blocker"
write_file "contracts/proto/platform/v1/trading.proto" 'syntax = "proto3";'
append_line "INVARIANTS.md" "Contract proto blocker."
expect_protected_failure "contract proto + protected governance"

new_repo "contracts-pb-blocker"
write_file "contracts/trading.pb" "frozen protobuf descriptor"
append_line "AGENTS.md" "Contract protobuf blocker."
git -C "$repo" add contracts/trading.pb AGENTS.md
expect_protected_failure "contract protobuf + protected governance"

new_repo "contracts-protobuf-committed-ci-blocker"
write_file "contracts/proto/platform/v1/trading.proto" 'syntax = "proto3";'
append_line "INVARIANTS.md" "Committed protobuf contract blocker."
git -C "$repo" add contracts/proto/platform/v1/trading.proto INVARIANTS.md
git -C "$repo" commit -qm "protobuf contract with protected governance"
run_ci_check
expect_error_result "committed protobuf contract + protected governance" "$EXPECTED_ERROR"

new_repo "protobuf-artifacts-only"
write_file "contracts/trading.proto" 'syntax = "proto3";'
write_file "contracts/proto/platform/v1/trading.pb" "frozen protobuf descriptor"
expect_pass "protobuf artifacts only"

new_repo "migrations-go-blocker"
write_file "migrations/embed.go" $'package migrations\n\nconst embedded = true'
write_file "policy/migration-policy.md" "Migration policy change."
expect_protected_failure "migration Go + protected governance"

new_repo "production-surfaces-only"
write_file "contracts/handler.go" $'package contracts\n\nconst handler = true'
write_file "contracts/gen/platform/v1/model.go" $'package platformv1\n\nconst generated = true'
write_file "contracts/compatibility-manifest.json" '{"version":1}'
write_file "contracts/realtime-v1.json" '{"version":1}'
write_file "contracts/openapi/client-v1.json" '{"openapi":"3.1.0"}'
write_file "migrations/embed.go" $'package migrations\n\nconst embedded = true'
expect_pass "production compatibility and migration surfaces only"

new_repo "contract-tests-excluded"
write_file "contracts/handler_test.go" $'package contracts\n\nconst fixture = true'
append_line "INVARIANTS.md" "Governance with contract test only."
expect_pass "contract test remains excluded from implementation"

# Port decisions are protected governance, independently of whether they are
# changed alone or beside implementation.
new_repo "implementation-port-decision"
append_line "internal/service.go" "const decisionConflict = true"
write_file "ports/decisions/rounding.md" "# Owner decision required"
expect_protected_failure "implementation + port decision"

new_repo "port-decision-only"
write_file "ports/decisions/rounding.md" "# Owner decision required"
expect_pass "port decision only"

# Additional repository instructions, prompts, review policy, and all GitHub
# enforcement/PR surfaces are protected path-by-path.
protected_paths=(
  CONTRIBUTING.md
  REVIEW_CHECKLIST.md
  .agents/reviewer.toml
  prompts/review.md
  .github/pull_request_template.md
)
protected_index=0
for protected_path in "${protected_paths[@]}"; do
  protected_index=$((protected_index + 1))
  new_repo "additional-protected-$protected_index"
  append_line "internal/service.go" "const additionalProtected$protected_index = true"
  write_file "$protected_path" "Protected repository policy."
  expect_protected_failure "implementation + $protected_path"
done

# A dependency added in committed history remains implementation even when an
# unstaged worktree edit removes it.
new_repo "go-mod-cancellation"
append_line "go.mod" "require example.com/dependency v1.0.0"
git -C "$repo" add go.mod
git -C "$repo" commit -qm "add dependency"
write_file "go.mod" $'module example.com/policytest\n\ngo 1.24.0'
append_line "INVARIANTS.md" "Protected change beside dependency revert."
expect_protected_failure "committed dependency masked by worktree revert"

# One fixture exercises every local layer simultaneously. The duplicate
# DATABASE.md path also proves path output is deduplicated.
new_repo "mixed-effective-diff"
write_file "README.md" "# Committed companion documentation"
git -C "$repo" add README.md
git -C "$repo" commit -qm "committed companion documentation"
append_line "internal/service.go" "const stagedImplementation = true"
append_line "DATABASE.md" "Staged companion detail."
git -C "$repo" add internal/service.go DATABASE.md
append_line "DATABASE.md" "Unstaged companion follow-up."
write_file "policy/untracked-policy.md" "Untracked protected governance."
[[ -n "$(git -C "$repo" diff --name-only main...HEAD)" ]] ||
  fail "mixed effective diff: missing committed layer"
[[ -n "$(git -C "$repo" diff --cached --name-only)" ]] ||
  fail "mixed effective diff: missing staged layer"
[[ -n "$(git -C "$repo" diff --name-only)" ]] ||
  fail "mixed effective diff: missing unstaged layer"
[[ "$(git -C "$repo" ls-files --others --exclude-standard)" == "policy/untracked-policy.md" ]] ||
  fail "mixed effective diff: missing relevant untracked layer"
expect_protected_failure "mixed committed staged unstaged untracked diff"

# Missing, invalid, and unrelated base refs fail closed with stable diagnostics
# in local and GitHub Actions modes.
new_repo "missing-base"
git -C "$repo" update-ref -d refs/remotes/origin/main
set +e
check_output="$(
  cd "$repo"
  env -u BASE_REF -u CI -u GITHUB_ACTIONS -u GITHUB_BASE_REF \
    ./scripts/check-governance-change.sh 2>&1
)"
check_status=$?
set -e
expect_error_result "missing local base" \
  "POLICY ERROR: governance change check requires a base ref; set BASE_REF or fetch origin/main"

new_repo "invalid-base"
set +e
check_output="$(
  cd "$repo"
  env -u CI -u GITHUB_ACTIONS -u GITHUB_BASE_REF \
    BASE_REF=missing ./scripts/check-governance-change.sh 2>&1
)"
check_status=$?
set -e
expect_error_result "invalid local base" \
  "POLICY ERROR: governance change check base ref is not a commit: missing"
set +e
check_output="$(
  cd "$repo"
  env -u BASE_REF CI=true GITHUB_ACTIONS=true GITHUB_BASE_REF=missing \
    ./scripts/check-governance-change.sh 2>&1
)"
check_status=$?
set -e
expect_error_result "invalid GitHub base" \
  "POLICY ERROR: governance change check base ref is not a commit: origin/missing"

new_repo "unrelated-base"
empty_tree="$(git -C "$repo" mktree </dev/null)"
unrelated_commit="$(printf 'unrelated\n' | git -C "$repo" commit-tree "$empty_tree")"
git -C "$repo" update-ref refs/heads/unrelated "$unrelated_commit"
set +e
check_output="$(
  cd "$repo"
  env -u CI -u GITHUB_ACTIONS -u GITHUB_BASE_REF \
    BASE_REF=unrelated ./scripts/check-governance-change.sh 2>&1
)"
check_status=$?
set -e
expect_error_result "unrelated local base" \
  "POLICY ERROR: governance change check cannot find a merge base between unrelated and HEAD"
set +e
check_output="$(
  cd "$repo"
  env -u CI -u GITHUB_BASE_REF GITHUB_ACTIONS=true BASE_REF=unrelated \
    ./scripts/check-governance-change.sh 2>&1
)"
check_status=$?
set -e
expect_error_result "unrelated GitHub base" \
  "POLICY ERROR: governance change check cannot find a merge base between unrelated and HEAD"

# Diverged histories with a valid common ancestor use that merge base.
new_repo "divergent-valid-base"
git -C "$repo" switch -q main
write_file "base-only.txt" "Base branch change."
git -C "$repo" add base-only.txt
git -C "$repo" commit -qm "advance base"
git -C "$repo" update-ref refs/remotes/origin/main HEAD
git -C "$repo" switch -q feature
append_line "internal/service.go" "const divergentImplementation = true"
append_line "INVARIANTS.md" "Divergent protected change."
git -C "$repo" add internal/service.go INVARIANTS.md
git -C "$repo" commit -qm "feature changes"
[[ "$(git -C "$repo" merge-base origin/main HEAD)" != "$(git -C "$repo" rev-parse origin/main)" ]] ||
  fail "divergent valid base: fixture did not diverge"
run_local_origin_check
expect_error_result "divergent valid local base" "$EXPECTED_ERROR"
run_ci_check
expect_error_result "divergent valid GitHub base" "$EXPECTED_ERROR"

echo "governance change checker tests passed"
