-- Close the remaining EngineStore authority ACLs inherited from legacy
-- migrator-owner defaults. This catalog-only cutover rewrites no relation.
-- The engine advisory fence and relation locks drain pre-cutover writers; a
-- timeout rolls back the ACLs and migration journal atomically.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
SET LOCAL search_path = pg_catalog;

DO $$
DECLARE
    configured_shard integer;
BEGIN
    SELECT shard_id::integer
      INTO configured_shard
      FROM engine.deployment_shard
     WHERE singleton;
    IF configured_shard IS NOT NULL THEN
        PERFORM pg_catalog.pg_advisory_xact_lock(1346850639, configured_shard);
    END IF;
END
$$;

-- Relation locks do not serialize ACL or trigger/function catalog DDL. Fence
-- the catalogs themselves with NOWAIT so an inverse DDL lock order fails
-- immediately instead of joining a deadlock. Retain these locks through the
-- ACL cutover, function replacement, migration journal insert, and commit.
LOCK TABLE pg_catalog.pg_default_acl IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_shdepend IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_depend IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_attribute IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_attrdef IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_constraint IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_index IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_proc IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_policy IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_authid IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_auth_members IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_inherits IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_namespace IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_event_trigger IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_class IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_am IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_rewrite IN SHARE MODE NOWAIT;
LOCK TABLE pg_catalog.pg_trigger IN SHARE MODE NOWAIT;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_event_trigger AS event_trigger
         WHERE event_trigger.evtenabled <> 'D'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'enabled event triggers are forbidden during runtime authority migration';
    END IF;
END
$$;

-- A stored owner default can grant authority to a role on a future object
-- without appearing in any current object ACL. Keep
-- the evidence intact and require an explicit operator repair before cutover.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_default_acl AS default_acl
         CROSS JOIN LATERAL pg_catalog.aclexplode(
             default_acl.defaclacl
         ) AS privilege
         WHERE privilege.grantee <> default_acl.defaclrole
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'non-owner default privileges are forbidden';
    END IF;
END
$$;

-- The migrator records this migration only after the SQL body completes.
-- Exact-validate the complete journal catalog so no default, trigger, rule, or
-- other hidden authority can execute after the ACL scrub but before the final
-- manifest reload. Preserve divergent evidence and fail before changing any
-- protected object.
DO $$
DECLARE
    relation_oid constant pg_catalog.oid :=
        'engine.schema_migrations'::pg_catalog.regclass;
    migration_owner_oid pg_catalog.oid;
    actual_columns text[];
    actual_constraints text[];
    actual_indexes text[];
    previous_enable_seqscan text :=
        pg_catalog.current_setting('enable_seqscan');
    previous_enable_indexscan text :=
        pg_catalog.current_setting('enable_indexscan');
    previous_enable_indexonlyscan text :=
        pg_catalog.current_setting('enable_indexonlyscan');
    previous_enable_bitmapscan text :=
        pg_catalog.current_setting('enable_bitmapscan');
    heap_manifest_count bigint;
    heap_manifest_hash bytea;
    index_manifest_count bigint;
    index_manifest_hash bytea;
BEGIN
    SELECT role.oid
      INTO migration_owner_oid
      FROM pg_catalog.pg_authid AS role
     WHERE role.rolname::text OPERATOR(pg_catalog.=) current_user;
    IF migration_owner_oid IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority migration owner is divergent';
    END IF;

    SELECT pg_catalog.array_agg(
               pg_catalog.format(
                   '%s:%s:%s:%s:%s:%s:%s',
                   attribute.attname,
                   pg_catalog.format_type(
                       attribute.atttypid,
                       attribute.atttypmod
                   ),
                   attribute.attnotnull,
                   attribute.atthasdef,
                   attribute.attidentity::text,
                   attribute.attgenerated::text,
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
           AND relation.relhasindex
           AND NOT relation.relrowsecurity
           AND NOT relation.relforcerowsecurity
           AND NOT relation.relispartition
    )
        OR actual_columns IS DISTINCT FROM ARRAY[
            'filename:text:t:f:::',
            'checksum:bytea:t:f:::',
            'applied_at:timestamp with time zone:t:t:::clock_timestamp()'
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
        OR NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_index AS index_row
             WHERE index_row.indrelid = relation_oid
               AND index_row.indexrelid =
                       'engine.schema_migrations_pkey'::pg_catalog.regclass
               AND index_row.indisunique
               AND index_row.indisprimary
               AND NOT index_row.indisexclusion
               AND index_row.indimmediate
               AND NOT index_row.indisclustered
               AND index_row.indisvalid
               AND NOT index_row.indcheckxmin
               AND index_row.indisready
               AND index_row.indislive
               AND NOT index_row.indisreplident
               AND NOT index_row.indnullsnotdistinct
               AND index_row.indexprs IS NULL
               AND index_row.indpred IS NULL
        )
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
              CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
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

    -- A previously false execution hint can leave heap rows absent from the
    -- primary index even after every catalog flag is restored. Prove the exact
    -- predecessor manifest independently through both physical access paths.
    PERFORM pg_catalog.set_config('enable_seqscan', 'on', true);
    PERFORM pg_catalog.set_config('enable_indexscan', 'off', true);
    PERFORM pg_catalog.set_config('enable_indexonlyscan', 'off', true);
    PERFORM pg_catalog.set_config('enable_bitmapscan', 'off', true);
    SELECT pg_catalog.count(*),
           pg_catalog.sha256(
               pg_catalog.convert_to(
                   pg_catalog.string_agg(
                       migration.filename || ':' ||
                       pg_catalog.encode(migration.checksum, 'hex') || E'\n',
                       ''
                       ORDER BY migration.filename
                   ),
                   'UTF8'
               )
           )
      INTO heap_manifest_count, heap_manifest_hash
      FROM engine.schema_migrations AS migration;

    PERFORM pg_catalog.set_config('enable_seqscan', 'off', true);
    PERFORM pg_catalog.set_config('enable_indexscan', 'on', true);
    PERFORM pg_catalog.set_config('enable_indexonlyscan', 'on', true);
    PERFORM pg_catalog.set_config('enable_bitmapscan', 'off', true);
    SELECT pg_catalog.count(*),
           pg_catalog.sha256(
               pg_catalog.convert_to(
                   pg_catalog.string_agg(
                       migration.filename || ':' ||
                       pg_catalog.encode(migration.checksum, 'hex') || E'\n',
                       ''
                       ORDER BY migration.filename
                   ),
                   'UTF8'
               )
           )
      INTO index_manifest_count, index_manifest_hash
      FROM engine.schema_migrations AS migration
     WHERE migration.filename >= '';

    IF heap_manifest_count <> 42
       OR index_manifest_count <> 42
       OR heap_manifest_hash IS DISTINCT FROM pg_catalog.decode(
           'e157f3a5ce0dabe82d8a4dd32d5913bf74973b8e984f1c779ac1d515bb378156',
           'hex'
       )
       OR index_manifest_hash IS DISTINCT FROM heap_manifest_hash
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'migration journal physical manifest is divergent';
    END IF;

    PERFORM pg_catalog.set_config(
        'enable_seqscan', previous_enable_seqscan, true
    );
    PERFORM pg_catalog.set_config(
        'enable_indexscan', previous_enable_indexscan, true
    );
    PERFORM pg_catalog.set_config(
        'enable_indexonlyscan', previous_enable_indexonlyscan, true
    );
    PERFORM pg_catalog.set_config(
        'enable_bitmapscan', previous_enable_bitmapscan, true
    );
END
$$;

-- Match the durable writer order and acquire every fence before inspecting or
-- changing authority.
LOCK TABLE engine.deployment_shard IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE engine.shard_ownership_epochs IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE engine.shard_checkpoints IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE engine.shard_faults IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE engine.duplicate_delivery_receipts IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE trading.risk_configs IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE market.books IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE ledger.transactions IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE ledger.entries IN SHARE ROW EXCLUSIVE MODE;

-- These relations are on the input-to-commit authority path. Reject every
-- rewrite rule and exact-validate every executable default while the relation
-- and protected-catalog fences are held. Otherwise owner-installed catalog
-- code could suppress an economic write or regain authority after the ACL
-- scrub within the same committed engine transaction.
DO $$
DECLARE
    actual_defaults text[];
    executable_catalog_hash bytea;
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_rewrite AS rule
         WHERE rule.ev_class = ANY (ARRAY[
                   'engine.deployment_shard'::pg_catalog.regclass,
                   'engine.shard_ownership_epochs'::pg_catalog.regclass,
                   'engine.shard_checkpoints'::pg_catalog.regclass,
                   'engine.shard_faults'::pg_catalog.regclass,
                   'engine.duplicate_delivery_receipts'::pg_catalog.regclass,
                   'trading.risk_configs'::pg_catalog.regclass,
                   'market.books'::pg_catalog.regclass,
                   'ledger.transactions'::pg_catalog.regclass,
                   'ledger.entries'::pg_catalog.regclass
               ]::pg_catalog.oid[])
    ) OR EXISTS (
        SELECT 1
          FROM pg_catalog.pg_class AS relation
         WHERE relation.oid = ANY (ARRAY[
                   'engine.deployment_shard'::pg_catalog.regclass,
                   'engine.shard_ownership_epochs'::pg_catalog.regclass,
                   'engine.shard_checkpoints'::pg_catalog.regclass,
                   'engine.shard_faults'::pg_catalog.regclass,
                   'engine.duplicate_delivery_receipts'::pg_catalog.regclass,
                   'trading.risk_configs'::pg_catalog.regclass,
                   'market.books'::pg_catalog.regclass,
                   'ledger.transactions'::pg_catalog.regclass,
                   'ledger.entries'::pg_catalog.regclass
               ]::pg_catalog.oid[])
           AND relation.relhasrules
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority rewrite-rule catalog is divergent';
    END IF;

    SELECT pg_catalog.array_agg(
               pg_catalog.format(
                   '%s.%s:%s:%s:%s',
                   namespace.nspname,
                   relation.relname,
                   attribute.attnum,
                   attribute.attname,
                   pg_catalog.pg_get_expr(
                       default_value.adbin,
                       default_value.adrelid
                   )
               )
               ORDER BY
                   namespace.nspname,
                   relation.relname,
                   attribute.attnum
           )
      INTO actual_defaults
      FROM pg_catalog.pg_attrdef AS default_value
      JOIN pg_catalog.pg_attribute AS attribute
        ON attribute.attrelid = default_value.adrelid
       AND attribute.attnum = default_value.adnum
      JOIN pg_catalog.pg_class AS relation
        ON relation.oid = default_value.adrelid
      JOIN pg_catalog.pg_namespace AS namespace
        ON namespace.oid = relation.relnamespace
     WHERE default_value.adrelid = ANY (ARRAY[
               'engine.deployment_shard'::pg_catalog.regclass,
               'engine.shard_ownership_epochs'::pg_catalog.regclass,
               'engine.shard_checkpoints'::pg_catalog.regclass,
               'engine.shard_faults'::pg_catalog.regclass,
               'engine.duplicate_delivery_receipts'::pg_catalog.regclass,
               'trading.risk_configs'::pg_catalog.regclass,
               'market.books'::pg_catalog.regclass,
               'ledger.transactions'::pg_catalog.regclass,
               'ledger.entries'::pg_catalog.regclass
           ]::pg_catalog.oid[]);

    IF actual_defaults IS DISTINCT FROM ARRAY[
        'engine.deployment_shard:1:singleton:true',
        'engine.deployment_shard:3:selected_at:clock_timestamp()',
        'engine.duplicate_delivery_receipts:10:committed_at:clock_timestamp()',
        'engine.shard_checkpoints:6:updated_at:clock_timestamp()',
        'engine.shard_faults:9:committed_at:clock_timestamp()',
        'engine.shard_ownership_epochs:3:acquired_at:clock_timestamp()',
        'ledger.transactions:5:created_at:clock_timestamp()',
        'market.books:6:updated_at:clock_timestamp()',
        'trading.risk_configs:5:version:1'
    ]::text[] THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority default catalog is divergent';
    END IF;

    -- Freeze every relation, column, constraint, index, and trigger field that can
    -- execute or redirect work on a later runtime read/write. The canonical
    -- 218-row PG19 tip-42 catalog deliberately excludes OIDs and owners; owner
    -- authority is checked separately below.
    WITH targets(relation_oid) AS (
        VALUES
            ('engine.deployment_shard'::pg_catalog.regclass),
            ('engine.shard_ownership_epochs'::pg_catalog.regclass),
            ('engine.shard_checkpoints'::pg_catalog.regclass),
            ('engine.shard_faults'::pg_catalog.regclass),
            ('engine.duplicate_delivery_receipts'::pg_catalog.regclass),
            ('trading.risk_configs'::pg_catalog.regclass),
            ('market.books'::pg_catalog.regclass),
            ('ledger.transactions'::pg_catalog.regclass),
            ('ledger.entries'::pg_catalog.regclass)
    ), trigger_oids(trigger_oid) AS (
        SELECT trigger_row.oid
          FROM pg_catalog.pg_trigger AS trigger_row
         WHERE trigger_row.tgrelid IN (
                   SELECT relation_oid FROM targets
               )
        UNION
        SELECT trigger_row.oid
          FROM pg_catalog.pg_trigger AS trigger_row
         WHERE trigger_row.tgconstraint IN (
                   SELECT constraint_row.oid
                     FROM pg_catalog.pg_constraint AS constraint_row
                    WHERE constraint_row.conrelid IN (
                              SELECT relation_oid FROM targets
                          )
               )
    ), catalog_lines AS (
        SELECT pg_catalog.jsonb_build_array(
                   'relation',
                   namespace.nspname,
                   relation.relname,
                   relation.relkind::text,
                   relation.relpersistence::text,
                   relation.relrowsecurity,
                   relation.relforcerowsecurity,
                   relation.relispartition,
                   relation.relhasrules,
                   relation.relhastriggers,
                   relation.relhassubclass,
                   relation.relchecks,
                   relation.relnatts,
                   relation.relhasindex,
                   relation.relreplident::text,
                   access_method.amname
               )::text AS line
          FROM targets
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = targets.relation_oid
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          LEFT JOIN pg_catalog.pg_am AS access_method
            ON access_method.oid = relation.relam
        UNION ALL
        SELECT pg_catalog.jsonb_build_array(
                   'column',
                   namespace.nspname,
                   relation.relname,
                   attribute.attnum,
                   attribute.attname,
                   pg_catalog.format_type(
                       attribute.atttypid,
                       attribute.atttypmod
                   ),
                   attribute.attnotnull,
                   attribute.atthasdef,
                   attribute.attidentity::text,
                   attribute.attgenerated::text,
                   attribute.attisdropped,
                   attribute.attcollation::pg_catalog.regcollation::text,
                   COALESCE(
                       pg_catalog.pg_get_expr(
                           default_value.adbin,
                           default_value.adrelid
                       ),
                       ''
                   )
               )::text
          FROM targets
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = targets.relation_oid
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          JOIN pg_catalog.pg_attribute AS attribute
            ON attribute.attrelid = relation.oid
           AND attribute.attnum > 0
          LEFT JOIN pg_catalog.pg_attrdef AS default_value
            ON default_value.adrelid = attribute.attrelid
           AND default_value.adnum = attribute.attnum
        UNION ALL
        SELECT pg_catalog.jsonb_build_array(
                   'constraint',
                   namespace.nspname,
                   relation.relname,
                   constraint_row.conname,
                   constraint_row.contype::text,
                   constraint_row.condeferrable,
                   constraint_row.condeferred,
                   constraint_row.convalidated,
                   constraint_row.connoinherit,
                   pg_catalog.pg_get_constraintdef(
                       constraint_row.oid,
                       false
                   )
               )::text
          FROM targets
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = targets.relation_oid
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          JOIN pg_catalog.pg_constraint AS constraint_row
            ON constraint_row.conrelid = relation.oid
        UNION ALL
        SELECT pg_catalog.jsonb_build_array(
                   'index',
                   namespace.nspname,
                   relation.relname,
                   index_relation.relname,
                   index_row.indisunique,
                   index_row.indisprimary,
                   index_row.indisexclusion,
                   index_row.indimmediate,
                   index_row.indisclustered,
                   index_row.indisvalid,
                   index_row.indcheckxmin,
                   index_row.indisready,
                   index_row.indislive,
                   index_row.indisreplident,
                   pg_catalog.pg_get_indexdef(index_row.indexrelid)
               )::text
          FROM targets
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = targets.relation_oid
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          JOIN pg_catalog.pg_index AS index_row
            ON index_row.indrelid = relation.oid
          JOIN pg_catalog.pg_class AS index_relation
            ON index_relation.oid = index_row.indexrelid
        UNION ALL
        SELECT pg_catalog.jsonb_build_array(
                   'trigger',
                   namespace.nspname,
                   relation.relname,
                   CASE
                       WHEN trigger_row.tgisinternal THEN ''
                       ELSE trigger_row.tgname
                   END,
                   trigger_row.tgfoid::pg_catalog.regprocedure::text,
                   COALESCE(constraint_row.conname, ''),
                   trigger_row.tgtype,
                   trigger_row.tgenabled::text,
                   trigger_row.tgisinternal,
                   trigger_row.tgparentid = 0,
                   COALESCE(
                       constrained_namespace.nspname || '.' ||
                           constrained_relation.relname,
                       ''
                   ),
                   COALESCE(
                       constraint_index_namespace.nspname || '.' ||
                           constraint_index.relname,
                       ''
                   ),
                   trigger_row.tgdeferrable,
                   trigger_row.tginitdeferred,
                   trigger_row.tgnargs,
                   trigger_row.tgattr::text,
                   pg_catalog.octet_length(trigger_row.tgargs),
                   COALESCE(
                       pg_catalog.pg_get_expr(
                           trigger_row.tgqual,
                           trigger_row.tgrelid
                       ),
                       ''
                   ),
                   COALESCE(trigger_row.tgoldtable, ''),
                   COALESCE(trigger_row.tgnewtable, '')
               )::text
          FROM trigger_oids
          JOIN pg_catalog.pg_trigger AS trigger_row
            ON trigger_row.oid = trigger_oids.trigger_oid
          JOIN pg_catalog.pg_class AS relation
            ON relation.oid = trigger_row.tgrelid
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
          LEFT JOIN pg_catalog.pg_constraint AS constraint_row
            ON constraint_row.oid = trigger_row.tgconstraint
          LEFT JOIN pg_catalog.pg_class AS constrained_relation
            ON constrained_relation.oid = trigger_row.tgconstrrelid
          LEFT JOIN pg_catalog.pg_namespace AS constrained_namespace
            ON constrained_namespace.oid = constrained_relation.relnamespace
          LEFT JOIN pg_catalog.pg_class AS constraint_index
            ON constraint_index.oid = trigger_row.tgconstrindid
          LEFT JOIN pg_catalog.pg_namespace AS constraint_index_namespace
            ON constraint_index_namespace.oid = constraint_index.relnamespace
    )
    SELECT pg_catalog.sha256(
               pg_catalog.convert_to(
                   pg_catalog.string_agg(line, E'\n' ORDER BY line),
                   'UTF8'
               )
           )
      INTO executable_catalog_hash
      FROM catalog_lines;

    IF executable_catalog_hash <> pg_catalog.decode(
           '33153843089d58c1179df5828b464281bd1522752d5804dea6714270d5742769',
           'hex'
       )
       OR EXISTS (
           SELECT 1
             FROM pg_catalog.pg_inherits AS inheritance
            WHERE inheritance.inhrelid = ANY (ARRAY[
                      'engine.deployment_shard'::pg_catalog.regclass,
                      'engine.shard_ownership_epochs'::pg_catalog.regclass,
                      'engine.shard_checkpoints'::pg_catalog.regclass,
                      'engine.shard_faults'::pg_catalog.regclass,
                      'engine.duplicate_delivery_receipts'::pg_catalog.regclass,
                      'trading.risk_configs'::pg_catalog.regclass,
                      'market.books'::pg_catalog.regclass,
                      'ledger.transactions'::pg_catalog.regclass,
                      'ledger.entries'::pg_catalog.regclass
                  ]::pg_catalog.oid[])
               OR inheritance.inhparent = ANY (ARRAY[
                      'engine.deployment_shard'::pg_catalog.regclass,
                      'engine.shard_ownership_epochs'::pg_catalog.regclass,
                      'engine.shard_checkpoints'::pg_catalog.regclass,
                      'engine.shard_faults'::pg_catalog.regclass,
                      'engine.duplicate_delivery_receipts'::pg_catalog.regclass,
                      'trading.risk_configs'::pg_catalog.regclass,
                      'market.books'::pg_catalog.regclass,
                      'ledger.transactions'::pg_catalog.regclass,
                      'ledger.entries'::pg_catalog.regclass
                  ]::pg_catalog.oid[])
       )
       OR EXISTS (
           SELECT 1
             FROM pg_catalog.pg_policy AS policy
            WHERE policy.polrelid = ANY (ARRAY[
                      'engine.deployment_shard'::pg_catalog.regclass,
                      'engine.shard_ownership_epochs'::pg_catalog.regclass,
                      'engine.shard_checkpoints'::pg_catalog.regclass,
                      'engine.shard_faults'::pg_catalog.regclass,
                      'engine.duplicate_delivery_receipts'::pg_catalog.regclass,
                      'trading.risk_configs'::pg_catalog.regclass,
                      'market.books'::pg_catalog.regclass,
                      'ledger.transactions'::pg_catalog.regclass,
                      'ledger.entries'::pg_catalog.regclass
                  ]::pg_catalog.oid[])
       )
       OR EXISTS (
           SELECT 1
             FROM pg_catalog.pg_attrdef AS default_value
             LEFT JOIN pg_catalog.pg_depend AS dependency
               ON dependency.classid =
                      'pg_catalog.pg_attrdef'::pg_catalog.regclass
              AND dependency.objid = default_value.oid
              AND dependency.objsubid = 0
            WHERE default_value.adrelid = ANY (ARRAY[
                      'engine.deployment_shard'::pg_catalog.regclass,
                      'engine.shard_ownership_epochs'::pg_catalog.regclass,
                      'engine.shard_checkpoints'::pg_catalog.regclass,
                      'engine.shard_faults'::pg_catalog.regclass,
                      'engine.duplicate_delivery_receipts'::pg_catalog.regclass,
                      'trading.risk_configs'::pg_catalog.regclass,
                      'market.books'::pg_catalog.regclass,
                      'ledger.transactions'::pg_catalog.regclass,
                      'ledger.entries'::pg_catalog.regclass
                  ]::pg_catalog.oid[])
            GROUP BY
                default_value.oid,
                default_value.adrelid,
                default_value.adnum
           HAVING pg_catalog.count(dependency.objid) <> 1
               OR pg_catalog.bool_or(
                      dependency.refclassid <>
                          'pg_catalog.pg_class'::pg_catalog.regclass
                      OR dependency.refobjid <> default_value.adrelid
                      OR dependency.refobjsubid <> default_value.adnum
                      OR dependency.deptype <> 'a'
                  )
       )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority executable catalog is divergent';
    END IF;
END
$$;

-- A restored exact catalog does not prove that enforcement was never disabled.
-- With every target writer drained and locked, reject latent FK or ledger
-- corruption before the ACL scrub can bless it as tip 43.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM engine.shard_ownership_epochs AS child
          LEFT JOIN engine.deployment_shard AS parent
            ON parent.shard_id = child.shard_id
         WHERE parent.shard_id IS NULL
    ) OR EXISTS (
        SELECT 1
          FROM engine.shard_checkpoints AS child
          LEFT JOIN engine.deployment_shard AS parent
            ON parent.shard_id = child.shard_id
         WHERE parent.shard_id IS NULL
    ) OR EXISTS (
        SELECT 1
          FROM engine.shard_faults AS child
          LEFT JOIN engine.deployment_shard AS parent
            ON parent.shard_id = child.shard_id
         WHERE parent.shard_id IS NULL
    ) OR EXISTS (
        SELECT 1
          FROM engine.duplicate_delivery_receipts AS child
          LEFT JOIN engine.deployment_shard AS parent
            ON parent.shard_id = child.shard_id
         WHERE parent.shard_id IS NULL
    ) OR EXISTS (
        SELECT 1
          FROM trading.risk_configs AS child
          LEFT JOIN trading.accounts AS parent
            ON parent.account_id = child.account_id
         WHERE parent.account_id IS NULL
    ) OR EXISTS (
        SELECT 1
          FROM trading.risk_configs AS child
          LEFT JOIN trading.instruments AS parent
            ON parent.instrument_id = child.instrument_id
         WHERE parent.instrument_id IS NULL
    ) OR EXISTS (
        SELECT 1
          FROM market.books AS child
          LEFT JOIN trading.instruments AS parent
            ON parent.instrument_id = child.instrument_id
         WHERE parent.instrument_id IS NULL
    ) OR EXISTS (
        SELECT 1
          FROM ledger.entries AS child
          LEFT JOIN ledger.transactions AS parent
            ON parent.transaction_id = child.transaction_id
         WHERE parent.transaction_id IS NULL
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority foreign-key state is divergent';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM ledger.transactions AS transaction_row
         WHERE NOT EXISTS (
                   SELECT 1
                     FROM ledger.entries AS entry
                    WHERE entry.transaction_id =
                              transaction_row.transaction_id
               )
    ) OR EXISTS (
        SELECT 1
          FROM ledger.entries AS entry
         GROUP BY entry.transaction_id, entry.currency
        HAVING pg_catalog.sum(entry.amount) <> 0::numeric
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority ledger state is divergent';
    END IF;
END
$$;

DO $$
DECLARE
    required_role text;
    bootstrap_role_oid pg_catalog.oid :=
        'platformgo_admin_bootstrap'::pg_catalog.regrole;
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
              FROM pg_catalog.pg_authid AS role
             WHERE role.rolname::text OPERATOR(pg_catalog.=) required_role
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

    IF NOT EXISTS (
        SELECT 1
          FROM pg_catalog.pg_authid AS role
         WHERE role.oid = bootstrap_role_oid
           AND role.rolname::text OPERATOR(pg_catalog.=)
               'platformgo_admin_bootstrap'
           AND NOT role.rolcanlogin
           AND NOT role.rolsuper
           AND NOT role.rolcreatedb
           AND NOT role.rolcreaterole
           AND NOT role.rolreplication
           AND NOT role.rolbypassrls
    )
       OR EXISTS (
           SELECT 1
             FROM pg_catalog.pg_auth_members AS membership
            WHERE membership.member = bootstrap_role_oid
               OR membership.roleid = bootstrap_role_oid
       )
       OR (
           SELECT pg_catalog.count(*)
             FROM pg_catalog.pg_shdepend AS dependency
            WHERE dependency.refclassid =
                      'pg_catalog.pg_authid'::pg_catalog.regclass
              AND dependency.refobjid = bootstrap_role_oid
       ) <> 2
       OR EXISTS (
           SELECT 1
             FROM pg_catalog.pg_shdepend AS dependency
            WHERE dependency.refclassid =
                      'pg_catalog.pg_authid'::pg_catalog.regclass
              AND dependency.refobjid = bootstrap_role_oid
              AND NOT (
                  dependency.deptype = 'a'
                  AND (
                      (
                          dependency.classid =
                              'pg_catalog.pg_namespace'::pg_catalog.regclass
                          AND dependency.objid =
                              'identity'::pg_catalog.regnamespace
                      )
                      OR (
                          dependency.classid =
                              'pg_catalog.pg_proc'::pg_catalog.regclass
                          AND dependency.objid =
                              'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'::pg_catalog.regprocedure
                      )
                  )
              )
       )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '42501',
            MESSAGE = 'required pre-provisioned admin bootstrap role is missing or unsafe';
    END IF;
END
$$;

DO $$
DECLARE
    owner_oid pg_catalog.oid;
    function_oid constant pg_catalog.oid :=
        'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
            ::pg_catalog.regprocedure::pg_catalog.oid;
BEGIN
    SELECT role.oid
      INTO owner_oid
      FROM pg_catalog.pg_authid AS role
     WHERE role.rolname::text OPERATOR(pg_catalog.=) current_user;
    IF owner_oid IS NULL
       OR NOT EXISTS (
           SELECT 1
             FROM pg_catalog.pg_proc AS procedure
             JOIN pg_catalog.pg_language AS language
               ON language.oid = procedure.prolang
            WHERE procedure.oid = function_oid
              AND procedure.proowner = owner_oid
              AND procedure.prosecdef
              AND NOT procedure.proleakproof
              AND NOT procedure.proisstrict
              AND procedure.prokind = 'f'
              AND procedure.provolatile = 'v'
              AND procedure.proparallel = 'u'
              AND procedure.pronargs = 6
              AND procedure.proargtypes =
                  '25 17 25 2950 25 17'::pg_catalog.oidvector
              AND procedure.prorettype = 'record'::pg_catalog.regtype
              AND language.lanname = 'plpgsql'
              AND COALESCE(procedure.proconfig, ARRAY[]::text[]) = ARRAY[
                  'search_path=pg_catalog',
                  'lock_timeout=5s',
                  'statement_timeout=10s'
              ]
              AND pg_catalog.sha256(
                  pg_catalog.convert_to(
                      pg_catalog.pg_get_functiondef(procedure.oid),
                      'UTF8'
                  )
              ) = pg_catalog.decode(
                  '4703c998f0c288324b9142fd857d223aaa48bc23da17cb21b1d953b96e147340',
                  'hex'
              )
       )
       OR (
           SELECT pg_catalog.count(*)
             FROM pg_catalog.pg_proc AS procedure
             CROSS JOIN LATERAL pg_catalog.aclexplode(
                 COALESCE(
                     procedure.proacl,
                     pg_catalog.acldefault('f', procedure.proowner)
                 )
             ) AS privilege
            WHERE procedure.oid = function_oid
              AND (
                  privilege.privilege_type <> 'EXECUTE'
                  OR privilege.is_grantable
                  OR privilege.grantor <> owner_oid
                  OR privilege.grantee NOT IN (
                      owner_oid,
                      'platformgo_admin_bootstrap'::pg_catalog.regrole
                  )
              )
       ) <> 0
       OR (
           SELECT pg_catalog.count(*)
             FROM pg_catalog.pg_proc AS procedure
             CROSS JOIN LATERAL pg_catalog.aclexplode(
                 COALESCE(
                     procedure.proacl,
                     pg_catalog.acldefault('f', procedure.proowner)
                 )
             ) AS privilege
            WHERE procedure.oid = function_oid
       ) <> 2
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'admin bootstrap function authority is divergent';
    END IF;

    IF NOT EXISTS (
        SELECT 1
          FROM pg_catalog.pg_namespace AS namespace
         WHERE namespace.nspname = 'identity'
           AND namespace.nspowner = owner_oid
    )
       OR (
           SELECT pg_catalog.count(*)
             FROM pg_catalog.pg_namespace AS namespace
             CROSS JOIN LATERAL pg_catalog.aclexplode(
                 COALESCE(
                     namespace.nspacl,
                     pg_catalog.acldefault('n', namespace.nspowner)
                 )
             ) AS privilege
            WHERE namespace.nspname = 'identity'
              AND NOT (
                  privilege.grantor = owner_oid
                  AND NOT privilege.is_grantable
                  AND (
                      (
                          privilege.grantee = owner_oid
                          AND privilege.privilege_type IN ('CREATE', 'USAGE')
                      )
                      OR (
                          privilege.grantee IN (
                              'platformgo_api'::pg_catalog.regrole,
                              'platformgo_engine'::pg_catalog.regrole,
                              'platformgo_admin_bootstrap'::pg_catalog.regrole
                          )
                          AND privilege.privilege_type = 'USAGE'
                      )
                  )
              )
       ) <> 0
       OR (
           SELECT pg_catalog.count(*)
             FROM pg_catalog.pg_namespace AS namespace
             CROSS JOIN LATERAL pg_catalog.aclexplode(
                 COALESCE(
                     namespace.nspacl,
                     pg_catalog.acldefault('n', namespace.nspowner)
                 )
             ) AS privilege
            WHERE namespace.nspname = 'identity'
       ) <> 5
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'identity schema authority is divergent';
    END IF;

    -- Schema ownership and CREATE authority are executable catalog authority:
    -- a hostile owner could drop the complete economic schema after this
    -- migration had otherwise accepted its relation-level catalog.
    IF (
        SELECT pg_catalog.count(*)
          FROM pg_catalog.pg_namespace AS namespace
         WHERE namespace.nspname IN ('engine', 'trading', 'market', 'ledger')
           AND namespace.nspowner = owner_oid
    ) <> 4
       OR EXISTS (
           WITH expected(
               schema_name,
               grantee_oid,
               privilege_type,
               grantor_oid,
               is_grantable
           ) AS (
               VALUES
                   ('engine', owner_oid, 'CREATE', owner_oid, false),
                   ('engine', owner_oid, 'USAGE', owner_oid, false),
                   ('engine', 'platformgo_api'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('engine', 'platformgo_engine'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('engine', 'platformgo_outbox'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('engine', 'platformgo_projector'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('engine', 'platformgo_realtime'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('engine', 'platformgo_realtime_repair'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('trading', owner_oid, 'CREATE', owner_oid, false),
                   ('trading', owner_oid, 'USAGE', owner_oid, false),
                   ('trading', 'platformgo_api'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('trading', 'platformgo_engine'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('trading', 'platformgo_outbox'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('market', owner_oid, 'CREATE', owner_oid, false),
                   ('market', owner_oid, 'USAGE', owner_oid, false),
                   ('market', 'platformgo_api'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('market', 'platformgo_engine'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('ledger', owner_oid, 'CREATE', owner_oid, false),
                   ('ledger', owner_oid, 'USAGE', owner_oid, false),
                   ('ledger', 'platformgo_api'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false),
                   ('ledger', 'platformgo_engine'::pg_catalog.regrole::pg_catalog.oid, 'USAGE', owner_oid, false)
           ),
           actual(
               schema_name,
               grantee_oid,
               privilege_type,
               grantor_oid,
               is_grantable
           ) AS (
               SELECT
                   namespace.nspname,
                   privilege.grantee,
                   privilege.privilege_type,
                   privilege.grantor,
                   privilege.is_grantable
                 FROM pg_catalog.pg_namespace AS namespace
                 CROSS JOIN LATERAL pg_catalog.aclexplode(
                     COALESCE(
                         namespace.nspacl,
                         pg_catalog.acldefault('n', namespace.nspowner)
                     )
                 ) AS privilege
                WHERE namespace.nspname IN (
                    'engine',
                    'trading',
                    'market',
                    'ledger'
                )
           ),
           difference AS (
               (
                   SELECT schema_name, grantee_oid, privilege_type, grantor_oid, is_grantable
                     FROM actual
                   EXCEPT ALL
                   SELECT schema_name, grantee_oid, privilege_type, grantor_oid, is_grantable
                     FROM expected
               )
               UNION ALL
               (
                   SELECT schema_name, grantee_oid, privilege_type, grantor_oid, is_grantable
                     FROM expected
                   EXCEPT ALL
                   SELECT schema_name, grantee_oid, privilege_type, grantor_oid, is_grantable
                     FROM actual
               )
           )
           SELECT 1
             FROM difference
       )
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority schema ownership or ACL is divergent';
    END IF;
END
$$;

-- Revoking TRIGGER cannot remove objects created while the grant existed.
-- Trust the rows only when the complete current-main trigger graph and every
-- bound trigger function still match the frozen tip-42 catalog.
DO $$
BEGIN
    IF EXISTS (
        WITH expected(
            relation_name,
            trigger_name,
            function_name,
            trigger_type,
            enabled
        ) AS (
            VALUES
                ('engine.deployment_shard', 'deployment_shard_is_immutable', 'engine.reject_immutable_change()', 27::smallint, 'O'::"char"),
                ('engine.shard_ownership_epochs', 'shard_ownership_epochs_require_runtime_schema_revision', 'engine.require_runtime_schema_revision()', 23::smallint, 'O'::"char"),
                ('engine.shard_checkpoints', 'shard_checkpoints_require_decision_hash_v4_runtime', 'engine.require_decision_hash_v4_runtime()', 23::smallint, 'O'::"char"),
                ('engine.shard_checkpoints', 'shard_checkpoints_require_runtime_schema_revision', 'engine.require_runtime_schema_revision()', 23::smallint, 'O'::"char"),
                ('engine.shard_faults', 'shard_faults_are_immutable', 'engine.reject_immutable_change()', 27::smallint, 'O'::"char"),
                ('engine.shard_faults', 'shard_faults_require_decision_hash_v4_runtime', 'engine.require_decision_hash_v4_runtime()', 7::smallint, 'O'::"char"),
                ('engine.shard_faults', 'shard_faults_require_runtime_schema_revision', 'engine.require_runtime_schema_revision()', 7::smallint, 'O'::"char"),
                ('engine.duplicate_delivery_receipts', 'duplicate_delivery_receipts_are_immutable', 'engine.reject_immutable_change()', 27::smallint, 'O'::"char"),
                ('engine.duplicate_delivery_receipts', 'duplicate_delivery_receipts_require_decision_hash_v4_runtime', 'engine.require_decision_hash_v4_runtime()', 7::smallint, 'O'::"char"),
                ('engine.duplicate_delivery_receipts', 'duplicate_delivery_receipts_require_runtime_schema_revision', 'engine.require_runtime_schema_revision()', 7::smallint, 'O'::"char"),
                ('engine.duplicate_delivery_receipts', 'duplicate_receipts_require_decision_hash_v3', 'engine.require_duplicate_delivery_hash_v3()', 7::smallint, 'O'::"char"),
                ('engine.duplicate_delivery_receipts', 'duplicate_receipts_require_decision_hash_v4', 'engine.require_duplicate_delivery_hash_v4()', 7::smallint, 'O'::"char"),
                ('ledger.transactions', 'ledger_transactions_are_immutable', 'engine.reject_immutable_change()', 27::smallint, 'O'::"char"),
                ('ledger.entries', 'ledger_entries_are_immutable', 'engine.reject_immutable_change()', 27::smallint, 'O'::"char"),
                ('ledger.entries', 'ledger_transaction_must_balance', 'ledger.assert_transaction_balanced()', 29::smallint, 'O'::"char")
        ),
        actual(
            relation_name,
            trigger_name,
            function_name,
            trigger_type,
            enabled
        ) AS (
            SELECT
                trigger_row.tgrelid::pg_catalog.regclass::text,
                trigger_row.tgname::text,
                trigger_row.tgfoid::pg_catalog.regprocedure::text,
                trigger_row.tgtype,
                trigger_row.tgenabled
              FROM pg_catalog.pg_trigger AS trigger_row
             WHERE NOT trigger_row.tgisinternal
               AND trigger_row.tgrelid = ANY (ARRAY[
                   'engine.deployment_shard'::pg_catalog.regclass::pg_catalog.oid,
                   'engine.shard_ownership_epochs'::pg_catalog.regclass::pg_catalog.oid,
                   'engine.shard_checkpoints'::pg_catalog.regclass::pg_catalog.oid,
                   'engine.shard_faults'::pg_catalog.regclass::pg_catalog.oid,
                   'engine.duplicate_delivery_receipts'::pg_catalog.regclass::pg_catalog.oid,
                   'trading.risk_configs'::pg_catalog.regclass::pg_catalog.oid,
                   'market.books'::pg_catalog.regclass::pg_catalog.oid,
                   'ledger.transactions'::pg_catalog.regclass::pg_catalog.oid,
                   'ledger.entries'::pg_catalog.regclass::pg_catalog.oid
               ])
        )
        (
            SELECT relation_name, trigger_name, function_name, trigger_type, enabled
              FROM expected
            EXCEPT
            SELECT relation_name, trigger_name, function_name, trigger_type, enabled
              FROM actual
        )
        UNION ALL
        (
            SELECT relation_name, trigger_name, function_name, trigger_type, enabled
              FROM actual
            EXCEPT
            SELECT relation_name, trigger_name, function_name, trigger_type, enabled
              FROM expected
        )
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority trigger catalog is divergent';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM pg_catalog.pg_trigger AS trigger_row
         WHERE NOT trigger_row.tgisinternal
           AND trigger_row.tgrelid = ANY (ARRAY[
               'engine.deployment_shard'::pg_catalog.regclass::pg_catalog.oid,
               'engine.shard_ownership_epochs'::pg_catalog.regclass::pg_catalog.oid,
               'engine.shard_checkpoints'::pg_catalog.regclass::pg_catalog.oid,
               'engine.shard_faults'::pg_catalog.regclass::pg_catalog.oid,
               'engine.duplicate_delivery_receipts'::pg_catalog.regclass::pg_catalog.oid,
               'trading.risk_configs'::pg_catalog.regclass::pg_catalog.oid,
               'market.books'::pg_catalog.regclass::pg_catalog.oid,
               'ledger.transactions'::pg_catalog.regclass::pg_catalog.oid,
               'ledger.entries'::pg_catalog.regclass::pg_catalog.oid
           ])
           AND (
               trigger_row.tgparentid <> 0
               OR trigger_row.tgconstrrelid <> 0
               OR trigger_row.tgconstrindid <> 0
               OR trigger_row.tgnargs <> 0
               OR trigger_row.tgattr <> ''::pg_catalog.int2vector
               OR pg_catalog.octet_length(trigger_row.tgargs) <> 0
               OR trigger_row.tgqual IS NOT NULL
               OR trigger_row.tgoldtable IS NOT NULL
               OR trigger_row.tgnewtable IS NOT NULL
               OR (
                   trigger_row.tgname = 'ledger_transaction_must_balance'
                   AND (
                       trigger_row.tgrelid <> 'ledger.entries'::pg_catalog.regclass
                       OR trigger_row.tgconstraint = 0
                       OR NOT trigger_row.tgdeferrable
                       OR NOT trigger_row.tginitdeferred
                   )
               )
               OR (
                   trigger_row.tgname <> 'ledger_transaction_must_balance'
                   AND (
                       trigger_row.tgconstraint <> 0
                       OR trigger_row.tgdeferrable
                       OR trigger_row.tginitdeferred
                   )
               )
           )
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority trigger metadata is divergent';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM (
              VALUES
                  ('engine.reject_immutable_change()'::pg_catalog.regprocedure, false, ARRAY[]::text[], 'a5f2a8ffb83cfa112d2f564a0d7f3909f7e7e40a7205931ce79f8f8623b53423'),
                  ('engine.require_decision_hash_v4_runtime()'::pg_catalog.regprocedure, false, ARRAY['search_path=pg_catalog']::text[], '53b4d7d870bab724f00553514b8894f3ee21eaa1a347d6fab296bf69353d06d7'),
                  ('engine.require_duplicate_delivery_hash_v3()'::pg_catalog.regprocedure, false, ARRAY[]::text[], 'b076ede7ec5cdc41b2e7cbfea0d4cc055bcdb5089d9e63353cfefc52872788e4'),
                  ('engine.require_duplicate_delivery_hash_v4()'::pg_catalog.regprocedure, false, ARRAY[]::text[], '942e3d18c80e49db2a350a26f8f3fba3177bea5ce7236f823c634f2fdd6e7ce8'),
                  ('engine.require_runtime_schema_revision()'::pg_catalog.regprocedure, false, ARRAY['search_path=pg_catalog']::text[], '80a7a896af4c54713f910ab7a80d968b58c64caab4c2f8f84fc11e98e37a810c'),
                  ('ledger.assert_transaction_balanced()'::pg_catalog.regprocedure, false, ARRAY[]::text[], '84b0cbf619b35b376e4ce7244bee0f3b3969c59d7c908ead8330ebc212d52a06')
          ) AS expected(function_oid, security_definer, settings, definition_hash)
          JOIN pg_catalog.pg_proc AS procedure ON procedure.oid = expected.function_oid
         WHERE procedure.proowner <>
                   (SELECT role.oid FROM pg_catalog.pg_roles AS role WHERE role.rolname = current_user)
            OR procedure.prosecdef <> expected.security_definer
            OR COALESCE(procedure.proconfig, ARRAY[]::text[]) <> expected.settings
            OR pg_catalog.sha256(
                   pg_catalog.convert_to(
                       pg_catalog.pg_get_functiondef(procedure.oid),
                       'UTF8'
                   )
               ) <> pg_catalog.decode(expected.definition_hash, 'hex')
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'runtime authority trigger function catalog is divergent';
    END IF;
END
$$;

-- An unexpected role that could mutate an authority or append-only relation
-- makes its pre-cutover history untrustworthy. Preserve the catalog evidence
-- and stop; do not silently bless it by revoking the grant.
DO $$
DECLARE
    engine_role pg_catalog.oid := 'platformgo_engine'::pg_catalog.regrole;
    relation_oid pg_catalog.oid;
    relation_owner pg_catalog.oid;
    allowed_engine_table_mutations text[];
BEGIN
    FOREACH relation_oid IN ARRAY ARRAY[
        'engine.deployment_shard'::pg_catalog.regclass::pg_catalog.oid,
        'engine.shard_ownership_epochs'::pg_catalog.regclass::pg_catalog.oid,
        'engine.shard_checkpoints'::pg_catalog.regclass::pg_catalog.oid,
        'engine.shard_faults'::pg_catalog.regclass::pg_catalog.oid,
        'engine.duplicate_delivery_receipts'::pg_catalog.regclass::pg_catalog.oid,
        'trading.risk_configs'::pg_catalog.regclass::pg_catalog.oid,
        'market.books'::pg_catalog.regclass::pg_catalog.oid,
        'ledger.transactions'::pg_catalog.regclass::pg_catalog.oid,
        'ledger.entries'::pg_catalog.regclass::pg_catalog.oid
    ]
    LOOP
        SELECT relation.relowner
          INTO relation_owner
          FROM pg_catalog.pg_class AS relation
         WHERE relation.oid = relation_oid;
        IF relation_owner <> (
            SELECT role.oid
              FROM pg_catalog.pg_roles AS role
             WHERE role.rolname = current_user
        )
        THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'runtime authority relation has an unexpected owner';
        END IF;

        allowed_engine_table_mutations := CASE relation_oid
            WHEN 'engine.shard_ownership_epochs'::pg_catalog.regclass::pg_catalog.oid
                THEN ARRAY['INSERT']::text[]
            WHEN 'engine.shard_checkpoints'::pg_catalog.regclass::pg_catalog.oid
                THEN ARRAY['INSERT', 'UPDATE']::text[]
            WHEN 'engine.shard_faults'::pg_catalog.regclass::pg_catalog.oid
                THEN ARRAY['INSERT']::text[]
            WHEN 'engine.duplicate_delivery_receipts'::pg_catalog.regclass::pg_catalog.oid
                THEN ARRAY['INSERT']::text[]
            WHEN 'trading.risk_configs'::pg_catalog.regclass::pg_catalog.oid
                THEN ARRAY['INSERT', 'UPDATE']::text[]
            WHEN 'market.books'::pg_catalog.regclass::pg_catalog.oid
                THEN ARRAY['INSERT', 'UPDATE']::text[]
            WHEN 'ledger.transactions'::pg_catalog.regclass::pg_catalog.oid
                THEN ARRAY['INSERT']::text[]
            WHEN 'ledger.entries'::pg_catalog.regclass::pg_catalog.oid
                THEN ARRAY['INSERT']::text[]
            ELSE ARRAY[]::text[]
        END;

        IF EXISTS (
            SELECT 1
              FROM pg_catalog.pg_class AS relation
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      relation.relacl,
                      pg_catalog.acldefault('r', relation.relowner)
                  )
              ) AS privilege
             WHERE relation.oid = relation_oid
               AND privilege.grantee NOT IN (relation.relowner, engine_role)
               AND privilege.privilege_type IN (
                   'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER',
                   'MAINTAIN'
               )
        ) OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_class AS relation
              CROSS JOIN LATERAL pg_catalog.aclexplode(
                  COALESCE(
                      relation.relacl,
                      pg_catalog.acldefault('r', relation.relowner)
                  )
              ) AS privilege
             WHERE relation.oid = relation_oid
               AND privilege.grantee = engine_role
               AND privilege.privilege_type IN (
                   'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER',
                   'MAINTAIN'
               )
               AND (
                   NOT privilege.privilege_type = ANY (allowed_engine_table_mutations)
                   OR privilege.grantor <> relation_owner
                   OR privilege.is_grantable
               )
        ) OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_attribute AS attribute
              CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
             WHERE attribute.attrelid = relation_oid
               AND attribute.attnum > 0
               AND NOT attribute.attisdropped
               AND privilege.grantee NOT IN (relation_owner, engine_role)
               AND privilege.privilege_type IN ('INSERT', 'UPDATE', 'REFERENCES')
        ) OR EXISTS (
            SELECT 1
              FROM pg_catalog.pg_attribute AS attribute
              CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
             WHERE attribute.attrelid = relation_oid
               AND attribute.attnum > 0
               AND NOT attribute.attisdropped
               AND privilege.grantee = engine_role
               AND privilege.privilege_type IN ('INSERT', 'UPDATE', 'REFERENCES')
               AND (
                   relation_oid <>
                       'engine.shard_ownership_epochs'::pg_catalog.regclass::pg_catalog.oid
                   OR privilege.privilege_type <> 'UPDATE'
                   OR attribute.attname NOT IN ('epoch', 'acquired_at')
                   OR privilege.grantor <> relation_owner
                   OR privilege.is_grantable
               )
        ) THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'runtime authority carried an unexpected mutation grant before cutover';
        END IF;
    END LOOP;

END
$$;

DO $$
DECLARE
    relation_oid pg_catalog.oid;
    unexpected_grantee pg_catalog.name;
    column_name pg_catalog.name;
BEGIN
    FOR relation_oid IN
        SELECT target_oid
          FROM unnest(ARRAY[
              'engine.deployment_shard'::pg_catalog.regclass::pg_catalog.oid,
              'engine.shard_ownership_epochs'::pg_catalog.regclass::pg_catalog.oid,
              'engine.shard_checkpoints'::pg_catalog.regclass::pg_catalog.oid,
              'engine.shard_faults'::pg_catalog.regclass::pg_catalog.oid,
              'engine.duplicate_delivery_receipts'::pg_catalog.regclass::pg_catalog.oid,
              'trading.risk_configs'::pg_catalog.regclass::pg_catalog.oid,
              'market.books'::pg_catalog.regclass::pg_catalog.oid,
              'ledger.transactions'::pg_catalog.regclass::pg_catalog.oid,
              'ledger.entries'::pg_catalog.regclass::pg_catalog.oid
          ]) WITH ORDINALITY AS target(target_oid, position)
         ORDER BY position
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
              JOIN pg_catalog.pg_roles AS role ON role.oid = privilege.grantee
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
              CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
              JOIN pg_catalog.pg_roles AS role ON role.oid = privilege.grantee
              JOIN pg_catalog.pg_class AS relation ON relation.oid = attribute.attrelid
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

GRANT SELECT ON TABLE engine.deployment_shard TO platformgo_api;
GRANT SELECT ON TABLE trading.risk_configs, market.books TO platformgo_api;

GRANT SELECT ON TABLE engine.deployment_shard TO platformgo_engine;
GRANT SELECT, INSERT ON TABLE engine.shard_ownership_epochs TO platformgo_engine;
GRANT UPDATE (epoch, acquired_at) ON TABLE engine.shard_ownership_epochs
    TO platformgo_engine;
GRANT SELECT, INSERT, UPDATE ON TABLE engine.shard_checkpoints TO platformgo_engine;
GRANT SELECT, INSERT ON TABLE
    engine.shard_faults,
    engine.duplicate_delivery_receipts
TO platformgo_engine;
GRANT SELECT, INSERT, UPDATE ON TABLE trading.risk_configs, market.books
    TO platformgo_engine;
GRANT SELECT, INSERT ON TABLE ledger.transactions, ledger.entries
    TO platformgo_engine;

-- The bootstrap authority intentionally binds itself to the exact migration
-- tip. Advance that fence without copying a second 1,200-line function body:
-- accept only the byte-for-byte current definition, make one exact textual
-- substitution, and require this migration to be the sole successor of the
-- immutable bootstrap migration. CREATE OR REPLACE preserves its identity,
-- owner, security-definer flag, settings, and existing execute ACL.
DO $$
DECLARE
    function_oid pg_catalog.oid :=
        'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
            ::pg_catalog.regprocedure::pg_catalog.oid;
    function_definition text;
    old_count_fence constant text :=
        '(SELECT pg_catalog.count(*) FROM engine.schema_migrations) <> 42';
    new_count_fence constant text :=
        '(SELECT pg_catalog.count(*) FROM engine.schema_migrations) <> 43';
    old_filename constant text :=
        '20260731000100_phase3_admin_bootstrap_authority.up.sql';
    new_filename constant text :=
        '20260731000200_phase3_runtime_authority_acl.up.sql';
    old_prefix_count constant text := $count$        ) <> 41
        OR (
            SELECT migration.checksum$count$;
    new_prefix_count constant text := $count$        ) <> 42
        OR (
            SELECT migration.checksum$count$;
    old_prefix_hash constant text :=
        '2b2fc2fc638c3a303e2811d5bf20a72e84c79994233bda21e53f6f342395c501';
    new_prefix_hash constant text :=
        'e157f3a5ce0dabe82d8a4dd32d5913bf74973b8e984f1c779ac1d515bb378156';
BEGIN
    SELECT pg_catalog.pg_get_functiondef(function_oid)
      INTO function_definition;
    IF pg_catalog.sha256(
           pg_catalog.convert_to(function_definition, 'UTF8')
       ) <> pg_catalog.decode(
           '4703c998f0c288324b9142fd857d223aaa48bc23da17cb21b1d953b96e147340',
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
