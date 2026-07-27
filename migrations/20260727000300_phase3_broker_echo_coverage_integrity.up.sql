-- Phase 3 broker-echo replay coverage integrity authority.
--
-- This is a forward-only correction to the aggregate coverage introduced by
-- the preceding capacity migration. Every runtime remains stopped while it is
-- applied. Existing rows retain the prior application-clock expiry contract;
-- later claims use PostgreSQL statement time and exact 24-hour retention. It
-- rejects authority that cannot provide exact replay or whose remaining
-- lifetime exceeds the bounded-capacity retry contract, then exposes current
-- contract and remaining-lifetime violations to startup and readiness checks.
-- The discriminator, true default, and insert fence commit together, so every
-- committed 00300 tip rejects new rows that claim the legacy exemption.
-- The initial SHARE lock closes writers before validation. The ALTER TABLE
-- subcommands then require bounded ACCESS EXCLUSIVE, and constraint validation
-- requires SHARE UPDATE EXCLUSIVE. The constant boolean default avoids a heap
-- rewrite; the relation is already capped at 1,000 rows and the transaction
-- rolls back completely on any five-second lock or fifteen-second statement
-- timeout.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

LOCK TABLE identity.broker_echo_replays IN SHARE MODE;

ALTER TABLE identity.broker_echo_replays
    ADD COLUMN postgres_time_authority boolean NOT NULL DEFAULT false;

ALTER TABLE identity.broker_echo_replays
    ALTER COLUMN postgres_time_authority SET DEFAULT true;

CREATE FUNCTION identity.guard_broker_echo_replay_insert()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF NEW.postgres_time_authority IS DISTINCT FROM true THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'broker echo legacy time authority is migration-only';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER broker_echo_replays_require_postgres_time_authority
BEFORE INSERT ON identity.broker_echo_replays
FOR EACH ROW
EXECUTE FUNCTION identity.guard_broker_echo_replay_insert();

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM identity.broker_echo_replays AS replay
         WHERE NOT identity.valid_broker_echo_response(
               replay.request_hash,
               replay.response_status,
               replay.response_headers,
               replay.response_body,
               replay.created_at,
               replay.expires_at
           )
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'broker echo replay coverage contains an invalid response';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM identity.broker_echo_replays AS replay
         WHERE replay.expires_at >
               pg_catalog.statement_timestamp() + interval '24 hours'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'broker echo replay coverage exceeds the maximum remaining lifetime';
    END IF;
END;
$$;

ALTER TABLE identity.broker_echo_replays
    ADD CONSTRAINT broker_echo_replays_have_valid_exact_response
    CHECK (
        identity.valid_broker_echo_response(
            request_hash,
            response_status,
            response_headers,
            response_body,
            created_at,
            expires_at
        )
        AND (
            NOT postgres_time_authority
            OR expires_at = created_at + interval '24 hours'
        )
    ) NOT VALID;

ALTER TABLE identity.broker_echo_replays
    VALIDATE CONSTRAINT broker_echo_replays_have_valid_exact_response;

DROP FUNCTION identity.broker_echo_replay_coverage();

CREATE FUNCTION identity.broker_echo_replay_coverage()
RETURNS TABLE (
    max_total_rows integer,
    max_rows_per_principal integer,
    purge_batch_size integer,
    max_batches_per_cycle integer,
    cleanup_interval_seconds integer,
    cleanup_cycle_timeout_seconds integer,
    expired_readiness_slo_seconds integer,
    max_retry_after_seconds integer,
    total_rows bigint,
    live_rows bigint,
    invalid_live_rows bigint,
    overlong_live_rows bigint,
    expired_rows bigint,
    maximum_principal_rows bigint,
    oldest_live_expires_at text,
    oldest_expired_at text,
    oldest_expired_age_seconds bigint
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT
        policy.max_total_rows,
        policy.max_rows_per_principal,
        policy.purge_batch_size,
        policy.max_batches_per_cycle,
        policy.cleanup_interval_seconds,
        policy.cleanup_cycle_timeout_seconds,
        policy.expired_readiness_slo_seconds,
        policy.max_retry_after_seconds,
        pg_catalog.count(replay.*)::bigint,
        pg_catalog.count(replay.*) FILTER (
            WHERE replay.expires_at > pg_catalog.statement_timestamp()
        )::bigint,
        pg_catalog.count(replay.*) FILTER (
            WHERE replay.expires_at > pg_catalog.statement_timestamp()
              AND (
                  NOT identity.valid_broker_echo_response(
                      replay.request_hash,
                      replay.response_status,
                      replay.response_headers,
                      replay.response_body,
                      replay.created_at,
                      replay.expires_at
                  )
                  OR (
                      replay.postgres_time_authority
                      AND replay.expires_at <>
                          replay.created_at + interval '24 hours'
                  )
              )
        )::bigint,
        pg_catalog.count(replay.*) FILTER (
            WHERE replay.expires_at >
                  pg_catalog.statement_timestamp() + interval '24 hours'
        )::bigint,
        pg_catalog.count(replay.*) FILTER (
            WHERE replay.expires_at <= pg_catalog.statement_timestamp()
        )::bigint,
        COALESCE((
            SELECT pg_catalog.max(per_scope.row_count)
              FROM (
                    SELECT pg_catalog.count(*)::bigint AS row_count
                      FROM identity.broker_echo_replays AS grouped
                     GROUP BY grouped.scope
              ) AS per_scope
        ), 0::bigint),
        COALESCE(
            pg_catalog.min(replay.expires_at) FILTER (
                WHERE replay.expires_at > pg_catalog.statement_timestamp()
            )::text,
            ''
        ),
        COALESCE(
            pg_catalog.min(replay.expires_at) FILTER (
                WHERE replay.expires_at <= pg_catalog.statement_timestamp()
            )::text,
            ''
        ),
        COALESCE(
            pg_catalog.ceil(
                extract(
                    epoch FROM
                        pg_catalog.statement_timestamp() -
                        pg_catalog.min(replay.expires_at) FILTER (
                            WHERE replay.expires_at <=
                                  pg_catalog.statement_timestamp()
                        )
                )
            )::bigint,
            0::bigint
        )
      FROM identity.broker_echo_replay_policy AS policy
      LEFT JOIN identity.broker_echo_replays AS replay ON true
     WHERE policy.singleton
     GROUP BY
        policy.max_total_rows,
        policy.max_rows_per_principal,
        policy.purge_batch_size,
        policy.max_batches_per_cycle,
        policy.cleanup_interval_seconds,
        policy.cleanup_cycle_timeout_seconds,
        policy.expired_readiness_slo_seconds,
        policy.max_retry_after_seconds;
$$;

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
