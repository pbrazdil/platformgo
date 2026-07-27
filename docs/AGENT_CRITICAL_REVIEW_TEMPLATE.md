# Critical Review Template

Use a read-only named reviewer from `.codex/agents/`. For Responses API orchestration, use the `critical-review` profile—or the explicitly authorized `release-gate` profile—from `policy/openai-agent-policy.json`. Both remain on `gpt-5.6-sol`; pro mode is an API execution setting, not another model.

```text
Goal:
Independently review <change or design> for <money, determinism, migration, recovery, security, or release risk>.

Scope:
- Advisory diff or full exact candidate SHA/tree/base:
- Normative tests and invariants:
- Excluded areas:
- Review checkpoint:

Review questions:
- What concrete failure can violate correctness or the named invariant?
- What transaction, ordering, retry, acknowledgment, rounding, recovery, or compatibility boundary is involved?
- What evidence proves or disproves the concern?

Required evidence:
- Cite exact files, symbols, SQL, tests, or configuration.
- For P0/P1 give every field below; without a concrete executable failure
  sequence a finding must not be P0/P1.

Output:
List findings in severity order. For each finding return:
1. Severity and concise title
2. Violated invariant or contract
3. Executable failure sequence
4. Representative fixture
5. Failing regression test
6. Minimum fix property
7. Exact candidate evidence
8. Monetary/operational impact

At advisory discovery, Exact candidate evidence may be `pending`; it is
mandatory for finding closure and GO on the stable candidate.

Severity:

- P0/P1 blocks publish and merge and requires all fields above.
- P2 is non-blocking only when it cannot affect money, durable state, ordering,
  idempotency, compatibility, security, or recoverability; name a concrete
  follow-up. A finding that affects any protected area is P1, not P2.
- P3 is non-blocking cleanup or a nit with no demonstrated correctness impact;
  do not edit a stable candidate solely for cosmetics. If a P3 recommendation
  is implemented, the changed tree invalidates all exact-SHA evidence.
- A recommendation without an executable failure sequence is non-blocking or
  follow-up, not P0/P1.

End with a clear go/no-go recommendation and list any validation that was unavailable.
Advisory review may inspect a working diff but cannot grant release approval;
announce potential blockers immediately, then re-review the correction delta.
Final release review is one independent read-only audit of the stable exact
candidate. After full validation, evidence and GO must not transfer to a
changed tree; rerun the complete required full-validation set and obtain fresh
exact-SHA specialist and independent final reviews.

Authority boundary:
Read-only. Do not edit files, mutate external systems, or approve a release.
```

Return findings and evidence only; do not expose hidden chain-of-thought or fill the report with style commentary unrelated to risk.
