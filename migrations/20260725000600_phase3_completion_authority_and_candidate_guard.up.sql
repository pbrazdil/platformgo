-- Phase 3 forward hardening for completion authority and unreleased-candidate
-- data. This migration is intentionally additive to already-applied 004/005.
-- Lock/rewrite: privilege metadata only plus bounded reads of identity tables.
-- Transaction: the migrator applies this file atomically with lock_timeout.

DO $$
DECLARE
    authority_applied_at timestamptz;
BEGIN
    SELECT applied_at
      INTO authority_applied_at
      FROM engine.schema_migrations
     WHERE filename =
        '20260725000400_phase3_authority_and_replay_hardening.up.sql';

    IF authority_applied_at IS NULL THEN
        RAISE EXCEPTION
            'phase 3 authority migration history is missing'
            USING ERRCODE = '55000';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM identity.users
         WHERE created_at < authority_applied_at
    ) OR EXISTS (
        SELECT 1
          FROM identity.user_accounts
         WHERE created_at < authority_applied_at
    ) OR EXISTS (
        SELECT 1
          FROM identity.idempotency_responses
         WHERE created_at < authority_applied_at
    ) THEN
        RAISE EXCEPTION
            'ambiguous identity data predates tenant authority migration; backup and owner-directed classification or reset is required'
            USING ERRCODE = '55000';
    END IF;
END;
$$;

REVOKE UPDATE (
    state,
    response_status,
    response_headers,
    response_body
) ON trading.idempotency_records
FROM platformgo_api;
