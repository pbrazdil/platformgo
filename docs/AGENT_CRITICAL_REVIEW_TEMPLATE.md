# Critical Review Template

Use a read-only named reviewer from `.codex/agents/`. For Responses API orchestration, use the `critical-review` profile—or the explicitly authorized `release-gate` profile—from `policy/openai-agent-policy.json`. Both remain on `gpt-5.6-sol`; pro mode is an API execution setting, not another model.

```text
Goal:
Independently review <change or design> for <money, determinism, migration, recovery, security, or release risk>.

Scope:
- Commit/diff/files:
- Normative tests and invariants:
- Excluded areas:

Review questions:
- What concrete failure can violate correctness or the named invariant?
- What transaction, ordering, retry, acknowledgment, rounding, recovery, or compatibility boundary is involved?
- What evidence proves or disproves the concern?

Required evidence:
- Cite exact files, symbols, SQL, tests, or configuration.
- Give a reproducible failure sequence for every material finding.
- Name the missing or required regression test.

Output:
List findings in severity order. For each finding return:
1. Severity and concise title
2. Evidence
3. Failure sequence
4. Monetary/operational impact
5. Specific mitigation
6. Required regression or upgrade test

End with a clear go/no-go recommendation and list any validation that was unavailable.

Approval boundary:
Read-only. Do not edit files, mutate external systems, or approve a release.
```

Return findings and evidence only; do not expose hidden chain-of-thought or fill the report with style commentary unrelated to risk.
