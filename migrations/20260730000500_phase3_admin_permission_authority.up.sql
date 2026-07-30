SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10s';

CREATE TABLE identity.rbac_roles (
    role_id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    builtin boolean NOT NULL DEFAULT false,
    CONSTRAINT rbac_roles_name_valid CHECK (
        name = btrim(name)
        AND length(name) BETWEEN 1 AND 128
    )
);

CREATE TABLE identity.rbac_role_parents (
    role_id uuid NOT NULL REFERENCES identity.rbac_roles(role_id)
        ON DELETE CASCADE,
    parent_id uuid NOT NULL REFERENCES identity.rbac_roles(role_id)
        ON DELETE CASCADE,
    PRIMARY KEY (role_id, parent_id),
    CONSTRAINT rbac_role_parent_not_self CHECK (role_id <> parent_id)
);

CREATE TABLE identity.rbac_admin_roles (
    admin_subject text NOT NULL,
    role_id uuid NOT NULL REFERENCES identity.rbac_roles(role_id)
        ON DELETE CASCADE,
    PRIMARY KEY (admin_subject, role_id),
    CONSTRAINT rbac_admin_subject_canonical CHECK (
        admin_subject ~
        '^admin::urn:xb:admin:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    )
);

CREATE TABLE identity.rbac_policies (
    role_id uuid NOT NULL REFERENCES identity.rbac_roles(role_id)
        ON DELETE CASCADE,
    resource text NOT NULL,
    action text NOT NULL,
    effect text NOT NULL,
    PRIMARY KEY (role_id, resource, action),
    CONSTRAINT rbac_policy_resource_valid CHECK (
        resource IN (
            '*',
            'diagnostics',
            'admins',
            'users',
            'roles',
            'api-keys',
            'schedules',
            'accounts',
            'orders',
            'instruments',
            'collections',
            'tenants'
        )
    ),
    CONSTRAINT rbac_policy_action_valid CHECK (
        action IN ('*', 'read', 'write', 'create', 'delete')
    ),
    CONSTRAINT rbac_policy_effect_valid CHECK (
        effect IN ('allow', 'deny')
    )
);

CREATE FUNCTION identity.admin_has_permission(
    requested_subject text,
    requested_resource text,
    requested_action text
)
RETURNS boolean
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    WITH RECURSIVE effective_roles(role_id) AS (
        SELECT assignment.role_id
          FROM identity.rbac_admin_roles AS assignment
         WHERE assignment.admin_subject
               OPERATOR(pg_catalog.=) requested_subject

        UNION

        SELECT parent.parent_id
          FROM effective_roles AS child
          JOIN identity.rbac_role_parents AS parent
            ON parent.role_id OPERATOR(pg_catalog.=) child.role_id
    ),
    matching_policies AS (
        SELECT policy.effect
          FROM effective_roles AS role
          JOIN identity.rbac_policies AS policy
            ON policy.role_id OPERATOR(pg_catalog.=) role.role_id
         WHERE (
                policy.resource OPERATOR(pg_catalog.=) '*'
                OR policy.resource OPERATOR(pg_catalog.=) requested_resource
           )
           AND (
                policy.action OPERATOR(pg_catalog.=) '*'
                OR policy.action OPERATOR(pg_catalog.=) requested_action
           )
    )
    SELECT
        COALESCE(
            pg_catalog.bool_or(effect OPERATOR(pg_catalog.=) 'allow'),
            false
        )
        AND NOT COALESCE(
            pg_catalog.bool_or(effect OPERATOR(pg_catalog.=) 'deny'),
            false
        )
      FROM matching_policies
     WHERE requested_subject OPERATOR(pg_catalog.~)
               '^admin::urn:xb:admin:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
       AND requested_resource OPERATOR(pg_catalog.=) ANY (
            ARRAY[
                'diagnostics',
                'admins',
                'users',
                'roles',
                'api-keys',
                'schedules',
                'accounts',
                'orders',
                'instruments',
                'collections',
                'tenants'
            ]::text[]
       )
       AND requested_action OPERATOR(pg_catalog.=) ANY (
            ARRAY['read', 'write', 'create', 'delete']::text[]
       )
$$;

DO $$
DECLARE
    relation_oid pg_catalog.oid;
    relation_name text;
    column_acl record;
    unexpected_grantee pg_catalog.name;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY[
        'identity.rbac_roles',
        'identity.rbac_role_parents',
        'identity.rbac_admin_roles',
        'identity.rbac_policies'
    ]
    LOOP
        relation_oid := relation_name::pg_catalog.regclass::pg_catalog.oid;
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

        FOR column_acl IN
            SELECT
                attribute.attname,
                privilege.privilege_type
              FROM pg_catalog.pg_attribute AS attribute
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  attribute.attacl
              ) AS privilege
             WHERE attribute.attrelid = relation_oid
               AND attribute.attnum > 0
               AND NOT attribute.attisdropped
               AND attribute.attacl IS NOT NULL
               AND privilege.grantee = 0
             ORDER BY
                attribute.attname,
                privilege.privilege_type
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE %s (%I) ON TABLE %s FROM PUBLIC CASCADE',
                column_acl.privilege_type,
                column_acl.attname,
                relation_oid::pg_catalog.regclass
            );
        END LOOP;

        FOR column_acl IN
            SELECT
                attribute.attname,
                privilege.privilege_type,
                role.rolname
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
             ORDER BY
                attribute.attname,
                privilege.privilege_type,
                role.rolname
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE %s (%I) ON TABLE %s FROM %I CASCADE',
                column_acl.privilege_type,
                column_acl.attname,
                relation_oid::pg_catalog.regclass,
                column_acl.rolname
            );
        END LOOP;
    END LOOP;
END
$$;

DO $$
DECLARE
    function_oid constant pg_catalog.oid :=
        'identity.admin_has_permission(text,text,text)'
            ::pg_catalog.regprocedure::pg_catalog.oid;
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

GRANT EXECUTE ON FUNCTION identity.admin_has_permission(
    text,
    text,
    text
) TO platformgo_api;
