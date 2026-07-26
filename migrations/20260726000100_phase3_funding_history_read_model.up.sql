-- Phase 3 exact funding-history compatibility read model.
--
-- Lock/rewrite: the source table is not rewritten or updated. Creating the
-- projection foreign key retains SHARE ROW EXCLUSIVE on funding_settlements
-- until commit, blocking old-writer inserts. The bounded projection backfill
-- reads at most 100000 immutable funding rows; indexes are built only on the
-- new bounded projection. The no-overlap deployment remains halted throughout.
-- Transaction: the migrator applies both read functions and grants atomically
-- under its bounded lock_timeout.
-- Compatibility: the new binary writes the projection atomically with each
-- funding settlement. Older binaries fail exact immutable-migration
-- verification and must not overlap.
-- Failure/retry: databases above the explicit backfill bound or rows without a
-- valid durable receipt fail closed and roll back the whole migration. A larger
-- bound requires measured downtime and owner approval before this unapplied
-- migration changes; retry otherwise uses this unchanged forward migration.

CREATE TABLE trading.funding_history_projection (
    funding_id uuid PRIMARY KEY
        REFERENCES trading.funding_settlements(funding_id),
    account_id text NOT NULL,
    instrument_id text NOT NULL,
    position_id uuid NOT NULL,
    logical_time bigint NOT NULL
);

CREATE TRIGGER funding_history_projection_is_immutable
BEFORE UPDATE OR DELETE ON trading.funding_history_projection
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

REVOKE ALL ON TABLE trading.funding_history_projection FROM PUBLIC;
GRANT SELECT, INSERT ON TABLE trading.funding_history_projection
TO platformgo_engine;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM trading.funding_settlements
         OFFSET 100000
         LIMIT 1
    ) THEN
        RAISE EXCEPTION
            'funding history backfill exceeds the 100000-row halted-migration bound'
            USING
                ERRCODE = '55000',
                HINT = 'measure and obtain owner approval for a larger halted-migration bound before changing this unapplied migration';
    END IF;
END;
$$;

INSERT INTO trading.funding_history_projection (
    funding_id,
    account_id,
    instrument_id,
    position_id,
    logical_time
)
SELECT
    funding.funding_id,
    funding.account_id,
    funding.instrument_id,
    funding.position_id,
    (receipt.envelope ->> 'LogicalTime')::bigint
  FROM trading.funding_settlements AS funding
  JOIN engine.account_shards AS account_shard
    ON account_shard.account_id = funding.account_id
  JOIN engine.input_receipts AS receipt
    ON receipt.shard_id = account_shard.shard_id
   AND receipt.input_id = funding.input_id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM trading.funding_settlements AS funding
          LEFT JOIN trading.funding_history_projection AS history
            ON history.funding_id = funding.funding_id
         WHERE history.funding_id IS NULL
    ) THEN
        RAISE EXCEPTION
            'funding history contains a row without a valid durable receipt projection'
            USING ERRCODE = '55000';
    END IF;
END;
$$;

CREATE INDEX funding_history_projection_account_idx
ON trading.funding_history_projection (
    account_id,
    logical_time DESC,
    funding_id DESC
);

CREATE INDEX funding_history_projection_instrument_idx
ON trading.funding_history_projection (
    instrument_id,
    logical_time DESC,
    funding_id DESC
);

CREATE INDEX funding_history_projection_account_position_idx
ON trading.funding_history_projection (
    account_id,
    position_id,
    logical_time
);

CREATE FUNCTION trading.require_funding_history_projection()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
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
$$;

REVOKE ALL ON FUNCTION trading.require_funding_history_projection()
FROM PUBLIC;

CREATE CONSTRAINT TRIGGER funding_settlement_requires_history_projection
AFTER INSERT ON trading.funding_settlements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trading.require_funding_history_projection();

CREATE FUNCTION trading.read_account_funding_history(
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
    position_id uuid,
    signed_quantity numeric,
    oracle_price numeric,
    funding_rate numeric,
    funding_amount numeric,
    settlement_currency text,
    funding_logical_time bigint,
    account_login bigint
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
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time,
            NULL::bigint
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
         WHERE history.account_id = requested_account_id
         ORDER BY history.logical_time DESC, history.funding_id DESC
         LIMIT requested_limit;
    ELSIF requested_forward THEN
        RETURN QUERY
        SELECT
            funding.funding_id,
            funding.instrument_id,
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time,
            NULL::bigint
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
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
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time,
            NULL::bigint
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
         WHERE history.account_id = requested_account_id
         ORDER BY history.logical_time ASC, history.funding_id ASC
         LIMIT requested_limit;
    ELSE
        RETURN QUERY
        SELECT
            funding.funding_id,
            funding.instrument_id,
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time,
            NULL::bigint
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
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

CREATE FUNCTION trading.read_symbol_funding_history(
    requested_symbol text,
    requested_cursor_time bigint,
    requested_cursor_id uuid,
    requested_cursor_present boolean,
    requested_limit integer,
    requested_forward boolean
)
RETURNS TABLE (
    funding_id uuid,
    instrument_id text,
    position_id uuid,
    signed_quantity numeric,
    oracle_price numeric,
    funding_rate numeric,
    funding_amount numeric,
    settlement_currency text,
    funding_logical_time bigint,
    account_login bigint
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
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time,
            profile.login
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
          LEFT JOIN identity.account_profiles AS profile
            ON profile.account_id = history.account_id
         WHERE history.instrument_id = requested_symbol
         ORDER BY history.logical_time DESC, history.funding_id DESC
         LIMIT requested_limit;
    ELSIF requested_forward THEN
        RETURN QUERY
        SELECT
            funding.funding_id,
            funding.instrument_id,
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time,
            profile.login
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
          LEFT JOIN identity.account_profiles AS profile
            ON profile.account_id = history.account_id
         WHERE history.instrument_id = requested_symbol
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
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time,
            profile.login
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
          LEFT JOIN identity.account_profiles AS profile
            ON profile.account_id = history.account_id
         WHERE history.instrument_id = requested_symbol
         ORDER BY history.logical_time ASC, history.funding_id ASC
         LIMIT requested_limit;
    ELSE
        RETURN QUERY
        SELECT
            funding.funding_id,
            funding.instrument_id,
            funding.position_id,
            funding.signed_quantity,
            funding.oracle_price,
            funding.rate,
            funding.amount,
            funding.settlement_currency,
            history.logical_time,
            profile.login
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
          LEFT JOIN identity.account_profiles AS profile
            ON profile.account_id = history.account_id
         WHERE history.instrument_id = requested_symbol
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

CREATE FUNCTION trading.account_position_funding_total(
    requested_account_id text,
    requested_position_id uuid,
    requested_since bigint
)
RETURNS numeric
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    total numeric;
BEGIN
    IF requested_since IS NULL THEN
        SELECT COALESCE(sum(funding.amount), 0)
          INTO total
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
         WHERE history.account_id = requested_account_id
           AND history.position_id = requested_position_id;
    ELSE
        SELECT COALESCE(sum(funding.amount), 0)
          INTO total
          FROM trading.funding_history_projection AS history
          JOIN trading.funding_settlements AS funding
            ON funding.funding_id = history.funding_id
         WHERE history.account_id = requested_account_id
           AND history.position_id = requested_position_id
           AND history.logical_time >= requested_since;
    END IF;
    RETURN total;
END;
$$;

CREATE FUNCTION trading.account_funding_history_count(requested_account_id text)
RETURNS bigint
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT count(*)
      FROM trading.funding_history_projection AS history
     WHERE history.account_id = requested_account_id
$$;

CREATE FUNCTION trading.symbol_funding_history_count(requested_symbol text)
RETURNS bigint
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT count(*)
      FROM trading.funding_history_projection AS history
     WHERE history.instrument_id = requested_symbol
$$;

REVOKE ALL ON FUNCTION trading.read_account_funding_history(
    text,
    bigint,
    uuid,
    boolean,
    integer,
    boolean
) FROM PUBLIC;
REVOKE ALL ON FUNCTION trading.read_symbol_funding_history(
    text,
    bigint,
    uuid,
    boolean,
    integer,
    boolean
) FROM PUBLIC;
REVOKE ALL ON FUNCTION trading.account_position_funding_total(
    text,
    uuid,
    bigint
) FROM PUBLIC;
REVOKE ALL ON FUNCTION trading.account_funding_history_count(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION trading.symbol_funding_history_count(text) FROM PUBLIC;

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
GRANT EXECUTE ON FUNCTION trading.account_position_funding_total(
    text,
    uuid,
    bigint
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION trading.account_funding_history_count(text)
TO platformgo_api;
GRANT EXECUTE ON FUNCTION trading.symbol_funding_history_count(text)
TO platformgo_api;
