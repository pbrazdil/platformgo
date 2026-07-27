# Agent Task Template

Use this template with the applicable named agent in `.codex/agents/` or the approved Responses API profile in `policy/openai-agent-policy.json`. Standing rules come from `AGENTS.md`, nested `AGENTS.md`, and `MODEL_POLICY.md`; do not paste them into the task.

```text
Profile:
<implementation | inventory | named Codex agent>

Goal:
<one measurable outcome>

Risk and preflight:
- Risk classification:
- Preflight required: yes/no and why
- Preflight artifact/reviewers:

Scope and ownership:
- Files or packages owned:
- Files that must not be edited:
- Relevant source repository, revision, path, line, test, issue, or contract:
- Reserved `ports/test-port-map.csv` owner and rows:
- Parallel streams and single owners for shared authority files:

Inputs and context:
<only material facts not already in repository instructions>

Task-specific constraints:
<constraints unique to this assignment; omit when none>

Required evidence:
- Failing tests before implementation:
- Focused green evidence:
- Advisory checkpoint/finding closure:
- Exact candidate SHA/tree/base evidence, when release-bound:
- Other tests, file references, failure sequences, or boundary proof:

Success criteria:
<objective conditions that make the task complete>

Authority boundary:
<use AGENTS.md; state only a narrower boundary or an additional action requiring explicit task authority>

Validation:
<smallest implementation checks; full stable-candidate gates, if this owner is responsible>

Deliverables:
<files, tests, report, or decision record>
```

Request conclusions and evidence, not hidden chain-of-thought. For implementation work, instruct the agent to make the changes and return the standard completion report from `AGENTS.md`; do not request a prose-only plan unless planning is the task.
