-- Phase 3 broker mutation compatibility and least-privilege provisioning.
-- Lock/rewrite: new table and function only; no existing table rewrite.
-- Transaction: applied atomically by the migrator. Account provisioning runs
-- inside the caller transaction so its idempotency response and all account
-- ownership records commit or roll back together.
-- Compatibility: Phase 2 binaries ignore this additive surface. Phase 3
-- binaries require it before serving broker account provisioning.

CREATE TABLE identity.account_profiles (
    account_id text PRIMARY KEY
        REFERENCES identity.user_accounts(account_id),
    login bigint NOT NULL UNIQUE CHECK (login > 0),
    base_currency text NOT NULL CHECK (base_currency <> ''),
    market_venue text NOT NULL CHECK (market_venue <> ''),
    permitted_classes text[] NOT NULL CHECK (
        cardinality(permitted_classes) > 0
    ),
    created_at timestamptz NOT NULL
);

REVOKE ALL ON identity.account_profiles FROM PUBLIC;
GRANT SELECT ON identity.account_profiles TO platformgo_api;

CREATE FUNCTION identity.provision_broker_account(
    requested_account_id text,
    requested_user_id text,
    requested_login bigint,
    requested_base_currency text,
    requested_market_venue text,
    requested_permitted_classes text[],
    requested_created_at timestamptz
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    deployment_shard_id bigint;
BEGIN
    IF requested_account_id NOT LIKE 'urn:xb:account:%'
        OR requested_user_id NOT LIKE 'urn:xb:user:%'
        OR requested_login <= 0
        OR requested_base_currency = ''
        OR requested_market_venue = ''
        OR cardinality(requested_permitted_classes) = 0
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid broker account provisioning request';
    END IF;

    PERFORM 1
      FROM identity.users
     WHERE user_id = requested_user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'unknown broker account user';
    END IF;

    SELECT shard_id
      INTO STRICT deployment_shard_id
      FROM engine.deployment_shard;

    INSERT INTO trading.accounts (account_id, oms_mode)
    VALUES (requested_account_id, 'NETTING');

    INSERT INTO engine.account_shards (account_id, shard_id)
    VALUES (requested_account_id, deployment_shard_id);

    INSERT INTO identity.user_accounts (user_id, account_id, created_at)
    VALUES (requested_user_id, requested_account_id, requested_created_at);

    INSERT INTO identity.account_profiles (
        account_id,
        login,
        base_currency,
        market_venue,
        permitted_classes,
        created_at
    ) VALUES (
        requested_account_id,
        requested_login,
        requested_base_currency,
        requested_market_venue,
        requested_permitted_classes,
        requested_created_at
    );
END;
$$;

REVOKE ALL ON FUNCTION identity.provision_broker_account(
    text,
    text,
    bigint,
    text,
    text,
    text[],
    timestamptz
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.provision_broker_account(
    text,
    text,
    bigint,
    text,
    text,
    text[],
    timestamptz
) TO platformgo_api;
