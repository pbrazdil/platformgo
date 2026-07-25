DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM trading.commands AS command
          FULL OUTER JOIN trading.idempotency_records AS idempotency
            ON idempotency.command_id = command.command_id
         WHERE command.command_id IS NULL
            OR idempotency.command_id IS NULL
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'command and idempotency authority must be one-to-one';
    END IF;
END;
$$;

ALTER TABLE trading.idempotency_records
ADD CONSTRAINT idempotency_records_command_fk
FOREIGN KEY (command_id)
REFERENCES trading.commands(command_id)
DEFERRABLE INITIALLY DEFERRED
NOT VALID;

ALTER TABLE trading.idempotency_records
VALIDATE CONSTRAINT idempotency_records_command_fk;

CREATE OR REPLACE FUNCTION trading.require_command_idempotency()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM trading.idempotency_records
         WHERE command_id = NEW.command_id
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23503',
            MESSAGE = 'command requires exactly one idempotency record';
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER commands_require_idempotency
AFTER INSERT ON trading.commands
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trading.require_command_idempotency();

CREATE TRIGGER commands_cannot_be_deleted
BEFORE DELETE ON trading.commands
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER command_identity_is_immutable
BEFORE UPDATE OF
    command_id,
    account_id,
    account_sequence,
    command_type,
    schema_version,
    canonical_payload,
    logical_time,
    created_at
ON trading.commands
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER idempotency_records_cannot_be_deleted
BEFORE DELETE ON trading.idempotency_records
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER idempotency_identity_is_immutable
BEFORE UPDATE OF
    scope,
    idempotency_key,
    request_hash,
    command_id,
    created_at,
    expires_at
ON trading.idempotency_records
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();
