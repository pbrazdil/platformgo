#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

[[ -d migrations ]] || exit 0
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0

fail() {
  echo "MIGRATION POLICY ERROR: $*" >&2
  exit 1
}

ci=0
if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  ci=1
fi

if ((ci)); then
  base="${BASE_REF:-}"
  if [[ -z "$base" && -n "${GITHUB_BASE_REF:-}" ]]; then
    base="origin/${GITHUB_BASE_REF}"
  fi
else
  base="origin/main"
  if [[ -n "${BASE_REF:-}" ]]; then
    git rev-parse --verify "${BASE_REF}^{commit}" >/dev/null 2>&1 ||
      fail "local base override is not a commit: $BASE_REF"
    git rev-parse --verify "${base}^{commit}" >/dev/null 2>&1 ||
      fail "immutable migration check requires fetched origin/main"
    [[ "$(git rev-parse "${BASE_REF}^{commit}")" == "$(git rev-parse "${base}^{commit}")" ]] ||
      fail "local base override must resolve to trusted origin/main"
  fi
fi

[[ -n "$base" ]] ||
  fail "immutable migration check requires a protected base ref"
git rev-parse --verify "${base}^{commit}" >/dev/null 2>&1 ||
  fail "immutable migration check base ref is not a commit: $base"

if ((ci)); then
  [[ -z "$(git status --porcelain --untracked-files=all)" ]] ||
    fail "hosted immutable migration check requires a clean checkout"
fi

is_migration_sql() {
  [[ "$1" =~ ^migrations/[0-9]{14}_[a-z0-9_]+\.up\.sql$ ]]
}

blob_at() {
  local revision="$1"
  local path="$2"
  git rev-parse "${revision}:${path}" 2>/dev/null
}

index_blob() {
  local path="$1"
  local record
  record="$(git ls-files --stage -- "$path")"
  [[ -n "$record" ]] || return 1
  [[ "$(wc -l <<<"$record" | tr -d ' ')" == "1" ]] || return 1
  printf '%s\n' "$record" | awk '{print $2}'
}

worktree_blob() {
  local path="$1"
  [[ -f "$path" && ! -L "$path" ]] || return 1
  git hash-object -- "$path"
}

bad=0
frozen_tip=""
while IFS= read -r path; do
  is_migration_sql "$path" || continue
  if [[ -z "$frozen_tip" || "$path" > "$frozen_tip" ]]; then
    frozen_tip="$path"
  fi

  frozen_blob="$(blob_at "$base" "$path")" ||
    fail "cannot read frozen migration from protected base: $path"

  head_blob="$(blob_at HEAD "$path" || true)"
  if [[ "$head_blob" != "$frozen_blob" ]]; then
    echo "Frozen migration history is immutable in HEAD: $path" >&2
    bad=1
  fi

  if ((ci)); then
    continue
  fi

  staged_blob="$(index_blob "$path" || true)"
  if [[ "$staged_blob" != "$frozen_blob" ]]; then
    echo "Frozen migration history is immutable in the index: $path" >&2
    bad=1
  fi

  local_blob="$(worktree_blob "$path" || true)"
  if [[ "$local_blob" != "$frozen_blob" ]]; then
    echo "Frozen migration history is immutable in the worktree: $path" >&2
    bad=1
  fi
done < <(git ls-tree -r --name-only "$base" -- migrations)

if [[ -n "$frozen_tip" ]]; then
  while IFS= read -r path; do
    is_migration_sql "$path" || continue
    if git cat-file -e "${base}:${path}" 2>/dev/null; then
      continue
    fi
    if [[ "$path" < "$frozen_tip" || "$path" == "$frozen_tip" ]]; then
      echo "Frozen migration order is immutable; candidate precedes protected tip: $path" >&2
      bad=1
    fi
  done < <(
    {
      git ls-tree -r --name-only HEAD -- migrations
      if ((!ci)); then
        git ls-files -- migrations
        find migrations -maxdepth 1 -type f -name '*.up.sql' -print
      fi
    } | LC_ALL=C sort -u
  )
fi

((bad == 0))
