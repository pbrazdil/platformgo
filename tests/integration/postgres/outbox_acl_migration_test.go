package postgres_test

import (
	"bytes"
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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	outboxACLPreviousMigration = "20260729000200_phase3_admin_risk_monitor_acl.up.sql"
	outboxACLMigration         = "20260729000300_phase3_outbox_acl.up.sql"
)

func TestOutboxACLUpgradesPopulatedCurrentMain(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertOutboxACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, outboxACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedOutboxACLState(t, pool)
	before := readOutboxACLState(t, pool)
	assertOutboxACLPreviousCatalog(t, before)

	var beforeCount int
	var beforeTip string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(filename)
		  FROM engine.schema_migrations`,
	).Scan(&beforeCount, &beforeTip); err != nil {
		t.Fatalf("read current-main migration history: %v", err)
	}
	if beforeCount != 32 || beforeTip != outboxACLPreviousMigration {
		t.Fatalf(
			"current-main history = count %d tip %q, want 32/%q",
			beforeCount,
			beforeTip,
			outboxACLPreviousMigration,
		)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, outboxACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade durable outbox ACL: %v", err)
	}
	if after := readOutboxACLState(t, pool); after != before {
		t.Fatalf(
			"ACL-only upgrade changed outbox or neighboring catalog state:\nbefore=%+v\nafter=%+v",
			before,
			after,
		)
	}

	assertOutboxACLHistory(t, pool, 33, outboxACLMigration)
	assertOutboxACLChecksum(t, pool)
	assertOutboxACLRawAllowlist(t, pool)
	assertOutboxACLRuntimePrivileges(t, pool)
	assertOutboxACLRuntimeUsability(t, pool)
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify current outbox ACL history: %v", err)
	}
	previous := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, outboxACLPreviousMigration),
	)
	if err := previous.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous binary verification = %v, want schema-ahead", err)
	}

	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("idempotent outbox ACL migration rerun: %v", err)
	}
	assertOutboxACLHistory(t, pool, 33, outboxACLMigration)
	assertOutboxACLChecksum(t, pool)
	assertOutboxACLRawAllowlist(t, pool)
	if afterRerun := readOutboxACLState(t, pool); afterRerun != before {
		t.Fatalf(
			"idempotent rerun changed outbox or neighboring catalog state:\nbefore=%+v\nafter=%+v",
			before,
			afterRerun,
		)
	}
}

func TestOutboxACLScrubsHostileDefaultsAndGrantChains(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertOutboxACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("outbox_hostile_%d", os.Getpid())
	dependentRole := "platformgo_projector"
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	dependentID := pgx.Identifier{dependentRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile role: %v", err)
	}
	cleaned := false
	t.Cleanup(func() {
		if cleaned {
			return
		}
		if err := cleanupOutboxACLHostileRole(
			context.Background(),
			pool,
			ownerID,
			hostileID,
		); err != nil {
			t.Errorf("cleanup hostile outbox role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA messaging;
		GRANT USAGE ON SCHEMA messaging TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA messaging
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s WITH GRANT OPTION`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile outbox owner defaults: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, outboxACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema under hostile defaults: %v", err)
	}
	seedOutboxACLState(t, pool)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT, DELETE ON messaging.outbox TO PUBLIC;
		GRANT INSERT (subject), UPDATE (subject)
			ON messaging.outbox TO PUBLIC;
		GRANT SELECT, DELETE ON messaging.outbox
			TO %[1]s WITH GRANT OPTION;
		GRANT INSERT (subject), UPDATE (subject)
			ON messaging.outbox TO %[1]s WITH GRANT OPTION`,
		hostileID,
	)); err != nil {
		t.Fatalf("install direct hostile outbox ACLs: %v", err)
	}
	grantTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin dependent outbox grant chain: %v", err)
	}
	if _, err := grantTx.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("assume hostile outbox grantor: %v", err)
	}
	if _, err := grantTx.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT, DELETE ON messaging.outbox TO %[1]s;
		GRANT INSERT (subject), UPDATE (subject)
			ON messaging.outbox TO %[1]s`,
		dependentID,
	)); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("install dependent outbox grants: %v", err)
	}
	if err := grantTx.Commit(ctx); err != nil {
		t.Fatalf("commit dependent outbox grant chain: %v", err)
	}

	assertOutboxACLRoleStatementAllowed(
		t,
		pool,
		hostileRole,
		`UPDATE messaging.outbox
		    SET subject = 'ops.v1.outbox.hostile-direct'
		  WHERE message_id = '019fab10-0000-4000-8000-000000000001'`,
	)
	assertOutboxACLRoleStatementAllowed(
		t,
		pool,
		dependentRole,
		`UPDATE messaging.outbox
		    SET subject = 'ops.v1.outbox.hostile-dependent'
		  WHERE message_id = '019fab10-0000-4000-8000-000000000001'`,
	)
	if got := readOutboxACLUnexpectedACL(t, pool); len(got) <= len(outboxACLAllowlist()) {
		t.Fatalf("hostile fixture did not expand outbox ACL: %v", got)
	}
	before := readOutboxACLState(t, pool)
	assertOutboxACLPreviousCatalog(t, before)

	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, outboxACLMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply hostile outbox ACL scrub: %v", err)
	}
	if after := readOutboxACLState(t, pool); after != before {
		t.Fatalf(
			"hostile ACL scrub changed rows/catalog/defaults/inbox state:\nbefore=%+v\nafter=%+v",
			before,
			after,
		)
	}
	assertOutboxACLHistory(t, pool, 33, outboxACLMigration)
	assertOutboxACLRawAllowlist(t, pool)
	assertOutboxACLRuntimePrivileges(t, pool)
	assertOutboxACLRuntimeUsability(t, pool)
	for _, role := range []string{hostileRole, dependentRole} {
		assertOutboxACLRoleHasNoPrivileges(t, pool, role)
		assertOutboxACLRoleAllOperationsDenied(t, pool, role)
	}

	if err := cleanupOutboxACLHostileRole(
		ctx,
		pool,
		ownerID,
		hostileID,
	); err != nil {
		t.Fatalf("cleanup hostile outbox role: %v", err)
	}
	cleaned = true
}

func TestOutboxACLPreRevocationWriterRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertOutboxACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, outboxACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedOutboxACLState(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT SELECT (message_id), UPDATE (subject)
			ON messaging.outbox TO platformgo_projector;
		GRANT DELETE ON messaging.outbox TO PUBLIC`,
	); err != nil {
		t.Fatalf("grant pre-revocation outbox writer: %v", err)
	}
	before := readOutboxACLState(t, pool)
	beforeACL := readOutboxACLRawState(t, pool)

	writerTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin pre-revocation outbox writer: %v", err)
	}
	if _, err := writerTx.Exec(ctx, `
		SET LOCAL ROLE platformgo_projector;
		UPDATE messaging.outbox
		   SET subject = 'ops.v1.outbox.uncommitted-writer'
		 WHERE message_id = '019fab10-0000-4000-8000-000000000001'`); err != nil {
		_ = writerTx.Rollback(ctx)
		t.Fatalf("execute pre-revocation outbox update: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, outboxACLMigration),
	)
	err = current.Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		_ = writerTx.Rollback(ctx)
		t.Fatalf(
			"pre-revocation writer migration error = %v, want SQLSTATE 55P03",
			err,
		)
	}
	var journaled bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM engine.schema_migrations
			 WHERE filename = $1
		)`,
		outboxACLMigration,
	).Scan(&journaled); err != nil {
		_ = writerTx.Rollback(ctx)
		t.Fatalf("inspect failed outbox ACL migration journal: %v", err)
	}
	if journaled {
		_ = writerTx.Rollback(ctx)
		t.Fatal("pre-revocation writer failure journaled outbox ACL migration")
	}
	assertOutboxACLHistory(t, pool, 32, outboxACLPreviousMigration)
	if afterACL := readOutboxACLRawState(t, pool); afterACL != beforeACL {
		_ = writerTx.Rollback(ctx)
		t.Fatal("pre-revocation writer failure changed outbox ACL")
	}
	if after := readOutboxACLState(t, pool); after != before {
		_ = writerTx.Rollback(ctx)
		t.Fatalf(
			"pre-revocation writer failure changed committed state:\nbefore=%+v\nafter=%+v",
			before,
			after,
		)
	}

	if err := writerTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback pre-revocation outbox writer: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry after pre-revocation outbox writer drain: %v", err)
	}
	if after := readOutboxACLState(t, pool); after != before {
		t.Fatalf(
			"successful writer-fenced retry changed outbox state:\nbefore=%+v\nafter=%+v",
			before,
			after,
		)
	}
	assertOutboxACLHistory(t, pool, 33, outboxACLMigration)
	assertOutboxACLChecksum(t, pool)
	assertOutboxACLRawAllowlist(t, pool)
	assertOutboxACLRuntimePrivileges(t, pool)
	assertOutboxACLRuntimeUsability(t, pool)
}

type outboxACLState struct {
	RowDigest       [sha256.Size]byte
	FileNode        uint32
	Indexes         string
	Constraints     string
	TriggerCount    int
	TriggerNames    string
	Triggers        string
	TriggerFunction string
	Defaults        string
	InboxACL        string
	OwnerDefaults   string
}

func assertOutboxACLPostgres19Beta2(t *testing.T, pool *pgxpool.Pool) {
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

func seedOutboxACLState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO messaging.outbox (
			message_id,
			subject,
			schema_version,
			payload,
			producer_class,
			engine_shard_id,
			engine_input_id,
			attempts,
			next_attempt_at,
			claimed_at,
			published_at,
			publish_sequence,
			last_error,
			created_at
		) VALUES
		(
			'019fab10-0000-4000-8000-000000000001',
			'ops.v1.outbox.pending',
			1,
			'{"kind":"acl-proof","marketSequence":0}',
			'api',
			NULL,
			NULL,
			2,
			'2026-07-29T12:01:00Z',
			'2026-07-29T12:00:30Z',
			NULL,
			NULL,
			'transient publish failure',
			'2026-07-29T12:00:00Z'
		),
		(
			'019fab10-0000-4000-8000-000000000002',
			'ops.v1.outbox.published',
			2,
			'{"kind":"acl-proof-published","marketSequence":0}',
			'api',
			NULL,
			NULL,
			1,
			'2026-07-29T12:02:00Z',
			'2026-07-29T12:01:30Z',
			'2026-07-29T12:01:45Z',
			41,
			NULL,
			'2026-07-29T12:01:00Z'
		)`,
	); err != nil {
		t.Fatalf("seed populated outbox ACL state: %v", err)
	}
}

func readOutboxACLState(t *testing.T, pool *pgxpool.Pool) outboxACLState {
	t.Helper()
	ctx := context.Background()
	var state outboxACLState
	var canonical string
	if err := pool.QueryRow(ctx, `
		SELECT
			COALESCE(
				jsonb_agg(
					jsonb_build_array(
						message_id::text,
						subject,
						schema_version,
						payload,
						producer_class,
						engine_shard_id,
						engine_input_id::text,
						attempts,
						next_attempt_at,
						claimed_at,
						published_at,
						publish_sequence,
						last_error,
						created_at
					)
					ORDER BY message_id
				),
				'[]'::jsonb
			)::text,
			pg_relation_filenode('messaging.outbox'::regclass)
		  FROM messaging.outbox`,
	).Scan(&canonical, &state.FileNode); err != nil {
		t.Fatalf("read explicit-column outbox digest and relation file: %v", err)
	}
	state.RowDigest = sha256.Sum256([]byte(canonical))

	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			string_agg(
				pg_catalog.format(
					'%s|unique=%s|primary=%s|valid=%s|ready=%s|%s',
					index_relation.relname,
					index.indisunique,
					index.indisprimary,
					index.indisvalid,
					index.indisready,
					pg_catalog.pg_get_indexdef(index.indexrelid)
				),
				E'\n'
				ORDER BY index_relation.relname
			),
			''
		)
		  FROM pg_catalog.pg_index AS index
		  JOIN pg_catalog.pg_class AS index_relation
		    ON index_relation.oid = index.indexrelid
		 WHERE index.indrelid = 'messaging.outbox'::regclass`,
	).Scan(&state.Indexes); err != nil {
		t.Fatalf("read outbox index catalog: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			string_agg(
				pg_catalog.format(
					'%s|type=%s|deferrable=%s|deferred=%s|validated=%s|%s',
					catalog_constraint.conname,
					catalog_constraint.contype,
					catalog_constraint.condeferrable,
					catalog_constraint.condeferred,
					catalog_constraint.convalidated,
					pg_catalog.pg_get_constraintdef(catalog_constraint.oid, true)
				),
				E'\n'
				ORDER BY catalog_constraint.conname
			),
			''
		)
		  FROM pg_catalog.pg_constraint AS catalog_constraint
		 WHERE catalog_constraint.conrelid = 'messaging.outbox'::regclass`,
	).Scan(&state.Constraints); err != nil {
		t.Fatalf("read outbox constraint catalog: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*)::integer,
			COALESCE(string_agg(trigger.tgname, E'\n' ORDER BY trigger.tgname), ''),
			COALESCE(
				string_agg(
					pg_catalog.format(
						'%s|enabled=%s|type=%s|deferrable=%s|deferred=%s|function=%s|%s',
						trigger.tgname,
						trigger.tgenabled,
						trigger.tgtype,
						trigger.tgdeferrable,
						trigger.tginitdeferred,
						trigger.tgfoid::regprocedure,
						pg_catalog.pg_get_triggerdef(trigger.oid, true)
					),
					E'\n'
					ORDER BY trigger.tgname
				),
				''
			)
		  FROM pg_catalog.pg_trigger AS trigger
		 WHERE trigger.tgrelid = 'messaging.outbox'::regclass
		   AND NOT trigger.tgisinternal`,
	).Scan(
		&state.TriggerCount,
		&state.TriggerNames,
		&state.Triggers,
	); err != nil {
		t.Fatalf("read outbox trigger catalog: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT pg_catalog.format(
			'owner=%s|language=%s|volatile=%s|strict=%s|security_definer=%s|parallel=%s|config=%s|result=%s|args=%s|%s',
			owner.rolname,
			language.lanname,
			procedure.provolatile,
			procedure.proisstrict,
			procedure.prosecdef,
			procedure.proparallel,
			COALESCE(procedure.proconfig::text, '<NULL>'),
			procedure.prorettype::regtype,
			pg_catalog.pg_get_function_identity_arguments(procedure.oid),
			pg_catalog.pg_get_functiondef(procedure.oid)
		)
		  FROM pg_catalog.pg_proc AS procedure
		  JOIN pg_catalog.pg_roles AS owner
		    ON owner.oid = procedure.proowner
		  JOIN pg_catalog.pg_language AS language
		    ON language.oid = procedure.prolang
		 WHERE procedure.oid =
		       'messaging.require_outbox_command_market_sequence_binding()'::regprocedure`,
	).Scan(&state.TriggerFunction); err != nil {
		t.Fatalf("read outbox trigger function catalog: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			string_agg(
				pg_catalog.format(
					'%s=%s',
					attribute.attname,
					CASE
						WHEN default_value.oid IS NULL THEN '<NULL>'
						ELSE pg_catalog.pg_get_expr(
							default_value.adbin,
							default_value.adrelid
						)
					END
				),
				E'\n'
				ORDER BY attribute.attnum
			),
			''
		)
		  FROM pg_catalog.pg_attribute AS attribute
		  LEFT JOIN pg_catalog.pg_attrdef AS default_value
		    ON default_value.adrelid = attribute.attrelid
		   AND default_value.adnum = attribute.attnum
		 WHERE attribute.attrelid = 'messaging.outbox'::regclass
		   AND attribute.attnum > 0
		   AND NOT attribute.attisdropped`,
	).Scan(&state.Defaults); err != nil {
		t.Fatalf("read outbox column defaults: %v", err)
	}
	state.InboxACL = readOutboxACLRelationRawACL(t, pool, "messaging.inbox")
	state.OwnerDefaults = readOutboxACLOwnerDefaults(t, pool)
	return state
}

func assertOutboxACLPreviousCatalog(t *testing.T, state outboxACLState) {
	t.Helper()
	wantTriggerNames := strings.Join([]string{
		"outbox_insert_requires_command_market_sequence_binding",
		"outbox_update_requires_command_market_sequence_binding",
	}, "\n")
	if state.TriggerCount != 2 || state.TriggerNames != wantTriggerNames {
		t.Fatalf(
			"current-main outbox triggers = count %d names %q, want 2/%q",
			state.TriggerCount,
			state.TriggerNames,
			wantTriggerNames,
		)
	}
	for _, index := range []string{"outbox_pending_idx", "outbox_pkey"} {
		if !strings.Contains(state.Indexes, index+"|") {
			t.Fatalf("current-main outbox indexes missing %s: %s", index, state.Indexes)
		}
	}
	if state.Constraints == "" ||
		state.TriggerFunction == "" ||
		state.Defaults == "" ||
		state.InboxACL == "" {
		t.Fatalf("current-main outbox preservation catalog is incomplete: %+v", state)
	}
}

func assertOutboxACLHistory(
	t *testing.T,
	pool *pgxpool.Pool,
	wantCount int,
	wantTip string,
) {
	t.Helper()
	var count int
	var tip string
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*), max(filename)
		  FROM engine.schema_migrations`,
	).Scan(&count, &tip); err != nil {
		t.Fatalf("read outbox ACL migration history: %v", err)
	}
	if count != wantCount || tip != wantTip {
		t.Fatalf(
			"outbox ACL history = count %d tip %q, want %d/%q",
			count,
			tip,
			wantCount,
			wantTip,
		)
	}
}

func assertOutboxACLChecksum(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"..",
		"migrations",
		outboxACLMigration,
	))
	if err != nil {
		t.Fatalf("read outbox ACL migration: %v", err)
	}
	want := sha256.Sum256(raw)
	var got []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT checksum
		  FROM engine.schema_migrations
		 WHERE filename = $1`,
		outboxACLMigration,
	).Scan(&got); err != nil {
		t.Fatalf("read outbox ACL migration checksum: %v", err)
	}
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("outbox ACL checksum = %x, want %x", got, want)
	}
}

func readOutboxACLRelationRawACL(
	t *testing.T,
	pool *pgxpool.Pool,
	relation string,
) string {
	t.Helper()
	var tableACL, columnACL string
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COALESCE(relation.relacl::text, '<NULL>'),
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
					 WHERE attribute.attrelid = relation.oid
					   AND attribute.attnum > 0
					   AND NOT attribute.attisdropped
				),
				''
			)
		  FROM pg_catalog.pg_class AS relation
		 WHERE relation.oid = $1::regclass`,
		relation,
	).Scan(&tableACL, &columnACL); err != nil {
		t.Fatalf("read raw %s ACL: %v", relation, err)
	}
	return tableACL + "\n" + columnACL
}

func readOutboxACLRawState(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	return readOutboxACLRelationRawACL(t, pool, "messaging.outbox")
}

func readOutboxACLOwnerDefaults(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var defaults string
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(
			string_agg(
				pg_catalog.format(
					'%s|%s|%s',
					namespace.nspname,
					default_acl.defaclobjtype,
					default_acl.defaclacl::text
				),
				E'\n'
				ORDER BY namespace.nspname, default_acl.defaclobjtype
			),
			'<NONE>'
		)
		  FROM pg_catalog.pg_default_acl AS default_acl
		  JOIN pg_catalog.pg_namespace AS namespace
		    ON namespace.oid = default_acl.defaclnamespace
		 WHERE default_acl.defaclrole = (
			SELECT usesysid
			  FROM pg_catalog.pg_user
			 WHERE usename = current_user
		 )
		   AND namespace.nspname = 'messaging'`,
	).Scan(&defaults); err != nil {
		t.Fatalf("read messaging owner default privileges: %v", err)
	}
	return defaults
}

func readOutboxACLUnexpectedACL(
	t *testing.T,
	pool *pgxpool.Pool,
) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		WITH relation AS (
			SELECT oid, relowner, relacl
			  FROM pg_catalog.pg_class
			 WHERE oid = 'messaging.outbox'::regclass
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
			SELECT grantee, privilege_type, is_grantable, scope, relowner
			  FROM table_acl
			UNION ALL
			SELECT grantee, privilege_type, is_grantable, scope, relowner
			  FROM column_acl
		  ) AS acl
		 WHERE acl.grantee IS DISTINCT FROM (
			SELECT rolname FROM pg_catalog.pg_roles WHERE oid = acl.relowner
		 )`)
	if err != nil {
		t.Fatalf("inspect complete outbox ACL: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var grantee, privilege, scope string
		var grantable bool
		if err := rows.Scan(&grantee, &privilege, &grantable, &scope); err != nil {
			t.Fatalf("scan complete outbox ACL: %v", err)
		}
		got = append(
			got,
			fmt.Sprintf("%s|%s|%t|%s", grantee, privilege, grantable, scope),
		)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate complete outbox ACL: %v", err)
	}
	sort.Strings(got)
	return got
}

func outboxACLAllowlist() []string {
	want := []string{
		"platformgo_api|INSERT|false|column:message_id",
		"platformgo_api|INSERT|false|column:payload",
		"platformgo_api|INSERT|false|column:schema_version",
		"platformgo_api|INSERT|false|column:subject",
		"platformgo_api|SELECT|false|table",
		"platformgo_engine|INSERT|false|table",
		"platformgo_engine|SELECT|false|table",
		"platformgo_outbox|SELECT|false|table",
		"platformgo_outbox|UPDATE|false|column:attempts",
		"platformgo_outbox|UPDATE|false|column:claimed_at",
		"platformgo_outbox|UPDATE|false|column:last_error",
		"platformgo_outbox|UPDATE|false|column:next_attempt_at",
		"platformgo_outbox|UPDATE|false|column:publish_sequence",
		"platformgo_outbox|UPDATE|false|column:published_at",
	}
	sort.Strings(want)
	return want
}

func assertOutboxACLRawAllowlist(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	got := readOutboxACLUnexpectedACL(t, pool)
	want := outboxACLAllowlist()
	if !slices.Equal(got, want) {
		t.Fatalf("outbox raw ACL = %v, want exact %v", got, want)
	}
}

func assertOutboxACLRuntimePrivileges(t *testing.T, pool *pgxpool.Pool) {
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
		"SELECT",
		"INSERT",
		"UPDATE",
		"DELETE",
		"TRUNCATE",
		"REFERENCES",
		"TRIGGER",
		"MAINTAIN",
	}
	for _, role := range runtimeRoles {
		for _, privilege := range tablePrivileges {
			want := (role == "platformgo_api" && privilege == "SELECT") ||
				(role == "platformgo_engine" &&
					(privilege == "SELECT" || privilege == "INSERT")) ||
				(role == "platformgo_outbox" && privilege == "SELECT")
			var got bool
			if err := pool.QueryRow(context.Background(), `
				SELECT has_table_privilege(
					$1,
					'messaging.outbox',
					$2
				)`,
				role,
				privilege,
			).Scan(&got); err != nil {
				t.Fatalf(
					"inspect %s outbox table %s: %v",
					role,
					privilege,
					err,
				)
			}
			if got != want {
				t.Fatalf(
					"%s outbox table %s = %t, want %t",
					role,
					privilege,
					got,
					want,
				)
			}
		}
	}

	rows, err := pool.Query(context.Background(), `
		SELECT attribute.attname
		  FROM pg_catalog.pg_attribute AS attribute
		 WHERE attribute.attrelid = 'messaging.outbox'::regclass
		   AND attribute.attnum > 0
		   AND NOT attribute.attisdropped
		 ORDER BY attribute.attnum`)
	if err != nil {
		t.Fatalf("read outbox columns for privilege matrix: %v", err)
	}
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			rows.Close()
			t.Fatalf("scan outbox privilege column: %v", err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate outbox privilege columns: %v", err)
	}
	rows.Close()

	apiInsert := map[string]bool{
		"message_id":     true,
		"subject":        true,
		"schema_version": true,
		"payload":        true,
	}
	outboxUpdate := map[string]bool{
		"attempts":         true,
		"next_attempt_at":  true,
		"claimed_at":       true,
		"published_at":     true,
		"publish_sequence": true,
		"last_error":       true,
	}
	for _, role := range runtimeRoles {
		for _, column := range columns {
			for _, privilege := range []string{
				"SELECT",
				"INSERT",
				"UPDATE",
				"REFERENCES",
			} {
				want := false
				switch role {
				case "platformgo_api":
					want = privilege == "SELECT" ||
						(privilege == "INSERT" && apiInsert[column])
				case "platformgo_engine":
					want = privilege == "SELECT" || privilege == "INSERT"
				case "platformgo_outbox":
					want = privilege == "SELECT" ||
						(privilege == "UPDATE" && outboxUpdate[column])
				}
				var got bool
				if err := pool.QueryRow(context.Background(), `
					SELECT has_column_privilege(
						$1,
						'messaging.outbox',
						$2,
						$3
					)`,
					role,
					column,
					privilege,
				).Scan(&got); err != nil {
					t.Fatalf(
						"inspect %s outbox column %s %s: %v",
						role,
						column,
						privilege,
						err,
					)
				}
				if got != want {
					t.Fatalf(
						"%s outbox column %s %s = %t, want %t",
						role,
						column,
						privilege,
						got,
						want,
					)
				}
			}
		}
	}
}

func assertOutboxACLRuntimeUsability(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	assertOutboxACLRoleStatementAllowed(
		t,
		pool,
		"platformgo_api",
		"SELECT message_id, subject FROM messaging.outbox",
	)
	assertOutboxACLRoleStatementAllowed(
		t,
		pool,
		"platformgo_api",
		`INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			'019fab10-0000-4000-8000-000000000011',
			'ops.v1.outbox.api-allowed',
			1,
			'{"marketSequence":0}'
		)`,
	)
	assertOutboxACLRoleStatementAllowed(
		t,
		pool,
		"platformgo_engine",
		`INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			'019fab10-0000-4000-8000-000000000012',
			'ops.v1.outbox.engine-allowed',
			1,
			'{"marketSequence":0}'
		)`,
	)
	assertOutboxACLRoleStatementAllowed(
		t,
		pool,
		"platformgo_outbox",
		`UPDATE messaging.outbox
		    SET attempts = attempts + 1,
		        next_attempt_at = '2026-07-29T12:10:00Z',
		        claimed_at = '2026-07-29T12:09:00Z',
		        published_at = '2026-07-29T12:09:30Z',
		        publish_sequence = 42,
		        last_error = NULL
		  WHERE message_id = '019fab10-0000-4000-8000-000000000001'`,
	)

	denied := map[string][]string{
		"platformgo_api": {
			`INSERT INTO messaging.outbox (
				message_id, subject, schema_version, payload, producer_class
			) VALUES (
				'019fab10-0000-4000-8000-000000000021',
				'ops.v1.outbox.api-forbidden',
				1,
				'{"marketSequence":0}',
				'api'
			)`,
			"UPDATE messaging.outbox SET attempts = attempts",
			"DELETE FROM messaging.outbox",
			"TRUNCATE messaging.outbox",
		},
		"platformgo_engine": {
			"UPDATE messaging.outbox SET attempts = attempts",
			"DELETE FROM messaging.outbox",
			"TRUNCATE messaging.outbox",
		},
		"platformgo_outbox": {
			`INSERT INTO messaging.outbox (
				message_id, subject, schema_version, payload
			) VALUES (
				'019fab10-0000-4000-8000-000000000022',
				'ops.v1.outbox.worker-forbidden',
				1,
				'{"marketSequence":0}'
			)`,
			"UPDATE messaging.outbox SET subject = subject",
			"DELETE FROM messaging.outbox",
			"TRUNCATE messaging.outbox",
		},
	}
	for _, role := range []string{
		"platformgo_api",
		"platformgo_engine",
		"platformgo_outbox",
	} {
		for _, statement := range denied[role] {
			assertOutboxACLRoleStatementDenied(t, pool, role, statement)
		}
	}
}

func assertOutboxACLRoleHasNoPrivileges(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	for _, privilege := range []string{
		"SELECT",
		"INSERT",
		"UPDATE",
		"DELETE",
		"TRUNCATE",
		"REFERENCES",
		"TRIGGER",
		"MAINTAIN",
	} {
		var got bool
		if err := pool.QueryRow(context.Background(), `
			SELECT has_table_privilege($1, 'messaging.outbox', $2)`,
			role,
			privilege,
		).Scan(&got); err != nil {
			t.Fatalf("inspect hostile %s outbox %s: %v", role, privilege, err)
		}
		if got {
			t.Fatalf("hostile role %s retained outbox %s", role, privilege)
		}
	}
	for _, column := range []string{
		"message_id",
		"subject",
		"schema_version",
		"payload",
		"producer_class",
		"engine_shard_id",
		"engine_input_id",
		"attempts",
		"next_attempt_at",
		"claimed_at",
		"published_at",
		"publish_sequence",
		"last_error",
		"created_at",
	} {
		for _, privilege := range []string{
			"SELECT",
			"INSERT",
			"UPDATE",
			"REFERENCES",
		} {
			var got bool
			if err := pool.QueryRow(context.Background(), `
				SELECT has_column_privilege(
					$1,
					'messaging.outbox',
					$2,
					$3
				)`,
				role,
				column,
				privilege,
			).Scan(&got); err != nil {
				t.Fatalf(
					"inspect hostile %s outbox %s %s: %v",
					role,
					column,
					privilege,
					err,
				)
			}
			if got {
				t.Fatalf(
					"hostile role %s retained outbox column %s %s",
					role,
					column,
					privilege,
				)
			}
		}
	}
}

func assertOutboxACLRoleAllOperationsDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	for _, statement := range []string{
		"SELECT message_id FROM messaging.outbox",
		`INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			'019fab10-0000-4000-8000-000000000031',
			'ops.v1.outbox.hostile-forbidden',
			1,
			'{"marketSequence":0}'
		)`,
		"UPDATE messaging.outbox SET attempts = attempts",
		"UPDATE messaging.outbox SET subject = subject",
		"DELETE FROM messaging.outbox",
		"TRUNCATE messaging.outbox",
	} {
		assertOutboxACLRoleStatementDenied(t, pool, role, statement)
	}
}

func assertOutboxACLRoleStatementAllowed(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
	statement string,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin %s allowed outbox operation: %v", role, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
	); err != nil {
		t.Fatalf("assume outbox role %s: %v", role, err)
	}
	if _, err := tx.Exec(context.Background(), statement); err != nil {
		t.Fatalf("role %s statement %q should be allowed: %v", role, statement, err)
	}
}

func assertOutboxACLRoleStatementDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
	statement string,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin %s denied outbox operation: %v", role, err)
	}
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
	); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("assume outbox role %s: %v", role, err)
	}
	_, statementErr := tx.Exec(context.Background(), statement)
	_ = tx.Rollback(context.Background())
	var postgresError *pgconn.PgError
	if !errors.As(statementErr, &postgresError) ||
		postgresError.Code != "42501" {
		t.Fatalf(
			"role %s statement %q error = %v, want SQLSTATE 42501",
			role,
			statement,
			statementErr,
		)
	}
}

func cleanupOutboxACLHostileRole(
	ctx context.Context,
	pool *pgxpool.Pool,
	ownerIdentifier string,
	roleIdentifier string,
) error {
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		DO $cleanup$
		DECLARE
			column_name name;
		BEGIN
			IF pg_catalog.to_regclass('messaging.outbox') IS NOT NULL THEN
				EXECUTE
					'REVOKE ALL PRIVILEGES ON TABLE messaging.outbox FROM %[1]s CASCADE';
				FOR column_name IN
					SELECT attribute.attname
					  FROM pg_catalog.pg_attribute AS attribute
					 WHERE attribute.attrelid =
					       'messaging.outbox'::pg_catalog.regclass
					   AND attribute.attnum > 0
					   AND NOT attribute.attisdropped
				LOOP
					EXECUTE pg_catalog.format(
						'REVOKE ALL PRIVILEGES (%%I) ON TABLE messaging.outbox FROM %[1]s CASCADE',
						column_name
					);
				END LOOP;
			END IF;
		END
		$cleanup$`,
		roleIdentifier,
	)); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA messaging
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		REVOKE ALL PRIVILEGES ON SCHEMA messaging FROM %[2]s CASCADE;
		DROP OWNED BY %[2]s CASCADE`,
		ownerIdentifier,
		roleIdentifier,
	)); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, "DROP ROLE "+roleIdentifier)
	return err
}
