-- Phase 3 forward correction for engine-owned broker account provisioning.
-- Lock/rewrite: schema privilege metadata only; no table lock or rewrite.
-- Transaction: applied atomically by the migrator with lock_timeout.
-- Compatibility: additive privilege required by Phase 3 engine binaries;
-- Phase 2 binaries do not access the identity schema from the engine role.
-- Failure/retry: a failed GRANT rolls back with the migration transaction and
-- is safe to retry under the migrator's global advisory lock.

GRANT USAGE ON SCHEMA identity TO platformgo_engine;
