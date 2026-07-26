-- Decision-hash v3 durable balance-projection authority.
--
-- Lock: trading.instruments and market.books are locked before both receipt
-- tables, matching the writer's instrument-before-book-before-receipt order
-- and closing overlap with an in-flight old writer. Locks are bounded by
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

LOCK TABLE trading.instruments IN SHARE ROW EXCLUSIVE MODE;

LOCK TABLE market.books IN ACCESS EXCLUSIVE MODE;

LOCK TABLE
    engine.input_receipts,
    engine.duplicate_delivery_receipts
IN SHARE ROW EXCLUSIVE MODE;

ALTER TABLE market.books
    ALTER COLUMN mark_price DROP NOT NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM (
              SELECT
                  settlement_currency AS currency,
                  settlement_currency_scale AS scale
                FROM trading.instruments
              UNION ALL
              SELECT
                  change ->> 'SettlementCurrency' AS currency,
                  (change ->> 'SettlementCurrencyScale')::smallint AS scale
                FROM engine.input_receipts AS receipt
                CROSS JOIN LATERAL jsonb_array_elements(
                    CASE
                        WHEN jsonb_typeof(
                            receipt.decision -> 'InstrumentChanges'
                        ) = 'array'
                        THEN receipt.decision -> 'InstrumentChanges'
                        ELSE '[]'::jsonb
                    END
                ) AS change
          ) AS currency_scales
         WHERE currency IS NOT NULL
           AND scale IS NOT NULL
         GROUP BY currency
        HAVING count(DISTINCT scale) > 1
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'one currency code has multiple settlement scales',
            HINT = 'keep writers halted; preserve the database and resolve the catalog conflict through an owner-reviewed forward repair or restore/reset';
    END IF;

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

CREATE FUNCTION trading.require_currency_scale_consistency()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND NEW.settlement_currency = OLD.settlement_currency
       AND NEW.settlement_currency_scale <>
           OLD.settlement_currency_scale THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'settlement currency code must use one scale';
    END IF;
    IF EXISTS (
        SELECT 1
          FROM trading.instruments AS existing
         WHERE existing.settlement_currency = NEW.settlement_currency
           AND existing.settlement_currency_scale <>
               NEW.settlement_currency_scale
           AND existing.instrument_id <> NEW.instrument_id
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'settlement currency code must use one scale';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER instruments_require_currency_scale_consistency
BEFORE INSERT OR UPDATE ON trading.instruments
FOR EACH ROW EXECUTE FUNCTION trading.require_currency_scale_consistency();
