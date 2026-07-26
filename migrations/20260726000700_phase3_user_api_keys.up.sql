-- Phase 3 user API-key creation and audit authority.
-- Lock/rewrite: creates new schemas, tables, indexes, and a function. The new
-- foreign key briefly locks identity.users for metadata only; no existing row
-- is rewritten or scanned.
-- Transaction: the migrator applies this file and its checksum journal record
-- atomically. Runtime key creation, owner-cap enforcement, and audit insertion
-- occur in one function transaction.
-- Compatibility: older binaries ignore the additive objects. The API role can
-- execute only the bounded creation function and cannot mutate either table
-- directly.
-- Failure/retry: lock acquisition is bounded. A failed migration leaves no
-- partial schema or journal entry and retries from migration 006.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

CREATE SCHEMA audit;

CREATE TABLE identity.api_keys (
    api_key_id uuid PRIMARY KEY,
    owner_user_id text NOT NULL REFERENCES identity.users(user_id),
    name text NOT NULL CHECK (
        name = btrim(name)
        AND char_length(name) BETWEEN 1 AND 128
    ),
    key_hash bytea NOT NULL CHECK (octet_length(key_hash) = 32),
    prefix text NOT NULL UNIQUE CHECK (prefix ~ '^[0-9a-f]{12}$'),
    scopes text[] NOT NULL DEFAULT ARRAY[]::text[] CHECK (
        cardinality(scopes) <= 32
    ),
    created_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX api_keys_active_owner_idx
ON identity.api_keys (owner_user_id, api_key_id)
WHERE revoked_at IS NULL;

CREATE TABLE audit.events (
    event_id uuid PRIMARY KEY,
    occurred_at timestamptz NOT NULL,
    request_id text NOT NULL CHECK (
        request_id = btrim(request_id)
        AND char_length(request_id) BETWEEN 1 AND 128
    ),
    actor_kind text NOT NULL CHECK (actor_kind = 'user'),
    actor_id text NOT NULL CHECK (actor_id LIKE 'urn:xb:user:%'),
    action text NOT NULL CHECK (action <> ''),
    target_kind text NOT NULL CHECK (target_kind <> ''),
    target_id text NOT NULL CHECK (target_id <> ''),
    outcome text NOT NULL CHECK (outcome = 'success'),
    detail jsonb NOT NULL
);

CREATE INDEX audit_events_action_idx
ON audit.events (action, outcome, occurred_at, event_id);

CREATE INDEX audit_events_actor_idx
ON audit.events (actor_kind, actor_id, occurred_at, event_id);

CREATE FUNCTION identity.guard_api_key_mutation()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP = 'DELETE'
        OR OLD.api_key_id IS DISTINCT FROM NEW.api_key_id
        OR OLD.owner_user_id IS DISTINCT FROM NEW.owner_user_id
        OR OLD.name IS DISTINCT FROM NEW.name
        OR OLD.key_hash IS DISTINCT FROM NEW.key_hash
        OR OLD.prefix IS DISTINCT FROM NEW.prefix
        OR OLD.scopes IS DISTINCT FROM NEW.scopes
        OR OLD.created_at IS DISTINCT FROM NEW.created_at
        OR OLD.revoked_at IS NOT NULL
        OR NEW.revoked_at IS NULL
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'API-key history is immutable except first revocation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER api_keys_guard_mutation
BEFORE UPDATE OR DELETE ON identity.api_keys
FOR EACH ROW EXECUTE FUNCTION identity.guard_api_key_mutation();

CREATE TRIGGER audit_events_are_immutable
BEFORE UPDATE OR DELETE ON audit.events
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE FUNCTION identity.create_user_api_key(
    requested_owner_user_id text,
    requested_api_key_id uuid,
    requested_name text,
    requested_key_hash bytea,
    requested_prefix text,
    requested_scopes text[],
    requested_created_at timestamptz,
    requested_audit_event_id uuid,
    requested_request_id text,
    requested_max_active integer,
    requested_configuration_version bigint
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET lock_timeout = '5s'
AS $$
DECLARE
    owner_exists text;
    active_count bigint;
BEGIN
    IF requested_max_active <= 0
        OR requested_max_active > 25
        OR requested_configuration_version <= 0
        OR requested_scopes IS NULL
        OR cardinality(requested_scopes) > 32
        OR EXISTS (
            SELECT 1
              FROM unnest(requested_scopes) AS requested_scope(value)
             WHERE requested_scope.value = ''
                OR requested_scope.value <> btrim(requested_scope.value)
                OR char_length(requested_scope.value) > 128
        )
        OR (
            SELECT count(*) <> count(DISTINCT requested_scope.value)
              FROM unnest(requested_scopes) AS requested_scope(value)
        )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid API-key creation configuration';
    END IF;

    SELECT users.user_id
      INTO owner_exists
      FROM identity.users AS users
     WHERE users.user_id = requested_owner_user_id
     FOR UPDATE;

    IF owner_exists IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = 'P0002',
            MESSAGE = 'API-key owner is unavailable';
    END IF;

    SELECT count(*)
      INTO active_count
      FROM identity.api_keys AS keys
     WHERE keys.owner_user_id = requested_owner_user_id
       AND keys.revoked_at IS NULL;

    IF active_count >= requested_max_active THEN
        RAISE EXCEPTION USING
            ERRCODE = 'P0001',
            MESSAGE = 'active API-key limit reached';
    END IF;

    INSERT INTO identity.api_keys (
        api_key_id,
        owner_user_id,
        name,
        key_hash,
        prefix,
        scopes,
        created_at
    ) VALUES (
        requested_api_key_id,
        requested_owner_user_id,
        requested_name,
        requested_key_hash,
        requested_prefix,
        requested_scopes,
        requested_created_at
    );

    INSERT INTO audit.events (
        event_id,
        occurred_at,
        request_id,
        actor_kind,
        actor_id,
        action,
        target_kind,
        target_id,
        outcome,
        detail
    ) VALUES (
        requested_audit_event_id,
        requested_created_at,
        requested_request_id,
        'user',
        requested_owner_user_id,
        'user-key.create',
        'api_key',
        'urn:xb:apikey:' || requested_api_key_id::text,
        'success',
        jsonb_build_object(
            'configurationVersion', requested_configuration_version,
            'before', NULL,
            'after', jsonb_build_object(
                'name', requested_name,
                'scopes', requested_scopes
            )
        )
    );
END;
$$;

REVOKE ALL ON SCHEMA audit FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA audit FROM PUBLIC;
REVOKE ALL ON identity.api_keys FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.create_user_api_key(
    text,
    uuid,
    text,
    bytea,
    text,
    text[],
    timestamptz,
    uuid,
    text,
    integer,
    bigint
) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION identity.create_user_api_key(
    text,
    uuid,
    text,
    bytea,
    text,
    text[],
    timestamptz,
    uuid,
    text,
    integer,
    bigint
) TO platformgo_api;
