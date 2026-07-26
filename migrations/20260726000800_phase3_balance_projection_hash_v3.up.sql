-- Decision-hash v3 durable balance-projection authority.
--
-- Lock: both receipt tables take SHARE ROW EXCLUSIVE before the historical
-- guard, closing overlap with an in-flight old writer. The lock is bounded by
-- lock_timeout.
-- Rewrite: none. The guard is read-only and bounded by statement_timeout.
-- Transaction: the migrator applies this file atomically; guard refusal or
-- lock contention leaves the prior schema and immutable receipts unchanged.
-- Compatibility: v2 non-order receipts remain valid. Pre-v3 order receipts
-- require owner-reviewed forward repair or restore/reset. Once installed, the
-- trigger deliberately prevents an old writer from appending any v2
-- decision, including market-only decisions whose equity projection may be
-- incomplete.

SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '30s';

LOCK TABLE
    engine.input_receipts,
    engine.duplicate_delivery_receipts
IN SHARE ROW EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts
         WHERE decision_hash_version < 3
           AND jsonb_typeof(decision -> 'OrderChanges') = 'array'
           AND jsonb_array_length(decision -> 'OrderChanges') > 0
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'pre-v3 order receipts may have incomplete durable balance projections',
            HINT = 'keep writers halted; preserve the database and use an owner-reviewed forward repair or restore/reset before applying this migration';
    END IF;
END
$$;

CREATE FUNCTION engine.require_balance_projection_hash_v3()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.decision_hash_version < 3 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'new receipts require balance-projection decision hash version 3';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER input_receipts_require_balance_projection_hash_v3
BEFORE INSERT ON engine.input_receipts
FOR EACH ROW EXECUTE FUNCTION engine.require_balance_projection_hash_v3();

CREATE FUNCTION engine.require_duplicate_delivery_hash_v3()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(
        (NEW.decision ->> 'DecisionHashVersion')::integer,
        2
    ) < 3 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'new duplicate receipts require decision hash version 3';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER duplicate_receipts_require_decision_hash_v3
BEFORE INSERT ON engine.duplicate_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION engine.require_duplicate_delivery_hash_v3();
