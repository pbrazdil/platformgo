-- Phase 3 broker-echo replay provenance and statement-mutation guards.
--
-- This is a forward-only correction after the legacy/current time-authority
-- discriminator and insert fence were installed atomically. Every runtime
-- remains stopped while it is applied. The table contains at most 1,000 rows.
-- The migration takes ACCESS EXCLUSIVE before catalog work, performs no heap
-- rewrite or backfill, and either commits the truncate guard and normalized
-- ACLs with its journal row or rolls back to the exact preceding tip for a
-- clean retry.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

LOCK TABLE identity.broker_echo_replays IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    integrity_applied_at timestamptz;
BEGIN
    IF to_regprocedure(
        'identity.guard_broker_echo_replay_insert()'
    ) IS NULL OR NOT EXISTS (
        SELECT 1
          FROM pg_catalog.pg_proc AS procedure
          JOIN pg_catalog.pg_language AS language
            ON language.oid = procedure.prolang
         WHERE procedure.oid =
               'identity.guard_broker_echo_replay_insert()'::pg_catalog.regprocedure
           AND language.lanname = 'plpgsql'
           AND procedure.prorettype = 'pg_catalog.trigger'::pg_catalog.regtype
           AND procedure.prokind = 'f'
           AND procedure.pronargs = 0
           AND NOT procedure.proretset
           AND NOT procedure.prosecdef
           AND NOT procedure.proisstrict
           AND procedure.provolatile = 'v'
           AND procedure.proparallel = 'u'
           AND procedure.proconfig =
               ARRAY['search_path=pg_catalog']::text[]
           AND procedure.proowner = (
               SELECT role.oid
                 FROM pg_catalog.pg_roles AS role
                WHERE role.rolname = CURRENT_USER
           )
           AND procedure.prosrc = $guard$
BEGIN
    IF NEW.postgres_time_authority IS DISTINCT FROM true THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'broker echo legacy time authority is migration-only';
    END IF;
    RETURN NEW;
END;
$guard$
    ) OR NOT EXISTS (
        SELECT 1
          FROM pg_catalog.pg_trigger AS trigger
         WHERE trigger.tgrelid =
               'identity.broker_echo_replays'::pg_catalog.regclass
           AND trigger.tgname =
               'broker_echo_replays_require_postgres_time_authority'
           AND trigger.tgfoid =
               'identity.guard_broker_echo_replay_insert()'::pg_catalog.regprocedure
           AND trigger.tgenabled = 'O'
           AND trigger.tgtype = 7
           AND NOT trigger.tgisinternal
           AND pg_catalog.pg_get_triggerdef(trigger.oid, true) =
               'CREATE TRIGGER ' ||
               'broker_echo_replays_require_postgres_time_authority ' ||
               'BEFORE INSERT ON identity.broker_echo_replays FOR EACH ROW ' ||
               'EXECUTE FUNCTION identity.guard_broker_echo_replay_insert()'
    ) OR (
        SELECT pg_catalog.array_agg(
                   trigger.tgenabled::text || ':' ||
                   pg_catalog.pg_get_triggerdef(trigger.oid, true)
                   ORDER BY trigger.tgname
               )
          FROM pg_catalog.pg_trigger AS trigger
         WHERE trigger.tgrelid =
               'identity.broker_echo_replays'::pg_catalog.regclass
           AND NOT trigger.tgisinternal
    ) IS DISTINCT FROM ARRAY[
        'O:CREATE TRIGGER broker_echo_replays_guard_mutation ' ||
        'BEFORE DELETE OR UPDATE ON identity.broker_echo_replays ' ||
        'FOR EACH ROW EXECUTE FUNCTION ' ||
        'identity.guard_broker_echo_replay_mutation()',
        'O:CREATE TRIGGER ' ||
        'broker_echo_replays_require_postgres_time_authority ' ||
        'BEFORE INSERT ON identity.broker_echo_replays FOR EACH ROW ' ||
        'EXECUTE FUNCTION identity.guard_broker_echo_replay_insert()'
    ]::text[]
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'broker echo integrity tip is missing its insert fence';
    END IF;

    SELECT migration.applied_at
      INTO STRICT integrity_applied_at
      FROM engine.schema_migrations AS migration
     WHERE migration.filename =
           '20260727000300_phase3_broker_echo_coverage_integrity.up.sql';

    IF EXISTS (
        SELECT 1
          FROM identity.broker_echo_replays AS replay
         WHERE NOT replay.postgres_time_authority
           AND replay.created_at > integrity_applied_at
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'broker echo replay contains a post-cutover legacy marker';
    END IF;
END;
$$;

CREATE FUNCTION identity.guard_broker_echo_replay_truncate()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = '55000',
        MESSAGE = 'broker echo replay authority cannot be truncated';
END;
$$;

CREATE TRIGGER broker_echo_replays_reject_truncate
BEFORE TRUNCATE ON identity.broker_echo_replays
FOR EACH STATEMENT
EXECUTE FUNCTION identity.guard_broker_echo_replay_truncate();

DO $$
DECLARE
    object_oid oid;
    unexpected_grantee name;
BEGIN
    FOREACH object_oid IN ARRAY ARRAY[
        'identity.idempotency_responses'::pg_catalog.regclass::oid,
        'identity.broker_echo_replays'::pg_catalog.regclass::oid,
        'identity.broker_echo_replay_policy'::pg_catalog.regclass::oid
    ]
    LOOP
        EXECUTE pg_catalog.format(
            'REVOKE ALL ON TABLE %s FROM PUBLIC',
            object_oid::pg_catalog.regclass
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
             WHERE relation.oid = object_oid
               AND role.oid <> relation.relowner
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL ON TABLE %s FROM %I',
                object_oid::pg_catalog.regclass,
                unexpected_grantee
            );
        END LOOP;
    END LOOP;

    FOREACH object_oid IN ARRAY ARRAY[
        'identity.guard_broker_echo_replay_mutation()'::pg_catalog.regprocedure::oid,
        'identity.guard_broker_echo_replay_insert()'::pg_catalog.regprocedure::oid,
        'identity.guard_broker_echo_replay_truncate()'::pg_catalog.regprocedure::oid,
        'identity.guard_broker_echo_replay_policy()'::pg_catalog.regprocedure::oid,
        'identity.valid_broker_echo_response(bytea,integer,jsonb,bytea,timestamptz,timestamptz)'::pg_catalog.regprocedure::oid,
        'identity.claim_broker_echo_response(text,bytea,bytea,integer,jsonb,bytea)'::pg_catalog.regprocedure::oid,
        'identity.purge_expired_broker_echo_replays(integer)'::pg_catalog.regprocedure::oid,
        'identity.broker_echo_replay_coverage()'::pg_catalog.regprocedure::oid,
        'identity.claim_broker_echo(text,text,bytea,text,timestamptz)'::pg_catalog.regprocedure::oid
    ]
    LOOP
        EXECUTE pg_catalog.format(
            'REVOKE ALL ON FUNCTION %s FROM PUBLIC',
            object_oid::pg_catalog.regprocedure
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
             WHERE procedure.oid = object_oid
               AND role.oid <> procedure.proowner
        LOOP
            EXECUTE pg_catalog.format(
                'REVOKE ALL ON FUNCTION %s FROM %I',
                object_oid::pg_catalog.regprocedure,
                unexpected_grantee
            );
        END LOOP;
    END LOOP;
END;
$$;

GRANT EXECUTE ON FUNCTION identity.claim_broker_echo_response(
    text,
    bytea,
    bytea,
    integer,
    jsonb,
    bytea
) TO platformgo_api;
GRANT EXECUTE ON FUNCTION identity.purge_expired_broker_echo_replays(integer)
TO platformgo_api;
GRANT EXECUTE ON FUNCTION identity.broker_echo_replay_coverage()
TO platformgo_api;
