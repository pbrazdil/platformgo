-- Phase 3 broker-echo exact-response and bounded-retention cutover.
--
-- This is a no-overlap API cutover. Withdraw broker traffic, stop and drain
-- every old API process and identity transaction, and take a restore-verified
-- backup before applying it. The SHARE lock fences the legacy claim function
-- while live rows are validated and copied. Start only a new binary that has
-- verified this exact schema tip after the transaction commits.
--
-- No existing table is rewritten or mutated. The locked legacy scan is bounded
-- to 1,000 live broker-echo rows. A canonical legacy response occupies exactly
-- 46 bytes when rendered from jsonb, so 46,000 bytes is both the accepted-data
-- maximum and a corruption guard. The complete legacy relation, including
-- indexes and TOAST, must also remain at or below 64 MiB so the otherwise
-- indexless authoritative scan has a hard physical-work ceiling. Operators
-- must record all three preflight values below the bounds before cutover.
--
-- Definite errors roll the DDL, backfill, ACL changes, and migration journal
-- entry back atomically. An unknown commit outcome must be classified from the
-- exact journal checksum and catalog state before any binary starts. Before new
-- traffic, rollback means restoring the complete backup and old binary; after
-- new traffic, recovery is a reviewed forward fix.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

LOCK TABLE identity.idempotency_responses IN SHARE MODE;

DO $$
DECLARE
    live_rows bigint;
    live_json_bytes bigint;
    legacy_relation_bytes bigint;
BEGIN
    legacy_relation_bytes := pg_catalog.pg_total_relation_size(
        'identity.idempotency_responses'::pg_catalog.regclass
    );
    IF legacy_relation_bytes > 67108864 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'legacy replay relation exceeds the reviewed migration size bound';
    END IF;

    SELECT
        pg_catalog.count(*),
        COALESCE(
            pg_catalog.sum(
                pg_catalog.octet_length(legacy.response_body::text)
            ),
            0
        )
      INTO live_rows, live_json_bytes
      FROM identity.idempotency_responses AS legacy
     WHERE pg_catalog.left(
               legacy.scope,
               pg_catalog.char_length('broker-echo' || pg_catalog.chr(31))
           ) = 'broker-echo' || pg_catalog.chr(31)
       AND legacy.expires_at > pg_catalog.transaction_timestamp();

    IF live_rows > 1000 OR live_json_bytes > 46000 THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'live broker-echo replay backfill exceeds the reviewed migration bound';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM identity.idempotency_responses AS legacy
         WHERE pg_catalog.left(
                   legacy.scope,
                   pg_catalog.char_length(
                       'broker-echo' || pg_catalog.chr(31)
                   )
               ) = 'broker-echo' || pg_catalog.chr(31)
           AND legacy.expires_at > pg_catalog.transaction_timestamp()
           AND (
               pg_catalog.octet_length(legacy.scope) <=
                   pg_catalog.octet_length(
                       'broker-echo' || pg_catalog.chr(31) ||
                       'urn:xb:apikey:'
                   )
               OR pg_catalog.octet_length(legacy.scope) >
                   pg_catalog.octet_length(
                       'broker-echo' || pg_catalog.chr(31)
                   ) + 512
               OR pg_catalog.left(
                   legacy.scope,
                   pg_catalog.char_length(
                       'broker-echo' || pg_catalog.chr(31) ||
                       'urn:xb:apikey:'
                   )
               ) <>
                   'broker-echo' || pg_catalog.chr(31) ||
                   'urn:xb:apikey:'
               OR pg_catalog.strpos(
                   pg_catalog.substr(
                       legacy.scope,
                       pg_catalog.char_length(
                           'broker-echo' || pg_catalog.chr(31)
                       ) + 1
                   ),
                   pg_catalog.chr(31)
               ) > 0
               OR legacy.response_status <> 200
               OR pg_catalog.octet_length(legacy.request_hash) <> 32
               OR pg_catalog.jsonb_typeof(legacy.response_body) <> 'object'
               OR pg_catalog.jsonb_typeof(legacy.response_body -> 'id') <>
                   'string'
               OR legacy.response_body <>
                   pg_catalog.jsonb_build_object(
                       'id',
                       legacy.response_body -> 'id'
                   )
               OR legacy.response_body ->> 'id' !~
                   '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
               OR NOT pg_catalog.isfinite(legacy.created_at)
               OR NOT pg_catalog.isfinite(legacy.expires_at)
               OR legacy.expires_at <= legacy.created_at
           )
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'live legacy broker-echo replay cannot be reconstructed exactly';
    END IF;
END;
$$;

CREATE TABLE identity.broker_echo_replays (
    scope text NOT NULL CHECK (
        pg_catalog.octet_length(scope) >
            pg_catalog.octet_length(
                'broker-echo' || pg_catalog.chr(31) || 'urn:xb:apikey:'
            )
        AND pg_catalog.octet_length(scope) <=
            pg_catalog.octet_length(
                'broker-echo' || pg_catalog.chr(31)
            ) + 512
        AND pg_catalog.left(
            scope,
            pg_catalog.char_length(
                'broker-echo' || pg_catalog.chr(31) || 'urn:xb:apikey:'
            )
        ) =
            'broker-echo' || pg_catalog.chr(31) || 'urn:xb:apikey:'
        AND pg_catalog.strpos(
            pg_catalog.substr(
                scope,
                pg_catalog.char_length(
                    'broker-echo' || pg_catalog.chr(31)
                ) + 1
            ),
            pg_catalog.chr(31)
        ) = 0
    ),
    idempotency_key_hash bytea NOT NULL CHECK (
        pg_catalog.octet_length(idempotency_key_hash) = 32
    ),
    request_hash bytea NOT NULL CHECK (
        pg_catalog.octet_length(request_hash) = 32
    ),
    response_status integer NOT NULL CHECK (response_status = 200),
    response_headers jsonb NOT NULL CHECK (
        pg_catalog.jsonb_typeof(response_headers) = 'object'
        AND response_headers -> 'Content-Type' IS NOT NULL
        AND response_headers -> 'Content-Type' =
            '["application/json"]'::pg_catalog.jsonb
        AND pg_catalog.octet_length(response_headers::text) <= 8192
    ),
    response_body bytea NOT NULL CHECK (
        pg_catalog.octet_length(response_body) BETWEEN 1 AND 1048576
    ),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (scope, idempotency_key_hash),
    CHECK (
        pg_catalog.isfinite(created_at)
        AND pg_catalog.isfinite(expires_at)
        AND expires_at > created_at
    )
);

CREATE INDEX broker_echo_replays_expiry_idx
ON identity.broker_echo_replays (
    expires_at,
    scope,
    idempotency_key_hash
);

INSERT INTO identity.broker_echo_replays (
    scope,
    idempotency_key_hash,
    request_hash,
    response_status,
    response_headers,
    response_body,
    created_at,
    expires_at
)
SELECT
    legacy.scope,
    pg_catalog.sha256(
        pg_catalog.convert_to(legacy.idempotency_key, 'UTF8')
    ),
    legacy.request_hash,
    legacy.response_status,
    '{"Content-Type":["application/json"]}'::pg_catalog.jsonb,
    pg_catalog.convert_to(
        '{"id":"' || (legacy.response_body ->> 'id') || E'"}\n',
        'UTF8'
    ),
    legacy.created_at,
    legacy.expires_at
  FROM identity.idempotency_responses AS legacy
 WHERE pg_catalog.left(
           legacy.scope,
           pg_catalog.char_length('broker-echo' || pg_catalog.chr(31))
       ) = 'broker-echo' || pg_catalog.chr(31)
   AND legacy.expires_at > pg_catalog.transaction_timestamp();

CREATE FUNCTION identity.guard_broker_echo_replay_mutation()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
        OR OLD.expires_at > pg_catalog.statement_timestamp()
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'broker echo replay responses are immutable before expiry';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER broker_echo_replays_guard_mutation
BEFORE UPDATE OR DELETE ON identity.broker_echo_replays
FOR EACH ROW
EXECUTE FUNCTION identity.guard_broker_echo_replay_mutation();

CREATE FUNCTION identity.purge_expired_broker_echo_replays(
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
BEGIN
    IF requested_limit IS NULL
        OR requested_limit < 1
        OR requested_limit > 1000
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'broker echo purge limit must be between 1 and 1000';
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

CREATE FUNCTION identity.claim_broker_echo_response(
    requested_principal text,
    requested_idempotency_hash bytea,
    requested_hash bytea,
    requested_status integer,
    requested_headers jsonb,
    requested_body bytea
)
RETURNS TABLE (
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
    requested_json jsonb;
    requested_id text;
    inserted boolean;
    stored_hash bytea;
    stored_status integer;
    stored_headers jsonb;
    stored_body bytea;
    stored_created_at timestamptz;
    stored_expires_at timestamptz;
    stored_json jsonb;
    stored_id text;
    deleted_rows bigint;
BEGIN
    IF requested_principal IS NULL
        OR requested_idempotency_hash IS NULL
        OR requested_hash IS NULL
        OR requested_status IS NULL
        OR requested_headers IS NULL
        OR requested_body IS NULL
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
        OR requested_status <> 200
        OR pg_catalog.jsonb_typeof(requested_headers) <> 'object'
        OR requested_headers -> 'Content-Type' IS NULL
        OR requested_headers -> 'Content-Type' <>
            '["application/json"]'::pg_catalog.jsonb
        OR pg_catalog.octet_length(requested_headers::text) > 8192
        OR pg_catalog.octet_length(requested_body) NOT BETWEEN 1 AND 1048576
        OR EXISTS (
            SELECT 1
             FROM pg_catalog.jsonb_each(requested_headers)
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
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid broker echo claim';
    END IF;

    BEGIN
        requested_json :=
            pg_catalog.convert_from(requested_body, 'UTF8')::pg_catalog.jsonb;
    EXCEPTION
        WHEN OTHERS THEN
            RAISE EXCEPTION USING
                ERRCODE = '22023',
                MESSAGE = 'invalid broker echo response body';
    END;

    requested_id := requested_json ->> 'id';
    IF pg_catalog.jsonb_typeof(requested_json) <> 'object'
        OR requested_json -> 'id' IS NULL
        OR pg_catalog.jsonb_typeof(requested_json -> 'id') <> 'string'
        OR requested_id !~
            '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
        OR pg_catalog.right(
            pg_catalog.convert_from(requested_body, 'UTF8'),
            1
        ) <> pg_catalog.chr(10)
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '22023',
            MESSAGE = 'invalid broker echo response body';
    END IF;

    requested_scope :=
        'broker-echo' || pg_catalog.chr(31) || requested_principal;

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

    BEGIN
        stored_json :=
            pg_catalog.convert_from(stored_body, 'UTF8')::pg_catalog.jsonb;
    EXCEPTION
        WHEN OTHERS THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'stored broker echo response is invalid';
    END;
    stored_id := stored_json ->> 'id';

    IF stored_hash IS NULL
        OR pg_catalog.octet_length(stored_hash) <> 32
        OR stored_status <> 200
        OR pg_catalog.jsonb_typeof(stored_headers) <> 'object'
        OR stored_headers -> 'Content-Type' IS NULL
        OR stored_headers -> 'Content-Type' <>
            '["application/json"]'::pg_catalog.jsonb
        OR pg_catalog.octet_length(stored_headers::text) > 8192
        OR pg_catalog.octet_length(stored_body) NOT BETWEEN 1 AND 1048576
        OR EXISTS (
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
        OR pg_catalog.jsonb_typeof(stored_json) <> 'object'
        OR stored_json -> 'id' IS NULL
        OR pg_catalog.jsonb_typeof(stored_json -> 'id') <> 'string'
        OR stored_id !~
            '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
        OR pg_catalog.right(
            pg_catalog.convert_from(stored_body, 'UTF8'),
            1
        ) <> pg_catalog.chr(10)
        OR NOT pg_catalog.isfinite(stored_created_at)
        OR NOT pg_catalog.isfinite(stored_expires_at)
        OR stored_expires_at <= stored_created_at
    THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'stored broker echo response is invalid';
    END IF;

    IF NOT inserted AND stored_expires_at <= authority_time THEN
        DELETE FROM identity.broker_echo_replays AS replay
         WHERE replay.scope = requested_scope
           AND replay.idempotency_key_hash = requested_idempotency_hash;
        GET DIAGNOSTICS deleted_rows = ROW_COUNT;
        IF deleted_rows <> 1 THEN
            RAISE EXCEPTION USING
                ERRCODE = '55000',
                MESSAGE = 'expired broker echo replay replacement lost its lock';
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

        stored_hash := requested_hash;
        stored_status := requested_status;
        stored_headers := requested_headers;
        stored_body := requested_body;
    ELSIF stored_hash <> requested_hash THEN
        RAISE EXCEPTION USING
            ERRCODE = '23505',
            MESSAGE = 'broker echo idempotency conflict';
    END IF;

    RETURN QUERY
    SELECT stored_status, stored_headers, stored_body;
END;
$$;

DO $$
DECLARE
    object_oid oid;
    unexpected_grantee name;
BEGIN
    FOREACH object_oid IN ARRAY ARRAY[
        'identity.idempotency_responses'::pg_catalog.regclass::oid,
        'identity.broker_echo_replays'::pg_catalog.regclass::oid
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
        'identity.purge_expired_broker_echo_replays(integer)'::pg_catalog.regprocedure::oid,
        'identity.claim_broker_echo_response(text,bytea,bytea,integer,jsonb,bytea)'::pg_catalog.regprocedure::oid,
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

GRANT EXECUTE ON FUNCTION
    identity.purge_expired_broker_echo_replays(integer)
TO platformgo_api;

GRANT EXECUTE ON FUNCTION identity.claim_broker_echo_response(
    text,
    bytea,
    bytea,
    integer,
    jsonb,
    bytea
) TO platformgo_api;
