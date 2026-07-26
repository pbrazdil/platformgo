Title: Preserve current Go execution-time leverage authority

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_fills.rs::fill_surfaces_frozen_effective_leverage`
- `internal/engine/trading_frozen_leverage_test.go::TestTradingFillFreezesEffectiveLeverageV4`
- `tests/integration/postgres/frozen_effective_leverage_test.go::TestFillSurfacesFrozenEffectiveLeverage`

Conflict or ambiguity:
The pinned source test writes optional values directly into a legacy fill
mirror and proves that leverage `10` surfaces exactly, `5.00` serializes
canonically as `5`, and a missing value remains absent. It does not define the
execution authority that produces the value, bind it into deterministic
decision/replay state, or specify how configuration changes interact with an
already committed fill. The accepted Go engine has no arbitrary fill-mirror
writer. Its v4 execution path derives the value from exactly one durable
account/instrument risk configuration, or the execution-time instrument maximum
when no explicit risk exists.

Economic/API impact:
Effective leverage is an exact positive economic input to the execution fact.
Decision-hash v4 binds it before the fill, state, receipt, and PostgreSQL
projection commit atomically. A later risk or instrument change cannot rewrite
history. Ambiguous, missing, nonpositive, or above-maximum authority fails
closed. Valid v2/v3 decisions remain replayable with absent/SQL `NULL`
leverage. This decision does not activate the external `/fills` route or claim
its complete response contract.

Options considered:
1. Add an unrestricted mirror writer and accept arbitrary nullable leverage as
   current execution behavior.
2. Reconstruct leverage while reading history or import legacy group/capping
   mechanics not established by this source assertion.
3. Preserve current deterministic Go execution authority, canonical exact
   values, immutable v4 binding, and historical v2/v3 absence while retaining
   the source's three observable assertions.

Decision:
Choose option 3. The current Go v4 execution path is authoritative: a fill
freezes the unique explicit account/instrument leverage when configured,
otherwise the instrument maximum. The exact value is canonicalized, hashed,
persisted, recovered, and reconciled as one immutable fact. Historical v2/v3
absence remains authoritative and is never inferred or backfilled.

Tests added/changed:
- `TestTradingFillFreezesEffectiveLeverageV4` proves explicit `10`, canonical
  `5.00` to `5`, the instrument-maximum default, and immutable history after a
  later risk change.
- Fixed v2/v3 and v4 hash tests prove historical compatibility and current
  deterministic binding.
- `TestFillSurfacesFrozenEffectiveLeverage` drives real engine execution through
  PostgreSQL 19 Beta 2 and proves atomic persistence, both duplicate-delivery
  paths, fresh-owner recovery, immutable compatibility reads, and fail-closed
  reconciliation.
- The migration 009/010 suite proves a no-backfill upgrade, exact decimal
  storage, invalid-authority preflight, old-writer fencing, bounded rollback and
  retry, and separate constraint validation.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-26
