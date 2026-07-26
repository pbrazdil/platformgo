-- Phase 3 user API-key creation, replay, rate-limit, and audit authority.
-- Lock/rewrite: creates new schemas, tables, indexes, and functions. The new
-- foreign keys briefly lock identity.users for metadata only; no existing row
-- is rewritten or scanned.
-- Transaction: creation claims optional idempotency, enforces the durable
-- policy, inserts the key, appends its audit fact, and stores only encrypted
-- replay material in one transaction.
-- Compatibility: older binaries ignore the additive objects. The API role can
-- execute only the bounded authority functions and cannot mutate the durable
-- key, policy, replay, rate-limit, or audit tables directly.
-- Failure/retry: lock acquisition is bounded. A failed migration leaves no
-- partial schema or journal entry and retries from migration 006.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

CREATE SCHEMA audit;

CREATE TABLE identity.api_key_policy (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version bigint NOT NULL CHECK (version > 0),
    max_active_per_owner integer NOT NULL CHECK (
        max_active_per_owner BETWEEN 1 AND 25
    ),
    client_rate_limit_max_requests integer NOT NULL CHECK (
        client_rate_limit_max_requests > 0
    ),
    client_rate_limit_window_seconds integer NOT NULL CHECK (
        client_rate_limit_window_seconds > 0
    ),
    idempotency_ttl_seconds integer NOT NULL CHECK (
        idempotency_ttl_seconds > 0
    )
);

INSERT INTO identity.api_key_policy (
    singleton,
    version,
    max_active_per_owner,
    client_rate_limit_max_requests,
    client_rate_limit_window_seconds,
    idempotency_ttl_seconds
) VALUES (
    true,
    1,
    25,
    600,
    60,
    86400
);

CREATE TABLE identity.api_keys (
    api_key_id uuid PRIMARY KEY,
    owner_user_id text NOT NULL REFERENCES identity.users(user_id),
    name text NOT NULL CHECK (name <> ''),
    key_hash bytea NOT NULL CHECK (octet_length(key_hash) = 32),
    prefix text NOT NULL UNIQUE CHECK (prefix ~ '^[0-9a-f]{12}$'),
    scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    created_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX api_keys_active_owner_idx
ON identity.api_keys (owner_user_id, api_key_id)
WHERE revoked_at IS NULL;

CREATE TABLE identity.api_key_replays (
    owner_user_id text NOT NULL REFERENCES identity.users(user_id),
    idempotency_key text NOT NULL CHECK (idempotency_key <> ''),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    response_status integer NOT NULL CHECK (response_status = 201),
    replay_key_id text NOT NULL CHECK (
        replay_key_id ~ '^[A-Za-z0-9._-]{1,64}$'
    ),
    response_nonce bytea NOT NULL CHECK (octet_length(response_nonce) = 12),
    response_ciphertext bytea NOT NULL CHECK (
        octet_length(response_ciphertext) > 16
    ),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    PRIMARY KEY (owner_user_id, idempotency_key)
);

CREATE INDEX api_key_replays_expiry_idx
ON identity.api_key_replays (expires_at, owner_user_id, idempotency_key);

CREATE TABLE identity.client_rate_limits (
    owner_user_id text PRIMARY KEY REFERENCES identity.users(user_id),
    window_started_at timestamptz NOT NULL,
    request_count integer NOT NULL CHECK (request_count > 0)
);

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

CREATE FUNCTION identity.claim_client_rate_limit(
    requested_owner_user_id text,
    requested_at timestamptz
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET lock_timeout = '5s'
AS $$
DECLARE
    policy_max integer;
    policy_window_seconds integer;
    stored_window_started_at timestamptz;
    stored_request_count integer;
    inserted_count integer;
    authority_time timestamptz;
BEGIN
    authority_time := statement_timestamp();
    IF requested_owner_user_id NOT LIKE 'urn:xb:user:%'
        OR NOT isfinite(requested_at)
        OR abs(extract(epoch FROM requested_at - authority_time)) > 5
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid client rate-limit claim';
    END IF;

    SELECT
        policy.client_rate_limit_max_requests,
        policy.client_rate_limit_window_seconds
      INTO STRICT policy_max, policy_window_seconds
      FROM identity.api_key_policy AS policy
     WHERE policy.singleton
     FOR SHARE;

    INSERT INTO identity.client_rate_limits (
        owner_user_id,
        window_started_at,
        request_count
    ) VALUES (
        requested_owner_user_id,
        authority_time,
        1
    )
    ON CONFLICT (owner_user_id) DO NOTHING;
    GET DIAGNOSTICS inserted_count = ROW_COUNT;
    IF inserted_count = 1 THEN
        RETURN true;
    END IF;

    SELECT limits.window_started_at, limits.request_count
      INTO STRICT stored_window_started_at, stored_request_count
      FROM identity.client_rate_limits AS limits
     WHERE limits.owner_user_id = requested_owner_user_id
     FOR UPDATE;

    IF authority_time < stored_window_started_at THEN
        RETURN false;
    END IF;
    IF authority_time >= stored_window_started_at
        + make_interval(secs => policy_window_seconds)
    THEN
        UPDATE identity.client_rate_limits
           SET window_started_at = authority_time,
               request_count = 1
         WHERE owner_user_id = requested_owner_user_id;
        RETURN true;
    END IF;
    IF stored_request_count >= policy_max THEN
        RETURN false;
    END IF;

    UPDATE identity.client_rate_limits
       SET request_count = request_count + 1
     WHERE owner_user_id = requested_owner_user_id;
    RETURN true;
END;
$$;

CREATE FUNCTION identity.create_user_api_key(
    requested_owner_user_id text,
    requested_api_key_id uuid,
    requested_name text,
    requested_key_hash bytea,
    requested_prefix text,
    requested_scopes text[],
    requested_audit_event_id uuid,
    requested_request_id text,
    requested_idempotency_key text,
    requested_request_hash bytea,
    requested_replay_key_id text,
    requested_response_nonce bytea,
    requested_response_ciphertext bytea
)
RETURNS TABLE (
    replayed boolean,
    replay_key_id text,
    response_nonce bytea,
    response_ciphertext bytea
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET lock_timeout = '5s'
AS $$
DECLARE
    owner_exists text;
    authority_time timestamptz;
    active_count bigint;
    policy_version bigint;
    policy_max_active integer;
    policy_idempotency_ttl integer;
    stored_request_hash bytea;
    stored_replay_key_id text;
    stored_response_nonce bytea;
    stored_response_ciphertext bytea;
    stored_expires_at timestamptz;
BEGIN
    IF requested_name = ''
        OR octet_length(requested_key_hash) <> 32
        OR requested_prefix !~ '^[0-9a-f]{12}$'
        OR requested_scopes IS NULL
        OR octet_length(requested_request_hash) <> 32
        OR requested_replay_key_id !~ '^[A-Za-z0-9._-]{1,64}$'
        OR octet_length(requested_response_nonce) <> 12
        OR octet_length(requested_response_ciphertext) <= 16
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid API-key creation request';
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

    SELECT
        policy.version,
        policy.max_active_per_owner,
        policy.idempotency_ttl_seconds
      INTO STRICT
        policy_version,
        policy_max_active,
        policy_idempotency_ttl
      FROM identity.api_key_policy AS policy
     WHERE policy.singleton
     FOR SHARE;

    authority_time := statement_timestamp();

    IF requested_idempotency_key <> '' THEN
        SELECT
            replays.request_hash,
            replays.replay_key_id,
            replays.response_nonce,
            replays.response_ciphertext,
            replays.expires_at
          INTO
            stored_request_hash,
            stored_replay_key_id,
            stored_response_nonce,
            stored_response_ciphertext,
            stored_expires_at
          FROM identity.api_key_replays AS replays
         WHERE replays.owner_user_id = requested_owner_user_id
           AND replays.idempotency_key = requested_idempotency_key
         FOR UPDATE;

        IF FOUND AND stored_expires_at <= authority_time THEN
            DELETE FROM identity.api_key_replays
             WHERE owner_user_id = requested_owner_user_id
               AND idempotency_key = requested_idempotency_key;
            stored_request_hash := NULL;
        ELSIF FOUND THEN
            IF stored_request_hash <> requested_request_hash THEN
                RAISE EXCEPTION USING
                    ERRCODE = 'P0003',
                    MESSAGE = 'API-key idempotency conflict';
            END IF;
            RETURN QUERY SELECT
                true,
                stored_replay_key_id,
                stored_response_nonce,
                stored_response_ciphertext;
            RETURN;
        END IF;
    END IF;

    SELECT count(*)
      INTO active_count
      FROM identity.api_keys AS keys
     WHERE keys.owner_user_id = requested_owner_user_id
       AND keys.revoked_at IS NULL;

    IF active_count >= policy_max_active THEN
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
        authority_time
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
        authority_time,
        requested_request_id,
        'user',
        requested_owner_user_id,
        'user-key.create',
        'api_key',
        'urn:xb:apikey:' || requested_api_key_id::text,
        'success',
        jsonb_build_object(
            'configurationVersion', policy_version,
            'effectiveMaxActive', policy_max_active,
            'before', NULL,
            'after', jsonb_build_object(
                'name', requested_name,
                'scopes', requested_scopes
            )
        )
    );

    IF requested_idempotency_key <> '' THEN
        INSERT INTO identity.api_key_replays (
            owner_user_id,
            idempotency_key,
            request_hash,
            response_status,
            replay_key_id,
            response_nonce,
            response_ciphertext,
            created_at,
            expires_at
        ) VALUES (
            requested_owner_user_id,
            requested_idempotency_key,
            requested_request_hash,
            201,
            requested_replay_key_id,
            requested_response_nonce,
            requested_response_ciphertext,
            authority_time,
            authority_time + make_interval(secs => policy_idempotency_ttl)
        );
    END IF;

    RETURN QUERY SELECT
        false,
        requested_replay_key_id,
        requested_response_nonce,
        requested_response_ciphertext;
END;
$$;

REVOKE ALL ON SCHEMA audit FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA audit FROM PUBLIC;
REVOKE ALL ON identity.api_key_policy FROM PUBLIC;
REVOKE ALL ON identity.api_keys FROM PUBLIC;
REVOKE ALL ON identity.api_key_replays FROM PUBLIC;
REVOKE ALL ON identity.client_rate_limits FROM PUBLIC;

REVOKE ALL ON FUNCTION identity.claim_client_rate_limit(
    text,
    timestamptz
) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.create_user_api_key(
    text,
    uuid,
    text,
    bytea,
    text,
    text[],
    uuid,
    text,
    text,
    bytea,
    text,
    bytea,
    bytea
) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION identity.claim_client_rate_limit(
    text,
    timestamptz
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION identity.create_user_api_key(
    text,
    uuid,
    text,
    bytea,
    text,
    text[],
    uuid,
    text,
    text,
    bytea,
    text,
    bytea,
    bytea
) TO platformgo_api;
