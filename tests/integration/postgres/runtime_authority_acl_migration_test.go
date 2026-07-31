package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	runtimeAuthorityACLFoundationMigration = "20260724000100_durable_execution_foundation.up.sql"
	runtimeAuthorityACLPreviousMigration   = "20260731000100_phase3_admin_bootstrap_authority.up.sql"
	runtimeAuthorityACLMigration           = "20260731000200_phase3_runtime_authority_acl.up.sql"
)

var runtimeAuthorityACLRelations = []string{
	"engine.deployment_shard",
	"engine.shard_ownership_epochs",
	"engine.shard_checkpoints",
	"engine.shard_faults",
	"engine.duplicate_delivery_receipts",
	"trading.risk_configs",
	"market.books",
	"ledger.transactions",
	"ledger.entries",
}

func runtimeAuthorityACLMigrationChecksum(t *testing.T) []byte {
	t.Helper()
	file, ok := migrationFilesThrough(t, runtimeAuthorityACLMigration)[runtimeAuthorityACLMigration]
	if !ok {
		t.Fatalf("migration %s is missing", runtimeAuthorityACLMigration)
	}
	checksum := sha256.Sum256(file.Data)
	return checksum[:]
}

func TestRuntimeAuthorityACLMigrationRequiresExactPrivilegedOwner(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	exactOwnerName := fmt.Sprintf("runtime_authority_exact_owner_%d", os.Getpid())
	exactOwnerID := pgx.Identifier{exactOwnerName}.Sanitize()
	exactOwnerPool := adminBootstrapSuperuserLoginPool(
		t,
		pool,
		exactOwnerName,
	)
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"ALTER ROLE "+exactOwnerID+" SUPERUSER",
		)
	})
	if err := newCurrentTestMigrator(
		t,
		exactOwnerPool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply tip 42 as exact authority owner: %v", err)
	}

	before := runtimeAuthorityCutoverDigest(t, pool)
	if _, err := pool.Exec(ctx, "ALTER ROLE "+exactOwnerID+" NOSUPERUSER"); err != nil {
		t.Fatalf("remove exact owner's temporary superuser authority: %v", err)
	}

	err := newCurrentTestMigrator(
		t,
		exactOwnerPool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	if !adminBootstrapIsPostgresCode(err, "42501") {
		t.Fatalf("migration 43 as unprivileged exact owner error = %v, want 42501", err)
	}
	if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
		t.Fatalf("rejected unprivileged-owner migration changed state: before %s after %s", before, after)
	}

	err = newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("migration 43 as distinct superuser error = %v, want 55000", err)
	}
	if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
		t.Fatalf("rejected distinct-owner migration changed state: before %s after %s", before, after)
	}
	assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)
	if _, err := pool.Exec(ctx, "ALTER ROLE "+exactOwnerID+" SUPERUSER"); err != nil {
		t.Fatalf("restore exact owner's temporary superuser authority: %v", err)
	}

	exactOwner := newCurrentTestMigrator(
		t,
		exactOwnerPool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	)
	if err := exactOwner.Migrate(ctx); err != nil {
		t.Fatalf("apply migration 43 as exact privileged owner: %v", err)
	}
	if err := exactOwner.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify exact-owner migration 43: %v", err)
	}
	assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)

	if _, err := pool.Exec(ctx, "ALTER ROLE "+exactOwnerID+" NOSUPERUSER"); err != nil {
		t.Fatalf("remove function owner's post-migration superuser authority: %v", err)
	}
	terminalLogin := fmt.Sprintf("runtime_authority_bootstrap_%d", os.Getpid())
	terminal := runtimeRoleLoginPool(
		t,
		pool,
		terminalLogin,
		"platformgo_admin_bootstrap",
	)
	keyHash := sha256.Sum256([]byte("runtime-authority-bootstrap-lifecycle"))
	assertAdminBootstrapSQLState(
		t,
		terminal,
		"42501",
		"runtime-authority-bootstrap-lifecycle",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000093",
		"00000000-0000-4000-8000-000000000093",
		"2026-07-31T00:00:00.000000Z",
	)
	assertAdminBootstrapAuthorityCounts(t, pool, 0, 0)
	if _, err := pool.Exec(ctx, "ALTER ROLE "+exactOwnerID+" SUPERUSER"); err != nil {
		t.Fatalf("restore function owner's bootstrap superuser authority: %v", err)
	}
	result := callAdminBootstrap(
		t,
		terminal,
		"runtime-authority-bootstrap-lifecycle",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000093",
		"00000000-0000-4000-8000-000000000093",
		"2026-07-31T00:00:00.000000Z",
	)
	if result.outcome != "created" {
		t.Fatalf("terminal bootstrap lifecycle outcome = %q, want created", result.outcome)
	}
	freshVerifier := postgresPool(t)
	if _, err := pool.Exec(ctx, `
		UPDATE engine.schema_migrations
		   SET checksum = pg_catalog.decode(pg_catalog.repeat('00', 32), 'hex')
		 WHERE filename = $1`, runtimeAuthorityACLMigration); err != nil {
		t.Fatalf("tamper migration-43 checksum before terminal cleanup: %v", err)
	}
	if err := verifyAdminBootstrapMigrationBeforeTerminalCleanup(
		ctx,
		freshVerifier,
		runtimeAuthorityACLMigrationChecksum(t),
	); err == nil {
		t.Fatal("fresh-session verification accepted divergent migration-43 checksum")
	}
	var ownerStillSuperuser, terminalCanLogin, terminalRemainsMember bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = $1),
			(SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname = $2),
			pg_catalog.pg_has_role($2, 'platformgo_admin_bootstrap', 'member')`,
		exactOwnerName,
		terminalLogin,
	).Scan(&ownerStillSuperuser, &terminalCanLogin, &terminalRemainsMember); err != nil {
		t.Fatalf("inspect authority after failed fresh verification: %v", err)
	}
	if !ownerStillSuperuser || !terminalCanLogin || !terminalRemainsMember {
		t.Fatalf(
			"authority cleaned before exact-tip verification = owner superuser %t "+
				"terminal login %t member %t",
			ownerStillSuperuser,
			terminalCanLogin,
			terminalRemainsMember,
		)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE engine.schema_migrations
		   SET checksum = $2
		 WHERE filename = $1`,
		runtimeAuthorityACLMigration,
		runtimeAuthorityACLMigrationChecksum(t),
	); err != nil {
		t.Fatalf("restore disposable migration-43 checksum fixture: %v", err)
	}
	if err := verifyAdminBootstrapMigrationBeforeTerminalCleanup(
		ctx,
		freshVerifier,
		runtimeAuthorityACLMigrationChecksum(t),
	); err != nil {
		t.Fatalf("verify repaired migration-43 checksum before cleanup: %v", err)
	}
	assertAdminBootstrapAuthorityCounts(t, freshVerifier, 1, 1)
	if _, err := pool.Exec(ctx, "ALTER ROLE "+exactOwnerID+" NOSUPERUSER"); err != nil {
		t.Fatalf("remove function owner's terminal superuser authority: %v", err)
	}
	var stillSuperuser bool
	if err := pool.QueryRow(ctx, `
		SELECT rolsuper FROM pg_roles WHERE rolname = $1`, exactOwnerName).Scan(&stillSuperuser); err != nil {
		t.Fatalf("inspect final function-owner authority: %v", err)
	}
	if stillSuperuser {
		t.Fatal("function owner retained superuser authority after durable verification")
	}
}

func runtimeAuthorityCutoverDigest(
	t *testing.T,
	pool interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
) string {
	t.Helper()
	var digest string
	if err := pool.QueryRow(context.Background(), `
		SELECT pg_catalog.encode(
			pg_catalog.sha256(pg_catalog.convert_to(
				pg_catalog.concat_ws('|',
					(
						SELECT pg_catalog.string_agg(
							filename || ':' || pg_catalog.encode(checksum, 'hex'),
							',' ORDER BY filename
						)
						  FROM engine.schema_migrations
					),
					(
						SELECT pg_catalog.concat_ws(':',
							owner.rolname,
							COALESCE(procedure.proacl::text, ''),
							COALESCE(procedure.proconfig::text, ''),
							pg_catalog.pg_get_functiondef(procedure.oid)
						)
						  FROM pg_catalog.pg_proc AS procedure
						  JOIN pg_catalog.pg_roles AS owner
						    ON owner.oid = procedure.proowner
						 WHERE procedure.oid =
							'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
								::pg_catalog.regprocedure
					),
					(
						SELECT pg_catalog.string_agg(
							pg_catalog.concat_ws(':',
								namespace.nspname,
								owner.rolname,
								COALESCE(namespace.nspacl::text, '')
							),
							',' ORDER BY namespace.nspname
						)
						  FROM pg_catalog.pg_namespace AS namespace
						  JOIN pg_catalog.pg_roles AS owner
						    ON owner.oid = namespace.nspowner
						 WHERE namespace.nspname IN (
							'engine', 'identity', 'ledger', 'market', 'trading'
						 )
					),
					(
						SELECT pg_catalog.string_agg(
							pg_catalog.concat_ws(':',
								namespace.nspname,
								relation.relname,
								COALESCE(relation.relacl::text, ''),
								relation.relfilenode::text
							),
							',' ORDER BY namespace.nspname, relation.relname
						)
						  FROM pg_catalog.pg_class AS relation
						  JOIN pg_catalog.pg_namespace AS namespace
						    ON namespace.oid = relation.relnamespace
						 WHERE (namespace.nspname || '.' || relation.relname) = ANY($1)
					),
					(
						SELECT pg_catalog.string_agg(
							pg_catalog.concat_ws(':',
								attribute.attrelid::pg_catalog.regclass::text,
								attribute.attname,
								COALESCE(attribute.attacl::text, '')
							),
							',' ORDER BY attribute.attrelid, attribute.attnum
						)
						  FROM pg_catalog.pg_attribute AS attribute
						 WHERE attribute.attnum > 0
						   AND NOT attribute.attisdropped
						   AND attribute.attrelid::pg_catalog.regclass::text = ANY($1)
					),
					(SELECT COALESCE(string_agg(to_jsonb(row)::text, ',' ORDER BY to_jsonb(row)::text), '') FROM engine.deployment_shard AS row),
					(SELECT COALESCE(string_agg(to_jsonb(row)::text, ',' ORDER BY to_jsonb(row)::text), '') FROM engine.shard_ownership_epochs AS row),
					(SELECT COALESCE(string_agg(to_jsonb(row)::text, ',' ORDER BY to_jsonb(row)::text), '') FROM engine.shard_checkpoints AS row),
					(SELECT COALESCE(string_agg(to_jsonb(row)::text, ',' ORDER BY to_jsonb(row)::text), '') FROM engine.shard_faults AS row),
					(SELECT COALESCE(string_agg(to_jsonb(row)::text, ',' ORDER BY to_jsonb(row)::text), '') FROM engine.duplicate_delivery_receipts AS row),
					(SELECT COALESCE(string_agg(to_jsonb(row)::text, ',' ORDER BY to_jsonb(row)::text), '') FROM trading.risk_configs AS row),
					(SELECT COALESCE(string_agg(to_jsonb(row)::text, ',' ORDER BY to_jsonb(row)::text), '') FROM market.books AS row),
					(SELECT COALESCE(string_agg(to_jsonb(row)::text, ',' ORDER BY to_jsonb(row)::text), '') FROM ledger.transactions AS row),
					(SELECT COALESCE(string_agg(to_jsonb(row)::text, ',' ORDER BY to_jsonb(row)::text), '') FROM ledger.entries AS row)
				),
				'UTF8'
			)),
			'hex'
		)`, runtimeAuthorityACLRelations).Scan(&digest); err != nil {
		t.Fatalf("capture runtime authority cutover state: %v", err)
	}
	return digest
}

func TestRuntimeAuthorityACLMigrationRejectsExcessEngineLedgerAuthority(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply tip 42: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin balanced ledger fixture: %v", err)
	}
	transactionID := "00000000-0000-4000-8000-000000000094"
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger.transactions (
			transaction_id, business_key, input_id, logical_time
		) VALUES ($1, 'runtime-authority-ledger', $2, 1785456000000000000)`,
		transactionID,
		"00000000-0000-4000-8000-000000000095",
	); err == nil {
		_, err = tx.Exec(ctx, `
			INSERT INTO ledger.entries (
				entry_id, transaction_id, account_id, currency, amount
			) VALUES
				($1, $2, 'account-runtime-authority', 'USDC', 1),
				($3, $2, 'system:clearing', 'USDC', -1)`,
			"00000000-0000-4000-8000-000000000096",
			transactionID,
			"00000000-0000-4000-8000-000000000097",
		)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert balanced ledger fixture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit balanced ledger fixture: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		GRANT TRUNCATE ON TABLE ledger.entries TO platformgo_engine`); err != nil {
		t.Fatalf("grant excess engine ledger authority: %v", err)
	}
	engine := runtimeRoleLoginPool(
		t,
		pool,
		fmt.Sprintf("runtime_authority_ledger_engine_%d", os.Getpid()),
		"platformgo_engine",
	)
	var inheritedTruncate bool
	if err := engine.QueryRow(ctx, `
		SELECT has_table_privilege(current_user, 'ledger.entries', 'TRUNCATE')`,
	).Scan(&inheritedTruncate); err != nil {
		t.Fatalf("inspect inherited excess engine ledger authority: %v", err)
	}
	if !inheritedTruncate {
		t.Fatal("engine login did not inherit excess TRUNCATE authority")
	}

	before := runtimeAuthorityCutoverDigest(t, pool)
	err = newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("excess engine ledger authority migration error = %v, want 55000", err)
	}
	if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
		t.Fatalf("rejected ledger-authority migration changed evidence: before %s after %s", before, after)
	}
	assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)

	var transactions, entries int
	var engineCanTruncate bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries),
			has_table_privilege(
				'platformgo_engine', 'ledger.entries', 'TRUNCATE'
			)`).Scan(&transactions, &entries, &engineCanTruncate); err != nil {
		t.Fatalf("inspect preserved ledger authority evidence: %v", err)
	}
	if transactions != 1 || entries != 2 || !engineCanTruncate {
		t.Fatalf(
			"preserved ledger evidence = transactions %d entries %d truncate %t",
			transactions,
			entries,
			engineCanTruncate,
		)
	}
}

func TestRuntimeAuthorityACLMigrationRejectsExcessEngineMutationMatrix(t *testing.T) {
	tests := []struct {
		name  string
		grant string
	}{
		{"deployment shard insert", "GRANT INSERT ON engine.deployment_shard TO platformgo_engine"},
		{"ownership epochs table update", "GRANT UPDATE ON engine.shard_ownership_epochs TO platformgo_engine"},
		{"checkpoints delete", "GRANT DELETE ON engine.shard_checkpoints TO platformgo_engine"},
		{"faults truncate", "GRANT TRUNCATE ON engine.shard_faults TO platformgo_engine"},
		{"duplicate receipts references", "GRANT REFERENCES ON engine.duplicate_delivery_receipts TO platformgo_engine"},
		{"risk configs trigger", "GRANT TRIGGER ON trading.risk_configs TO platformgo_engine"},
		{"books delete", "GRANT DELETE ON market.books TO platformgo_engine"},
		{"ledger transactions update", "GRANT UPDATE ON ledger.transactions TO platformgo_engine"},
		{"ledger entries truncate", "GRANT TRUNCATE ON ledger.entries TO platformgo_engine"},
		{"ledger entries maintain", "GRANT MAINTAIN ON ledger.entries TO platformgo_engine"},
		{"allowed table grant option", "GRANT INSERT ON engine.shard_checkpoints TO platformgo_engine WITH GRANT OPTION"},
		{"allowed column grant option", "GRANT UPDATE (epoch) ON engine.shard_ownership_epochs TO platformgo_engine WITH GRANT OPTION"},
		{"unexpected column update", "GRANT UPDATE (state_hash) ON engine.shard_checkpoints TO platformgo_engine"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatalf("apply tip 42: %v", err)
			}
			if _, err := pool.Exec(ctx, test.grant); err != nil {
				t.Fatalf("install excess engine authority: %v", err)
			}

			before := runtimeAuthorityCutoverDigest(t, pool)
			err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLMigration),
			).Migrate(ctx)
			if !adminBootstrapIsPostgresCode(err, "55000") {
				t.Fatalf("excess engine authority migration error = %v, want 55000", err)
			}
			if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
				t.Fatalf("rejected authority migration changed evidence: before %s after %s", before, after)
			}
			assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)
		})
	}
}

func TestRuntimeAuthorityACLMigrationScrubsBenignGrantsAndRestoresRoleMatrix(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}

	hostileRole := fmt.Sprintf("runtime_authority_reader_%d", os.Getpid())
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create unexpected reader: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
			DROP OWNED BY %[1]s CASCADE;
			DROP ROLE %[1]s`, hostileID))
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON TABLE
			engine.deployment_shard,
			engine.shard_ownership_epochs,
			engine.shard_checkpoints,
			engine.shard_faults,
			engine.duplicate_delivery_receipts,
			trading.risk_configs,
			market.books,
			ledger.transactions,
			ledger.entries
		TO %[1]s WITH GRANT OPTION;
		GRANT SELECT (state_hash) ON engine.shard_checkpoints
		TO %[1]s WITH GRANT OPTION`, hostileID)); err != nil {
		t.Fatalf("install benign unexpected grants: %v", err)
	}

	type relationState struct {
		filenode uint32
		rows     int64
	}
	before := make(map[string]relationState, len(runtimeAuthorityACLRelations))
	for _, relation := range runtimeAuthorityACLRelations {
		var state relationState
		if err := pool.QueryRow(ctx, `
			SELECT pg_relation_filenode($1::regclass),
			       (xpath('/row/count/text()', query_to_xml(
			           format('SELECT count(*) AS count FROM %s', $1),
			           false,
			           true,
			           ''
			       )))[1]::text::bigint`, relation).Scan(&state.filenode, &state.rows); err != nil {
			t.Fatalf("capture %s state: %v", relation, err)
		}
		before[relation] = state
	}

	files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
	if _, exists := files[runtimeAuthorityACLMigration]; !exists {
		t.Fatalf("expected forward migration %s", runtimeAuthorityACLMigration)
	}
	current := platformpostgres.NewMigrator(pool, files)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply runtime authority ACL migration: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify runtime authority ACL migration: %v", err)
	}

	for _, relation := range runtimeAuthorityACLRelations {
		var after relationState
		if err := pool.QueryRow(ctx, `
			SELECT pg_relation_filenode($1::regclass),
			       (xpath('/row/count/text()', query_to_xml(
			           format('SELECT count(*) AS count FROM %s', $1),
			           false,
			           true,
			           ''
			       )))[1]::text::bigint`, relation).Scan(&after.filenode, &after.rows); err != nil {
			t.Fatalf("capture migrated %s state: %v", relation, err)
		}
		if after != before[relation] {
			t.Fatalf("migration rewrote %s: before %+v after %+v", relation, before[relation], after)
		}
		for _, privilege := range []string{
			"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN",
		} {
			var allowed bool
			if err := pool.QueryRow(ctx, `SELECT has_table_privilege($1, $2, $3)`,
				hostileRole,
				relation,
				privilege,
			).Scan(&allowed); err != nil {
				t.Fatalf("inspect scrubbed %s on %s: %v", privilege, relation, err)
			}
			if allowed {
				t.Fatalf("unexpected reader retained %s on %s", privilege, relation)
			}
		}
	}

	assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_api", map[string]string{
		"engine.deployment_shard": "SELECT",
		"trading.risk_configs":    "SELECT",
		"market.books":            "SELECT",
	})
	assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_engine", map[string]string{
		"engine.deployment_shard":            "SELECT",
		"engine.shard_ownership_epochs":      "SELECT,INSERT",
		"engine.shard_checkpoints":           "SELECT,INSERT,UPDATE",
		"engine.shard_faults":                "SELECT,INSERT",
		"engine.duplicate_delivery_receipts": "SELECT,INSERT",
		"trading.risk_configs":               "SELECT,INSERT,UPDATE",
		"market.books":                       "SELECT,INSERT,UPDATE",
		"ledger.transactions":                "SELECT,INSERT",
		"ledger.entries":                     "SELECT,INSERT",
	})
	for _, column := range []string{"epoch", "acquired_at"} {
		var allowed bool
		if err := pool.QueryRow(ctx, `
			SELECT has_column_privilege(
				'platformgo_engine',
				'engine.shard_ownership_epochs',
				$1,
				'UPDATE'
			)`, column).Scan(&allowed); err != nil {
			t.Fatalf("inspect ownership epoch column %s: %v", column, err)
		}
		if !allowed {
			t.Fatalf("platformgo_engine lacks UPDATE on ownership epoch %s", column)
		}
	}
	assertRuntimeAuthorityColumnPrivileges(t, pool)
}

func assertRuntimeAuthorityTablePrivileges(
	t *testing.T,
	pool interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	role string,
	want map[string]string,
) {
	t.Helper()
	ctx := context.Background()
	for _, relation := range runtimeAuthorityACLRelations {
		for _, privilege := range []string{
			"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN",
		} {
			var allowed bool
			if err := pool.QueryRow(ctx, `SELECT has_table_privilege($1, $2, $3)`,
				role,
				relation,
				privilege,
			).Scan(&allowed); err != nil {
				t.Fatalf("inspect %s %s on %s: %v", role, privilege, relation, err)
			}
			expected := false
			for _, granted := range strings.Split(want[relation], ",") {
				if granted == privilege {
					expected = true
				}
			}
			if allowed != expected {
				t.Fatalf("%s %s on %s = %t, want %t", role, privilege, relation, allowed, expected)
			}
		}
	}
}

func assertRuntimeAuthorityColumnPrivileges(
	t *testing.T,
	pool interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
) {
	t.Helper()
	var total, exact int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(*),
			count(*) FILTER (
				WHERE relation.oid =
				          'engine.shard_ownership_epochs'::pg_catalog.regclass
				  AND attribute.attname IN ('epoch', 'acquired_at')
				  AND grantee.rolname = 'platformgo_engine'
				  AND privilege.privilege_type = 'UPDATE'
				  AND privilege.grantor = relation.relowner
				  AND NOT privilege.is_grantable
			)
		  FROM pg_catalog.pg_attribute AS attribute
		  JOIN pg_catalog.pg_class AS relation
		    ON relation.oid = attribute.attrelid
		 CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
		  JOIN pg_catalog.pg_roles AS grantee ON grantee.oid = privilege.grantee
		 WHERE attribute.attrelid = ANY (ARRAY[
			'engine.deployment_shard'::pg_catalog.regclass::pg_catalog.oid,
			'engine.shard_ownership_epochs'::pg_catalog.regclass::pg_catalog.oid,
			'engine.shard_checkpoints'::pg_catalog.regclass::pg_catalog.oid,
			'engine.shard_faults'::pg_catalog.regclass::pg_catalog.oid,
			'engine.duplicate_delivery_receipts'::pg_catalog.regclass::pg_catalog.oid,
			'trading.risk_configs'::pg_catalog.regclass::pg_catalog.oid,
			'market.books'::pg_catalog.regclass::pg_catalog.oid,
			'ledger.transactions'::pg_catalog.regclass::pg_catalog.oid,
			'ledger.entries'::pg_catalog.regclass::pg_catalog.oid
		 ])
		   AND attribute.attnum > 0
		   AND NOT attribute.attisdropped
		   AND grantee.rolname IN ('platformgo_api', 'platformgo_engine')`,
	).Scan(&total, &exact); err != nil {
		t.Fatalf("inspect runtime authority column ACLs: %v", err)
	}
	if total != 2 || exact != 2 {
		t.Fatalf("runtime authority API/engine column ACLs = total %d exact %d, want 2/2", total, exact)
	}
}

// An unexpected writer on an authority or append-only relation makes the
// pre-cutover history untrustworthy. The forward repair must preserve that
// evidence and fail closed instead of silently blessing it by revoking ACLs.
func TestRuntimeAuthorityACLMigrationRejectsHostileMutationAuthority(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	var serverVersion string
	if err := pool.QueryRow(ctx, "SHOW server_version").Scan(&serverVersion); err != nil {
		t.Fatalf("read PostgreSQL server version: %v", err)
	}
	if !strings.HasPrefix(serverVersion, "19beta2") {
		t.Fatalf("PostgreSQL server version = %q, want 19beta2", serverVersion)
	}
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("runtime_authority_hostile_%d", os.Getpid())
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
			ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
				REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
			DROP OWNED BY %[2]s CASCADE;
			DROP ROLE %[2]s`, ownerID, hostileID))
	})

	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s WITH GRANT OPTION`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile owner defaults: %v", err)
	}
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLFoundationMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply foundation schema under hostile defaults: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		DO $cleanup$
		DECLARE
			relation_name text;
		BEGIN
			FOR relation_name IN
				SELECT format('%%I.%%I', namespace.nspname, relation.relname)
				  FROM pg_catalog.pg_class AS relation
				  JOIN pg_catalog.pg_namespace AS namespace
				    ON namespace.oid = relation.relnamespace
				 WHERE relation.relkind IN ('r', 'p')
				   AND namespace.nspname IN ('engine', 'trading', 'market', 'ledger')
				   AND format('%%I.%%I', namespace.nspname, relation.relname) <> ALL (ARRAY[
					'engine.deployment_shard',
					'engine.shard_ownership_epochs',
					'engine.shard_checkpoints',
					'engine.shard_faults',
					'engine.duplicate_delivery_receipts',
					'trading.risk_configs',
					'market.books',
					'ledger.transactions',
					'ledger.entries'
				   ])
			LOOP
				EXECUTE format(
					'REVOKE ALL PRIVILEGES ON TABLE %%s FROM %[2]s CASCADE',
					relation_name
				);
			END LOOP;
		END
		$cleanup$`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("remove hostile owner defaults before bootstrap migration: %v", err)
	}
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply current-main tip: %v", err)
	}

	for _, relation := range runtimeAuthorityACLRelations {
		var canMutate bool
		if err := pool.QueryRow(ctx, `
			SELECT has_table_privilege($1, $2, 'INSERT')
			    OR has_table_privilege($1, $2, 'UPDATE')
			    OR has_table_privilege($1, $2, 'DELETE')
			    OR has_table_privilege($1, $2, 'TRUNCATE')`,
			hostileRole,
			relation,
		).Scan(&canMutate); err != nil {
			t.Fatalf("inspect hostile authority on %s: %v", relation, err)
		}
		if !canMutate {
			t.Fatalf("fixture did not retain hostile mutation authority on %s", relation)
		}
	}

	var historyBefore int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM engine.schema_migrations").Scan(&historyBefore); err != nil {
		t.Fatalf("count current migration history: %v", err)
	}
	files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
	if _, exists := files[runtimeAuthorityACLMigration]; !exists {
		t.Fatalf(
			"RED: expected forward migration %s is missing after current tip %s",
			runtimeAuthorityACLMigration,
			runtimeAuthorityACLPreviousMigration,
		)
	}
	err := newCurrentTestMigrator(t, pool, files).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("hostile authority migration error = %v, want SQLSTATE 55000", err)
	}
	var historyAfter int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM engine.schema_migrations").Scan(&historyAfter); err != nil {
		t.Fatalf("count rejected migration history: %v", err)
	}
	if historyAfter != historyBefore {
		t.Fatalf("rejected migration history count = %d, want %d", historyAfter, historyBefore)
	}
	for _, relation := range runtimeAuthorityACLRelations {
		var stillHasAuthority bool
		if err := pool.QueryRow(ctx, `
			SELECT has_table_privilege($1, $2, 'INSERT')
			    OR has_table_privilege($1, $2, 'UPDATE')
			    OR has_table_privilege($1, $2, 'DELETE')
			    OR has_table_privilege($1, $2, 'TRUNCATE')`,
			hostileRole,
			relation,
		).Scan(&stillHasAuthority); err != nil {
			t.Fatalf("inspect preserved hostile authority on %s: %v", relation, err)
		}
		if !stillHasAuthority {
			t.Fatalf("rejected migration scrubbed evidence on %s", relation)
		}
	}
}

func TestRuntimeAuthorityACLMigrationRejectsTargetSchemaAuthorityDrift(t *testing.T) {
	for _, namespace := range []string{"engine", "trading", "market", "ledger"} {
		for _, drift := range []string{"owner", "create", "create_grant_option"} {
			t.Run(namespace+"_"+drift, func(t *testing.T) {
				ctx := context.Background()
				pool := postgresPool(t)
				resetDurableSchemas(t, pool)
				if err := newCurrentTestMigrator(
					t,
					pool,
					migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
				).MigrateAndProvision(ctx, 7); err != nil {
					t.Fatalf("apply current-main schema: %v", err)
				}
				var owner string
				if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
					t.Fatalf("read migration owner: %v", err)
				}
				ownerID := pgx.Identifier{owner}.Sanitize()
				hostileRole := fmt.Sprintf(
					"runtime_schema_%s_%s_%d",
					namespace,
					drift,
					os.Getpid(),
				)
				hostileID := pgx.Identifier{hostileRole}.Sanitize()
				namespaceID := pgx.Identifier{namespace}.Sanitize()
				if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
					t.Fatalf("create hostile schema role: %v", err)
				}
				t.Cleanup(func() {
					_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
						ALTER SCHEMA %[1]s OWNER TO %[2]s;
						REVOKE CREATE ON SCHEMA %[1]s FROM %[3]s;
						DROP OWNED BY %[3]s CASCADE;
						DROP ROLE %[3]s`, namespaceID, ownerID, hostileID))
				})
				switch drift {
				case "owner":
					if _, err := pool.Exec(ctx, fmt.Sprintf(
						"ALTER SCHEMA %s OWNER TO %s",
						namespaceID,
						hostileID,
					)); err != nil {
						t.Fatalf("install hostile schema owner: %v", err)
					}
				case "create":
					if _, err := pool.Exec(ctx, fmt.Sprintf(
						"GRANT CREATE ON SCHEMA %s TO %s",
						namespaceID,
						hostileID,
					)); err != nil {
						t.Fatalf("install hostile schema CREATE: %v", err)
					}
				case "create_grant_option":
					if _, err := pool.Exec(ctx, fmt.Sprintf(
						"GRANT CREATE ON SCHEMA %s TO %s WITH GRANT OPTION",
						namespaceID,
						hostileID,
					)); err != nil {
						t.Fatalf("install hostile schema CREATE grant option: %v", err)
					}
				}

				before := runtimeAuthorityCutoverDigest(t, pool)
				err := newCurrentTestMigrator(
					t,
					pool,
					migrationFilesThrough(t, runtimeAuthorityACLMigration),
				).Migrate(ctx)
				var postgresError *pgconn.PgError
				if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
					t.Fatalf("schema authority migration error = %v, want SQLSTATE 55000", err)
				}
				if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
					t.Fatalf("rejected schema authority changed evidence: before %s after %s", before, after)
				}
				assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)
				var hostileOwner, hostileCreate, hostileGrantOption bool
				if err := pool.QueryRow(ctx, `
					SELECT
						pg_catalog.pg_get_userbyid(namespace.nspowner) = $2,
						pg_catalog.has_schema_privilege($2, $1, 'CREATE'),
						EXISTS (
							SELECT 1
							  FROM pg_catalog.aclexplode(namespace.nspacl) AS privilege
							 WHERE privilege.grantee = $2::pg_catalog.regrole
							   AND privilege.privilege_type = 'CREATE'
							   AND privilege.is_grantable
						)
					  FROM pg_catalog.pg_namespace AS namespace
					 WHERE namespace.nspname = $1`,
					namespace,
					hostileRole,
				).Scan(&hostileOwner, &hostileCreate, &hostileGrantOption); err != nil {
					t.Fatalf("inspect preserved schema authority: %v", err)
				}
				if (drift == "owner" && !hostileOwner) ||
					(drift == "create" && !hostileCreate) ||
					(drift == "create_grant_option" && !hostileGrantOption) {
					t.Fatal("rejected migration scrubbed schema authority evidence")
				}

				if _, err := pool.Exec(ctx, fmt.Sprintf(`
					ALTER SCHEMA %[1]s OWNER TO %[2]s;
					REVOKE CREATE ON SCHEMA %[1]s FROM %[3]s CASCADE`,
					namespaceID,
					ownerID,
					hostileID,
				)); err != nil {
					t.Fatalf("repair hostile schema authority: %v", err)
				}
				current := newCurrentTestMigrator(
					t,
					pool,
					migrationFilesThrough(t, runtimeAuthorityACLMigration),
				)
				if err := current.Migrate(ctx); err != nil {
					t.Fatalf("retry migration after schema authority repair: %v", err)
				}
				if err := current.VerifyCurrent(ctx); err != nil {
					t.Fatalf("verify schema authority repair retry: %v", err)
				}
				assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
			})
		}
	}
}

func TestRuntimeAuthorityACLMigrationRejectsHostileDefaultTableAuthority(t *testing.T) {
	for _, schema := range []string{
		"global",
		"audit",
		"engine",
		"identity",
		"ledger",
		"market",
		"messaging",
		"realtime",
		"trading",
	} {
		t.Run(schema, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatalf("apply current-main schema: %v", err)
			}

			probeSchema := schema
			scopeClause := " IN SCHEMA " + pgx.Identifier{schema}.Sanitize()
			namespaceName := schema
			if schema == "global" {
				probeSchema = "ledger"
				scopeClause = ""
				namespaceName = ""
			}
			grantDefault := "ALTER DEFAULT PRIVILEGES" + scopeClause +
				" GRANT TRUNCATE ON TABLES TO platformgo_engine"
			revokeDefault := "ALTER DEFAULT PRIVILEGES" + scopeClause +
				" REVOKE TRUNCATE ON TABLES FROM platformgo_engine"
			probeRelation := pgx.Identifier{
				probeSchema,
				"runtime_authority_default_acl_probe",
			}.Sanitize()
			if _, err := pool.Exec(ctx, grantDefault); err != nil {
				t.Fatalf("install hostile default table authority: %v", err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), revokeDefault)
				_, _ = pool.Exec(
					context.Background(),
					"DROP TABLE IF EXISTS "+probeRelation,
				)
			})

			before := runtimeAuthorityCutoverDigest(t, pool)
			err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLMigration),
			).Migrate(ctx)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
				t.Fatalf("hostile default authority migration error = %v, want SQLSTATE 55000", err)
			}
			if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
				t.Fatalf("rejected default authority changed evidence: before %s after %s", before, after)
			}
			assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)
			var defaultAuthorityPreserved bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					  FROM pg_catalog.pg_default_acl AS defaults
					 CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
					 WHERE defaults.defaclobjtype = 'r'
					   AND (
						   ($1 = '' AND defaults.defaclnamespace = 0)
						   OR defaults.defaclnamespace = pg_catalog.to_regnamespace($1)
					   )
					   AND privilege.grantee = 'platformgo_engine'::pg_catalog.regrole
					   AND privilege.privilege_type = 'TRUNCATE'
				)`, namespaceName).Scan(&defaultAuthorityPreserved); err != nil {
				t.Fatalf("inspect preserved default authority: %v", err)
			}
			if !defaultAuthorityPreserved {
				t.Fatal("rejected migration scrubbed default authority evidence")
			}

			if _, err := pool.Exec(ctx, revokeDefault); err != nil {
				t.Fatalf("repair hostile default table authority: %v", err)
			}
			current := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLMigration),
			)
			if err := current.Migrate(ctx); err != nil {
				t.Fatalf("retry migration after default authority repair: %v", err)
			}
			if err := current.VerifyCurrent(ctx); err != nil {
				t.Fatalf("verify default authority repair retry: %v", err)
			}
			assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
			if _, err := pool.Exec(ctx, "CREATE TABLE "+probeRelation+" (id bigint PRIMARY KEY)"); err != nil {
				t.Fatalf("create post-cutover default ACL probe: %v", err)
			}
			var engineCanTruncate bool
			if err := pool.QueryRow(ctx, `
				SELECT pg_catalog.has_table_privilege(
					'platformgo_engine', $1, 'TRUNCATE'
				)`, probeRelation).Scan(&engineCanTruncate); err != nil {
				t.Fatalf("inspect repaired future table authority: %v", err)
			}
			if engineCanTruncate {
				t.Fatal("repaired default ACL still grants engine TRUNCATE")
			}
		})
	}
}

func TestRuntimeAuthorityACLMigrationRejectsHostileDefaultMaintainAuthority(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER DEFAULT PRIVILEGES IN SCHEMA ledger
		GRANT MAINTAIN ON TABLES TO platformgo_engine`); err != nil {
		t.Fatalf("install hostile default MAINTAIN authority: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			ALTER DEFAULT PRIVILEGES IN SCHEMA ledger
			REVOKE MAINTAIN ON TABLES FROM platformgo_engine;
			DROP TABLE IF EXISTS ledger.runtime_authority_maintain_probe`)
	})

	before := runtimeAuthorityCutoverDigest(t, pool)
	err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("hostile default MAINTAIN migration error = %v, want SQLSTATE 55000", err)
	}
	if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
		t.Fatalf("rejected default MAINTAIN changed evidence: before %s after %s", before, after)
	}
	assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)
	var defaultAuthorityPreserved bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM pg_catalog.pg_default_acl AS defaults
			 CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
			 WHERE defaults.defaclobjtype = 'r'
			   AND defaults.defaclnamespace = 'ledger'::pg_catalog.regnamespace
			   AND privilege.grantee = 'platformgo_engine'::pg_catalog.regrole
			   AND privilege.privilege_type = 'MAINTAIN'
		)`).Scan(&defaultAuthorityPreserved); err != nil {
		t.Fatalf("inspect preserved default MAINTAIN authority: %v", err)
	}
	if !defaultAuthorityPreserved {
		t.Fatal("rejected migration scrubbed default MAINTAIN evidence")
	}

	if _, err := pool.Exec(ctx, `
		ALTER DEFAULT PRIVILEGES IN SCHEMA ledger
		REVOKE MAINTAIN ON TABLES FROM platformgo_engine`); err != nil {
		t.Fatalf("repair hostile default MAINTAIN authority: %v", err)
	}
	current := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after default MAINTAIN repair: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify default MAINTAIN repair retry: %v", err)
	}
	assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE ledger.runtime_authority_maintain_probe (
			id bigint PRIMARY KEY
		);
		INSERT INTO ledger.runtime_authority_maintain_probe VALUES (1)`); err != nil {
		t.Fatalf("create post-cutover MAINTAIN probe: %v", err)
	}
	var engineCanMaintain bool
	if err := pool.QueryRow(ctx, `
		SELECT pg_catalog.has_table_privilege(
			'platformgo_engine',
			'ledger.runtime_authority_maintain_probe',
			'MAINTAIN'
		)`).Scan(&engineCanMaintain); err != nil {
		t.Fatalf("inspect repaired future MAINTAIN authority: %v", err)
	}
	if engineCanMaintain {
		t.Fatal("repaired default ACL still grants engine MAINTAIN")
	}
	engine := runtimeRoleLoginPool(
		t,
		pool,
		fmt.Sprintf("runtime_authority_maintain_engine_%d", os.Getpid()),
		"platformgo_engine",
	)
	_, reindexErr := engine.Exec(ctx, `
		REINDEX TABLE ledger.runtime_authority_maintain_probe`)
	if !adminBootstrapIsPostgresCode(reindexErr, "42501") {
		t.Fatalf("engine REINDEX error = %v, want SQLSTATE 42501", reindexErr)
	}
}

func TestRuntimeAuthorityACLMigrationRejectsHostileDefaultSelectAuthority(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER DEFAULT PRIVILEGES IN SCHEMA identity
		GRANT SELECT ON TABLES TO platformgo_engine`); err != nil {
		t.Fatalf("install hostile default SELECT authority: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			ALTER DEFAULT PRIVILEGES IN SCHEMA identity
			REVOKE SELECT ON TABLES FROM platformgo_engine;
			DROP TABLE IF EXISTS identity.runtime_authority_secret_probe`)
	})

	before := runtimeAuthorityCutoverDigest(t, pool)
	err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("hostile default SELECT migration error = %v, want SQLSTATE 55000", err)
	}
	if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
		t.Fatalf("rejected default SELECT changed evidence: before %s after %s", before, after)
	}
	assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)
	var defaultAuthorityPreserved bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM pg_catalog.pg_default_acl AS defaults
			 CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
			 WHERE defaults.defaclobjtype = 'r'
			   AND defaults.defaclnamespace = 'identity'::pg_catalog.regnamespace
			   AND privilege.grantee = 'platformgo_engine'::pg_catalog.regrole
			   AND privilege.privilege_type = 'SELECT'
		)`).Scan(&defaultAuthorityPreserved); err != nil {
		t.Fatalf("inspect preserved default SELECT authority: %v", err)
	}
	if !defaultAuthorityPreserved {
		t.Fatal("rejected migration scrubbed default SELECT evidence")
	}

	if _, err := pool.Exec(ctx, `
		ALTER DEFAULT PRIVILEGES IN SCHEMA identity
		REVOKE SELECT ON TABLES FROM platformgo_engine`); err != nil {
		t.Fatalf("repair hostile default SELECT authority: %v", err)
	}
	current := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after default SELECT repair: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify default SELECT repair retry: %v", err)
	}
	assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE identity.runtime_authority_secret_probe (
			secret text PRIMARY KEY
		);
		INSERT INTO identity.runtime_authority_secret_probe VALUES ('future-secret')`); err != nil {
		t.Fatalf("create post-cutover SELECT probe: %v", err)
	}
	var engineCanSelect bool
	if err := pool.QueryRow(ctx, `
		SELECT pg_catalog.has_table_privilege(
			'platformgo_engine',
			'identity.runtime_authority_secret_probe',
			'SELECT'
		)`).Scan(&engineCanSelect); err != nil {
		t.Fatalf("inspect repaired future SELECT authority: %v", err)
	}
	if engineCanSelect {
		t.Fatal("repaired default ACL still grants engine SELECT")
	}
	engine := runtimeRoleLoginPool(
		t,
		pool,
		fmt.Sprintf("runtime_authority_select_engine_%d", os.Getpid()),
		"platformgo_engine",
	)
	var secret string
	selectErr := engine.QueryRow(ctx, `
		SELECT secret FROM identity.runtime_authority_secret_probe`).Scan(&secret)
	if !adminBootstrapIsPostgresCode(selectErr, "42501") {
		t.Fatalf("engine secret SELECT error = %v, want SQLSTATE 42501", selectErr)
	}
}

func TestRuntimeAuthorityACLMigrationRejectsHostileDefaultFunctionAuthority(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER DEFAULT PRIVILEGES IN SCHEMA identity
		GRANT EXECUTE ON FUNCTIONS TO platformgo_engine`); err != nil {
		t.Fatalf("install hostile default function authority: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			ALTER DEFAULT PRIVILEGES IN SCHEMA identity
			REVOKE EXECUTE ON FUNCTIONS FROM platformgo_engine;
			DROP FUNCTION IF EXISTS identity.runtime_authority_default_function_probe()`)
	})

	before := runtimeAuthorityCutoverDigest(t, pool)
	err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("hostile default function migration error = %v, want SQLSTATE 55000", err)
	}
	if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
		t.Fatalf("rejected default function changed evidence: before %s after %s", before, after)
	}
	assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)
	var defaultAuthorityPreserved bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM pg_catalog.pg_default_acl AS defaults
			 CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
			 WHERE defaults.defaclobjtype = 'f'
			   AND defaults.defaclnamespace = 'identity'::pg_catalog.regnamespace
			   AND privilege.grantee = 'platformgo_engine'::pg_catalog.regrole
			   AND privilege.privilege_type = 'EXECUTE'
		)`).Scan(&defaultAuthorityPreserved); err != nil {
		t.Fatalf("inspect preserved default function authority: %v", err)
	}
	if !defaultAuthorityPreserved {
		t.Fatal("rejected migration scrubbed default function evidence")
	}

	if _, err := pool.Exec(ctx, `
		ALTER DEFAULT PRIVILEGES IN SCHEMA identity
		REVOKE EXECUTE ON FUNCTIONS FROM platformgo_engine`); err != nil {
		t.Fatalf("repair hostile default function authority: %v", err)
	}
	current := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after default function repair: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify default function repair retry: %v", err)
	}
	assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION identity.runtime_authority_default_function_probe()
		RETURNS text
		LANGUAGE sql
		SECURITY DEFINER
		SET search_path = pg_catalog
		AS 'SELECT ''future-secret''::text';
		REVOKE ALL ON FUNCTION identity.runtime_authority_default_function_probe() FROM PUBLIC;
		GRANT EXECUTE ON FUNCTION identity.runtime_authority_default_function_probe() TO platformgo_api`); err != nil {
		t.Fatalf("create post-cutover function authority probe: %v", err)
	}
	var engineCanExecute bool
	if err := pool.QueryRow(ctx, `
		SELECT pg_catalog.has_function_privilege(
			'platformgo_engine',
			'identity.runtime_authority_default_function_probe()',
			'EXECUTE'
		)`).Scan(&engineCanExecute); err != nil {
		t.Fatalf("inspect repaired future function authority: %v", err)
	}
	if engineCanExecute {
		t.Fatal("repaired default ACL still grants engine function EXECUTE")
	}
	engine := runtimeRoleLoginPool(
		t,
		pool,
		fmt.Sprintf("runtime_authority_function_engine_%d", os.Getpid()),
		"platformgo_engine",
	)
	var secret string
	executeErr := engine.QueryRow(ctx, `
		SELECT identity.runtime_authority_default_function_probe()`).Scan(&secret)
	if !adminBootstrapIsPostgresCode(executeErr, "42501") {
		t.Fatalf("engine definer function error = %v, want SQLSTATE 42501", executeErr)
	}
}

func TestRuntimeAuthorityACLMigrationRejectsProjectionMutationAuthority(
	t *testing.T,
) {
	for _, relation := range []string{
		"engine.shard_checkpoints",
		"trading.risk_configs",
		"market.books",
	} {
		t.Run(relation, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatalf("apply current-main schema: %v", err)
			}
			hostileRole := fmt.Sprintf(
				"runtime_projection_writer_%d_%s",
				os.Getpid(),
				strings.NewReplacer(".", "_").Replace(relation),
			)
			hostileID := pgx.Identifier{hostileRole}.Sanitize()
			if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
				t.Fatalf("create unexpected projection writer: %v", err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
					DROP OWNED BY %[1]s CASCADE;
					DROP ROLE %[1]s`, hostileID))
			})
			if _, err := pool.Exec(ctx, fmt.Sprintf(
				"GRANT UPDATE ON TABLE %s TO %s",
				pgx.Identifier(strings.Split(relation, ".")).Sanitize(),
				hostileID,
			)); err != nil {
				t.Fatalf("grant unexpected projection authority: %v", err)
			}
			files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
			err := newCurrentTestMigrator(t, pool, files).Migrate(ctx)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
				t.Fatalf("projection authority migration error = %v, want SQLSTATE 55000", err)
			}
			var applied bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					  FROM engine.schema_migrations
					 WHERE filename = $1
				)`, runtimeAuthorityACLMigration).Scan(&applied); err != nil {
				t.Fatalf("inspect rejected migration history: %v", err)
			}
			if applied {
				t.Fatal("rejected projection-authority migration was journaled")
			}
		})
	}
}

func TestRuntimeAuthorityACLMigrationRejectsPersistedRogueTrigger(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION ledger.runtime_authority_rogue_trigger()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $function$
		BEGIN
			RETURN NEW;
		END
		$function$;
		CREATE TRIGGER runtime_authority_rogue_trigger
		BEFORE INSERT ON ledger.transactions
		FOR EACH ROW EXECUTE FUNCTION ledger.runtime_authority_rogue_trigger('persisted-argument')`); err != nil {
		t.Fatalf("install persisted rogue trigger: %v", err)
	}

	files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
	err := newCurrentTestMigrator(t, pool, files).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("rogue-trigger migration error = %v, want SQLSTATE 55000", err)
	}
	var triggerStillExists, applied bool
	if err := pool.QueryRow(ctx, `
		SELECT
			to_regprocedure('ledger.runtime_authority_rogue_trigger()') IS NOT NULL
			AND EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_trigger
				 WHERE tgrelid = 'ledger.transactions'::regclass
				   AND tgname = 'runtime_authority_rogue_trigger'
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			)`, runtimeAuthorityACLMigration).Scan(&triggerStillExists, &applied); err != nil {
		t.Fatalf("inspect rejected rogue-trigger migration: %v", err)
	}
	if !triggerStillExists || applied {
		t.Fatalf("rejected rogue-trigger migration preserved=%t applied=%t", triggerStillExists, applied)
	}
}

func TestRuntimeAuthorityACLMigrationLaterLockRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}

	hostileRole := fmt.Sprintf("runtime_authority_lock_reader_%d", os.Getpid())
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create lock-test reader: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
			DROP OWNED BY %[1]s CASCADE;
			DROP ROLE %[1]s`, hostileID))
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		"GRANT SELECT ON engine.deployment_shard TO %s",
		hostileID,
	)); err != nil {
		t.Fatalf("install lock-test grant: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin later-relation blocker: %v", err)
	}
	if _, err := blocker.Exec(ctx, "LOCK TABLE ledger.entries IN ACCESS EXCLUSIVE MODE"); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatalf("lock later relation: %v", err)
	}
	files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
	migrationErr := platformpostgres.NewMigrator(pool, files).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(migrationErr, &postgresError) || postgresError.Code != "55P03" {
		_ = blocker.Rollback(ctx)
		t.Fatalf("contended migration error = %v, want SQLSTATE 55P03", migrationErr)
	}
	var applied, readerStillAllowed bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM engine.schema_migrations WHERE filename = $1
			),
			has_table_privilege($2, 'engine.deployment_shard', 'SELECT')`,
		runtimeAuthorityACLMigration,
		hostileRole,
	).Scan(&applied, &readerStillAllowed); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatalf("inspect lock-timeout rollback: %v", err)
	}
	if applied || !readerStillAllowed {
		_ = blocker.Rollback(ctx)
		t.Fatalf("lock-timeout rollback applied=%t preservedGrant=%t", applied, readerStillAllowed)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release later-relation blocker: %v", err)
	}

	current := platformpostgres.NewMigrator(pool, files)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry runtime authority ACL migration: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("idempotent runtime authority ACL migration: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried runtime authority ACL migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM engine.schema_migrations WHERE filename = $1
			),
			has_table_privilege($2, 'engine.deployment_shard', 'SELECT')`,
		runtimeAuthorityACLMigration,
		hostileRole,
	).Scan(&applied, &readerStillAllowed); err != nil {
		t.Fatalf("inspect retried migration: %v", err)
	}
	if !applied || readerStillAllowed {
		t.Fatalf("retried migration applied=%t retainedGrant=%t", applied, readerStillAllowed)
	}
}

func TestRuntimeAuthorityACLMigrationRejectsBootstrapFunctionAuthorityDrift(
	t *testing.T,
) {
	for _, drift := range []string{"owner", "execute_acl"} {
		t.Run(drift, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatalf("apply current-main schema: %v", err)
			}
			var owner string
			if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatalf("read migration owner: %v", err)
			}
			hostileRole := fmt.Sprintf(
				"runtime_bootstrap_%s_%d",
				drift,
				os.Getpid(),
			)
			hostileID := pgx.Identifier{hostileRole}.Sanitize()
			ownerID := pgx.Identifier{owner}.Sanitize()
			if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
				t.Fatalf("create bootstrap drift role: %v", err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
					ALTER FUNCTION identity.bootstrap_first_admin(
						text, bytea, text, uuid, text, bytea
					) OWNER TO %[1]s;
					DROP OWNED BY %[2]s CASCADE;
					DROP ROLE %[2]s`, ownerID, hostileID))
			})
			switch drift {
			case "owner":
				if _, err := pool.Exec(ctx, fmt.Sprintf(`
					ALTER FUNCTION identity.bootstrap_first_admin(
						text, bytea, text, uuid, text, bytea
					) OWNER TO %s`, hostileID)); err != nil {
					t.Fatalf("change bootstrap function owner: %v", err)
				}
			case "execute_acl":
				if _, err := pool.Exec(ctx, fmt.Sprintf(`
					GRANT EXECUTE ON FUNCTION identity.bootstrap_first_admin(
						text, bytea, text, uuid, text, bytea
					) TO %s WITH GRANT OPTION`, hostileID)); err != nil {
					t.Fatalf("grant unexpected bootstrap execute: %v", err)
				}
			}

			err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLMigration),
			).Migrate(ctx)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
				t.Fatalf("bootstrap drift migration error = %v, want SQLSTATE 55000", err)
			}
			var applied bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM engine.schema_migrations WHERE filename = $1
				)`, runtimeAuthorityACLMigration).Scan(&applied); err != nil {
				t.Fatalf("inspect bootstrap drift migration history: %v", err)
			}
			if applied {
				t.Fatal("bootstrap drift migration was journaled")
			}
		})
	}
}

func TestRuntimeAuthorityACLMigrationRejectsEnabledEventTrigger(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	hostileRole := fmt.Sprintf("runtime_authority_event_%d", os.Getpid())
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create event-trigger target role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
			DROP EVENT TRIGGER IF EXISTS runtime_authority_event_trigger;
			DROP FUNCTION IF EXISTS public.runtime_authority_event_trigger();
			DROP OWNED BY %[1]s CASCADE;
			DROP ROLE %[1]s`, hostileID))
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION public.runtime_authority_event_trigger()
		RETURNS event_trigger
		LANGUAGE plpgsql
		AS $function$
		BEGIN
			EXECUTE 'GRANT UPDATE ON trading.risk_configs TO %s';
		END
		$function$;
		CREATE EVENT TRIGGER runtime_authority_event_trigger
		ON ddl_command_end
		WHEN TAG IN ('CREATE FUNCTION')
		EXECUTE FUNCTION public.runtime_authority_event_trigger()`, hostileID)); err != nil {
		t.Fatalf("install enabled event trigger: %v", err)
	}

	err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("event-trigger migration error = %v, want SQLSTATE 55000", err)
	}
	var eventTriggerEnabled, applied bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_event_trigger
				 WHERE evtname = 'runtime_authority_event_trigger'
				   AND evtenabled <> 'D'
			),
			EXISTS (
				SELECT 1 FROM engine.schema_migrations WHERE filename = $1
			)`, runtimeAuthorityACLMigration).Scan(&eventTriggerEnabled, &applied); err != nil {
		t.Fatalf("inspect rejected event-trigger migration: %v", err)
	}
	if !eventTriggerEnabled || applied {
		t.Fatalf("rejected event-trigger migration preserved=%t applied=%t", eventTriggerEnabled, applied)
	}
}

func TestRuntimeAuthorityBootstrapRejectsDivergentMigration43Checksum(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply runtime authority schema: %v", err)
	}
	terminal := runtimeRoleLoginPool(
		t,
		pool,
		"runtime_authority_checksum_bootstrap_login",
		"platformgo_admin_bootstrap",
	)
	if _, err := pool.Exec(ctx, `
		UPDATE engine.schema_migrations
		   SET checksum = decode(repeat('00', 32), 'hex')
		 WHERE filename = $1`, runtimeAuthorityACLMigration); err != nil {
		t.Fatalf("tamper migration-43 checksum: %v", err)
	}
	keyHash := sha256.Sum256([]byte("runtime-authority-checksum-key"))
	var outcome string
	err := terminal.QueryRow(ctx, `
		SELECT outcome
		  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5, $6)`,
		"runtime-authority-checksum-request",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000143",
		"00000000-0000-4000-8000-000000000143",
		"2026-07-31T00:00:00.000000Z",
		runtimeAuthorityACLMigrationChecksum(t),
	).Scan(&outcome)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("divergent migration-43 bootstrap error = %v outcome=%q, want 55000", err, outcome)
	}
	assertAdminBootstrapAuthorityCounts(t, pool, 0, 0)
}

func TestRuntimeAuthorityACLMigrationRejectsMigrationJournalSideEffects(t *testing.T) {
	tests := []struct {
		name          string
		install       string
		repair        string
		artifactQuery string
	}{
		{
			name: "after insert trigger",
			install: `
				CREATE FUNCTION engine.runtime_authority_hostile_journal_trigger()
				RETURNS trigger
				LANGUAGE plpgsql
				AS $function$
				BEGIN
					EXECUTE 'GRANT TRUNCATE ON ledger.entries TO platformgo_engine';
					RETURN NEW;
				END
				$function$;
				CREATE TRIGGER runtime_authority_hostile_journal_trigger
				AFTER INSERT ON engine.schema_migrations
				FOR EACH ROW
				EXECUTE FUNCTION engine.runtime_authority_hostile_journal_trigger()`,
			repair: `
				DROP TRIGGER runtime_authority_hostile_journal_trigger
				ON engine.schema_migrations;
				DROP FUNCTION engine.runtime_authority_hostile_journal_trigger()`,
			artifactQuery: `
				SELECT EXISTS (
					SELECT 1
					  FROM pg_catalog.pg_trigger
					 WHERE tgrelid = 'engine.schema_migrations'::pg_catalog.regclass
					   AND tgname = 'runtime_authority_hostile_journal_trigger'
				)`,
		},
		{
			name: "insert rule",
			install: `
				CREATE RULE runtime_authority_suppress_journal AS
				ON INSERT TO engine.schema_migrations
				DO INSTEAD NOTHING`,
			repair: `
				DROP RULE runtime_authority_suppress_journal
				ON engine.schema_migrations`,
			artifactQuery: `
				SELECT EXISTS (
					SELECT 1
					  FROM pg_catalog.pg_rewrite
					 WHERE ev_class = 'engine.schema_migrations'::pg_catalog.regclass
					   AND rulename = 'runtime_authority_suppress_journal'
				)`,
		},
		{
			name: "applied at default",
			install: `
				CREATE FUNCTION engine.runtime_authority_hostile_journal_default()
				RETURNS timestamp with time zone
				LANGUAGE plpgsql
				VOLATILE
				AS $function$
				BEGIN
					EXECUTE 'GRANT TRUNCATE ON ledger.entries TO platformgo_engine';
					RETURN pg_catalog.clock_timestamp();
				END
				$function$;
				ALTER TABLE engine.schema_migrations
				ALTER COLUMN applied_at
				SET DEFAULT engine.runtime_authority_hostile_journal_default()`,
			repair: `
				ALTER TABLE engine.schema_migrations
				ALTER COLUMN applied_at SET DEFAULT pg_catalog.clock_timestamp();
				DROP FUNCTION engine.runtime_authority_hostile_journal_default()`,
			artifactQuery: `
				SELECT EXISTS (
					SELECT 1
					  FROM pg_catalog.pg_attribute AS attribute
					  JOIN pg_catalog.pg_attrdef AS default_value
					    ON default_value.adrelid = attribute.attrelid
					   AND default_value.adnum = attribute.attnum
					 WHERE attribute.attrelid =
					           'engine.schema_migrations'::pg_catalog.regclass
					   AND attribute.attname = 'applied_at'
					   AND pg_catalog.pg_get_expr(
					           default_value.adbin,
					           default_value.adrelid
					       ) = 'engine.runtime_authority_hostile_journal_default()'
				)`,
		},
		{
			name: "applied at default execution hint",
			install: `
				UPDATE pg_catalog.pg_attribute
				   SET atthasdef = false
				 WHERE attrelid =
				           'engine.schema_migrations'::pg_catalog.regclass
				   AND attname = 'applied_at'`,
			repair: `
				UPDATE pg_catalog.pg_attribute
				   SET atthasdef = true
				 WHERE attrelid =
				           'engine.schema_migrations'::pg_catalog.regclass
				   AND attname = 'applied_at'`,
			artifactQuery: `
				SELECT NOT atthasdef
				  FROM pg_catalog.pg_attribute
				 WHERE attrelid =
				           'engine.schema_migrations'::pg_catalog.regclass
				   AND attname = 'applied_at'`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatalf("apply tip 42: %v", err)
			}
			if _, err := pool.Exec(ctx, `
				WITH journal_transaction AS (
					INSERT INTO ledger.transactions (
						transaction_id, business_key, input_id, logical_time
					) VALUES (
						'00000000-0000-4000-8000-000000000098',
						'runtime-authority-journal-side-effect',
						'00000000-0000-4000-8000-000000000099',
						1785456000000000000
					)
					RETURNING transaction_id
				)
				INSERT INTO ledger.entries (
					entry_id, transaction_id, account_id, currency, amount
				)
				SELECT
					'00000000-0000-4000-8000-000000000100'::uuid,
					transaction_id,
					'account-runtime-authority-journal',
					'USDC',
					1
				FROM journal_transaction
				UNION ALL
				SELECT
					'00000000-0000-4000-8000-000000000101'::uuid,
					transaction_id,
					'system:clearing',
					'USDC',
					-1
				FROM journal_transaction`); err != nil {
				t.Fatalf("install balanced ledger evidence: %v", err)
			}
			if _, err := pool.Exec(ctx, test.install); err != nil {
				t.Fatalf("install migration journal side effect: %v", err)
			}

			before := runtimeAuthorityCutoverDigest(t, pool)
			err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLMigration),
			).Migrate(ctx)
			if !adminBootstrapIsPostgresCode(err, "55000") {
				t.Fatalf("journal-side-effect migration error = %v, want 55000", err)
			}
			if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
				t.Fatalf("rejected migration changed evidence: before %s after %s", before, after)
			}
			assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)

			var artifactPreserved bool
			if err := pool.QueryRow(ctx, test.artifactQuery).Scan(&artifactPreserved); err != nil {
				t.Fatalf("inspect preserved journal side effect: %v", err)
			}
			if !artifactPreserved {
				t.Fatal("rejected migration removed journal side-effect evidence")
			}
			var transactions, entries int
			var engineCanTruncate bool
			if err := pool.QueryRow(ctx, `
				SELECT
					(SELECT count(*) FROM ledger.transactions),
					(SELECT count(*) FROM ledger.entries),
					has_table_privilege(
						'platformgo_engine', 'ledger.entries', 'TRUNCATE'
					)`,
			).Scan(&transactions, &entries, &engineCanTruncate); err != nil {
				t.Fatalf("inspect preserved ledger evidence: %v", err)
			}
			if transactions != 1 || entries != 2 || engineCanTruncate {
				t.Fatalf(
					"preserved ledger evidence = transactions %d entries %d truncate %t",
					transactions,
					entries,
					engineCanTruncate,
				)
			}

			if _, err := pool.Exec(ctx, test.repair); err != nil {
				t.Fatalf("repair migration journal side effect: %v", err)
			}
			files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
			current := platformpostgres.NewMigrator(pool, files)
			if err := current.Migrate(ctx); err != nil {
				t.Fatalf("retry migration after journal repair: %v", err)
			}
			if err := current.VerifyCurrent(ctx); err != nil {
				t.Fatalf("verify retry after journal repair: %v", err)
			}
			assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
			assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_engine", map[string]string{
				"engine.deployment_shard":            "SELECT",
				"engine.shard_ownership_epochs":      "SELECT,INSERT",
				"engine.shard_checkpoints":           "SELECT,INSERT,UPDATE",
				"engine.shard_faults":                "SELECT,INSERT",
				"engine.duplicate_delivery_receipts": "SELECT,INSERT",
				"trading.risk_configs":               "SELECT,INSERT,UPDATE",
				"market.books":                       "SELECT,INSERT,UPDATE",
				"ledger.transactions":                "SELECT,INSERT",
				"ledger.entries":                     "SELECT,INSERT",
			})
			assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_api", map[string]string{
				"engine.deployment_shard": "SELECT",
				"trading.risk_configs":    "SELECT",
				"market.books":            "SELECT",
			})
			assertRuntimeAuthorityColumnPrivileges(t, pool)

		})
	}
}

func TestRuntimeAuthorityACLMigrationRejectsTargetRewriteRule(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply tip 42: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH ledger_transaction AS (
			INSERT INTO ledger.transactions (
				transaction_id, business_key, input_id, logical_time
			) VALUES (
				'00000000-0000-4000-8000-000000000102',
				'runtime-authority-target-rule',
				'00000000-0000-4000-8000-000000000103',
				1785456000000000000
			)
			RETURNING transaction_id
		)
		INSERT INTO ledger.entries (
			entry_id, transaction_id, account_id, currency, amount
		)
		SELECT
			'00000000-0000-4000-8000-000000000104'::uuid,
			transaction_id,
			'account-runtime-authority-target-rule',
			'USDC',
			1
		FROM ledger_transaction
		UNION ALL
		SELECT
			'00000000-0000-4000-8000-000000000105'::uuid,
			transaction_id,
			'system:clearing',
			'USDC',
			-1
		FROM ledger_transaction;
		CREATE RULE runtime_authority_suppress_ledger_entry AS
		ON INSERT TO ledger.entries
		DO INSTEAD NOTHING`); err != nil {
		t.Fatalf("install target-rule fixture: %v", err)
	}

	before := runtimeAuthorityCutoverDigest(t, pool)
	err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("target-rule migration error = %v, want 55000", err)
	}
	if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
		t.Fatalf("rejected target-rule migration changed evidence: before %s after %s", before, after)
	}
	assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)

	var rulePreserved bool
	var transactions, entries int
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_rewrite
				 WHERE ev_class = 'ledger.entries'::pg_catalog.regclass
				   AND rulename = 'runtime_authority_suppress_ledger_entry'
			),
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries)`,
	).Scan(&rulePreserved, &transactions, &entries); err != nil {
		t.Fatalf("inspect preserved target-rule evidence: %v", err)
	}
	if !rulePreserved || transactions != 1 || entries != 2 {
		t.Fatalf(
			"target-rule evidence = rule %t transactions %d entries %d",
			rulePreserved,
			transactions,
			entries,
		)
	}

	if _, err := pool.Exec(ctx, `
		DROP RULE runtime_authority_suppress_ledger_entry ON ledger.entries`,
	); err != nil {
		t.Fatalf("repair target-rule fixture: %v", err)
	}
	// PostgreSQL keeps relhasrules as a conservative true hint until VACUUM
	// proves that the last rewrite rule is gone. The production preflight
	// requires both the exact graph and a consistent hint.
	if _, err := pool.Exec(ctx, `VACUUM ledger.entries`); err != nil {
		t.Fatalf("repair target-rule catalog hint: %v", err)
	}
	files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
	current := platformpostgres.NewMigrator(pool, files)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after target-rule repair: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retry after target-rule repair: %v", err)
	}
	assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
	assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_engine", map[string]string{
		"engine.deployment_shard":            "SELECT",
		"engine.shard_ownership_epochs":      "SELECT,INSERT",
		"engine.shard_checkpoints":           "SELECT,INSERT,UPDATE",
		"engine.shard_faults":                "SELECT,INSERT",
		"engine.duplicate_delivery_receipts": "SELECT,INSERT",
		"trading.risk_configs":               "SELECT,INSERT,UPDATE",
		"market.books":                       "SELECT,INSERT,UPDATE",
		"ledger.transactions":                "SELECT,INSERT",
		"ledger.entries":                     "SELECT,INSERT",
	})
	assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_api", map[string]string{
		"engine.deployment_shard": "SELECT",
		"trading.risk_configs":    "SELECT",
		"market.books":            "SELECT",
	})
	assertRuntimeAuthorityColumnPrivileges(t, pool)
}

func TestRuntimeAuthorityACLMigrationRejectsTargetExecutableDefault(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply tip 42: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH ledger_transaction AS (
			INSERT INTO ledger.transactions (
				transaction_id, business_key, input_id, logical_time
			) VALUES (
				'00000000-0000-4000-8000-000000000106',
				'runtime-authority-target-default',
				'00000000-0000-4000-8000-000000000107',
				1785456000000000000
			)
			RETURNING transaction_id
		)
		INSERT INTO ledger.entries (
			entry_id, transaction_id, account_id, currency, amount
		)
		SELECT
			'00000000-0000-4000-8000-000000000108'::uuid,
			transaction_id,
			'account-runtime-authority-target-default',
			'USDC',
			1
		FROM ledger_transaction
		UNION ALL
		SELECT
			'00000000-0000-4000-8000-000000000109'::uuid,
			transaction_id,
			'system:clearing',
			'USDC',
			-1
		FROM ledger_transaction;
		CREATE FUNCTION engine.runtime_authority_hostile_created_at_default()
		RETURNS timestamp with time zone
		LANGUAGE plpgsql
		VOLATILE
		SECURITY DEFINER
		SET search_path = pg_catalog
		AS $function$
		BEGIN
			EXECUTE 'GRANT TRUNCATE ON ledger.entries TO platformgo_engine';
			RETURN pg_catalog.clock_timestamp();
		END
		$function$;
		GRANT EXECUTE
		ON FUNCTION engine.runtime_authority_hostile_created_at_default()
		TO PUBLIC;
		ALTER TABLE ledger.transactions
		ALTER COLUMN created_at
		SET DEFAULT engine.runtime_authority_hostile_created_at_default()`); err != nil {
		t.Fatalf("install target-default fixture: %v", err)
	}

	before := runtimeAuthorityCutoverDigest(t, pool)
	err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("target-default migration error = %v, want 55000", err)
	}
	if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
		t.Fatalf("rejected target-default migration changed evidence: before %s after %s", before, after)
	}
	assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)

	var defaultPreserved, engineCanTruncate bool
	var transactions, entries int
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_attribute AS attribute
				  JOIN pg_catalog.pg_attrdef AS default_value
				    ON default_value.adrelid = attribute.attrelid
				   AND default_value.adnum = attribute.attnum
				 WHERE attribute.attrelid =
				           'ledger.transactions'::pg_catalog.regclass
				   AND attribute.attname = 'created_at'
				   AND pg_catalog.pg_get_expr(
				           default_value.adbin,
				           default_value.adrelid
				       ) = 'engine.runtime_authority_hostile_created_at_default()'
			),
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries),
			has_table_privilege(
				'platformgo_engine', 'ledger.entries', 'TRUNCATE'
			)`,
	).Scan(
		&defaultPreserved,
		&transactions,
		&entries,
		&engineCanTruncate,
	); err != nil {
		t.Fatalf("inspect preserved target-default evidence: %v", err)
	}
	if !defaultPreserved || transactions != 1 || entries != 2 || engineCanTruncate {
		t.Fatalf(
			"target-default evidence = default %t transactions %d entries %d truncate %t",
			defaultPreserved,
			transactions,
			entries,
			engineCanTruncate,
		)
	}

	if _, err := pool.Exec(ctx, `
		ALTER TABLE ledger.transactions
		ALTER COLUMN created_at SET DEFAULT pg_catalog.clock_timestamp();
		DROP FUNCTION engine.runtime_authority_hostile_created_at_default()`); err != nil {
		t.Fatalf("repair target-default fixture: %v", err)
	}
	files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
	current := platformpostgres.NewMigrator(pool, files)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after target-default repair: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retry after target-default repair: %v", err)
	}
	assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
	assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_engine", map[string]string{
		"engine.deployment_shard":            "SELECT",
		"engine.shard_ownership_epochs":      "SELECT,INSERT",
		"engine.shard_checkpoints":           "SELECT,INSERT,UPDATE",
		"engine.shard_faults":                "SELECT,INSERT",
		"engine.duplicate_delivery_receipts": "SELECT,INSERT",
		"trading.risk_configs":               "SELECT,INSERT,UPDATE",
		"market.books":                       "SELECT,INSERT,UPDATE",
		"ledger.transactions":                "SELECT,INSERT",
		"ledger.entries":                     "SELECT,INSERT",
	})
	assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_api", map[string]string{
		"engine.deployment_shard": "SELECT",
		"trading.risk_configs":    "SELECT",
		"market.books":            "SELECT",
	})
	assertRuntimeAuthorityColumnPrivileges(t, pool)
}

func TestRuntimeAuthorityACLMigrationRejectsTargetExecutableCatalog(t *testing.T) {
	tests := []struct {
		name          string
		install       string
		repair        string
		artifactQuery string
	}{
		{
			name: "disabled internal foreign key trigger",
			install: `
				DO $block$
				DECLARE
					trigger_name name;
				BEGIN
					SELECT tgname
					  INTO trigger_name
					  FROM pg_catalog.pg_trigger
					 WHERE tgrelid = 'ledger.entries'::pg_catalog.regclass
					   AND tgisinternal
					   AND tgtype = 5
					 ORDER BY tgname
					 LIMIT 1;
					IF trigger_name IS NULL THEN
						RAISE EXCEPTION 'ledger entry FK insert trigger is missing';
					END IF;
					EXECUTE pg_catalog.format(
						'ALTER TABLE ledger.entries DISABLE TRIGGER %I',
						trigger_name
					);
				END
				$block$`,
			repair: `
				DO $block$
				DECLARE
					trigger_name name;
				BEGIN
					FOR trigger_name IN
						SELECT tgname
						  FROM pg_catalog.pg_trigger
						 WHERE tgrelid = 'ledger.entries'::pg_catalog.regclass
						   AND tgisinternal
						   AND tgenabled <> 'O'
					LOOP
						EXECUTE pg_catalog.format(
							'ALTER TABLE ledger.entries ENABLE TRIGGER %I',
							trigger_name
						);
					END LOOP;
				END
				$block$`,
			artifactQuery: `
				SELECT EXISTS (
					SELECT 1 FROM pg_catalog.pg_trigger
					 WHERE tgrelid = 'ledger.entries'::pg_catalog.regclass
					   AND tgisinternal
					   AND tgenabled <> 'O'
				)`,
		},
		{
			name: "false trigger execution hint",
			install: `
				UPDATE pg_catalog.pg_class
				   SET relhastriggers = false
				 WHERE oid = 'ledger.entries'::pg_catalog.regclass`,
			repair: `
				UPDATE pg_catalog.pg_class
				   SET relhastriggers = true
				 WHERE oid = 'ledger.entries'::pg_catalog.regclass`,
			artifactQuery: `
				SELECT NOT relhastriggers AND EXISTS (
					SELECT 1 FROM pg_catalog.pg_trigger
					 WHERE tgrelid = 'ledger.entries'::pg_catalog.regclass
				)
				FROM pg_catalog.pg_class
				WHERE oid = 'ledger.entries'::pg_catalog.regclass`,
		},
		{
			name: "false default execution hint",
			install: `
				UPDATE pg_catalog.pg_attribute
				   SET atthasdef = false
				 WHERE attrelid = 'ledger.transactions'::pg_catalog.regclass
				   AND attname = 'created_at'`,
			repair: `
				UPDATE pg_catalog.pg_attribute
				   SET atthasdef = true
				 WHERE attrelid = 'ledger.transactions'::pg_catalog.regclass
				   AND attname = 'created_at'`,
			artifactQuery: `
				SELECT NOT atthasdef
				  FROM pg_catalog.pg_attribute
				 WHERE attrelid = 'ledger.transactions'::pg_catalog.regclass
				   AND attname = 'created_at'`,
		},
		{
			name: "check constraint",
			install: `
				CREATE FUNCTION engine.runtime_authority_hostile_ledger_check()
				RETURNS boolean
				LANGUAGE plpgsql VOLATILE SECURITY DEFINER
				SET search_path = pg_catalog
				AS $function$
				BEGIN
					EXECUTE 'GRANT TRUNCATE ON ledger.entries TO platformgo_engine';
					RETURN true;
				END
				$function$;
				ALTER TABLE ledger.transactions
				ADD CONSTRAINT runtime_authority_hostile_ledger_check
				CHECK (engine.runtime_authority_hostile_ledger_check()) NOT VALID`,
			repair: `
				ALTER TABLE ledger.transactions
				DROP CONSTRAINT runtime_authority_hostile_ledger_check;
				DROP FUNCTION engine.runtime_authority_hostile_ledger_check()`,
			artifactQuery: `
				SELECT EXISTS (
					SELECT 1 FROM pg_catalog.pg_constraint
					 WHERE conrelid = 'ledger.transactions'::pg_catalog.regclass
					   AND conname = 'runtime_authority_hostile_ledger_check'
				)`,
		},
		{
			name: "row security policy",
			install: `
				CREATE FUNCTION engine.runtime_authority_hostile_ledger_policy()
				RETURNS boolean
				LANGUAGE plpgsql VOLATILE SECURITY DEFINER
				SET search_path = pg_catalog
				AS $function$
				BEGIN
					EXECUTE 'GRANT TRUNCATE ON ledger.entries TO platformgo_engine';
					RETURN true;
				END
				$function$;
				ALTER TABLE ledger.transactions ENABLE ROW LEVEL SECURITY;
				CREATE POLICY runtime_authority_hostile_ledger_policy
				ON ledger.transactions
				FOR INSERT TO platformgo_engine
				WITH CHECK (engine.runtime_authority_hostile_ledger_policy())`,
			repair: `
				DROP POLICY runtime_authority_hostile_ledger_policy
				ON ledger.transactions;
				ALTER TABLE ledger.transactions DISABLE ROW LEVEL SECURITY;
				DROP FUNCTION engine.runtime_authority_hostile_ledger_policy()`,
			artifactQuery: `
				SELECT relrowsecurity AND EXISTS (
					SELECT 1 FROM pg_catalog.pg_policy
					 WHERE polrelid = 'ledger.transactions'::pg_catalog.regclass
					   AND polname = 'runtime_authority_hostile_ledger_policy'
				)
				FROM pg_catalog.pg_class
				WHERE oid = 'ledger.transactions'::pg_catalog.regclass`,
		},
		{
			name: "expression index",
			install: `
				CREATE FUNCTION engine.runtime_authority_hostile_ledger_index()
				RETURNS boolean
				LANGUAGE plpgsql IMMUTABLE SECURITY DEFINER
				SET search_path = pg_catalog
				AS $function$
				BEGIN
					IF pg_catalog.current_setting('role', true) = 'platformgo_engine' THEN
						EXECUTE 'GRANT TRUNCATE ON ledger.entries TO platformgo_engine';
					END IF;
					RETURN true;
				END
				$function$;
				CREATE INDEX runtime_authority_hostile_ledger_index
				ON ledger.transactions
				((engine.runtime_authority_hostile_ledger_index()))`,
			repair: `
				DROP INDEX ledger.runtime_authority_hostile_ledger_index;
				DROP FUNCTION engine.runtime_authority_hostile_ledger_index()`,
			artifactQuery: `
				SELECT pg_catalog.to_regclass(
					'ledger.runtime_authority_hostile_ledger_index'
				) IS NOT NULL`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatalf("apply tip 42: %v", err)
			}
			if _, err := pool.Exec(ctx, `
				WITH ledger_transaction AS (
					INSERT INTO ledger.transactions (
						transaction_id, business_key, input_id, logical_time
					) VALUES (
						'00000000-0000-4000-8000-000000000110',
						'runtime-authority-executable-catalog',
						'00000000-0000-4000-8000-000000000111',
						1785456000000000000
					)
					RETURNING transaction_id
				)
				INSERT INTO ledger.entries (
					entry_id, transaction_id, account_id, currency, amount
				)
				SELECT
					'00000000-0000-4000-8000-000000000112'::uuid,
					transaction_id,
					'account-runtime-authority-executable-catalog',
					'USDC', 1
				FROM ledger_transaction
				UNION ALL
				SELECT
					'00000000-0000-4000-8000-000000000113'::uuid,
					transaction_id,
					'system:clearing',
					'USDC', -1
				FROM ledger_transaction`); err != nil {
				t.Fatalf("install balanced ledger evidence: %v", err)
			}
			if _, err := pool.Exec(ctx, test.install); err != nil {
				t.Fatalf("install executable catalog fixture: %v", err)
			}

			before := runtimeAuthorityCutoverDigest(t, pool)
			err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLMigration),
			).Migrate(ctx)
			if !adminBootstrapIsPostgresCode(err, "55000") {
				t.Fatalf("executable-catalog migration error = %v, want 55000", err)
			}
			if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
				t.Fatalf("rejected executable catalog changed evidence: before %s after %s", before, after)
			}
			assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)

			var artifactPreserved, engineCanTruncate bool
			var transactions, entries int
			if err := pool.QueryRow(ctx, `
				SELECT
					(`+test.artifactQuery+`),
					(SELECT count(*) FROM ledger.transactions),
					(SELECT count(*) FROM ledger.entries),
					has_table_privilege(
						'platformgo_engine', 'ledger.entries', 'TRUNCATE'
					)`,
			).Scan(
				&artifactPreserved,
				&transactions,
				&entries,
				&engineCanTruncate,
			); err != nil {
				t.Fatalf("inspect executable catalog evidence: %v", err)
			}
			if !artifactPreserved || transactions != 1 || entries != 2 || engineCanTruncate {
				t.Fatalf(
					"executable catalog evidence = artifact %t transactions %d entries %d truncate %t",
					artifactPreserved,
					transactions,
					entries,
					engineCanTruncate,
				)
			}

			if _, err := pool.Exec(ctx, test.repair); err != nil {
				t.Fatalf("repair executable catalog fixture: %v", err)
			}
			files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
			current := platformpostgres.NewMigrator(pool, files)
			if err := current.Migrate(ctx); err != nil {
				t.Fatalf("retry migration after executable catalog repair: %v", err)
			}
			if err := current.VerifyCurrent(ctx); err != nil {
				t.Fatalf("verify executable catalog repair retry: %v", err)
			}
			assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
			assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_engine", map[string]string{
				"engine.deployment_shard":            "SELECT",
				"engine.shard_ownership_epochs":      "SELECT,INSERT",
				"engine.shard_checkpoints":           "SELECT,INSERT,UPDATE",
				"engine.shard_faults":                "SELECT,INSERT",
				"engine.duplicate_delivery_receipts": "SELECT,INSERT",
				"trading.risk_configs":               "SELECT,INSERT,UPDATE",
				"market.books":                       "SELECT,INSERT,UPDATE",
				"ledger.transactions":                "SELECT,INSERT",
				"ledger.entries":                     "SELECT,INSERT",
			})
			assertRuntimeAuthorityTablePrivileges(t, pool, "platformgo_api", map[string]string{
				"engine.deployment_shard": "SELECT",
				"trading.risk_configs":    "SELECT",
				"market.books":            "SELECT",
			})
			assertRuntimeAuthorityColumnPrivileges(t, pool)

			if test.name == "disabled internal foreign key trigger" {
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatalf("begin repaired orphan rejection probe: %v", err)
				}
				if _, err := tx.Exec(ctx, "SET LOCAL ROLE platformgo_engine"); err != nil {
					_ = tx.Rollback(ctx)
					t.Fatalf("assume engine role for orphan rejection: %v", err)
				}
				_, insertErr := tx.Exec(ctx, `
					INSERT INTO ledger.entries (
						entry_id, transaction_id, account_id, currency, amount
					) VALUES
						(
							'00000000-0000-4000-8000-000000000114',
							'00000000-0000-4000-8000-000000000199',
							'account-runtime-authority-orphan',
							'USDC', 1
						),
						(
							'00000000-0000-4000-8000-000000000115',
							'00000000-0000-4000-8000-000000000199',
							'system:clearing',
							'USDC', -1
						)`)
				_ = tx.Rollback(ctx)
				if !adminBootstrapIsPostgresCode(insertErr, "23503") {
					t.Fatalf("repaired orphan insert error = %v, want 23503", insertErr)
				}
			}
			if test.name == "false default execution hint" {
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatalf("begin repaired default probe: %v", err)
				}
				if _, err := tx.Exec(ctx, "SET LOCAL ROLE platformgo_engine"); err != nil {
					_ = tx.Rollback(ctx)
					t.Fatalf("assume engine role for default probe: %v", err)
				}
				var defaultExecuted bool
				err = tx.QueryRow(ctx, `
					INSERT INTO ledger.transactions (
						transaction_id, business_key, input_id, logical_time
					) VALUES (
						'00000000-0000-4000-8000-000000000116',
						'runtime-authority-default-hint-probe',
						'00000000-0000-4000-8000-000000000117',
						1785456000000000000
					)
					RETURNING created_at IS NOT NULL`,
				).Scan(&defaultExecuted)
				_ = tx.Rollback(ctx)
				if err != nil || !defaultExecuted {
					t.Fatalf("repaired default probe executed=%t error=%v", defaultExecuted, err)
				}
			}
		})
	}
}

func TestRuntimeAuthorityACLMigrationPinsCatalogSearchPath(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply tip 42: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION public.clock_timestamp()
		RETURNS timestamp with time zone
		LANGUAGE plpgsql VOLATILE SECURITY DEFINER
		SET search_path = pg_catalog
		AS $function$
		BEGIN
			EXECUTE 'GRANT TRUNCATE ON ledger.entries TO platformgo_engine';
			RETURN pg_catalog.clock_timestamp();
		END
		$function$;
		ALTER TABLE engine.schema_migrations
			ALTER COLUMN applied_at SET DEFAULT public.clock_timestamp();
		ALTER TABLE engine.deployment_shard
			ALTER COLUMN selected_at SET DEFAULT public.clock_timestamp();
		ALTER TABLE engine.shard_ownership_epochs
			ALTER COLUMN acquired_at SET DEFAULT public.clock_timestamp();
		ALTER TABLE engine.shard_checkpoints
			ALTER COLUMN updated_at SET DEFAULT public.clock_timestamp();
		ALTER TABLE engine.shard_faults
			ALTER COLUMN committed_at SET DEFAULT public.clock_timestamp();
		ALTER TABLE engine.duplicate_delivery_receipts
			ALTER COLUMN committed_at SET DEFAULT public.clock_timestamp();
		ALTER TABLE market.books
			ALTER COLUMN updated_at SET DEFAULT public.clock_timestamp();
		ALTER TABLE ledger.transactions
			ALTER COLUMN created_at SET DEFAULT public.clock_timestamp()`); err != nil {
		t.Fatalf("install shadowed default fixture: %v", err)
	}

	var owner string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	ownerID := pgx.Identifier{owner}.Sanitize()
	if _, err := pool.Exec(ctx, "ALTER ROLE "+ownerID+" SET search_path = public, pg_catalog"); err != nil {
		t.Fatalf("set hostile owner search path: %v", err)
	}
	pool.Reset()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER ROLE "+ownerID+" RESET search_path")
		pool.Reset()
	})

	before := runtimeAuthorityCutoverDigest(t, pool)
	err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx)
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("shadowed-default migration error = %v, want 55000", err)
	}
	if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
		t.Fatalf("rejected shadowed defaults changed evidence: before %s after %s", before, after)
	}
	assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)

	var hostileBindings int
	var engineCanTruncate bool
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*),
			(SELECT has_table_privilege(
				'platformgo_engine', 'ledger.entries', 'TRUNCATE'
			))
		FROM pg_catalog.pg_attrdef AS default_value
		JOIN pg_catalog.pg_depend AS dependency
		  ON dependency.classid = 'pg_catalog.pg_attrdef'::pg_catalog.regclass
		 AND dependency.objid = default_value.oid
		WHERE dependency.refclassid = 'pg_catalog.pg_proc'::pg_catalog.regclass
		  AND dependency.refobjid = 'public.clock_timestamp()'::pg_catalog.regprocedure`,
	).Scan(&hostileBindings, &engineCanTruncate); err != nil {
		t.Fatalf("inspect shadowed default evidence: %v", err)
	}
	if hostileBindings != 8 || engineCanTruncate {
		t.Fatalf("shadowed default evidence = bindings %d truncate %t", hostileBindings, engineCanTruncate)
	}

	if _, err := pool.Exec(ctx, `
		ALTER TABLE engine.schema_migrations
			ALTER COLUMN applied_at SET DEFAULT pg_catalog.clock_timestamp();
		ALTER TABLE engine.deployment_shard
			ALTER COLUMN selected_at SET DEFAULT pg_catalog.clock_timestamp();
		ALTER TABLE engine.shard_ownership_epochs
			ALTER COLUMN acquired_at SET DEFAULT pg_catalog.clock_timestamp();
		ALTER TABLE engine.shard_checkpoints
			ALTER COLUMN updated_at SET DEFAULT pg_catalog.clock_timestamp();
		ALTER TABLE engine.shard_faults
			ALTER COLUMN committed_at SET DEFAULT pg_catalog.clock_timestamp();
		ALTER TABLE engine.duplicate_delivery_receipts
			ALTER COLUMN committed_at SET DEFAULT pg_catalog.clock_timestamp();
		ALTER TABLE market.books
			ALTER COLUMN updated_at SET DEFAULT pg_catalog.clock_timestamp();
		ALTER TABLE ledger.transactions
			ALTER COLUMN created_at SET DEFAULT pg_catalog.clock_timestamp();
		DROP FUNCTION public.clock_timestamp()`); err != nil {
		t.Fatalf("repair shadowed default fixture: %v", err)
	}
	files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
	current := platformpostgres.NewMigrator(pool, files)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after shadowed-default repair: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify shadowed-default repair retry: %v", err)
	}
	assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
}

func TestRuntimeAuthorityACLMigrationRejectsLatentLedgerCorruption(t *testing.T) {
	tests := []struct {
		name          string
		install       string
		repair        string
		artifactQuery string
	}{
		{
			name: "balanced orphan entries",
			install: `
				DO $block$
				DECLARE
					trigger_name name;
				BEGIN
					SELECT tgname
					  INTO trigger_name
					  FROM pg_catalog.pg_trigger
					 WHERE tgrelid = 'ledger.entries'::pg_catalog.regclass
					   AND tgisinternal
					   AND tgtype = 5
					 ORDER BY tgname
					 LIMIT 1;
					EXECUTE pg_catalog.format(
						'ALTER TABLE ledger.entries DISABLE TRIGGER %I',
						trigger_name
					);
				END
				$block$;
				INSERT INTO ledger.entries (
					entry_id, transaction_id, account_id, currency, amount
				) VALUES
					(
						'00000000-0000-4000-8000-000000000120',
						'00000000-0000-4000-8000-000000000122',
						'system:clearing', 'USDC', 11
					),
					(
						'00000000-0000-4000-8000-000000000121',
						'00000000-0000-4000-8000-000000000122',
						'system:clearing', 'USDC', -11
					);
				SET CONSTRAINTS ledger.ledger_transaction_must_balance IMMEDIATE;
				DO $block$
				DECLARE
					trigger_name name;
				BEGIN
					FOR trigger_name IN
						SELECT tgname
						  FROM pg_catalog.pg_trigger
						 WHERE tgrelid = 'ledger.entries'::pg_catalog.regclass
						   AND tgisinternal
						   AND tgenabled <> 'O'
					LOOP
						EXECUTE pg_catalog.format(
							'ALTER TABLE ledger.entries ENABLE TRIGGER %I',
							trigger_name
						);
					END LOOP;
				END
				$block$`,
			repair: `
				INSERT INTO ledger.transactions (
					transaction_id, business_key, input_id, logical_time
				) VALUES (
					'00000000-0000-4000-8000-000000000122',
					'runtime-authority-orphan-repair',
					'00000000-0000-4000-8000-000000000123',
					1785456000000000000
				)`,
			artifactQuery: `
				SELECT count(*) = 2
				FROM ledger.entries AS entry
				LEFT JOIN ledger.transactions AS transaction_row
				  ON transaction_row.transaction_id = entry.transaction_id
				WHERE transaction_row.transaction_id IS NULL`,
		},
		{
			name: "unbalanced transaction currency",
			install: `
				INSERT INTO ledger.transactions (
					transaction_id, business_key, input_id, logical_time
				) VALUES (
					'00000000-0000-4000-8000-000000000124',
					'runtime-authority-unbalanced',
					'00000000-0000-4000-8000-000000000125',
					1785456000000000000
				);
				ALTER TABLE ledger.entries
				DISABLE TRIGGER ledger_transaction_must_balance;
				INSERT INTO ledger.entries (
					entry_id, transaction_id, account_id, currency, amount
				) VALUES
					(
						'00000000-0000-4000-8000-000000000126',
						'00000000-0000-4000-8000-000000000124',
						'account-runtime-authority-unbalanced', 'USDC', 11
					),
					(
						'00000000-0000-4000-8000-000000000127',
						'00000000-0000-4000-8000-000000000124',
						'system:clearing', 'USDC', -10
					);
				ALTER TABLE ledger.entries
				ENABLE TRIGGER ledger_transaction_must_balance`,
			repair: `
				INSERT INTO ledger.entries (
					entry_id, transaction_id, account_id, currency, amount
				) VALUES (
					'00000000-0000-4000-8000-000000000128',
					'00000000-0000-4000-8000-000000000124',
					'system:clearing', 'USDC', -1
				)`,
			artifactQuery: `
				SELECT count(*) = 1
				FROM (
					SELECT transaction_id, currency
					FROM ledger.entries
					GROUP BY transaction_id, currency
					HAVING pg_catalog.sum(amount) <> 0::numeric
				) AS unbalanced`,
		},
		{
			name: "empty transaction",
			install: `
				INSERT INTO ledger.transactions (
					transaction_id, business_key, input_id, logical_time
				) VALUES (
					'00000000-0000-4000-8000-000000000129',
					'runtime-authority-empty-transaction',
					'00000000-0000-4000-8000-000000000130',
					1785456000000000000
				)`,
			repair: `
				INSERT INTO ledger.entries (
					entry_id, transaction_id, account_id, currency, amount
				) VALUES
					(
						'00000000-0000-4000-8000-000000000131',
						'00000000-0000-4000-8000-000000000129',
						'account-runtime-authority-empty', 'USDC', 1
					),
					(
						'00000000-0000-4000-8000-000000000132',
						'00000000-0000-4000-8000-000000000129',
						'system:clearing', 'USDC', -1
					)`,
			artifactQuery: `
				SELECT count(*) = 1
				FROM ledger.transactions AS transaction_row
				WHERE NOT EXISTS (
					SELECT 1 FROM ledger.entries AS entry
					WHERE entry.transaction_id = transaction_row.transaction_id
				)`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatalf("apply tip 42: %v", err)
			}
			if _, err := pool.Exec(ctx, test.install); err != nil {
				t.Fatalf("install latent ledger corruption: %v", err)
			}

			before := runtimeAuthorityCutoverDigest(t, pool)
			err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLMigration),
			).Migrate(ctx)
			if !adminBootstrapIsPostgresCode(err, "55000") {
				t.Fatalf("latent-corruption migration error = %v, want 55000", err)
			}
			if after := runtimeAuthorityCutoverDigest(t, pool); after != before {
				t.Fatalf("rejected latent corruption changed evidence: before %s after %s", before, after)
			}
			assertMigrationHistoryTip(t, pool, 42, runtimeAuthorityACLPreviousMigration)
			var artifactPreserved bool
			if err := pool.QueryRow(ctx, test.artifactQuery).Scan(&artifactPreserved); err != nil {
				t.Fatalf("inspect latent ledger evidence: %v", err)
			}
			if !artifactPreserved {
				t.Fatal("rejected migration did not preserve latent ledger evidence")
			}

			if _, err := pool.Exec(ctx, test.repair); err != nil {
				t.Fatalf("repair latent ledger fixture: %v", err)
			}
			files := migrationFilesThrough(t, runtimeAuthorityACLMigration)
			current := platformpostgres.NewMigrator(pool, files)
			if err := current.Migrate(ctx); err != nil {
				t.Fatalf("retry migration after latent ledger repair: %v", err)
			}
			if err := current.VerifyCurrent(ctx); err != nil {
				t.Fatalf("verify latent ledger repair retry: %v", err)
			}
			assertMigrationHistoryTip(t, pool, 43, runtimeAuthorityACLMigration)
		})
	}
}

func TestRuntimeAuthorityACLMigrationFencesConcurrentCatalogDDL(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	hostileRole := fmt.Sprintf("runtime_authority_concurrent_%d", os.Getpid())
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create concurrent catalog role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
			DROP OWNED BY %[1]s CASCADE;
			DROP ROLE %[1]s`, hostileID))
	})

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin relation blocker: %v", err)
	}
	if _, err := blocker.Exec(ctx, "LOCK TABLE ledger.entries IN ACCESS EXCLUSIVE MODE"); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatalf("block later migration relation: %v", err)
	}
	migrationResult := make(chan error, 1)
	go func() {
		migrationResult <- platformpostgres.NewMigrator(
			pool,
			migrationFilesThrough(t, runtimeAuthorityACLMigration),
		).Migrate(ctx)
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := waitForRuntimeAuthorityCatalogFence(waitCtx, pool); err != nil {
		_ = blocker.Rollback(ctx)
		<-migrationResult
		t.Fatal(err)
	}

	var internalTriggerName string
	if err := pool.QueryRow(ctx, `
		SELECT tgname
		  FROM pg_catalog.pg_trigger
		 WHERE tgrelid = 'ledger.entries'::pg_catalog.regclass
		   AND tgisinternal
		   AND tgtype = 5
		 ORDER BY tgname
		 LIMIT 1`,
	).Scan(&internalTriggerName); err != nil {
		_ = blocker.Rollback(ctx)
		<-migrationResult
		t.Fatalf("select concurrent internal trigger: %v", err)
	}
	for _, statement := range []string{
		fmt.Sprintf(
			"GRANT SELECT ON ledger.transactions TO %s",
			hostileID,
		),
		"ALTER DEFAULT PRIVILEGES IN SCHEMA ledger " +
			"GRANT TRUNCATE ON TABLES TO platformgo_engine",
		"ALTER FUNCTION engine.reject_immutable_change() COST 101",
		"ALTER ROLE platformgo_engine BYPASSRLS",
		"GRANT platformgo_outbox TO platformgo_engine",
		"CREATE RULE runtime_authority_concurrent_rule AS " +
			"ON INSERT TO ledger.transactions DO INSTEAD NOTHING",
		"ALTER TABLE ledger.transactions ALTER COLUMN created_at " +
			"SET DEFAULT pg_catalog.statement_timestamp()",
		"ALTER TABLE ledger.transactions ADD CONSTRAINT " +
			"runtime_authority_concurrent_check CHECK (logical_time > 0) NOT VALID",
		"ALTER TABLE ledger.transactions ENABLE ROW LEVEL SECURITY",
		"CREATE INDEX runtime_authority_concurrent_index " +
			"ON ledger.transactions (logical_time)",
		"ALTER TABLE ledger.entries DISABLE TRIGGER " +
			pgx.Identifier{internalTriggerName}.Sanitize(),
		"UPDATE pg_catalog.pg_attrdef SET adbin = adbin " +
			"WHERE adrelid = 'ledger.transactions'::pg_catalog.regclass " +
			"AND adnum = (SELECT attnum FROM pg_catalog.pg_attribute " +
			"WHERE attrelid = 'ledger.transactions'::pg_catalog.regclass " +
			"AND attname = 'created_at')",
		"UPDATE pg_catalog.pg_attribute SET atthasdef = atthasdef " +
			"WHERE attrelid = 'ledger.transactions'::pg_catalog.regclass " +
			"AND attname = 'created_at'",
	} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			_ = blocker.Rollback(ctx)
			<-migrationResult
			t.Fatalf("begin concurrent catalog DDL: %v", err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '250ms'"); err != nil {
			_ = tx.Rollback(ctx)
			_ = blocker.Rollback(ctx)
			<-migrationResult
			t.Fatalf("set concurrent catalog timeout: %v", err)
		}
		_, statementErr := tx.Exec(ctx, statement)
		_ = tx.Rollback(ctx)
		var postgresError *pgconn.PgError
		if !errors.As(statementErr, &postgresError) || postgresError.Code != "55P03" {
			_ = blocker.Rollback(ctx)
			<-migrationResult
			t.Fatalf("concurrent statement %q error = %v, want SQLSTATE 55P03", statement, statementErr)
		}
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release migration relation blocker: %v", err)
	}
	if err := <-migrationResult; err != nil {
		t.Fatalf("catalog-fenced migration: %v", err)
	}
	var hostileCanRead bool
	if err := pool.QueryRow(ctx, `
		SELECT has_table_privilege($1, 'ledger.transactions', 'SELECT')`,
		hostileRole,
	).Scan(&hostileCanRead); err != nil {
		t.Fatalf("inspect concurrent grant result: %v", err)
	}
	if hostileCanRead {
		t.Fatal("concurrent catalog grant crossed the migration fence")
	}
}

func TestRuntimeAuthorityACLMigrationRejectsConcurrentRoleAuthorityDrift(
	t *testing.T,
) {
	for _, drift := range []string{"attribute", "membership"} {
		t.Run(drift, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatalf("apply current-main schema: %v", err)
			}
			parentRole := fmt.Sprintf("runtime_authority_parent_%d", os.Getpid())
			parentID := pgx.Identifier{parentRole}.Sanitize()
			switch drift {
			case "attribute":
				if _, err := pool.Exec(ctx, "ALTER ROLE platformgo_engine SUPERUSER"); err != nil {
					t.Fatalf("install unsafe runtime attribute: %v", err)
				}
				t.Cleanup(func() {
					_, _ = pool.Exec(context.Background(), "ALTER ROLE platformgo_engine NOSUPERUSER")
				})
			case "membership":
				if _, err := pool.Exec(ctx, fmt.Sprintf(`
					CREATE ROLE %[1]s NOLOGIN;
					GRANT %[1]s TO platformgo_engine`, parentID)); err != nil {
					t.Fatalf("install unsafe runtime membership: %v", err)
				}
				t.Cleanup(func() {
					_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
						REVOKE %[1]s FROM platformgo_engine;
						DROP ROLE %[1]s`, parentID))
				})
			}

			err := newCurrentTestMigrator(
				t,
				pool,
				migrationFilesThrough(t, runtimeAuthorityACLMigration),
			).Migrate(ctx)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
				t.Fatalf("runtime role drift migration error = %v, want SQLSTATE 42501", err)
			}
			var applied bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM engine.schema_migrations WHERE filename = $1
				)`, runtimeAuthorityACLMigration).Scan(&applied); err != nil {
				t.Fatalf("inspect runtime role drift history: %v", err)
			}
			if applied {
				t.Fatal("runtime role drift migration was journaled")
			}
		})
	}
}

func waitForRuntimeAuthorityCatalogFence(
	ctx context.Context,
	pool interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var fenced bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_locks AS waiting
				 WHERE waiting.locktype = 'relation'
				   AND waiting.relation = 'ledger.entries'::regclass
				   AND NOT waiting.granted
				   AND EXISTS (
					SELECT 1
					  FROM pg_catalog.pg_locks AS held
					 WHERE held.pid = waiting.pid
					   AND held.locktype = 'relation'
					   AND held.relation IN (
						'pg_catalog.pg_class'::regclass,
						'pg_catalog.pg_proc'::regclass,
						'pg_catalog.pg_authid'::regclass,
						'pg_catalog.pg_auth_members'::regclass
					   )
					   AND held.granted
				   )
			)`).Scan(&fenced); err != nil {
			return fmt.Errorf("inspect runtime authority catalog fence: %w", err)
		}
		if fenced {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for runtime authority catalog fence: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
