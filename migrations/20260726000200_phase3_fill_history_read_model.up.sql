-- Phase 3 fill-history compatibility index.
-- Lock/rewrite: CREATE INDEX takes SHARE lock on the immutable fills table and
-- reads existing rows without rewriting them. Lock acquisition is bounded to
-- five seconds and the whole build to fifteen seconds so an unexpectedly large
-- or busy production table fails closed and rolls back instead of causing an
-- unbounded write outage.
-- Transaction: applied atomically by the migrator.
-- Compatibility: older binaries ignore the additive index.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

CREATE INDEX fills_account_history_idx
ON trading.fills (account_id, logical_time DESC, fill_id DESC);
