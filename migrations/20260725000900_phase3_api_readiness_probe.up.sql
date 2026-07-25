-- Phase 3 least-privilege API readiness probe.
--
-- Lock/rewrite: function and privilege metadata only.
-- Transaction: applied atomically by the migrator.
-- Security: the API receives one bounded read-only probe instead of SELECT on
-- engine-owned checkpoints, faults, or PostgreSQL lock catalogs.

CREATE FUNCTION engine.runtime_command_ready_probe(requested_shard_id bigint)
RETURNS TABLE (
    engine_active boolean,
    engine_ready boolean,
    outbox_active boolean,
    outbox_ready boolean,
    checkpoint_ready boolean,
    stale_command_outbox boolean
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT
        EXISTS (
            SELECT 1
              FROM pg_catalog.pg_locks
             WHERE locktype = 'advisory'
               AND classid = 1346850639::oid
               AND objid = requested_shard_id::oid
               AND granted
        ),
        EXISTS (
            SELECT 1
              FROM pg_catalog.pg_locks
             WHERE locktype = 'advisory'
               AND classid = 1346851397::oid
               AND objid = requested_shard_id::oid
               AND granted
        ),
        EXISTS (
            SELECT 1
              FROM pg_catalog.pg_locks
             WHERE locktype = 'advisory'
               AND classid = 1346850626::oid
               AND objid = 0::oid
               AND granted
        ),
        EXISTS (
            SELECT 1
              FROM pg_catalog.pg_locks
             WHERE locktype = 'advisory'
               AND classid = 1346851394::oid
               AND objid = 0::oid
               AND granted
        ),
        COALESCE((
            SELECT ready
              FROM engine.shard_checkpoints
             WHERE shard_id = requested_shard_id
        ), NOT EXISTS (
            SELECT 1
              FROM engine.shard_faults
             WHERE shard_id = requested_shard_id
        )),
        EXISTS (
            SELECT 1
              FROM messaging.outbox
             WHERE producer_class = 'api'
               AND published_at IS NULL
               AND created_at < clock_timestamp() - interval '5 seconds'
        );
$$;

REVOKE ALL ON FUNCTION engine.runtime_command_ready_probe(bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION engine.runtime_command_ready_probe(bigint)
TO platformgo_api;
