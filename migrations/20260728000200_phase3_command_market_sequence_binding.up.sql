-- Bound both historical preflight scans as well as lock acquisition. A timeout
-- rolls back the whole candidate and is safe to retry after operator review.
SET LOCAL statement_timeout = '10s';

-- A live engine owns this same session advisory key. Acquiring its
-- transaction-scoped counterpart before table locks prevents an old binary
-- from pausing at cutover and resuming under pre-cutover write semantics.
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
END;
$$;

-- A pending legacy command with an explicit market fence cannot be replayed
-- after a later market transition without changing which book state it uses.
-- Refuse that ambiguous cutover instead of silently rebinding money behavior.
-- SHARE waits for in-flight legacy command writers and prevents another
-- ROW EXCLUSIVE admission from crossing the preflight-to-DDL boundary.
-- SHARE ROW EXCLUSIVE applies the same no-overlap rule to legacy market
-- commits and is retained until their durable post-cutover guard is installed.
LOCK TABLE trading.commands IN SHARE MODE;
LOCK TABLE engine.input_receipts IN SHARE ROW EXCLUSIVE MODE;

-- A process can verify the previous schema and pause before acquiring shard
-- ownership. Replacing the per-write revision fence prevents that preverified
-- process from resuming after this transaction commits. Cover every durable
-- engine authority row, including rejection/fault paths that do not insert a
-- business receipt.
CREATE OR REPLACE FUNCTION engine.require_runtime_schema_revision()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF current_setting(
           'platformgo.runtime_schema_revision',
           true
       ) IS DISTINCT FROM
           '20260728000200_phase3_command_market_sequence_binding' THEN
        RAISE EXCEPTION
            'engine runtime schema revision is missing or incompatible'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER duplicate_delivery_receipts_require_runtime_schema_revision
BEFORE INSERT ON engine.duplicate_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION engine.require_runtime_schema_revision();

CREATE TRIGGER shard_faults_require_runtime_schema_revision
BEFORE INSERT ON engine.shard_faults
FOR EACH ROW EXECUTE FUNCTION engine.require_runtime_schema_revision();

CREATE TRIGGER shard_checkpoints_require_runtime_schema_revision
BEFORE INSERT OR UPDATE ON engine.shard_checkpoints
FOR EACH ROW EXECUTE FUNCTION engine.require_runtime_schema_revision();

CREATE TRIGGER shard_ownership_epochs_require_runtime_schema_revision
BEFORE INSERT OR UPDATE ON engine.shard_ownership_epochs
FOR EACH ROW EXECUTE FUNCTION engine.require_runtime_schema_revision();

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM trading.commands AS command
          JOIN messaging.outbox AS outbox
            ON outbox.message_id = command.command_id
         WHERE command.status = 'pending'
           AND COALESCE((outbox.payload ->> 'marketSequence')::bigint, 0) <> 0
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'pending explicit command market bindings must be drained before upgrade';
    END IF;
END;
$$;

-- Lock/rewrite: this nullable add is metadata-only and does not backfill or
-- rewrite trading.commands. NULL identifies legacy rows; runtime comparison
-- derives only those rows' representation from their immutable API outbox.
ALTER TABLE trading.commands
    ADD COLUMN market_sequence_binding text;

ALTER TABLE trading.commands
    ALTER COLUMN market_sequence_binding SET DEFAULT 'ordered',
    ADD CONSTRAINT commands_market_sequence_binding_valid
        CHECK (
            market_sequence_binding IS NULL
            OR market_sequence_binding = 'ordered'
        ) NOT VALID;

-- Keep the previous binary safe during the one-release compatibility window:
-- it omits the new column and therefore receives the ordered default, while
-- this deferred commit check rejects its formerly legal nonzero API envelope.
CREATE FUNCTION trading.require_command_market_sequence_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.market_sequence_binding IS DISTINCT FROM 'ordered' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'new command market binding must be ordered';
    END IF;
    IF EXISTS (
        SELECT 1
          FROM messaging.outbox AS outbox
         WHERE outbox.message_id = NEW.command_id
           AND (
               outbox.producer_class <> 'api'
               OR COALESCE(
                   (outbox.payload ->> 'marketSequence')::bigint,
                   0
               ) <> 0
           )
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'command market binding does not match API outbox';
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER commands_require_market_sequence_binding
AFTER INSERT OR UPDATE OF market_sequence_binding ON trading.commands
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trading.require_command_market_sequence_binding();

CREATE FUNCTION messaging.require_outbox_command_market_sequence_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    binding text;
BEGIN
    SELECT command.market_sequence_binding
      INTO binding
      FROM trading.commands AS command
     WHERE command.command_id = NEW.message_id;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    IF binding IS DISTINCT FROM 'ordered'
        OR NEW.producer_class <> 'api'
        OR COALESCE(
            (NEW.payload ->> 'marketSequence')::bigint,
            0
        ) <> 0
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'API outbox does not match command market binding';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER outbox_insert_requires_command_market_sequence_binding
BEFORE INSERT ON messaging.outbox
FOR EACH ROW
EXECUTE FUNCTION messaging.require_outbox_command_market_sequence_binding();

CREATE TRIGGER outbox_update_requires_command_market_sequence_binding
BEFORE UPDATE OF payload, producer_class ON messaging.outbox
FOR EACH ROW
EXECUTE FUNCTION messaging.require_outbox_command_market_sequence_binding();

CREATE FUNCTION engine.require_authoritative_market_receipt()
RETURNS trigger
LANGUAGE plpgsql
AS $$
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
$$;

CREATE TRIGGER input_receipts_require_authoritative_market_state
BEFORE INSERT ON engine.input_receipts
FOR EACH ROW EXECUTE FUNCTION engine.require_authoritative_market_receipt();
