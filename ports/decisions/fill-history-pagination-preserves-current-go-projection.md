Title: Preserve the current Go fill projection while accepting pagination

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/trading/e2e_fills.rs::fills_history_reads_and_paginates`
- `tests/integration/postgres/fill_history_test.go::TestFillsHistoryReadsAndPaginates`

Conflict or ambiguity:
The pinned source test proves limit-based pagination, a filter-wide total, a
cursor when older rows remain, a final page, and exactly-once traversal. Its
first row also asserts the legacy mirror's enriched shape, including catalog,
position, commission, leverage, reason, liquidity, order-type, and user
metadata. The current Go compatibility store intentionally exposes a narrow
internal immutable fill projection. Some legacy fields are not durably
available at this boundary, and current Go fills require a correlatable order
and a classified position effect. The external `/fills` route remains
inventory.

Economic/API impact:
The accepted behavior is read-only. It cannot create or mutate an order, fill,
position, ledger entry, balance, or outbox record. Account and optional side or
fill-ID filters apply identically to both the page and its total in one
PostgreSQL statement. Strict `(logical_time, fill_id)` tuple cursors provide a
stable unique order even when execution times match. This decision does not
activate the external fills route or add legacy fields to a frozen wire
contract.

Options considered:
1. Recreate the legacy mirror row and invent or denormalize every asserted
   field before accepting its pagination behavior.
2. Activate an incomplete external fills route around the current narrow
   projection.
3. Preserve the current Go projection as authority and accept the source
   pagination requirements independently of unimplemented legacy row fields.

Decision:
Choose option 3. The accepted observable requirements are the bounded first
page, filter-wide total, opaque continuation cursor, final page, and exactly
once traversal. Deterministic UUID fill IDs replace the source fixture's
free-form identifiers, with the UUID as the tie-breaker for equal logical
times. The current narrow internal Go projection remains authoritative. The
implementation does not import the legacy mirror shape, invent unavailable
catalog metadata, weaken the durable order/effect model, or activate the
external fills route. External field and route compatibility require their own
complete implementation and review.

Tests added/changed:
- `TestFillsHistoryReadsAndPaginates` proves newest-first pagination across
  three equal-logical-time fills, stable filter-wide totals on nonempty and
  empty windows, forward and backward navigation, cursorless backward
  canonicalization, malformed-cursor rejection, and exactly-once traversal.
- The PostgreSQL 17 regression exercises the least-privilege API role and the
  exact production query. Its 100,000-fill plan proof requires the existing
  account-history or account-side-history index and rejects a sequential scan
  of `trading.fills`.
- The test exercises only the current internal fill projection. It does not
  prove or activate an external HTTP fills response.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-26
