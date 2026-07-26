-- Phase 3 client account-summary constraint validation.
-- Lock/rewrite: VALIDATE CONSTRAINT scans existing trading.accounts rows under
-- SHARE UPDATE EXCLUSIVE, which remains compatible with ordinary SELECT,
-- INSERT, UPDATE, and DELETE traffic. The earlier ACCESS EXCLUSIVE migrations
-- have already committed. No table rewrite occurs.
-- Lock acquisition and total execution are bounded for clean retry.
-- Transaction: both validations and the migration journal entry commit
-- atomically.
-- Compatibility: validation changes no accepted value or runtime contract.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

ALTER TABLE trading.accounts
    VALIDATE CONSTRAINT accounts_status_check;

ALTER TABLE trading.accounts
    VALIDATE CONSTRAINT accounts_margin_mode_check;
