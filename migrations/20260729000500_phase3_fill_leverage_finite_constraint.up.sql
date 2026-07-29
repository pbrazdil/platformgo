-- Reject non-finite or non-positive execution-time fill leverage.
-- Lock/rewrite: the transaction first acquires the engine's configured-shard
-- ownership advisory lock, then adding this NOT VALID CHECK takes ACCESS
-- EXCLUSIVE on trading.fills. It does not scan or rewrite existing rows.
-- Validation is deferred so the table lock is released before the scan.
-- Transaction: the constraint and migration journal entry commit atomically.
-- Compatibility: NULL history remains valid and every new non-NULL value is
-- checked immediately. Lock acquisition and execution are bounded for retry.

SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '30s';

DO $$
DECLARE
    configured_shard integer;
BEGIN
    SELECT shard_id::integer
      INTO configured_shard
      FROM engine.deployment_shard
     WHERE singleton;
    IF configured_shard IS NOT NULL THEN
        PERFORM pg_advisory_xact_lock(1346850639, configured_shard);
    END IF;
END
$$;

ALTER TABLE trading.fills
    ADD CONSTRAINT fills_effective_leverage_finite_positive CHECK (
        effective_leverage IS NULL
        OR (
            effective_leverage > 0
            AND effective_leverage NOT IN (
                'NaN'::numeric,
                'Infinity'::numeric,
                '-Infinity'::numeric
            )
        )
    ) NOT VALID;
