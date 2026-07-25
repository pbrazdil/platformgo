-- Forward correction for the tenant-scoped broker-user convergence function.
-- Lock/rewrite: replaces one function only; no table lock or rewrite.
-- Transaction/failure: atomic with the migration checksum and retry-safe.

CREATE OR REPLACE FUNCTION identity.create_broker_user(
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
          AND normalized_email IS NOT NULL
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
