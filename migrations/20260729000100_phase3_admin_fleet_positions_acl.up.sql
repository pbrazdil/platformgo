-- Bound catalog enumeration and lock acquisition. The migrator supplies the
-- shorter five-second lock_timeout and rolls back this ACL-only change with
-- its migration journal on any failure.
SET LOCAL statement_timeout = '10s';

-- Fence transactions that already executed position DML under a privilege
-- this migration may revoke. Without this fence, their statements could
-- commit after the ACL and migration journal publish a successful cutover.
LOCK TABLE trading.positions IN SHARE MODE;

DO $$
DECLARE
    relation_oid pg_catalog.oid :=
        'trading.positions'::pg_catalog.regclass::pg_catalog.oid;
    unexpected_grantee pg_catalog.name;
    column_name pg_catalog.name;
BEGIN
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
END
$$;

GRANT SELECT ON TABLE trading.positions TO platformgo_api;
GRANT SELECT, INSERT, UPDATE ON TABLE trading.positions TO platformgo_engine;
