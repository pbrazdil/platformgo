-- Phase 3 broker-echo bounded-capacity and cleanup-ownership authority.
--
-- This is the forward companion to the exact-response cutover. Every runtime
-- remains stopped while it is applied. The previous migration can be a
-- classifiable intermediate tip: this migration removes only rows already
-- expired by PostgreSQL time, rejects live data above either capacity bound,
-- and never shortens or deletes a live exact response.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

LOCK TABLE identity.broker_echo_replays IN SHARE MODE;

DO $$
BEGIN
    IF pg_catalog.pg_total_relation_size(
        'identity.broker_echo_replays'::pg_catalog.regclass
    ) > 67108864 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'broker echo replay authority exceeds the reviewed physical bound';
    END IF;
END;
$$;

CREATE FUNCTION identity.valid_broker_echo_response(
    stored_request_hash bytea,
    stored_status integer,
    stored_headers jsonb,
    stored_body bytea,
    stored_created_at timestamptz,
    stored_expires_at timestamptz
)
RETURNS boolean
LANGUAGE plpgsql
IMMUTABLE
SET search_path = pg_catalog
AS $$
DECLARE
    stored_json jsonb;
    stored_id text;
BEGIN
    BEGIN
        stored_json :=
            pg_catalog.convert_from(stored_body, 'UTF8')::pg_catalog.jsonb;
    EXCEPTION
        WHEN OTHERS THEN
            RETURN false;
    END;
    stored_id := stored_json ->> 'id';

    RETURN COALESCE((
        pg_catalog.octet_length(stored_request_hash) = 32
        AND stored_status = 200
        AND pg_catalog.jsonb_typeof(stored_headers) = 'object'
        AND stored_headers -> 'Content-Type' IS NOT NULL
        AND stored_headers -> 'Content-Type' =
            '["application/json"]'::pg_catalog.jsonb
        AND pg_catalog.octet_length(stored_headers::text) <= 8192
        AND pg_catalog.octet_length(stored_body) BETWEEN 1 AND 1048576
        AND NOT EXISTS (
            SELECT 1
              FROM pg_catalog.jsonb_each(stored_headers)
                   AS header(name, values)
             WHERE pg_catalog.octet_length(header.name) NOT BETWEEN 1 AND 128
                OR header.name ~ E'[\r\n]'
                OR CASE
                    WHEN pg_catalog.jsonb_typeof(header.values) <> 'array'
                        THEN true
                    ELSE
                        pg_catalog.jsonb_array_length(header.values) = 0
                        OR EXISTS (
                            SELECT 1
                              FROM pg_catalog.jsonb_array_elements(
                                       header.values
                                   ) AS element(value)
                             WHERE pg_catalog.jsonb_typeof(element.value) <>
                                       'string'
                                OR pg_catalog.octet_length(
                                    element.value #>> '{}'
                                ) > 1024
                                OR (element.value #>> '{}') ~ E'[\r\n]'
                        )
                END
        )
        AND pg_catalog.jsonb_typeof(stored_json) = 'object'
        AND stored_json -> 'id' IS NOT NULL
        AND pg_catalog.jsonb_typeof(stored_json -> 'id') = 'string'
        AND stored_id ~
            '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
        AND pg_catalog.right(
            pg_catalog.convert_from(stored_body, 'UTF8'),
            1
        ) = pg_catalog.chr(10)
        AND pg_catalog.isfinite(stored_created_at)
        AND pg_catalog.isfinite(stored_expires_at)
        AND stored_expires_at > stored_created_at
    ), false);
END;
$$;

SELECT identity.purge_expired_broker_echo_replays(1000);

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
            MESSAGE = 'broker echo replay authority contains an invalid response';
    END IF;

    IF (
        SELECT pg_catalog.count(*)
          FROM identity.broker_echo_replays
    ) > 1000 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'broker echo replay total exceeds capacity';
    END IF;

    IF EXISTS (
        SELECT replay.scope
          FROM identity.broker_echo_replays AS replay
         GROUP BY replay.scope
        HAVING pg_catalog.count(*) > 100
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'broker echo replay principal exceeds capacity';
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
                'broker echo replay exceeds the maximum supported remaining lifetime';
    END IF;
END;
$$;

CREATE TABLE identity.broker_echo_replay_policy (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    max_total_rows integer NOT NULL CHECK (max_total_rows = 1000),
    max_rows_per_principal integer NOT NULL CHECK (
        max_rows_per_principal = 100
    ),
    purge_batch_size integer NOT NULL CHECK (purge_batch_size = 100),
    max_batches_per_cycle integer NOT NULL CHECK (
        max_batches_per_cycle = 10
    ),
    cleanup_interval_seconds integer NOT NULL CHECK (
        cleanup_interval_seconds = 60
    ),
    cleanup_cycle_timeout_seconds integer NOT NULL CHECK (
        cleanup_cycle_timeout_seconds = 10
    ),
    expired_readiness_slo_seconds integer NOT NULL CHECK (
        expired_readiness_slo_seconds = 120
    ),
    max_retry_after_seconds integer NOT NULL CHECK (
        max_retry_after_seconds = 86460
    ),
    CHECK (
        purge_batch_size::bigint * max_batches_per_cycle::bigint >=
            max_total_rows::bigint
    ),
    CHECK (
        max_rows_per_principal <= max_total_rows
    ),
    CHECK (
        max_retry_after_seconds >=
            86400 + cleanup_interval_seconds
    )
);

INSERT INTO identity.broker_echo_replay_policy (
    singleton,
    max_total_rows,
    max_rows_per_principal,
    purge_batch_size,
    max_batches_per_cycle,
    cleanup_interval_seconds,
    cleanup_cycle_timeout_seconds,
    expired_readiness_slo_seconds,
    max_retry_after_seconds
) VALUES (
    true,
    1000,
    100,
    100,
    10,
    60,
    10,
    120,
    86460
);

CREATE FUNCTION identity.guard_broker_echo_replay_policy()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = '55000',
        MESSAGE = 'broker echo replay policy is immutable';
END;
$$;

CREATE TRIGGER broker_echo_replay_policy_is_immutable
BEFORE UPDATE OR DELETE ON identity.broker_echo_replay_policy
FOR EACH ROW
EXECUTE FUNCTION identity.guard_broker_echo_replay_policy();

CREATE TRIGGER broker_echo_replay_policy_rejects_truncate
BEFORE TRUNCATE ON identity.broker_echo_replay_policy
FOR EACH STATEMENT
EXECUTE FUNCTION identity.guard_broker_echo_replay_policy();

CREATE OR REPLACE FUNCTION identity.purge_expired_broker_echo_replays(
    requested_limit integer
)
RETURNS integer
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET lock_timeout = '5s'
AS $$
DECLARE
    deleted_rows integer;
    policy_batch_size integer;
BEGIN
    SELECT policy.purge_batch_size
      INTO STRICT policy_batch_size
      FROM identity.broker_echo_replay_policy AS policy
     WHERE policy.singleton
     FOR UPDATE;

    IF requested_limit IS NULL
        OR requested_limit < 1
        OR requested_limit > policy_batch_size
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE =
                'broker echo purge limit exceeds the immutable policy batch';
    END IF;

    WITH expired AS (
        SELECT replay.scope, replay.idempotency_key_hash
          FROM identity.broker_echo_replays AS replay
         WHERE replay.expires_at <= pg_catalog.statement_timestamp()
         ORDER BY
               replay.expires_at,
               replay.scope COLLATE pg_catalog."C",
               replay.idempotency_key_hash
         FOR UPDATE SKIP LOCKED
         LIMIT requested_limit
    ),
    deleted AS (
        DELETE FROM identity.broker_echo_replays AS replay
         USING expired
         WHERE replay.scope = expired.scope
           AND replay.idempotency_key_hash =
               expired.idempotency_key_hash
         RETURNING 1
    )
    SELECT pg_catalog.count(*)::integer
      INTO deleted_rows
      FROM deleted;

    RETURN deleted_rows;
END;
$$;

DROP FUNCTION identity.claim_broker_echo_response(
    text,
    bytea,
    bytea,
    integer,
    jsonb,
    bytea
);

CREATE FUNCTION identity.claim_broker_echo_response(
    requested_principal text,
    requested_idempotency_hash bytea,
    requested_hash bytea,
    requested_status integer,
    requested_headers jsonb,
    requested_body bytea
)
RETURNS TABLE (
    outcome text,
    retry_after_seconds bigint,
    capacity_scope text,
    response_status integer,
    response_headers jsonb,
    response_body bytea
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET lock_timeout = '5s'
AS $$
DECLARE
    authority_time timestamptz := pg_catalog.statement_timestamp();
    requested_scope text;
    inserted boolean;
    stored_hash bytea;
    stored_status integer;
    stored_headers jsonb;
    stored_body bytea;
    stored_created_at timestamptz;
    stored_expires_at timestamptz;
    deleted_rows bigint;
    policy_max_total integer;
    policy_max_principal integer;
    policy_cleanup_interval integer;
    policy_max_retry integer;
    total_rows bigint;
    principal_rows bigint;
    principal_limited boolean;
    global_limited boolean;
    principal_expiry timestamptz;
    global_expiry timestamptz;
    principal_retry bigint := 0;
    global_retry bigint := 0;
    retry_after bigint;
    limited_scope text;
BEGIN
    IF requested_principal IS NULL
        OR requested_idempotency_hash IS NULL
        OR requested_hash IS NULL
        OR pg_catalog.octet_length(requested_principal) <=
            pg_catalog.octet_length('urn:xb:apikey:')
        OR pg_catalog.octet_length(requested_principal) > 512
        OR pg_catalog.left(
            requested_principal,
            pg_catalog.char_length('urn:xb:apikey:')
        ) <> 'urn:xb:apikey:'
        OR pg_catalog.strpos(
            requested_principal,
            pg_catalog.chr(31)
        ) > 0
        OR pg_catalog.octet_length(requested_idempotency_hash) <> 32
        OR pg_catalog.octet_length(requested_hash) <> 32
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid broker echo claim identity';
    END IF;

    requested_scope :=
        'broker-echo' || pg_catalog.chr(31) || requested_principal;

    SELECT
        replay.request_hash,
        replay.response_status,
        replay.response_headers,
        replay.response_body,
        replay.created_at,
        replay.expires_at
      INTO
        stored_hash,
        stored_status,
        stored_headers,
        stored_body,
        stored_created_at,
        stored_expires_at
      FROM identity.broker_echo_replays AS replay
     WHERE replay.scope = requested_scope
       AND replay.idempotency_key_hash = requested_idempotency_hash;

    IF FOUND AND stored_expires_at > authority_time THEN
        IF NOT identity.valid_broker_echo_response(
            stored_hash,
            stored_status,
            stored_headers,
            stored_body,
            stored_created_at,
            stored_expires_at
        ) THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'stored broker echo response is invalid';
        END IF;
        IF stored_hash <> requested_hash THEN
            RAISE EXCEPTION USING
                ERRCODE = '23505',
                MESSAGE = 'broker echo idempotency conflict';
        END IF;
        RETURN QUERY SELECT
            'stored'::text,
            0::bigint,
            ''::text,
            stored_status,
            stored_headers,
            stored_body;
        RETURN;
    END IF;

    SELECT
        policy.max_total_rows,
        policy.max_rows_per_principal,
        policy.cleanup_interval_seconds,
        policy.max_retry_after_seconds
      INTO STRICT
        policy_max_total,
        policy_max_principal,
        policy_cleanup_interval,
        policy_max_retry
      FROM identity.broker_echo_replay_policy AS policy
     WHERE policy.singleton
     FOR UPDATE;

    stored_hash := NULL;
    stored_status := NULL;
    stored_headers := NULL;
    stored_body := NULL;
    stored_created_at := NULL;
    stored_expires_at := NULL;

    SELECT
        replay.request_hash,
        replay.response_status,
        replay.response_headers,
        replay.response_body,
        replay.created_at,
        replay.expires_at
      INTO
        stored_hash,
        stored_status,
        stored_headers,
        stored_body,
        stored_created_at,
        stored_expires_at
      FROM identity.broker_echo_replays AS replay
     WHERE replay.scope = requested_scope
       AND replay.idempotency_key_hash = requested_idempotency_hash
     FOR UPDATE;

    IF FOUND AND NOT identity.valid_broker_echo_response(
        stored_hash,
        stored_status,
        stored_headers,
        stored_body,
        stored_created_at,
        stored_expires_at
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'stored broker echo response is invalid';
    END IF;

    IF FOUND AND stored_expires_at > authority_time THEN
        IF stored_hash <> requested_hash THEN
            RAISE EXCEPTION USING
                ERRCODE = '23505',
                MESSAGE = 'broker echo idempotency conflict';
        END IF;
        RETURN QUERY SELECT
            'stored'::text,
            0::bigint,
            ''::text,
            stored_status,
            stored_headers,
            stored_body;
        RETURN;
    END IF;

    IF FOUND THEN
        IF NOT identity.valid_broker_echo_response(
            requested_hash,
            requested_status,
            requested_headers,
            requested_body,
            authority_time,
            authority_time + interval '24 hours'
        ) THEN
            RAISE EXCEPTION USING
                ERRCODE = '22023',
                MESSAGE = 'invalid broker echo response';
        END IF;

        DELETE FROM identity.broker_echo_replays AS replay
         WHERE replay.scope = requested_scope
           AND replay.idempotency_key_hash = requested_idempotency_hash;
        GET DIAGNOSTICS deleted_rows = ROW_COUNT;
        IF deleted_rows <> 1 THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE =
                    'expired broker echo replay replacement lost its lock';
        END IF;

        INSERT INTO identity.broker_echo_replays (
            scope,
            idempotency_key_hash,
            request_hash,
            response_status,
            response_headers,
            response_body,
            created_at,
            expires_at
        ) VALUES (
            requested_scope,
            requested_idempotency_hash,
            requested_hash,
            requested_status,
            requested_headers,
            requested_body,
            authority_time,
            authority_time + interval '24 hours'
        );

        RETURN QUERY SELECT
            'stored'::text,
            0::bigint,
            ''::text,
            requested_status,
            requested_headers,
            requested_body;
        RETURN;
    END IF;

    SELECT
        pg_catalog.count(*),
        pg_catalog.count(*) FILTER (
            WHERE replay.scope = requested_scope
        )
      INTO total_rows, principal_rows
      FROM identity.broker_echo_replays AS replay;

    principal_limited := principal_rows >= policy_max_principal;
    global_limited := total_rows >= policy_max_total;

    IF principal_limited OR global_limited THEN
        IF principal_limited THEN
            SELECT pg_catalog.min(replay.expires_at)
              INTO principal_expiry
              FROM identity.broker_echo_replays AS replay
             WHERE replay.scope = requested_scope;
            principal_retry :=
                CASE
                    WHEN principal_expiry <= authority_time
                        THEN policy_cleanup_interval
                    ELSE
                        pg_catalog.ceil(
                            extract(
                                epoch FROM principal_expiry - authority_time
                            )
                        )::bigint + policy_cleanup_interval
                END;
        END IF;
        IF global_limited THEN
            SELECT pg_catalog.min(replay.expires_at)
              INTO global_expiry
              FROM identity.broker_echo_replays AS replay;
            global_retry :=
                CASE
                    WHEN global_expiry <= authority_time
                        THEN policy_cleanup_interval
                    ELSE
                        pg_catalog.ceil(
                            extract(
                                epoch FROM global_expiry - authority_time
                            )
                        )::bigint + policy_cleanup_interval
                END;
        END IF;

        retry_after := least(
            policy_max_retry::bigint,
            greatest(
                1::bigint,
                principal_retry,
                global_retry
            )
        );
        limited_scope :=
            CASE
                WHEN principal_limited AND global_limited THEN 'both'
                WHEN principal_limited THEN 'principal'
                ELSE 'global'
            END;
        RETURN QUERY SELECT
            'capacity_limited'::text,
            retry_after,
            limited_scope,
            0,
            '{}'::pg_catalog.jsonb,
            ''::bytea;
        RETURN;
    END IF;

    IF NOT identity.valid_broker_echo_response(
        requested_hash,
        requested_status,
        requested_headers,
        requested_body,
        authority_time,
        authority_time + interval '24 hours'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid broker echo response';
    END IF;

    INSERT INTO identity.broker_echo_replays AS replay (
        scope,
        idempotency_key_hash,
        request_hash,
        response_status,
        response_headers,
        response_body,
        created_at,
        expires_at
    ) VALUES (
        requested_scope,
        requested_idempotency_hash,
        requested_hash,
        requested_status,
        requested_headers,
        requested_body,
        authority_time,
        authority_time + interval '24 hours'
    )
    ON CONFLICT (scope, idempotency_key_hash)
    DO SELECT FOR UPDATE
    RETURNING WITH (
        OLD AS old_replay,
        NEW AS returned_replay
    )
        old_replay.scope IS NULL,
        returned_replay.request_hash,
        returned_replay.response_status,
        returned_replay.response_headers,
        returned_replay.response_body,
        returned_replay.created_at,
        returned_replay.expires_at
    INTO
        inserted,
        stored_hash,
        stored_status,
        stored_headers,
        stored_body,
        stored_created_at,
        stored_expires_at;

    IF NOT identity.valid_broker_echo_response(
        stored_hash,
        stored_status,
        stored_headers,
        stored_body,
        stored_created_at,
        stored_expires_at
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'stored broker echo response is invalid';
    END IF;
    IF NOT inserted AND stored_hash <> requested_hash THEN
        RAISE EXCEPTION USING
            ERRCODE = '23505',
            MESSAGE = 'broker echo idempotency conflict';
    END IF;

    RETURN QUERY SELECT
        'stored'::text,
        0::bigint,
        ''::text,
        stored_status,
        stored_headers,
        stored_body;
END;
$$;

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
