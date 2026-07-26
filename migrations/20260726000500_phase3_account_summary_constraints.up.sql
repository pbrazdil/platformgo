-- Phase 3 client account-summary compatibility constraints.
-- Lock/rewrite: adding NOT VALID CHECK constraints takes ACCESS EXCLUSIVE but
-- does not scan or rewrite existing rows. Validation is deferred to the next
-- migration so this lock is released at commit before the table scan begins.
-- Lock acquisition and total execution are bounded for clean retry.
-- Transaction: applied atomically by the migrator.
-- Compatibility: new writes are checked immediately; older binaries already
-- write only the active/cross defaults added by the previous migration.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

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
