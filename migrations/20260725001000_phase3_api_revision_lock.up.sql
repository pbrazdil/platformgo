-- Phase 3 least-privilege economic revision lock.
--
-- Lock/rewrite: function and privilege metadata only.
-- Transaction: the row-share locks acquired by the function remain held by
-- the caller transaction through command admission.
-- Security: the API can lock and read only the singleton runtime revision and
-- one parameterized instrument revision; it receives no UPDATE privilege.

CREATE FUNCTION engine.lock_command_economic_revisions(
    requested_instrument_id text
)
RETURNS TABLE (
    configuration_version bigint,
    instrument_version bigint
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT
        configuration.version,
        instrument.revision
      FROM engine.runtime_configuration AS configuration
      JOIN trading.instruments AS instrument
        ON instrument.instrument_id = requested_instrument_id
     WHERE configuration.singleton
       FOR SHARE OF configuration, instrument;
$$;

REVOKE ALL ON FUNCTION engine.lock_command_economic_revisions(text)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION engine.lock_command_economic_revisions(text)
TO platformgo_api;
