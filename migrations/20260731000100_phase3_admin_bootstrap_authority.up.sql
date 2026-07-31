-- Terminal-only, exactly-once first-administrator RBAC bootstrap authority.
-- This migration seeds one immutable built-in role and policy, but no
-- administrator. Production HTTP/admin authentication remains uncomposed.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

DO $$
DECLARE
    relation_name text;
    relation_oid pg_catalog.oid;
    relation_owner pg_catalog.oid;
    relation_kind "char";
    relation_persistence "char";
    relation_row_security boolean;
    relation_force_row_security boolean;
    expected_columns text[];
    actual_columns text[];
    expected_constraints text[];
    actual_constraints text[];
BEGIN
    FOREACH relation_name IN ARRAY ARRAY[
        'identity.rbac_roles',
        'identity.rbac_role_parents',
        'identity.rbac_admin_roles',
        'identity.rbac_policies'
    ]
    LOOP
        relation_oid := pg_catalog.to_regclass(relation_name);
        IF relation_oid IS NULL THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'admin permission authority catalog is divergent';
        END IF;
        SELECT
            relation.relowner,
            relation.relkind,
            relation.relpersistence,
            relation.relrowsecurity,
            relation.relforcerowsecurity
          INTO
            relation_owner,
            relation_kind,
            relation_persistence,
            relation_row_security,
            relation_force_row_security
          FROM pg_catalog.pg_class AS relation
         WHERE relation.oid = relation_oid;

        SELECT pg_catalog.array_agg(
                   pg_catalog.format(
                       '%s:%s:%s:%s',
                       attribute.attname,
                       pg_catalog.format_type(
                           attribute.atttypid,
                           attribute.atttypmod
                       ),
                       attribute.attnotnull,
                       COALESCE(
                           pg_catalog.pg_get_expr(
                               default_value.adbin,
                               default_value.adrelid
                           ),
                           ''
                       )
                   )
                   ORDER BY attribute.attnum
               )
          INTO actual_columns
          FROM pg_catalog.pg_attribute AS attribute
          LEFT JOIN pg_catalog.pg_attrdef AS default_value
            ON default_value.adrelid = attribute.attrelid
           AND default_value.adnum = attribute.attnum
         WHERE attribute.attrelid = relation_oid
           AND attribute.attnum > 0
           AND NOT attribute.attisdropped;

        SELECT pg_catalog.array_agg(
                   pg_catalog.format(
                       '%s:%s',
                       constraint_row.conname,
                       pg_catalog.pg_get_constraintdef(
                           constraint_row.oid,
                           true
                       )
                   )
                   ORDER BY constraint_row.conname
               )
          INTO actual_constraints
          FROM pg_catalog.pg_constraint AS constraint_row
         WHERE constraint_row.conrelid = relation_oid;

        CASE relation_name
        WHEN 'identity.rbac_roles' THEN
            expected_columns := ARRAY[
                'role_id:uuid:t:',
                'name:text:t:',
                'builtin:boolean:t:false'
            ];
            expected_constraints := ARRAY[
                'rbac_roles_builtin_not_null:NOT NULL builtin',
                'rbac_roles_name_key:UNIQUE (name)',
                'rbac_roles_name_not_null:NOT NULL name',
                'rbac_roles_name_valid:CHECK (name = btrim(name) AND length(name) >= 1 AND length(name) <= 128)',
                'rbac_roles_pkey:PRIMARY KEY (role_id)',
                'rbac_roles_role_id_not_null:NOT NULL role_id'
            ];
        WHEN 'identity.rbac_role_parents' THEN
            expected_columns := ARRAY[
                'role_id:uuid:t:',
                'parent_id:uuid:t:'
            ];
            expected_constraints := ARRAY[
                'rbac_role_parent_not_self:CHECK (role_id <> parent_id)',
                'rbac_role_parents_parent_id_fkey:FOREIGN KEY (parent_id) REFERENCES identity.rbac_roles(role_id) ON DELETE CASCADE',
                'rbac_role_parents_parent_id_not_null:NOT NULL parent_id',
                'rbac_role_parents_pkey:PRIMARY KEY (role_id, parent_id)',
                'rbac_role_parents_role_id_fkey:FOREIGN KEY (role_id) REFERENCES identity.rbac_roles(role_id) ON DELETE CASCADE',
                'rbac_role_parents_role_id_not_null:NOT NULL role_id'
            ];
        WHEN 'identity.rbac_admin_roles' THEN
            expected_columns := ARRAY[
                'admin_subject:text:t:',
                'role_id:uuid:t:'
            ];
            expected_constraints := ARRAY[
                'rbac_admin_roles_admin_subject_not_null:NOT NULL admin_subject',
                'rbac_admin_roles_pkey:PRIMARY KEY (admin_subject, role_id)',
                'rbac_admin_roles_role_id_fkey:FOREIGN KEY (role_id) REFERENCES identity.rbac_roles(role_id) ON DELETE CASCADE',
                'rbac_admin_roles_role_id_not_null:NOT NULL role_id',
                'rbac_admin_subject_canonical:CHECK (admin_subject ~ ''^admin::urn:xb:admin:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$''::text)'
            ];
        WHEN 'identity.rbac_policies' THEN
            expected_columns := ARRAY[
                'role_id:uuid:t:',
                'resource:text:t:',
                'action:text:t:',
                'effect:text:t:'
            ];
            expected_constraints := ARRAY[
                'rbac_policies_action_not_null:NOT NULL action',
                'rbac_policies_effect_not_null:NOT NULL effect',
                'rbac_policies_pkey:PRIMARY KEY (role_id, resource, action)',
                'rbac_policies_resource_not_null:NOT NULL resource',
                'rbac_policies_role_id_fkey:FOREIGN KEY (role_id) REFERENCES identity.rbac_roles(role_id) ON DELETE CASCADE',
                'rbac_policies_role_id_not_null:NOT NULL role_id',
                'rbac_policy_action_valid:CHECK (action = ANY (ARRAY[''*''::text, ''read''::text, ''write''::text, ''create''::text, ''delete''::text]))',
                'rbac_policy_effect_valid:CHECK (effect = ANY (ARRAY[''allow''::text, ''deny''::text]))',
                'rbac_policy_resource_valid:CHECK (resource = ANY (ARRAY[''*''::text, ''diagnostics''::text, ''admins''::text, ''users''::text, ''roles''::text, ''api-keys''::text, ''schedules''::text, ''accounts''::text, ''orders''::text, ''instruments''::text, ''collections''::text, ''tenants''::text]))'
            ];
        END CASE;

        IF relation_owner <> current_user::pg_catalog.regrole::pg_catalog.oid
            OR relation_kind <> 'r'
            OR relation_persistence <> 'p'
            OR relation_row_security
            OR relation_force_row_security
            OR actual_columns IS DISTINCT FROM expected_columns
            OR actual_constraints IS DISTINCT FROM expected_constraints
            OR EXISTS (
                SELECT 1
                  FROM pg_catalog.pg_trigger AS trigger_row
                 WHERE trigger_row.tgrelid = relation_oid
                   AND NOT trigger_row.tgisinternal
            )
            OR EXISTS (
                SELECT 1
                  FROM pg_catalog.pg_rewrite AS rule
                 WHERE rule.ev_class = relation_oid
            )
        THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'admin permission authority catalog is divergent';
        END IF;
    END LOOP;

    IF NOT EXISTS (
        SELECT 1
          FROM pg_catalog.pg_proc AS procedure
          JOIN pg_catalog.pg_language AS language
            ON language.oid = procedure.prolang
         WHERE procedure.oid =
                   'identity.admin_has_permission(text,text,text)'::pg_catalog.regprocedure
           AND procedure.proowner =
                   current_user::pg_catalog.regrole::pg_catalog.oid
           AND language.lanname = 'sql'
           AND procedure.provolatile = 's'
           AND procedure.prosecdef
           AND NOT procedure.proisstrict
           AND NOT procedure.proleakproof
           AND NOT procedure.proretset
           AND procedure.prorettype = 'boolean'::pg_catalog.regtype
           AND procedure.prokind = 'f'
           AND procedure.proparallel = 'u'
           AND procedure.pronargs = 3
           AND pg_catalog.oidvectortypes(procedure.proargtypes) =
                   'text, text, text'
           AND procedure.proconfig =
                   ARRAY['search_path=pg_catalog']::text[]
           AND pg_catalog.sha256(
                   pg_catalog.convert_to(procedure.prosrc, 'UTF8')
               ) =
                   pg_catalog.decode(
                       'becb8103abb0127c1f3a94f8235d4eca49a4506c8f921209ab4d714f7c600df8',
                       'hex'
                   )
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin permission authority catalog is divergent';
    END IF;
END
$$;

DO $$
DECLARE
    bootstrap_role_oid pg_catalog.oid;
    bootstrap_role_can_login boolean;
    bootstrap_role_super boolean;
    bootstrap_role_create_db boolean;
    bootstrap_role_create_role boolean;
    bootstrap_role_replication boolean;
    bootstrap_role_bypass_rls boolean;
BEGIN
    SELECT
        role.oid,
        role.rolcanlogin,
        role.rolsuper,
        role.rolcreatedb,
        role.rolcreaterole,
        role.rolreplication,
        role.rolbypassrls
      INTO
        bootstrap_role_oid,
        bootstrap_role_can_login,
        bootstrap_role_super,
        bootstrap_role_create_db,
        bootstrap_role_create_role,
        bootstrap_role_replication,
        bootstrap_role_bypass_rls
      FROM pg_catalog.pg_roles
      AS role
     WHERE role.rolname = 'platformgo_admin_bootstrap';

    IF NOT FOUND
        OR bootstrap_role_can_login
        OR bootstrap_role_super
        OR bootstrap_role_create_db
        OR bootstrap_role_create_role
        OR bootstrap_role_replication
        OR bootstrap_role_bypass_rls
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_auth_members AS membership
             WHERE membership.member = bootstrap_role_oid
                OR membership.roleid = bootstrap_role_oid
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_shdepend AS dependency
             WHERE dependency.refclassid =
                       'pg_catalog.pg_authid'::pg_catalog.regclass
               AND dependency.refobjid = bootstrap_role_oid
               AND dependency.deptype IN ('a', 'o')
        )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '42501',
            MESSAGE =
                'required pre-provisioned admin bootstrap role is missing or unsafe';
    END IF;
END
$$;

LOCK TABLE identity.rbac_roles IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE identity.rbac_role_parents IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE identity.rbac_admin_roles IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE identity.rbac_policies IN SHARE ROW EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM identity.rbac_roles)
        OR EXISTS (SELECT 1 FROM identity.rbac_role_parents)
        OR EXISTS (SELECT 1 FROM identity.rbac_admin_roles)
        OR EXISTS (SELECT 1 FROM identity.rbac_policies)
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin RBAC graph is not empty at bootstrap cutover';
    END IF;
END
$$;

INSERT INTO identity.rbac_roles (role_id, name, builtin)
VALUES (
    '00000000-0000-4000-8000-000000000001',
    'platformgo-superadmin',
    true
);

INSERT INTO identity.rbac_policies (role_id, resource, action, effect)
VALUES (
    '00000000-0000-4000-8000-000000000001',
    '*',
    '*',
    'allow'
);

CREATE TABLE audit.admin_bootstrap_events (
    event_id uuid PRIMARY KEY,
    admin_sequence bigint NOT NULL UNIQUE CHECK (admin_sequence = 1),
    actor_login text NOT NULL CHECK (
        actor_login ~ '^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$'
    ),
    request_id text NOT NULL UNIQUE CHECK (
        request_id ~ '^[A-Za-z0-9._:-]{1,128}$'
    ),
    idempotency_key_hash bytea NOT NULL UNIQUE CHECK (
        pg_catalog.octet_length(idempotency_key_hash) = 32
    ),
    request_hash bytea NOT NULL CHECK (
        pg_catalog.octet_length(request_hash) = 32
    ),
    admin_subject text NOT NULL CHECK (
        admin_subject ~
        '^admin::urn:xb:admin:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    ),
    logical_time_text text NOT NULL CHECK (
        logical_time_text ~
        '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z$'
    ),
    occurred_at timestamptz NOT NULL,
    role_id uuid NOT NULL CHECK (
        role_id = '00000000-0000-4000-8000-000000000001'
    ),
    role_name text NOT NULL CHECK (role_name = 'platformgo-superadmin'),
    configuration_version bigint NOT NULL CHECK (configuration_version = 1),
    outcome text NOT NULL CHECK (outcome = 'success'),
    detail jsonb NOT NULL
);

CREATE TRIGGER admin_bootstrap_events_are_immutable
BEFORE UPDATE OR DELETE ON audit.admin_bootstrap_events
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER admin_bootstrap_events_reject_truncate
BEFORE TRUNCATE ON audit.admin_bootstrap_events
FOR EACH STATEMENT EXECUTE FUNCTION engine.reject_immutable_change();

CREATE FUNCTION identity.bootstrap_first_admin(
    requested_request_id text,
    requested_idempotency_key_hash bytea,
    requested_admin_subject text,
    requested_event_id uuid,
    requested_logical_time_text text
)
RETURNS TABLE (
    outcome text,
    admin_subject text,
    role_name text,
    configuration_version bigint,
    event_id uuid,
    logical_time_text text
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET lock_timeout = '5s'
SET statement_timeout = '10s'
AS $$
DECLARE
    bootstrap_role_id constant uuid :=
        '00000000-0000-4000-8000-000000000001';
    bootstrap_role_name constant text := 'platformgo-superadmin';
    bootstrap_configuration_version constant bigint := 1;
    authority_time timestamptz;
    computed_request_hash bytea;
    committed_event audit.admin_bootstrap_events%ROWTYPE;
    committed_event_found boolean;
    caller text := session_user;
    role_count bigint;
    parent_count bigint;
    assignment_count bigint;
    policy_count bigint;
BEGIN
    IF NOT pg_catalog.pg_has_role(
        caller,
        'platformgo_admin_bootstrap',
        'member'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '42501',
            MESSAGE = 'admin bootstrap caller is not authorized';
    END IF;
    IF caller !~ '^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$'
        OR requested_request_id !~
            '^[A-Za-z0-9._:-]{1,128}$'
        OR pg_catalog.octet_length(requested_idempotency_key_hash) <> 32
        OR requested_admin_subject !~
            '^admin::urn:xb:admin:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        OR requested_event_id IS NULL
        OR requested_logical_time_text !~
            '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z$'
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid admin bootstrap request';
    END IF;

    BEGIN
        authority_time := requested_logical_time_text::timestamptz;
    EXCEPTION WHEN datetime_field_overflow OR invalid_datetime_format THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid admin bootstrap logical time';
    END;
    IF pg_catalog.to_char(
        authority_time AT TIME ZONE 'UTC',
        'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
    ) <> requested_logical_time_text THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'noncanonical admin bootstrap logical time';
    END IF;

    computed_request_hash := pg_catalog.sha256(pg_catalog.convert_to(
        'platformgo.admin-bootstrap.request.v1' || E'\n' ||
        caller || E'\n' ||
        requested_request_id || E'\n' ||
        requested_admin_subject || E'\n' ||
        requested_event_id::text || E'\n' ||
        requested_logical_time_text || E'\n' ||
        bootstrap_role_id::text || E'\n' ||
        bootstrap_role_name || E'\n' ||
        bootstrap_configuration_version::text || E'\n' ||
        '*' || E'\n' ||
        '*' || E'\n' ||
        'allow' || E'\n',
        'UTF8'
    ));

    -- Shared migration lock first, then the fixed bootstrap singleton lock,
    -- then the RBAC relations in migration order.
    PERFORM pg_catalog.pg_advisory_xact_lock_shared(88288443778895);
    PERFORM pg_catalog.pg_advisory_xact_lock(88288443778896);
    LOCK TABLE identity.rbac_roles IN SHARE ROW EXCLUSIVE MODE;
    LOCK TABLE identity.rbac_role_parents IN SHARE ROW EXCLUSIVE MODE;
    LOCK TABLE identity.rbac_admin_roles IN SHARE ROW EXCLUSIVE MODE;
    LOCK TABLE identity.rbac_policies IN SHARE ROW EXCLUSIVE MODE;
    LOCK TABLE audit.admin_bootstrap_events IN SHARE ROW EXCLUSIVE MODE;

    SELECT
        event.event_id,
        event.admin_sequence,
        event.actor_login,
        event.request_id,
        event.idempotency_key_hash,
        event.request_hash,
        event.admin_subject,
        event.logical_time_text,
        event.occurred_at,
        event.role_id,
        event.role_name,
        event.configuration_version,
        event.outcome,
        event.detail
      INTO committed_event
      FROM audit.admin_bootstrap_events AS event
     WHERE event.admin_sequence = 1;
    committed_event_found := FOUND;

    SELECT pg_catalog.count(*)
      INTO role_count
      FROM identity.rbac_roles AS role
     WHERE role.role_id OPERATOR(pg_catalog.=) bootstrap_role_id
       AND role.name OPERATOR(pg_catalog.=) bootstrap_role_name
       AND role.builtin;
    SELECT pg_catalog.count(*) INTO parent_count
      FROM identity.rbac_role_parents;
    SELECT pg_catalog.count(*)
      INTO policy_count
      FROM identity.rbac_policies AS policy
     WHERE policy.role_id OPERATOR(pg_catalog.=) bootstrap_role_id
       AND policy.resource OPERATOR(pg_catalog.=) '*'
       AND policy.action OPERATOR(pg_catalog.=) '*'
       AND policy.effect OPERATOR(pg_catalog.=) 'allow';

    IF committed_event_found THEN
        SELECT pg_catalog.count(*)
          INTO assignment_count
          FROM identity.rbac_admin_roles AS assignment
         WHERE assignment.admin_subject
                   OPERATOR(pg_catalog.=) committed_event.admin_subject
           AND assignment.role_id
                   OPERATOR(pg_catalog.=) bootstrap_role_id;
        IF role_count <> 1
            OR (SELECT pg_catalog.count(*) FROM identity.rbac_roles) <> 1
            OR parent_count <> 0
            OR assignment_count <> 1
            OR (
                SELECT pg_catalog.count(*)
                  FROM identity.rbac_admin_roles
            ) <> 1
            OR policy_count <> 1
            OR (
                SELECT pg_catalog.count(*) FROM identity.rbac_policies
            ) <> 1
            OR committed_event.role_id
                   OPERATOR(pg_catalog.!=) bootstrap_role_id
            OR committed_event.role_name
                   OPERATOR(pg_catalog.!=) bootstrap_role_name
            OR committed_event.configuration_version
                   OPERATOR(pg_catalog.!=) bootstrap_configuration_version
        THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'admin bootstrap authority is divergent';
        END IF;

        IF committed_event.idempotency_key_hash
                OPERATOR(pg_catalog.=) requested_idempotency_key_hash
            AND committed_event.request_hash
                OPERATOR(pg_catalog.=) computed_request_hash
            AND committed_event.actor_login OPERATOR(pg_catalog.=) caller
            AND committed_event.request_id
                OPERATOR(pg_catalog.=) requested_request_id
            AND committed_event.admin_subject
                OPERATOR(pg_catalog.=) requested_admin_subject
            AND committed_event.event_id
                OPERATOR(pg_catalog.=) requested_event_id
            AND committed_event.logical_time_text
                OPERATOR(pg_catalog.=) requested_logical_time_text
        THEN
            RETURN QUERY SELECT
                'replayed'::text,
                committed_event.admin_subject,
                committed_event.role_name,
                committed_event.configuration_version,
                committed_event.event_id,
                committed_event.logical_time_text;
            RETURN;
        END IF;
        IF committed_event.idempotency_key_hash
                OPERATOR(pg_catalog.=) requested_idempotency_key_hash
            OR committed_event.request_id
                OPERATOR(pg_catalog.=) requested_request_id
            OR committed_event.event_id
                OPERATOR(pg_catalog.=) requested_event_id
        THEN
            RAISE EXCEPTION USING
                ERRCODE = '22000',
                MESSAGE = 'admin bootstrap idempotency conflict';
        END IF;
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'administrator is already bootstrapped';
    END IF;

    SELECT pg_catalog.count(*) INTO assignment_count
      FROM identity.rbac_admin_roles;

    IF role_count <> 1
        OR (SELECT pg_catalog.count(*) FROM identity.rbac_roles) <> 1
        OR parent_count <> 0
        OR assignment_count <> 0
        OR policy_count <> 1
        OR (SELECT pg_catalog.count(*) FROM identity.rbac_policies) <> 1
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority is divergent';
    END IF;

    INSERT INTO identity.rbac_admin_roles (admin_subject, role_id)
    VALUES (requested_admin_subject, bootstrap_role_id);

    INSERT INTO audit.admin_bootstrap_events (
        event_id,
        admin_sequence,
        actor_login,
        request_id,
        idempotency_key_hash,
        request_hash,
        admin_subject,
        logical_time_text,
        occurred_at,
        role_id,
        role_name,
        configuration_version,
        outcome,
        detail
    ) VALUES (
        requested_event_id,
        1,
        caller,
        requested_request_id,
        requested_idempotency_key_hash,
        computed_request_hash,
        requested_admin_subject,
        requested_logical_time_text,
        authority_time,
        bootstrap_role_id,
        bootstrap_role_name,
        bootstrap_configuration_version,
        'success',
        pg_catalog.jsonb_build_object(
            'after',
            pg_catalog.jsonb_build_object(
                'adminSubject', requested_admin_subject,
                'roleName', bootstrap_role_name,
                'configurationVersion', bootstrap_configuration_version
            ),
            'before',
            NULL,
            'operationVersion',
            'platformgo.admin-bootstrap.request.v1',
            'policy',
            pg_catalog.jsonb_build_object(
                'resource', '*',
                'action', '*',
                'effect', 'allow'
            )
        )
    );

    RETURN QUERY SELECT
        'created'::text,
        requested_admin_subject,
        bootstrap_role_name,
        bootstrap_configuration_version,
        requested_event_id,
        requested_logical_time_text;
END
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
        'identity.rbac_policies',
        'audit.admin_bootstrap_events'
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
             ORDER BY attribute.attname, privilege.privilege_type
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
    function_oid pg_catalog.oid;
    function_name text;
    unexpected_grantee pg_catalog.name;
BEGIN
    FOREACH function_name IN ARRAY ARRAY[
        'identity.admin_has_permission(text,text,text)',
        'identity.bootstrap_first_admin(text,bytea,text,uuid,text)'
    ]
    LOOP
        function_oid :=
            function_name::pg_catalog.regprocedure::pg_catalog.oid;
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
    END LOOP;
END
$$;

GRANT EXECUTE ON FUNCTION identity.admin_has_permission(
    text,
    text,
    text
) TO platformgo_api;

GRANT USAGE ON SCHEMA identity TO platformgo_admin_bootstrap;
GRANT EXECUTE ON FUNCTION identity.bootstrap_first_admin(
    text,
    bytea,
    text,
    uuid,
    text
) TO platformgo_admin_bootstrap;
