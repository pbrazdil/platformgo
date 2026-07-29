-- Bound catalog enumeration and lock acquisition. The migrator supplies the
-- shorter five-second lock_timeout and rolls back this ACL/catalog-only change
-- with its migration journal on any failure.
SET LOCAL statement_timeout = '10s';

-- Fence transactions that already executed lifecycle DML under a privilege
-- this migration may revoke. This deterministic order prevents a paused shard
-- admission from inverting the later provisioning-intent lock.
LOCK TABLE trading.accounts IN SHARE MODE;
LOCK TABLE engine.account_shards IN SHARE MODE;
LOCK TABLE identity.account_provisioning_intents IN SHARE MODE;

DO $$
DECLARE
    relation_oid pg_catalog.oid;
    relation_oids pg_catalog.oid[] := ARRAY[
        'trading.accounts'::pg_catalog.regclass::pg_catalog.oid,
        'engine.account_shards'::pg_catalog.regclass::pg_catalog.oid,
        'identity.account_provisioning_intents'::pg_catalog.regclass::pg_catalog.oid
    ];
    unexpected_grantee pg_catalog.name;
    column_name pg_catalog.name;
BEGIN
    FOREACH relation_oid IN ARRAY relation_oids
    LOOP
        EXECUTE pg_catalog.format(
            'REVOKE ALL PRIVILEGES ON TABLE %s FROM PUBLIC CASCADE',
            relation_oid::pg_catalog.regclass
        );

        FOR unexpected_grantee IN
            SELECT DISTINCT role.rolname
              FROM pg_catalog.pg_class AS relation
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      relation.relacl,
                      pg_catalog.acldefault('r', relation.relowner)
                  )
              ) AS privilege
              JOIN pg_catalog.pg_roles AS role
                ON role.oid = privilege.grantee
             WHERE relation.oid = relation_oid
               AND role.oid <> relation.relowner
             ORDER BY role.rolname
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL PRIVILEGES ON TABLE %s FROM %I CASCADE',
                relation_oid::pg_catalog.regclass,
                unexpected_grantee
            );
        END LOOP;

        FOR column_name IN
            SELECT attribute.attname
              FROM pg_catalog.pg_attribute AS attribute
             WHERE attribute.attrelid = relation_oid
               AND attribute.attnum > 0
               AND NOT attribute.attisdropped
             ORDER BY attribute.attnum
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL PRIVILEGES (%I) ON TABLE %s FROM PUBLIC CASCADE',
                column_name,
                relation_oid::pg_catalog.regclass
            );
        END LOOP;

        FOR unexpected_grantee, column_name IN
            SELECT DISTINCT role.rolname, attribute.attname
              FROM pg_catalog.pg_attribute AS attribute
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  attribute.attacl
              ) AS privilege
              JOIN pg_catalog.pg_roles AS role
                ON role.oid = privilege.grantee
              JOIN pg_catalog.pg_class AS relation
                ON relation.oid = attribute.attrelid
             WHERE attribute.attrelid = relation_oid
               AND attribute.attnum > 0
               AND NOT attribute.attisdropped
               AND attribute.attacl IS NOT NULL
               AND role.oid <> relation.relowner
             ORDER BY role.rolname, attribute.attname
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL PRIVILEGES (%I) ON TABLE %s FROM %I CASCADE',
                column_name,
                relation_oid::pg_catalog.regclass,
                unexpected_grantee
            );
        END LOOP;
    END LOOP;
END
$$;

GRANT SELECT ON TABLE trading.accounts TO platformgo_api;
GRANT SELECT, INSERT, UPDATE ON TABLE trading.accounts TO platformgo_engine;
GRANT SELECT, INSERT ON TABLE engine.account_shards TO platformgo_api;
GRANT SELECT ON TABLE engine.account_shards TO platformgo_engine;
GRANT SELECT, INSERT ON TABLE identity.account_provisioning_intents
TO platformgo_api;
GRANT SELECT ON TABLE identity.account_provisioning_intents
TO platformgo_engine;

CREATE OR REPLACE FUNCTION trading.admin_risk_state_exists()
RETURNS boolean
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT
        EXISTS (SELECT 1 FROM trading.accounts)
        OR EXISTS (SELECT 1 FROM trading.commands)
        OR EXISTS (SELECT 1 FROM engine.account_shards)
        OR EXISTS (SELECT 1 FROM ledger.balances)
        OR EXISTS (SELECT 1 FROM ledger.transactions)
        OR EXISTS (SELECT 1 FROM ledger.entries)
$$;

-- CREATE OR REPLACE preserves a pre-existing same-signature function owner.
-- Transfer authority inside this transaction before publishing any runtime
-- EXECUTE grant so an unexpected prior owner cannot retain definer control.
ALTER FUNCTION trading.admin_risk_state_exists() OWNER TO CURRENT_USER;

DO $$
DECLARE
    function_oid pg_catalog.oid :=
        'trading.admin_risk_state_exists()'::pg_catalog.regprocedure::pg_catalog.oid;
    unexpected_grantee pg_catalog.name;
BEGIN
    EXECUTE pg_catalog.format(
        'REVOKE ALL PRIVILEGES ON FUNCTION %s FROM PUBLIC CASCADE',
        function_oid::pg_catalog.regprocedure
    );

    FOR unexpected_grantee IN
        SELECT DISTINCT role.rolname
          FROM pg_catalog.pg_proc AS procedure
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              COALESCE(
                  procedure.proacl,
                  pg_catalog.acldefault('f', procedure.proowner)
              )
          ) AS privilege
          JOIN pg_catalog.pg_roles AS role
            ON role.oid = privilege.grantee
         WHERE procedure.oid = function_oid
           AND role.oid <> procedure.proowner
         ORDER BY role.rolname
    LOOP
        EXECUTE pg_catalog.format(
            'REVOKE ALL PRIVILEGES ON FUNCTION %s FROM %I CASCADE',
            function_oid::pg_catalog.regprocedure,
            unexpected_grantee
        );
    END LOOP;
END
$$;

GRANT EXECUTE ON FUNCTION trading.admin_risk_state_exists()
TO platformgo_api;
