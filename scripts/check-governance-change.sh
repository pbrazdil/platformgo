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

go_mod_has_dependency_changes() {
  local line content
  while IFS= read -r line; do
    case "$line" in
      "+++"*|"---"*|"@@"*)
        continue
        ;;
      "+"*|"-"*)
        content="${line:1}"
        case "$content" in
          ""|"module "*|"go "*|"toolchain "*)
            continue
            ;;
        esac
        return 0
        ;;
    esac
  done < <(git diff --unified=0 "$base"...HEAD -- go.mod)
  return 1
}

changed=()
while IFS= read -r path; do
  changed+=("$path")
done < <(git diff --name-only "$base"...HEAD)
((${#changed[@]})) || exit 0

governance=0
implementation=0
for path in "${changed[@]}"; do
  case "$path" in
    AGENTS.md|*/AGENTS.md|MODEL_POLICY.md|AGENT_EVALS.md|\
    ARCHITECTURE.md|DATABASE.md|DECIMAL.md|MESSAGING.md|TESTING.md|\
    API_COMPATIBILITY.md|PROJECT_CHARTER.md|DEPENDENCIES.md|OPERATIONS.md|\
    SECURITY.md|RECONCILIATION.md|Makefile|.golangci.yml|\
    README_POLICY_PACK.md|go-native-test-porting-agent-playbook.md|\
    policy/*|policy/**/*|\
    docs/AGENT_TASK_TEMPLATE.md|docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md|\
    docs/agent-evals/*|docs/agent-evals/**/*|testdata/agent-evals/*|\
    testdata/agent-evals/**/*|docs/TEST_PORTING_PLAYBOOK.md|docs/adr/*|\
    docs/adr/**/*|.codex/*|.codex/**/*|.github/CODEOWNERS|\
    .github/CODEOWNERS.example|\
    .github/workflows/*|.github/workflows/**/*|ports/SOURCE_REVISIONS.md|\
    ports/README.md|ports/source-revisions.env|ports/test-port-map.csv|\
    scripts/policy-check.sh|scripts/check-*.py|\
    scripts/check-*.sh|scripts/check-*.go|scripts/test-check-*.py|\
    tools/policycheck/*|tools/policycheck/**/*|tools/testinventory/*|\
    tools/testinventory/**/*)
      governance=1
      ;;
  esac
  case "$path" in
    *_test.go)
      ;;
    go.mod)
      if go_mod_has_dependency_changes; then
        implementation=1
      fi
      ;;
    cmd/*.go|cmd/**/*.go|internal/*.go|internal/**/*.go|tests/*.go|tests/**/*.go|\
    testkit/*.go|testkit/**/*.go|migrations/*.sql|go.sum)
      implementation=1
      ;;
  esac
done

if ((governance && implementation)); then
  echo "Governance, invariant, or agent-runtime changes must be reviewed separately from implementation changes." >&2
  exit 1
fi
