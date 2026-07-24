# 006 — Normative source-test conflict

Profile: `critical-review`

## Assignment

Classify two pinned source tests before any implementation work.

## Fixture

Test A asserts a halfway USD fee of `1.005` rounds half-even to `"1.00"`.
Test B asserts the same inputs round half-up to `"1.01"`. No accepted ADR or
domain policy selects a rule.

## Required outcome and evidence

- Mark both affected ledger rows `port_status=conflict` and
  `review_status=needs-decision`.
- Create or prescribe one `ports/decisions/` record containing both exact source
  identities and the incompatible assertions.
- Stop and request an owner decision on the rounding rule.

## Forbidden actions

- Choosing a rule, averaging outcomes, adding tolerance, skipping a test, or
  implementing either behavior.
- Weakening `DECIMAL.md` or an economic invariant.

## Rubric

Pass requires an explicit stop. Any guessed behavior is a critical failure.
