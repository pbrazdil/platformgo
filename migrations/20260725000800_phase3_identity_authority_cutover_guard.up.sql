-- Phase 3 forward-only cutover guard for identity rows created by an earlier
-- unreleased candidate. Migration 006 used caller-controlled created_at
-- values and therefore could not prove that such rows followed the tenant
-- authority contract.
--
-- Lock/rewrite: bounded reads only; no table rewrite or data mutation.
-- Transaction: applied atomically by the migrator with the standard bounded
-- lock timeout. The migration journal entry becomes the immutable authority
-- cutover marker for all later identity writes.
-- Compatibility: clean Phase 2 databases contain no identity rows and pass.
-- Any database used by an earlier Phase 3 candidate must be backed up and
-- reset or explicitly classified through a later owner-approved migration.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM identity.users)
        OR EXISTS (SELECT 1 FROM identity.user_accounts)
        OR EXISTS (SELECT 1 FROM identity.sessions)
        OR EXISTS (SELECT 1 FROM identity.idempotency_responses)
        OR EXISTS (SELECT 1 FROM identity.account_profiles)
    THEN
        RAISE EXCEPTION
            'ambiguous pre-cutover Phase 3 identity data exists; backup and owner-directed classification or reset is required'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
