#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0

fail() {
  echo "POLICY ERROR: $*" >&2
  exit 1
}

base="${BASE_REF:-}"
if [[ -z "$base" && -n "${GITHUB_BASE_REF:-}" ]]; then
  base="origin/${GITHUB_BASE_REF}"
fi
if [[ -z "$base" ]] && git rev-parse --verify origin/main >/dev/null 2>&1; then
  base="origin/main"
fi
[[ -n "$base" ]] ||
  fail "governance change check requires a base ref; set BASE_REF or fetch origin/main"
git rev-parse --verify "${base}^{commit}" >/dev/null 2>&1 ||
  fail "governance change check base ref is not a commit: $base"

ci=0
if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  ci=1
fi

if ! merge_base="$(git merge-base "$base" HEAD 2>/dev/null)"; then
  fail "governance change check cannot find a merge base between $base and HEAD"
fi

effective_go_mod_diff() {
  if ((ci)); then
    git diff --unified=0 "$merge_base"...HEAD -- go.mod
    return
  fi

  # Use the same committed, staged, and unstaged union as path classification.
  # This remains conservative when the index and worktree contain opposing
  # edits: either side may be the developer's next commit.
  git diff --unified=0 "$merge_base"...HEAD -- go.mod
  git diff --cached --unified=0 -- go.mod
  git diff --unified=0 -- go.mod

  if git ls-files --others --exclude-standard -- go.mod | grep -Fqx "go.mod"; then
    while IFS= read -r line; do
      printf '+%s\n' "$line"
    done <go.mod
  fi
}

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
  done < <(effective_go_mod_diff)
  return 1
}

changed=()
while IFS= read -r path; do
  changed+=("$path")
done < <(
  {
    git diff --no-renames --name-only "$merge_base"...HEAD
    if ((!ci)); then
      git diff --no-renames --cached --name-only
      git diff --no-renames --name-only
      git ls-files --others --exclude-standard
    fi
  } | LC_ALL=C sort -u
)
((${#changed[@]})) || exit 0

protected_governance=0
implementation=0
for path in "${changed[@]}"; do
  case "$path" in
    AGENTS.md|*/AGENTS.md|MODEL_POLICY.md|AGENT_EVALS.md|\
    PROJECT_CHARTER.md|INVARIANTS.md|DECIMAL.md|Makefile|.golangci.yml|\
    CONTRIBUTING.md|REVIEW_CHECKLIST.md|README_POLICY_PACK.md|\
    go-native-test-porting-agent-playbook.md|\
    policy/*|policy/**/*|\
    docs/AGENT_TASK_TEMPLATE.md|docs/AGENT_CRITICAL_REVIEW_TEMPLATE.md|\
    docs/agent-evals/*|docs/agent-evals/**/*|testdata/agent-evals/*|\
    testdata/agent-evals/**/*|docs/TEST_PORTING_PLAYBOOK.md|docs/adr/*|\
    docs/adr/**/*|.codex/*|.codex/**/*|.agents/*|.agents/**/*|\
    prompts/*|prompts/**/*|.github/*|.github/**/*|\
    ports/SOURCE_REVISIONS.md|ports/README.md|ports/source-revisions.env|\
    ports/test-port-map.csv|ports/decisions/*|ports/decisions/**/*|\
    scripts/policy-check.sh|scripts/check-*.py|\
    scripts/check-*.sh|scripts/check-*.go|scripts/test-check-*.py|\
    scripts/test-check-*.sh|\
    tools/policycheck/*|tools/policycheck/**/*|tools/testinventory/*|\
    tools/testinventory/**/*)
      protected_governance=1
      ;;
  esac

  # Root companion documents (README.md, ARCHITECTURE.md, DATABASE.md,
  # MESSAGING.md, TESTING.md, API_COMPATIBILITY.md, DEPENDENCIES.md,
  # OPERATIONS.md, SECURITY.md, and RECONCILIATION.md) intentionally are not
  # protected governance: faithful updates belong with the implementation.
  case "$path" in
    *_test.go)
      ;;
    go.mod)
      if go_mod_has_dependency_changes; then
        implementation=1
      fi
      ;;
    contracts/*.go|contracts/**/*.go|migrations/*.go|migrations/**/*.go|\
    contracts/*.json|contracts/**/*.json|\
    contracts/*.proto|contracts/**/*.proto|contracts/*.pb|contracts/**/*.pb)
      implementation=1
      ;;
    cmd/*.go|cmd/**/*.go|internal/*.go|internal/**/*.go|tests/*.go|tests/**/*.go|\
    testkit/*.go|testkit/**/*.go|migrations/*.sql|go.sum)
      implementation=1
      ;;
  esac
done

if ((protected_governance && implementation)); then
  echo "Protected governance or invariant changes must be reviewed separately from implementation changes." >&2
  exit 1
fi
