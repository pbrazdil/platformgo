-- Fence the append-only currency-scale authority against writers and trigger
-- functions that obtained privileges before the earlier ACL repair.
--
-- Lock: take the engine-owner advisory lock, then SHARE ROW EXCLUSIVE on
-- instruments, currency_scales, and business receipts in production writer
-- order. This drains old writes and retains the fence through validation,
-- trigger replacement, ACL repair, and the migration journal commit.
-- Rewrite: none. Existing relation rows and storage are never rewritten.
-- Transaction: any timeout, malformed authority, or registry mismatch rolls
-- back the complete migration. Invalid facts are preserved for owner review;
-- this migration never repairs, removes, or invents an economic binding.
-- Compatibility: the runtime revision is advanced atomically. A process that
-- verified the prior tip but resumes after this commit fails its next guarded
-- engine write rather than continuing with pre-fence semantics.

SET LOCAL lock_timeout = '5s';
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
        PERFORM pg_catalog.pg_advisory_xact_lock(
            1346850639,
            configured_shard
        );
    END IF;
END
$$;

LOCK TABLE trading.instruments IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE trading.currency_scales IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE engine.input_receipts IN SHARE ROW EXCLUSIVE MODE;

-- Revoking TRIGGER does not remove objects created while the grant existed.
-- Inventory every non-internal trigger on the three authority relations before
-- trusting their rows or publishing a new trigger set.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_trigger AS trigger
         WHERE trigger.tgrelid IN (
                   'trading.instruments'::pg_catalog.regclass,
                   'trading.currency_scales'::pg_catalog.regclass,
                   'engine.input_receipts'::pg_catalog.regclass
               )
           AND NOT trigger.tgisinternal
           AND NOT (
               trigger.tgqual IS NULL
               AND trigger.tgattr::pg_catalog.text = ''
               AND trigger.tgoldtable IS NULL
               AND trigger.tgnewtable IS NULL
               AND pg_catalog.octet_length(trigger.tgargs) = 0
               AND (
                 (
                   trigger.tgrelid =
                       'trading.instruments'::pg_catalog.regclass
                   AND trigger.tgname =
                       'instruments_require_currency_scale_consistency'
                   AND trigger.tgfoid =
                       'trading.require_currency_scale_consistency()'::pg_catalog.regprocedure
                   AND trigger.tgtype = 23
                   AND trigger.tgenabled = 'O'
                   AND trigger.tgnargs = 0
               )
               OR (
                   trigger.tgrelid =
                       'trading.currency_scales'::pg_catalog.regclass
                   AND trigger.tgname =
                       'currency_scale_registry_is_append_only'
                   AND trigger.tgfoid =
                       'trading.reject_currency_scale_registry_mutation()'::pg_catalog.regprocedure
                   AND trigger.tgtype = 58
                   AND trigger.tgenabled = 'O'
                   AND trigger.tgnargs = 0
               )
               OR (
                   trigger.tgrelid =
                       'engine.input_receipts'::pg_catalog.regclass
                   AND trigger.tgname = 'input_receipts_are_immutable'
                   AND trigger.tgfoid =
                       'engine.reject_immutable_change()'::pg_catalog.regprocedure
                   AND trigger.tgtype = 27
                   AND trigger.tgenabled = 'O'
                   AND trigger.tgnargs = 0
               )
               OR (
                   trigger.tgrelid =
                       'engine.input_receipts'::pg_catalog.regclass
                   AND trigger.tgname =
                       'input_receipts_require_runtime_schema_revision'
                   AND trigger.tgfoid =
                       'engine.require_runtime_schema_revision()'::pg_catalog.regprocedure
                   AND trigger.tgtype = 7
                   AND trigger.tgenabled = 'O'
                   AND trigger.tgnargs = 0
               )
               OR (
                   trigger.tgrelid =
                       'engine.input_receipts'::pg_catalog.regclass
                   AND trigger.tgname =
                       'input_receipts_require_balance_projection_hash_v3'
                   AND trigger.tgfoid =
                       'engine.require_balance_projection_hash_v3()'::pg_catalog.regprocedure
                   AND trigger.tgtype = 7
                   AND trigger.tgenabled = 'O'
                   AND trigger.tgnargs = 0
               )
               OR (
                   trigger.tgrelid =
                       'engine.input_receipts'::pg_catalog.regclass
                   AND trigger.tgname =
                       'input_receipts_require_decision_hash_v4_runtime'
                   AND trigger.tgfoid =
                       'engine.require_decision_hash_v4_runtime()'::pg_catalog.regprocedure
                   AND trigger.tgtype = 7
                   AND trigger.tgenabled = 'O'
                   AND trigger.tgnargs = 0
               )
               OR (
                   trigger.tgrelid =
                       'engine.input_receipts'::pg_catalog.regclass
                   AND trigger.tgname =
                       'input_receipts_require_fill_effective_leverage_hash_v4'
                   AND trigger.tgfoid =
                       'engine.require_fill_effective_leverage_hash_v4()'::pg_catalog.regprocedure
                   AND trigger.tgtype = 7
                   AND trigger.tgenabled = 'O'
                   AND trigger.tgnargs = 0
               )
               OR (
                   trigger.tgrelid =
                       'engine.input_receipts'::pg_catalog.regclass
                   AND trigger.tgname =
                       'input_receipts_require_authoritative_market_state'
                   AND trigger.tgfoid =
                       'engine.require_authoritative_market_receipt()'::pg_catalog.regprocedure
                   AND trigger.tgtype = 7
                   AND trigger.tgenabled = 'O'
                   AND trigger.tgnargs = 0
               )
               )
           )
    ) OR (
        SELECT pg_catalog.count(*)
          FROM pg_catalog.pg_trigger AS trigger
         WHERE trigger.tgrelid IN (
                   'trading.instruments'::pg_catalog.regclass,
                   'trading.currency_scales'::pg_catalog.regclass,
                   'engine.input_receipts'::pg_catalog.regclass
               )
           AND NOT trigger.tgisinternal
    ) <> 8
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'currency authority relation has an unexpected pre-cutover trigger',
            HINT = 'keep writers halted; preserve trigger and row evidence for owner classification, remove it only after proving or restoring authority, then retry';
    END IF;
END
$$;

-- A source that carried an unexpected mutation grant before this lock cannot
-- attest its own rows. Do not scrub that evidence and bless possibly forged
-- facts; stop for owner classification or restore instead.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_class AS relation
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              COALESCE(
                  relation.relacl,
                  pg_catalog.acldefault('r', relation.relowner)
              )
          ) AS privilege
         WHERE relation.oid IN (
                   'trading.instruments'::pg_catalog.regclass,
                   'engine.input_receipts'::pg_catalog.regclass
               )
           AND privilege.grantee <> relation.relowner
           AND privilege.privilege_type IN (
                   'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'TRIGGER'
               )
           AND NOT (
               relation.oid =
                   'trading.instruments'::pg_catalog.regclass
               AND privilege.grantee =
                   'platformgo_engine'::pg_catalog.regrole
               AND privilege.privilege_type IN ('INSERT', 'UPDATE')
               AND NOT privilege.is_grantable
           )
           AND NOT (
               relation.oid =
                   'engine.input_receipts'::pg_catalog.regclass
               AND privilege.grantee =
                   'platformgo_engine'::pg_catalog.regrole
               AND privilege.privilege_type = 'INSERT'
               AND NOT privilege.is_grantable
           )
    ) OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_attribute AS attribute
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = attribute.attrelid
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              attribute.attacl
          ) AS privilege
         WHERE attribute.attrelid IN (
                   'trading.instruments'::pg_catalog.regclass,
                   'engine.input_receipts'::pg_catalog.regclass
               )
           AND attribute.attnum > 0
           AND NOT attribute.attisdropped
           AND privilege.grantee <> relation.relowner
           AND privilege.privilege_type IN ('INSERT', 'UPDATE')
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'currency authority source carried an unexpected mutation grant before cutover',
            HINT = 'keep writers halted; preserve ACL and row evidence for owner classification, revoke only after proving or restoring source authority, then retry';
    END IF;
END
$$;

-- Reconstruct expected authority without consulting the registry. Reject
-- malformed sources before any cast, then require bidirectional equality.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts
         WHERE pg_catalog.jsonb_typeof(decision)
               IS DISTINCT FROM 'object'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'receipt decision history is not a canonical object',
            HINT = 'keep writers halted; preserve the database and resolve the malformed receipt through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts
         WHERE decision ? 'InstrumentChanges'
           AND pg_catalog.jsonb_typeof(decision -> 'InstrumentChanges')
               NOT IN ('array', 'null')
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument change history is not a canonical array',
            HINT = 'keep writers halted; preserve the database and resolve the malformed receipt through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts
         WHERE CASE
                   WHEN pg_catalog.jsonb_typeof(
                       decision -> 'InstrumentChanges'
                   ) = 'array'
                   THEN pg_catalog.jsonb_array_length(
                       decision -> 'InstrumentChanges'
                   )
                   ELSE 0
               END > 1
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument change history has an impossible effect cardinality',
            HINT = 'keep writers halted; preserve the database and resolve the malformed receipt through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts AS receipt
          CROSS JOIN LATERAL pg_catalog.jsonb_array_elements(
              CASE
                  WHEN pg_catalog.jsonb_typeof(
                      receipt.decision -> 'InstrumentChanges'
                  ) = 'array'
                  THEN receipt.decision -> 'InstrumentChanges'
                  ELSE '[]'::pg_catalog.jsonb
              END
         ) AS change
         WHERE pg_catalog.jsonb_typeof(change) <> 'object'
            OR pg_catalog.jsonb_typeof(change -> 'InstrumentID')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'InstrumentID', '') = ''
            OR pg_catalog.octet_length(change ->> 'InstrumentID') > 255
            OR change ->> 'InstrumentID' IS DISTINCT FROM
               pg_catalog.btrim(change ->> 'InstrumentID')
            OR change ->> 'InstrumentID' ~ '[[:space:][:cntrl:]]'
            OR pg_catalog.jsonb_typeof(change -> 'Revision')
               IS DISTINCT FROM 'number'
            OR CASE
                   WHEN COALESCE(change ->> 'Revision', '')
                        ~ '^[1-9][0-9]*$'
                   THEN (change ->> 'Revision')::pg_catalog.numeric
                        <= 9223372036854775807
                   ELSE false
               END IS NOT TRUE
            OR pg_catalog.jsonb_typeof(change -> 'PriceScale')
               IS DISTINCT FROM 'number'
            OR COALESCE(change ->> 'PriceScale', '')
               !~ '^(0|[1-9]|1[0-8])$'
            OR pg_catalog.jsonb_typeof(change -> 'QuantityScale')
               IS DISTINCT FROM 'number'
            OR COALESCE(change ->> 'QuantityScale', '')
               !~ '^(0|[1-9]|1[0-8])$'
            OR pg_catalog.jsonb_typeof(change -> 'SettlementCurrency')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'SettlementCurrency', '')
               !~ '^[A-Z0-9]{3,12}$'
            OR pg_catalog.jsonb_typeof(
                change -> 'SettlementCurrencyScale'
            ) IS DISTINCT FROM 'number'
            OR COALESCE(
                change ->> 'SettlementCurrencyScale',
                ''
            ) !~ '^(0|[1-9]|1[0-8])$'
            OR pg_catalog.jsonb_typeof(change -> 'InitialMarginRate')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'InitialMarginRate', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]+)?$'
            OR pg_catalog.jsonb_typeof(
                change -> 'MaintenanceMarginRate'
            ) IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'MaintenanceMarginRate', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]+)?$'
            OR pg_catalog.jsonb_typeof(change -> 'MaxLeverage')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'MaxLeverage', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]+)?$'
            OR pg_catalog.jsonb_typeof(change -> 'MakerFeeRate')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'MakerFeeRate', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]+)?$'
            OR pg_catalog.jsonb_typeof(change -> 'TakerFeeRate')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'TakerFeeRate', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]+)?$'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument change history has an invalid snapshot shape',
            HINT = 'keep writers halted; preserve the database and resolve the malformed receipt through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts AS receipt
          CROSS JOIN LATERAL pg_catalog.jsonb_array_elements(
              CASE
                  WHEN pg_catalog.jsonb_typeof(
                      receipt.decision -> 'InstrumentChanges'
                  ) = 'array'
                  THEN receipt.decision -> 'InstrumentChanges'
                  ELSE '[]'::pg_catalog.jsonb
              END
          ) AS change
          CROSS JOIN LATERAL (
              VALUES
                  ('initial_margin_rate', change ->> 'InitialMarginRate'),
                  ('maintenance_margin_rate', change ->> 'MaintenanceMarginRate'),
                  ('max_leverage', change ->> 'MaxLeverage'),
                  ('maker_fee_rate', change ->> 'MakerFeeRate'),
                  ('taker_fee_rate', change ->> 'TakerFeeRate')
          ) AS economic(field_name, value_text)
         WHERE economic.value_text IS DISTINCT FROM
                   pg_catalog.trim_scale(
                       economic.value_text::pg_catalog.numeric
                   )::pg_catalog.text
            OR pg_catalog.scale(
                   economic.value_text::pg_catalog.numeric
               ) > 18
            OR pg_catalog.length(
                   pg_catalog.split_part(
                       pg_catalog.ltrim(economic.value_text, '-'),
                       '.',
                       1
                   )
               ) > 20
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument change history has a noncanonical exact value',
            HINT = 'keep writers halted; preserve the database and resolve the malformed receipt through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts AS receipt
          CROSS JOIN LATERAL pg_catalog.jsonb_array_elements(
              CASE
                  WHEN pg_catalog.jsonb_typeof(
                      receipt.decision -> 'InstrumentChanges'
                  ) = 'array'
                  THEN receipt.decision -> 'InstrumentChanges'
                  ELSE '[]'::pg_catalog.jsonb
              END
          ) AS change
         WHERE (change ->> 'InitialMarginRate')::pg_catalog.numeric < 0
            OR (change ->> 'MaintenanceMarginRate')::pg_catalog.numeric < 0
            OR (change ->> 'MaxLeverage')::pg_catalog.numeric <= 0
            OR (change ->> 'MakerFeeRate')::pg_catalog.numeric
               NOT BETWEEN -1 AND 1
            OR (change ->> 'TakerFeeRate')::pg_catalog.numeric
               NOT BETWEEN -1 AND 1
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument change history has an out-of-domain exact value',
            HINT = 'keep writers halted; preserve the database and resolve the invalid instrument effect through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts AS receipt
         WHERE CASE
                   WHEN pg_catalog.jsonb_typeof(
                       receipt.decision -> 'InstrumentChanges'
                   ) = 'array'
                   THEN pg_catalog.jsonb_array_length(
                       receipt.decision -> 'InstrumentChanges'
                   ) > 0
                   ELSE false
               END
           AND (
               pg_catalog.jsonb_typeof(
                   receipt.decision -> 'CommandResult'
               ) IS DISTINCT FROM 'object'
               OR pg_catalog.jsonb_typeof(
                   receipt.decision #> '{CommandResult,Status}'
               ) IS DISTINCT FROM 'string'
               OR receipt.decision #>> '{CommandResult,Status}'
                  IS DISTINCT FROM 'accepted'
           )
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument change history is not an accepted decision',
            HINT = 'keep writers halted; preserve the database and resolve the non-authoritative receipt through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM trading.instruments
         WHERE settlement_currency !~ '^[A-Z0-9]{3,12}$'
            OR settlement_currency_scale NOT BETWEEN 0 AND 18
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument catalog has an invalid currency identity',
            HINT = 'keep writers halted; preserve the database and resolve the malformed catalog through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        WITH changes AS (
            SELECT
                receipt.shard_id,
                receipt.stream_sequence,
                expanded.ordinality,
                expanded.change,
                pg_catalog.count(*) OVER (
                    PARTITION BY expanded.change ->> 'InstrumentID'
                ) AS projection_version
              FROM engine.input_receipts AS receipt
              CROSS JOIN LATERAL pg_catalog.jsonb_array_elements(
                  CASE
                      WHEN pg_catalog.jsonb_typeof(
                          receipt.decision -> 'InstrumentChanges'
                      ) = 'array'
                      THEN receipt.decision -> 'InstrumentChanges'
                      ELSE '[]'::pg_catalog.jsonb
                  END
              ) WITH ORDINALITY AS expanded(change, ordinality)
             WHERE receipt.decision #>>
                   '{CommandResult,Status}' = 'accepted'
        ),
        latest AS (
            SELECT DISTINCT ON (change ->> 'InstrumentID')
                change,
                projection_version
              FROM changes
             ORDER BY
                change ->> 'InstrumentID',
                shard_id DESC,
                stream_sequence DESC,
                ordinality DESC
        )
        SELECT 1
          FROM latest
          FULL OUTER JOIN trading.instruments AS instrument
            ON instrument.instrument_id =
               latest.change ->> 'InstrumentID'
         WHERE latest.change IS NULL
            OR instrument.instrument_id IS NULL
            OR instrument.revision IS DISTINCT FROM
               (latest.change ->> 'Revision')::pg_catalog.int8
            OR instrument.price_scale IS DISTINCT FROM
               (latest.change ->> 'PriceScale')::pg_catalog.int2
            OR instrument.quantity_scale IS DISTINCT FROM
               (latest.change ->> 'QuantityScale')::pg_catalog.int2
            OR instrument.settlement_currency IS DISTINCT FROM
               latest.change ->> 'SettlementCurrency'
            OR instrument.settlement_currency_scale IS DISTINCT FROM
               (
                   latest.change ->> 'SettlementCurrencyScale'
               )::pg_catalog.int2
            OR pg_catalog.trim_scale(
                   instrument.initial_margin_rate
               )::pg_catalog.text IS DISTINCT FROM
               latest.change ->> 'InitialMarginRate'
            OR pg_catalog.trim_scale(
                   instrument.maintenance_margin_rate
               )::pg_catalog.text IS DISTINCT FROM
               latest.change ->> 'MaintenanceMarginRate'
            OR pg_catalog.trim_scale(
                   instrument.max_leverage
               )::pg_catalog.text IS DISTINCT FROM
               latest.change ->> 'MaxLeverage'
            OR pg_catalog.trim_scale(
                   instrument.maker_fee_rate
               )::pg_catalog.text IS DISTINCT FROM
               latest.change ->> 'MakerFeeRate'
            OR pg_catalog.trim_scale(
                   instrument.taker_fee_rate
               )::pg_catalog.text IS DISTINCT FROM
               latest.change ->> 'TakerFeeRate'
            OR instrument.version IS DISTINCT FROM
               latest.projection_version
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'current instrument projection does not equal accepted durable history',
            HINT = 'keep writers halted; preserve the database and resolve the unproven catalog projection through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM (
              SELECT
                  change ->> 'SettlementCurrency' AS currency,
                  (
                      change ->> 'SettlementCurrencyScale'
                  )::pg_catalog.int2 AS scale
                FROM engine.input_receipts AS receipt
                CROSS JOIN LATERAL pg_catalog.jsonb_array_elements(
                    CASE
                        WHEN pg_catalog.jsonb_typeof(
                            receipt.decision -> 'InstrumentChanges'
                        ) = 'array'
                        THEN receipt.decision -> 'InstrumentChanges'
                        ELSE '[]'::pg_catalog.jsonb
                    END
                ) AS change
               WHERE receipt.decision #>>
                     '{CommandResult,Status}' = 'accepted'
          ) AS authority
         GROUP BY authority.currency
        HAVING pg_catalog.count(DISTINCT authority.scale) > 1
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'one currency code has multiple authoritative settlement scales',
            HINT = 'keep writers halted; preserve the database and resolve the authority conflict through an owner-reviewed forward repair or complete restore';
    END IF;

    IF EXISTS (
        WITH authoritative AS (
            SELECT
                authority.currency,
                pg_catalog.min(authority.scale) AS scale
              FROM (
                  SELECT
                      change ->> 'SettlementCurrency' AS currency,
                      (
                          change ->> 'SettlementCurrencyScale'
                      )::pg_catalog.int2 AS scale
                    FROM engine.input_receipts AS receipt
                    CROSS JOIN LATERAL pg_catalog.jsonb_array_elements(
                        CASE
                            WHEN pg_catalog.jsonb_typeof(
                                receipt.decision -> 'InstrumentChanges'
                            ) = 'array'
                            THEN receipt.decision -> 'InstrumentChanges'
                            ELSE '[]'::pg_catalog.jsonb
                        END
                    ) AS change
                   WHERE receipt.decision #>>
                         '{CommandResult,Status}' = 'accepted'
              ) AS authority
             GROUP BY authority.currency
        )
        SELECT 1
          FROM authoritative
          FULL OUTER JOIN trading.currency_scales AS registry
            ON registry.currency = authoritative.currency
         WHERE authoritative.currency IS NULL
            OR registry.currency IS NULL
            OR authoritative.scale IS DISTINCT FROM registry.scale
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'currency scale registry does not equal durable instrument authority',
            HINT = 'keep writers halted; do not repair registry facts in place; use an owner-reviewed forward repair or complete restore';
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION trading.require_currency_scale_consistency()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    registered_scale pg_catalog.int2;
BEGIN
    IF TG_RELID IS DISTINCT FROM 'trading.instruments'::pg_catalog.regclass
       OR TG_NAME IS DISTINCT FROM
          'instruments_require_currency_scale_consistency'
       OR TG_LEVEL IS DISTINCT FROM 'ROW'
       OR TG_WHEN IS DISTINCT FROM 'AFTER'
       OR TG_OP NOT IN ('INSERT', 'UPDATE')
       OR TG_NARGS <> 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'currency scale consistency function has invalid trigger origin';
    END IF;

    SELECT registry.scale
      INTO registered_scale
      FROM trading.currency_scales AS registry
     WHERE registry.currency = NEW.settlement_currency;

    IF FOUND THEN
        IF registered_scale IS DISTINCT FROM NEW.settlement_currency_scale THEN
            RAISE EXCEPTION USING
                ERRCODE = '23514',
                MESSAGE = 'settlement currency code must use one scale';
        END IF;
        RETURN NEW;
    END IF;

    INSERT INTO trading.currency_scales (currency, scale)
    VALUES (NEW.settlement_currency, NEW.settlement_currency_scale)
    ON CONFLICT (currency) DO NOTHING;

    SELECT registry.scale
      INTO registered_scale
      FROM trading.currency_scales AS registry
     WHERE registry.currency = NEW.settlement_currency;

    IF registered_scale IS DISTINCT FROM NEW.settlement_currency_scale THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'settlement currency code must use one scale';
    END IF;
    RETURN NEW;
END
$$;

ALTER FUNCTION trading.require_currency_scale_consistency()
OWNER TO CURRENT_USER;

CREATE FUNCTION trading.require_currency_scale_registry_authority()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    matching_pairs pg_catalog.int8;
    distinct_scales pg_catalog.int8;
BEGIN
    IF TG_RELID IS DISTINCT FROM
          'trading.currency_scales'::pg_catalog.regclass
       OR TG_NAME IS DISTINCT FROM
          'currency_scale_registry_requires_authority'
       OR TG_LEVEL IS DISTINCT FROM 'ROW'
       OR TG_WHEN IS DISTINCT FROM 'BEFORE'
       OR TG_OP IS DISTINCT FROM 'INSERT'
       OR TG_NARGS <> 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'currency scale registry authority function has invalid trigger origin';
    END IF;

    SELECT
        pg_catalog.count(*) FILTER (
            WHERE authority.scale = NEW.scale
        ),
        pg_catalog.count(DISTINCT authority.scale)
      INTO matching_pairs, distinct_scales
      FROM (
          SELECT instrument.settlement_currency_scale AS scale
            FROM trading.instruments AS instrument
           WHERE instrument.settlement_currency = NEW.currency
      ) AS authority;

    IF matching_pairs = 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'currency scale registry insert lacks exact durable instrument authority';
    END IF;
    IF distinct_scales <> 1 THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'settlement currency code must use one scale';
    END IF;
    RETURN NEW;
END
$$;

ALTER FUNCTION trading.require_currency_scale_registry_authority()
OWNER TO CURRENT_USER;

CREATE OR REPLACE FUNCTION trading.reject_currency_scale_registry_mutation()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_RELID IS DISTINCT FROM
          'trading.currency_scales'::pg_catalog.regclass
       OR TG_NAME IS DISTINCT FROM
          'currency_scale_registry_is_append_only'
       OR TG_LEVEL IS DISTINCT FROM 'STATEMENT'
       OR TG_WHEN IS DISTINCT FROM 'BEFORE'
       OR TG_OP NOT IN ('UPDATE', 'DELETE', 'TRUNCATE')
       OR TG_NARGS <> 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'currency scale mutation guard has invalid trigger origin';
    END IF;
    RAISE EXCEPTION USING
        ERRCODE = '55000',
        MESSAGE = 'currency scale registry is append-only';
END
$$;

ALTER FUNCTION trading.reject_currency_scale_registry_mutation()
OWNER TO CURRENT_USER;

DROP TRIGGER instruments_require_currency_scale_consistency
ON trading.instruments;

CREATE TRIGGER instruments_require_currency_scale_consistency
AFTER INSERT OR UPDATE ON trading.instruments
FOR EACH ROW EXECUTE FUNCTION
    trading.require_currency_scale_consistency();

ALTER TABLE trading.instruments
ENABLE ALWAYS TRIGGER instruments_require_currency_scale_consistency;

CREATE TRIGGER currency_scale_registry_requires_authority
BEFORE INSERT ON trading.currency_scales
FOR EACH ROW EXECUTE FUNCTION
    trading.require_currency_scale_registry_authority();

ALTER TABLE trading.currency_scales
ENABLE ALWAYS TRIGGER currency_scale_registry_requires_authority;

ALTER TABLE trading.currency_scales
ENABLE ALWAYS TRIGGER currency_scale_registry_is_append_only;

CREATE OR REPLACE FUNCTION engine.require_runtime_schema_revision()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF pg_catalog.current_setting(
           'platformgo.runtime_schema_revision',
           true
       ) IS DISTINCT FROM
           '20260730000200_phase3_currency_scale_authority_fence' THEN
        RAISE EXCEPTION
            'engine runtime schema revision is missing or incompatible'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END
$$;

ALTER FUNCTION engine.require_runtime_schema_revision()
OWNER TO CURRENT_USER;

-- Remove PUBLIC, named non-owner grants, grant options, and dependent chains
-- from every currency-registry trigger function and the replaced runtime
-- revision fence. Trigger execution needs no runtime EXECUTE grant.
DO $$
DECLARE
    function_oid pg_catalog.oid;
    unexpected_grantee pg_catalog.name;
BEGIN
    FOREACH function_oid IN ARRAY ARRAY[
        'trading.require_currency_scale_consistency()'::pg_catalog.regprocedure::pg_catalog.oid,
        'trading.require_currency_scale_registry_authority()'::pg_catalog.regprocedure::pg_catalog.oid,
        'trading.reject_currency_scale_registry_mutation()'::pg_catalog.regprocedure::pg_catalog.oid,
        'engine.require_runtime_schema_revision()'::pg_catalog.regprocedure::pg_catalog.oid
    ]
    LOOP
        EXECUTE pg_catalog.format(
            'REVOKE ALL PRIVILEGES ON FUNCTION %s FROM PUBLIC CASCADE',
            function_oid::pg_catalog.regprocedure
        );

        FOR unexpected_grantee IN
            SELECT DISTINCT role.rolname
              FROM pg_catalog.pg_proc AS procedure
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      procedure.proacl,
                      pg_catalog.acldefault('f', procedure.proowner)
                  )
              ) AS privilege
              JOIN pg_catalog.pg_roles AS role
                ON role.oid = privilege.grantee
             WHERE procedure.oid = function_oid
               AND role.oid <> procedure.proowner
             ORDER BY role.rolname
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL PRIVILEGES ON FUNCTION %s FROM %I CASCADE',
                function_oid::pg_catalog.regprocedure,
                unexpected_grantee
            );
        END LOOP;
    END LOOP;
END
$$;

-- Re-scrub both sources and the registry so the guard never elevates a row
-- admitted through a stale non-mutating grant into currency authority.
DO $$
DECLARE
    relation_oid pg_catalog.oid;
    relation_oids pg_catalog.oid[] := ARRAY[
        'trading.instruments'::pg_catalog.regclass::pg_catalog.oid,
        'trading.currency_scales'::pg_catalog.regclass::pg_catalog.oid,
        'engine.input_receipts'::pg_catalog.regclass::pg_catalog.oid
    ];
    unexpected_grantee pg_catalog.name;
    column_name pg_catalog.name;
BEGIN
    FOREACH relation_oid IN ARRAY relation_oids
    LOOP
        EXECUTE pg_catalog.format(
            'REVOKE ALL PRIVILEGES ON TABLE %s FROM PUBLIC CASCADE',
            relation_oid::pg_catalog.regclass
        );

        FOR unexpected_grantee IN
            SELECT DISTINCT role.rolname
              FROM pg_catalog.pg_class AS relation
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      relation.relacl,
                      pg_catalog.acldefault('r', relation.relowner)
                  )
              ) AS privilege
              JOIN pg_catalog.pg_roles AS role
                ON role.oid = privilege.grantee
             WHERE relation.oid = relation_oid
               AND role.oid <> relation.relowner
             ORDER BY role.rolname
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL PRIVILEGES ON TABLE %s FROM %I CASCADE',
                relation_oid::pg_catalog.regclass,
                unexpected_grantee
            );
        END LOOP;

        FOR column_name IN
            SELECT attribute.attname
              FROM pg_catalog.pg_attribute AS attribute
             WHERE attribute.attrelid = relation_oid
               AND attribute.attnum > 0
               AND NOT attribute.attisdropped
             ORDER BY attribute.attnum
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL PRIVILEGES (%I) ON TABLE %s FROM PUBLIC CASCADE',
                column_name,
                relation_oid::pg_catalog.regclass
            );
        END LOOP;

        FOR unexpected_grantee, column_name IN
            SELECT DISTINCT role.rolname, attribute.attname
              FROM pg_catalog.pg_attribute AS attribute
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  attribute.attacl
              ) AS privilege
              JOIN pg_catalog.pg_roles AS role
                ON role.oid = privilege.grantee
              JOIN pg_catalog.pg_class AS relation
                ON relation.oid = attribute.attrelid
             WHERE attribute.attrelid = relation_oid
               AND attribute.attnum > 0
               AND NOT attribute.attisdropped
               AND attribute.attacl IS NOT NULL
               AND role.oid <> relation.relowner
             ORDER BY role.rolname, attribute.attname
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL PRIVILEGES (%I) ON TABLE %s FROM %I CASCADE',
                column_name,
                relation_oid::pg_catalog.regclass,
                unexpected_grantee
            );
        END LOOP;
    END LOOP;
END
$$;

GRANT SELECT ON TABLE trading.instruments TO platformgo_api;
GRANT SELECT, INSERT, UPDATE ON TABLE trading.instruments
TO platformgo_engine;

GRANT SELECT ON TABLE trading.currency_scales TO platformgo_api;
GRANT SELECT ON TABLE trading.currency_scales TO platformgo_engine;

GRANT SELECT, INSERT ON TABLE engine.input_receipts TO platformgo_engine;
GRANT SELECT (
    shard_id,
    input_id
) ON TABLE engine.input_receipts TO platformgo_outbox;
