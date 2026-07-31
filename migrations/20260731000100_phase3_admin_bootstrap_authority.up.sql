-- Terminal-only, exactly-once first-administrator RBAC bootstrap authority.
-- This migration seeds one immutable built-in role and policy, but no
-- administrator. Production HTTP/admin authentication remains uncomposed.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_catalog.pg_roles AS role
         WHERE role.rolname = current_user
           AND role.rolsuper
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '42501',
            MESSAGE =
                'admin bootstrap migration requires a superuser catalog fence';
    END IF;
END
$$;

-- DDL and ACL statements below also acquire PostgreSQL object locks. Obtain
-- every existing schema and function object lock before any catalog fence, in
-- one fixed order. A bounded wait here holds no protected catalog lock, so a
-- concurrent multi-object DDL statement can continue its catalog work without
-- forming an object-lock -> catalog-lock inverse cycle.
SET LOCAL deadlock_timeout = '2s';
SET LOCAL lock_timeout = '1s';

DO $$
DECLARE
    locked_classid pg_catalog.oid;
    locked_objid pg_catalog.oid;
BEGIN
    SELECT address.classid, address.objid
      INTO locked_classid, locked_objid
      FROM pg_catalog.pg_get_object_address(
               'schema',
               ARRAY['audit'],
               ARRAY[]::text[]
           ) AS address;
    IF locked_classid IS NULL OR locked_objid IS NULL OR locked_objid = 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'admin bootstrap authority object catalog is divergent';
    END IF;

    SELECT address.classid, address.objid
      INTO locked_classid, locked_objid
      FROM pg_catalog.pg_get_object_address(
               'schema',
               ARRAY['identity'],
               ARRAY[]::text[]
           ) AS address;
    IF locked_classid IS NULL OR locked_objid IS NULL OR locked_objid = 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'admin bootstrap authority object catalog is divergent';
    END IF;

    SELECT address.classid, address.objid
      INTO locked_classid, locked_objid
      FROM pg_catalog.pg_get_object_address(
               'function',
               ARRAY['engine', 'reject_immutable_change'],
               ARRAY[]::text[]
           ) AS address;
    IF locked_classid IS NULL OR locked_objid IS NULL OR locked_objid = 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'admin bootstrap authority object catalog is divergent';
    END IF;

    SELECT address.classid, address.objid
      INTO locked_classid, locked_objid
      FROM pg_catalog.pg_get_object_address(
               'function',
               ARRAY['identity', 'admin_has_permission'],
               ARRAY['text', 'text', 'text']
           ) AS address;
    IF locked_classid IS NULL OR locked_objid IS NULL OR locked_objid = 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'admin bootstrap authority object catalog is divergent';
    END IF;
EXCEPTION
    WHEN invalid_schema_name OR undefined_function OR wrong_object_type THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'admin bootstrap authority object catalog is divergent';
END
$$;

SET LOCAL lock_timeout = '5s';

-- Global role, membership, namespace/function ACL, dependency, and
-- default-privilege facts are shared outside the four RBAC relations. A
-- bounded SHARE fence rejects any concurrent catalog writer before the exact
-- preflight obtains its snapshot, then retains the accepted authority through
-- commit. PostgreSQL maintenance and DDL use incompatible catalog lock orders,
-- so never wait while retaining a partial catalog fence.
LOCK TABLE pg_catalog.pg_default_acl IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_shdepend IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_attribute IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_proc IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_authid IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_auth_members IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_inherits IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_namespace IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_event_trigger IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_class IN SHARE MODE NOWAIT;

DO $$
BEGIN
    PERFORM 1
      FROM pg_catalog.pg_event_trigger AS event_trigger
     ORDER BY event_trigger.oid
     FOR UPDATE NOWAIT;

    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_event_trigger AS event_trigger
         WHERE event_trigger.evtenabled <> 'D'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'enabled event triggers are forbidden during admin bootstrap migration';
    END IF;
END
$$;

DO $$
DECLARE
    required_role text;
BEGIN
    FOREACH required_role IN ARRAY ARRAY[
        'platformgo_api',
        'platformgo_engine',
        'platformgo_outbox',
        'platformgo_projector',
        'platformgo_realtime',
        'platformgo_realtime_repair'
    ]
    LOOP
        IF NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_roles AS role
             WHERE role.rolname = required_role
               AND NOT role.rolcanlogin
               AND NOT role.rolsuper
               AND NOT role.rolcreatedb
               AND NOT role.rolcreaterole
               AND NOT role.rolreplication
               AND NOT role.rolbypassrls
               AND NOT EXISTS (
                   SELECT 1
                     FROM pg_catalog.pg_auth_members AS membership
                    WHERE membership.member = role.oid
               )
        ) THEN
            RAISE EXCEPTION USING
                ERRCODE = '42501',
                MESSAGE = pg_catalog.format(
                    'required pre-provisioned runtime role %s is missing or unsafe',
                    required_role
                );
        END IF;
    END LOOP;
END
$$;

DO $$
DECLARE
    locked_function_count bigint;
BEGIN
    PERFORM 1
      FROM pg_catalog.pg_proc AS procedure
     WHERE procedure.oid IN (
               pg_catalog.to_regprocedure(
                   'identity.admin_has_permission(text,text,text)'
               ),
               pg_catalog.to_regprocedure(
                   'engine.reject_immutable_change()'
               )
           )
     ORDER BY procedure.oid
     FOR UPDATE NOWAIT;
    GET DIAGNOSTICS locked_function_count = ROW_COUNT;
    IF locked_function_count <> 2 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin permission authority catalog is divergent';
    END IF;
END
$$;

DO $$
DECLARE
    locked_row_count bigint;
    migration_owner_oid pg_catalog.oid;
BEGIN
    SELECT role.oid
      INTO migration_owner_oid
      FROM pg_catalog.pg_authid AS role
     WHERE role.rolname::text OPERATOR(pg_catalog.=) current_user;
    IF migration_owner_oid IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap migration owner is divergent';
    END IF;

    PERFORM 1
      FROM pg_catalog.pg_namespace AS namespace
     WHERE namespace.oid IN (
               'engine'::pg_catalog.regnamespace,
               'identity'::pg_catalog.regnamespace,
               'audit'::pg_catalog.regnamespace
           )
     ORDER BY namespace.oid
     FOR UPDATE NOWAIT;
    GET DIAGNOSTICS locked_row_count = ROW_COUNT;
    IF locked_row_count <> 3
        OR EXISTS (
            SELECT 1
             FROM pg_catalog.pg_namespace AS namespace
             WHERE namespace.nspname IN ('engine', 'identity', 'audit')
               AND namespace.nspowner <> migration_owner_oid
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_namespace AS namespace
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      namespace.nspacl,
                      pg_catalog.acldefault('n', namespace.nspowner)
                  )
              ) AS privilege
             WHERE namespace.nspname IN ('engine', 'identity', 'audit')
               AND privilege.grantee <> namespace.nspowner
               AND privilege.privilege_type = 'CREATE'
        )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority catalog is divergent';
    END IF;

    PERFORM 1
      FROM pg_catalog.pg_class AS relation
     WHERE relation.oid IN (
               'engine.schema_migrations'::pg_catalog.regclass,
               'identity.rbac_roles'::pg_catalog.regclass,
               'identity.rbac_role_parents'::pg_catalog.regclass,
               'identity.rbac_admin_roles'::pg_catalog.regclass,
               'identity.rbac_policies'::pg_catalog.regclass
           )
     ORDER BY relation.oid
     FOR UPDATE NOWAIT;
    GET DIAGNOSTICS locked_row_count = ROW_COUNT;
    IF locked_row_count <> 5 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority catalog is divergent';
    END IF;

    PERFORM 1
      FROM pg_catalog.pg_attribute AS attribute
     WHERE attribute.attrelid IN (
               'engine.schema_migrations'::pg_catalog.regclass,
               'identity.rbac_roles'::pg_catalog.regclass,
               'identity.rbac_role_parents'::pg_catalog.regclass,
               'identity.rbac_admin_roles'::pg_catalog.regclass,
               'identity.rbac_policies'::pg_catalog.regclass
           )
       AND attribute.attnum > 0
       AND NOT attribute.attisdropped
     ORDER BY attribute.attrelid, attribute.attnum
     FOR UPDATE NOWAIT;
    GET DIAGNOSTICS locked_row_count = ROW_COUNT;
    IF locked_row_count <> 14 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority catalog is divergent';
    END IF;
END
$$;

LOCK TABLE engine.schema_migrations IN SHARE ROW EXCLUSIVE MODE NOWAIT;

DO $$
DECLARE
    relation_oid constant pg_catalog.oid :=
        'engine.schema_migrations'::pg_catalog.regclass;
    migration_owner_oid pg_catalog.oid;
    actual_columns text[];
    actual_constraints text[];
    actual_indexes text[];
BEGIN
    SELECT role.oid
      INTO migration_owner_oid
      FROM pg_catalog.pg_authid AS role
     WHERE role.rolname::text OPERATOR(pg_catalog.=) current_user;
    IF migration_owner_oid IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap migration owner is divergent';
    END IF;

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

    SELECT pg_catalog.array_agg(
               pg_catalog.pg_get_indexdef(index_row.indexrelid)
               ORDER BY index_relation.relname
           )
      INTO actual_indexes
      FROM pg_catalog.pg_index AS index_row
      JOIN pg_catalog.pg_class AS index_relation
        ON index_relation.oid = index_row.indexrelid
     WHERE index_row.indrelid = relation_oid;

    IF NOT EXISTS (
        SELECT 1
         FROM pg_catalog.pg_class AS relation
         WHERE relation.oid = relation_oid
           AND relation.relowner = migration_owner_oid
           AND relation.relkind = 'r'
           AND relation.relpersistence = 'p'
           AND NOT relation.relrowsecurity
           AND NOT relation.relforcerowsecurity
           AND NOT relation.relispartition
    )
        OR actual_columns IS DISTINCT FROM ARRAY[
            'filename:text:t:',
            'checksum:bytea:t:',
            'applied_at:timestamp with time zone:t:clock_timestamp()'
        ]::text[]
        OR actual_constraints IS DISTINCT FROM ARRAY[
            'schema_migrations_applied_at_not_null:NOT NULL applied_at',
            'schema_migrations_checksum_check:CHECK (octet_length(checksum) = 32)',
            'schema_migrations_checksum_not_null:NOT NULL checksum',
            'schema_migrations_filename_not_null:NOT NULL filename',
            'schema_migrations_pkey:PRIMARY KEY (filename)'
        ]::text[]
        OR actual_indexes IS DISTINCT FROM ARRAY[
            'CREATE UNIQUE INDEX schema_migrations_pkey ON engine.schema_migrations USING btree (filename)'
        ]::text[]
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_trigger AS trigger_row
             WHERE trigger_row.tgrelid = relation_oid
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_inherits AS inheritance
             WHERE inheritance.inhrelid = relation_oid
                OR inheritance.inhparent = relation_oid
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_rewrite AS rule
             WHERE rule.ev_class = relation_oid
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_class AS relation
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      relation.relacl,
                      pg_catalog.acldefault('r', relation.relowner)
                  )
              ) AS privilege
             WHERE relation.oid = relation_oid
               AND privilege.grantee <> relation.relowner
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_attribute AS attribute
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  attribute.attacl
              ) AS privilege
             WHERE attribute.attrelid = relation_oid
               AND attribute.attnum > 0
               AND NOT attribute.attisdropped
               AND privilege.grantee <> migration_owner_oid
        )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'migration journal catalog is divergent';
    END IF;
END
$$;

DO $$
BEGIN
    EXECUTE
        'LOCK TABLE identity.rbac_roles IN SHARE ROW EXCLUSIVE MODE NOWAIT';
    EXECUTE
        'LOCK TABLE identity.rbac_role_parents IN SHARE ROW EXCLUSIVE MODE NOWAIT';
    EXECUTE
        'LOCK TABLE identity.rbac_admin_roles IN SHARE ROW EXCLUSIVE MODE NOWAIT';
    EXECUTE
        'LOCK TABLE identity.rbac_policies IN SHARE ROW EXCLUSIVE MODE NOWAIT';
EXCEPTION
    WHEN undefined_table THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin permission authority catalog is divergent';
END
$$;

DO $$
DECLARE
    migration_owner_oid pg_catalog.oid;
    relation_name text;
    relation_oid pg_catalog.oid;
    relation_owner pg_catalog.oid;
    relation_kind "char";
    relation_persistence "char";
    relation_row_security boolean;
    relation_force_row_security boolean;
    relation_is_partition boolean;
    expected_columns text[];
    actual_columns text[];
    expected_constraints text[];
    actual_constraints text[];
    expected_indexes text[];
    actual_indexes text[];
    expected_internal_trigger_count bigint;
BEGIN
    SELECT role.oid
      INTO migration_owner_oid
      FROM pg_catalog.pg_authid AS role
     WHERE role.rolname::text OPERATOR(pg_catalog.=) current_user;
    IF migration_owner_oid IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap migration owner is divergent';
    END IF;

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
            relation.relforcerowsecurity,
            relation.relispartition
          INTO
            relation_owner,
            relation_kind,
            relation_persistence,
            relation_row_security,
            relation_force_row_security,
            relation_is_partition
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

        SELECT pg_catalog.array_agg(
                   pg_catalog.pg_get_indexdef(index_row.indexrelid)
                   ORDER BY index_relation.relname
               )
          INTO actual_indexes
          FROM pg_catalog.pg_index AS index_row
          JOIN pg_catalog.pg_class AS index_relation
            ON index_relation.oid = index_row.indexrelid
         WHERE index_row.indrelid = relation_oid;

        CASE relation_name -- migration preflight
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
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX rbac_roles_name_key ON identity.rbac_roles USING btree (name)',
                'CREATE UNIQUE INDEX rbac_roles_pkey ON identity.rbac_roles USING btree (role_id)'
            ];
            expected_internal_trigger_count := 8;
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
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX rbac_role_parents_pkey ON identity.rbac_role_parents USING btree (role_id, parent_id)'
            ];
            expected_internal_trigger_count := 4;
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
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX rbac_admin_roles_pkey ON identity.rbac_admin_roles USING btree (admin_subject, role_id)'
            ];
            expected_internal_trigger_count := 2;
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
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX rbac_policies_pkey ON identity.rbac_policies USING btree (role_id, resource, action)'
            ];
            expected_internal_trigger_count := 2;
        END CASE;

        IF relation_owner <> migration_owner_oid
            OR relation_kind <> 'r'
            OR relation_persistence <> 'p'
            OR relation_row_security
            OR relation_force_row_security
            OR relation_is_partition
            OR EXISTS (
                SELECT 1
                  FROM pg_catalog.pg_inherits AS inheritance
                 WHERE inheritance.inhrelid = relation_oid
                    OR inheritance.inhparent = relation_oid
            )
            OR actual_columns IS DISTINCT FROM expected_columns
            OR actual_constraints IS DISTINCT FROM expected_constraints
            OR actual_indexes IS DISTINCT FROM expected_indexes
            OR (
                SELECT pg_catalog.count(*)
                  FROM pg_catalog.pg_trigger AS trigger_row
                 WHERE trigger_row.tgrelid = relation_oid
                   AND trigger_row.tgisinternal
            ) <> expected_internal_trigger_count
            OR EXISTS (
                SELECT 1
                  FROM pg_catalog.pg_trigger AS trigger_row
                  JOIN pg_catalog.pg_constraint AS constraint_row
                    ON constraint_row.oid = trigger_row.tgconstraint
                  JOIN pg_catalog.pg_proc AS procedure
                    ON procedure.oid = trigger_row.tgfoid
                 WHERE trigger_row.tgrelid = relation_oid
                   AND trigger_row.tgisinternal
                   AND (
                       constraint_row.contype <> 'f'
                       OR trigger_row.tgenabled <> 'O'
                       OR trigger_row.tgparentid <> 0
                       OR trigger_row.tgdeferrable
                       OR trigger_row.tginitdeferred
                       OR trigger_row.tgnargs <> 0
                       OR trigger_row.tgattr <>
                              ''::pg_catalog.int2vector
                       OR pg_catalog.octet_length(trigger_row.tgargs) <> 0
                       OR trigger_row.tgqual IS NOT NULL
                       OR trigger_row.tgoldtable IS NOT NULL
                       OR trigger_row.tgnewtable IS NOT NULL
                       OR (
                           procedure.proname = 'RI_FKey_check_ins'
                           AND trigger_row.tgtype <> 5
                       )
                       OR (
                           procedure.proname = 'RI_FKey_check_upd'
                           AND trigger_row.tgtype <> 17
                       )
                       OR (
                           procedure.proname = 'RI_FKey_cascade_del'
                           AND trigger_row.tgtype <> 9
                       )
                       OR (
                           procedure.proname = 'RI_FKey_noaction_upd'
                           AND trigger_row.tgtype <> 17
                       )
                       OR procedure.proname NOT IN (
                           'RI_FKey_check_ins',
                           'RI_FKey_check_upd',
                           'RI_FKey_cascade_del',
                           'RI_FKey_noaction_upd'
                       )
                   )
            )
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
                   migration_owner_oid
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

    IF NOT EXISTS (
        SELECT 1
          FROM pg_catalog.pg_proc AS procedure
          JOIN pg_catalog.pg_language AS language
            ON language.oid = procedure.prolang
         WHERE procedure.oid =
                   'engine.reject_immutable_change()'::pg_catalog.regprocedure
           AND procedure.proowner =
                   migration_owner_oid
           AND language.lanname = 'plpgsql'
           AND procedure.provolatile = 'v'
           AND NOT procedure.prosecdef
           AND NOT procedure.proisstrict
           AND NOT procedure.proleakproof
           AND NOT procedure.proretset
           AND procedure.prorettype = 'trigger'::pg_catalog.regtype
           AND procedure.prokind = 'f'
           AND procedure.proparallel = 'u'
           AND procedure.pronargs = 0
           AND pg_catalog.oidvectortypes(procedure.proargtypes) = ''
           AND COALESCE(
                   procedure.proconfig,
                   ARRAY[]::text[]
               ) = ARRAY[]::text[]
           AND pg_catalog.sha256(
                   pg_catalog.convert_to(procedure.prosrc, 'UTF8')
               ) =
                   pg_catalog.decode(
                       '21f8d1c5780fa5134d4c75b1af5011ffa00c01fdfb0c23dd102896b10916e7af',
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
        )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '42501',
            MESSAGE =
                'required pre-provisioned admin bootstrap role is missing or unsafe';
    END IF;
END
$$;

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

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_rewrite AS rule
         WHERE rule.ev_class =
                   'audit.admin_bootstrap_events'::pg_catalog.regclass
    )
        OR (
            SELECT pg_catalog.count(*)
              FROM pg_catalog.pg_trigger AS trigger_row
             WHERE trigger_row.tgrelid =
                       'audit.admin_bootstrap_events'::pg_catalog.regclass
               AND NOT trigger_row.tgisinternal
        ) <> 2
        OR NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_trigger AS trigger_row
             WHERE trigger_row.tgrelid =
                       'audit.admin_bootstrap_events'::pg_catalog.regclass
               AND trigger_row.tgname =
                       'admin_bootstrap_events_are_immutable'
               AND trigger_row.tgfoid =
                       'engine.reject_immutable_change()'::pg_catalog.regprocedure
               AND trigger_row.tgenabled = 'O'
               AND NOT trigger_row.tgisinternal
               AND trigger_row.tgparentid = 0
               AND trigger_row.tgtype = 27
               AND trigger_row.tgconstrrelid = 0
               AND trigger_row.tgconstrindid = 0
               AND trigger_row.tgconstraint = 0
               AND NOT trigger_row.tgdeferrable
               AND NOT trigger_row.tginitdeferred
               AND trigger_row.tgnargs = 0
               AND trigger_row.tgattr = ''::pg_catalog.int2vector
               AND pg_catalog.octet_length(trigger_row.tgargs) = 0
               AND trigger_row.tgqual IS NULL
               AND trigger_row.tgoldtable IS NULL
               AND trigger_row.tgnewtable IS NULL
        )
        OR NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_trigger AS trigger_row
             WHERE trigger_row.tgrelid =
                       'audit.admin_bootstrap_events'::pg_catalog.regclass
               AND trigger_row.tgname =
                       'admin_bootstrap_events_reject_truncate'
               AND trigger_row.tgfoid =
                       'engine.reject_immutable_change()'::pg_catalog.regprocedure
               AND trigger_row.tgenabled = 'O'
               AND NOT trigger_row.tgisinternal
               AND trigger_row.tgparentid = 0
               AND trigger_row.tgtype = 34
               AND trigger_row.tgconstrrelid = 0
               AND trigger_row.tgconstrindid = 0
               AND trigger_row.tgconstraint = 0
               AND NOT trigger_row.tgdeferrable
               AND NOT trigger_row.tginitdeferred
               AND trigger_row.tgnargs = 0
               AND trigger_row.tgattr = ''::pg_catalog.int2vector
               AND pg_catalog.octet_length(trigger_row.tgargs) = 0
               AND trigger_row.tgqual IS NULL
               AND trigger_row.tgoldtable IS NULL
               AND trigger_row.tgnewtable IS NULL
        )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap audit catalog is divergent';
    END IF;
END
$$;

CREATE FUNCTION identity.bootstrap_first_admin(
    requested_request_id text,
    requested_idempotency_key_hash bytea,
    requested_admin_subject text,
    requested_event_id uuid,
    requested_logical_time_text text,
    requested_migration_checksum bytea
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
    committed_authority_time timestamptz;
    committed_request_hash bytea;
    committed_event audit.admin_bootstrap_events%ROWTYPE;
    committed_event_found boolean;
    caller text := session_user;
    caller_oid pg_catalog.oid;
    owner_oid pg_catalog.oid;
    role_count bigint;
    parent_count bigint;
    assignment_count bigint;
    policy_count bigint;
    locked_function_count bigint;
    relation_name text;
    relation_oid pg_catalog.oid;
    relation_owner pg_catalog.oid;
    relation_kind "char";
    relation_persistence "char";
    relation_row_security boolean;
    relation_force_row_security boolean;
    relation_is_partition boolean;
    expected_columns text[];
    actual_columns text[];
    expected_constraints text[];
    actual_constraints text[];
    expected_indexes text[];
    actual_indexes text[];
    expected_internal_trigger_count bigint;
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
    IF requested_request_id IS NULL
        OR requested_idempotency_key_hash IS NULL
        OR requested_admin_subject IS NULL
        OR requested_event_id IS NULL
        OR requested_logical_time_text IS NULL
        OR requested_migration_checksum IS NULL
        OR caller !~ '^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$'
        OR requested_request_id !~
            '^[A-Za-z0-9._:-]{1,128}$'
        OR pg_catalog.octet_length(requested_idempotency_key_hash) <> 32
        OR pg_catalog.octet_length(requested_migration_checksum) <> 32
        OR requested_admin_subject !~
            '^admin::urn:xb:admin:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
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
    -- then the same protected catalogs and RBAC relations as the migration.
    PERFORM pg_catalog.pg_advisory_xact_lock_shared(88288443778895);
    PERFORM pg_catalog.pg_advisory_xact_lock(88288443778896);
    -- Never wait while retaining a partial catalog fence.
    EXECUTE 'LOCK TABLE pg_catalog.pg_default_acl IN SHARE MODE NOWAIT';
    EXECUTE 'LOCK TABLE pg_catalog.pg_shdepend IN SHARE MODE NOWAIT';
    EXECUTE 'LOCK TABLE pg_catalog.pg_attribute IN SHARE MODE NOWAIT';
    EXECUTE 'LOCK TABLE pg_catalog.pg_proc IN SHARE MODE NOWAIT';
    EXECUTE 'LOCK TABLE pg_catalog.pg_authid IN SHARE MODE NOWAIT';
    EXECUTE 'LOCK TABLE pg_catalog.pg_auth_members IN SHARE MODE NOWAIT';
    EXECUTE 'LOCK TABLE pg_catalog.pg_inherits IN SHARE MODE NOWAIT';
    EXECUTE 'LOCK TABLE pg_catalog.pg_namespace IN SHARE MODE NOWAIT';
    EXECUTE 'LOCK TABLE pg_catalog.pg_class IN SHARE MODE NOWAIT';
    SELECT role.oid
      INTO caller_oid
      FROM pg_catalog.pg_authid AS role
     WHERE role.rolname::text OPERATOR(pg_catalog.=) caller;
    SELECT role.oid
      INTO owner_oid
      FROM pg_catalog.pg_authid AS role
     WHERE role.rolname::text OPERATOR(pg_catalog.=) current_user;
    IF caller_oid IS NULL OR owner_oid IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap role identity is divergent';
    END IF;
    PERFORM 1
      FROM pg_catalog.pg_proc AS procedure
     WHERE procedure.oid IN (
               pg_catalog.to_regprocedure(
                   'identity.admin_has_permission(text,text,text)'
               ),
               pg_catalog.to_regprocedure(
                   'engine.reject_immutable_change()'
               )
           )
     ORDER BY procedure.oid
     FOR UPDATE NOWAIT;
    GET DIAGNOSTICS locked_function_count = ROW_COUNT;
    IF locked_function_count <> 2 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority is divergent';
    END IF;
    PERFORM 1
      FROM pg_catalog.pg_namespace AS namespace
     WHERE namespace.oid IN (
               'engine'::pg_catalog.regnamespace,
               'identity'::pg_catalog.regnamespace,
               'audit'::pg_catalog.regnamespace
           )
     ORDER BY namespace.oid
     FOR UPDATE NOWAIT;
    GET DIAGNOSTICS locked_function_count = ROW_COUNT;
    IF locked_function_count <> 3
        OR EXISTS (
            SELECT 1
             FROM pg_catalog.pg_namespace AS namespace
             WHERE namespace.nspname IN ('engine', 'identity', 'audit')
               AND namespace.nspowner <> owner_oid
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_namespace AS namespace
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      namespace.nspacl,
                      pg_catalog.acldefault('n', namespace.nspowner)
                  )
              ) AS privilege
             WHERE namespace.nspname IN ('engine', 'identity', 'audit')
               AND privilege.grantee <> namespace.nspowner
               AND privilege.privilege_type = 'CREATE'
        )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority is divergent';
    END IF;
    PERFORM 1
     FROM pg_catalog.pg_class AS relation
     WHERE relation.oid IN (
               'engine.schema_migrations'::pg_catalog.regclass,
               'identity.rbac_roles'::pg_catalog.regclass,
               'identity.rbac_role_parents'::pg_catalog.regclass,
               'identity.rbac_admin_roles'::pg_catalog.regclass,
               'identity.rbac_policies'::pg_catalog.regclass,
               'audit.admin_bootstrap_events'::pg_catalog.regclass
           )
     ORDER BY relation.oid
     FOR UPDATE NOWAIT;
    GET DIAGNOSTICS locked_function_count = ROW_COUNT;
    IF locked_function_count <> 6 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority is divergent';
    END IF;
    PERFORM 1
     FROM pg_catalog.pg_attribute AS attribute
     WHERE attribute.attrelid IN (
               'engine.schema_migrations'::pg_catalog.regclass,
               'identity.rbac_roles'::pg_catalog.regclass,
               'identity.rbac_role_parents'::pg_catalog.regclass,
               'identity.rbac_admin_roles'::pg_catalog.regclass,
               'identity.rbac_policies'::pg_catalog.regclass,
               'audit.admin_bootstrap_events'::pg_catalog.regclass
           )
       AND attribute.attnum > 0
       AND NOT attribute.attisdropped
     ORDER BY attribute.attrelid, attribute.attnum
     FOR UPDATE NOWAIT;
    GET DIAGNOSTICS locked_function_count = ROW_COUNT;
    IF locked_function_count <> 28 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority is divergent';
    END IF;
    LOCK TABLE engine.schema_migrations IN SHARE ROW EXCLUSIVE MODE NOWAIT;
    LOCK TABLE identity.rbac_roles IN SHARE ROW EXCLUSIVE MODE NOWAIT;
    LOCK TABLE identity.rbac_role_parents IN SHARE ROW EXCLUSIVE MODE NOWAIT;
    LOCK TABLE identity.rbac_admin_roles IN SHARE ROW EXCLUSIVE MODE NOWAIT;
    LOCK TABLE identity.rbac_policies IN SHARE ROW EXCLUSIVE MODE NOWAIT;
    LOCK TABLE audit.admin_bootstrap_events IN SHARE ROW EXCLUSIVE MODE NOWAIT;

    FOREACH relation_name IN ARRAY ARRAY[
        'engine.schema_migrations',
        'identity.rbac_roles',
        'identity.rbac_role_parents',
        'identity.rbac_admin_roles',
        'identity.rbac_policies',
        'audit.admin_bootstrap_events'
    ]
    LOOP
        relation_oid := pg_catalog.to_regclass(relation_name);
        IF relation_oid IS NULL THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'admin bootstrap authority is divergent';
        END IF;

        SELECT
            relation.relowner,
            relation.relkind,
            relation.relpersistence,
            relation.relrowsecurity,
            relation.relforcerowsecurity,
            relation.relispartition
          INTO
            relation_owner,
            relation_kind,
            relation_persistence,
            relation_row_security,
            relation_force_row_security,
            relation_is_partition
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

        SELECT pg_catalog.array_agg(
                   pg_catalog.pg_get_indexdef(index_row.indexrelid)
                   ORDER BY index_relation.relname
               )
          INTO actual_indexes
          FROM pg_catalog.pg_index AS index_row
          JOIN pg_catalog.pg_class AS index_relation
            ON index_relation.oid = index_row.indexrelid
         WHERE index_row.indrelid = relation_oid;

        CASE relation_name -- runtime preflight
        WHEN 'engine.schema_migrations' THEN
            expected_columns := ARRAY[
                'filename:text:t:',
                'checksum:bytea:t:',
                'applied_at:timestamp with time zone:t:clock_timestamp()'
            ];
            expected_constraints := ARRAY[
                'schema_migrations_applied_at_not_null:NOT NULL applied_at',
                'schema_migrations_checksum_check:CHECK (octet_length(checksum) = 32)',
                'schema_migrations_checksum_not_null:NOT NULL checksum',
                'schema_migrations_filename_not_null:NOT NULL filename',
                'schema_migrations_pkey:PRIMARY KEY (filename)'
            ];
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX schema_migrations_pkey ON engine.schema_migrations USING btree (filename)'
            ];
            expected_internal_trigger_count := 0;
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
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX rbac_roles_name_key ON identity.rbac_roles USING btree (name)',
                'CREATE UNIQUE INDEX rbac_roles_pkey ON identity.rbac_roles USING btree (role_id)'
            ];
            expected_internal_trigger_count := 8;
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
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX rbac_role_parents_pkey ON identity.rbac_role_parents USING btree (role_id, parent_id)'
            ];
            expected_internal_trigger_count := 4;
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
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX rbac_admin_roles_pkey ON identity.rbac_admin_roles USING btree (admin_subject, role_id)'
            ];
            expected_internal_trigger_count := 2;
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
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX rbac_policies_pkey ON identity.rbac_policies USING btree (role_id, resource, action)'
            ];
            expected_internal_trigger_count := 2;
        WHEN 'audit.admin_bootstrap_events' THEN
            expected_columns := ARRAY[
                'event_id:uuid:t:',
                'admin_sequence:bigint:t:',
                'actor_login:text:t:',
                'request_id:text:t:',
                'idempotency_key_hash:bytea:t:',
                'request_hash:bytea:t:',
                'admin_subject:text:t:',
                'logical_time_text:text:t:',
                'occurred_at:timestamp with time zone:t:',
                'role_id:uuid:t:',
                'role_name:text:t:',
                'configuration_version:bigint:t:',
                'outcome:text:t:',
                'detail:jsonb:t:'
            ];
            expected_constraints := ARRAY[
                'admin_bootstrap_events_actor_login_check:CHECK (actor_login ~ ''^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$''::text)',
                'admin_bootstrap_events_actor_login_not_null:NOT NULL actor_login',
                'admin_bootstrap_events_admin_sequence_check:CHECK (admin_sequence = 1)',
                'admin_bootstrap_events_admin_sequence_key:UNIQUE (admin_sequence)',
                'admin_bootstrap_events_admin_sequence_not_null:NOT NULL admin_sequence',
                'admin_bootstrap_events_admin_subject_check:CHECK (admin_subject ~ ''^admin::urn:xb:admin:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$''::text)',
                'admin_bootstrap_events_admin_subject_not_null:NOT NULL admin_subject',
                'admin_bootstrap_events_configuration_version_check:CHECK (configuration_version = 1)',
                'admin_bootstrap_events_configuration_version_not_null:NOT NULL configuration_version',
                'admin_bootstrap_events_detail_not_null:NOT NULL detail',
                'admin_bootstrap_events_event_id_not_null:NOT NULL event_id',
                'admin_bootstrap_events_idempotency_key_hash_check:CHECK (octet_length(idempotency_key_hash) = 32)',
                'admin_bootstrap_events_idempotency_key_hash_key:UNIQUE (idempotency_key_hash)',
                'admin_bootstrap_events_idempotency_key_hash_not_null:NOT NULL idempotency_key_hash',
                'admin_bootstrap_events_logical_time_text_check:CHECK (logical_time_text ~ ''^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z$''::text)',
                'admin_bootstrap_events_logical_time_text_not_null:NOT NULL logical_time_text',
                'admin_bootstrap_events_occurred_at_not_null:NOT NULL occurred_at',
                'admin_bootstrap_events_outcome_check:CHECK (outcome = ''success''::text)',
                'admin_bootstrap_events_outcome_not_null:NOT NULL outcome',
                'admin_bootstrap_events_pkey:PRIMARY KEY (event_id)',
                'admin_bootstrap_events_request_hash_check:CHECK (octet_length(request_hash) = 32)',
                'admin_bootstrap_events_request_hash_not_null:NOT NULL request_hash',
                'admin_bootstrap_events_request_id_check:CHECK (request_id ~ ''^[A-Za-z0-9._:-]{1,128}$''::text)',
                'admin_bootstrap_events_request_id_key:UNIQUE (request_id)',
                'admin_bootstrap_events_request_id_not_null:NOT NULL request_id',
                'admin_bootstrap_events_role_id_check:CHECK (role_id = ''00000000-0000-4000-8000-000000000001''::uuid)',
                'admin_bootstrap_events_role_id_not_null:NOT NULL role_id',
                'admin_bootstrap_events_role_name_check:CHECK (role_name = ''platformgo-superadmin''::text)',
                'admin_bootstrap_events_role_name_not_null:NOT NULL role_name'
            ];
            expected_indexes := ARRAY[
                'CREATE UNIQUE INDEX admin_bootstrap_events_admin_sequence_key ON audit.admin_bootstrap_events USING btree (admin_sequence)',
                'CREATE UNIQUE INDEX admin_bootstrap_events_idempotency_key_hash_key ON audit.admin_bootstrap_events USING btree (idempotency_key_hash)',
                'CREATE UNIQUE INDEX admin_bootstrap_events_pkey ON audit.admin_bootstrap_events USING btree (event_id)',
                'CREATE UNIQUE INDEX admin_bootstrap_events_request_id_key ON audit.admin_bootstrap_events USING btree (request_id)'
            ];
            expected_internal_trigger_count := 0;
        END CASE;

        IF relation_owner <> owner_oid
            OR relation_kind <> 'r'
            OR relation_persistence <> 'p'
            OR relation_row_security
            OR relation_force_row_security
            OR relation_is_partition
            OR EXISTS (
                SELECT 1
                  FROM pg_catalog.pg_inherits AS inheritance
                 WHERE inheritance.inhrelid = relation_oid
                    OR inheritance.inhparent = relation_oid
            )
            OR actual_columns IS DISTINCT FROM expected_columns
            OR actual_constraints IS DISTINCT FROM expected_constraints
            OR actual_indexes IS DISTINCT FROM expected_indexes
            OR (
                SELECT pg_catalog.count(*)
                  FROM pg_catalog.pg_trigger AS trigger_row
                 WHERE trigger_row.tgrelid = relation_oid
                   AND trigger_row.tgisinternal
            ) <> expected_internal_trigger_count
            OR EXISTS (
                SELECT 1
                  FROM pg_catalog.pg_trigger AS trigger_row
                  JOIN pg_catalog.pg_constraint AS constraint_row
                    ON constraint_row.oid = trigger_row.tgconstraint
                  JOIN pg_catalog.pg_proc AS procedure
                    ON procedure.oid = trigger_row.tgfoid
                 WHERE trigger_row.tgrelid = relation_oid
                   AND trigger_row.tgisinternal
                   AND (
                       constraint_row.contype <> 'f'
                       OR trigger_row.tgenabled <> 'O'
                       OR trigger_row.tgparentid <> 0
                       OR trigger_row.tgdeferrable
                       OR trigger_row.tginitdeferred
                       OR trigger_row.tgnargs <> 0
                       OR trigger_row.tgattr <>
                              ''::pg_catalog.int2vector
                       OR pg_catalog.octet_length(trigger_row.tgargs) <> 0
                       OR trigger_row.tgqual IS NOT NULL
                       OR trigger_row.tgoldtable IS NOT NULL
                       OR trigger_row.tgnewtable IS NOT NULL
                       OR (
                           procedure.proname = 'RI_FKey_check_ins'
                           AND trigger_row.tgtype <> 5
                       )
                       OR (
                           procedure.proname = 'RI_FKey_check_upd'
                           AND trigger_row.tgtype <> 17
                       )
                       OR (
                           procedure.proname = 'RI_FKey_cascade_del'
                           AND trigger_row.tgtype <> 9
                       )
                       OR (
                           procedure.proname = 'RI_FKey_noaction_upd'
                           AND trigger_row.tgtype <> 17
                       )
                       OR procedure.proname NOT IN (
                           'RI_FKey_check_ins',
                           'RI_FKey_check_upd',
                           'RI_FKey_cascade_del',
                           'RI_FKey_noaction_upd'
                       )
                   )
            )
            OR EXISTS (
                SELECT 1
                  FROM pg_catalog.pg_class AS relation
                  CROSS JOIN LATERAL pg_catalog.aclexplode(
                      COALESCE(
                          relation.relacl,
                          pg_catalog.acldefault('r', relation.relowner)
                      )
                  ) AS privilege
                 WHERE relation.oid = relation_oid
                   AND privilege.grantee <> relation.relowner
            )
            OR EXISTS (
                SELECT 1
                  FROM pg_catalog.pg_attribute AS attribute
                  CROSS JOIN LATERAL pg_catalog.aclexplode(
                      attribute.attacl
                  ) AS privilege
                 WHERE attribute.attrelid = relation_oid
                   AND attribute.attnum > 0
                   AND NOT attribute.attisdropped
                   AND privilege.grantee <> relation_owner
            )
        THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'admin bootstrap authority is divergent';
        END IF;
    END LOOP;

    IF (SELECT pg_catalog.count(*) FROM engine.schema_migrations) <> 42
        OR (
            SELECT pg_catalog.count(*)
              FROM engine.schema_migrations AS migration
             WHERE migration.filename <
                       '20260731000100_phase3_admin_bootstrap_authority.up.sql'
        ) <> 41
        OR (
            SELECT migration.checksum
              FROM engine.schema_migrations AS migration
             WHERE migration.filename =
                       '20260731000100_phase3_admin_bootstrap_authority.up.sql'
        ) IS DISTINCT FROM requested_migration_checksum
        OR (
            SELECT pg_catalog.sha256(
                       pg_catalog.convert_to(
                           pg_catalog.string_agg(
                               migration.filename || ':' ||
                               pg_catalog.encode(migration.checksum, 'hex') ||
                               E'\n',
                               ''
                               ORDER BY migration.filename
                           ),
                           'UTF8'
                       )
                   )
              FROM engine.schema_migrations AS migration
             WHERE migration.filename <
                       '20260731000100_phase3_admin_bootstrap_authority.up.sql'
        ) IS DISTINCT FROM pg_catalog.decode(
            '2b2fc2fc638c3a303e2811d5bf20a72e84c79994233bda21e53f6f342395c501',
            'hex'
        )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap migration authority is divergent';
    END IF;

    IF NOT EXISTS (
        SELECT 1
          FROM pg_catalog.pg_roles AS role
         WHERE role.rolname = 'platformgo_admin_bootstrap'
           AND NOT role.rolcanlogin
           AND NOT role.rolsuper
           AND NOT role.rolcreatedb
           AND NOT role.rolcreaterole
           AND NOT role.rolreplication
           AND NOT role.rolbypassrls
    )
        OR NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_auth_members AS membership
             WHERE membership.roleid =
                       'platformgo_admin_bootstrap'::pg_catalog.regrole
               AND membership.member = caller_oid
               AND NOT membership.admin_option
               AND membership.inherit_option
               AND membership.set_option
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_auth_members AS membership
              JOIN pg_catalog.pg_roles AS member_role
                ON member_role.oid = membership.member
             WHERE membership.roleid =
                       'platformgo_admin_bootstrap'::pg_catalog.regrole
               AND (
                   membership.admin_option
                   OR NOT membership.inherit_option
                   OR NOT membership.set_option
                   OR NOT member_role.rolcanlogin
                   OR member_role.rolsuper
                   OR member_role.rolcreatedb
                   OR member_role.rolcreaterole
                   OR member_role.rolreplication
                       OR member_role.rolbypassrls
               )
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_auth_members AS bootstrap_membership
              JOIN pg_catalog.pg_auth_members AS other_membership
                ON other_membership.member =
                       bootstrap_membership.member
             WHERE bootstrap_membership.roleid =
                       'platformgo_admin_bootstrap'::pg_catalog.regrole
               AND other_membership.roleid <>
                       'platformgo_admin_bootstrap'::pg_catalog.regrole
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_auth_members AS bootstrap_membership
              JOIN pg_catalog.pg_shdepend AS dependency
                ON dependency.refclassid =
                       'pg_catalog.pg_authid'::pg_catalog.regclass
               AND dependency.refobjid = bootstrap_membership.member
             WHERE bootstrap_membership.roleid =
                       'platformgo_admin_bootstrap'::pg_catalog.regrole
        )
        OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_auth_members AS membership
             WHERE membership.member =
                       'platformgo_admin_bootstrap'::pg_catalog.regrole
        )
        OR (
            SELECT pg_catalog.count(*) = 2
               AND pg_catalog.bool_and(
                   (
                       dependency.classid =
                           'pg_catalog.pg_namespace'::pg_catalog.regclass
                       AND dependency.objid =
                           'identity'::pg_catalog.regnamespace
                       AND dependency.deptype = 'a'
                   )
                   OR (
                       dependency.classid =
                           'pg_catalog.pg_proc'::pg_catalog.regclass
                       AND dependency.objid =
                           'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'::pg_catalog.regprocedure
                       AND dependency.deptype = 'a'
                   )
               )
              FROM pg_catalog.pg_shdepend AS dependency
             WHERE dependency.refclassid =
                       'pg_catalog.pg_authid'::pg_catalog.regclass
               AND dependency.refobjid =
                       'platformgo_admin_bootstrap'::pg_catalog.regrole
        ) IS DISTINCT FROM true
        OR (
            SELECT pg_catalog.count(*) = 1
               AND pg_catalog.bool_and(
                   privilege.privilege_type = 'USAGE'
                   AND NOT privilege.is_grantable
                   AND privilege.grantor = namespace.nspowner
               )
              FROM pg_catalog.pg_namespace AS namespace
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      namespace.nspacl,
                      pg_catalog.acldefault('n', namespace.nspowner)
                  )
              ) AS privilege
             WHERE namespace.oid =
                       'identity'::pg_catalog.regnamespace
               AND privilege.grantee =
                       'platformgo_admin_bootstrap'::pg_catalog.regrole
        ) IS DISTINCT FROM true
        OR (
            SELECT pg_catalog.count(*) = 1
               AND pg_catalog.bool_and(
                   privilege.grantee =
                       'platformgo_admin_bootstrap'::pg_catalog.regrole
                   AND privilege.privilege_type = 'EXECUTE'
                   AND NOT privilege.is_grantable
                   AND privilege.grantor = procedure.proowner
               )
              FROM pg_catalog.pg_proc AS procedure
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      procedure.proacl,
                      pg_catalog.acldefault('f', procedure.proowner)
                  )
              ) AS privilege
             WHERE procedure.oid =
                       'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'::pg_catalog.regprocedure
               AND privilege.grantee <> procedure.proowner
        ) IS DISTINCT FROM true
        OR (
            SELECT pg_catalog.count(*) = 1
               AND pg_catalog.bool_and(
                   privilege.grantee =
                       'platformgo_api'::pg_catalog.regrole
                   AND privilege.privilege_type = 'EXECUTE'
                   AND NOT privilege.is_grantable
                   AND privilege.grantor = procedure.proowner
               )
              FROM pg_catalog.pg_proc AS procedure
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      procedure.proacl,
                      pg_catalog.acldefault('f', procedure.proowner)
                  )
              ) AS privilege
             WHERE procedure.oid =
                       'identity.admin_has_permission(text,text,text)'::pg_catalog.regprocedure
               AND privilege.grantee <> procedure.proowner
        ) IS DISTINCT FROM true
        OR NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_proc AS procedure
              JOIN pg_catalog.pg_language AS language
                ON language.oid = procedure.prolang
             WHERE procedure.oid =
                       'identity.admin_has_permission(text,text,text)'::pg_catalog.regprocedure
           AND procedure.proowner =
                   owner_oid
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
        )
        OR NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_proc AS procedure
              JOIN pg_catalog.pg_language AS language
                ON language.oid = procedure.prolang
             WHERE procedure.oid =
                       'engine.reject_immutable_change()'::pg_catalog.regprocedure
           AND procedure.proowner =
                   owner_oid
               AND language.lanname = 'plpgsql'
               AND procedure.provolatile = 'v'
               AND NOT procedure.prosecdef
               AND NOT procedure.proisstrict
               AND NOT procedure.proleakproof
               AND NOT procedure.proretset
               AND procedure.prorettype = 'trigger'::pg_catalog.regtype
               AND procedure.prokind = 'f'
               AND procedure.proparallel = 'u'
               AND procedure.pronargs = 0
               AND pg_catalog.oidvectortypes(procedure.proargtypes) = ''
               AND COALESCE(
                       procedure.proconfig,
                       ARRAY[]::text[]
                   ) = ARRAY[]::text[]
               AND pg_catalog.sha256(
                       pg_catalog.convert_to(procedure.prosrc, 'UTF8')
                   ) =
                       pg_catalog.decode(
                           '21f8d1c5780fa5134d4c75b1af5011ffa00c01fdfb0c23dd102896b10916e7af',
                           'hex'
                       )
        )
        OR EXISTS (
            SELECT 1
             FROM pg_catalog.pg_trigger AS trigger_row
             WHERE trigger_row.tgrelid IN (
                       'engine.schema_migrations'::pg_catalog.regclass,
                       'identity.rbac_roles'::pg_catalog.regclass,
                       'identity.rbac_role_parents'::pg_catalog.regclass,
                       'identity.rbac_admin_roles'::pg_catalog.regclass,
                       'identity.rbac_policies'::pg_catalog.regclass
                   )
               AND NOT trigger_row.tgisinternal
        )
        OR EXISTS (
            SELECT 1
             FROM pg_catalog.pg_rewrite AS rule
             WHERE rule.ev_class IN (
                       'engine.schema_migrations'::pg_catalog.regclass,
                       'identity.rbac_roles'::pg_catalog.regclass,
                       'identity.rbac_role_parents'::pg_catalog.regclass,
                       'identity.rbac_admin_roles'::pg_catalog.regclass,
                       'identity.rbac_policies'::pg_catalog.regclass,
                       'audit.admin_bootstrap_events'::pg_catalog.regclass
                   )
        )
        OR (
            SELECT pg_catalog.count(*)
              FROM pg_catalog.pg_trigger AS trigger_row
             WHERE trigger_row.tgrelid =
                       'audit.admin_bootstrap_events'::pg_catalog.regclass
               AND NOT trigger_row.tgisinternal
        ) <> 2
        OR NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_trigger AS trigger_row
             WHERE trigger_row.tgrelid =
                       'audit.admin_bootstrap_events'::pg_catalog.regclass
               AND trigger_row.tgname =
                       'admin_bootstrap_events_are_immutable'
               AND trigger_row.tgfoid =
                       'engine.reject_immutable_change()'::pg_catalog.regprocedure
               AND trigger_row.tgenabled = 'O'
               AND NOT trigger_row.tgisinternal
               AND trigger_row.tgparentid = 0
               AND trigger_row.tgtype = 27
               AND trigger_row.tgconstrrelid = 0
               AND trigger_row.tgconstrindid = 0
               AND trigger_row.tgconstraint = 0
               AND NOT trigger_row.tgdeferrable
               AND NOT trigger_row.tginitdeferred
               AND trigger_row.tgnargs = 0
               AND trigger_row.tgattr = ''::pg_catalog.int2vector
               AND pg_catalog.octet_length(trigger_row.tgargs) = 0
               AND trigger_row.tgqual IS NULL
               AND trigger_row.tgoldtable IS NULL
               AND trigger_row.tgnewtable IS NULL
        )
        OR NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_trigger AS trigger_row
             WHERE trigger_row.tgrelid =
                       'audit.admin_bootstrap_events'::pg_catalog.regclass
               AND trigger_row.tgname =
                       'admin_bootstrap_events_reject_truncate'
               AND trigger_row.tgfoid =
                       'engine.reject_immutable_change()'::pg_catalog.regprocedure
               AND trigger_row.tgenabled = 'O'
               AND NOT trigger_row.tgisinternal
               AND trigger_row.tgparentid = 0
               AND trigger_row.tgtype = 34
               AND trigger_row.tgconstrrelid = 0
               AND trigger_row.tgconstrindid = 0
               AND trigger_row.tgconstraint = 0
               AND NOT trigger_row.tgdeferrable
               AND NOT trigger_row.tginitdeferred
               AND trigger_row.tgnargs = 0
               AND trigger_row.tgattr = ''::pg_catalog.int2vector
               AND pg_catalog.octet_length(trigger_row.tgargs) = 0
               AND trigger_row.tgqual IS NULL
               AND trigger_row.tgoldtable IS NULL
               AND trigger_row.tgnewtable IS NULL
        )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority is divergent';
    END IF;

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
        BEGIN
            committed_authority_time :=
                committed_event.logical_time_text::timestamptz;
        EXCEPTION
            WHEN datetime_field_overflow OR invalid_datetime_format THEN
                RAISE EXCEPTION USING
                    ERRCODE = '55000',
                    MESSAGE = 'admin bootstrap authority is divergent';
        END;
        committed_request_hash := pg_catalog.sha256(
            pg_catalog.convert_to(
                'platformgo.admin-bootstrap.request.v1' || E'\n' ||
                committed_event.actor_login || E'\n' ||
                committed_event.request_id || E'\n' ||
                committed_event.admin_subject || E'\n' ||
                committed_event.event_id::text || E'\n' ||
                committed_event.logical_time_text || E'\n' ||
                bootstrap_role_id::text || E'\n' ||
                bootstrap_role_name || E'\n' ||
                bootstrap_configuration_version::text || E'\n' ||
                '*' || E'\n' ||
                '*' || E'\n' ||
                'allow' || E'\n',
                'UTF8'
            )
        );
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
            OR committed_event.occurred_at
                   OPERATOR(pg_catalog.!=) committed_authority_time
            OR pg_catalog.to_char(
                   committed_authority_time AT TIME ZONE 'UTC',
                   'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
               ) OPERATOR(pg_catalog.!=)
                   committed_event.logical_time_text
            OR committed_event.request_hash
                   OPERATOR(pg_catalog.!=) committed_request_hash
            OR committed_event.outcome OPERATOR(pg_catalog.!=) 'success'
            OR committed_event.detail OPERATOR(pg_catalog.!=)
                   pg_catalog.jsonb_build_object(
                       'after',
                       pg_catalog.jsonb_build_object(
                           'adminSubject', committed_event.admin_subject,
                           'roleName', bootstrap_role_name,
                           'configurationVersion',
                           bootstrap_configuration_version
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
                'created'::text,
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

    IF (
        SELECT pg_catalog.count(*)
          FROM identity.rbac_roles AS role
         WHERE role.role_id = bootstrap_role_id
           AND role.name = bootstrap_role_name
           AND role.builtin
    ) <> 1
        OR (SELECT pg_catalog.count(*) FROM identity.rbac_roles) <> 1
        OR (SELECT pg_catalog.count(*) FROM identity.rbac_role_parents) <> 0
        OR (
            SELECT pg_catalog.count(*)
              FROM identity.rbac_admin_roles AS assignment
             WHERE assignment.admin_subject = requested_admin_subject
               AND assignment.role_id = bootstrap_role_id
        ) <> 1
        OR (SELECT pg_catalog.count(*) FROM identity.rbac_admin_roles) <> 1
        OR (
            SELECT pg_catalog.count(*)
              FROM identity.rbac_policies AS policy
             WHERE policy.role_id = bootstrap_role_id
               AND policy.resource = '*'
               AND policy.action = '*'
               AND policy.effect = 'allow'
        ) <> 1
        OR (SELECT pg_catalog.count(*) FROM identity.rbac_policies) <> 1
        OR (
            SELECT pg_catalog.count(*)
              FROM audit.admin_bootstrap_events AS event
             WHERE event.event_id = requested_event_id
               AND event.admin_sequence = 1
               AND event.actor_login = caller
               AND event.request_id = requested_request_id
               AND event.idempotency_key_hash =
                       requested_idempotency_key_hash
               AND event.request_hash = computed_request_hash
               AND event.admin_subject = requested_admin_subject
               AND event.logical_time_text = requested_logical_time_text
               AND event.occurred_at = authority_time
               AND event.role_id = bootstrap_role_id
               AND event.role_name = bootstrap_role_name
               AND event.configuration_version =
                       bootstrap_configuration_version
               AND event.outcome = 'success'
               AND event.detail = pg_catalog.jsonb_build_object(
                       'after',
                       pg_catalog.jsonb_build_object(
                           'adminSubject', requested_admin_subject,
                           'roleName', bootstrap_role_name,
                           'configurationVersion',
                           bootstrap_configuration_version
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
        ) <> 1
        OR (SELECT pg_catalog.count(*) FROM audit.admin_bootstrap_events) <> 1
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap authority is divergent';
    END IF;

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
        'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
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
    text,
    bytea
) TO platformgo_admin_bootstrap;
