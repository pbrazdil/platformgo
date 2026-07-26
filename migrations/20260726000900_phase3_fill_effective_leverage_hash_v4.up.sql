-- Decision-hash v4 durable execution-time leverage authority.
--
-- Cutover: the transaction first acquires the engine's shard-ownership
-- advisory lock. A live old engine therefore makes the migration fail within
-- lock_timeout, and no legitimate writer can overlap the DDL or version fence.
-- Rewrite: adding a nullable column without a default is metadata-only.
-- Historical fills deliberately remain NULL; no leverage is reconstructed.
-- Transaction: the migrator applies this file atomically, so lock contention
-- or a guard refusal leaves the v3 schema and immutable history unchanged.
-- Compatibility: existing v2/v3 receipts and NULL fill history remain valid.
-- The positive constraint remains NOT VALID here: it already protects every
-- new write, while the following migration validates history without retaining
-- this migration's short ACCESS EXCLUSIVE column-add lock.
-- Once installed, the decision-hash runtime fence covers business receipts,
-- duplicate receipts, faults, and checkpoints from every old v3 writer.

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

    IF EXISTS (
        SELECT 1
          FROM trading.risk_configs AS risk_config
          JOIN trading.instruments AS instrument
            ON instrument.instrument_id = risk_config.instrument_id
         WHERE risk_config.leverage > instrument.max_leverage
    ) THEN
        RAISE EXCEPTION
            'preexisting risk leverage exceeds instrument maximum'
            USING ERRCODE = '55000';
    END IF;
END
$$;

ALTER TABLE trading.fills
    ADD COLUMN effective_leverage numeric(38, 18),
    ADD CONSTRAINT fills_effective_leverage_positive
        CHECK (
            effective_leverage IS NULL
            OR effective_leverage > 0
        )
        NOT VALID;

CREATE FUNCTION engine.require_decision_hash_v4_runtime()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF current_setting(
           'platformgo.engine_decision_hash_version',
           true
       ) IS DISTINCT FROM '4' THEN
        RAISE EXCEPTION
            'engine decision-hash runtime version is missing or incompatible'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER input_receipts_require_decision_hash_v4_runtime
BEFORE INSERT ON engine.input_receipts
FOR EACH ROW EXECUTE FUNCTION engine.require_decision_hash_v4_runtime();

CREATE TRIGGER duplicate_delivery_receipts_require_decision_hash_v4_runtime
BEFORE INSERT ON engine.duplicate_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION engine.require_decision_hash_v4_runtime();

CREATE TRIGGER shard_faults_require_decision_hash_v4_runtime
BEFORE INSERT ON engine.shard_faults
FOR EACH ROW EXECUTE FUNCTION engine.require_decision_hash_v4_runtime();

CREATE TRIGGER shard_checkpoints_require_decision_hash_v4_runtime
BEFORE INSERT OR UPDATE ON engine.shard_checkpoints
FOR EACH ROW EXECUTE FUNCTION engine.require_decision_hash_v4_runtime();

REVOKE ALL ON FUNCTION engine.require_decision_hash_v4_runtime() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION engine.require_decision_hash_v4_runtime()
TO platformgo_engine;

CREATE FUNCTION engine.require_fill_effective_leverage_hash_v4()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.decision_hash_version < 4 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'new receipts require fill effective-leverage decision hash version 4';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER input_receipts_require_fill_effective_leverage_hash_v4
BEFORE INSERT ON engine.input_receipts
FOR EACH ROW EXECUTE FUNCTION engine.require_fill_effective_leverage_hash_v4();

CREATE FUNCTION engine.require_duplicate_delivery_hash_v4()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(
        (NEW.decision ->> 'DecisionHashVersion')::integer,
        2
    ) < 4 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'new duplicate receipts require decision hash version 4';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER duplicate_receipts_require_decision_hash_v4
BEFORE INSERT ON engine.duplicate_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION engine.require_duplicate_delivery_hash_v4();
