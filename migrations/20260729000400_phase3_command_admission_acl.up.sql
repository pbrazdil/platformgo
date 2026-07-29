-- Repair command-admission authority inherited from hostile migrator-owner
-- defaults. This catalog-only cutover changes no rows or relation storage.
-- The migrator supplies a shorter five-second lock_timeout; any timeout rolls
-- back the ACL, triggers, and migration journal atomically.
SET LOCAL statement_timeout = '10s';

-- Fence the engine before admission. Engine shutdown drains the admission gate
-- before releasing shard ownership, so this order cannot deadlock with drain.
DO $$
DECLARE
    configured_shard integer;
BEGIN
    SELECT shard_id::integer
      INTO configured_shard
      FROM engine.deployment_shard
     WHERE singleton;
    IF configured_shard IS NOT NULL THEN
        PERFORM pg_advisory_xact_lock(1346850639, configured_shard);
    END IF;
END;
$$;

-- New API admission takes this key in shared mode before touching command
-- authority. The exclusive migration fence prevents an admission from crossing
-- the ACL cutover.
SELECT pg_advisory_xact_lock(1346847044, 0);

-- SHARE waits for transactions that already used a privilege being revoked and
-- prevents another writer from committing under the pre-cutover ACL. Keep the
-- order aligned with engine completion and command-admission ownership.
LOCK TABLE trading.commands IN SHARE MODE;
LOCK TABLE trading.idempotency_records IN SHARE MODE;
LOCK TABLE trading.command_replay_responses IN SHARE MODE;

CREATE TRIGGER commands_reject_truncate
BEFORE TRUNCATE ON trading.commands
FOR EACH STATEMENT EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER idempotency_records_reject_truncate
BEFORE TRUNCATE ON trading.idempotency_records
FOR EACH STATEMENT EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER command_replay_responses_reject_truncate
BEFORE TRUNCATE ON trading.command_replay_responses
FOR EACH STATEMENT EXECUTE FUNCTION engine.reject_immutable_change();

DO $$
DECLARE
    relation_oid pg_catalog.oid;
    unexpected_grantee pg_catalog.name;
    column_name pg_catalog.name;
BEGIN
    FOR relation_oid IN
        SELECT target_oid
          FROM unnest(ARRAY[
              'trading.commands'::pg_catalog.regclass::pg_catalog.oid,
              'trading.idempotency_records'::pg_catalog.regclass::pg_catalog.oid,
              'trading.command_replay_responses'::pg_catalog.regclass::pg_catalog.oid
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

GRANT SELECT, INSERT ON TABLE
    trading.commands,
    trading.idempotency_records,
    trading.command_replay_responses
TO platformgo_api;

GRANT SELECT ON TABLE
    trading.commands,
    trading.idempotency_records,
    trading.command_replay_responses
TO platformgo_engine;

GRANT UPDATE (
    status,
    result,
    completed_at
) ON TABLE trading.commands TO platformgo_engine;

GRANT UPDATE (
    state,
    response_status,
    response_headers,
    response_body
) ON TABLE trading.idempotency_records TO platformgo_engine;

GRANT SELECT (
    command_id,
    account_id,
    account_sequence,
    command_type,
    schema_version,
    canonical_payload,
    logical_time
) ON TABLE trading.commands TO platformgo_outbox;
