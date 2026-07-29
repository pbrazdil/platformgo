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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	commandAdmissionACLPreviousMigration = "20260729000300_phase3_outbox_acl.up.sql"
	commandAdmissionACLMigration         = "20260729000400_phase3_command_admission_acl.up.sql"
)

var commandAdmissionACLRelations = []string{
	"trading.commands",
	"trading.idempotency_records",
	"trading.command_replay_responses",
}

func TestCommandAdmissionACLUpgradesHostilePopulatedCurrentTip(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertCommandAdmissionACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("command_admission_hostile_%d", os.Getpid())
	dependentRole := "platformgo_projector"
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	dependentID := pgx.Identifier{dependentRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile role: %v", err)
	}
	t.Cleanup(func() {
		if err := cleanupCommandAdmissionACLHostileRole(
			context.Background(),
			pool,
			ownerID,
			hostileID,
		); err != nil {
			t.Errorf("cleanup hostile command-admission role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA trading;
		GRANT USAGE ON SCHEMA trading TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s WITH GRANT OPTION`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile trading defaults: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, commandAdmissionACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current 33-file schema under hostile defaults: %v", err)
	}
	seedCommandAdmissionACLState(t, pool)
	seedCommandAdmissionACLNeighborState(t, pool)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT USAGE ON SCHEMA trading TO %[2]s;
		GRANT SELECT ON
			trading.commands,
			trading.idempotency_records,
			trading.command_replay_responses
		TO PUBLIC;
		GRANT UPDATE (status) ON trading.commands
			TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (response_status) ON trading.idempotency_records
			TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (response_status) ON trading.command_replay_responses
			TO %[1]s WITH GRANT OPTION`,
		hostileID,
		dependentID,
	)); err != nil {
		t.Fatalf("install hostile command-admission column ACLs: %v", err)
	}
	delegation, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin dependent command-admission grants: %v", err)
	}
	if _, err := delegation.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		_ = delegation.Rollback(ctx)
		t.Fatalf("assume hostile command-admission grantor: %v", err)
	}
	if _, err := delegation.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON
			trading.commands,
			trading.idempotency_records,
			trading.command_replay_responses
		TO %[1]s;
		GRANT UPDATE (status) ON trading.commands TO %[1]s;
		GRANT UPDATE (response_status) ON trading.idempotency_records TO %[1]s;
		GRANT UPDATE (response_status) ON trading.command_replay_responses TO %[1]s`,
		dependentID,
	)); err != nil {
		_ = delegation.Rollback(ctx)
		t.Fatalf("delegate hostile command-admission grants: %v", err)
	}
	if err := delegation.Commit(ctx); err != nil {
		t.Fatalf("commit dependent command-admission grants: %v", err)
	}

	for _, relation := range commandAdmissionACLRelations {
		for _, privilege := range []string{"SELECT", "INSERT", "TRUNCATE"} {
			var allowed bool
			if err := pool.QueryRow(ctx, `
				SELECT has_table_privilege($1, $2, $3)`,
				hostileRole,
				relation,
				privilege,
			).Scan(&allowed); err != nil {
				t.Fatalf(
					"inspect hostile %s on %s: %v",
					privilege,
					relation,
					err,
				)
			}
			if !allowed {
				t.Fatalf(
					"hostile defaults did not reproduce %s on %s",
					privilege,
					relation,
				)
			}
		}
	}
	for relation, column := range map[string]string{
		"trading.commands":                 "status",
		"trading.idempotency_records":      "response_status",
		"trading.command_replay_responses": "response_status",
	} {
		var allowed bool
		if err := pool.QueryRow(ctx, `
			SELECT has_column_privilege($1, $2, $3, 'UPDATE')`,
			hostileRole,
			relation,
			column,
		).Scan(&allowed); err != nil {
			t.Fatalf("inspect hostile column ACL on %s: %v", relation, err)
		}
		if !allowed {
			t.Fatalf("hostile column UPDATE missing on %s.%s", relation, column)
		}
	}

	before := readCommandAdmissionACLState(t, pool)
	beforeCompanions := readCommandAdmissionACLCompanionState(t, pool)
	beforeControls := readCommandAdmissionACLControlState(t, pool, owner)
	files := migrationFilesThrough(t, commandAdmissionACLMigration)
	if _, exists := files[commandAdmissionACLMigration]; !exists {
		t.Fatalf(
			"RED: expected forward migration %s is missing after current tip %s",
			commandAdmissionACLMigration,
			commandAdmissionACLPreviousMigration,
		)
	}

	current := platformpostgres.NewMigrator(pool, files)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply command-admission ACL migration: %v", err)
	}
	if after := readCommandAdmissionACLState(t, pool); after != before {
		t.Fatalf(
			"ACL/guard migration changed command-admission rows or files:\nbefore=%+v\nafter=%+v",
			before,
			after,
		)
	}
	if after := readCommandAdmissionACLCompanionState(t, pool); after != beforeCompanions {
		t.Fatalf(
			"migration changed neighboring order-intent/outbox state:\nbefore=%+v\nafter=%+v",
			beforeCompanions,
			after,
		)
	}
	if after := readCommandAdmissionACLControlState(t, pool, owner); after != beforeControls {
		t.Fatalf(
			"migration changed owner defaults or role memberships:\nbefore=%+v\nafter=%+v",
			beforeControls,
			after,
		)
	}
	assertCommandAdmissionACLRawAllowlist(t, pool)
	assertCommandAdmissionACLRuntimeAllowlist(t, pool)
	assertCommandAdmissionACLTruncateGuards(t, pool)

	for _, relation := range commandAdmissionACLRelations {
		_, err := pool.Exec(ctx, "TRUNCATE "+relation+" CASCADE")
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
			t.Fatalf(
				"owner TRUNCATE %s error = %v, want SQLSTATE 55000",
				relation,
				err,
			)
		}
	}
	for _, role := range []string{hostileRole, dependentRole} {
		for _, relation := range commandAdmissionACLRelations {
			var canSelect, canInsert, canTruncate bool
			if err := pool.QueryRow(ctx, `
				SELECT
					has_table_privilege($1, $2, 'SELECT'),
					has_table_privilege($1, $2, 'INSERT'),
					has_table_privilege($1, $2, 'TRUNCATE')`,
				role,
				relation,
			).Scan(&canSelect, &canInsert, &canTruncate); err != nil {
				t.Fatalf("inspect scrubbed %s ACL on %s: %v", role, relation, err)
			}
			if canSelect || canInsert || canTruncate {
				t.Fatalf(
					"role %s retained ACL on %s: select=%t insert=%t truncate=%t",
					role,
					relation,
					canSelect,
					canInsert,
					canTruncate,
				)
			}
		}
	}

	var count int
	var tip string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(filename) FROM engine.schema_migrations`,
	).Scan(&count, &tip); err != nil {
		t.Fatalf("read upgraded migration history: %v", err)
	}
	if count != 34 || tip != commandAdmissionACLMigration {
		t.Fatalf("upgraded history = %d/%q, want 34/%q", count, tip, commandAdmissionACLMigration)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify upgraded command-admission schema: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("rerun command-admission migration: %v", err)
	}
	var rerunCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&rerunCount); err != nil {
		t.Fatalf("read rerun migration count: %v", err)
	}
	if rerunCount != count {
		t.Fatalf("rerun migration count = %d, want %d", rerunCount, count)
	}
	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "migrations", commandAdmissionACLMigration,
	))
	if err != nil {
		t.Fatalf("read command-admission migration bytes: %v", err)
	}
	wantChecksum := sha256.Sum256(raw)
	var storedChecksum []byte
	if err := pool.QueryRow(ctx, `
		SELECT checksum FROM engine.schema_migrations WHERE filename = $1`,
		commandAdmissionACLMigration,
	).Scan(&storedChecksum); err != nil {
		t.Fatalf("read command-admission migration checksum: %v", err)
	}
	if !equalBytes(storedChecksum, wantChecksum[:]) {
		t.Fatalf("migration checksum = %x, want %x", storedChecksum, wantChecksum)
	}
	previous := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, commandAdmissionACLPreviousMigration),
	)
	if err := previous.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous binary verification = %v, want schema-ahead", err)
	}
}

func TestCommandAdmissionACLPreRevocationWriterRollsBackAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertCommandAdmissionACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, commandAdmissionACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current 33-file schema: %v", err)
	}
	seedCommandAdmissionACLState(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT USAGE ON SCHEMA trading TO platformgo_projector;
		GRANT SELECT (status) ON trading.commands TO platformgo_projector;
		GRANT UPDATE (status) ON trading.commands TO platformgo_projector;
		GRANT SELECT ON trading.command_replay_responses TO PUBLIC`,
	); err != nil {
		t.Fatalf("install pre-revocation ACL fixture: %v", err)
	}
	beforeState := readCommandAdmissionACLState(t, pool)
	beforeACL := readCommandAdmissionACLUnexpected(t, pool)
	beforeTriggers := readCommandAdmissionACLTriggerState(t, pool)

	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin pre-revocation writer: %v", err)
	}
	if _, err := writer.Exec(ctx, `
		SET LOCAL ROLE platformgo_projector;
		UPDATE trading.commands SET status = status`,
	); err != nil {
		_ = writer.Rollback(ctx)
		t.Fatalf("execute pre-revocation command update: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, commandAdmissionACLMigration),
	)
	err = current.Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		_ = writer.Rollback(ctx)
		t.Fatalf("contended migration error = %v, want SQLSTATE 55P03", err)
	}
	var count int
	var tip string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(filename) FROM engine.schema_migrations`,
	).Scan(&count, &tip); err != nil {
		_ = writer.Rollback(ctx)
		t.Fatalf("read rolled-back migration history: %v", err)
	}
	if count != 33 || tip != commandAdmissionACLPreviousMigration {
		_ = writer.Rollback(ctx)
		t.Fatalf(
			"rolled-back history = %d/%q, want 33/%q",
			count,
			tip,
			commandAdmissionACLPreviousMigration,
		)
	}
	if after := readCommandAdmissionACLState(t, pool); after != beforeState {
		_ = writer.Rollback(ctx)
		t.Fatalf(
			"failed migration changed rows/files:\nbefore=%+v\nafter=%+v",
			beforeState,
			after,
		)
	}
	if after := readCommandAdmissionACLUnexpected(t, pool); !slices.Equal(after, beforeACL) {
		_ = writer.Rollback(ctx)
		t.Fatalf("failed migration changed ACL:\nbefore=%v\nafter=%v", beforeACL, after)
	}
	if after := readCommandAdmissionACLTriggerState(t, pool); after != beforeTriggers {
		_ = writer.Rollback(ctx)
		t.Fatalf(
			"failed migration changed trigger catalog:\nbefore=%s\nafter=%s",
			beforeTriggers,
			after,
		)
	}

	if err := writer.Rollback(ctx); err != nil {
		t.Fatalf("rollback pre-revocation writer: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry after writer drain: %v", err)
	}
	if after := readCommandAdmissionACLState(t, pool); after != beforeState {
		t.Fatalf(
			"successful retry changed rows/files:\nbefore=%+v\nafter=%+v",
			beforeState,
			after,
		)
	}
	assertCommandAdmissionACLRawAllowlist(t, pool)
	assertCommandAdmissionACLRuntimeAllowlist(t, pool)
	assertCommandAdmissionACLTruncateGuards(t, pool)
}

func TestCommandAdmissionACLPreventsExecutableReplayTruncate(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertCommandAdmissionACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("command_replay_truncate_hostile_%d", os.Getpid())
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile replay role: %v", err)
	}
	t.Cleanup(func() {
		if err := cleanupCommandAdmissionACLHostileRole(
			context.Background(),
			pool,
			ownerID,
			hostileID,
		); err != nil {
			t.Errorf("cleanup hostile replay role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA trading;
		GRANT USAGE ON SCHEMA trading TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s WITH GRANT OPTION`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile replay defaults: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, commandAdmissionACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply vulnerable 33-file schema: %v", err)
	}
	seedCommandAdmissionACLState(t, pool)

	requestHash := commandAdmissionACLRequestHash()
	before, found, err := platformpostgres.NewCommandJournal(pool).Replay(
		ctx,
		"command-admission-acl",
		"key-1",
		requestHash,
	)
	if err != nil || !found {
		t.Fatalf("load exact replay before attack: found=%t error=%v", found, err)
	}
	assertCommandAdmissionACLExactReplay(t, before.Response)

	attack, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hostile replay truncate: %v", err)
	}
	if _, err := attack.Exec(
		ctx,
		"SET LOCAL ROLE "+hostileID+"; TRUNCATE trading.command_replay_responses",
	); err != nil {
		_ = attack.Rollback(ctx)
		t.Fatalf("execute hostile current-tip replay truncate: %v", err)
	}
	if err := attack.Commit(ctx); err != nil {
		t.Fatalf("commit hostile current-tip replay truncate: %v", err)
	}

	broken, found, err := platformpostgres.NewCommandJournal(pool).Replay(
		ctx,
		"command-admission-acl",
		"key-1",
		requestHash,
	)
	if err != nil || !found {
		t.Fatalf("load replay after hostile truncate: found=%t error=%v", found, err)
	}
	if broken.Response.Status != 0 ||
		len(broken.Response.Headers) != 0 ||
		len(broken.Response.Body) != 0 {
		t.Fatalf(
			"truncated production replay unexpectedly remained exact: status=%d headers=%q body=%q",
			broken.Response.Status,
			broken.Response.Headers,
			broken.Response.Body,
		)
	}
	restoreCommandAdmissionACLReplay(t, pool)

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, commandAdmissionACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply command-admission ACL correction: %v", err)
	}
	denied, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin corrected hostile replay truncate: %v", err)
	}
	if _, err := denied.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		_ = denied.Rollback(ctx)
		t.Fatalf("assume scrubbed hostile role: %v", err)
	}
	_, truncateErr := denied.Exec(ctx, "TRUNCATE trading.command_replay_responses")
	_ = denied.Rollback(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(truncateErr, &postgresError) || postgresError.Code != "42501" {
		t.Fatalf(
			"corrected hostile replay truncate error = %v, want SQLSTATE 42501",
			truncateErr,
		)
	}

	after, found, err := platformpostgres.NewCommandJournal(pool).Replay(
		ctx,
		"command-admission-acl",
		"key-1",
		requestHash,
	)
	if err != nil || !found {
		t.Fatalf("load exact replay after denied attack: found=%t error=%v", found, err)
	}
	assertCommandAdmissionACLExactReplay(t, after.Response)
}

func TestCommandAdmissionACLRuntimeRoleOperations(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	assertCommandAdmissionACLPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, commandAdmissionACLMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply corrected command-admission schema: %v", err)
	}

	api := runtimeRoleLoginPool(
		t,
		admin,
		fmt.Sprintf("command_acl_api_%d", os.Getpid()),
		"platformgo_api",
	)
	engine := runtimeRoleLoginPool(
		t,
		admin,
		fmt.Sprintf("command_acl_engine_%d", os.Getpid()),
		"platformgo_engine",
	)
	outbox := runtimeRoleLoginPool(
		t,
		admin,
		fmt.Sprintf("command_acl_outbox_%d", os.Getpid()),
		"platformgo_outbox",
	)

	admission, err := api.Begin(ctx)
	if err != nil {
		t.Fatalf("begin API command admission: %v", err)
	}
	if _, err := admission.Exec(ctx, `
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'runtime-command-acl',
			'key-1',
			decode(repeat('22', 32), 'hex'),
			'019fac10-0000-4000-8000-000000000001',
			'in_progress',
			'2026-07-30T00:00:00Z'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019fac10-0000-4000-8000-000000000001',
			'urn:xb:account:runtime-command-acl',
			1,
			'submit_order',
			1,
			'{"submitOrder":{"intentId":"runtime-command-acl"}}',
			'pending',
			1785369600000000001
		);
		INSERT INTO trading.command_replay_responses (
			command_id, response_status, response_headers, response_body
		) VALUES (
			'019fac10-0000-4000-8000-000000000001',
			202,
			'{"content-type":["application/json"]}',
			convert_to('{"status":"accepted"}' || chr(10), 'UTF8')
		);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			'019fac10-0000-4000-8000-000000000001',
			'engine.input.7.command.v1',
			1,
			'{"marketSequence":0}'
		)`,
	); err != nil {
		_ = admission.Rollback(ctx)
		t.Fatalf("execute API command admission graph: %v", err)
	}
	if err := admission.Commit(ctx); err != nil {
		t.Fatalf("commit API command admission graph: %v", err)
	}
	var apiGraphCount int
	if err := api.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.commands AS command
		  JOIN trading.idempotency_records AS idempotency
		    ON idempotency.command_id = command.command_id
		  JOIN trading.command_replay_responses AS replay
		    ON replay.command_id = command.command_id
		 WHERE command.command_id =
		       '019fac10-0000-4000-8000-000000000001'`,
	).Scan(&apiGraphCount); err != nil {
		t.Fatalf("API read admitted graph: %v", err)
	}
	if apiGraphCount != 1 {
		t.Fatalf("API admitted graph count = %d, want 1", apiGraphCount)
	}
	for _, statement := range []string{
		`UPDATE trading.commands SET status = status`,
		`UPDATE trading.idempotency_records SET state = state`,
		`UPDATE trading.command_replay_responses
		    SET response_status = response_status`,
		`TRUNCATE trading.commands CASCADE`,
		`TRUNCATE trading.idempotency_records CASCADE`,
		`TRUNCATE trading.command_replay_responses CASCADE`,
	} {
		assertCommandAdmissionACLStatementDenied(t, api, statement)
	}

	var engineGraphCount int
	if err := engine.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.commands AS command
		  JOIN trading.idempotency_records AS idempotency
		    ON idempotency.command_id = command.command_id
		  JOIN trading.command_replay_responses AS replay
		    ON replay.command_id = command.command_id`,
	).Scan(&engineGraphCount); err != nil {
		t.Fatalf("engine read admitted graph: %v", err)
	}
	if engineGraphCount != 1 {
		t.Fatalf("engine admitted graph count = %d, want 1", engineGraphCount)
	}
	if _, err := engine.Exec(ctx, `
		UPDATE trading.commands
		   SET status = 'accepted',
		       result = '{"status":"accepted"}',
		       completed_at = '2026-07-29T00:03:00Z'
		 WHERE command_id = '019fac10-0000-4000-8000-000000000001';
		UPDATE trading.idempotency_records
		   SET state = 'completed',
		       response_status = 202,
		       response_headers = '{"content-type":["application/json"]}',
		       response_body =
		           convert_to('{"status":"accepted"}' || chr(10), 'UTF8')
		 WHERE command_id = '019fac10-0000-4000-8000-000000000001'`,
	); err != nil {
		t.Fatalf("engine execute exact completion columns: %v", err)
	}
	for _, statement := range []string{
		`UPDATE trading.commands SET logical_time = logical_time`,
		`UPDATE trading.idempotency_records SET request_hash = request_hash`,
		`UPDATE trading.command_replay_responses
		    SET response_status = response_status`,
		`INSERT INTO trading.command_replay_responses (
			command_id, response_status, response_headers, response_body
		) VALUES (
			'019fac10-0000-4000-8000-000000000001', 202, '{}', ''::bytea
		)`,
		`TRUNCATE trading.commands CASCADE`,
	} {
		assertCommandAdmissionACLStatementDenied(t, engine, statement)
	}

	var commandID, accountID, commandType, payload string
	var accountSequence int64
	var schemaVersion int
	var logicalTime int64
	if err := outbox.QueryRow(ctx, `
		SELECT
			command_id::text, account_id, account_sequence, command_type,
			schema_version, canonical_payload::text, logical_time
		  FROM trading.commands
		 WHERE command_id = '019fac10-0000-4000-8000-000000000001'`,
	).Scan(
		&commandID,
		&accountID,
		&accountSequence,
		&commandType,
		&schemaVersion,
		&payload,
		&logicalTime,
	); err != nil {
		t.Fatalf("outbox read exact command columns: %v", err)
	}
	if commandID != "019fac10-0000-4000-8000-000000000001" ||
		accountID != "urn:xb:account:runtime-command-acl" ||
		accountSequence != 1 ||
		commandType != "submit_order" ||
		schemaVersion != 1 ||
		payload != `{"submitOrder": {"intentId": "runtime-command-acl"}}` ||
		logicalTime != 1785369600000000001 {
		t.Fatalf(
			"outbox command projection = %q/%q/%d/%q/%d/%s/%d",
			commandID,
			accountID,
			accountSequence,
			commandType,
			schemaVersion,
			payload,
			logicalTime,
		)
	}
	assertCommandAdmissionACLStatementDenied(
		t,
		outbox,
		`SELECT status FROM trading.commands`,
	)
}

func TestCommandAdmissionACLAdvisoryFencesRollBackAndRetry(t *testing.T) {
	for _, test := range []struct {
		name        string
		lockSQL     string
		unlockSQL   string
		namespace   int64
		key         int64
		fenceDetail string
	}{
		{
			name:        "engine owner precedes admission fence",
			lockSQL:     "SELECT pg_advisory_lock($1, $2)",
			unlockSQL:   "SELECT pg_advisory_unlock($1, $2)",
			namespace:   1346850639,
			key:         7,
			fenceDetail: "engine-owner",
		},
		{
			name:        "exclusive admission gate precedes relation locks",
			lockSQL:     "SELECT pg_advisory_lock_shared($1, $2)",
			unlockSQL:   "SELECT pg_advisory_unlock_shared($1, $2)",
			namespace:   1346847044,
			key:         0,
			fenceDetail: "admission-gate",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			assertCommandAdmissionACLPostgres19Beta2(t, pool)
			resetDurableSchemas(t, pool)
			if err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, commandAdmissionACLPreviousMigration),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatalf("apply current 33-file schema: %v", err)
			}
			seedCommandAdmissionACLState(t, pool)
			if _, err := pool.Exec(ctx, `
				GRANT SELECT ON trading.command_replay_responses TO PUBLIC`,
			); err != nil {
				t.Fatalf("install advisory rollback ACL fixture: %v", err)
			}
			beforeState := readCommandAdmissionACLState(t, pool)
			beforeACL := readCommandAdmissionACLUnexpected(t, pool)
			beforeTriggers := readCommandAdmissionACLTriggerState(t, pool)

			blocker, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatalf("acquire %s blocker connection: %v", test.fenceDetail, err)
			}
			defer blocker.Release()
			if _, err := blocker.Exec(
				ctx,
				test.lockSQL,
				test.namespace,
				test.key,
			); err != nil {
				t.Fatalf("hold %s advisory lock: %v", test.fenceDetail, err)
			}
			locked := true
			defer func() {
				if locked {
					_, _ = blocker.Exec(
						context.Background(),
						test.unlockSQL,
						test.namespace,
						test.key,
					)
				}
			}()

			current := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, commandAdmissionACLMigration),
			)
			started := time.Now()
			err = current.Migrate(ctx)
			elapsed := time.Since(started)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
				t.Fatalf(
					"%s contention error = %v, want SQLSTATE 55P03",
					test.fenceDetail,
					err,
				)
			}
			if elapsed < 4*time.Second || elapsed > 8*time.Second {
				t.Fatalf(
					"%s bounded wait = %s, want approximately 5s",
					test.fenceDetail,
					elapsed,
				)
			}
			assertCommandAdmissionACLRolledBackTip(
				t,
				pool,
				beforeState,
				beforeACL,
				beforeTriggers,
				test.fenceDetail,
			)

			var released bool
			if err := blocker.QueryRow(
				ctx,
				test.unlockSQL,
				test.namespace,
				test.key,
			).Scan(&released); err != nil {
				t.Fatalf("release %s advisory lock: %v", test.fenceDetail, err)
			}
			if !released {
				t.Fatalf("%s advisory lock was not held", test.fenceDetail)
			}
			locked = false
			if err := current.Migrate(ctx); err != nil {
				t.Fatalf("retry after %s drain: %v", test.fenceDetail, err)
			}
			if after := readCommandAdmissionACLState(t, pool); after != beforeState {
				t.Fatalf(
					"%s retry changed rows/files:\nbefore=%+v\nafter=%+v",
					test.fenceDetail,
					beforeState,
					after,
				)
			}
			assertCommandAdmissionACLRawAllowlist(t, pool)
			assertCommandAdmissionACLTruncateGuards(t, pool)
		})
	}
}

func assertCommandAdmissionACLRolledBackTip(
	t *testing.T,
	pool *pgxpool.Pool,
	beforeState commandAdmissionACLState,
	beforeACL []string,
	beforeTriggers string,
	detail string,
) {
	t.Helper()
	var count int
	var tip string
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*), max(filename) FROM engine.schema_migrations`,
	).Scan(&count, &tip); err != nil {
		t.Fatalf("read %s rolled-back history: %v", detail, err)
	}
	if count != 33 || tip != commandAdmissionACLPreviousMigration {
		t.Fatalf(
			"%s rolled-back history = %d/%q, want 33/%q",
			detail,
			count,
			tip,
			commandAdmissionACLPreviousMigration,
		)
	}
	if after := readCommandAdmissionACLState(t, pool); after != beforeState {
		t.Fatalf(
			"%s failed migration changed rows/files:\nbefore=%+v\nafter=%+v",
			detail,
			beforeState,
			after,
		)
	}
	if after := readCommandAdmissionACLUnexpected(t, pool); !slices.Equal(after, beforeACL) {
		t.Fatalf(
			"%s failed migration changed ACL:\nbefore=%v\nafter=%v",
			detail,
			beforeACL,
			after,
		)
	}
	if after := readCommandAdmissionACLTriggerState(t, pool); after != beforeTriggers {
		t.Fatalf(
			"%s failed migration changed trigger catalog:\nbefore=%s\nafter=%s",
			detail,
			beforeTriggers,
			after,
		)
	}
}

func assertCommandAdmissionACLStatementDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	statement string,
) {
	t.Helper()
	_, err := pool.Exec(context.Background(), statement)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
		t.Fatalf("statement %q error = %v, want SQLSTATE 42501", statement, err)
	}
}

type commandAdmissionACLState struct {
	CommandsDigest    [sha256.Size]byte
	CommandsFile      uint32
	IdempotencyDigest [sha256.Size]byte
	IdempotencyFile   uint32
	ReplayDigest      [sha256.Size]byte
	ReplayFile        uint32
}

type commandAdmissionACLCompanionState struct {
	OrderIntentsDigest [sha256.Size]byte
	OrderIntentsFile   uint32
	OrderIntentsACL    string
	OutboxDigest       [sha256.Size]byte
	OutboxFile         uint32
	OutboxACL          string
}

type commandAdmissionACLControlState struct {
	OwnerDefaults string
	Memberships   string
}

func seedCommandAdmissionACLState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin command-admission seed: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'command-admission-acl',
			'key-1',
			decode(repeat('11', 32), 'hex'),
			'019fac00-0000-4000-8000-000000000001',
			'in_progress',
			'2026-07-30T00:00:00Z'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019fac00-0000-4000-8000-000000000001',
			'urn:xb:account:command-admission-acl',
			1,
			'submit_order',
			1,
			'{"submitOrder":{"intentId":"command-admission-acl"}}',
			'pending',
			1785369600000000000
		);
		INSERT INTO trading.command_replay_responses (
			command_id, response_status, response_headers, response_body,
			created_at
		) VALUES (
			'019fac00-0000-4000-8000-000000000001',
			202,
			'{"content-type":["application/json"]}',
			convert_to('{"status":"accepted"}' || chr(10), 'UTF8'),
			'2026-07-29T00:00:00Z'
		)`,
	); err != nil {
		t.Fatalf("seed populated command-admission authority: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit command-admission seed: %v", err)
	}
}

func seedCommandAdmissionACLNeighborState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.order_intents (
			order_id, command_id, account_id, intent_id, created_at
		) VALUES (
			'019fac00-0000-4000-8000-000000000002',
			'019fac00-0000-4000-8000-000000000001',
			'urn:xb:account:command-admission-acl',
			'neighbor-intent',
			'2026-07-29T00:01:00Z'
		);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload, producer_class,
			created_at
		) VALUES (
			'019fac00-0000-4000-8000-000000000003',
			'engine.input.7.command.v1',
			1,
			'{"kind":"neighbor","marketSequence":0}',
			'api',
			'2026-07-29T00:02:00Z'
		)`,
	); err != nil {
		t.Fatalf("seed neighboring order-intent/outbox state: %v", err)
	}
}

func readCommandAdmissionACLCompanionState(
	t *testing.T,
	pool *pgxpool.Pool,
) commandAdmissionACLCompanionState {
	t.Helper()
	var orderIntents, outbox string
	var state commandAdmissionACLCompanionState
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(
				SELECT COALESCE(
					jsonb_agg(
						jsonb_build_array(
							order_id::text, command_id::text, account_id,
							intent_id, created_at
						)
						ORDER BY order_id
					),
					'[]'::jsonb
				)::text
				  FROM trading.order_intents
			),
			pg_relation_filenode('trading.order_intents'::regclass),
			(
				SELECT COALESCE(
					jsonb_agg(
						jsonb_build_array(
							message_id::text, subject, schema_version, payload,
							producer_class, engine_shard_id,
							engine_input_id::text, attempts, next_attempt_at,
							claimed_at, published_at, publish_sequence,
							last_error, created_at
						)
						ORDER BY message_id
					),
					'[]'::jsonb
				)::text
				  FROM messaging.outbox
			),
			pg_relation_filenode('messaging.outbox'::regclass)`,
	).Scan(
		&orderIntents,
		&state.OrderIntentsFile,
		&outbox,
		&state.OutboxFile,
	); err != nil {
		t.Fatalf("read neighboring order-intent/outbox state: %v", err)
	}
	state.OrderIntentsDigest = sha256.Sum256([]byte(orderIntents))
	state.OutboxDigest = sha256.Sum256([]byte(outbox))
	state.OrderIntentsACL = readCommandAdmissionACLRawRelationState(
		t,
		pool,
		"trading.order_intents",
	)
	state.OutboxACL = readCommandAdmissionACLRawRelationState(
		t,
		pool,
		"messaging.outbox",
	)
	return state
}

func readCommandAdmissionACLControlState(
	t *testing.T,
	pool *pgxpool.Pool,
	owner string,
) commandAdmissionACLControlState {
	t.Helper()
	var state commandAdmissionACLControlState
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(
			string_agg(
				namespace.nspname || '|' ||
				default_acl.defaclobjtype::text || '|' ||
				default_acl.defaclacl::text,
				E'\n'
				ORDER BY namespace.nspname, default_acl.defaclobjtype
			),
			''
		)
		  FROM pg_catalog.pg_default_acl AS default_acl
		  LEFT JOIN pg_catalog.pg_namespace AS namespace
		    ON namespace.oid = default_acl.defaclnamespace
		 WHERE default_acl.defaclrole = $1::regrole`,
		owner,
	).Scan(&state.OwnerDefaults); err != nil {
		t.Fatalf("read migration-owner defaults: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(
			string_agg(
				granted.rolname || '|' || member.rolname || '|' ||
				grantor.rolname || '|' ||
				membership.admin_option::text || '|' ||
				membership.inherit_option::text || '|' ||
				membership.set_option::text,
				E'\n'
				ORDER BY granted.rolname, member.rolname, grantor.rolname
			),
			''
		)
		  FROM pg_catalog.pg_auth_members AS membership
		  JOIN pg_catalog.pg_roles AS granted
		    ON granted.oid = membership.roleid
		  JOIN pg_catalog.pg_roles AS member
		    ON member.oid = membership.member
		  JOIN pg_catalog.pg_roles AS grantor
		    ON grantor.oid = membership.grantor`,
	).Scan(&state.Memberships); err != nil {
		t.Fatalf("read role memberships: %v", err)
	}
	return state
}

func readCommandAdmissionACLRawRelationState(
	t *testing.T,
	pool *pgxpool.Pool,
	relation string,
) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COALESCE(catalog.relacl::text, '<NULL>') || E'\n' ||
			COALESCE(
				(
					SELECT string_agg(
						attribute.attname || '=' ||
							COALESCE(attribute.attacl::text, '<NULL>'),
						E'\n'
						ORDER BY attribute.attnum
					)
					  FROM pg_catalog.pg_attribute AS attribute
					 WHERE attribute.attrelid = catalog.oid
					   AND attribute.attnum > 0
					   AND NOT attribute.attisdropped
				),
				''
			)
		  FROM pg_catalog.pg_class AS catalog
		 WHERE catalog.oid = $1::regclass`,
		relation,
	).Scan(&state); err != nil {
		t.Fatalf("read raw ACL for %s: %v", relation, err)
	}
	return state
}

func restoreCommandAdmissionACLReplay(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.command_replay_responses (
			command_id, response_status, response_headers, response_body,
			created_at
		) VALUES (
			'019fac00-0000-4000-8000-000000000001',
			202,
			'{"content-type":["application/json"]}',
			convert_to('{"status":"accepted"}' || chr(10), 'UTF8'),
			'2026-07-29T00:00:00Z'
		)`,
	); err != nil {
		t.Fatalf("restore exact replay after vulnerability proof: %v", err)
	}
}

func commandAdmissionACLRequestHash() [sha256.Size]byte {
	var requestHash [sha256.Size]byte
	for index := range requestHash {
		requestHash[index] = 0x11
	}
	return requestHash
}

func assertCommandAdmissionACLExactReplay(
	t *testing.T,
	response platformpostgres.StoredResponse,
) {
	t.Helper()
	if response.Status != 202 ||
		string(response.Headers) != `{"content-type":["application/json"]}` ||
		string(response.Body) != "{\"status\":\"accepted\"}\n" {
		t.Fatalf(
			"stored replay = status %d headers %s body %q, want exact admitted response",
			response.Status,
			response.Headers,
			response.Body,
		)
	}
}

func readCommandAdmissionACLState(
	t *testing.T,
	pool *pgxpool.Pool,
) commandAdmissionACLState {
	t.Helper()
	var commands, idempotency, replay string
	var state commandAdmissionACLState
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(
				SELECT jsonb_agg(
					jsonb_build_object(
						'command_id', command_id::text,
						'account_id', account_id,
						'account_sequence', account_sequence,
						'command_type', command_type,
						'schema_version', schema_version,
						'canonical_payload', canonical_payload,
						'status', status,
						'result', result,
						'logical_time', logical_time,
						'created_at', created_at::text,
						'completed_at', completed_at::text,
						'market_sequence_binding', market_sequence_binding
					)
					ORDER BY command_id
				)::text
				  FROM trading.commands
			),
			pg_relation_filenode('trading.commands'::regclass),
			(
				SELECT jsonb_agg(
					jsonb_build_object(
						'scope', scope,
						'idempotency_key', idempotency_key,
						'request_hash', encode(request_hash, 'hex'),
						'command_id', command_id::text,
						'state', state,
						'response_status', response_status,
						'response_headers', response_headers,
						'response_body', encode(response_body, 'hex'),
						'created_at', created_at::text,
						'expires_at', expires_at::text
					)
					ORDER BY scope, idempotency_key
				)::text
				  FROM trading.idempotency_records
			),
			pg_relation_filenode('trading.idempotency_records'::regclass),
			(
				SELECT jsonb_agg(
					jsonb_build_object(
						'command_id', command_id::text,
						'response_status', response_status,
						'response_headers', response_headers,
						'response_body', encode(response_body, 'hex'),
						'created_at', created_at::text
					)
					ORDER BY command_id
				)::text
				  FROM trading.command_replay_responses
			),
			pg_relation_filenode('trading.command_replay_responses'::regclass)`,
	).Scan(
		&commands,
		&state.CommandsFile,
		&idempotency,
		&state.IdempotencyFile,
		&replay,
		&state.ReplayFile,
	); err != nil {
		t.Fatalf("read command-admission state: %v", err)
	}
	state.CommandsDigest = sha256.Sum256([]byte(commands))
	state.IdempotencyDigest = sha256.Sum256([]byte(idempotency))
	state.ReplayDigest = sha256.Sum256([]byte(replay))
	return state
}

func readCommandAdmissionACLTriggerState(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(
			string_agg(
				trigger.tgrelid::regclass::text || '|' ||
				trigger.tgname || '|' ||
				trigger.tgenabled::text || '|' ||
				pg_catalog.pg_get_triggerdef(trigger.oid, true),
				E'\n'
				ORDER BY trigger.tgrelid::regclass::text, trigger.tgname
			),
			''
		)
		  FROM pg_catalog.pg_trigger AS trigger
		 WHERE trigger.tgrelid = ANY(ARRAY[
			'trading.commands'::regclass::oid,
			'trading.idempotency_records'::regclass::oid,
			'trading.command_replay_responses'::regclass::oid
		 ])
		   AND NOT trigger.tgisinternal`,
	).Scan(&state); err != nil {
		t.Fatalf("read command-admission trigger state: %v", err)
	}
	return state
}

func assertCommandAdmissionACLRawAllowlist(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	got := readCommandAdmissionACLUnexpected(t, pool)
	want := []string{
		"platformgo_api|INSERT|false|trading.command_replay_responses|table",
		"platformgo_api|INSERT|false|trading.commands|table",
		"platformgo_api|INSERT|false|trading.idempotency_records|table",
		"platformgo_api|SELECT|false|trading.command_replay_responses|table",
		"platformgo_api|SELECT|false|trading.commands|table",
		"platformgo_api|SELECT|false|trading.idempotency_records|table",
		"platformgo_engine|SELECT|false|trading.command_replay_responses|table",
		"platformgo_engine|SELECT|false|trading.commands|table",
		"platformgo_engine|SELECT|false|trading.idempotency_records|table",
		"platformgo_engine|UPDATE|false|trading.commands|column:completed_at",
		"platformgo_engine|UPDATE|false|trading.commands|column:result",
		"platformgo_engine|UPDATE|false|trading.commands|column:status",
		"platformgo_engine|UPDATE|false|trading.idempotency_records|column:response_body",
		"platformgo_engine|UPDATE|false|trading.idempotency_records|column:response_headers",
		"platformgo_engine|UPDATE|false|trading.idempotency_records|column:response_status",
		"platformgo_engine|UPDATE|false|trading.idempotency_records|column:state",
		"platformgo_outbox|SELECT|false|trading.commands|column:account_id",
		"platformgo_outbox|SELECT|false|trading.commands|column:account_sequence",
		"platformgo_outbox|SELECT|false|trading.commands|column:canonical_payload",
		"platformgo_outbox|SELECT|false|trading.commands|column:command_id",
		"platformgo_outbox|SELECT|false|trading.commands|column:command_type",
		"platformgo_outbox|SELECT|false|trading.commands|column:logical_time",
		"platformgo_outbox|SELECT|false|trading.commands|column:schema_version",
	}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("command-admission raw ACL = %v, want exact %v", got, want)
	}
}

func readCommandAdmissionACLUnexpected(
	t *testing.T,
	pool *pgxpool.Pool,
) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		WITH target AS (
			SELECT relation.oid, relation.relowner, relation.relacl,
			       namespace.nspname, relation.relname
			  FROM pg_catalog.pg_class AS relation
			  JOIN pg_catalog.pg_namespace AS namespace
			    ON namespace.oid = relation.relnamespace
			 WHERE relation.oid = ANY(ARRAY[
				'trading.commands'::regclass::oid,
				'trading.idempotency_records'::regclass::oid,
				'trading.command_replay_responses'::regclass::oid
			 ])
		),
		all_acl AS (
			SELECT
				target.relowner,
				CASE WHEN privilege.grantee = 0
					THEN 'PUBLIC' ELSE role.rolname END AS grantee,
				privilege.privilege_type,
				privilege.is_grantable,
				target.nspname || '.' || target.relname AS relation_name,
				'table'::text AS scope
			  FROM target
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				COALESCE(
					target.relacl,
					pg_catalog.acldefault('r', target.relowner)
				)
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS role
			    ON role.oid = privilege.grantee
			UNION ALL
			SELECT
				target.relowner,
				CASE WHEN privilege.grantee = 0
					THEN 'PUBLIC' ELSE role.rolname END,
				privilege.privilege_type,
				privilege.is_grantable,
				target.nspname || '.' || target.relname,
				'column:' || attribute.attname
			  FROM target
			  JOIN pg_catalog.pg_attribute AS attribute
			    ON attribute.attrelid = target.oid
			   AND attribute.attnum > 0
			   AND NOT attribute.attisdropped
			  CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl)
			    AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS role
			    ON role.oid = privilege.grantee
			 WHERE attribute.attacl IS NOT NULL
		)
		SELECT grantee, privilege_type, is_grantable, relation_name, scope
		  FROM all_acl
		 WHERE grantee IS DISTINCT FROM (
			SELECT rolname FROM pg_catalog.pg_roles
			 WHERE oid = all_acl.relowner
		 )`)
	if err != nil {
		t.Fatalf("inspect command-admission raw ACL: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var grantee, privilege, relation, scope string
		var grantable bool
		if err := rows.Scan(
			&grantee,
			&privilege,
			&grantable,
			&relation,
			&scope,
		); err != nil {
			t.Fatalf("scan command-admission raw ACL: %v", err)
		}
		got = append(
			got,
			fmt.Sprintf(
				"%s|%s|%t|%s|%s",
				grantee,
				privilege,
				grantable,
				relation,
				scope,
			),
		)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate command-admission raw ACL: %v", err)
	}
	sort.Strings(got)
	return got
}

func assertCommandAdmissionACLRuntimeAllowlist(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	checks := []struct {
		role, relation, privilege string
		want                      bool
	}{
		{"platformgo_api", "trading.commands", "SELECT", true},
		{"platformgo_api", "trading.commands", "INSERT", true},
		{"platformgo_api", "trading.commands", "UPDATE", false},
		{"platformgo_engine", "trading.commands", "SELECT", true},
		{"platformgo_engine", "trading.commands", "INSERT", false},
		{"platformgo_engine", "trading.commands", "TRUNCATE", false},
		{"platformgo_api", "trading.idempotency_records", "SELECT", true},
		{"platformgo_api", "trading.idempotency_records", "INSERT", true},
		{"platformgo_api", "trading.idempotency_records", "UPDATE", false},
		{"platformgo_engine", "trading.idempotency_records", "SELECT", true},
		{"platformgo_engine", "trading.idempotency_records", "INSERT", false},
		{"platformgo_engine", "trading.idempotency_records", "TRUNCATE", false},
		{"platformgo_api", "trading.command_replay_responses", "SELECT", true},
		{"platformgo_api", "trading.command_replay_responses", "INSERT", true},
		{"platformgo_api", "trading.command_replay_responses", "UPDATE", false},
		{"platformgo_engine", "trading.command_replay_responses", "SELECT", true},
		{"platformgo_engine", "trading.command_replay_responses", "INSERT", false},
		{"platformgo_engine", "trading.command_replay_responses", "TRUNCATE", false},
	}
	for _, check := range checks {
		var got bool
		if err := pool.QueryRow(context.Background(), `
			SELECT has_table_privilege($1, $2, $3)`,
			check.role,
			check.relation,
			check.privilege,
		).Scan(&got); err != nil {
			t.Fatalf(
				"inspect runtime ACL %s/%s/%s: %v",
				check.role,
				check.relation,
				check.privilege,
				err,
			)
		}
		if got != check.want {
			t.Fatalf(
				"runtime ACL %s/%s/%s = %t, want %t",
				check.role,
				check.relation,
				check.privilege,
				got,
				check.want,
			)
		}
	}
}

func assertCommandAdmissionACLTruncateGuards(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	triggerNames := map[string]string{
		"trading.commands":                 "commands_reject_truncate",
		"trading.idempotency_records":      "idempotency_records_reject_truncate",
		"trading.command_replay_responses": "command_replay_responses_reject_truncate",
	}
	for _, relation := range commandAdmissionACLRelations {
		var triggerName, functionName, definition string
		if err := pool.QueryRow(context.Background(), `
			SELECT
				tgname,
				tgfoid::regprocedure::text,
				pg_catalog.pg_get_triggerdef(oid, true)
			  FROM pg_catalog.pg_trigger
			 WHERE tgrelid = $1::regclass
			   AND NOT tgisinternal
			   AND tgenabled = 'O'
			   AND (tgtype::integer & 2) = 2
			   AND (tgtype::integer & 32) = 32`,
			relation,
		).Scan(&triggerName, &functionName, &definition); err != nil {
			t.Fatalf("inspect truncate guard on %s: %v", relation, err)
		}
		wantName := triggerNames[relation]
		wantDefinition := fmt.Sprintf(
			"CREATE TRIGGER %s BEFORE TRUNCATE ON %s FOR EACH STATEMENT EXECUTE FUNCTION engine.reject_immutable_change()",
			wantName,
			relation,
		)
		if triggerName != wantName ||
			functionName != "engine.reject_immutable_change()" ||
			definition != wantDefinition {
			t.Fatalf(
				"truncate guard on %s = %q/%q/%q, want %q/%q/%q",
				relation,
				triggerName,
				functionName,
				definition,
				wantName,
				"engine.reject_immutable_change()",
				wantDefinition,
			)
		}
	}

	var functionOwner string
	var securityDefiner bool
	var functionConfig []string
	var functionACL []string
	if err := pool.QueryRow(context.Background(), `
		SELECT
			owner.rolname,
			procedure.prosecdef,
			procedure.proconfig,
			procedure.proacl
		  FROM pg_catalog.pg_proc AS procedure
		  JOIN pg_catalog.pg_roles AS owner
		    ON owner.oid = procedure.proowner
		 WHERE procedure.oid =
		       'engine.reject_immutable_change()'::regprocedure`,
	).Scan(
		&functionOwner,
		&securityDefiner,
		&functionConfig,
		&functionACL,
	); err != nil {
		t.Fatalf("inspect immutable trigger function metadata: %v", err)
	}
	var migrationOwner string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT current_user",
	).Scan(&migrationOwner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	if functionOwner != migrationOwner ||
		securityDefiner ||
		functionConfig != nil ||
		functionACL != nil {
		t.Fatalf(
			"immutable trigger function metadata = owner %q security_definer=%t config=%v acl=%v, want owner %q invoker/null/null",
			functionOwner,
			securityDefiner,
			functionConfig,
			functionACL,
			migrationOwner,
		)
	}
}

func assertCommandAdmissionACLPostgres19Beta2(
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
		t.Fatalf("PostgreSQL server version = %q, want 19beta2", version)
	}
}

func cleanupCommandAdmissionACLHostileRole(
	ctx context.Context,
	pool *pgxpool.Pool,
	ownerID string,
	hostileID string,
) error {
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		DROP OWNED BY %[2]s CASCADE`,
		ownerID,
		hostileID,
	)); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, "DROP ROLE "+hostileID)
	return err
}
