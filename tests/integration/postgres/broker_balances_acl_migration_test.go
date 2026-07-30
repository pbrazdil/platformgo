package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	brokerBalancesACLPreviousMigration = "20260729000600_phase3_validate_fill_leverage_finite.up.sql"
	brokerBalancesACLMigration         = "20260730000100_phase3_broker_balances_acl.up.sql"
)

var brokerBalancesACLRelations = []string{
	"identity.user_accounts",
	"identity.account_profiles",
	"ledger.balances",
}

func assertBrokerBalancesACLPostgres19Beta2(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	var version string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT current_setting('server_version')",
	).Scan(&version); err != nil {
		t.Fatalf("read PostgreSQL server version: %v", err)
	}
	if version != "19beta2" && !strings.HasPrefix(version, "19beta2 ") {
		t.Fatalf("PostgreSQL server version = %q, want PostgreSQL 19 Beta 2", version)
	}
}

func TestBrokerBalancesACLMigrationUpgradesHostileCurrentMain(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerBalancesACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("broker_balances_hostile_%d", os.Getpid())
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	dependentRole := "platformgo_projector"
	dependentID := pgx.Identifier{dependentRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile role: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`
			ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
				REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
			DROP OWNED BY %[2]s CASCADE;
			DROP ROLE %[2]s`,
			ownerID,
			hostileID,
		)); err != nil {
			t.Errorf("cleanup hostile broker-balances role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s WITH GRANT OPTION`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile owner defaults: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerBalancesACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema under hostile defaults: %v", err)
	}
	seedBrokerBalancesACLState(t, pool)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT USAGE ON SCHEMA identity, ledger TO %[1]s;
		GRANT SELECT ON identity.user_accounts TO PUBLIC;
		GRANT SELECT ON identity.account_profiles TO PUBLIC;
		GRANT SELECT ON ledger.balances TO PUBLIC;
		GRANT UPDATE (broker_subject) ON identity.user_accounts
			TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (broker_subject) ON identity.account_profiles
			TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (equity) ON ledger.balances
			TO %[1]s WITH GRANT OPTION`,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile direct ACLs: %v", err)
	}
	delegation, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin dependent hostile grants: %v", err)
	}
	if _, err := delegation.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		_ = delegation.Rollback(ctx)
		t.Fatalf("assume hostile grantor: %v", err)
	}
	if _, err := delegation.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON
			identity.user_accounts,
			identity.account_profiles,
			ledger.balances
		TO %[1]s;
		GRANT UPDATE (broker_subject) ON identity.user_accounts TO %[1]s;
		GRANT UPDATE (broker_subject) ON identity.account_profiles TO %[1]s;
		GRANT UPDATE (equity) ON ledger.balances TO %[1]s`,
		dependentID,
	)); err != nil {
		_ = delegation.Rollback(ctx)
		t.Fatalf("delegate hostile grants: %v", err)
	}
	if err := delegation.Commit(ctx); err != nil {
		t.Fatalf("commit dependent hostile grants: %v", err)
	}
	assertBrokerBalancesACLFixtureVulnerable(t, pool, hostileRole, dependentRole)

	beforeState := readBrokerBalancesACLState(t, pool)
	beforeDefaults := readAdminRiskOwnerDefaults(t, pool)
	beforeHistory := readBrokerBalancesACLHistory(t, pool)
	files := migrationFilesThrough(t, brokerBalancesACLMigration)
	if _, exists := files[brokerBalancesACLMigration]; !exists {
		t.Fatalf(
			"RED: expected forward migration %s is missing after current tip %s",
			brokerBalancesACLMigration,
			brokerBalancesACLPreviousMigration,
		)
	}

	current := platformpostgres.NewMigrator(pool, files)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply broker-balances ACL migration: %v", err)
	}
	assertBrokerBalancesACLStateUnchanged(t, pool, beforeState)
	if after := readAdminRiskOwnerDefaults(t, pool); after != beforeDefaults {
		t.Fatal("broker-balances ACL migration changed owner default privileges")
	}
	assertBrokerBalancesACLHistoryAdvanced(t, pool, beforeHistory)
	assertBrokerBalancesACLAllowlist(t, pool)
	assertBrokerBalancesACLRuntimePrivileges(t, pool)
	assertBrokerBalancesACLOperations(
		t,
		pool,
		hostileRole,
		dependentRole,
	)
	for _, role := range []string{hostileRole, dependentRole} {
		for _, relation := range brokerBalancesACLRelations {
			for _, privilege := range []string{
				"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE",
			} {
				var allowed bool
				if err := pool.QueryRow(ctx, `
					SELECT has_table_privilege($1, $2, $3)`,
					role,
					relation,
					privilege,
				).Scan(&allowed); err != nil {
					t.Fatalf("inspect scrubbed %s %s on %s: %v", role, privilege, relation, err)
				}
				if allowed {
					t.Fatalf("%s retained %s on %s", role, privilege, relation)
				}
			}
		}
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("idempotent broker-balances ACL rerun: %v", err)
	}
	assertBrokerBalancesACLStateUnchanged(t, pool, beforeState)
	assertBrokerBalancesACLHistoryAdvanced(t, pool, beforeHistory)
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify current broker-balances ACL schema: %v", err)
	}
	previous := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerBalancesACLPreviousMigration),
	)
	if err := previous.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous binary verification = %v, want schema-ahead", err)
	}
}

func TestBrokerBalancesACLMigrationLaterLockRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerBalancesACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerBalancesACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedBrokerBalancesACLState(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT USAGE ON SCHEMA ledger TO platformgo_projector;
		GRANT SELECT ON ledger.balances TO platformgo_projector;
		GRANT UPDATE (equity) ON ledger.balances TO platformgo_projector`,
	); err != nil {
		t.Fatalf("grant pre-revocation balance writer: %v", err)
	}
	beforeState := readBrokerBalancesACLState(t, pool)
	beforeACL := readAdminRiskRawACLState(t, pool, brokerBalancesACLRelations)
	beforeDefaults := readAdminRiskOwnerDefaults(t, pool)
	beforeHistory := readBrokerBalancesACLHistory(t, pool)

	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin pre-revocation balance writer: %v", err)
	}
	if _, err := writer.Exec(ctx, `
		SET LOCAL ROLE platformgo_projector;
		UPDATE ledger.balances
		   SET equity = equity
		 WHERE account_id =
			'urn:xb:account:00000000-0000-4000-8000-000000000001'
		   AND currency = 'USDC'`); err != nil {
		_ = writer.Rollback(ctx)
		t.Fatalf("execute pre-revocation balance update: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerBalancesACLMigration),
	)
	result := make(chan error, 1)
	go func() {
		result <- current.Migrate(context.Background())
	}()
	waitAdminRiskRelationLock(
		t,
		pool,
		result,
		"ledger.balances",
		"ShareLock",
	)
	for _, relation := range brokerBalancesACLRelations[:2] {
		var granted bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_locks
				 WHERE relation = $1::pg_catalog.regclass
				   AND mode = 'ShareLock'
				   AND granted
			)`,
			relation,
		).Scan(&granted); err != nil {
			_ = writer.Rollback(ctx)
			t.Fatalf("inspect granted migration lock on %s: %v", relation, err)
		}
		if !granted {
			_ = writer.Rollback(ctx)
			t.Fatalf("migration did not lock %s before waiting on balances", relation)
		}
	}
	var migrationErr error
	select {
	case migrationErr = <-result:
	case <-time.After(7 * time.Second):
		_ = writer.Rollback(ctx)
		t.Fatal("timed out waiting for bounded broker-balances ACL failure")
	}
	assertAdminRiskSQLState(t, migrationErr, "55P03")
	if after := readBrokerBalancesACLHistory(t, pool); !slices.Equal(after, beforeHistory) {
		_ = writer.Rollback(ctx)
		t.Fatalf("failed migration changed history: before=%v after=%v", beforeHistory, after)
	}
	if after := readAdminRiskRawACLState(
		t,
		pool,
		brokerBalancesACLRelations,
	); after != beforeACL {
		_ = writer.Rollback(ctx)
		t.Fatal("failed migration changed broker-balances ACLs")
	}
	assertBrokerBalancesACLStateUnchanged(t, pool, beforeState)
	if after := readAdminRiskOwnerDefaults(t, pool); after != beforeDefaults {
		_ = writer.Rollback(ctx)
		t.Fatal("failed migration changed owner defaults")
	}
	if err := writer.Rollback(ctx); err != nil {
		t.Fatalf("rollback pre-revocation balance writer: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry broker-balances ACL migration after writer drain: %v", err)
	}
	assertBrokerBalancesACLStateUnchanged(t, pool, beforeState)
	assertBrokerBalancesACLHistoryAdvanced(t, pool, beforeHistory)
	assertBrokerBalancesACLAllowlist(t, pool)
	assertBrokerBalancesACLRuntimePrivileges(t, pool)
}

func TestBrokerBalancesACLMigrationWaitsForProductionOrderWriter(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerBalancesACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerBalancesACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:00000000-0000-4000-8000-000000000002', 'NETTING');
		INSERT INTO identity.users (
			user_id, broker_subject, login, normalized_login
		) VALUES (
			'urn:xb:user:broker-balances-writer',
			'urn:xb:tenant:broker-balances-writer',
			'broker-balances-writer',
			'broker-balances-writer'
		)`,
	); err != nil {
		t.Fatalf("seed production-order writer authority: %v", err)
	}
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin production-order writer: %v", err)
	}
	if _, err := writer.Exec(ctx, `
		SET LOCAL ROLE platformgo_engine;
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES (
			'urn:xb:user:broker-balances-writer',
			'urn:xb:account:00000000-0000-4000-8000-000000000002',
			'urn:xb:tenant:broker-balances-writer'
		);
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES (
			'urn:xb:account:00000000-0000-4000-8000-000000000002',
			9912,
			'USDC',
			'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'],
			'2026-07-30T00:00:00Z',
			'urn:xb:tenant:broker-balances-writer'
		);
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES (
			'urn:xb:account:00000000-0000-4000-8000-000000000002',
			'USDC', 250, 0, 250, 250, 18
		)`,
	); err != nil {
		_ = writer.Rollback(ctx)
		t.Fatalf("execute production-order writer: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerBalancesACLMigration),
	)
	result := make(chan error, 1)
	go func() {
		result <- current.Migrate(context.Background())
	}()
	waitAdminRiskRelationLock(
		t,
		pool,
		result,
		"identity.user_accounts",
		"ShareLock",
	)
	if err := writer.Commit(ctx); err != nil {
		t.Fatalf("commit production-order writer: %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("migration after production-order writer commit: %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("migration did not complete after production-order writer commit")
	}
	var ownership, profile, balance int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.user_accounts
			  WHERE account_id =
				'urn:xb:account:00000000-0000-4000-8000-000000000002'),
			(SELECT count(*) FROM identity.account_profiles
			  WHERE account_id =
				'urn:xb:account:00000000-0000-4000-8000-000000000002'),
			(SELECT count(*) FROM ledger.balances
			  WHERE account_id =
				'urn:xb:account:00000000-0000-4000-8000-000000000002')`,
	).Scan(&ownership, &profile, &balance); err != nil {
		t.Fatalf("read committed production-order writer state: %v", err)
	}
	if ownership != 1 || profile != 1 || balance != 1 {
		t.Fatalf(
			"production-order writer state = %d/%d/%d, want 1/1/1",
			ownership,
			profile,
			balance,
		)
	}
	assertBrokerBalancesACLAllowlist(t, pool)
	assertBrokerBalancesACLRuntimePrivileges(t, pool)
}

type brokerBalancesACLRelationState struct {
	Digest   [sha256.Size]byte
	FileNode uint32
	Owner    string
}

func seedBrokerBalancesACLState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:00000000-0000-4000-8000-000000000001', 'NETTING');
		INSERT INTO identity.users (
			user_id, broker_subject, login, normalized_login
		) VALUES (
			'urn:xb:user:broker-balances-acl',
			'urn:xb:tenant:broker-balances-acl',
			'broker-balances-acl',
			'broker-balances-acl'
		);
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES (
			'urn:xb:user:broker-balances-acl',
			'urn:xb:account:00000000-0000-4000-8000-000000000001',
			'urn:xb:tenant:broker-balances-acl'
		);
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES (
			'urn:xb:account:00000000-0000-4000-8000-000000000001',
			9911,
			'USDC',
			'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'],
			'2026-07-30T00:00:00Z',
			'urn:xb:tenant:broker-balances-acl'
		);
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence,
			updated_at
		) VALUES (
			'urn:xb:account:00000000-0000-4000-8000-000000000001',
			'USDC', 1000, 25, 975, 1000, 17, '2026-07-30T00:00:00Z'
		)`,
	); err != nil {
		t.Fatalf("seed broker-balances ACL state: %v", err)
	}
}

func readBrokerBalancesACLState(
	t *testing.T,
	pool *pgxpool.Pool,
) map[string]brokerBalancesACLRelationState {
	t.Helper()
	queries := map[string]string{
		"identity.user_accounts": `
			SELECT COALESCE(jsonb_agg(jsonb_build_array(
				user_id, account_id, broker_subject, created_at
			) ORDER BY user_id, account_id), '[]'::jsonb)::text`,
		"identity.account_profiles": `
			SELECT COALESCE(jsonb_agg(jsonb_build_array(
				account_id, login, base_currency, market_venue,
				permitted_classes, created_at, broker_subject
			) ORDER BY account_id), '[]'::jsonb)::text`,
		"ledger.balances": `
			SELECT COALESCE(jsonb_agg(jsonb_build_array(
				account_id, currency, total, used, free, equity,
				ledger_sequence, updated_at
			) ORDER BY account_id, currency), '[]'::jsonb)::text`,
	}
	result := make(map[string]brokerBalancesACLRelationState, len(queries))
	for _, relation := range brokerBalancesACLRelations {
		relationID := pgx.Identifier(strings.Split(relation, ".")).Sanitize()
		var canonical string
		if err := pool.QueryRow(
			context.Background(),
			queries[relation]+" FROM "+relationID,
		).Scan(&canonical); err != nil {
			t.Fatalf("read %s rows: %v", relation, err)
		}
		var state brokerBalancesACLRelationState
		if err := pool.QueryRow(context.Background(), `
			SELECT
				pg_catalog.pg_relation_filenode($1::pg_catalog.regclass),
				pg_catalog.pg_get_userbyid(relowner)
			  FROM pg_catalog.pg_class
			 WHERE oid = $1::pg_catalog.regclass`,
			relation,
		).Scan(&state.FileNode, &state.Owner); err != nil {
			t.Fatalf("read %s relation identity: %v", relation, err)
		}
		state.Digest = sha256.Sum256([]byte(canonical))
		result[relation] = state
	}
	return result
}

func assertBrokerBalancesACLStateUnchanged(
	t *testing.T,
	pool *pgxpool.Pool,
	before map[string]brokerBalancesACLRelationState,
) {
	t.Helper()
	after := readBrokerBalancesACLState(t, pool)
	for _, relation := range brokerBalancesACLRelations {
		if after[relation] != before[relation] {
			t.Fatalf("%s changed: before=%v after=%v", relation, before[relation], after[relation])
		}
	}
}

func readBrokerBalancesACLHistory(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT filename, encode(checksum, 'hex')
		  FROM engine.schema_migrations
		 ORDER BY filename`)
	if err != nil {
		t.Fatalf("read broker-balances migration history: %v", err)
	}
	defer rows.Close()
	var history []string
	for rows.Next() {
		var filename, checksum string
		if err := rows.Scan(&filename, &checksum); err != nil {
			t.Fatalf("scan broker-balances migration history: %v", err)
		}
		history = append(history, filename+"|"+checksum)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate broker-balances migration history: %v", err)
	}
	return history
}

func assertBrokerBalancesACLHistoryAdvanced(
	t *testing.T,
	pool *pgxpool.Pool,
	before []string,
) {
	t.Helper()
	after := readBrokerBalancesACLHistory(t, pool)
	if len(after) != len(before)+1 || !slices.Equal(after[:len(before)], before) {
		t.Fatalf("migration history changed unexpectedly: before=%v after=%v", before, after)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", brokerBalancesACLMigration))
	if err != nil {
		t.Fatalf("read broker-balances ACL migration: %v", err)
	}
	want := fmt.Sprintf("%s|%x", brokerBalancesACLMigration, sha256.Sum256(raw))
	if after[len(after)-1] != want {
		t.Fatalf("migration tip = %q, want %q", after[len(after)-1], want)
	}
}

func assertBrokerBalancesACLFixtureVulnerable(
	t *testing.T,
	pool *pgxpool.Pool,
	roles ...string,
) {
	t.Helper()
	for _, role := range roles {
		for _, relation := range brokerBalancesACLRelations {
			var tableAllowed bool
			if err := pool.QueryRow(context.Background(), `
				SELECT has_table_privilege($1, $2, 'SELECT')`,
				role,
				relation,
			).Scan(&tableAllowed); err != nil {
				t.Fatalf("inspect vulnerable %s on %s: %v", role, relation, err)
			}
			if !tableAllowed {
				t.Fatalf("fixture did not expose %s SELECT on %s", role, relation)
			}
		}
	}
}

func assertBrokerBalancesACLAllowlist(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	wants := map[string][]string{
		"identity.user_accounts": {
			"platformgo_api|SELECT|false|table",
			"platformgo_engine|INSERT|false|table",
			"platformgo_engine|SELECT|false|table",
		},
		"identity.account_profiles": {
			"platformgo_api|SELECT|false|table",
			"platformgo_engine|INSERT|false|table",
		},
		"ledger.balances": {
			"platformgo_api|SELECT|false|table",
			"platformgo_engine|INSERT|false|table",
			"platformgo_engine|SELECT|false|table",
			"platformgo_engine|UPDATE|false|table",
		},
	}
	for _, relation := range brokerBalancesACLRelations {
		got := readAdminRiskNonOwnerACL(t, pool, relation)
		want := wants[relation]
		sort.Strings(want)
		if !slices.Equal(got, want) {
			t.Fatalf("%s ACL = %v, want exact %v", relation, got, want)
		}
	}
}

func assertBrokerBalancesACLRuntimePrivileges(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	runtimeRoles := []string{
		"platformgo_api",
		"platformgo_engine",
		"platformgo_outbox",
		"platformgo_projector",
		"platformgo_realtime",
		"platformgo_realtime_repair",
	}
	tablePrivileges := []string{
		"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE",
		"REFERENCES", "TRIGGER", "MAINTAIN",
	}
	for _, role := range runtimeRoles {
		for _, relation := range brokerBalancesACLRelations {
			for _, privilege := range tablePrivileges {
				want := role == "platformgo_api" && privilege == "SELECT"
				if role == "platformgo_engine" {
					switch relation {
					case "identity.user_accounts":
						want = privilege == "SELECT" || privilege == "INSERT"
					case "identity.account_profiles":
						want = privilege == "INSERT"
					case "ledger.balances":
						want = privilege == "SELECT" ||
							privilege == "INSERT" ||
							privilege == "UPDATE"
					}
				}
				var got bool
				if err := pool.QueryRow(context.Background(), `
					SELECT has_table_privilege($1, $2, $3)`,
					role,
					relation,
					privilege,
				).Scan(&got); err != nil {
					t.Fatalf("inspect %s %s on %s: %v", role, privilege, relation, err)
				}
				if got != want {
					t.Fatalf("%s %s on %s = %t, want %t", role, privilege, relation, got, want)
				}
			}
		}
	}
}

func assertBrokerBalancesACLOperations(
	t *testing.T,
	pool *pgxpool.Pool,
	deniedRoles ...string,
) {
	t.Helper()
	ctx := context.Background()
	api, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin API broker-balances ACL read: %v", err)
	}
	if _, err := api.Exec(ctx, "SET LOCAL ROLE platformgo_api"); err != nil {
		_ = api.Rollback(ctx)
		t.Fatalf("assume API role: %v", err)
	}
	var ownership, profile, balance int
	if err := api.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.user_accounts),
			(SELECT count(*) FROM identity.account_profiles),
			(SELECT count(*) FROM ledger.balances)`,
	).Scan(&ownership, &profile, &balance); err != nil {
		_ = api.Rollback(ctx)
		t.Fatalf("execute least-privilege API reads: %v", err)
	}
	if ownership == 0 || profile == 0 || balance == 0 {
		_ = api.Rollback(ctx)
		t.Fatalf("API read counts = %d/%d/%d, want populated", ownership, profile, balance)
	}
	if err := api.Rollback(ctx); err != nil {
		t.Fatalf("rollback API read transaction: %v", err)
	}

	for _, statement := range []string{
		`INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES (
			'urn:xb:user:denied',
			'urn:xb:account:00000000-0000-4000-8000-000000000099',
			'urn:xb:tenant:denied'
		)`,
		`INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES (
			'urn:xb:account:00000000-0000-4000-8000-000000000099',
			9999, 'USDC', 'HYPERLIQUID', ARRAY['CRYPTOCURRENCY'],
			'2026-07-30T00:00:00Z', 'urn:xb:tenant:denied'
		)`,
		`UPDATE ledger.balances SET equity = equity`,
		`DELETE FROM identity.user_accounts`,
		`TRUNCATE ledger.balances`,
	} {
		assertBrokerBalancesACLStatementDenied(
			t,
			pool,
			"platformgo_api",
			statement,
		)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:00000000-0000-4000-8000-000000000003', 'NETTING');
		INSERT INTO identity.users (
			user_id, broker_subject, login, normalized_login
		) VALUES (
			'urn:xb:user:broker-balances-ops',
			'urn:xb:tenant:broker-balances-ops',
			'broker-balances-ops',
			'broker-balances-ops'
		)`,
	); err != nil {
		t.Fatalf("seed post-cutover engine operation authority: %v", err)
	}
	engine, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin post-cutover engine operations: %v", err)
	}
	if _, err := engine.Exec(ctx, "SET LOCAL ROLE platformgo_engine"); err != nil {
		_ = engine.Rollback(ctx)
		t.Fatalf("assume engine role: %v", err)
	}
	if _, err := engine.Exec(ctx, `
		SELECT account_id FROM identity.user_accounts;
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES (
			'urn:xb:user:broker-balances-ops',
			'urn:xb:account:00000000-0000-4000-8000-000000000003',
			'urn:xb:tenant:broker-balances-ops'
		);
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES (
			'urn:xb:account:00000000-0000-4000-8000-000000000003',
			9913, 'USDC', 'HYPERLIQUID', ARRAY['CRYPTOCURRENCY'],
			'2026-07-30T00:00:00Z',
			'urn:xb:tenant:broker-balances-ops'
		);
		SELECT account_id FROM ledger.balances;
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES (
			'urn:xb:account:00000000-0000-4000-8000-000000000003',
			'USDC', 25, 0, 25, 25, 19
		);
		UPDATE ledger.balances
		   SET equity = equity
		 WHERE account_id =
			'urn:xb:account:00000000-0000-4000-8000-000000000003'`,
	); err != nil {
		_ = engine.Rollback(ctx)
		t.Fatalf("execute allowed post-cutover engine operations: %v", err)
	}
	if err := engine.Rollback(ctx); err != nil {
		t.Fatalf("rollback post-cutover engine operations: %v", err)
	}
	assertBrokerBalancesACLStatementDenied(
		t,
		pool,
		"platformgo_engine",
		"SELECT account_id FROM identity.account_profiles",
	)

	for _, role := range deniedRoles {
		assertBrokerBalancesACLStatementDenied(
			t,
			pool,
			role,
			"SELECT account_id FROM identity.user_accounts",
		)
		assertBrokerBalancesACLStatementDenied(
			t,
			pool,
			role,
			"UPDATE ledger.balances SET equity = equity",
		)
	}
}

func assertBrokerBalancesACLStatementDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
	statement string,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin denied %s operation: %v", role, err)
	}
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
	); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("assume denied role %s: %v", role, err)
	}
	_, statementErr := tx.Exec(context.Background(), statement)
	_ = tx.Rollback(context.Background())
	assertAdminRiskSQLState(t, statementErr, "42501")
}
