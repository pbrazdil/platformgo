-- Phase 3 broker account-list tenant authority index.
-- Lock/rewrite: CREATE INDEX reads identity.user_accounts without rewriting
-- rows and takes SHARE, which permits reads but blocks account provisioning
-- writes. Operators drain account-provisioning writers before this upgrade.
-- Lock acquisition is bounded to five seconds and the complete build to
-- fifteen seconds so a busy or unexpectedly large relation fails closed.
-- Transaction: the migrator commits the index and checksum journal atomically.
-- Compatibility: older binaries ignore the additive index, but after commit
-- rollback uses a code-revert artifact that retains this migration.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

LOCK TABLE identity.user_accounts IN SHARE MODE;

CREATE INDEX user_accounts_broker_list_idx
ON identity.user_accounts (broker_subject, user_id, account_id)
WHERE broker_subject IS NOT NULL;
