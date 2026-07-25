-- Phase 3 authority, replay, and tenant hardening.
-- Lock/rewrite: ALTERs identity tables that are new in Phase 3 and validates
-- one FK to trading.accounts. The migrator applies a bounded lock_timeout;
-- operators still drain command/account admission for this upgrade.
-- Transaction: every statement and checksum record commits atomically.
-- Compatibility: Phase 2 binaries ignore the additive tables. Earlier Phase 3
-- candidates must be stopped before this forward correction is applied.

CREATE TABLE engine.runtime_configuration (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version bigint NOT NULL CHECK (version = 1)
);

INSERT INTO engine.runtime_configuration (singleton, version)
VALUES (true, 1);

CREATE TRIGGER runtime_configuration_is_immutable
BEFORE UPDATE OR DELETE ON engine.runtime_configuration
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TABLE trading.command_replay_responses (
    command_id uuid PRIMARY KEY REFERENCES trading.commands(command_id),
    response_status integer NOT NULL CHECK (
        response_status BETWEEN 100 AND 599
    ),
    response_headers jsonb NOT NULL,
    response_body bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER command_replay_responses_are_immutable
BEFORE UPDATE OR DELETE ON trading.command_replay_responses
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM identity.account_profiles) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'stop the unreleased Phase 3 candidate and clear its broker account fixtures before authority hardening';
    END IF;
END;
$$;

ALTER TABLE identity.users
ADD COLUMN broker_subject text;

ALTER TABLE identity.users
ADD CONSTRAINT users_broker_subject_format CHECK (
    broker_subject IS NULL
    OR broker_subject LIKE 'urn:xb:tenant:%'
);

ALTER TABLE identity.users
DROP CONSTRAINT users_normalized_login_key;

ALTER TABLE identity.users
DROP CONSTRAINT users_normalized_email_key;

CREATE UNIQUE INDEX users_client_login_key
ON identity.users (normalized_login)
WHERE broker_subject IS NULL;

CREATE UNIQUE INDEX users_client_email_key
ON identity.users (normalized_email)
WHERE broker_subject IS NULL AND normalized_email IS NOT NULL;

CREATE UNIQUE INDEX users_broker_login_key
ON identity.users (broker_subject, normalized_login)
WHERE broker_subject IS NOT NULL;

CREATE UNIQUE INDEX users_broker_email_key
ON identity.users (broker_subject, normalized_email)
WHERE broker_subject IS NOT NULL AND normalized_email IS NOT NULL;

ALTER TABLE identity.users
ADD CONSTRAINT users_id_broker_subject_key
UNIQUE (user_id, broker_subject);

ALTER TABLE identity.user_accounts
ADD COLUMN broker_subject text;

ALTER TABLE identity.user_accounts
ADD CONSTRAINT user_accounts_broker_subject_format CHECK (
    broker_subject IS NULL
    OR broker_subject LIKE 'urn:xb:tenant:%'
);

ALTER TABLE identity.user_accounts
ADD CONSTRAINT user_accounts_trading_account_fk
FOREIGN KEY (account_id)
REFERENCES trading.accounts(account_id)
NOT VALID;

ALTER TABLE identity.user_accounts
VALIDATE CONSTRAINT user_accounts_trading_account_fk;

ALTER TABLE identity.user_accounts
ADD CONSTRAINT user_accounts_broker_user_fk
FOREIGN KEY (user_id, broker_subject)
REFERENCES identity.users(user_id, broker_subject)
NOT VALID;

ALTER TABLE identity.user_accounts
VALIDATE CONSTRAINT user_accounts_broker_user_fk;

ALTER TABLE identity.account_profiles
ADD COLUMN broker_subject text NOT NULL;

ALTER TABLE identity.account_profiles
ADD CONSTRAINT account_profiles_broker_subject_format CHECK (
    broker_subject LIKE 'urn:xb:tenant:%'
);

ALTER TABLE identity.account_profiles
ADD CONSTRAINT account_profiles_supported_currency CHECK (
    base_currency = 'USDC'
);

ALTER TABLE identity.account_profiles
ADD CONSTRAINT account_profiles_supported_venue CHECK (
    market_venue = 'HYPERLIQUID'
);

ALTER TABLE identity.account_profiles
ADD CONSTRAINT account_profiles_supported_classes CHECK (
    permitted_classes = ARRAY['CRYPTOCURRENCY']::text[]
    AND array_position(permitted_classes, NULL) IS NULL
);

ALTER TABLE identity.account_profiles
ADD CONSTRAINT account_profiles_finite_created_at CHECK (
    isfinite(created_at)
);

CREATE TABLE identity.account_provisioning_intents (
    command_id uuid PRIMARY KEY REFERENCES trading.commands(command_id),
    account_id text NOT NULL UNIQUE CHECK (
        account_id LIKE 'urn:xb:account:%'
    ),
    broker_subject text NOT NULL CHECK (
        broker_subject LIKE 'urn:xb:tenant:%'
    ),
    user_id text NOT NULL,
    login bigint NOT NULL UNIQUE CHECK (login > 0),
    base_currency text NOT NULL CHECK (base_currency = 'USDC'),
    market_venue text NOT NULL CHECK (market_venue = 'HYPERLIQUID'),
    permitted_classes text[] NOT NULL CHECK (
        permitted_classes = ARRAY['CRYPTOCURRENCY']::text[]
        AND array_position(permitted_classes, NULL) IS NULL
    ),
    created_at timestamptz NOT NULL CHECK (isfinite(created_at)),
    FOREIGN KEY (user_id, broker_subject)
        REFERENCES identity.users(user_id, broker_subject)
);

CREATE TRIGGER account_provisioning_intents_are_immutable
BEFORE UPDATE OR DELETE ON identity.account_provisioning_intents
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE FUNCTION identity.create_broker_user(
    requested_broker_subject text,
    requested_user_id text,
    requested_login text,
    requested_email text
)
RETURNS TABLE (
    user_id text,
    login text,
    email text,
    created boolean
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    inserted_count bigint;
BEGIN
    IF requested_broker_subject NOT LIKE 'urn:xb:tenant:%'
        OR requested_user_id NOT LIKE 'urn:xb:user:%'
        OR requested_login = ''
        OR requested_login <> lower(btrim(requested_login))
        OR requested_email = ''
        OR requested_email <> lower(btrim(requested_email))
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid tenant-scoped broker user';
    END IF;

    INSERT INTO identity.users (
        user_id,
        broker_subject,
        login,
        normalized_login,
        email,
        normalized_email
    ) VALUES (
        requested_user_id,
        requested_broker_subject,
        requested_login,
        requested_login,
        requested_email,
        requested_email
    )
    ON CONFLICT (broker_subject, normalized_email)
        WHERE broker_subject IS NOT NULL
        DO NOTHING;
    GET DIAGNOSTICS inserted_count = ROW_COUNT;

    RETURN QUERY
    SELECT
        identity.users.user_id,
        identity.users.login,
        identity.users.email,
        inserted_count = 1
      FROM identity.users
     WHERE identity.users.broker_subject = requested_broker_subject
       AND identity.users.normalized_email = requested_email;
END;
$$;

CREATE FUNCTION identity.claim_broker_echo(
    requested_principal text,
    requested_idempotency_key text,
    requested_hash bytea,
    requested_result_id text,
    requested_expires_at timestamptz
)
RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    requested_scope text;
    stored_hash bytea;
    stored_status integer;
    stored_body jsonb;
BEGIN
    IF requested_principal NOT LIKE 'urn:xb:apikey:%'
        OR requested_idempotency_key = ''
        OR octet_length(requested_hash) <> 32
        OR requested_result_id = ''
        OR NOT isfinite(requested_expires_at)
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid broker echo claim';
    END IF;
    requested_scope := 'broker-echo' || chr(31) || requested_principal;

    INSERT INTO identity.idempotency_responses (
        scope,
        idempotency_key,
        request_hash,
        response_status,
        response_body,
        expires_at
    ) VALUES (
        requested_scope,
        requested_idempotency_key,
        requested_hash,
        200,
        jsonb_build_object('id', requested_result_id),
        requested_expires_at
    )
    ON CONFLICT (scope, idempotency_key) DO NOTHING;

    SELECT request_hash, response_status, response_body
      INTO STRICT stored_hash, stored_status, stored_body
      FROM identity.idempotency_responses
     WHERE scope = requested_scope
       AND idempotency_key = requested_idempotency_key;

    IF stored_hash <> requested_hash THEN
        RAISE EXCEPTION USING
            ERRCODE = '23505',
            MESSAGE = 'broker echo idempotency conflict';
    END IF;
    IF stored_status <> 200 OR stored_body->>'id' IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'broker echo stored response is inconsistent';
    END IF;
    RETURN stored_body->>'id';
END;
$$;

REVOKE INSERT ON identity.users FROM platformgo_api;
REVOKE INSERT ON identity.user_accounts FROM platformgo_api;
REVOKE INSERT ON identity.idempotency_responses FROM platformgo_api;
REVOKE EXECUTE ON FUNCTION identity.provision_broker_account(
    text,
    text,
    bigint,
    text,
    text,
    text[],
    timestamptz
) FROM platformgo_api;

REVOKE ALL ON FUNCTION identity.create_broker_user(
    text,
    text,
    text,
    text
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.create_broker_user(
    text,
    text,
    text,
    text
) TO platformgo_api;

REVOKE ALL ON FUNCTION identity.claim_broker_echo(
    text,
    text,
    bytea,
    text,
    timestamptz
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.claim_broker_echo(
    text,
    text,
    bytea,
    text,
    timestamptz
) TO platformgo_api;

GRANT SELECT ON engine.runtime_configuration TO platformgo_api;
GRANT SELECT ON engine.runtime_configuration TO platformgo_engine;

GRANT SELECT, INSERT ON trading.command_replay_responses
TO platformgo_api;
GRANT SELECT ON trading.command_replay_responses TO platformgo_engine;
GRANT SELECT ON trading.idempotency_records TO platformgo_engine;
GRANT UPDATE (
    state,
    response_status,
    response_headers,
    response_body
) ON trading.idempotency_records TO platformgo_engine;

GRANT SELECT, INSERT ON identity.account_provisioning_intents
TO platformgo_api;
GRANT SELECT ON identity.account_provisioning_intents
TO platformgo_engine;
GRANT SELECT ON identity.users TO platformgo_engine;
GRANT INSERT ON identity.user_accounts TO platformgo_engine;
GRANT INSERT ON identity.account_profiles TO platformgo_engine;
