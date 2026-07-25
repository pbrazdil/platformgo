-- Phase 3 additive identity compatibility surface.
-- Lock/rewrite: new schema and tables only; no existing table rewrite.
-- Transaction: applied atomically by the migrator and safe to retry only
-- through the migration checksum journal.
-- Compatibility: Phase 2 binaries ignore the new schema. Phase 3 binaries
-- require it before serving identity-backed routes.

CREATE SCHEMA identity;

CREATE TABLE identity.users (
    user_id text PRIMARY KEY CHECK (user_id LIKE 'urn:xb:user:%'),
    login text NOT NULL CHECK (login <> ''),
    normalized_login text NOT NULL UNIQUE CHECK (
        normalized_login = lower(btrim(login))
    ),
    email text,
    normalized_email text UNIQUE CHECK (
        normalized_email IS NULL
        OR (email IS NOT NULL AND normalized_email = lower(btrim(email)))
    ),
    password_hash text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE identity.user_accounts (
    user_id text NOT NULL REFERENCES identity.users(user_id),
    account_id text NOT NULL UNIQUE CHECK (account_id LIKE 'urn:xb:account:%'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (user_id, account_id)
);

CREATE TABLE identity.sessions (
    session_id uuid PRIMARY KEY,
    user_id text NOT NULL REFERENCES identity.users(user_id),
    refresh_hash bytea NOT NULL UNIQUE CHECK (octet_length(refresh_hash) = 32),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE TABLE identity.idempotency_responses (
    scope text NOT NULL CHECK (scope <> ''),
    idempotency_key text NOT NULL CHECK (idempotency_key <> ''),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    response_status integer NOT NULL CHECK (response_status BETWEEN 200 AND 599),
    response_body jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (scope, idempotency_key)
);

CREATE TABLE trading.order_intents (
    order_id uuid PRIMARY KEY,
    command_id uuid NOT NULL UNIQUE REFERENCES trading.commands(command_id),
    account_id text NOT NULL CHECK (account_id <> ''),
    intent_id text NOT NULL CHECK (intent_id <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER order_intents_are_immutable
BEFORE UPDATE OR DELETE ON trading.order_intents
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER identity_idempotency_responses_are_immutable
BEFORE UPDATE OR DELETE ON identity.idempotency_responses
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

REVOKE ALL ON SCHEMA identity FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA identity FROM PUBLIC;

GRANT USAGE ON SCHEMA identity TO platformgo_api;
GRANT SELECT, INSERT ON identity.users TO platformgo_api;
GRANT SELECT, INSERT ON identity.user_accounts TO platformgo_api;
GRANT SELECT, INSERT ON identity.sessions TO platformgo_api;
GRANT UPDATE (revoked_at) ON identity.sessions TO platformgo_api;
GRANT SELECT, INSERT ON identity.idempotency_responses TO platformgo_api;
GRANT SELECT, INSERT ON trading.order_intents TO platformgo_api;
