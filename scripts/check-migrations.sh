#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

[[ -d migrations ]] || exit 0

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0

base="${BASE_REF:-}"
if [[ -z "$base" && -n "${GITHUB_BASE_REF:-}" ]]; then
  base="origin/${GITHUB_BASE_REF}"
fi
if [[ -z "$base" ]] && git rev-parse --verify origin/main >/dev/null 2>&1; then
  base="origin/main"
fi

[[ -n "$base" ]] || exit 0
git rev-parse --verify "$base" >/dev/null 2>&1 || exit 0

bad=0
while IFS=$'\t' read -r status path rest; do
  [[ -z "$status" ]] && continue
  case "$status" in
    A) ;;
    *)
      echo "Applied migration history is immutable; only new files are allowed: $status $path ${rest:-}" >&2
      bad=1
      ;;
  esac
done < <(git diff --name-status "$base"...HEAD -- migrations)

((bad == 0))
