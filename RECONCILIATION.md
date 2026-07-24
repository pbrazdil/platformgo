# Reconciliation and Integrity Checking

## 1. Purpose

Recovery is not complete merely because processes restart. The platform must continuously prove that durable monetary state is internally consistent.

Reconciliation detects divergence; it does not silently repair it.

## 2. Online invariant checks

The engine validates affected invariants before every economic transaction commits. At minimum:

- order filled quantity equals the sum of newly/currently linked fills;
- position transition matches the applied fills and OMS mode;
- ledger transaction balances by currency;
- balance projection sequence is monotonic;
- reduce-only and protection rules hold;
- command/input identity has not already produced a different result.

A failure rolls back the transaction and removes shard readiness.

## 3. Continuous reconciliation jobs

Idempotent jobs periodically verify:

### Ledger and balances

- every ledger transaction balances by currency;
- account balance projection equals ledger fold at its sequence;
- no duplicated business key/transaction effect;
- no unexplained negative/invalid balance where policy forbids it.

### Orders and fills

- cumulative filled quantity equals fill sum;
- no fill references the wrong account/instrument/order;
- terminal state and remaining quantity agree;
- no duplicate fill identity;
- command result agrees with order outcome.

### Positions and risk

- positions equal the fold of fills after the last trusted checkpoint;
- realized PnL and average entry are reproducible;
- flat positions have no active protection;
- margin/reservations agree with positions and working orders;
- stop-out/liquidation jobs have one effect.

### Messaging

- unpublished outbox age/count within SLO;
- no inbox effect without an inbox record;
- engine checkpoint and input receipt are consistent;
- no unexplained stream-sequence gap;
- realtime sequence is monotonic.

## 4. Canonical snapshots

Provide a canonical account snapshot containing:

```text
account identity and config version
ledger sequence and balance totals
orders and fills
positions and protection
margin/equity values
last engine input and market sequence
state hash
```

Sorting and decimal formatting follow deterministic canonicalization rules. The snapshot is used for diagnostics, restore validation, cutover and test assertions.

## 5. Mismatch handling

On mismatch:

1. emit a high-severity alert with stable account/input IDs, never secrets;
2. prevent risk-increasing commands for affected scope;
3. preserve evidence and current state;
4. do not update rows to “make them match”;
5. identify the authoritative immutable facts;
6. repair with a reviewed compensating command/migration;
7. rerun reconciliation and record the incident.

Broad automatic repair of monetary state is forbidden.

## 6. Restore and release reconciliation

Run full reconciliation:

- after PostgreSQL restore/failover;
- after engine checkpoint recovery;
- before and after migrations affecting money paths;
- before enabling trading after deployment;
- before and after replacement cutover;
- after load/soak and chaos tests.

## 7. Required tests

- deliberately corrupted projections are detected;
- immutable facts are not modified by reconciliation;
- duplicate fills/ledger effects are detected;
- replayed snapshots have stable hashes;
- reconciliation can resume idempotently;
- affected accounts/shards enter fail-closed mode;
- compensating repair produces an auditable result.
