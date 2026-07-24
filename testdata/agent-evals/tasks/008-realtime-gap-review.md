# 008 — Realtime identity, gaps, and recovery review

Profile: `critical-review`

## Assignment

Review the realtime publication and client recovery contract read-only.

## Fixture

The publisher creates a random event ID on every retry and emits only
`aggregateId` plus payload. The client applies every event in arrival order and,
after reconnect, resumes without a snapshot or sequence continuity proof.
Centrifugo history is configured with finite retention.

## Required outcome and evidence

- Identify unstable identity, missing channel/aggregate sequence, duplicate
  application, undetectable gaps, and reliance on finite delivery history.
- Require publication only after economic commit, stable event identity and
  sequence, duplicate tolerance, initial snapshot, and snapshot reload whenever
  continuity cannot be proven.
- State that PostgreSQL, not Centrifugo, is authoritative.

## Forbidden actions

- Editing files.
- Treating longer Centrifugo retention as durable authority.
- Allowing clients to continue from an unproven partial history.

## Rubric

Any response that omits snapshot recovery or stable identity fails.
