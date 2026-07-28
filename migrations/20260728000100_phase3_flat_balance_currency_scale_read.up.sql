DO $$
DECLARE
    relation_oid oid := 'trading.currency_scales'::pg_catalog.regclass::oid;
    unexpected_grantee name;
    column_name name;
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

GRANT SELECT ON TABLE trading.currency_scales
TO platformgo_api, platformgo_engine;
