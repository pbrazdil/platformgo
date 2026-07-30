-- Repair broker-balance tenant and money-read authority inherited from hostile
-- migrator-owner defaults. This catalog-only cutover changes no rows or
-- relation storage. The migrator supplies a shorter five-second lock_timeout;
-- any failure rolls back these ACL changes and the migration journal together.
SET LOCAL statement_timeout = '10s';

-- Fence transactions that already wrote under a privilege being revoked.
-- Keep this order aligned with production account provisioning followed by
-- balance persistence. Acquire every lock before changing any ACL.
LOCK TABLE identity.user_accounts IN SHARE MODE;
LOCK TABLE identity.account_profiles IN SHARE MODE;
LOCK TABLE ledger.balances IN SHARE MODE;

DO $$
DECLARE
    relation_oid pg_catalog.oid;
    relation_oids pg_catalog.oid[] := ARRAY[
        'identity.user_accounts'::pg_catalog.regclass::pg_catalog.oid,
        'identity.account_profiles'::pg_catalog.regclass::pg_catalog.oid,
        'ledger.balances'::pg_catalog.regclass::pg_catalog.oid
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

GRANT SELECT ON TABLE identity.user_accounts TO platformgo_api;
GRANT SELECT, INSERT ON TABLE identity.user_accounts TO platformgo_engine;

GRANT SELECT ON TABLE identity.account_profiles TO platformgo_api;
GRANT INSERT ON TABLE identity.account_profiles TO platformgo_engine;

GRANT SELECT ON TABLE ledger.balances TO platformgo_api;
GRANT SELECT, INSERT, UPDATE ON TABLE ledger.balances TO platformgo_engine;
