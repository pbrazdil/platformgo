-- Phase 3 client account-summary compatibility state.
-- Lock/rewrite: ADD COLUMN takes a brief ACCESS EXCLUSIVE lock. PostgreSQL 17
-- stores these constant defaults in catalog metadata, so existing rows are not
-- rewritten. Constraint validation scans trading.accounts under a
-- SHARE UPDATE EXCLUSIVE lock. Lock acquisition and total execution are
-- bounded so contention fails closed and the whole migration can be retried.
-- Transaction: applied atomically by the migrator.
-- Compatibility: older binaries ignore the additive columns and continue to
-- insert accounts through the active/cross defaults. Those defaults describe
-- the pre-migration runtime, where configured accounts admit normal trading
-- and unspecified instrument risk is cross-margin.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

ALTER TABLE trading.accounts
    ADD COLUMN status text NOT NULL DEFAULT 'ACTIVE',
    ADD COLUMN margin_mode text NOT NULL DEFAULT 'CROSS';

ALTER TABLE trading.accounts
    ADD CONSTRAINT accounts_status_check CHECK (
        status IN (
            'PENDING',
            'ACTIVE',
            'CLOSE_ONLY',
            'FROZEN',
            'READ_ONLY',
            'SUSPENDED',
            'CLOSED'
        )
    ) NOT VALID,
    ADD CONSTRAINT accounts_margin_mode_check CHECK (
        margin_mode IN ('CROSS', 'ISOLATED')
    ) NOT VALID;

ALTER TABLE trading.accounts
    VALIDATE CONSTRAINT accounts_status_check;

ALTER TABLE trading.accounts
    VALIDATE CONSTRAINT accounts_margin_mode_check;
