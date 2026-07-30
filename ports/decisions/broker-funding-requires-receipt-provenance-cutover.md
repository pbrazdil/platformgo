Title: Broker funding requires receipt-provenance schema cutover
Status: accepted owner correction
Date: 2026-07-30
Corrects: broker-funding-preserves-current-go-projection-and-tenant-authority.md

## Context

The accepted broker-funding activation decision required migration
`20260730000400_phase3_broker_funding_acl.up.sql` to remain ACL-only. Exact-SHA
money, migration, and determinism review proved that restriction cannot satisfy
the higher-order PostgreSQL authority, exact-unit, fail-closed, and recovery
invariants:

- hostile inherited table privileges could erase or forge funding history
  before the ACL cutover, after which an ACL-only migration would bless the
  divergent state;
- the durable funding relations do not retain the historical instrument
  revision and price/quantity scales needed to validate the returned economic
  values;
- an old engine writer could remain active across a persistence-shape change;
  and
- missing provenance could otherwise be omitted by a join and escape both the
  broker response and reconciliation.

Migration `20260730000400_phase3_broker_funding_acl.up.sql` has not reached a
protected branch or a shared or persistent database. Its applications were
limited to explicitly disposable local test databases, so its candidate bytes
are not frozen.

## Decision

Amend the unpublished migration candidate into a halted schema-and-persistence
cutover while preserving the accepted current-Go broker wire behavior.

The migration must, atomically and before granting broker read execution:

1. fence active shard ownership and use a lock order compatible with engine
   persistence;
2. verify the exact expected legacy trigger inventory and reject hostile or
   disabled trigger drift;
3. reconstruct accepted funding effects from immutable engine input receipts
   in explicit receipt and array order;
4. compare receipt authority, settlements, and history projections
   bidirectionally, rejecting missing, extra, duplicated, or divergent facts;
5. derive and backfill one immutable historical instrument-provenance row per
   funding effect from the instrument revision active at that receipt;
6. add immutable and `TRUNCATE` guards plus a deferred provenance-presence
   constraint for future engine writes;
7. expose provenance only through a narrowly granted security-definer broker
   read function;
8. advance the runtime schema revision so an old binary cannot acquire writer
   ownership or commit after cutover; and
9. scrub hostile table, column, function, and dependent grant chains before
   restoring only the documented runtime allowlist.

The engine must persist settlement, history projection, and historical
instrument provenance in the same transaction as the receipt, checkpoint,
ledger, balances, outboxes, and other effects. Reconciliation must derive and
compare the provenance from the same ordered receipt authority. Broker reads
must preserve rows with missing or inconsistent provenance long enough for the
buffered Go validator to reject the entire response; they must never silently
shorten a page.

## Compatibility and economic effect

This correction does not change valid broker funding JSON, pagination,
authorization, ordering, cursor encoding, totals, or current-Go query parsing.
It does not recalculate, round, repair, insert, or delete an economic effect.
Valid exact values remain byte-equivalent after canonical rendering.

The only externally observable change is fail-closed handling of unprovable or
corrupt durable state: migration or readiness remains unavailable, and the
broker route returns the existing opaque dependency failure instead of a false
empty, shortened, forged, off-tick, or off-step history.

No prior frozen migration is edited. If receipt-derived reconstruction cannot
prove the complete state within the bounded migration contract, the migration
must roll back with no journal, ACL, row, trigger, function, or catalog partial
effect. Recovery then uses the established reconciliation and owner-run
procedure; the migration must not guess or synthesize economic facts.

## Required evidence

- red-first PostgreSQL 19 tests for pre-cutover truncate, forged pairs, orphan
  settlements/projections, hostile trigger drift, active-writer fencing, and
  atomic rollback;
- deterministic provenance derivation for multiple instrument changes,
  including explicit same-receipt ordinality;
- new-writer atomic settlement, projection, provenance, receipt, and checkpoint
  proof;
- reconciliation failure for missing, extra, or mismatched provenance;
- broker whole-page failure for missing provenance, off-tick price, off-step
  signed quantity, and late-row corruption;
- hostile ACL/default-privilege and security-definer execution-plan proof;
- full policy, format, lint, test, race, deterministic-repeat, vulnerability,
  exact-SHA specialist, hosted CI, and independent final release gates.

Stop rather than merge if any valid historical effect cannot be reconstructed
from immutable receipts, if an old writer can survive or reacquire ownership
after cutover, or if the broker read can omit a committed effect before
fail-closed validation.
