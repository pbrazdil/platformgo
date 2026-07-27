#!/usr/bin/env bash
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKER="$SOURCE_ROOT/scripts/check-migrations.sh"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

repo=""

fail() {
  echo "MIGRATION CHECKER REGRESSION: $*" >&2
  exit 1
}

write_file() {
  local path="$1"
  local content="$2"
  mkdir -p "$(dirname "$repo/$path")"
  printf '%s\n' "$content" >"$repo/$path"
}

new_repo() {
  local name="$1"
  repo="$TMP_ROOT/$name"
  git init -q -b main "$repo"
  git -C "$repo" config user.email policy@example.invalid
  git -C "$repo" config user.name "Migration Policy Test"
  mkdir -p "$repo/scripts" "$repo/migrations"
  cp "$CHECKER" "$repo/scripts/check-migrations.sh"
  chmod +x "$repo/scripts/check-migrations.sh"
  write_file "README.md" "fixture"
  git -C "$repo" add .
  git -C "$repo" commit -qm "seed"
  write_file \
    "migrations/20260727000000_landed.up.sql" \
    "CREATE TABLE landed (id BIGINT PRIMARY KEY);"
  write_file "migrations/AGENTS.md" "landed migration instructions"
  git -C "$repo" add migrations
  git -C "$repo" commit -qm "land migration"
  git -C "$repo" branch protected
  git -C "$repo" update-ref refs/remotes/origin/main protected
  git -C "$repo" checkout -qb feature
}

run_check() {
  (
    cd "$repo"
    env -u GITHUB_ACTIONS -u GITHUB_BASE_REF -u GITHUB_EVENT_NAME \
      -u BASE_REF ./scripts/check-migrations.sh
  )
}

run_check_with_base() {
  local selected_base="$1"
  (
    cd "$repo"
    env -u GITHUB_ACTIONS -u GITHUB_BASE_REF -u GITHUB_EVENT_NAME \
      BASE_REF="$selected_base" ./scripts/check-migrations.sh
  )
}

run_ci_check() {
  (
    cd "$repo"
    env -u GITHUB_BASE_REF \
      GITHUB_ACTIONS=true \
      GITHUB_EVENT_NAME=push \
      BASE_REF="${1:-protected}" \
      ./scripts/check-migrations.sh
  )
}

expect_pass() {
  local label="$1"
  if ! output="$(run_check 2>&1)"; then
    fail "$label: expected pass, got: $output"
  fi
}

expect_fail() {
  local label="$1"
  if output="$(run_check 2>&1)"; then
    fail "$label: expected failure"
  fi
  [[ "$output" == *"immutable"* || "$output" == *"base"* ]] ||
    fail "$label: missing stable diagnostic: $output"
}

expect_ci_pass() {
  local label="$1"
  if ! output="$(run_ci_check 2>&1)"; then
    fail "$label: expected CI pass, got: $output"
  fi
}

expect_ci_fail() {
  local label="$1"
  if output="$(run_ci_check 2>&1)"; then
    fail "$label: expected CI failure"
  fi
  [[ "$output" == *"immutable"* || "$output" == *"base"* ]] ||
    fail "$label: missing stable CI diagnostic: $output"
}

# Landed path and bytes are frozen in every local snapshot.
new_repo "unstaged-edit"
write_file \
  "migrations/20260727000000_landed.up.sql" \
  "CREATE TABLE landed (id TEXT PRIMARY KEY);"
expect_fail "unstaged landed edit"

new_repo "staged-edit"
write_file \
  "migrations/20260727000000_landed.up.sql" \
  "CREATE TABLE landed (id TEXT PRIMARY KEY);"
git -C "$repo" add migrations/20260727000000_landed.up.sql
expect_fail "staged landed edit"

new_repo "staged-edit-worktree-revert"
write_file \
  "migrations/20260727000000_landed.up.sql" \
  "CREATE TABLE landed (id TEXT PRIMARY KEY);"
git -C "$repo" add migrations/20260727000000_landed.up.sql
git -C "$repo" show \
  protected:migrations/20260727000000_landed.up.sql \
  >"$repo/migrations/20260727000000_landed.up.sql"
expect_fail "staged landed edit with worktree revert"

new_repo "staged-delete-worktree-recreate"
git -C "$repo" rm -q migrations/20260727000000_landed.up.sql
git -C "$repo" show \
  protected:migrations/20260727000000_landed.up.sql \
  >"$repo/migrations/20260727000000_landed.up.sql"
expect_fail "staged landed delete with worktree recreation"

new_repo "committed-rename"
git -C "$repo" mv \
  migrations/20260727000000_landed.up.sql \
  migrations/20260727000001_renamed.up.sql
git -C "$repo" commit -qm "rename landed migration"
expect_fail "committed landed rename"

new_repo "committed-edit"
write_file \
  "migrations/20260727000000_landed.up.sql" \
  "CREATE TABLE landed (id TEXT PRIMARY KEY);"
git -C "$repo" add migrations/20260727000000_landed.up.sql
git -C "$repo" commit -qm "edit landed migration"
expect_fail "committed landed edit"

new_repo "committed-delete"
git -C "$repo" rm -q migrations/20260727000000_landed.up.sql
git -C "$repo" commit -qm "delete landed migration"
expect_fail "committed landed delete"

new_repo "ci-committed-edit"
write_file \
  "migrations/20260727000000_landed.up.sql" \
  "CREATE TABLE landed (id TEXT PRIMARY KEY);"
git -C "$repo" add migrations/20260727000000_landed.up.sql
git -C "$repo" commit -qm "edit landed migration"
expect_ci_fail "CI predecessor rejects landed edit"

new_repo "ci-new-candidate"
write_file \
  "migrations/20260727000002_candidate.up.sql" \
  "CREATE TABLE candidate (id BIGINT PRIMARY KEY);"
git -C "$repo" add migrations/20260727000002_candidate.up.sql
git -C "$repo" commit -qm "add candidate"
expect_ci_pass "CI predecessor accepts candidate above frozen tip"

# Only top-level SQL history is frozen; migration support policy remains
# editable in its own isolated governance change.
new_repo "support-doc"
write_file "migrations/AGENTS.md" "clarified migration instructions"
expect_pass "migration support documentation"

new_repo "support-doc-staged"
write_file "migrations/AGENTS.md" "clarified staged migration instructions"
git -C "$repo" add migrations/AGENTS.md
expect_pass "staged migration support documentation"

new_repo "support-doc-committed"
write_file "migrations/AGENTS.md" "clarified committed migration instructions"
git -C "$repo" add migrations/AGENTS.md
git -C "$repo" commit -qm "clarify migration instructions"
expect_pass "committed migration support documentation"

# A candidate absent from protected history remains mutable until merge or
# shared/persistent application.
new_repo "candidate-edit"
write_file \
  "migrations/20260727000002_candidate.up.sql" \
  "CREATE TABLE candidate (id BIGINT PRIMARY KEY);"
git -C "$repo" add migrations/20260727000002_candidate.up.sql
git -C "$repo" commit -qm "add candidate"
write_file \
  "migrations/20260727000002_candidate.up.sql" \
  "CREATE TABLE candidate (id UUID PRIMARY KEY);"
expect_pass "unstaged edit of unshared candidate"

new_repo "candidate-rename"
write_file \
  "migrations/20260727000002_candidate.up.sql" \
  "CREATE TABLE candidate (id BIGINT PRIMARY KEY);"
git -C "$repo" add migrations/20260727000002_candidate.up.sql
git -C "$repo" commit -qm "add candidate"
git -C "$repo" mv \
  migrations/20260727000002_candidate.up.sql \
  migrations/20260727000003_candidate_renamed.up.sql
expect_pass "staged rename of unshared candidate"

new_repo "candidate-untracked"
write_file \
  "migrations/20260727000002_candidate.up.sql" \
  "CREATE TABLE candidate (id BIGINT PRIMARY KEY);"
expect_pass "untracked candidate"

new_repo "candidate-before-frozen-tip"
write_file \
  "migrations/20260726000000_inserted.up.sql" \
  "CREATE TABLE inserted_before_frozen_tip (id BIGINT PRIMARY KEY);"
expect_fail "candidate inserted before frozen tip"

new_repo "candidate-squash"
write_file \
  "migrations/20260727000002_candidate.up.sql" \
  "CREATE TABLE candidate (id BIGINT PRIMARY KEY);"
git -C "$repo" add migrations/20260727000002_candidate.up.sql
git -C "$repo" commit -qm "add first candidate"
git -C "$repo" reset -q --soft protected
git -C "$repo" rm -q --cached migrations/20260727000002_candidate.up.sql
rm "$repo/migrations/20260727000002_candidate.up.sql"
write_file \
  "migrations/20260727000003_squashed_candidate.up.sql" \
  "CREATE TABLE candidate (id UUID PRIMARY KEY);"
git -C "$repo" add migrations/20260727000003_squashed_candidate.up.sql
git -C "$repo" commit -qm "replace squashed candidate"
expect_pass "squashed replacement of unshared candidate"

# Base discovery is fail closed.
new_repo "invalid-base"
git -C "$repo" update-ref -d refs/remotes/origin/main
expect_fail "missing protected base ref"

new_repo "caller-base-bypass"
write_file \
  "migrations/20260727000000_landed.up.sql" \
  "CREATE TABLE landed (id TEXT PRIMARY KEY);"
git -C "$repo" add migrations/20260727000000_landed.up.sql
git -C "$repo" commit -qm "edit landed migration"
if output="$(run_check_with_base HEAD 2>&1)"; then
  fail "caller-selected HEAD base: expected failure"
fi
[[ "$output" == *"base"* ]] ||
  fail "caller-selected HEAD base: missing stable diagnostic: $output"

echo "migration checker regressions passed"
