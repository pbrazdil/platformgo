-- Validate the finite-positive fill-leverage constraint separately.
-- Lock/rewrite: VALIDATE CONSTRAINT scans trading.fills under SHARE UPDATE
-- EXCLUSIVE, which remains compatible with ordinary DML. It does not rewrite
-- the table. Lock acquisition and the scan are bounded for a clean retry.
-- Transaction: validation and the migration journal entry commit atomically.

SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE trading.fills
    VALIDATE CONSTRAINT fills_effective_leverage_finite_positive;
