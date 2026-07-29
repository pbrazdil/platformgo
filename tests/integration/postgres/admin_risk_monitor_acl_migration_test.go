package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	adminRiskACLPreviousMigration = "20260729000100_phase3_admin_fleet_positions_acl.up.sql"
	adminRiskACLMigration         = "20260729000200_phase3_admin_risk_monitor_acl.up.sql"
	adminRiskFunction             = "trading.admin_risk_state_exists()"
	legacyProvisionFunction       = "identity.provision_broker_account(text,text,bigint,text,text,text[],timestamp with time zone)"
)

var adminRiskRelations = []string{
	"trading.accounts",
	"engine.account_shards",
	"identity.account_provisioning_intents",
}

var adminRiskLedgerRelations = []string{
	"ledger.balances",
	"ledger.transactions",
	"ledger.entries",
}

var adminRiskPreservedRelations = append(
	slices.Clone(adminRiskRelations),
	adminRiskLedgerRelations...,
)

func TestAdminRiskACLUpgradesPopulatedCurrentMain(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminRiskMigrationState(t, pool)

	beforeRelations := readAdminRiskRelationStates(t, pool)
	beforeLedgerACL := readAdminRiskRawACLState(t, pool, []string{
		"ledger.balances",
		"ledger.transactions",
		"ledger.entries",
	})
	beforeLegacyFunction := readAdminRiskFunctionCatalog(
		t,
		pool,
		legacyProvisionFunction,
	)
	beforeDefaults := readAdminRiskOwnerDefaults(t, pool)

	var beforeCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("read current-main migration count: %v", err)
	}
	if beforeCount != 31 {
		t.Fatalf("current-main migration count = %d, want 31", beforeCount)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade admin risk ACL: %v", err)
	}

	assertAdminRiskStateUnchanged(t, pool, beforeRelations)
	if after := readAdminRiskRawACLState(t, pool, []string{
		"ledger.balances",
		"ledger.transactions",
		"ledger.entries",
	}); after != beforeLedgerACL {
		t.Fatal("admin risk migration changed raw ledger ACLs")
	}
	if after := readAdminRiskFunctionCatalog(
		t,
		pool,
		legacyProvisionFunction,
	); after != beforeLegacyFunction {
		t.Fatal("admin risk migration changed legacy provisioning function")
	}
	if after := readAdminRiskOwnerDefaults(t, pool); after != beforeDefaults {
		t.Fatal("admin risk migration changed owner default privileges")
	}

	var afterCount int
	var afterTip string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(filename) FROM engine.schema_migrations`,
	).Scan(&afterCount, &afterTip); err != nil {
		t.Fatalf("read upgraded migration history: %v", err)
	}
	if afterCount != 32 || afterTip != adminRiskACLMigration {
		t.Fatalf(
			"upgraded history = count %d tip %q, want 32/%q",
			afterCount,
			afterTip,
			adminRiskACLMigration,
		)
	}

	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "migrations", adminRiskACLMigration,
	))
	if err != nil {
		t.Fatalf("read target migration: %v", err)
	}
	wantChecksum := sha256.Sum256(raw)
	var storedChecksum []byte
	if err := pool.QueryRow(ctx, `
		SELECT checksum
		  FROM engine.schema_migrations
		 WHERE filename = $1`,
		adminRiskACLMigration,
	).Scan(&storedChecksum); err != nil {
		t.Fatalf("read target migration checksum: %v", err)
	}
	if !slices.Equal(storedChecksum, wantChecksum[:]) {
		t.Fatalf("migration checksum = %x, want %x", storedChecksum, wantChecksum)
	}

	assertAdminRiskTableACLs(t, pool)
	assertAdminRiskFunctionDefinition(t, pool)
	assertAdminRiskRuntimeBoundary(t, pool)
	assertAdminRiskLegacyProvisioningBoundary(t, pool)
	assertAdminRiskEngineProvisioningUsable(t, pool)
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify current migration history: %v", err)
	}
	previous := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLPreviousMigration),
	)
	if err := previous.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous binary verification = %v, want schema-ahead", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("idempotent target migration rerun: %v", err)
	}
	var rerunCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&rerunCount); err != nil {
		t.Fatalf("read rerun migration count: %v", err)
	}
	if rerunCount != 32 {
		t.Fatalf("rerun migration count = %d, want 32", rerunCount)
	}
}

func TestAdminRiskACLScrubsHostileTableAndFunctionGrantChains(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("admin_risk_hostile_%d", os.Getpid())
	hostileLogin := fmt.Sprintf("admin_risk_login_%d", os.Getpid())
	dependentRole := "platformgo_projector"
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	loginID := pgx.Identifier{hostileLogin}.Sanitize()
	dependentID := pgx.Identifier{dependentRole}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE ROLE %[1]s NOLOGIN;
		CREATE ROLE %[2]s LOGIN PASSWORD 'admin-risk-test-password'
			NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
		GRANT %[1]s TO %[2]s`,
		hostileID,
		loginID,
	)); err != nil {
		t.Fatalf("create hostile roles: %v", err)
	}
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			if err := cleanupAdminRiskHostileRoles(
				context.Background(),
				pool,
				ownerID,
				hostileID,
				loginID,
			); err != nil {
				t.Errorf("cleanup hostile risk ACL fixture: %v", err)
			}
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA trading;
		CREATE SCHEMA engine;
		GRANT USAGE ON SCHEMA trading, engine TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s WITH GRANT OPTION;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA engine
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s WITH GRANT OPTION;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			GRANT EXECUTE ON FUNCTIONS TO %[2]s WITH GRANT OPTION`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile owner defaults: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(
			t,
			"20260725000200_phase3_identity_compatibility.up.sql",
		),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply identity schema under hostile defaults: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT USAGE ON SCHEMA identity TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA identity
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s WITH GRANT OPTION`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile identity defaults: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLPreviousMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply current-main schema under hostile defaults: %v", err)
	}
	seedAdminRiskMigrationState(t, pool)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON trading.accounts TO PUBLIC;
		GRANT UPDATE (oms_mode) ON trading.accounts TO PUBLIC;
		GRANT SELECT ON engine.account_shards TO PUBLIC;
		GRANT UPDATE (assigned_at) ON engine.account_shards TO PUBLIC;
		GRANT SELECT ON identity.account_provisioning_intents TO PUBLIC;
		GRANT UPDATE (created_at)
			ON identity.account_provisioning_intents TO PUBLIC;
		GRANT SELECT ON trading.accounts, engine.account_shards,
			identity.account_provisioning_intents TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (oms_mode)
			ON trading.accounts TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (assigned_at)
			ON engine.account_shards TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (created_at)
			ON identity.account_provisioning_intents
			TO %[1]s WITH GRANT OPTION`,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile table ACLs: %v", err)
	}

	// The target function does not exist at the previous tip. A same-signature
	// placeholder owned by an unexpected role proves the migration cannot
	// retain hostile SECURITY DEFINER authority while replacing its bytes.
	if _, err := pool.Exec(ctx, "GRANT CREATE ON SCHEMA trading TO "+hostileID); err != nil {
		t.Fatalf("grant hostile function-create fixture: %v", err)
	}
	functionTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hostile function fixture: %v", err)
	}
	if _, err := functionTx.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		_ = functionTx.Rollback(ctx)
		t.Fatalf("assume hostile function owner: %v", err)
	}
	if _, err := functionTx.Exec(ctx, `
		CREATE FUNCTION trading.admin_risk_state_exists()
		RETURNS boolean
		LANGUAGE sql
		AS 'SELECT false'`); err != nil {
		_ = functionTx.Rollback(ctx)
		t.Fatalf("install hostile function fixture: %v", err)
	}
	if err := functionTx.Commit(ctx); err != nil {
		t.Fatalf("commit hostile function fixture: %v", err)
	}
	grantTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hostile dependent grants: %v", err)
	}
	if _, err := grantTx.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("assume hostile grantor: %v", err)
	}
	if _, err := grantTx.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON trading.accounts, engine.account_shards,
			identity.account_provisioning_intents TO %[1]s;
		GRANT UPDATE (oms_mode) ON trading.accounts TO %[1]s;
		GRANT EXECUTE ON FUNCTION trading.admin_risk_state_exists()
			TO %[1]s`,
		dependentID,
	)); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("install dependent ACL chain: %v", err)
	}
	if err := grantTx.Commit(ctx); err != nil {
		t.Fatalf("commit dependent ACL chain: %v", err)
	}

	beforeState := readAdminRiskRelationStates(t, pool)
	beforeDefaults := readAdminRiskOwnerDefaults(t, pool)
	beforeLedgerACL := readAdminRiskRawACLState(t, pool, []string{
		"ledger.balances",
		"ledger.transactions",
		"ledger.entries",
	})
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply admin risk ACL scrub: %v", err)
	}
	assertAdminRiskStateUnchanged(t, pool, beforeState)
	if after := readAdminRiskOwnerDefaults(t, pool); after != beforeDefaults {
		t.Fatal("risk ACL scrub changed hostile owner default definitions")
	}
	if after := readAdminRiskRawACLState(t, pool, []string{
		"ledger.balances",
		"ledger.transactions",
		"ledger.entries",
	}); after != beforeLedgerACL {
		t.Fatal("risk ACL scrub changed raw ledger ACLs")
	}
	assertAdminRiskTableACLs(t, pool)
	assertAdminRiskFunctionDefinition(t, pool)
	assertAdminRiskRuntimeBoundary(t, pool)
	for _, role := range []string{hostileRole, dependentRole, hostileLogin} {
		assertAdminRiskRoleDenied(t, pool, role)
	}

	if err := cleanupAdminRiskHostileRoles(
		ctx,
		pool,
		ownerID,
		hostileID,
		loginID,
	); err != nil {
		t.Fatalf("cleanup hostile risk ACL fixture: %v", err)
	}
	cleaned = true
}

func TestAdminRiskACLAccountsFirstLockRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminRiskMigrationState(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT UPDATE (oms_mode) ON trading.accounts TO PUBLIC`); err != nil {
		t.Fatalf("install rollback ACL fixture: %v", err)
	}
	beforeACL := readAdminRiskRawACLState(t, pool, adminRiskRelations)
	beforeState := readAdminRiskRelationStates(t, pool)

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin account writer blocker: %v", err)
	}
	if _, err := blocker.Exec(ctx, `
		UPDATE trading.accounts SET oms_mode = oms_mode`); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatalf("hold account RowExclusiveLock: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLMigration),
	)
	migrationErr := current.Migrate(ctx)
	assertAdminRiskLockTimeout(t, migrationErr, "accounts first lock")
	assertAdminRiskFailedMigrationUnchanged(
		t,
		pool,
		beforeACL,
		beforeState,
	)
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release account writer blocker: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry after account writer drain: %v", err)
	}
	assertAdminRiskStateUnchanged(t, pool, beforeState)
	assertAdminRiskTableACLs(t, pool)
}

func TestAdminRiskACLPauseAfterShardAdmissionIsBoundedAndRetryable(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminRiskMigrationState(t, pool)
	beforeACL := readAdminRiskRawACLState(t, pool, adminRiskRelations)
	beforeState := readAdminRiskRelationStates(t, pool)

	admission, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin paused admission: %v", err)
	}
	if _, err := admission.Exec(ctx, `
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('urn:xb:account:admin-risk-paused', 7)`); err != nil {
		_ = admission.Rollback(ctx)
		t.Fatalf("pause admission after shard assignment: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLMigration),
	)
	result := make(chan error, 1)
	go func() {
		result <- current.Migrate(ctx)
	}()
	waitAdminRiskRelationLock(
		t,
		pool,
		result,
		"engine.account_shards",
		"ShareLock",
	)
	migrationErr := <-result
	assertAdminRiskLockTimeout(t, migrationErr, "paused shard admission")
	assertAdminRiskFailedMigrationUnchanged(
		t,
		pool,
		beforeACL,
		beforeState,
	)

	if _, err := admission.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, broker_subject, login, normalized_login
		) VALUES (
			'urn:xb:user:admin-risk-paused',
			'urn:xb:tenant:admin-risk-paused',
			'admin-risk-paused',
			'admin-risk-paused'
		);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:urn:xb:account:admin-risk-paused',
			'admin-risk-paused',
			decode(repeat('92', 32), 'hex'),
			'019fa940-0000-4000-8000-000000000002',
			'in_progress',
			'2027-01-01T00:00:00Z'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019fa940-0000-4000-8000-000000000002',
			'urn:xb:account:admin-risk-paused',
			1,
			'configure_account',
			1,
			'{}',
			'pending',
			2
		);
		INSERT INTO identity.account_provisioning_intents (
			command_id, account_id, broker_subject, user_id, login,
			base_currency, market_venue, permitted_classes, created_at
		) VALUES (
			'019fa940-0000-4000-8000-000000000002',
			'urn:xb:account:admin-risk-paused',
			'urn:xb:tenant:admin-risk-paused',
			'urn:xb:user:admin-risk-paused',
			94002,
			'USDC',
			'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'],
			'2026-07-29T18:00:00Z'
		)`); err != nil {
		_ = admission.Rollback(ctx)
		t.Fatalf("finish paused admission after migration rollback: %v", err)
	}
	if err := admission.Commit(ctx); err != nil {
		t.Fatalf("commit paused admission: %v", err)
	}
	afterAdmission := readAdminRiskRelationStates(t, pool)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry after paused admission commits: %v", err)
	}
	assertAdminRiskStateUnchanged(t, pool, afterAdmission)
	assertAdminRiskTableACLs(t, pool)
}

func TestAdminRiskACLLateIntentLockRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminRiskMigrationState(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT UPDATE (oms_mode) ON trading.accounts TO PUBLIC;
		GRANT UPDATE (assigned_at) ON engine.account_shards TO PUBLIC`); err != nil {
		t.Fatalf("install partial-rollback ACL fixture: %v", err)
	}
	beforeACL := readAdminRiskRawACLState(t, pool, adminRiskRelations)
	beforeState := readAdminRiskRelationStates(t, pool)

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin late intent blocker: %v", err)
	}
	if _, err := blocker.Exec(ctx, `
		LOCK TABLE identity.account_provisioning_intents
		IN ROW EXCLUSIVE MODE`); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatalf("hold intent RowExclusiveLock: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminRiskACLMigration),
	)
	result := make(chan error, 1)
	go func() {
		result <- current.Migrate(ctx)
	}()
	waitAdminRiskRelationLock(
		t,
		pool,
		result,
		"identity.account_provisioning_intents",
		"ShareLock",
	)
	migrationErr := <-result
	assertAdminRiskLockTimeout(t, migrationErr, "late intent lock")
	assertAdminRiskFailedMigrationUnchanged(
		t,
		pool,
		beforeACL,
		beforeState,
	)
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release late intent blocker: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry after intent writer drain: %v", err)
	}
	assertAdminRiskStateUnchanged(t, pool, beforeState)
	assertAdminRiskTableACLs(t, pool)
}

type adminRiskRelationState struct {
	Digest   [sha256.Size]byte
	FileNode uint32
}

type adminRiskFunctionCatalog struct {
	Owner       string
	Result      string
	Language    string
	Volatility  string
	Security    bool
	Leakproof   bool
	ArgumentNum int
	Arguments   string
	Config      string
	Source      string
	ACL         string
}

func seedAdminRiskMigrationState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO identity.users (
			user_id, broker_subject, login, normalized_login
		) VALUES (
			'urn:xb:user:admin-risk-migration',
			'urn:xb:tenant:admin-risk-migration',
			'admin-risk-migration',
			'admin-risk-migration'
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:admin-risk-migration', 'NETTING');
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('urn:xb:account:admin-risk-migration', 7);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:urn:xb:account:admin-risk-migration',
			'admin-risk-migration',
			decode(repeat('91', 32), 'hex'),
			'019fa940-0000-4000-8000-000000000001',
			'in_progress',
			'2027-01-01T00:00:00Z'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019fa940-0000-4000-8000-000000000001',
			'urn:xb:account:admin-risk-migration',
			1,
			'configure_account',
			1,
			'{}',
			'pending',
			1
		);
		INSERT INTO identity.account_provisioning_intents (
			command_id, account_id, broker_subject, user_id, login,
			base_currency, market_venue, permitted_classes, created_at
		) VALUES (
			'019fa940-0000-4000-8000-000000000001',
			'urn:xb:account:admin-risk-migration',
			'urn:xb:tenant:admin-risk-migration',
			'urn:xb:user:admin-risk-migration',
			94001,
			'USDC',
			'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'],
			'2026-07-29T17:00:00Z'
		);
		INSERT INTO ledger.transactions (
			transaction_id, business_key, input_id, logical_time
		) VALUES (
			'019fa940-0000-4000-8000-000000000011',
			'admin-risk-migration',
			'019fa940-0000-4000-8000-000000000012',
			1
		);
		INSERT INTO ledger.entries (
			entry_id, transaction_id, account_id, currency, amount
		) VALUES
		(
			'019fa940-0000-4000-8000-000000000013',
			'019fa940-0000-4000-8000-000000000011',
			'urn:xb:account:admin-risk-migration',
			'USDC',
			10
		),
		(
			'019fa940-0000-4000-8000-000000000014',
			'019fa940-0000-4000-8000-000000000011',
			'urn:xb:account:admin-risk-counterparty',
			'USDC',
			-10
		);
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity,
			ledger_sequence, updated_at
		) VALUES (
			'urn:xb:account:admin-risk-migration',
			'USDC',
			10,
			2,
			8,
			10,
			1,
			'2026-07-29T17:00:00Z'
		)`); err != nil {
		t.Fatalf("seed admin risk migration state: %v", err)
	}
}

func readAdminRiskRelationStates(
	t *testing.T,
	pool *pgxpool.Pool,
) map[string]adminRiskRelationState {
	t.Helper()
	states := make(
		map[string]adminRiskRelationState,
		len(adminRiskPreservedRelations),
	)
	for _, relation := range adminRiskPreservedRelations {
		relationID := pgx.Identifier(strings.Split(relation, ".")).Sanitize()
		var canonical string
		var state adminRiskRelationState
		var query string
		switch relation {
		case "ledger.balances":
			query = fmt.Sprintf(`
				SELECT
					COALESCE(
						jsonb_agg(
							jsonb_build_array(
								account_id,
								currency,
								total,
								used,
								free,
								equity,
								ledger_sequence,
								updated_at
							)
							ORDER BY account_id, currency
						),
						'[]'::jsonb
					)::text,
					pg_catalog.pg_relation_filenode(%s::pg_catalog.regclass)
				  FROM %s`,
				quoteAdminRiskLiteral(relation),
				relationID,
			)
		case "ledger.transactions":
			query = fmt.Sprintf(`
				SELECT
					COALESCE(
						jsonb_agg(
							jsonb_build_array(
								transaction_id,
								business_key,
								input_id,
								logical_time,
								created_at
							)
							ORDER BY transaction_id
						),
						'[]'::jsonb
					)::text,
					pg_catalog.pg_relation_filenode(%s::pg_catalog.regclass)
				  FROM %s`,
				quoteAdminRiskLiteral(relation),
				relationID,
			)
		case "ledger.entries":
			query = fmt.Sprintf(`
				SELECT
					COALESCE(
						jsonb_agg(
							jsonb_build_array(
								entry_id,
								transaction_id,
								account_id,
								currency,
								amount
							)
							ORDER BY entry_id
						),
						'[]'::jsonb
					)::text,
					pg_catalog.pg_relation_filenode(%s::pg_catalog.regclass)
				  FROM %s`,
				quoteAdminRiskLiteral(relation),
				relationID,
			)
		default:
			query = fmt.Sprintf(`
			SELECT
				COALESCE(
					jsonb_agg(to_jsonb(row_value) ORDER BY to_jsonb(row_value)::text),
					'[]'::jsonb
				)::text,
				pg_catalog.pg_relation_filenode(%s::pg_catalog.regclass)
			  FROM %s AS row_value`,
				quoteAdminRiskLiteral(relation),
				relationID,
			)
		}
		if err := pool.QueryRow(context.Background(), query).Scan(
			&canonical,
			&state.FileNode,
		); err != nil {
			t.Fatalf("read %s digest and relation file: %v", relation, err)
		}
		state.Digest = sha256.Sum256([]byte(canonical))
		states[relation] = state
	}
	return states
}

func assertAdminRiskStateUnchanged(
	t *testing.T,
	pool *pgxpool.Pool,
	before map[string]adminRiskRelationState,
) {
	t.Helper()
	after := readAdminRiskRelationStates(t, pool)
	for _, relation := range adminRiskPreservedRelations {
		if after[relation] != before[relation] {
			t.Fatalf(
				"admin risk migration changed %s data/file: before=%v after=%v",
				relation,
				before[relation],
				after[relation],
			)
		}
	}
}

func readAdminRiskRawACLState(
	t *testing.T,
	pool *pgxpool.Pool,
	relations []string,
) string {
	t.Helper()
	var result strings.Builder
	for _, relation := range relations {
		var tableACL, columnACL string
		if err := pool.QueryRow(context.Background(), `
			SELECT
				COALESCE(class.relacl::text, '<NULL>'),
				COALESCE(
					(
						SELECT string_agg(
							pg_catalog.format(
								'%s=%s',
								attribute.attname,
								COALESCE(attribute.attacl::text, '<NULL>')
							),
							E'\n'
							ORDER BY attribute.attnum
						)
						  FROM pg_catalog.pg_attribute AS attribute
						 WHERE attribute.attrelid = class.oid
						   AND attribute.attnum > 0
						   AND NOT attribute.attisdropped
					),
					''
				)
			  FROM pg_catalog.pg_class AS class
			 WHERE class.oid = $1::pg_catalog.regclass`,
			relation,
		).Scan(&tableACL, &columnACL); err != nil {
			t.Fatalf("read %s raw ACL: %v", relation, err)
		}
		fmt.Fprintf(&result, "%s\n%s\n%s\n", relation, tableACL, columnACL)
	}
	return result.String()
}

func readAdminRiskFunctionCatalog(
	t *testing.T,
	pool *pgxpool.Pool,
	signature string,
) adminRiskFunctionCatalog {
	t.Helper()
	var catalog adminRiskFunctionCatalog
	if err := pool.QueryRow(context.Background(), `
		SELECT
			pg_catalog.pg_get_userbyid(procedure.proowner),
			procedure.prorettype::pg_catalog.regtype::text,
			language.lanname,
			procedure.provolatile::text,
			procedure.prosecdef,
			procedure.proleakproof,
			procedure.pronargs,
			pg_catalog.pg_get_function_arguments(procedure.oid),
			COALESCE(procedure.proconfig::text, '<NULL>'),
			procedure.prosrc,
			COALESCE(procedure.proacl::text, '<NULL>')
		  FROM pg_catalog.pg_proc AS procedure
		  JOIN pg_catalog.pg_language AS language
		    ON language.oid = procedure.prolang
		 WHERE procedure.oid = $1::pg_catalog.regprocedure`,
		signature,
	).Scan(
		&catalog.Owner,
		&catalog.Result,
		&catalog.Language,
		&catalog.Volatility,
		&catalog.Security,
		&catalog.Leakproof,
		&catalog.ArgumentNum,
		&catalog.Arguments,
		&catalog.Config,
		&catalog.Source,
		&catalog.ACL,
	); err != nil {
		t.Fatalf("read function catalog for %s: %v", signature, err)
	}
	return catalog
}

func readAdminRiskOwnerDefaults(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var defaults string
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(
			string_agg(
				pg_catalog.format(
					'%s|%s|%s|%s',
					pg_catalog.pg_get_userbyid(default_acl.defaclrole),
					COALESCE(namespace.nspname, ''),
					default_acl.defaclobjtype,
					default_acl.defaclacl::text
				),
				E'\n'
				ORDER BY
					default_acl.defaclrole,
					namespace.nspname,
					default_acl.defaclobjtype
			),
			''
		)
		  FROM pg_catalog.pg_default_acl AS default_acl
		  LEFT JOIN pg_catalog.pg_namespace AS namespace
		    ON namespace.oid = default_acl.defaclnamespace`,
	).Scan(&defaults); err != nil {
		t.Fatalf("read owner default privileges: %v", err)
	}
	return defaults
}

func assertAdminRiskTableACLs(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	wants := map[string][]string{
		"trading.accounts": {
			"platformgo_api|SELECT|false|table",
			"platformgo_engine|INSERT|false|table",
			"platformgo_engine|SELECT|false|table",
			"platformgo_engine|UPDATE|false|table",
		},
		"engine.account_shards": {
			"platformgo_api|INSERT|false|table",
			"platformgo_api|SELECT|false|table",
			"platformgo_engine|SELECT|false|table",
		},
		"identity.account_provisioning_intents": {
			"platformgo_api|INSERT|false|table",
			"platformgo_api|SELECT|false|table",
			"platformgo_engine|SELECT|false|table",
		},
	}
	for _, relation := range adminRiskRelations {
		got := readAdminRiskNonOwnerACL(t, pool, relation)
		want := wants[relation]
		sort.Strings(want)
		if !slices.Equal(got, want) {
			t.Fatalf("%s ACL = %v, want exact %v", relation, got, want)
		}
	}
}

func readAdminRiskNonOwnerACL(
	t *testing.T,
	pool *pgxpool.Pool,
	relation string,
) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		WITH relation AS (
			SELECT oid, relowner, relacl
			  FROM pg_catalog.pg_class
			 WHERE oid = $1::pg_catalog.regclass
		),
		table_acl AS (
			SELECT
				CASE
					WHEN privilege.grantee = 0 THEN 'PUBLIC'
					ELSE role.rolname
				END AS grantee,
				privilege.privilege_type,
				privilege.is_grantable,
				'table'::text AS scope,
				relation.relowner
			  FROM relation
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				COALESCE(
					relation.relacl,
					pg_catalog.acldefault('r', relation.relowner)
				)
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS role
			    ON role.oid = privilege.grantee
		),
		column_acl AS (
			SELECT
				CASE
					WHEN privilege.grantee = 0 THEN 'PUBLIC'
					ELSE role.rolname
				END AS grantee,
				privilege.privilege_type,
				privilege.is_grantable,
				'column:' || attribute.attname AS scope,
				relation.relowner
			  FROM relation
			  JOIN pg_catalog.pg_attribute AS attribute
			    ON attribute.attrelid = relation.oid
			   AND attribute.attnum > 0
			   AND NOT attribute.attisdropped
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				attribute.attacl
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS role
			    ON role.oid = privilege.grantee
			 WHERE attribute.attacl IS NOT NULL
		)
		SELECT grantee, privilege_type, is_grantable, scope
		  FROM (
			SELECT * FROM table_acl
			UNION ALL
			SELECT * FROM column_acl
		  ) AS acl
		 WHERE acl.grantee IS DISTINCT FROM (
			SELECT rolname FROM pg_catalog.pg_roles WHERE oid = acl.relowner
		 )
		 ORDER BY grantee, privilege_type, is_grantable, scope`,
		relation,
	)
	if err != nil {
		t.Fatalf("inspect complete %s ACL: %v", relation, err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var grantee, privilege, scope string
		var grantable bool
		if err := rows.Scan(&grantee, &privilege, &grantable, &scope); err != nil {
			t.Fatalf("scan complete %s ACL: %v", relation, err)
		}
		got = append(
			got,
			fmt.Sprintf("%s|%s|%t|%s", grantee, privilege, grantable, scope),
		)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate complete %s ACL: %v", relation, err)
	}
	return got
}

func assertAdminRiskFunctionDefinition(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var overloads int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		  FROM pg_catalog.pg_proc AS procedure
		  JOIN pg_catalog.pg_namespace AS namespace
		    ON namespace.oid = procedure.pronamespace
		 WHERE namespace.nspname = 'trading'
		   AND procedure.proname = 'admin_risk_state_exists'`,
	).Scan(&overloads); err != nil {
		t.Fatalf("count admin risk function overloads: %v", err)
	}
	if overloads != 1 {
		t.Fatalf("admin risk function overload count = %d, want 1", overloads)
	}
	got := readAdminRiskFunctionCatalog(t, pool, adminRiskFunction)
	var migrationOwner string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT current_user",
	).Scan(&migrationOwner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	if got.Owner != migrationOwner ||
		got.Result != "boolean" ||
		got.Language != "sql" ||
		got.Volatility != "s" ||
		!got.Security ||
		got.Leakproof ||
		got.ArgumentNum != 0 ||
		got.Arguments != "" ||
		got.Config != "{search_path=pg_catalog}" {
		t.Fatalf("admin risk function catalog = %#v", got)
	}
	wantSource := strings.Join(strings.Fields(`
		SELECT
			EXISTS (SELECT 1 FROM trading.accounts)
			OR EXISTS (SELECT 1 FROM trading.commands)
			OR EXISTS (SELECT 1 FROM engine.account_shards)
			OR EXISTS (SELECT 1 FROM ledger.balances)
			OR EXISTS (SELECT 1 FROM ledger.transactions)
			OR EXISTS (SELECT 1 FROM ledger.entries)
	`), " ")
	if source := strings.Join(strings.Fields(got.Source), " "); source != wantSource {
		t.Fatalf("admin risk function source = %q, want %q", source, wantSource)
	}

	rows, err := pool.Query(context.Background(), `
		WITH procedure AS (
			SELECT oid, proowner, proacl
			  FROM pg_catalog.pg_proc
			 WHERE oid = $1::pg_catalog.regprocedure
		)
		SELECT
			CASE
				WHEN privilege.grantee = 0 THEN 'PUBLIC'
				ELSE role.rolname
			END,
			privilege.privilege_type,
			privilege.is_grantable
		  FROM procedure
		  CROSS JOIN LATERAL pg_catalog.aclexplode(
			COALESCE(
				procedure.proacl,
				pg_catalog.acldefault('f', procedure.proowner)
			)
		  ) AS privilege
		  LEFT JOIN pg_catalog.pg_roles AS role
		    ON role.oid = privilege.grantee
		 WHERE privilege.grantee <> procedure.proowner
		 ORDER BY 1, 2, 3`,
		adminRiskFunction,
	)
	if err != nil {
		t.Fatalf("inspect admin risk function ACL: %v", err)
	}
	defer rows.Close()
	var acl []string
	for rows.Next() {
		var grantee, privilege string
		var grantable bool
		if err := rows.Scan(&grantee, &privilege, &grantable); err != nil {
			t.Fatalf("scan admin risk function ACL: %v", err)
		}
		acl = append(
			acl,
			fmt.Sprintf("%s|%s|%t", grantee, privilege, grantable),
		)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate admin risk function ACL: %v", err)
	}
	wantACL := []string{"platformgo_api|EXECUTE|false"}
	if !slices.Equal(acl, wantACL) {
		t.Fatalf("admin risk function ACL = %v, want %v", acl, wantACL)
	}
}

func assertAdminRiskRuntimeBoundary(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin API risk function probe: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_api",
	); err != nil {
		t.Fatalf("assume platformgo_api: %v", err)
	}
	var exists bool
	if err := tx.QueryRow(context.Background(), `
		SELECT trading.admin_risk_state_exists()`,
	).Scan(&exists); err != nil {
		t.Fatalf("execute admin risk state predicate as API: %v", err)
	}
	if !exists {
		t.Fatal("populated admin risk state predicate = false, want true")
	}
	for _, statement := range []string{
		"SELECT entry_id FROM ledger.entries",
		"SELECT transaction_id FROM ledger.transactions",
	} {
		denialTx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin raw ledger denial probe: %v", err)
		}
		if _, err := denialTx.Exec(
			context.Background(),
			"SET LOCAL ROLE platformgo_api",
		); err != nil {
			_ = denialTx.Rollback(context.Background())
			t.Fatalf("assume platformgo_api for raw ledger denial: %v", err)
		}
		_, statementErr := denialTx.Exec(context.Background(), statement)
		assertAdminRiskSQLState(t, statementErr, "42501")
		if err := denialTx.Rollback(context.Background()); err != nil {
			t.Fatalf("roll back raw ledger denial probe: %v", err)
		}
	}
}

func assertAdminRiskLegacyProvisioningBoundary(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	var apiCanExecute bool
	var engineCanReadIntent bool
	var engineCanInsertAccount bool
	if err := pool.QueryRow(context.Background(), `
		SELECT
			pg_catalog.has_function_privilege(
				'platformgo_api',
				$1,
				'EXECUTE'
			),
			pg_catalog.has_table_privilege(
				'platformgo_engine',
				'identity.account_provisioning_intents',
				'SELECT'
			),
			pg_catalog.has_table_privilege(
				'platformgo_engine',
				'trading.accounts',
				'INSERT'
			)`,
		legacyProvisionFunction,
	).Scan(
		&apiCanExecute,
		&engineCanReadIntent,
		&engineCanInsertAccount,
	); err != nil {
		t.Fatalf("inspect legacy/engine provisioning boundary: %v", err)
	}
	if apiCanExecute || !engineCanReadIntent || !engineCanInsertAccount {
		t.Fatalf(
			"legacy/engine provisioning privileges = api execute %t, "+
				"engine intent read %t, engine account insert %t",
			apiCanExecute,
			engineCanReadIntent,
			engineCanInsertAccount,
		)
	}
}

func assertAdminRiskEngineProvisioningUsable(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	ctx := context.Background()
	for _, test := range []struct {
		name      string
		commandID string
		accountID string
		userID    string
		login     int64
		commit    bool
	}{
		{
			name:      "commit",
			commandID: "019fa940-0000-4000-8000-000000000021",
			accountID: "urn:xb:account:admin-risk-engine-commit",
			userID:    "urn:xb:user:admin-risk-engine-commit",
			login:     94021,
			commit:    true,
		},
		{
			name:      "rollback",
			commandID: "019fa940-0000-4000-8000-000000000022",
			accountID: "urn:xb:account:admin-risk-engine-rollback",
			userID:    "urn:xb:user:admin-risk-engine-rollback",
			login:     94022,
		},
	} {
		test := test
		t.Run("engine provisioning "+test.name, func(t *testing.T) {
			brokerSubject := "urn:xb:tenant:admin-risk-engine-" + test.name
			loginText := "admin-risk-engine-" + test.name
			if _, err := pool.Exec(ctx, `
				WITH inserted_user AS (
					INSERT INTO identity.users (
						user_id, broker_subject, login, normalized_login
					) VALUES ($1,$2,$3,$3)
					RETURNING 1
				),
				inserted_idempotency AS (
					INSERT INTO trading.idempotency_records (
						scope, idempotency_key, request_hash, command_id,
						state, expires_at
					)
					SELECT
						'account:' || $4,
						$3,
						decode(repeat('93', 32), 'hex'),
						$5,
						'in_progress',
						'2027-01-01T00:00:00Z'
					  FROM inserted_user
					RETURNING 1
				),
				inserted_command AS (
					INSERT INTO trading.commands (
						command_id, account_id, account_sequence, command_type,
						schema_version, canonical_payload, status, logical_time
					)
					SELECT $5,$4,1,'configure_account',1,'{}','pending',$6
					  FROM inserted_idempotency
					RETURNING 1
				),
				inserted_shard AS (
					INSERT INTO engine.account_shards (account_id, shard_id)
					SELECT $4,7 FROM inserted_command
					RETURNING 1
				)
				INSERT INTO identity.account_provisioning_intents (
					command_id, account_id, broker_subject, user_id, login,
					base_currency, market_venue, permitted_classes, created_at
				)
				SELECT
					$5,$4,$2,$1,$7,'USDC','HYPERLIQUID',
					ARRAY['CRYPTOCURRENCY'],
					'2026-07-29T19:00:00Z'
				  FROM inserted_shard`,
				test.userID,
				brokerSubject,
				loginText,
				test.accountID,
				test.commandID,
				test.login,
				test.login,
			); err != nil {
				t.Fatalf("seed %s engine provisioning intent: %v", test.name, err)
			}

			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin %s engine provisioning: %v", test.name, err)
			}
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE platformgo_engine"); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatalf("assume engine role for %s provisioning: %v", test.name, err)
			}
			var loadedAccount string
			if err := tx.QueryRow(ctx, `
				SELECT account_id
				  FROM identity.account_provisioning_intents
				 WHERE command_id = $1`,
				test.commandID,
			).Scan(&loadedAccount); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatalf("load %s provisioning intent as engine: %v", test.name, err)
			}
			if loadedAccount != test.accountID {
				_ = tx.Rollback(ctx)
				t.Fatalf(
					"%s provisioning intent account = %q, want %q",
					test.name,
					loadedAccount,
					test.accountID,
				)
			}
			if _, err := tx.Exec(ctx, `
				WITH inserted_account AS (
					INSERT INTO trading.accounts (account_id, oms_mode)
					VALUES ($1, 'NETTING')
					RETURNING 1
				),
				inserted_ownership AS (
					INSERT INTO identity.user_accounts (
						user_id, account_id, broker_subject, created_at
					)
					SELECT $2,$1,$3,'2026-07-29T19:00:00Z'
					  FROM inserted_account
					RETURNING 1
				)
				INSERT INTO identity.account_profiles (
					account_id, broker_subject, login, base_currency,
					market_venue, permitted_classes, created_at
				)
				SELECT
					$1,$3,$4,'USDC','HYPERLIQUID',
					ARRAY['CRYPTOCURRENCY'],
					'2026-07-29T19:00:00Z'
				  FROM inserted_ownership`,
				test.accountID,
				test.userID,
				brokerSubject,
				test.login,
			); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatalf("persist %s provisioning graph as engine: %v", test.name, err)
			}
			if test.commit {
				if err := tx.Commit(ctx); err != nil {
					t.Fatalf("commit engine provisioning graph: %v", err)
				}
			} else if err := tx.Rollback(ctx); err != nil {
				t.Fatalf("roll back engine provisioning graph: %v", err)
			}

			var accounts, ownerships, profiles int
			if err := pool.QueryRow(ctx, `
				SELECT
					(SELECT count(*) FROM trading.accounts WHERE account_id = $1),
					(SELECT count(*) FROM identity.user_accounts WHERE account_id = $1),
					(SELECT count(*) FROM identity.account_profiles WHERE account_id = $1)`,
				test.accountID,
			).Scan(&accounts, &ownerships, &profiles); err != nil {
				t.Fatalf("inspect %s provisioning graph: %v", test.name, err)
			}
			want := 0
			if test.commit {
				want = 1
			}
			if accounts != want || ownerships != want || profiles != want {
				t.Fatalf(
					"%s provisioning graph accounts/ownerships/profiles = %d/%d/%d, want %d/%d/%d",
					test.name,
					accounts,
					ownerships,
					profiles,
					want,
					want,
					want,
				)
			}
		})
	}

	denialTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin legacy API denial probe: %v", err)
	}
	if _, err := denialTx.Exec(ctx, "SET LOCAL ROLE platformgo_api"); err != nil {
		_ = denialTx.Rollback(ctx)
		t.Fatalf("assume API role for legacy denial: %v", err)
	}
	_, denialErr := denialTx.Exec(ctx, `
		SELECT identity.provision_broker_account(
			'urn:xb:account:admin-risk-forged',
			'urn:xb:user:admin-risk-forged',
			94999,
			'USDC',
			'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'],
			'2026-07-29T19:00:00Z'
		)`)
	assertAdminRiskSQLState(t, denialErr, "42501")
	if err := denialTx.Rollback(ctx); err != nil {
		t.Fatalf("roll back legacy API denial probe: %v", err)
	}
}

func assertAdminRiskRoleDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	for _, statement := range []string{
		"SELECT account_id FROM trading.accounts",
		"SELECT account_id FROM engine.account_shards",
		"SELECT account_id FROM identity.account_provisioning_intents",
		"SELECT trading.admin_risk_state_exists()",
	} {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin %s denied risk operation: %v", role, err)
		}
		if _, err := tx.Exec(
			context.Background(),
			"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
		); err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatalf("assume role %s: %v", role, err)
		}
		_, statementErr := tx.Exec(context.Background(), statement)
		_ = tx.Rollback(context.Background())
		assertAdminRiskSQLState(t, statementErr, "42501")
	}
}

func assertAdminRiskFailedMigrationUnchanged(
	t *testing.T,
	pool *pgxpool.Pool,
	beforeACL string,
	beforeState map[string]adminRiskRelationState,
) {
	t.Helper()
	var journaled bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			  FROM engine.schema_migrations
			 WHERE filename = $1
		)`,
		adminRiskACLMigration,
	).Scan(&journaled); err != nil {
		t.Fatalf("inspect failed risk migration journal: %v", err)
	}
	if journaled {
		t.Fatal("failed admin risk migration was journaled")
	}
	if afterACL := readAdminRiskRawACLState(
		t,
		pool,
		adminRiskRelations,
	); afterACL != beforeACL {
		t.Fatal("failed admin risk migration changed table ACLs")
	}
	assertAdminRiskStateUnchanged(t, pool, beforeState)
}

func waitAdminRiskRelationLock(
	t *testing.T,
	pool *pgxpool.Pool,
	result <-chan error,
	relation string,
	mode string,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(context.Background(), `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_locks
				 WHERE relation = $1::pg_catalog.regclass
				   AND mode = $2
				   AND NOT granted
			)`,
			relation,
			mode,
		).Scan(&waiting); err != nil {
			t.Fatalf("inspect %s %s lock: %v", relation, mode, err)
		}
		if waiting {
			return
		}
		select {
		case migrationErr := <-result:
			t.Fatalf(
				"migration completed before waiting for %s %s: %v",
				relation,
				mode,
				migrationErr,
			)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s %s", relation, mode)
		}
		runtime.Gosched()
	}
}

func assertAdminRiskLockTimeout(
	t *testing.T,
	err error,
	stage string,
) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("%s migration error = %v, want SQLSTATE 55P03", stage, err)
	}
	if postgresError.Code == "40P01" {
		t.Fatalf("%s migration deadlocked: %v", stage, err)
	}
}

func assertAdminRiskSQLState(t *testing.T, err error, want string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != want {
		t.Fatalf("PostgreSQL error = %v, want SQLSTATE %s", err, want)
	}
}

func cleanupAdminRiskHostileRoles(
	ctx context.Context,
	pool *pgxpool.Pool,
	ownerID string,
	hostileID string,
	loginID string,
) error {
	if _, err := pool.Exec(ctx, "DROP ROLE IF EXISTS "+loginID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		DO $cleanup$
		DECLARE
			relation_name pg_catalog.text;
			column_name pg_catalog.name;
		BEGIN
			FOREACH relation_name IN ARRAY ARRAY[
				'trading.accounts',
				'engine.account_shards',
				'identity.account_provisioning_intents'
			]
			LOOP
				IF pg_catalog.to_regclass(relation_name) IS NOT NULL THEN
					EXECUTE pg_catalog.format(
						'REVOKE ALL PRIVILEGES ON TABLE %%s FROM %[1]s CASCADE',
						pg_catalog.to_regclass(relation_name)
					);
					FOR column_name IN
						SELECT attribute.attname
						  FROM pg_catalog.pg_attribute AS attribute
						 WHERE attribute.attrelid =
						       pg_catalog.to_regclass(relation_name)
						   AND attribute.attnum > 0
						   AND NOT attribute.attisdropped
					LOOP
						EXECUTE pg_catalog.format(
							'REVOKE ALL PRIVILEGES (%%I) ON TABLE %%s FROM %[1]s CASCADE',
							column_name,
							pg_catalog.to_regclass(relation_name)
						);
					END LOOP;
				END IF;
			END LOOP;
			IF pg_catalog.to_regprocedure(
				'trading.admin_risk_state_exists()'
			) IS NOT NULL THEN
				EXECUTE
					'REVOKE ALL PRIVILEGES ON FUNCTION ' ||
					'trading.admin_risk_state_exists() FROM %[1]s CASCADE';
			END IF;
		END
		$cleanup$`,
		hostileID,
	)); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA engine
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA identity
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			REVOKE ALL PRIVILEGES ON FUNCTIONS FROM %[2]s;
		REVOKE ALL PRIVILEGES ON SCHEMA trading, engine, identity
			FROM %[2]s CASCADE;
		DROP OWNED BY %[2]s CASCADE`,
		ownerID,
		hostileID,
	)); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, "DROP ROLE "+hostileID)
	return err
}

func quoteAdminRiskLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
