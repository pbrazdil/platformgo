#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

: "${PLATFORMGO_TEST_POSTGRES_DSN:?canonical PostgreSQL test DSN is required}"
: "${PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN:?dedicated template PostgreSQL test DSN is required}"
: "${PLATFORMGO_TEST_POSTGRES_RESET_AUTHORIZED:?schema-reset authorization is required}"
: "${PLATFORMGO_TEST_POSTGRES_TEMPLATE_AUTHORIZED:?template database DDL authorization is required}"

# Harness safety and reconciliation run in their own process so their
# intentionally exclusive cluster lifecycle cannot overlap the shared template.
go test ./internal/testsupport/postgresfixture -count=1
go test -race ./internal/testsupport/postgresfixture -count=1
go test ./tests/integration/postgres \
  -run '^(TestTemplateManagerRejectsPrimaryClusterBeforeDDL|TestDatabaseDDLAuthorizationIsIndependentFromSchemaReset|TestTemplateManagerRejectsDirtyMaintenanceRootBeforeDDL|TestFailedTemplateBuildRestoresDatabaseAndRoleInventory|TestTemplateManagerRejectsContaminatedTemplate0|TestTemplateRootIsMaintenanceOnlyAndCurrentClonesAreIsolated|TestTemplateManagerReconcilesUnknownCreateAndDrop|TestTemplateManagerFailsClosedOnForeignRoleDrift|TestTemplateManagerRefusesDropWithActiveSessions|TestCanonicalPostgresPoolIgnoresTemplateDSN)$' \
  -count=1
go test -race ./tests/integration/postgres \
  -run '^(TestTemplateManagerRejectsPrimaryClusterBeforeDDL|TestDatabaseDDLAuthorizationIsIndependentFromSchemaReset|TestTemplateManagerRejectsDirtyMaintenanceRootBeforeDDL|TestFailedTemplateBuildRestoresDatabaseAndRoleInventory|TestTemplateManagerRejectsContaminatedTemplate0|TestTemplateRootIsMaintenanceOnlyAndCurrentClonesAreIsolated|TestTemplateManagerReconcilesUnknownCreateAndDrop|TestTemplateManagerFailsClosedOnForeignRoleDrift|TestTemplateManagerRefusesDropWithActiveSessions|TestCanonicalPostgresPoolIgnoresTemplateDSN)$' \
  -shuffle=on -count=3

selected='^(TestCommandJournalRejectsConflictsAndReplaysCompletedResponse|TestCommandJournalRejectsOutOfOrderAndPrematureCompletion|TestCommandJournalDurablyBindsAccountToOneShard|TestDeploymentShardProvisioningDeterminesConcurrentAuthority|TestCommandJournalRejectsRedundantMetadataMismatch|TestOutboxRetriesUnknownOutcomeWithStableMessageID|TestOutboxDoesNotPublishLaterAccountCommandBeforeEarlierCommand|TestOutboxBlocksMissingPredecessorAndRejectsCorruptCommandBinding|TestInboxClaimAndConsumerEffectCommitTogether)$'
go test ./tests/integration/postgres -run "$selected" -count=1
go test -race ./tests/integration/postgres -run "$selected" -shuffle=on -count=3

# Two separate test binaries contend on the same marker database. The second
# must block on the session advisory lock, then observe a pristine cluster.
cross_process='^TestInboxClaimAndConsumerEffectCommitTogether$'
cross_status=0
go test ./tests/integration/postgres -run "$cross_process" -count=1 &
first_pid=$!
go test ./tests/integration/postgres -run "$cross_process" -count=1 &
second_pid=$!
wait "$first_pid" || cross_status=1
wait "$second_pid" || cross_status=1
if ((cross_status != 0)); then
  echo "cross-process PostgreSQL template serialization failed" >&2
  exit 1
fi
