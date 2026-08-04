-- Phase 3 prediction-market catalog read model.
--
-- Lock/rewrite: this forward migration creates three additive relations and
-- their indexes. It does not backfill rows, rewrite an existing heap, or
-- install a population writer. PostgreSQL holds the normal relation locks for
-- each CREATE statement only until this migration commits.
-- Transaction: the relations, constraints, indexes, and runtime ACLs are
-- committed atomically with the migration journal row. A definite DDL or ACL
-- failure rolls back the complete catalog addition.
-- Compatibility: instruments remain the economic authority. The new tables
-- contain prediction metadata and references only; the API receives SELECT
-- access and no runtime role receives prediction-table DML access.
-- Failure/retry: an unknown commit outcome requires comparing this filename
-- and checksum with the complete catalog before retrying. A definite
-- pre-commit failure can retry the unchanged forward migration from tip 43.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';
SET LOCAL search_path = pg_catalog;

-- The production migrator holds this same session-level advisory key.  The
-- transaction-level try-lock is intentionally re-entrant for that session,
-- while a direct/manual migration must fail fast instead of bypassing the
-- cooperating global maintenance window.
DO $$
BEGIN
    IF NOT pg_catalog.pg_try_advisory_xact_lock(88288443778895::bigint) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55P03',
            MESSAGE = 'prediction catalog migration requires the global maintenance fence';
    END IF;
END
$$;

-- Fence every existing object whose catalog definition is read or replaced
-- below. pg_get_object_address acquires PostgreSQL's object lock without
-- locking protected pg_catalog relations; the fixed order keeps concurrent
-- multi-object DDL from presenting an inverse object-lock cycle. The short
-- lock timeout makes a definite conflict retryable before any catalog DDL.
SET LOCAL lock_timeout = '1s';

DO $$
DECLARE
    schema_name text;
    table_index integer;
    table_schemas text[] := ARRAY['engine', 'engine', 'trading'];
    table_names text[] := ARRAY['schema_migrations', 'deployment_shard', 'instruments'];
    event_trigger_name text;
    class_id pg_catalog.oid;
    object_id pg_catalog.oid;
    object_sub_id integer;
BEGIN
    FOREACH schema_name IN ARRAY ARRAY[
        'audit', 'engine', 'identity', 'ledger', 'market', 'messaging',
        'realtime', 'trading'
    ]
    LOOP
        SELECT address.classid, address.objid, address.objsubid
          INTO class_id, object_id, object_sub_id
          FROM pg_catalog.pg_get_object_address(
              'schema',
              ARRAY[schema_name],
              ARRAY[]::text[]
          ) AS address;
        IF class_id IS NULL OR object_id IS NULL OR object_id = 0 THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'prediction catalog trusted schema object is divergent';
        END IF;
    END LOOP;

    FOR table_index IN 1..3
    LOOP
        SELECT address.classid, address.objid, address.objsubid
          INTO class_id, object_id, object_sub_id
          FROM pg_catalog.pg_get_object_address(
              'table',
              ARRAY[
                  table_schemas[table_index],
                  table_names[table_index]
              ],
              ARRAY[]::text[]
          ) AS address;
        IF class_id IS NULL OR object_id IS NULL OR object_id = 0 THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'prediction catalog authority relation object is divergent';
        END IF;
    END LOOP;

    SELECT address.classid, address.objid, address.objsubid
      INTO class_id, object_id, object_sub_id
      FROM pg_catalog.pg_get_object_address(
          'function',
          ARRAY['identity', 'bootstrap_first_admin'],
          ARRAY['text', 'bytea', 'text', 'uuid', 'text', 'bytea']
      ) AS address;
    IF class_id IS NULL OR object_id IS NULL OR object_id = 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog bootstrap function object is divergent';
    END IF;

    FOR event_trigger_name IN
        SELECT event_trigger.evtname::text
          FROM pg_catalog.pg_event_trigger AS event_trigger
         ORDER BY event_trigger.oid
    LOOP
        SELECT address.classid, address.objid, address.objsubid
          INTO class_id, object_id, object_sub_id
          FROM pg_catalog.pg_get_object_address(
              'event trigger',
              ARRAY[event_trigger_name],
              ARRAY[]::text[]
          ) AS address;
        IF class_id IS NULL OR object_id IS NULL OR object_id = 0 THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'prediction catalog event-trigger object is divergent';
        END IF;
    END LOOP;
EXCEPTION
    WHEN invalid_schema_name OR undefined_table OR undefined_function
       OR undefined_object OR wrong_object_type OR lock_not_available THEN
        RAISE EXCEPTION USING
            ERRCODE = '55P03',
            MESSAGE = 'prediction catalog object authority is contended or divergent';
END
$$;

SET LOCAL lock_timeout = '5s';

-- The migration owner is the exact role that owns the trusted predecessor
-- catalog.  PostgreSQL exposes role attributes and memberships through the
-- ordinary pg_roles/pg_auth_members views; pg_authid is deliberately not
-- queried because a demoted NOSUPERUSER owner cannot read it.
DO $$
DECLARE
    owner_oid pg_catalog.oid;
    role_is_superuser boolean;
    role_can_create_db boolean;
    role_can_create_role boolean;
    role_can_replicate boolean;
    role_can_bypass_rls boolean;
    trusted_schema_count bigint;
    trusted_function_owner pg_catalog.oid;
    trusted_function_argtypes pg_catalog.oidvector;
    trusted_function_signature text;
    trusted_function_security_definer boolean;
    trusted_function_leakproof boolean;
    trusted_function_strict boolean;
    trusted_function_volatility "char";
    trusted_function_parallel "char";
    trusted_function_return_type pg_catalog.oid;
    trusted_function_language text;
    trusted_function_config text[];
    trusted_function_acl_count bigint;
    preexisting_acl_count bigint;
    preexisting_acl_hash bytea;
BEGIN
    IF session_user IS DISTINCT FROM current_user THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog migration requires session_user=current_user';
    END IF;

    SELECT relation.relowner
      INTO owner_oid
      FROM pg_catalog.pg_class AS relation
     WHERE relation.oid = 'engine.schema_migrations'::pg_catalog.regclass;
    IF owner_oid IS NULL
       OR pg_catalog.pg_get_userbyid(owner_oid) IS DISTINCT FROM current_user
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog migration owner is divergent';
    END IF;

    SELECT role.rolsuper,
           role.rolcreatedb,
           role.rolcreaterole,
           role.rolreplication,
           role.rolbypassrls
      INTO role_is_superuser,
           role_can_create_db,
           role_can_create_role,
           role_can_replicate,
           role_can_bypass_rls
      FROM pg_catalog.pg_roles AS role
     WHERE role.rolname = current_user;
    IF NOT FOUND
       OR role_is_superuser
       OR role_can_create_db
       OR role_can_create_role
       OR role_can_replicate
       OR role_can_bypass_rls
       OR EXISTS (
           SELECT 1
             FROM pg_catalog.pg_auth_members AS membership
            WHERE membership.member = owner_oid
               OR membership.roleid = owner_oid
       )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog migration owner is unsafe';
    END IF;

    SELECT count(*)
      INTO trusted_schema_count
      FROM pg_catalog.pg_namespace AS namespace
     WHERE namespace.nspname IN (
               'audit', 'engine', 'identity', 'ledger', 'market',
               'messaging', 'realtime', 'trading'
           )
       AND namespace.nspowner = owner_oid;
    IF trusted_schema_count <> 8 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog trusted schema ownership is divergent';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_class AS relation
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
         WHERE namespace.nspname IN (
                   'audit', 'engine', 'identity', 'ledger', 'market',
                   'messaging', 'realtime', 'trading'
               )
           AND relation.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
           AND relation.relowner <> owner_oid
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog trusted relation ownership is divergent';
    END IF;

    -- Freeze the preexisting authority graph before creating any new table:
    -- no direct grant may come from a non-owner grantor, carry a grant option,
    -- or name a role outside the reviewed runtime manifest. Memberships are
    -- equally unsafe because they can widen an otherwise exact ACL graph.
    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_namespace AS namespace
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              COALESCE(
                  namespace.nspacl,
                  pg_catalog.acldefault('n', namespace.nspowner)
              )
          ) AS privilege
         WHERE namespace.nspname IN (
                   'audit', 'engine', 'identity', 'ledger', 'market',
                   'messaging', 'realtime', 'trading'
               )
           AND (
               privilege.grantor <> namespace.nspowner
               OR privilege.is_grantable
               OR privilege.grantee NOT IN (
                   namespace.nspowner,
                   'platformgo_admin_bootstrap'::pg_catalog.regrole,
                   'platformgo_api'::pg_catalog.regrole,
                   'platformgo_engine'::pg_catalog.regrole,
                   'platformgo_outbox'::pg_catalog.regrole,
                   'platformgo_projector'::pg_catalog.regrole,
                   'platformgo_realtime'::pg_catalog.regrole,
                   'platformgo_realtime_repair'::pg_catalog.regrole
               )
           )
    ) OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_class AS relation
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              COALESCE(
                  relation.relacl,
                  pg_catalog.acldefault('r', relation.relowner)
              )
          ) AS privilege
         WHERE namespace.nspname IN (
                   'audit', 'engine', 'identity', 'ledger', 'market',
                   'messaging', 'realtime', 'trading'
               )
           AND relation.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
           AND (
               privilege.grantor <> relation.relowner
               OR privilege.is_grantable
               OR privilege.grantee NOT IN (
                   relation.relowner,
                   'platformgo_admin_bootstrap'::pg_catalog.regrole,
                   'platformgo_api'::pg_catalog.regrole,
                   'platformgo_engine'::pg_catalog.regrole,
                   'platformgo_outbox'::pg_catalog.regrole,
                   'platformgo_projector'::pg_catalog.regrole,
                   'platformgo_realtime'::pg_catalog.regrole,
                   'platformgo_realtime_repair'::pg_catalog.regrole
               )
           )
    ) OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_attribute AS attribute
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = attribute.attrelid
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
         WHERE namespace.nspname IN (
                   'audit', 'engine', 'identity', 'ledger', 'market',
                   'messaging', 'realtime', 'trading'
               )
           AND attribute.attnum > 0
           AND NOT attribute.attisdropped
           AND (
               privilege.grantor <> relation.relowner
               OR privilege.is_grantable
               OR privilege.grantee NOT IN (
                   relation.relowner,
                   'platformgo_admin_bootstrap'::pg_catalog.regrole,
                   'platformgo_api'::pg_catalog.regrole,
                   'platformgo_engine'::pg_catalog.regrole,
                   'platformgo_outbox'::pg_catalog.regrole,
                   'platformgo_projector'::pg_catalog.regrole,
                   'platformgo_realtime'::pg_catalog.regrole,
                   'platformgo_realtime_repair'::pg_catalog.regrole
               )
           )
    ) OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_auth_members AS membership
          JOIN pg_catalog.pg_roles AS parent_role
            ON parent_role.oid = membership.roleid
          JOIN pg_catalog.pg_roles AS member_role
            ON member_role.oid = membership.member
         WHERE parent_role.rolname IN (
                   'platformgo_admin_bootstrap', 'platformgo_api',
                   'platformgo_engine', 'platformgo_outbox',
                   'platformgo_projector', 'platformgo_realtime',
                   'platformgo_realtime_repair'
               )
            OR member_role.rolname IN (
                   'platformgo_admin_bootstrap', 'platformgo_api',
                   'platformgo_engine', 'platformgo_outbox',
                   'platformgo_projector', 'platformgo_realtime',
                   'platformgo_realtime_repair'
               )
    ) OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_class AS relation
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          JOIN pg_catalog.pg_index AS index_row
            ON index_row.indrelid = relation.oid
          JOIN pg_catalog.pg_class AS index_relation
            ON index_relation.oid = index_row.indexrelid
         WHERE namespace.nspname IN (
                   'audit', 'engine', 'identity', 'ledger', 'market',
                   'messaging', 'realtime', 'trading'
               )
           AND index_relation.relowner <> relation.relowner
    ) OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_class AS relation
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          JOIN pg_catalog.pg_class AS toast
            ON toast.oid = relation.reltoastrelid
         WHERE namespace.nspname IN (
                   'audit', 'engine', 'identity', 'ledger', 'market',
                   'messaging', 'realtime', 'trading'
               )
           AND relation.reltoastrelid <> 0
           AND toast.relowner <> relation.relowner
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog preexisting authority graph is divergent';
    END IF;

    -- The reviewed tip-43 ACL graph is frozen as a canonical role-normalized
    -- digest. This catches both unknown grantees and an unauthorized privilege
    -- added to an otherwise reviewed runtime role, including default and
    -- column-level grants. Owner OIDs are normalized because a disposable
    -- deployment may use a different exact owner OID while preserving the
    -- same authority graph.
    WITH authority AS (
        SELECT relowner AS owner_oid
          FROM pg_catalog.pg_class
         WHERE oid = 'engine.schema_migrations'::pg_catalog.regclass
    ), schema_lines AS (
        SELECT 'schema|' || namespace.nspname || '|' ||
               CASE
                   WHEN privilege.grantee = authority.owner_oid THEN '<owner>'
                   WHEN privilege.grantee = 0 THEN 'PUBLIC'
                   ELSE COALESCE(grantee.rolname, '?')
               END || '|' || privilege.privilege_type || '|' ||
               CASE
                   WHEN privilege.grantor = authority.owner_oid THEN '<owner>'
                   WHEN privilege.grantor = 0 THEN 'PUBLIC'
                   ELSE COALESCE(grantor.rolname, '?')
               END || '|' || privilege.is_grantable AS line
          FROM pg_catalog.pg_namespace AS namespace
          CROSS JOIN authority
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              COALESCE(
                  namespace.nspacl,
                  pg_catalog.acldefault('n', namespace.nspowner)
              )
          ) AS privilege
          LEFT JOIN pg_catalog.pg_roles AS grantee
            ON grantee.oid = privilege.grantee
          LEFT JOIN pg_catalog.pg_roles AS grantor
            ON grantor.oid = privilege.grantor
         WHERE namespace.nspname IN (
                   'audit', 'engine', 'identity', 'ledger', 'market',
                   'messaging', 'realtime', 'trading'
               )
    ), relation_lines AS (
        SELECT 'relation|' || namespace.nspname || '|' || relation.relname || '|' ||
               CASE
                   WHEN privilege.grantee = authority.owner_oid THEN '<owner>'
                   WHEN privilege.grantee = 0 THEN 'PUBLIC'
                   ELSE COALESCE(grantee.rolname, '?')
               END || '|' || privilege.privilege_type || '|' ||
               CASE
                   WHEN privilege.grantor = authority.owner_oid THEN '<owner>'
                   WHEN privilege.grantor = 0 THEN 'PUBLIC'
                   ELSE COALESCE(grantor.rolname, '?')
               END || '|' || privilege.is_grantable AS line
          FROM pg_catalog.pg_class AS relation
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          CROSS JOIN authority
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              COALESCE(
                  relation.relacl,
                  pg_catalog.acldefault(
                      CASE WHEN relation.relkind = 'S' THEN 'S'::"char" ELSE 'r'::"char" END,
                      relation.relowner
                  )
              )
          ) AS privilege
          LEFT JOIN pg_catalog.pg_roles AS grantee
            ON grantee.oid = privilege.grantee
          LEFT JOIN pg_catalog.pg_roles AS grantor
            ON grantor.oid = privilege.grantor
         WHERE namespace.nspname IN (
                   'audit', 'engine', 'identity', 'ledger', 'market',
                   'messaging', 'realtime', 'trading'
               )
           AND relation.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
    ), column_lines AS (
        SELECT 'column|' || namespace.nspname || '|' || relation.relname || '|' ||
               attribute.attnum || '|' || attribute.attname || '|' ||
               CASE
                   WHEN privilege.grantee = authority.owner_oid THEN '<owner>'
                   WHEN privilege.grantee = 0 THEN 'PUBLIC'
                   ELSE COALESCE(grantee.rolname, '?')
               END || '|' || privilege.privilege_type || '|' ||
               CASE
                   WHEN privilege.grantor = authority.owner_oid THEN '<owner>'
                   WHEN privilege.grantor = 0 THEN 'PUBLIC'
                   ELSE COALESCE(grantor.rolname, '?')
               END || '|' || privilege.is_grantable AS line
          FROM pg_catalog.pg_attribute AS attribute
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = attribute.attrelid
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          CROSS JOIN authority
          CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
          LEFT JOIN pg_catalog.pg_roles AS grantee
            ON grantee.oid = privilege.grantee
          LEFT JOIN pg_catalog.pg_roles AS grantor
            ON grantor.oid = privilege.grantor
         WHERE namespace.nspname IN (
                   'audit', 'engine', 'identity', 'ledger', 'market',
                   'messaging', 'realtime', 'trading'
               )
           AND attribute.attnum > 0
           AND NOT attribute.attisdropped
    ), default_lines AS (
        SELECT 'default|' ||
               CASE
                   WHEN default_acl.defaclrole = authority.owner_oid THEN '<owner>'
                   ELSE COALESCE(default_role.rolname, '?')
               END || '|' || default_acl.defaclobjtype::text || '|' ||
               COALESCE(namespace.nspname, '') || '|' ||
               CASE
                   WHEN privilege.grantee = authority.owner_oid THEN '<owner>'
                   WHEN privilege.grantee = 0 THEN 'PUBLIC'
                   ELSE COALESCE(grantee.rolname, '?')
               END || '|' || privilege.privilege_type || '|' ||
               CASE
                   WHEN privilege.grantor = authority.owner_oid THEN '<owner>'
                   WHEN privilege.grantor = 0 THEN 'PUBLIC'
                   ELSE COALESCE(grantor.rolname, '?')
               END || '|' || privilege.is_grantable AS line
          FROM pg_catalog.pg_default_acl AS default_acl
          CROSS JOIN authority
          LEFT JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = default_acl.defaclnamespace
          LEFT JOIN pg_catalog.pg_roles AS default_role
            ON default_role.oid = default_acl.defaclrole
          CROSS JOIN LATERAL pg_catalog.aclexplode(default_acl.defaclacl) AS privilege
          LEFT JOIN pg_catalog.pg_roles AS grantee
            ON grantee.oid = privilege.grantee
          LEFT JOIN pg_catalog.pg_roles AS grantor
            ON grantor.oid = privilege.grantor
    ), all_lines AS (
        SELECT line FROM schema_lines
        UNION ALL SELECT line FROM relation_lines
        UNION ALL SELECT line FROM column_lines
        UNION ALL SELECT line FROM default_lines
    )
    SELECT count(*),
           pg_catalog.sha256(
               pg_catalog.convert_to(
                   pg_catalog.string_agg(line, E'\n' ORDER BY line),
                   'UTF8'
               )
           )
      INTO preexisting_acl_count, preexisting_acl_hash
      FROM all_lines;
    IF preexisting_acl_count <> 572
       OR preexisting_acl_hash IS DISTINCT FROM pg_catalog.decode(
           '5c94d8ab3246e67fe63820df35b7153e31e0a88c0701aabae42f32bdd8da4cfe',
           'hex'
       )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog frozen ACL graph is divergent';
    END IF;

    -- The bootstrap function is outside the relation ACL digest below, so
    -- freeze its complete executable authority graph explicitly. CREATE OR
    -- REPLACE preserves this identity, signature, security mode, settings,
    -- and ACL; any hostile change must reject this migration before DDL.
    SELECT procedure.proowner,
           procedure.proargtypes,
           pg_catalog.pg_get_function_identity_arguments(procedure.oid),
           procedure.prosecdef,
           procedure.proleakproof,
           procedure.proisstrict,
           procedure.provolatile,
           procedure.proparallel,
           procedure.prorettype,
           language.lanname,
           COALESCE(procedure.proconfig, ARRAY[]::text[])
      INTO trusted_function_owner,
           trusted_function_argtypes,
           trusted_function_signature,
           trusted_function_security_definer,
           trusted_function_leakproof,
           trusted_function_strict,
           trusted_function_volatility,
           trusted_function_parallel,
           trusted_function_return_type,
           trusted_function_language,
           trusted_function_config
      FROM pg_catalog.pg_proc AS procedure
      JOIN pg_catalog.pg_language AS language
        ON language.oid = procedure.prolang
     WHERE procedure.oid =
         'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
             ::pg_catalog.regprocedure
       AND procedure.prokind = 'f'
       AND procedure.prorettype = 'record'::pg_catalog.regtype
       AND language.lanname = 'plpgsql';
    IF NOT FOUND
       OR trusted_function_owner IS DISTINCT FROM owner_oid
       OR trusted_function_argtypes IS DISTINCT FROM
           '25 17 25 2950 25 17'::pg_catalog.oidvector
       OR trusted_function_signature IS DISTINCT FROM
           'requested_request_id text, requested_idempotency_key_hash bytea, requested_admin_subject text, requested_event_id uuid, requested_logical_time_text text, requested_migration_checksum bytea'
       OR trusted_function_security_definer IS DISTINCT FROM true
       OR trusted_function_leakproof IS DISTINCT FROM false
       OR trusted_function_strict IS DISTINCT FROM false
       OR trusted_function_volatility IS DISTINCT FROM 'v'
       OR trusted_function_parallel IS DISTINCT FROM 'u'
       OR trusted_function_return_type IS DISTINCT FROM 'record'::pg_catalog.regtype
       OR trusted_function_language IS DISTINCT FROM 'plpgsql'
       OR trusted_function_config IS DISTINCT FROM ARRAY[
           'search_path=pg_catalog',
           'lock_timeout=5s',
           'statement_timeout=10s'
       ]::text[]
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog bootstrap function security is divergent';
    END IF;

    SELECT count(*)
      INTO trusted_function_acl_count
      FROM pg_catalog.pg_proc AS procedure
      CROSS JOIN LATERAL pg_catalog.aclexplode(
          COALESCE(
              procedure.proacl,
              pg_catalog.acldefault('f', procedure.proowner)
          )
      ) AS privilege
     WHERE procedure.oid =
         'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
             ::pg_catalog.regprocedure;
    IF trusted_function_acl_count <> 2
       OR EXISTS (
           SELECT 1
             FROM pg_catalog.pg_proc AS procedure
             CROSS JOIN LATERAL pg_catalog.aclexplode(
                 COALESCE(
                     procedure.proacl,
                     pg_catalog.acldefault('f', procedure.proowner)
                 )
             ) AS privilege
            WHERE procedure.oid =
                'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
                    ::pg_catalog.regprocedure
              AND (
                  privilege.privilege_type <> 'EXECUTE'
                  OR privilege.is_grantable
                  OR privilege.grantor <> owner_oid
                  OR privilege.grantee NOT IN (
                      owner_oid,
                      'platformgo_admin_bootstrap'::pg_catalog.regrole
                  )
              )
       )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog bootstrap function ACL is divergent';
    END IF;
END
$$;

-- Classify and fence the deployment shard before taking any relation lock.
-- Engine writers acquire this transaction advisory lock before touching
-- trading.instruments; the migration therefore cannot hold an instruments
-- lock while waiting for the shard lock. The empty clean-schema path locks
-- and rechecks deployment_shard so a concurrent first provisioning cannot
-- turn an un-fenced empty decision into a live-writer race.
DO $$
DECLARE
    configured_shard integer;
    rechecked_shard integer;
    deployment_shard_count bigint;
    rechecked_count bigint;
    shard_lock_acquired boolean;
BEGIN
    SELECT count(*), min(shard_id)::integer
      INTO deployment_shard_count, configured_shard
      FROM engine.deployment_shard
     WHERE singleton;
    IF deployment_shard_count > 1
       OR (deployment_shard_count = 1 AND configured_shard IS NULL)
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog deployment shard state is divergent';
    END IF;

    IF deployment_shard_count = 1 THEN
        SELECT pg_catalog.pg_try_advisory_xact_lock(
                   1346850639,
                   configured_shard
               )
          INTO shard_lock_acquired;
        IF NOT shard_lock_acquired THEN
            RAISE EXCEPTION USING
                ERRCODE = '55P03',
                MESSAGE = 'prediction catalog deployment shard is contended';
        END IF;

        LOCK TABLE engine.schema_migrations
            IN SHARE ROW EXCLUSIVE MODE NOWAIT;
        LOCK TABLE engine.deployment_shard
            IN SHARE ROW EXCLUSIVE MODE NOWAIT;
        LOCK TABLE trading.instruments
            IN SHARE ROW EXCLUSIVE MODE NOWAIT;

        SELECT count(*), min(shard_id)::integer
          INTO rechecked_count, rechecked_shard
          FROM engine.deployment_shard
         WHERE singleton;
        IF rechecked_count <> 1
           OR rechecked_shard IS DISTINCT FROM configured_shard
        THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'prediction catalog deployment shard changed during fencing';
        END IF;
    ELSE
        LOCK TABLE engine.schema_migrations
            IN SHARE ROW EXCLUSIVE MODE NOWAIT;
        LOCK TABLE engine.deployment_shard
            IN SHARE ROW EXCLUSIVE MODE NOWAIT;

        SELECT count(*), min(shard_id)::integer
          INTO rechecked_count, rechecked_shard
          FROM engine.deployment_shard
         WHERE singleton;
        IF rechecked_count > 1
           OR (rechecked_count = 1 AND rechecked_shard IS NULL)
        THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'prediction catalog deployment shard state is divergent';
        END IF;
        IF rechecked_count = 1 THEN
            configured_shard := rechecked_shard;
            SELECT pg_catalog.pg_try_advisory_xact_lock(
                       1346850639,
                       rechecked_shard
                   )
              INTO shard_lock_acquired;
            IF NOT shard_lock_acquired THEN
                RAISE EXCEPTION USING
                    ERRCODE = '55P03',
                    MESSAGE = 'prediction catalog deployment shard is contended';
            END IF;
        ELSE
            configured_shard := NULL;
        END IF;
        LOCK TABLE trading.instruments
            IN SHARE ROW EXCLUSIVE MODE NOWAIT;

        SELECT count(*), min(shard_id)::integer
          INTO rechecked_count, rechecked_shard
          FROM engine.deployment_shard
         WHERE singleton;
        IF (configured_shard IS NULL AND rechecked_count <> 0)
           OR (configured_shard IS NOT NULL
               AND (
                   rechecked_count <> 1
                   OR rechecked_shard IS DISTINCT FROM configured_shard
               ))
        THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'prediction catalog deployment shard changed during empty fencing';
        END IF;
    END IF;
END
$$;

-- The successor is accepted only from the exact immutable migration-43
-- journal. Validate the heap and primary-index access paths, the terminal
-- filename/checksum, and the ordered prefix digest before any prediction DDL.
DO $$
DECLARE
    heap_count bigint;
    index_count bigint;
    heap_hash bytea;
    index_hash bytea;
    terminal_count bigint;
    terminal_filename text;
    terminal_checksum bytea;
BEGIN
    PERFORM pg_catalog.set_config('enable_seqscan', 'on', true);
    PERFORM pg_catalog.set_config('enable_indexscan', 'off', true);
    PERFORM pg_catalog.set_config('enable_indexonlyscan', 'off', true);
    PERFORM pg_catalog.set_config('enable_bitmapscan', 'off', true);
    SELECT count(*),
           pg_catalog.sha256(
               pg_catalog.convert_to(
                   pg_catalog.string_agg(
                       migration.filename || ':' ||
                       pg_catalog.encode(migration.checksum, 'hex') || E'\n',
                       '' ORDER BY migration.filename
                   ),
                   'UTF8'
               )
           )
      INTO heap_count, heap_hash
      FROM engine.schema_migrations AS migration;

    PERFORM pg_catalog.set_config('enable_seqscan', 'off', true);
    PERFORM pg_catalog.set_config('enable_indexscan', 'on', true);
    PERFORM pg_catalog.set_config('enable_indexonlyscan', 'on', true);
    PERFORM pg_catalog.set_config('enable_bitmapscan', 'off', true);
    SELECT count(*),
           pg_catalog.sha256(
               pg_catalog.convert_to(
                   pg_catalog.string_agg(
                       migration.filename || ':' ||
                       pg_catalog.encode(migration.checksum, 'hex') || E'\n',
                       '' ORDER BY migration.filename
                   ),
                   'UTF8'
               )
           )
      INTO index_count, index_hash
      FROM engine.schema_migrations AS migration
     WHERE migration.filename >= '';

    SELECT count(*), max(migration.filename)
      INTO terminal_count, terminal_filename
      FROM engine.schema_migrations AS migration
     WHERE migration.filename =
         '20260731000200_phase3_runtime_authority_acl.up.sql';
    SELECT migration.checksum
      INTO terminal_checksum
      FROM engine.schema_migrations AS migration
     WHERE migration.filename =
         '20260731000200_phase3_runtime_authority_acl.up.sql';

    IF heap_count <> 43
       OR index_count <> 43
       OR heap_hash IS DISTINCT FROM pg_catalog.decode(
           '9389a467b2e94e0c2f09b9321384212d06ec9562321e801425e30f0d83f0fe5c',
           'hex'
       )
       OR index_hash IS DISTINCT FROM heap_hash
       OR terminal_count <> 1
       OR terminal_filename IS DISTINCT FROM
           '20260731000200_phase3_runtime_authority_acl.up.sql'
       OR terminal_checksum IS DISTINCT FROM pg_catalog.decode(
           '44d3a947a8068f5123f191acac8e0d99f24ca1f4060f1a5bebb9f1eaf006a8be',
           'hex'
       )
       OR (SELECT max(filename) FROM engine.schema_migrations) IS DISTINCT FROM
           '20260731000200_phase3_runtime_authority_acl.up.sql'
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog predecessor migration manifest is divergent';
    END IF;
END
$$;

-- No event trigger may execute hidden DDL or DML during this catalog change.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_event_trigger
         WHERE evtenabled <> 'D'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'enabled event triggers are forbidden during prediction catalog migration';
    END IF;
END
$$;

-- A non-owner default privilege could grant access to these new relations
-- after the explicit ACL block below. Preserve that evidence and fail closed.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_default_acl AS default_acl
         CROSS JOIN LATERAL aclexplode(default_acl.defaclacl) AS privilege
         WHERE privilege.grantee <> default_acl.defaclrole
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'non-owner default privileges are forbidden';
    END IF;
END
$$;

CREATE TABLE trading.prediction_events (
    event_id uuid NOT NULL,
    source_venue text NOT NULL
        CONSTRAINT prediction_events_source_venue_check
        CHECK (source_venue IN ('hyperliquid', 'polymarket', 'kalshi')),
    event_key text NOT NULL
        CONSTRAINT prediction_events_event_key_check
        CHECK (event_key <> ''),
    title text NOT NULL
        CONSTRAINT prediction_events_title_check
        CHECK (title <> ''),
    series text,
    status text NOT NULL
        CONSTRAINT prediction_events_status_check
        CHECK (status IN ('open', 'closed', 'resolved', 'settled')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT prediction_events_pkey PRIMARY KEY (event_id)
);

CREATE UNIQUE INDEX prediction_events_source_venue_event_key_idx
ON trading.prediction_events (
    source_venue COLLATE "C",
    event_key COLLATE "C"
);

CREATE TABLE trading.prediction_markets (
    market_id uuid NOT NULL,
    source_venue text NOT NULL
        CONSTRAINT prediction_markets_source_venue_check
        CHECK (source_venue IN ('hyperliquid', 'polymarket', 'kalshi')),
    market_key text NOT NULL
        CONSTRAINT prediction_markets_market_key_check
        CHECK (market_key <> ''),
    question text NOT NULL
        CONSTRAINT prediction_markets_question_check
        CHECK (question <> ''),
    resolution_time timestamptz,
    mutually_exclusive boolean NOT NULL,
    status text NOT NULL
        CONSTRAINT prediction_markets_status_check
        CHECK (status IN ('open', 'closed', 'resolved', 'settled')),
    event_id uuid,
    stage_label text
        CONSTRAINT prediction_markets_stage_label_check
        CHECK (stage_label IS NULL OR stage_label <> ''),
    stage_ordinal integer
        CONSTRAINT prediction_markets_stage_ordinal_check
        CHECK (stage_ordinal IS NULL OR stage_ordinal >= 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT prediction_markets_pkey PRIMARY KEY (market_id),
    CONSTRAINT prediction_markets_event_fk
        FOREIGN KEY (event_id)
        REFERENCES trading.prediction_events(event_id)
);

CREATE UNIQUE INDEX prediction_markets_source_venue_market_key_idx
ON trading.prediction_markets (
    source_venue COLLATE "C",
    market_key COLLATE "C"
);

CREATE INDEX prediction_markets_catalog_order_idx
ON trading.prediction_markets (
    stage_ordinal ASC NULLS LAST,
    created_at DESC,
    source_venue COLLATE "C",
    market_key COLLATE "C",
    market_id
);

CREATE TABLE trading.prediction_legs (
    instrument_id text NOT NULL,
    market_id uuid NOT NULL,
    display_name text NOT NULL
        CONSTRAINT prediction_legs_display_name_check
        CHECK (display_name <> ''),
    outcome_index integer NOT NULL
        CONSTRAINT prediction_legs_outcome_index_check
        CHECK (outcome_index >= 0),
    outcome_label text NOT NULL
        CONSTRAINT prediction_legs_outcome_label_check
        CHECK (outcome_label <> ''),
    enabled boolean NOT NULL,
    CONSTRAINT prediction_legs_pkey PRIMARY KEY (instrument_id),
    CONSTRAINT prediction_legs_instrument_fk
        FOREIGN KEY (instrument_id)
        REFERENCES trading.instruments(instrument_id),
    CONSTRAINT prediction_legs_market_fk
        FOREIGN KEY (market_id)
        REFERENCES trading.prediction_markets(market_id),
    CONSTRAINT prediction_legs_market_outcome_key
        UNIQUE (market_id, outcome_index)
);

CREATE INDEX prediction_legs_market_order_idx
ON trading.prediction_legs (
    market_id,
    outcome_index,
    instrument_id COLLATE "C"
);

-- Defaults are checked above, but scrub both relation- and column-level ACLs
-- explicitly so the raw post-migration graph cannot inherit a direct grant.
DO $$
DECLARE
    relation_name text;
    column_name text;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY[
        'trading.prediction_events',
        'trading.prediction_markets',
        'trading.prediction_legs'
    ]
    LOOP
        EXECUTE pg_catalog.format(
            'REVOKE ALL PRIVILEGES ON TABLE %s FROM PUBLIC',
            relation_name
        );
        EXECUTE pg_catalog.format(
            'REVOKE ALL PRIVILEGES ON TABLE %s FROM platformgo_api, platformgo_engine, platformgo_outbox, platformgo_projector, platformgo_realtime, platformgo_realtime_repair',
            relation_name
        );
        FOR column_name IN
            SELECT attribute.attname
              FROM pg_catalog.pg_attribute AS attribute
             WHERE attribute.attrelid = relation_name::pg_catalog.regclass
               AND attribute.attnum > 0
               AND NOT attribute.attisdropped
             ORDER BY attribute.attnum
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL PRIVILEGES (%I) ON TABLE %s FROM PUBLIC',
                column_name,
                relation_name
            );
            EXECUTE pg_catalog.format(
                'REVOKE ALL PRIVILEGES (%I) ON TABLE %s FROM platformgo_api, platformgo_engine, platformgo_outbox, platformgo_projector, platformgo_realtime, platformgo_realtime_repair',
                column_name,
                relation_name
            );
        END LOOP;
    END LOOP;
END
$$;

GRANT SELECT ON TABLE
    trading.prediction_events,
    trading.prediction_markets,
    trading.prediction_legs
TO platformgo_api;

-- Validate the raw owner/ACL graph and physical validity before binding the
-- new migration checksum. The owner receives its implicit eight PostgreSQL 19
-- table privileges (including MAINTAIN); platformgo_api receives exactly one non-grantable SELECT and
-- no column-level ACL is permitted.
DO $$
DECLARE
    owner_oid pg_catalog.oid;
    api_oid pg_catalog.oid := 'platformgo_api'::pg_catalog.regrole;
BEGIN
    SELECT relation.relowner
      INTO owner_oid
      FROM pg_catalog.pg_class AS relation
     WHERE relation.oid = 'engine.schema_migrations'::pg_catalog.regclass;
    IF owner_oid IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog migration owner is divergent';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_class AS relation
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
         WHERE namespace.nspname = 'trading'
           AND relation.relname IN (
                   'prediction_events',
                   'prediction_markets',
                   'prediction_legs'
               )
           AND (
               relation.relowner <> owner_oid
               OR relation.relkind <> 'r'
               OR relation.relpersistence <> 'p'
               OR relation.relrowsecurity
               OR relation.relforcerowsecurity
               OR relation.relispartition
           )
    ) OR (
        SELECT count(*)
          FROM pg_catalog.pg_class AS relation
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
         WHERE namespace.nspname = 'trading'
           AND relation.relname IN (
                   'prediction_events',
                   'prediction_markets',
                   'prediction_legs'
               )
    ) <> 3
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog relation ownership is divergent';
    END IF;

    IF EXISTS (
        WITH targets(relation_oid) AS (
            VALUES
                ('trading.prediction_events'::pg_catalog.regclass),
                ('trading.prediction_markets'::pg_catalog.regclass),
                ('trading.prediction_legs'::pg_catalog.regclass)
        )
        SELECT 1
          FROM targets
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = targets.relation_oid
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              COALESCE(
                  relation.relacl,
                  pg_catalog.acldefault('r', relation.relowner)
              )
          ) AS privilege
         WHERE NOT (
             privilege.grantee = owner_oid
             AND privilege.grantor = owner_oid
             AND NOT privilege.is_grantable
             AND privilege.privilege_type IN (
                     'INSERT', 'SELECT', 'UPDATE', 'DELETE',
                     'TRUNCATE', 'REFERENCES', 'TRIGGER', 'MAINTAIN'
                 )
             OR privilege.grantee = api_oid
             AND privilege.grantor = owner_oid
             AND NOT privilege.is_grantable
             AND privilege.privilege_type = 'SELECT'
         )
    ) OR EXISTS (
        WITH targets(relation_oid) AS (
            VALUES
                ('trading.prediction_events'::pg_catalog.regclass),
                ('trading.prediction_markets'::pg_catalog.regclass),
                ('trading.prediction_legs'::pg_catalog.regclass)
        )
        SELECT 1
          FROM targets
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = targets.relation_oid
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              COALESCE(
                  relation.relacl,
                  pg_catalog.acldefault('r', relation.relowner)
              )
          ) AS privilege
         GROUP BY targets.relation_oid
        HAVING count(*) <> 9
            OR count(*) FILTER (
                   WHERE privilege.grantee = owner_oid
               ) <> 8
            OR count(*) FILTER (
                   WHERE privilege.grantee = api_oid
               ) <> 1
    ) OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_attribute AS attribute
         WHERE attribute.attrelid IN (
                   'trading.prediction_events'::pg_catalog.regclass,
                   'trading.prediction_markets'::pg_catalog.regclass,
                   'trading.prediction_legs'::pg_catalog.regclass
               )
           AND attribute.attnum > 0
           AND NOT attribute.attisdropped
           AND attribute.attacl IS NOT NULL
    )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog relation ACL graph is divergent';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_index AS index_row
         WHERE index_row.indrelid IN (
                   'trading.prediction_events'::pg_catalog.regclass,
                   'trading.prediction_markets'::pg_catalog.regclass,
                   'trading.prediction_legs'::pg_catalog.regclass
               )
           AND (
               NOT index_row.indisvalid
               OR NOT index_row.indisready
               OR NOT index_row.indislive
           )
    ) OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_constraint AS constraint_row
         WHERE constraint_row.conrelid IN (
                   'trading.prediction_events'::pg_catalog.regclass,
                   'trading.prediction_markets'::pg_catalog.regclass,
                   'trading.prediction_legs'::pg_catalog.regclass
               )
           AND NOT constraint_row.convalidated
    )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'prediction catalog physical validity is divergent';
    END IF;
END
$$;

-- Advance the terminal bootstrap authority from tip 43 to this exact tip.
-- Accept only the byte-for-byte predecessor function, then perform the four
-- exact textual substitutions that bind its migration-count, filename, and
-- prefix-manifest fences to migration 44. CREATE OR REPLACE preserves the
-- function identity, owner, SECURITY DEFINER setting, and existing ACL.
DO $$
DECLARE
    function_oid pg_catalog.oid :=
        'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
            ::pg_catalog.regprocedure::pg_catalog.oid;
    function_definition text;
    old_count_fence constant text :=
        '(SELECT pg_catalog.count(*) FROM engine.schema_migrations) <> 43';
    new_count_fence constant text :=
        '(SELECT pg_catalog.count(*) FROM engine.schema_migrations) <> 44';
    old_filename constant text :=
        '20260731000200_phase3_runtime_authority_acl.up.sql';
    new_filename constant text :=
        '20260803000100_phase3_prediction_market_catalog.up.sql';
    old_prefix_count constant text := $count$        ) <> 42
        OR (
            SELECT migration.checksum$count$;
    new_prefix_count constant text := $count$        ) <> 43
        OR (
            SELECT migration.checksum$count$;
    old_prefix_hash constant text :=
        'e157f3a5ce0dabe82d8a4dd32d5913bf74973b8e984f1c779ac1d515bb378156';
    new_prefix_hash constant text :=
        '9389a467b2e94e0c2f09b9321384212d06ec9562321e801425e30f0d83f0fe5c';
BEGIN
    SELECT pg_catalog.pg_get_functiondef(function_oid)
      INTO function_definition;
    IF pg_catalog.sha256(
           pg_catalog.convert_to(function_definition, 'UTF8')
       ) <> pg_catalog.decode(
           '16c5c551fdd4570dd44dc8f9d17db54d5e4e4dc0ba95ece691d9ba44bb655f40',
           'hex'
       )
       OR (
           pg_catalog.length(function_definition) -
           pg_catalog.length(pg_catalog.replace(
               function_definition,
               old_count_fence,
               ''
           ))
       ) <> pg_catalog.length(old_count_fence)
       OR (
           pg_catalog.length(function_definition) -
           pg_catalog.length(pg_catalog.replace(
               function_definition,
               old_filename,
               ''
           ))
       ) <> 3 * pg_catalog.length(old_filename)
       OR pg_catalog.strpos(function_definition, old_prefix_count) = 0
       OR pg_catalog.strpos(function_definition, old_prefix_hash) = 0
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap exact-tip authority is divergent';
    END IF;

    function_definition := pg_catalog.replace(
        function_definition,
        old_count_fence,
        new_count_fence
    );
    function_definition := pg_catalog.replace(
        function_definition,
        old_filename,
        new_filename
    );
    function_definition := pg_catalog.replace(
        function_definition,
        old_prefix_count,
        new_prefix_count
    );
    function_definition := pg_catalog.replace(
        function_definition,
        old_prefix_hash,
        new_prefix_hash
    );
    IF pg_catalog.strpos(function_definition, old_count_fence) <> 0
       OR pg_catalog.strpos(function_definition, old_filename) <> 0
       OR pg_catalog.strpos(function_definition, old_prefix_count) <> 0
       OR pg_catalog.strpos(function_definition, old_prefix_hash) <> 0
       OR pg_catalog.strpos(function_definition, new_count_fence) = 0
       OR pg_catalog.strpos(function_definition, new_filename) = 0
       OR pg_catalog.strpos(function_definition, new_prefix_count) = 0
       OR pg_catalog.strpos(function_definition, new_prefix_hash) = 0
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap exact-tip authority replacement failed';
    END IF;
    EXECUTE function_definition;
END
$$;
