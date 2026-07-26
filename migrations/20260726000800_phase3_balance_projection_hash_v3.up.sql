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

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
         FROM engine.input_receipts
         WHERE decision ? 'InstrumentChanges'
           AND jsonb_typeof(decision -> 'InstrumentChanges')
               NOT IN ('array', 'null')
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument change history is not a canonical array',
            HINT = 'keep writers halted; preserve the database and resolve the malformed receipt through an owner-reviewed forward repair or restore/reset';
    END IF;

    IF EXISTS (
        SELECT 1
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
         WHERE jsonb_typeof(change) <> 'object'
            OR COALESCE(change ->> 'SettlementCurrency', '') = ''
            OR CASE
                   WHEN COALESCE(
                       change ->> 'SettlementCurrencyScale',
                       ''
                   ) ~ '^[0-9]+$'
                   THEN (change ->> 'SettlementCurrencyScale')::numeric
                        NOT BETWEEN 0 AND 18
                   ELSE true
               END
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument change history has an invalid currency identity',
            HINT = 'keep writers halted; preserve the database and resolve the malformed receipt through an owner-reviewed forward repair or restore/reset';
    END IF;

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

CREATE TABLE trading.currency_scales (
    currency text PRIMARY KEY CHECK (currency <> ''),
    scale smallint NOT NULL CHECK (scale BETWEEN 0 AND 18)
);

INSERT INTO trading.currency_scales (currency, scale)
SELECT currency, min(scale)
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
 GROUP BY currency;

ALTER TABLE market.books
    ALTER COLUMN mark_price DROP NOT NULL;

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
SECURITY DEFINER
SET search_path = pg_catalog, trading
AS $$
DECLARE
    registered_scale smallint;
BEGIN
    INSERT INTO trading.currency_scales (currency, scale)
    VALUES (NEW.settlement_currency, NEW.settlement_currency_scale)
    ON CONFLICT (currency) DO NOTHING;

    SELECT scale
      INTO registered_scale
      FROM trading.currency_scales
     WHERE currency = NEW.settlement_currency;

    IF registered_scale IS DISTINCT FROM NEW.settlement_currency_scale THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'settlement currency code must use one scale';
    END IF;
    RETURN NEW;
END
$$;

REVOKE ALL ON FUNCTION trading.require_currency_scale_consistency()
FROM PUBLIC;

CREATE TRIGGER instruments_require_currency_scale_consistency
BEFORE INSERT OR UPDATE ON trading.instruments
FOR EACH ROW EXECUTE FUNCTION trading.require_currency_scale_consistency();

GRANT SELECT ON trading.currency_scales TO platformgo_engine;
