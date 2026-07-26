-- Phase 3 user API-key creation, replay, rate-limit, and audit authority.
-- Lock/rewrite: creates new schemas, tables, indexes, and functions. The new
-- foreign keys briefly lock identity.users for metadata only; no existing row
-- is rewritten or scanned.
-- Transaction: creation claims optional idempotency, enforces the durable
-- policy, inserts the key, appends its audit fact, and stores only encrypted
-- replay material in one transaction.
-- Binary rollback: older binaries reject this schema as ahead. After apply,
-- recover with a forward fix or halt and restore the pre-migration backup.
-- The API role can execute only the bounded authority functions and cannot
-- mutate the durable key, policy, replay, rate-limit, or audit tables directly.
-- Failure/retry: lock acquisition is bounded. A failed migration leaves no
-- partial schema or journal entry and retries from migration 006.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

CREATE SCHEMA audit;

CREATE TABLE identity.api_key_policy (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version bigint NOT NULL CHECK (version > 0),
    max_active_per_owner bigint NOT NULL,
    client_rate_limit_max_requests bigint NOT NULL CHECK (
        client_rate_limit_max_requests BETWEEN 0 AND 4294967295
    ),
    client_rate_limit_window_seconds numeric(20, 0) NOT NULL CHECK (
        client_rate_limit_window_seconds
            BETWEEN 0 AND 18446744073709551615
    ),
    idempotency_ttl_seconds numeric(20, 0) NOT NULL CHECK (
        idempotency_ttl_seconds BETWEEN 0 AND 18446744073709551615
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
    idempotency_key_hash bytea NOT NULL CHECK (
        octet_length(idempotency_key_hash) = 32
    ),
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
    PRIMARY KEY (owner_user_id, idempotency_key_hash)
);

CREATE INDEX api_key_replays_expiry_idx
ON identity.api_key_replays (
    expires_at,
    owner_user_id,
    idempotency_key_hash
);

CREATE TABLE identity.client_rate_limits (
    owner_user_id text PRIMARY KEY REFERENCES identity.users(user_id),
    window_started_at timestamptz NOT NULL,
    request_count bigint NOT NULL CHECK (request_count > 0)
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
    requested_owner_user_id text
)
RETURNS TABLE (
    allowed boolean,
    retry_after_seconds numeric(20, 0)
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET lock_timeout = '5s'
AS $$
DECLARE
    policy_max bigint;
    policy_window_seconds numeric(20, 0);
    stored_window_started_at timestamptz;
    stored_request_count bigint;
    inserted_count integer;
    authority_time timestamptz;
BEGIN
    authority_time := statement_timestamp();
    IF requested_owner_user_id NOT LIKE 'urn:xb:user:%' THEN
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
        IF policy_max = 0 THEN
            RETURN QUERY SELECT false, policy_window_seconds;
        ELSE
            RETURN QUERY SELECT true, 0::numeric;
        END IF;
        RETURN;
    END IF;

    SELECT limits.window_started_at, limits.request_count
      INTO STRICT stored_window_started_at, stored_request_count
      FROM identity.client_rate_limits AS limits
     WHERE limits.owner_user_id = requested_owner_user_id
     FOR UPDATE;

    IF authority_time < stored_window_started_at THEN
        RETURN QUERY SELECT false, policy_window_seconds;
        RETURN;
    END IF;
    IF authority_time >= stored_window_started_at
        AND extract(
            epoch FROM authority_time - stored_window_started_at
        )::numeric >= policy_window_seconds
    THEN
        UPDATE identity.client_rate_limits
           SET window_started_at = authority_time,
               request_count = 1
         WHERE owner_user_id = requested_owner_user_id;
        IF policy_max = 0 THEN
            RETURN QUERY SELECT false, policy_window_seconds;
        ELSE
            RETURN QUERY SELECT true, 0::numeric;
        END IF;
        RETURN;
    END IF;
    IF stored_request_count >= policy_max THEN
        RETURN QUERY SELECT false, policy_window_seconds;
        RETURN;
    END IF;

    UPDATE identity.client_rate_limits
       SET request_count = request_count + 1
     WHERE owner_user_id = requested_owner_user_id;
    RETURN QUERY SELECT true, 0::numeric;
    RETURN;
END;
$$;

CREATE FUNCTION identity.replay_user_api_key(
    requested_owner_user_id text,
    requested_idempotency_key_hash bytea,
    requested_request_hash bytea
)
RETURNS TABLE (
    found boolean,
    response_status integer,
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
    stored_request_hash bytea;
    stored_response_status integer;
    stored_replay_key_id text;
    stored_response_nonce bytea;
    stored_response_ciphertext bytea;
    stored_expires_at timestamptz;
BEGIN
    IF requested_owner_user_id NOT LIKE 'urn:xb:user:%'
        OR octet_length(requested_idempotency_key_hash) <> 32
        OR octet_length(requested_request_hash) <> 32
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid API-key replay request';
    END IF;

    SELECT
        replays.request_hash,
        replays.response_status,
        replays.replay_key_id,
        replays.response_nonce,
        replays.response_ciphertext,
        replays.expires_at
      INTO
        stored_request_hash,
        stored_response_status,
        stored_replay_key_id,
        stored_response_nonce,
        stored_response_ciphertext,
        stored_expires_at
     FROM identity.api_key_replays AS replays
     WHERE replays.owner_user_id = requested_owner_user_id
       AND replays.idempotency_key_hash = requested_idempotency_key_hash;

    IF NOT FOUND OR stored_expires_at <= statement_timestamp() THEN
        RETURN QUERY SELECT
            false,
            0,
            ''::text,
            ''::bytea,
            ''::bytea;
        RETURN;
    END IF;
    IF stored_request_hash <> requested_request_hash THEN
        RAISE EXCEPTION USING
            ERRCODE = 'P0003',
            MESSAGE = 'API-key idempotency conflict';
    END IF;
    RETURN QUERY SELECT
        true,
        stored_response_status,
        stored_replay_key_id,
        stored_response_nonce,
        stored_response_ciphertext;
END;
$$;

CREATE FUNCTION identity.verify_api_key_policy(
    expected_max_active_per_owner bigint,
    expected_rate_limit_max_requests bigint,
    expected_rate_limit_window_seconds numeric(20, 0),
    expected_idempotency_ttl_seconds numeric(20, 0)
)
RETURNS boolean
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT
        (expected_max_active_per_owner IS NULL
            OR policy.max_active_per_owner = expected_max_active_per_owner)
        AND (expected_rate_limit_max_requests IS NULL
            OR policy.client_rate_limit_max_requests
                = expected_rate_limit_max_requests)
        AND (expected_rate_limit_window_seconds IS NULL
            OR policy.client_rate_limit_window_seconds
                = expected_rate_limit_window_seconds)
        AND (expected_idempotency_ttl_seconds IS NULL
            OR policy.idempotency_ttl_seconds
                = expected_idempotency_ttl_seconds)
      FROM identity.api_key_policy AS policy
     WHERE policy.singleton
$$;

CREATE FUNCTION identity.purge_expired_api_key_replays(
    requested_batch_limit integer
)
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET lock_timeout = '5s'
AS $$
DECLARE
    deleted_count bigint;
BEGIN
    IF requested_batch_limit NOT BETWEEN 1 AND 1000 THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid API-key replay cleanup batch';
    END IF;

    WITH expired AS (
        SELECT replays.owner_user_id, replays.idempotency_key_hash
          FROM identity.api_key_replays AS replays
         WHERE replays.expires_at <= statement_timestamp()
         ORDER BY
            replays.expires_at,
            replays.owner_user_id,
            replays.idempotency_key_hash
         FOR UPDATE SKIP LOCKED
         LIMIT requested_batch_limit
    )
    DELETE FROM identity.api_key_replays AS replays
     USING expired
     WHERE replays.owner_user_id = expired.owner_user_id
       AND replays.idempotency_key_hash = expired.idempotency_key_hash;
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$;

CREATE FUNCTION identity.api_key_replay_coverage()
RETURNS TABLE (
    replay_key_id text,
    live_count bigint,
    oldest_expires_at text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT
        replays.replay_key_id,
        count(*)::bigint,
        min(replays.expires_at)::text
      FROM identity.api_key_replays AS replays
     WHERE replays.expires_at > statement_timestamp()
     GROUP BY replays.replay_key_id
     ORDER BY replays.replay_key_id;
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
    requested_idempotency_key_hash bytea,
    requested_request_hash bytea,
    requested_replay_key_id text,
    requested_response_nonce bytea,
    requested_response_ciphertext bytea
)
RETURNS TABLE (
    outcome text,
    response_status integer,
    retry_after_seconds numeric(20, 0),
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
    policy_max_active bigint;
    policy_idempotency_ttl numeric(20, 0);
    stored_request_hash bytea;
    stored_replay_key_id text;
    stored_response_nonce bytea;
    stored_response_ciphertext bytea;
    stored_expires_at timestamptz;
    rate_allowed boolean;
    rate_retry_after numeric(20, 0);
BEGIN
    IF requested_name = ''
        OR octet_length(requested_idempotency_key_hash) <> 32
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
       AND replays.idempotency_key_hash = requested_idempotency_key_hash
     FOR UPDATE;

    IF FOUND AND stored_expires_at <= authority_time THEN
        DELETE FROM identity.api_key_replays
         WHERE owner_user_id = requested_owner_user_id
           AND idempotency_key_hash = requested_idempotency_key_hash;
        stored_request_hash := NULL;
    ELSIF FOUND THEN
        IF stored_request_hash <> requested_request_hash THEN
            RETURN QUERY SELECT
                'idempotency_conflict'::text,
                0,
                0::numeric,
                ''::text,
                ''::bytea,
                ''::bytea;
            RETURN;
        END IF;
        RETURN QUERY SELECT
            'replayed'::text,
            201,
            0::numeric,
            stored_replay_key_id,
            stored_response_nonce,
            stored_response_ciphertext;
        RETURN;
    END IF;

    SELECT rate.allowed, rate.retry_after_seconds
      INTO STRICT rate_allowed, rate_retry_after
      FROM identity.claim_client_rate_limit(
        requested_owner_user_id
      ) AS rate;
    IF NOT rate_allowed THEN
        RETURN QUERY SELECT
            'rate_limited'::text,
            0,
            rate_retry_after,
            ''::text,
            ''::bytea,
            ''::bytea;
        RETURN;
    END IF;

    SELECT count(*)
      INTO active_count
      FROM identity.api_keys AS keys
     WHERE keys.owner_user_id = requested_owner_user_id
       AND keys.revoked_at IS NULL;

    IF active_count >= policy_max_active THEN
        RETURN QUERY SELECT
            'cap_conflict'::text,
            0,
            0::numeric,
            ''::text,
            ''::bytea,
            ''::bytea;
        RETURN;
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

    INSERT INTO identity.api_key_replays (
        owner_user_id,
        idempotency_key_hash,
        request_hash,
        response_status,
        replay_key_id,
        response_nonce,
        response_ciphertext,
        created_at,
        expires_at
    ) VALUES (
        requested_owner_user_id,
        requested_idempotency_key_hash,
        requested_request_hash,
        201,
        requested_replay_key_id,
        requested_response_nonce,
        requested_response_ciphertext,
        authority_time,
        CASE
            WHEN policy_idempotency_ttl > 8000000000000
                THEN 'infinity'::timestamptz
            ELSE authority_time + make_interval(
                secs => policy_idempotency_ttl::double precision
            )
        END
    );

    RETURN QUERY SELECT
        'created'::text,
        201,
        0::numeric,
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
    text
) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.replay_user_api_key(
    text,
    bytea,
    bytea
) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.verify_api_key_policy(
    bigint,
    bigint,
    numeric,
    numeric
) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.purge_expired_api_key_replays(
    integer
) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.api_key_replay_coverage()
FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.create_user_api_key(
    text,
    uuid,
    text,
    bytea,
    text,
    text[],
    uuid,
    text,
    bytea,
    bytea,
    text,
    bytea,
    bytea
) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION identity.claim_client_rate_limit(
    text
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION identity.replay_user_api_key(
    text,
    bytea,
    bytea
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION identity.verify_api_key_policy(
    bigint,
    bigint,
    numeric,
    numeric
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION identity.purge_expired_api_key_replays(
    integer
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION identity.api_key_replay_coverage()
TO platformgo_api;
GRANT EXECUTE ON FUNCTION identity.create_user_api_key(
    text,
    uuid,
    text,
    bytea,
    text,
    text[],
    uuid,
    text,
    bytea,
    bytea,
    text,
    bytea,
    bytea
) TO platformgo_api;
