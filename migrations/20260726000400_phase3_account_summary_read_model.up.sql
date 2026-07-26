-- Phase 3 client account-summary compatibility columns.
-- Lock/rewrite: ADD COLUMN takes a brief ACCESS EXCLUSIVE lock. PostgreSQL 17
-- stores these constant defaults in catalog metadata, so existing rows are not
-- rewritten. Constraints are added and validated only in later migrations so
-- this ACCESS EXCLUSIVE transaction commits before either operation.
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
