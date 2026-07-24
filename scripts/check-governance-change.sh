#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

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

changed=()
while IFS= read -r path; do
  changed+=("$path")
done < <(git diff --name-only "$base"...HEAD)
((${#changed[@]})) || exit 0

governance=0
implementation=0
for path in "${changed[@]}"; do
  case "$path" in
    AGENTS.md|*/AGENTS.md|MODEL_POLICY.md|AGENT_EVALS.md|INVARIANTS.md|policy/openai-agent-policy.json|docs/AGENT_TASK_TEMPLATE.md|docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md|docs/agent-evals/*|testdata/agent-evals/*|docs/TEST_PORTING_PLAYBOOK.md|.codex/*|.codex/**/*|scripts/policy-check.sh|scripts/check-agent-runtime.py|scripts/check-governance-change.sh|.golangci.yml)
      governance=1
      ;;
  esac
  case "$path" in
    *.go|migrations/*.sql|go.mod|go.sum)
      implementation=1
      ;;
  esac
done

if ((governance && implementation)); then
  echo "Governance, invariant, or agent-runtime changes must be reviewed separately from implementation changes." >&2
  exit 1
fi
