-- Repair funding-history authority inherited from hostile migrator-owner
-- defaults without editing the frozen funding read-model migration. The
-- The five-second owner/lock fence precedes every row read. Validation,
-- provenance backfill, guards, ACLs, and the journal commit atomically.
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

-- Engine persistence establishes instrument authority before writing the
-- settlement, projection, and receipt. Fence that exact order before trusting
-- any row or trigger.
LOCK TABLE engine.shard_ownership_epochs IN SHARE MODE;
LOCK TABLE trading.instruments IN SHARE MODE;
LOCK TABLE trading.funding_settlements IN SHARE MODE;
LOCK TABLE trading.funding_history_projection IN SHARE MODE;
LOCK TABLE engine.input_receipts IN SHARE MODE;

-- Rows are trustworthy only when every legacy guard still has its exact
-- relation, function, event/timing/level, enabled mode, and unqualified
-- definition. A hostile sidecar or disabled guard makes historical authority
-- unprovable even if current row values appear consistent.
DO $$
BEGIN
    IF pg_catalog.to_regprocedure(
           'trading.require_currency_scale_consistency()'
       ) IS NULL
       OR pg_catalog.to_regprocedure(
           'engine.reject_immutable_change()'
       ) IS NULL
       OR pg_catalog.to_regprocedure(
           'trading.require_funding_history_projection()'
       ) IS NULL
       OR pg_catalog.to_regprocedure(
           'engine.require_runtime_schema_revision()'
       ) IS NULL
       OR pg_catalog.to_regprocedure(
           'engine.require_balance_projection_hash_v3()'
       ) IS NULL
       OR pg_catalog.to_regprocedure(
           'engine.require_decision_hash_v4_runtime()'
       ) IS NULL
       OR pg_catalog.to_regprocedure(
           'engine.require_fill_effective_leverage_hash_v4()'
       ) IS NULL
       OR pg_catalog.to_regprocedure(
           'engine.require_authoritative_market_receipt()'
       ) IS NULL
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding authority is missing an expected pre-cutover trigger function';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM (
              VALUES
                  (
                      'engine.reject_immutable_change()'
                          ::pg_catalog.regprocedure,
                      false,
                      NULL::pg_catalog.text[],
                      $reject$
BEGIN
    RAISE EXCEPTION 'immutable relation %.% cannot be %',
        TG_TABLE_SCHEMA, TG_TABLE_NAME, lower(TG_OP)
        USING ERRCODE = '55000';
END;
$reject$::pg_catalog.text
                  ),
                  (
                      'engine.require_runtime_schema_revision()'
                          ::pg_catalog.regprocedure,
                      false,
                      ARRAY['search_path=pg_catalog']::pg_catalog.text[],
                      $revision$
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
$revision$::pg_catalog.text
                  ),
                  (
                      'trading.require_funding_history_projection()'
                          ::pg_catalog.regprocedure,
                      true,
                      ARRAY['search_path=pg_catalog']::pg_catalog.text[],
                      $history$
BEGIN
    IF NOT EXISTS (
        SELECT 1
         FROM trading.funding_history_projection AS history
          JOIN engine.account_shards AS account_shard
            ON account_shard.account_id = NEW.account_id
          JOIN engine.input_receipts AS receipt
            ON receipt.shard_id = account_shard.shard_id
           AND receipt.input_id = NEW.input_id
         WHERE history.funding_id = NEW.funding_id
           AND history.account_id = NEW.account_id
           AND history.instrument_id = NEW.instrument_id
           AND history.position_id = NEW.position_id
           AND history.logical_time =
               (receipt.envelope ->> 'LogicalTime')::bigint
    ) THEN
        RAISE EXCEPTION
            'funding settlement requires an exact durable history projection'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$history$::pg_catalog.text
                  ),
                  (
                      'engine.require_balance_projection_hash_v3()'
                          ::pg_catalog.regprocedure,
                      false,
                      NULL::pg_catalog.text[],
                      $balance_hash$
BEGIN
    IF NEW.decision_hash_version < 3 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'new receipts require balance-projection decision hash version 3';
    END IF;
    RETURN NEW;
END
$balance_hash$::pg_catalog.text
                  ),
                  (
                      'trading.require_currency_scale_consistency()'
                          ::pg_catalog.regprocedure,
                      true,
                      ARRAY['search_path=pg_catalog']::pg_catalog.text[],
                      $currency_scale$
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
$currency_scale$::pg_catalog.text
                  ),
                  (
                      'engine.require_decision_hash_v4_runtime()'
                          ::pg_catalog.regprocedure,
                      false,
                      ARRAY['search_path=pg_catalog']::pg_catalog.text[],
                      $decision_hash$
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
$decision_hash$::pg_catalog.text
                  ),
                  (
                      'engine.require_fill_effective_leverage_hash_v4()'
                          ::pg_catalog.regprocedure,
                      false,
                      NULL::pg_catalog.text[],
                      $leverage_hash$
BEGIN
    IF NEW.decision_hash_version < 4 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'new receipts require fill effective-leverage decision hash version 4';
    END IF;
    RETURN NEW;
END
$leverage_hash$::pg_catalog.text
                  ),
                  (
                      'engine.require_authoritative_market_receipt()'
                          ::pg_catalog.regprocedure,
                      false,
                      NULL::pg_catalog.text[],
                      $market_receipt$
BEGIN
    IF (NEW.envelope ->> 'Kind')::integer = 2
       AND (
           CASE
               WHEN jsonb_typeof(NEW.decision -> 'BookChanges') = 'array'
               THEN jsonb_array_length(NEW.decision -> 'BookChanges') = 0
               ELSE true
           END
           OR COALESCE(
               (NEW.envelope ->> 'MarketSequence')::bigint,
               0
           ) <> COALESCE(
               (NEW.decision ->> 'MarketSequence')::bigint,
               0
           )
           OR (NEW.envelope ->> 'StreamSequence')::bigint
               IS DISTINCT FROM NEW.stream_sequence
           OR (NEW.decision ->> 'StreamSequence')::bigint
               IS DISTINCT FROM NEW.stream_sequence
           OR (NEW.envelope ->> 'MarketSequence')::bigint
               IS DISTINCT FROM NEW.stream_sequence
       )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE =
                'market receipt must commit authoritative market state';
    END IF;
    RETURN NEW;
END;
$market_receipt$::pg_catalog.text
                  )
          ) AS expected(
              signature,
              security_definer,
              configuration,
              body
          )
          JOIN pg_catalog.pg_proc AS procedure
            ON procedure.oid = expected.signature
          JOIN pg_catalog.pg_language AS language
            ON language.oid = procedure.prolang
         WHERE procedure.proowner <> (
                   SELECT role.oid
                     FROM pg_catalog.pg_roles AS role
                    WHERE role.rolname = CURRENT_USER
               )
            OR language.lanname <> 'plpgsql'
            OR procedure.prorettype <>
               'trigger'::pg_catalog.regtype
            OR procedure.prokind <> 'f'
            OR procedure.pronargs <> 0
            OR procedure.proretset
            OR procedure.proisstrict
            OR procedure.proleakproof
            OR procedure.proparallel <> 'u'
            OR procedure.provolatile <> 'v'
            OR procedure.prosecdef IS DISTINCT FROM
               expected.security_definer
            OR procedure.proconfig IS DISTINCT FROM
               expected.configuration
            OR pg_catalog.btrim(procedure.prosrc) IS DISTINCT FROM
               pg_catalog.btrim(expected.body)
    )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding authority has an untrusted pre-cutover trigger function';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_trigger AS trigger_record
         WHERE trigger_record.tgrelid IN (
                   'trading.instruments'::pg_catalog.regclass,
                   'trading.funding_settlements'::pg_catalog.regclass,
                   'trading.funding_history_projection'::pg_catalog.regclass,
                   'engine.shard_ownership_epochs'::pg_catalog.regclass,
                   'engine.input_receipts'::pg_catalog.regclass
               )
           AND NOT trigger_record.tgisinternal
           AND NOT (
               trigger_record.tgqual IS NULL
               AND trigger_record.tgattr::pg_catalog.text = ''
               AND trigger_record.tgoldtable IS NULL
               AND trigger_record.tgnewtable IS NULL
               AND pg_catalog.octet_length(trigger_record.tgargs) = 0
               AND trigger_record.tgnargs = 0
               AND CASE
                   WHEN trigger_record.tgrelid =
                            'trading.funding_settlements'
                                ::pg_catalog.regclass
                        AND trigger_record.tgname =
                            'funding_settlement_requires_history_projection'
                   THEN trigger_record.tgdeferrable
                        AND trigger_record.tginitdeferred
                        AND trigger_record.tgconstraint <> 0
                        AND EXISTS (
                            SELECT 1
                              FROM pg_catalog.pg_constraint
                                   AS constraint_record
                             WHERE constraint_record.oid =
                                       trigger_record.tgconstraint
                               AND constraint_record.conname =
                                   'funding_settlement_requires_history_projection'
                               AND constraint_record.conrelid =
                                   'trading.funding_settlements'
                                       ::pg_catalog.regclass
                               AND constraint_record.contype = 't'
                               AND constraint_record.condeferrable
                               AND constraint_record.condeferred
                        )
                   ELSE NOT trigger_record.tgdeferrable
                        AND NOT trigger_record.tginitdeferred
                        AND trigger_record.tgconstraint = 0
               END
               AND (
                   (
                       trigger_record.tgrelid =
                           'trading.instruments'::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'instruments_require_currency_scale_consistency'
                       AND trigger_record.tgfoid =
                           'trading.require_currency_scale_consistency()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 21
                       AND trigger_record.tgenabled = 'A'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'engine.shard_ownership_epochs'
                               ::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'shard_ownership_epochs_require_runtime_schema_revision'
                       AND trigger_record.tgfoid =
                           'engine.require_runtime_schema_revision()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 23
                       AND trigger_record.tgenabled = 'O'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'trading.funding_settlements'::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'funding_settlements_are_immutable'
                       AND trigger_record.tgfoid =
                           'engine.reject_immutable_change()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 27
                       AND trigger_record.tgenabled = 'O'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'trading.funding_settlements'::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'funding_settlement_requires_history_projection'
                       AND trigger_record.tgfoid =
                           'trading.require_funding_history_projection()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 5
                       AND trigger_record.tgenabled = 'O'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'trading.funding_history_projection'
                               ::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'funding_history_projection_is_immutable'
                       AND trigger_record.tgfoid =
                           'engine.reject_immutable_change()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 27
                       AND trigger_record.tgenabled = 'O'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'engine.input_receipts'::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'input_receipts_are_immutable'
                       AND trigger_record.tgfoid =
                           'engine.reject_immutable_change()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 27
                       AND trigger_record.tgenabled = 'O'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'engine.input_receipts'::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'input_receipts_require_runtime_schema_revision'
                       AND trigger_record.tgfoid =
                           'engine.require_runtime_schema_revision()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 7
                       AND trigger_record.tgenabled = 'O'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'engine.input_receipts'::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'input_receipts_require_balance_projection_hash_v3'
                       AND trigger_record.tgfoid =
                           'engine.require_balance_projection_hash_v3()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 7
                       AND trigger_record.tgenabled = 'O'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'engine.input_receipts'::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'input_receipts_require_decision_hash_v4_runtime'
                       AND trigger_record.tgfoid =
                           'engine.require_decision_hash_v4_runtime()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 7
                       AND trigger_record.tgenabled = 'O'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'engine.input_receipts'::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'input_receipts_require_fill_effective_leverage_hash_v4'
                       AND trigger_record.tgfoid =
                           'engine.require_fill_effective_leverage_hash_v4()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 7
                       AND trigger_record.tgenabled = 'O'
                   )
                   OR (
                       trigger_record.tgrelid =
                           'engine.input_receipts'::pg_catalog.regclass
                       AND trigger_record.tgname =
                           'input_receipts_require_authoritative_market_state'
                       AND trigger_record.tgfoid =
                           'engine.require_authoritative_market_receipt()'
                               ::pg_catalog.regprocedure
                       AND trigger_record.tgtype = 7
                       AND trigger_record.tgenabled = 'O'
                   )
               )
           )
    ) OR (
        SELECT pg_catalog.count(*)
          FROM pg_catalog.pg_trigger AS trigger_record
         WHERE trigger_record.tgrelid IN (
                   'trading.instruments'::pg_catalog.regclass,
                   'trading.funding_settlements'::pg_catalog.regclass,
                   'trading.funding_history_projection'::pg_catalog.regclass,
                   'engine.shard_ownership_epochs'::pg_catalog.regclass,
                   'engine.input_receipts'::pg_catalog.regclass
               )
           AND NOT trigger_record.tgisinternal
    ) <> 11
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding authority relation has an unexpected pre-cutover trigger';
    END IF;
END
$$;

-- Funding history is an economic projection, not an independently repairable
-- read cache. Validate the complete canonical receipt source before any
-- catalog change or backfill cast.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts AS receipt
         WHERE pg_catalog.jsonb_typeof(receipt.decision)
               IS DISTINCT FROM 'object'
            OR (
                receipt.decision ? 'FundingChanges'
                AND pg_catalog.jsonb_typeof(
                    receipt.decision -> 'FundingChanges'
                ) NOT IN ('array', 'null')
            )
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding receipt history is not a canonical decision array';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts AS receipt
          CROSS JOIN LATERAL pg_catalog.jsonb_array_elements(
              CASE
                  WHEN pg_catalog.jsonb_typeof(
                      receipt.decision -> 'FundingChanges'
                  ) = 'array'
                  THEN receipt.decision -> 'FundingChanges'
                  ELSE '[]'::pg_catalog.jsonb
              END
          ) AS change
         WHERE pg_catalog.jsonb_typeof(change) IS DISTINCT FROM 'object'
            OR change
               - 'FundingID'
               - 'SettlementID'
               - 'PositionID'
               - 'AccountID'
               - 'InstrumentID'
               - 'SignedQuantity'
               - 'OraclePrice'
               - 'Rate'
               - 'Amount'
               - 'SettlementCurrency'
               <> '{}'::pg_catalog.jsonb
            OR NOT change ?& ARRAY[
                'FundingID',
                'SettlementID',
                'PositionID',
                'AccountID',
                'InstrumentID',
                'SignedQuantity',
                'OraclePrice',
                'Rate',
                'Amount',
                'SettlementCurrency'
            ]
            OR pg_catalog.jsonb_typeof(change -> 'FundingID')
               IS DISTINCT FROM 'array'
            OR CASE
                   WHEN pg_catalog.jsonb_typeof(change -> 'FundingID')
                        = 'array'
                   THEN pg_catalog.jsonb_array_length(
                       change -> 'FundingID'
                   ) = 16
                   AND NOT EXISTS (
                       SELECT 1
                         FROM pg_catalog.jsonb_array_elements(
                             change -> 'FundingID'
                         ) AS octet
                        WHERE pg_catalog.jsonb_typeof(octet) <> 'number'
                           OR octet::pg_catalog.text !~ '^(0|[1-9][0-9]{0,2})$'
                           OR octet::pg_catalog.text::pg_catalog.int2
                              NOT BETWEEN 0 AND 255
                   )
                   ELSE false
               END IS NOT TRUE
            OR pg_catalog.jsonb_typeof(change -> 'SettlementID')
               IS DISTINCT FROM 'array'
            OR CASE
                   WHEN pg_catalog.jsonb_typeof(change -> 'SettlementID')
                        = 'array'
                   THEN pg_catalog.jsonb_array_length(
                       change -> 'SettlementID'
                   ) = 16
                   AND NOT EXISTS (
                       SELECT 1
                         FROM pg_catalog.jsonb_array_elements(
                             change -> 'SettlementID'
                         ) AS octet
                        WHERE pg_catalog.jsonb_typeof(octet) <> 'number'
                           OR octet::pg_catalog.text !~ '^(0|[1-9][0-9]{0,2})$'
                           OR octet::pg_catalog.text::pg_catalog.int2
                              NOT BETWEEN 0 AND 255
                   )
                   ELSE false
               END IS NOT TRUE
            OR pg_catalog.jsonb_typeof(change -> 'PositionID')
               IS DISTINCT FROM 'array'
            OR CASE
                   WHEN pg_catalog.jsonb_typeof(change -> 'PositionID')
                        = 'array'
                   THEN pg_catalog.jsonb_array_length(
                       change -> 'PositionID'
                   ) = 16
                   AND NOT EXISTS (
                       SELECT 1
                         FROM pg_catalog.jsonb_array_elements(
                             change -> 'PositionID'
                         ) AS octet
                        WHERE pg_catalog.jsonb_typeof(octet) <> 'number'
                           OR octet::pg_catalog.text !~ '^(0|[1-9][0-9]{0,2})$'
                           OR octet::pg_catalog.text::pg_catalog.int2
                              NOT BETWEEN 0 AND 255
                   )
                   ELSE false
               END IS NOT TRUE
            OR pg_catalog.jsonb_typeof(change -> 'AccountID')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'AccountID', '') = ''
            OR pg_catalog.octet_length(change ->> 'AccountID') > 255
            OR pg_catalog.jsonb_typeof(change -> 'InstrumentID')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'InstrumentID', '') = ''
            OR pg_catalog.octet_length(change ->> 'InstrumentID') > 255
            OR pg_catalog.jsonb_typeof(change -> 'SettlementCurrency')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'SettlementCurrency', '')
               !~ '^[A-Z0-9]{3,12}$'
            OR pg_catalog.jsonb_typeof(change -> 'SignedQuantity')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'SignedQuantity', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            OR change ->> 'SignedQuantity' = '-0'
            OR pg_catalog.length(
                pg_catalog.split_part(
                    pg_catalog.ltrim(
                        change ->> 'SignedQuantity',
                        '-'
                    ),
                    '.',
                    1
                )
            ) > 20
            OR pg_catalog.length(
                pg_catalog.split_part(
                    change ->> 'SignedQuantity',
                    '.',
                    2
                )
            ) > 18
            OR pg_catalog.length(
                pg_catalog.replace(
                    pg_catalog.ltrim(
                        change ->> 'SignedQuantity',
                        '-'
                    ),
                    '.',
                    ''
                )
            ) > 38
            OR pg_catalog.jsonb_typeof(change -> 'OraclePrice')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'OraclePrice', '')
               !~ '^(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            OR change ->> 'OraclePrice' = '0'
            OR pg_catalog.length(
                pg_catalog.split_part(
                    change ->> 'OraclePrice',
                    '.',
                    1
                )
            ) > 20
            OR pg_catalog.length(
                pg_catalog.split_part(
                    change ->> 'OraclePrice',
                    '.',
                    2
                )
            ) > 18
            OR pg_catalog.length(
                pg_catalog.replace(
                    change ->> 'OraclePrice',
                    '.',
                    ''
                )
            ) > 38
            OR pg_catalog.jsonb_typeof(change -> 'Rate')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'Rate', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            OR change ->> 'Rate' = '-0'
            OR pg_catalog.length(
                pg_catalog.split_part(
                    pg_catalog.ltrim(change ->> 'Rate', '-'),
                    '.',
                    1
                )
            ) > 20
            OR pg_catalog.length(
                pg_catalog.split_part(change ->> 'Rate', '.', 2)
            ) > 18
            OR pg_catalog.length(
                pg_catalog.replace(
                    pg_catalog.ltrim(change ->> 'Rate', '-'),
                    '.',
                    ''
                )
            ) > 38
            OR pg_catalog.jsonb_typeof(change -> 'Amount')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'Amount', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            OR change ->> 'Amount' = '-0'
            OR pg_catalog.length(
                pg_catalog.split_part(
                    pg_catalog.ltrim(change ->> 'Amount', '-'),
                    '.',
                    1
                )
            ) > 20
            OR pg_catalog.length(
                pg_catalog.split_part(change ->> 'Amount', '.', 2)
            ) > 18
            OR pg_catalog.length(
                pg_catalog.replace(
                    pg_catalog.ltrim(change ->> 'Amount', '-'),
                    '.',
                    ''
                )
            ) > 38
            OR pg_catalog.jsonb_typeof(
                receipt.envelope -> 'LogicalTime'
            ) IS DISTINCT FROM 'number'
            OR CASE
                   WHEN COALESCE(
                       receipt.envelope ->> 'LogicalTime',
                       ''
                   ) ~ '^-?(0|[1-9][0-9]*)$'
                   THEN (
                       receipt.envelope ->> 'LogicalTime'
                   )::pg_catalog.numeric BETWEEN
                       -9223372036854775808 AND 9223372036854775807
                   ELSE false
               END IS NOT TRUE
            OR pg_catalog.jsonb_typeof(
                receipt.decision -> 'CommandResult'
            ) IS DISTINCT FROM 'object'
            OR receipt.decision #>>
               '{CommandResult,Status}' IS DISTINCT FROM 'accepted'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding receipt history contains a malformed or non-accepted effect';
    END IF;
END
$$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM engine.input_receipts AS receipt
         WHERE receipt.decision ? 'InstrumentChanges'
           AND pg_catalog.jsonb_typeof(
               receipt.decision -> 'InstrumentChanges'
           ) NOT IN ('array', 'null')
    ) OR EXISTS (
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
         WHERE pg_catalog.jsonb_typeof(change) IS DISTINCT FROM 'object'
            OR change
               - 'InstrumentID'
               - 'Revision'
               - 'PriceScale'
               - 'QuantityScale'
               - 'SettlementCurrency'
               - 'SettlementCurrencyScale'
               - 'InitialMarginRate'
               - 'MaintenanceMarginRate'
               - 'MaxLeverage'
               - 'MakerFeeRate'
               - 'TakerFeeRate'
               <> '{}'::pg_catalog.jsonb
            OR NOT change ?& ARRAY[
                'InstrumentID',
                'Revision',
                'PriceScale',
                'QuantityScale',
                'SettlementCurrency',
                'SettlementCurrencyScale',
                'InitialMarginRate',
                'MaintenanceMarginRate',
                'MaxLeverage',
                'MakerFeeRate',
                'TakerFeeRate'
            ]
            OR pg_catalog.jsonb_typeof(change -> 'InstrumentID')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'InstrumentID', '') = ''
            OR pg_catalog.octet_length(change ->> 'InstrumentID') > 255
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
               !~ '^(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            OR pg_catalog.jsonb_typeof(change -> 'MaintenanceMarginRate')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'MaintenanceMarginRate', '')
               !~ '^(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            OR pg_catalog.jsonb_typeof(change -> 'MaxLeverage')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'MaxLeverage', '')
               !~ '^(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            OR change ->> 'MaxLeverage' = '0'
            OR pg_catalog.jsonb_typeof(change -> 'MakerFeeRate')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'MakerFeeRate', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            OR change ->> 'MakerFeeRate' = '-0'
            OR pg_catalog.jsonb_typeof(change -> 'TakerFeeRate')
               IS DISTINCT FROM 'string'
            OR COALESCE(change ->> 'TakerFeeRate', '')
               !~ '^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            OR change ->> 'TakerFeeRate' = '-0'
            OR pg_catalog.jsonb_typeof(
                receipt.decision -> 'CommandResult'
            ) IS DISTINCT FROM 'object'
            OR receipt.decision #>>
               '{CommandResult,Status}' IS DISTINCT FROM 'accepted'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'instrument change history is not a canonical accepted decision array';
    END IF;
END
$$;

CREATE TEMPORARY TABLE expected_broker_funding
ON COMMIT DROP
AS
SELECT
    receipt.shard_id,
    receipt.stream_sequence,
    receipt.input_id,
    (receipt.envelope ->> 'LogicalTime')::pg_catalog.int8 AS logical_time,
    (
        SELECT pg_catalog.string_agg(
            pg_catalog.lpad(
                pg_catalog.to_hex(octet.value::pg_catalog.int4),
                2,
                '0'
            ),
            '' ORDER BY octet.ordinality
        )::pg_catalog.uuid
          FROM pg_catalog.jsonb_array_elements_text(
              change -> 'FundingID'
          ) WITH ORDINALITY AS octet(value, ordinality)
    ) AS funding_id,
    (
        SELECT pg_catalog.string_agg(
            pg_catalog.lpad(
                pg_catalog.to_hex(octet.value::pg_catalog.int4),
                2,
                '0'
            ),
            '' ORDER BY octet.ordinality
        )::pg_catalog.uuid
          FROM pg_catalog.jsonb_array_elements_text(
              change -> 'SettlementID'
          ) WITH ORDINALITY AS octet(value, ordinality)
    ) AS settlement_id,
    (
        SELECT pg_catalog.string_agg(
            pg_catalog.lpad(
                pg_catalog.to_hex(octet.value::pg_catalog.int4),
                2,
                '0'
            ),
            '' ORDER BY octet.ordinality
        )::pg_catalog.uuid
          FROM pg_catalog.jsonb_array_elements_text(
              change -> 'PositionID'
          ) WITH ORDINALITY AS octet(value, ordinality)
    ) AS position_id,
    change ->> 'AccountID' AS account_id,
    change ->> 'InstrumentID' AS instrument_id,
    (change ->> 'SignedQuantity')::pg_catalog.numeric AS signed_quantity,
    (change ->> 'OraclePrice')::pg_catalog.numeric AS oracle_price,
    (change ->> 'Rate')::pg_catalog.numeric AS rate,
    (change ->> 'Amount')::pg_catalog.numeric AS amount,
    change ->> 'SettlementCurrency' AS settlement_currency
  FROM engine.input_receipts AS receipt
  CROSS JOIN LATERAL pg_catalog.jsonb_array_elements(
      CASE
          WHEN pg_catalog.jsonb_typeof(
              receipt.decision -> 'FundingChanges'
          ) = 'array'
          THEN receipt.decision -> 'FundingChanges'
          ELSE '[]'::pg_catalog.jsonb
      END
  ) AS change
 WHERE receipt.decision #>> '{CommandResult,Status}' = 'accepted';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM trading.funding_settlements AS funding
          LEFT JOIN trading.funding_history_projection AS history
            ON history.funding_id = funding.funding_id
         WHERE history.funding_id IS NULL
    ) OR EXISTS (
        SELECT 1
          FROM trading.funding_history_projection AS history
          LEFT JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
         WHERE funding.funding_id IS NULL
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding authority contains an orphan settlement or projection';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM pg_temp.expected_broker_funding
         OFFSET 100000
         LIMIT 1
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding provenance backfill exceeds the 100000-row halted-migration bound';
    END IF;

    IF EXISTS (
        (
            SELECT
                expected.funding_id,
                expected.settlement_id,
                expected.position_id,
                expected.input_id,
                expected.account_id,
                expected.instrument_id,
                expected.signed_quantity,
                expected.oracle_price,
                expected.rate,
                expected.amount,
                expected.settlement_currency,
                expected.logical_time
              FROM pg_temp.expected_broker_funding AS expected
            EXCEPT ALL
            SELECT
                funding.funding_id,
                funding.settlement_id,
                funding.position_id,
                funding.input_id,
                funding.account_id,
                funding.instrument_id,
                funding.signed_quantity,
                funding.oracle_price,
                funding.rate,
                funding.amount,
                funding.settlement_currency,
                history.logical_time
              FROM trading.funding_settlements AS funding
              JOIN trading.funding_history_projection AS history
                ON history.funding_id = funding.funding_id
        )
        UNION ALL
        (
            SELECT
                funding.funding_id,
                funding.settlement_id,
                funding.position_id,
                funding.input_id,
                funding.account_id,
                funding.instrument_id,
                funding.signed_quantity,
                funding.oracle_price,
                funding.rate,
                funding.amount,
                funding.settlement_currency,
                history.logical_time
              FROM trading.funding_settlements AS funding
              JOIN trading.funding_history_projection AS history
                ON history.funding_id = funding.funding_id
            EXCEPT ALL
            SELECT
                expected.funding_id,
                expected.settlement_id,
                expected.position_id,
                expected.input_id,
                expected.account_id,
                expected.instrument_id,
                expected.signed_quantity,
                expected.oracle_price,
                expected.rate,
                expected.amount,
                expected.settlement_currency,
                expected.logical_time
              FROM pg_temp.expected_broker_funding AS expected
        )
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding settlements and projections diverge from immutable receipt authority';
    END IF;
END
$$;

CREATE TEMPORARY TABLE expected_funding_instrument_provenance
ON COMMIT DROP
AS
SELECT
    expected.funding_id,
    expected.instrument_id,
    (instrument.change ->> 'Revision')::pg_catalog.int8 AS revision,
    (instrument.change ->> 'PriceScale')::pg_catalog.int2 AS price_scale,
    (instrument.change ->> 'QuantityScale')::pg_catalog.int2 AS quantity_scale
  FROM pg_temp.expected_broker_funding AS expected
  JOIN LATERAL (
      SELECT expanded.change
        FROM engine.input_receipts AS instrument_receipt
        CROSS JOIN LATERAL pg_catalog.jsonb_array_elements(
            CASE
                WHEN pg_catalog.jsonb_typeof(
                    instrument_receipt.decision -> 'InstrumentChanges'
                ) = 'array'
                THEN instrument_receipt.decision -> 'InstrumentChanges'
                ELSE '[]'::pg_catalog.jsonb
            END
        ) WITH ORDINALITY AS expanded(change, ordinality)
       WHERE instrument_receipt.shard_id = expected.shard_id
         AND instrument_receipt.stream_sequence <= expected.stream_sequence
         AND instrument_receipt.decision #>>
             '{CommandResult,Status}' = 'accepted'
         AND expanded.change ->> 'InstrumentID' = expected.instrument_id
       ORDER BY
           instrument_receipt.stream_sequence DESC,
           expanded.ordinality DESC
       LIMIT 1
  ) AS instrument ON true;

DO $$
BEGIN
    IF (
        SELECT pg_catalog.count(*)
          FROM pg_temp.expected_funding_instrument_provenance
    ) IS DISTINCT FROM (
        SELECT pg_catalog.count(*)
          FROM pg_temp.expected_broker_funding
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding instrument provenance cannot be proven from immutable receipts';
    END IF;
END
$$;

CREATE TABLE trading.funding_instrument_provenance (
    funding_id uuid PRIMARY KEY
        REFERENCES trading.funding_settlements(funding_id),
    instrument_id text NOT NULL
        REFERENCES trading.instruments(instrument_id),
    revision bigint NOT NULL CHECK (revision > 0),
    price_scale smallint NOT NULL CHECK (price_scale BETWEEN 0 AND 18),
    quantity_scale smallint NOT NULL CHECK (quantity_scale BETWEEN 0 AND 18)
);

INSERT INTO trading.funding_instrument_provenance (
    funding_id,
    instrument_id,
    revision,
    price_scale,
    quantity_scale
)
SELECT
    funding_id,
    instrument_id,
    revision,
    price_scale,
    quantity_scale
  FROM pg_temp.expected_funding_instrument_provenance
 ORDER BY funding_id;

CREATE TRIGGER funding_instrument_provenance_is_immutable
BEFORE UPDATE OR DELETE ON trading.funding_instrument_provenance
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER funding_settlements_reject_truncate
BEFORE TRUNCATE ON trading.funding_settlements
FOR EACH STATEMENT EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER funding_history_projection_reject_truncate
BEFORE TRUNCATE ON trading.funding_history_projection
FOR EACH STATEMENT EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER funding_instrument_provenance_reject_truncate
BEFORE TRUNCATE ON trading.funding_instrument_provenance
FOR EACH STATEMENT EXECUTE FUNCTION engine.reject_immutable_change();

CREATE FUNCTION trading.require_funding_instrument_provenance()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM trading.funding_instrument_provenance AS provenance
          JOIN trading.instruments AS instrument
            ON instrument.instrument_id = provenance.instrument_id
         WHERE provenance.funding_id = NEW.funding_id
           AND provenance.instrument_id = NEW.instrument_id
           AND provenance.revision = instrument.revision
           AND provenance.price_scale = instrument.price_scale
           AND provenance.quantity_scale = instrument.quantity_scale
    ) THEN
        RAISE EXCEPTION
            'funding settlement requires exact immutable instrument provenance'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER funding_settlement_requires_instrument_provenance
AFTER INSERT ON trading.funding_settlements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION trading.require_funding_instrument_provenance();

-- Preserve both settlement completeness checks as genuine deferred constraint
-- triggers. Matching names, functions, and event bits are insufficient: an
-- ordinary immediate trigger can expose an unsafe partial writer order.
DO $$
BEGIN
    IF (
        SELECT pg_catalog.count(*)
          FROM pg_catalog.pg_trigger AS trigger_record
         WHERE trigger_record.tgrelid =
                   'trading.funding_settlements'::pg_catalog.regclass
           AND NOT trigger_record.tgisinternal
           AND (
               trigger_record.tgconstraint <> 0
               OR trigger_record.tgdeferrable
               OR trigger_record.tginitdeferred
           )
    ) <> 2
    OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_trigger AS trigger_record
         WHERE trigger_record.tgrelid =
                   'trading.funding_settlements'::pg_catalog.regclass
           AND NOT trigger_record.tgisinternal
           AND (
               trigger_record.tgconstraint <> 0
               OR trigger_record.tgdeferrable
               OR trigger_record.tginitdeferred
           )
           AND NOT (
               trigger_record.tgtype = 5
               AND trigger_record.tgenabled = 'O'
               AND trigger_record.tgdeferrable
               AND trigger_record.tginitdeferred
               AND trigger_record.tgconstraint <> 0
               AND trigger_record.tgqual IS NULL
               AND trigger_record.tgattr::pg_catalog.text = ''
               AND trigger_record.tgoldtable IS NULL
               AND trigger_record.tgnewtable IS NULL
               AND pg_catalog.octet_length(trigger_record.tgargs) = 0
               AND trigger_record.tgnargs = 0
               AND EXISTS (
                   SELECT 1
                     FROM pg_catalog.pg_constraint AS constraint_record
                    WHERE constraint_record.oid =
                              trigger_record.tgconstraint
                      AND constraint_record.conname =
                              trigger_record.tgname
                      AND constraint_record.conrelid =
                          'trading.funding_settlements'
                              ::pg_catalog.regclass
                      AND constraint_record.contype = 't'
                      AND constraint_record.condeferrable
                      AND constraint_record.condeferred
               )
               AND (
                   (
                       trigger_record.tgname =
                           'funding_settlement_requires_history_projection'
                       AND trigger_record.tgfoid =
                           'trading.require_funding_history_projection()'
                               ::pg_catalog.regprocedure
                   )
                   OR (
                       trigger_record.tgname =
                           'funding_settlement_requires_instrument_provenance'
                       AND trigger_record.tgfoid =
                           'trading.require_funding_instrument_provenance()'
                               ::pg_catalog.regprocedure
                   )
               )
           )
    )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'funding settlement constraint trigger catalog is not exact';
    END IF;
END
$$;

CREATE FUNCTION trading.read_broker_account_funding_history(
    requested_account_id text,
    requested_cursor_time bigint,
    requested_cursor_id uuid,
    requested_cursor_present boolean,
    requested_limit integer,
    requested_forward boolean
)
RETURNS TABLE (
    funding_id uuid,
    instrument_id text,
    instrument_revision bigint,
    price_scale smallint,
    quantity_scale smallint,
    position_id uuid,
    signed_quantity numeric,
    oracle_price numeric,
    funding_rate numeric,
    funding_amount numeric,
    settlement_currency text,
    funding_logical_time bigint
)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF requested_forward AND NOT requested_cursor_present THEN
        RETURN QUERY
        SELECT
            funding.funding_id,
            funding.instrument_id,
            provenance.revision,
            provenance.price_scale,
            provenance.quantity_scale,
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
          LEFT JOIN trading.funding_instrument_provenance AS provenance
            ON provenance.funding_id = funding.funding_id
           AND provenance.instrument_id = funding.instrument_id
         WHERE history.account_id = requested_account_id
         ORDER BY history.logical_time DESC, history.funding_id DESC
         LIMIT requested_limit;
    ELSIF requested_forward THEN
        RETURN QUERY
        SELECT
            funding.funding_id,
            funding.instrument_id,
            provenance.revision,
            provenance.price_scale,
            provenance.quantity_scale,
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
          LEFT JOIN trading.funding_instrument_provenance AS provenance
            ON provenance.funding_id = funding.funding_id
           AND provenance.instrument_id = funding.instrument_id
         WHERE history.account_id = requested_account_id
           AND (
               history.logical_time,
               history.funding_id
           ) < (
               requested_cursor_time,
               requested_cursor_id
           )
         ORDER BY history.logical_time DESC, history.funding_id DESC
         LIMIT requested_limit;
    ELSIF NOT requested_cursor_present THEN
        RETURN QUERY
        SELECT
            funding.funding_id,
            funding.instrument_id,
            provenance.revision,
            provenance.price_scale,
            provenance.quantity_scale,
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
          LEFT JOIN trading.funding_instrument_provenance AS provenance
            ON provenance.funding_id = funding.funding_id
           AND provenance.instrument_id = funding.instrument_id
         WHERE history.account_id = requested_account_id
         ORDER BY history.logical_time ASC, history.funding_id ASC
         LIMIT requested_limit;
    ELSE
        RETURN QUERY
        SELECT
            funding.funding_id,
            funding.instrument_id,
            provenance.revision,
            provenance.price_scale,
            provenance.quantity_scale,
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
          LEFT JOIN trading.funding_instrument_provenance AS provenance
            ON provenance.funding_id = funding.funding_id
           AND provenance.instrument_id = funding.instrument_id
         WHERE history.account_id = requested_account_id
           AND (
               history.logical_time,
               history.funding_id
           ) > (
               requested_cursor_time,
               requested_cursor_id
           )
         ORDER BY history.logical_time ASC, history.funding_id ASC
         LIMIT requested_limit;
    END IF;
END;
$$;

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
           '20260730000400_phase3_broker_funding_acl' THEN
        RAISE EXCEPTION
            'engine runtime schema revision is missing or incompatible'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END
$$;

ALTER FUNCTION engine.require_runtime_schema_revision()
OWNER TO CURRENT_USER;

DO $$
DECLARE
    relation_oid pg_catalog.oid;
    relation_oids pg_catalog.oid[] := ARRAY[
        'trading.funding_settlements'::pg_catalog.regclass::pg_catalog.oid,
        'trading.funding_history_projection'::pg_catalog.regclass::pg_catalog.oid,
        'trading.funding_instrument_provenance'
            ::pg_catalog.regclass::pg_catalog.oid
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

DO $$
DECLARE
    function_oid pg_catalog.oid;
    function_oids pg_catalog.oid[] := ARRAY[
        'trading.require_funding_history_projection()'
            ::pg_catalog.regprocedure::pg_catalog.oid,
        'trading.require_funding_instrument_provenance()'
            ::pg_catalog.regprocedure::pg_catalog.oid,
        'trading.read_account_funding_history(text,bigint,uuid,boolean,integer,boolean)'
            ::pg_catalog.regprocedure::pg_catalog.oid,
        'trading.read_broker_account_funding_history(text,bigint,uuid,boolean,integer,boolean)'
            ::pg_catalog.regprocedure::pg_catalog.oid,
        'trading.read_symbol_funding_history(text,bigint,uuid,boolean,integer,boolean)'
            ::pg_catalog.regprocedure::pg_catalog.oid,
        'trading.account_position_funding_total(text,uuid,bigint)'
            ::pg_catalog.regprocedure::pg_catalog.oid,
        'trading.account_funding_history_count(text)'
            ::pg_catalog.regprocedure::pg_catalog.oid,
        'trading.symbol_funding_history_count(text)'
            ::pg_catalog.regprocedure::pg_catalog.oid,
        'engine.require_runtime_schema_revision()'
            ::pg_catalog.regprocedure::pg_catalog.oid
    ];
    unexpected_grantee pg_catalog.name;
BEGIN
    FOREACH function_oid IN ARRAY function_oids
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

GRANT SELECT, INSERT ON TABLE trading.funding_settlements
TO platformgo_engine;
GRANT SELECT, INSERT ON TABLE trading.funding_history_projection
TO platformgo_engine;
GRANT SELECT, INSERT ON TABLE trading.funding_instrument_provenance
TO platformgo_engine;

GRANT EXECUTE ON FUNCTION trading.read_account_funding_history(
    text,
    bigint,
    uuid,
    boolean,
    integer,
    boolean
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION trading.read_symbol_funding_history(
    text,
    bigint,
    uuid,
    boolean,
    integer,
    boolean
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION trading.read_broker_account_funding_history(
    text,
    bigint,
    uuid,
    boolean,
    integer,
    boolean
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION trading.account_position_funding_total(
    text,
    uuid,
    bigint
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION trading.account_funding_history_count(text)
TO platformgo_api;
GRANT EXECUTE ON FUNCTION trading.symbol_funding_history_count(text)
TO platformgo_api;
