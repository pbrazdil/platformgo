package postgres_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
	"github.com/upcomers-org/platformgo/testkit"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInitialMigrationCreatesDurableExecutionSchema(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrations := os.DirFS(filepath.Join("..", "..", "..", "migrations"))
	migrator := platformpostgres.NewMigrator(pool, migrations)
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, relation := range []string{
		"engine.schema_migrations",
		"engine.deployment_shard",
		"engine.account_shards",
		"engine.shard_ownership_epochs",
		"engine.shard_checkpoints",
		"engine.input_receipts",
		"engine.duplicate_delivery_receipts",
		"engine.shard_faults",
		"trading.idempotency_records",
		"trading.commands",
		"trading.orders",
		"trading.fills",
		"trading.positions",
		"trading.funding_history_projection",
		"market.books",
		"ledger.transactions",
		"ledger.entries",
		"ledger.balances",
		"messaging.outbox",
		"messaging.inbox",
		"identity.users",
		"identity.user_accounts",
		"identity.sessions",
		"identity.idempotency_responses",
		"identity.account_profiles",
		"trading.order_intents",
		"realtime.channel_sequences",
		"realtime.publications",
		"realtime.publication_requeues",
	} {
		var exists bool
		if err := pool.QueryRow(
			context.Background(),
			"SELECT to_regclass($1) IS NOT NULL",
			relation,
		).Scan(&exists); err != nil {
			t.Fatalf("inspect %s: %v", relation, err)
		}
		if !exists {
			t.Fatalf("relation %s does not exist", relation)
		}
	}

	assertReceiptIdentityConstraints(t, pool)
	assertOutboxProducerAuthorityConstraints(t, pool)
	assertFinalMigrationHistory(t, pool)
	assertCommandIdempotencyAuthorityConstraints(t, pool)
	assertLedgerBalanceConstraint(t, pool)
	assertImmutableLedgerFacts(t, pool)
	assertAPIRoleCannotMutateEconomicTables(t, pool)
	assertAPIRoleIdentityBoundary(t, pool)
	assertRealtimeRoleBoundary(t, pool)
}

func TestCommandIdempotencyAuthorityMigrationUpgradesPopulatedBaseline(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	baselineName := "20260724000100_durable_execution_foundation.up.sql"
	baseline, err := os.ReadFile(filepath.Join(migrationDirectory, baselineName))
	if err != nil {
		t.Fatalf("read baseline migration: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		fstest.MapFS{baselineName: {Data: baseline}},
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous baseline: %v", err)
	}
	commandID := "019f9460-4b36-4e9b-8f44-682611f70061"
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin populated previous baseline: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO engine.deployment_shard (shard_id) VALUES (17);
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('upgrade-account', 17)`); err == nil {
		_, err = tx.Exec(ctx, `
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:upgrade-account', 'upgrade-command',
			decode(repeat('a1', 32), 'hex'), $1, 'in_progress',
			'2026-07-26T00:00:00Z'
		)`,
			commandID,
		)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			$1, 'upgrade-account', 1, 'adjust_balance',
			1, '{"amount":"10"}', 'pending', 1784970000000000000
		)`,
			commandID,
		)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES ($1, 'engine.input.17.command.v1', 1, '{}')`,
			commandID,
		)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("seed populated previous baseline: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit populated previous baseline: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx); err != nil {
		t.Fatalf("upgrade populated baseline: %v", err)
	}
	assertFinalMigrationHistory(t, pool)
	var commandCount int
	var idempotencyCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.commands WHERE command_id = $1),
			(SELECT count(*) FROM trading.idempotency_records WHERE command_id = $1)`,
		commandID,
	).Scan(&commandCount, &idempotencyCount); err != nil {
		t.Fatalf("inspect upgraded command authority: %v", err)
	}
	if commandCount != 1 || idempotencyCount != 1 {
		t.Fatalf(
			"upgraded authority counts = commands %d idempotency %d",
			commandCount,
			idempotencyCount,
		)
	}
	if _, err := pool.Exec(
		ctx,
		"DELETE FROM trading.idempotency_records WHERE command_id = $1",
		commandID,
	); err == nil {
		t.Fatal("upgraded idempotency authority was deletable")
	}
}

func TestPhase3MigrationsUpgradePopulatedPhase2Schema(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	phase2Files := fstest.MapFS{}
	for _, name := range []string{
		"20260724000100_durable_execution_foundation.up.sql",
		"20260725000100_command_idempotency_authority.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(migrationDirectory, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		phase2Files[name] = &fstest.MapFile{Data: raw}
	}
	if err := platformpostgres.NewMigrator(pool, phase2Files).Migrate(ctx); err != nil {
		t.Fatalf("apply previous Phase 2 schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO engine.deployment_shard (shard_id) VALUES (17);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('phase2-account', 'NETTING');
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('phase2-account', 17)`); err != nil {
		t.Fatalf("seed populated Phase 2 schema: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx); err != nil {
		t.Fatalf("upgrade Phase 2 schema to Phase 3: %v", err)
	}
	assertFinalMigrationHistory(t, pool)
	var shardID int64
	if err := pool.QueryRow(ctx, `
		SELECT shard_id
		  FROM engine.account_shards
		 WHERE account_id = 'phase2-account'`).Scan(&shardID); err != nil {
		t.Fatalf("read preserved Phase 2 account: %v", err)
	}
	if shardID != 17 {
		t.Fatalf("preserved Phase 2 account shard = %d, want 17", shardID)
	}
	var functionExists bool
	if err := pool.QueryRow(ctx, `
		SELECT to_regprocedure(
			'identity.provision_broker_account(text,text,bigint,text,text,text[],timestamp with time zone)'
		) IS NOT NULL`).Scan(&functionExists); err != nil {
		t.Fatalf("inspect broker provisioning function: %v", err)
	}
	if !functionExists {
		t.Fatal("broker provisioning function missing after Phase 3 upgrade")
	}
}

func TestEngineIdentitySchemaAccessUpgradePreservesIntentBeforeCutoverGuard(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	previousFiles := fstest.MapFS{}
	for _, name := range []string{
		"20260724000100_durable_execution_foundation.up.sql",
		"20260725000100_command_idempotency_authority.up.sql",
		"20260725000200_phase3_identity_compatibility.up.sql",
		"20260725000300_phase3_broker_mutation_compatibility.up.sql",
		"20260725000400_phase3_authority_and_replay_hardening.up.sql",
		"20260725000500_phase3_broker_user_conflict_target.up.sql",
		"20260725000600_phase3_completion_authority_and_candidate_guard.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(migrationDirectory, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		previousFiles[name] = &fstest.MapFile{Data: raw}
	}
	if err := platformpostgres.NewMigrator(pool, previousFiles).
		MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply previous Phase 3 schema: %v", err)
	}
	commandID := "019f9460-4b36-4e9b-8f44-682611f70107"
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			'urn:xb:user:schema-upgrade',
			'schema-upgrade',
			'schema-upgrade',
			'urn:xb:tenant:schema-upgrade'
		);
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('urn:xb:account:schema-upgrade', 7);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'broker-accounturn:xb:apikey:schema-upgrade',
			'schema-upgrade',
			decode(repeat('17', 32), 'hex'),
			'019f9460-4b36-4e9b-8f44-682611f70107',
			'in_progress',
			'2026-07-26T00:00:00Z'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019f9460-4b36-4e9b-8f44-682611f70107',
			'urn:xb:account:schema-upgrade',
			1,
			'configure_account',
			1,
			'{"configureAccount":{"accountId":"urn:xb:account:schema-upgrade","omsMode":"NETTING"}}',
			'pending',
			1785002400000000000
		);
		INSERT INTO identity.account_provisioning_intents (
			command_id, account_id, broker_subject, user_id, login,
			base_currency, market_venue, permitted_classes, created_at
		) VALUES (
			'019f9460-4b36-4e9b-8f44-682611f70107',
			'urn:xb:account:schema-upgrade',
			'urn:xb:tenant:schema-upgrade',
			'urn:xb:user:schema-upgrade',
			107,
			'USDC',
			'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'],
			'2026-07-25T18:00:00Z'
		)`); err != nil {
		t.Fatalf("seed previous Phase 3 provisioning intent: %v", err)
	}
	throughSevenFiles := fstest.MapFS{}
	for name, file := range previousFiles {
		throughSevenFiles[name] = file
	}
	raw, err := os.ReadFile(filepath.Join(
		migrationDirectory,
		"20260725000700_phase3_engine_identity_schema_access.up.sql",
	))
	if err != nil {
		t.Fatal(err)
	}
	throughSevenFiles["20260725000700_phase3_engine_identity_schema_access.up.sql"] = &fstest.MapFile{Data: raw}
	if err := platformpostgres.NewMigrator(
		pool,
		throughSevenFiles,
	).Migrate(ctx); err != nil {
		t.Fatalf("apply engine identity schema correction: %v", err)
	}
	var canUseIdentity bool
	var intentCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			has_schema_privilege(
				'platformgo_engine',
				'identity',
				'USAGE'
			),
			(
				SELECT count(*)
				  FROM identity.account_provisioning_intents
				 WHERE command_id = $1
			)`,
		commandID,
	).Scan(&canUseIdentity, &intentCount); err != nil {
		t.Fatal(err)
	}
	if !canUseIdentity || intentCount != 1 {
		t.Fatalf(
			"engine identity usage=%t preserved intents=%d",
			canUseIdentity,
			intentCount,
		)
	}
	err = platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("cutover guard error=%v, want SQLSTATE 55000", err)
	}
}

func TestRealtimeMigrationRejectsNonInjectiveExistingUserIDs(t *testing.T) {
	for name, userID := range map[string]string{
		"nested":     "urn:xb:user:tenant-a:alice",
		"non-ASCII":  "urn:xb:user:někdo",
		"reserved":   "urn:xb:user:alice/bob",
		"overlength": "urn:xb:user:" + strings.Repeat("a", 251),
	} {
		t.Run(name, func(t *testing.T) {
			assertRealtimeMigrationRejectsExistingUserID(t, userID)
		})
	}
}

func assertRealtimeMigrationRejectsExistingUserID(t *testing.T, userID string) {
	t.Helper()
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	previousFiles := fstest.MapFS{}
	for _, name := range []string{
		"20260724000100_durable_execution_foundation.up.sql",
		"20260725000100_command_idempotency_authority.up.sql",
		"20260725000200_phase3_identity_compatibility.up.sql",
		"20260725000300_phase3_broker_mutation_compatibility.up.sql",
		"20260725000400_phase3_authority_and_replay_hardening.up.sql",
		"20260725000500_phase3_broker_user_conflict_target.up.sql",
		"20260725000600_phase3_completion_authority_and_candidate_guard.up.sql",
		"20260725000700_phase3_engine_identity_schema_access.up.sql",
		"20260725000800_phase3_identity_authority_cutover_guard.up.sql",
		"20260725000900_phase3_api_readiness_probe.up.sql",
		"20260725001000_phase3_api_revision_lock.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(migrationDirectory, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		previousFiles[name] = &fstest.MapFile{Data: raw}
	}
	if err := platformpostgres.NewMigrator(pool, previousFiles).
		MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply pre-realtime schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (user_id, login, normalized_login)
		VALUES ($1, 'invalid-realtime-user', 'invalid-realtime-user')`,
		userID,
	); err != nil {
		t.Fatalf("seed invalid realtime user ID: %v", err)
	}
	err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx)
	if err == nil {
		t.Fatal("realtime migration accepted a non-injective existing user ID")
	}
	var (
		lastMigration  string
		userCount      int
		realtimeExists bool
	)
	if scanErr := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			(SELECT count(*) FROM identity.users
			  WHERE user_id = $1),
			to_regnamespace('realtime') IS NOT NULL`,
		userID,
	).Scan(&lastMigration, &userCount, &realtimeExists); scanErr != nil {
		t.Fatalf("inspect rejected realtime migration: %v", scanErr)
	}
	if lastMigration != "20260725001000_phase3_api_revision_lock.up.sql" ||
		userCount != 1 ||
		realtimeExists {
		t.Fatalf(
			"rejected migration state = last %q users %d realtime %t",
			lastMigration,
			userCount,
			realtimeExists,
		)
	}
}

func TestRealtimeMigrationUpgradesPopulatedPreviousSchema(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(
		t,
		"20260725001000_phase3_api_revision_lock.up.sql",
	)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 27); err != nil {
		t.Fatalf("apply previous schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:upgrade-realtime', 'NETTING');
		INSERT INTO identity.users (user_id, login, normalized_login)
		VALUES (
			'urn:xb:user:upgrade.realtime-1',
			'upgrade-realtime',
			'upgrade-realtime'
		);
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES (
			'urn:xb:user:upgrade.realtime-1',
			'urn:xb:account:upgrade-realtime'
		)`,
	); err != nil {
		t.Fatalf("seed previous schema: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade populated previous schema: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify upgraded schema: %v", err)
	}
	var preserved bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM identity.user_accounts
			 WHERE user_id = 'urn:xb:user:upgrade.realtime-1'
			   AND account_id = 'urn:xb:account:upgrade-realtime'
		)`,
	).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if !preserved {
		t.Fatal("populated previous-schema account mapping was not preserved")
	}
}

func TestRealtimeMigrationUsesBoundedLockAcquisitionAndRetriesCleanly(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(
		t,
		"20260725001000_phase3_api_revision_lock.up.sql",
	)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 29); err != nil {
		t.Fatalf("apply previous schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (user_id, login, normalized_login)
		VALUES (
			'urn:xb:user:realtime-lock-proof',
			'realtime-lock-proof',
			'realtime-lock-proof'
		)`,
	); err != nil {
		t.Fatalf("seed previous-schema user: %v", err)
	}
	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(
		ctx,
		"LOCK TABLE identity.users IN ACCESS SHARE MODE",
	); err != nil {
		t.Fatalf("lock identity users: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	startedAt := time.Now()
	err = current.Migrate(ctx)
	elapsed := time.Since(startedAt)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("contended realtime upgrade error = %v, want SQLSTATE 55P03", err)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("bounded realtime lock wait = %s, want approximately 5s", elapsed)
	}
	var (
		lastMigration  string
		userPreserved  bool
		realtimeExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			EXISTS (
				SELECT 1
				  FROM identity.users
				 WHERE user_id = 'urn:xb:user:realtime-lock-proof'
			),
			to_regnamespace('realtime') IS NOT NULL`,
	).Scan(&lastMigration, &userPreserved, &realtimeExists); err != nil {
		t.Fatalf("inspect rolled-back realtime migration: %v", err)
	}
	if lastMigration != "20260725001000_phase3_api_revision_lock.up.sql" ||
		!userPreserved ||
		realtimeExists {
		t.Fatalf(
			"contended realtime migration state = last %q user %t realtime %t",
			lastMigration,
			userPreserved,
			realtimeExists,
		)
	}

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry uncontended realtime upgrade: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried realtime upgrade: %v", err)
	}
	assertFinalMigrationHistory(t, pool)
}

func TestFundingHistoryMigrationUpgradesPopulatedRealtimeSchema(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(
		t,
		"20260725001100_phase3_committed_realtime_outbox.up.sql",
	)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 41); err != nil {
		t.Fatalf("apply previous realtime schema: %v", err)
	}
	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(41)
	ids := testkit.NewShardIDSequence(41)
	clock := testkit.NewManualClock(engine.NewLogicalTime(time.Date(
		2026,
		time.July,
		24,
		14,
		0,
		0,
		123456789,
		time.UTC,
	)))
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionConfigureInstrument,
			ConfigureInstrument: &engine.ConfigureInstrument{
				InstrumentID:            "BTC-PERP",
				Revision:                1,
				PriceScale:              2,
				QuantityScale:           3,
				SettlementCurrency:      "USDC",
				SettlementCurrencyScale: 2,
				InitialMarginRate:       "0.1",
				MaintenanceMarginRate:   "0.05",
				MaxLeverage:             "10",
				MakerFeeRate:            "0",
				TakerFeeRate:            "0",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	_, _, fundingInput, _ := applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: "account-1",
				OmsMode:   engine.OmsModeNetting,
			},
		},
		platformpostgres.ApplyOptions{},
	)
	const positionID = "019f9b6d-3154-4db1-b639-57c246e92201"
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.funding_settlements (
			funding_id, settlement_id, position_id, input_id,
			account_id, instrument_id, signed_quantity, oracle_price,
			rate, amount, settlement_currency
		) VALUES (
			'019f9b6d-3154-4db1-b639-57c246e92401',
			'019f9b6d-3154-4db1-b639-57c246e92501',
			$1,
			$2,
			'account-1',
			'BTC-PERP',
			3,
			100,
			0.01,
			-3,
			'USDC'
		)`,
		positionID,
		fundingInput.InputID.String(),
	); err != nil {
		t.Fatalf("seed previous-schema funding settlement: %v", err)
	}
	var (
		preUpgradeAmount string
	)
	if err := pool.QueryRow(ctx, `
		SELECT trim_scale(amount)::text
		  FROM trading.funding_settlements
		 WHERE account_id = 'account-1'`,
	).Scan(&preUpgradeAmount); err != nil {
		t.Fatalf("read pre-upgrade EngineStore funding: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade funding history read model: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify funding history migration: %v", err)
	}
	var (
		rowCount        int
		accountIndex    bool
		instrumentIndex bool
		positionIndex   bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.funding_settlements),
			to_regclass(
				'trading.funding_history_projection_account_idx'
			) IS NOT NULL,
			to_regclass(
				'trading.funding_history_projection_instrument_idx'
			) IS NOT NULL,
			to_regclass(
				'trading.funding_history_projection_account_position_idx'
			) IS NOT NULL`,
	).Scan(
		&rowCount,
		&accountIndex,
		&instrumentIndex,
		&positionIndex,
	); err != nil {
		t.Fatalf("read upgraded funding history: %v", err)
	}
	if rowCount != 1 || !accountIndex || !instrumentIndex || !positionIndex {
		t.Fatalf(
			"upgraded funding rows = %d indexes = %t/%t/%t",
			rowCount,
			accountIndex,
			instrumentIndex,
			positionIndex,
		)
	}

	compatibilityStore := platformpostgres.NewCompatibilityStore(pool)
	page, err := compatibilityStore.Funding(
		ctx,
		"account-1",
		edge.PageParams{Limit: 1},
	)
	if err != nil {
		t.Fatalf("read upgraded EngineStore funding page: %v", err)
	}
	fundingTime := time.Date(
		2026,
		time.July,
		24,
		14,
		0,
		1,
		123456789,
		time.UTC,
	)
	if len(page.Items) != 1 ||
		page.Items[0].FundingAmount != preUpgradeAmount ||
		page.Items[0].FundingTime != fundingTime.Format(time.RFC3339Nano) {
		t.Fatalf("upgraded EngineStore funding page = %#v", page)
	}
	since := fundingTime.Add(-time.Nanosecond)
	total, err := compatibilityStore.FundingPaidByPosition(
		ctx,
		"account-1",
		positionID,
		&since,
	)
	if err != nil || total != preUpgradeAmount {
		t.Fatalf(
			"upgraded since-scoped total = %q, want %q, error %v",
			total,
			preUpgradeAmount,
			err,
		)
	}
	assertFinalMigrationHistory(t, pool)
}

func TestFillHistoryMigrationUsesBoundedLockAcquisitionAndRetriesCleanly(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(
		t,
		"20260726000100_phase3_funding_history_read_model.up.sql",
	)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 41); err != nil {
		t.Fatalf("apply previous funding schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-upgrade', 'NETTING');
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			'urn:xb:user:fill-upgrade',
			'fill-upgrade',
			'fill-upgrade',
			'urn:xb:tenant:fill-upgrade'
		);
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES (
			'urn:xb:user:fill-upgrade',
			'urn:xb:account:fill-upgrade',
			'urn:xb:tenant:fill-upgrade'
		);
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES (
			'urn:xb:account:fill-upgrade',
			1001,
			'USDC',
			'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'],
			'2020-09-13T12:26:40Z',
			'urn:xb:tenant:fill-upgrade'
		);
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000001',
			'urn:xb:account:fill-upgrade',
			'BTC-PERP',
			'BUY',
			'MARKET',
			'IOC',
			'FILLED',
			1,
			1,
			60000,
			false,
			false,
			false,
			1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, fee, fee_currency, logical_time
		)
		SELECT
			format(
				'10000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'019fa844-26c0-7000-8000-000000000001'::uuid,
			format(
				'20000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'urn:xb:account:fill-upgrade',
			'BTC-PERP',
			'BUY',
			60000,
			0.01,
			'30000000-0000-0000-0000-000000000001'::uuid,
			'OPEN',
			'TAKER',
			0.5,
			'USDC',
			1600000000000000000 + sequence_number
		  FROM generate_series(1, 100) AS sequence(sequence_number)`); err != nil {
		t.Fatalf("seed populated fill-history schema: %v", err)
	}

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin fill-history lock: %v", err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(
		ctx,
		"LOCK TABLE trading.fills IN ROW EXCLUSIVE MODE",
	); err != nil {
		t.Fatalf("lock fills: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	startedAt := time.Now()
	err = current.Migrate(ctx)
	elapsed := time.Since(startedAt)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("contended fill-history upgrade error = %v, want SQLSTATE 55P03", err)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("bounded fill-history lock wait = %s, want approximately 5s", elapsed)
	}
	var (
		lastMigration string
		indexExists   bool
		fillCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			to_regclass('trading.fills_account_history_idx') IS NOT NULL,
			(SELECT count(*) FROM trading.fills)`,
	).Scan(&lastMigration, &indexExists, &fillCount); err != nil {
		t.Fatalf("inspect rolled-back fill-history migration: %v", err)
	}
	if lastMigration != "20260726000100_phase3_funding_history_read_model.up.sql" ||
		indexExists ||
		fillCount != 100 {
		t.Fatalf(
			"contended fill-history migration state = last %q index %t fills %d",
			lastMigration,
			indexExists,
			fillCount,
		)
	}

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry uncontended fill-history upgrade: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried fill-history upgrade: %v", err)
	}
	assertFinalMigrationHistory(t, pool)
	latest, err := platformpostgres.NewCompatibilityStore(pool).LatestFillExecution(
		ctx,
		"urn:xb:account:fill-upgrade",
	)
	if err != nil {
		t.Fatalf("read preserved fill history after upgrade: %v", err)
	}
	wantTime := time.Unix(0, 1600000000000000100).
		UTC().
		Format(time.RFC3339Nano)
	if latest.FillID != "10000000-0000-0000-0000-000000000064" ||
		latest.FilledAt != wantTime {
		t.Fatalf("preserved latest fill = %#v, want newest time %q", latest, wantTime)
	}
}

func TestFillFilterMigrationUsesBoundedLockAcquisitionAndRetriesCleanly(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(
		t,
		"20260726000200_phase3_fill_history_read_model.up.sql",
	)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 42); err != nil {
		t.Fatalf("apply previous fill-history schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-filter-upgrade', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000001',
			'urn:xb:account:fill-filter-upgrade',
			'BTC-PERP',
			'BUY',
			'MARKET',
			'IOC',
			'FILLED',
			1,
			1,
			60000,
			false,
			false,
			false,
			1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		)
		SELECT
			format(
				'10000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'019fa844-26c0-7000-8000-000000000001'::uuid,
			format(
				'20000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'urn:xb:account:fill-filter-upgrade',
			'BTC-PERP',
			CASE
				WHEN sequence_number % 2 = 0 THEN 'BUY'
				ELSE 'SELL'
			END,
			60000,
			0.01,
			'30000000-0000-0000-0000-000000000001'::uuid,
			'OPEN',
			'TAKER',
			1600000000000000000 + sequence_number
		  FROM generate_series(1, 100) AS sequence(sequence_number)`); err != nil {
		t.Fatalf("seed populated fill-filter schema: %v", err)
	}

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin fill-filter lock: %v", err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(
		ctx,
		"LOCK TABLE trading.fills IN ROW EXCLUSIVE MODE",
	); err != nil {
		t.Fatalf("lock fills: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	startedAt := time.Now()
	err = current.Migrate(ctx)
	elapsed := time.Since(startedAt)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("contended fill-filter upgrade error = %v, want SQLSTATE 55P03", err)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("bounded fill-filter lock wait = %s, want approximately 5s", elapsed)
	}
	var (
		lastMigration    string
		historyIndex     bool
		sideHistoryIndex bool
		fillCount        int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			to_regclass('trading.fills_account_history_idx') IS NOT NULL,
			to_regclass('trading.fills_account_side_history_idx') IS NOT NULL,
			(SELECT count(*) FROM trading.fills)`,
	).Scan(
		&lastMigration,
		&historyIndex,
		&sideHistoryIndex,
		&fillCount,
	); err != nil {
		t.Fatalf("inspect rolled-back fill-filter migration: %v", err)
	}
	if lastMigration != "20260726000200_phase3_fill_history_read_model.up.sql" ||
		!historyIndex ||
		sideHistoryIndex ||
		fillCount != 100 {
		t.Fatalf(
			"contended fill-filter migration state = last %q old-index %t "+
				"new-index %t fills %d",
			lastMigration,
			historyIndex,
			sideHistoryIndex,
			fillCount,
		)
	}

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry uncontended fill-filter upgrade: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried fill-filter upgrade: %v", err)
	}
	assertFinalMigrationHistory(t, pool)

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_filter_upgrade_api_login",
		"platformgo_api",
	)
	filtered, err := platformpostgres.NewCompatibilityStore(apiPool).
		FilterFillExecutions(
			ctx,
			"urn:xb:account:fill-filter-upgrade",
			platformpostgres.FillExecutionFilter{Side: "buy", Limit: 200},
		)
	if err != nil {
		t.Fatalf("read preserved filtered fills after upgrade: %v", err)
	}
	if len(filtered.Items) != 50 || filtered.Total != 50 {
		t.Fatalf("preserved BUY fills = %#v, want 50 rows and total 50", filtered)
	}
	if filtered.Items[0].FillID !=
		"10000000-0000-0000-0000-000000000064" {
		t.Fatalf("newest preserved BUY fill = %q", filtered.Items[0].FillID)
	}
}

func TestRuntimeMigrationVerificationIsExactAndOldEngineIsFenced(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(
		t,
		"20260725001000_phase3_api_revision_lock.up.sql",
	)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 31); err != nil {
		t.Fatalf("apply previous schema: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := current.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaBehind,
	) {
		t.Fatalf("previous schema verification error = %v, want behind", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade current schema: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("current schema verification: %v", err)
	}

	oldPrivilegedEngine, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = oldPrivilegedEngine.Exec(ctx, testInputReceiptInsertSQL, 31, 1)
	if err == nil {
		_ = oldPrivilegedEngine.Rollback(ctx)
		t.Fatal("privileged old engine without runtime revision committed a receipt")
	}
	var postgresErr *pgconn.PgError
	if !errors.As(err, &postgresErr) || postgresErr.Code != "55000" {
		_ = oldPrivilegedEngine.Rollback(ctx)
		t.Fatalf("privileged old-engine fence error = %v, want SQLSTATE 55000", err)
	}
	_ = oldPrivilegedEngine.Rollback(ctx)

	const oldEngineLogin = "platformgo_old_engine_test"
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE ROLE %s LOGIN PASSWORD 'old-engine-test-password'
			NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
		GRANT platformgo_engine TO %s`,
		pgx.Identifier{oldEngineLogin}.Sanitize(),
		pgx.Identifier{oldEngineLogin}.Sanitize(),
	)); err != nil {
		t.Fatalf("create inherited old-engine login: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			fmt.Sprintf(
				"DROP ROLE IF EXISTS %s",
				pgx.Identifier{oldEngineLogin}.Sanitize(),
			),
		)
	})
	oldEngineConfig, err := pgxpool.ParseConfig(
		os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN"),
	)
	if err != nil {
		t.Fatal(err)
	}
	oldEngineConfig.ConnConfig.User = oldEngineLogin
	oldEngineConfig.ConnConfig.Password = "old-engine-test-password"
	oldEnginePool, err := pgxpool.NewWithConfig(ctx, oldEngineConfig)
	if err != nil {
		t.Fatalf("open inherited old-engine login: %v", err)
	}
	oldEngine, err := oldEnginePool.Begin(ctx)
	if err != nil {
		oldEnginePool.Close()
		t.Fatalf("begin inherited old-engine transaction: %v", err)
	}
	_, err = oldEngine.Exec(ctx, testInputReceiptInsertSQL, 31, 1)
	if err == nil {
		_ = oldEngine.Rollback(ctx)
		oldEnginePool.Close()
		t.Fatal("inherited old-engine login committed a receipt")
	}
	if !errors.As(err, &postgresErr) || postgresErr.Code != "55000" {
		_ = oldEngine.Rollback(ctx)
		oldEnginePool.Close()
		t.Fatalf("inherited old-engine fence error = %v, want SQLSTATE 55000", err)
	}
	_ = oldEngine.Rollback(ctx)
	oldEnginePool.Close()

	currentEngine, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = currentEngine.Exec(ctx, "SET LOCAL ROLE platformgo_engine"); err == nil {
		_, err = currentEngine.Exec(ctx, `
			SELECT set_config(
				'platformgo.runtime_schema_revision',
				'20260725001100_phase3_committed_realtime_outbox',
				true
			)`)
	}
	if err == nil {
		_, err = currentEngine.Exec(ctx, testInputReceiptInsertSQL, 31, 2)
	}
	if err != nil {
		_ = currentEngine.Rollback(ctx)
		t.Fatalf("current engine receipt: %v", err)
	}
	if err := currentEngine.Commit(ctx); err != nil {
		t.Fatalf("commit current engine receipt: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO engine.schema_migrations (filename, checksum)
		VALUES (
			'99999999999999_unknown_future.up.sql',
			decode(repeat('01', 32), 'hex')
		)`,
	); err != nil {
		t.Fatal(err)
	}
	if err := current.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("future schema verification error = %v, want ahead", err)
	}
}

const testInputReceiptInsertSQL = `
	INSERT INTO engine.input_receipts (
		shard_id, input_id, stream_sequence, schema_version,
		input_hash_version, input_hash, decision_hash_version, decision_hash,
		resulting_state_hash, envelope, decision, business_input_hash,
		business_input_hash_version
	) VALUES (
		$1,
		CASE $2::bigint
			WHEN 1 THEN '019f9460-4b36-4e9b-8f44-682611f73101'::uuid
			ELSE '019f9460-4b36-4e9b-8f44-682611f73102'::uuid
		END,
		$2::bigint,
		1,
		1,
		decode(repeat('00', 32), 'hex'),
		1,
		decode(repeat('01', 32), 'hex'),
		decode(repeat('02', 32), 'hex'),
		'{}',
		'{}',
		decode(repeat('03', 32), 'hex'),
		1
	)`

func TestPhase3UpgradeRejectsAmbiguousCandidateIdentityData(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	candidateFiles := fstest.MapFS{}
	for _, name := range []string{
		"20260724000100_durable_execution_foundation.up.sql",
		"20260725000100_command_idempotency_authority.up.sql",
		"20260725000200_phase3_identity_compatibility.up.sql",
		"20260725000300_phase3_broker_mutation_compatibility.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(migrationDirectory, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		candidateFiles[name] = &fstest.MapFile{Data: raw}
	}
	if err := platformpostgres.NewMigrator(pool, candidateFiles).Migrate(ctx); err != nil {
		t.Fatalf("apply unreleased candidate history: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			created_at
		) VALUES (
			'urn:xb:user:ambiguous', 'ambiguous', 'ambiguous',
			'ambiguous@example.com', 'ambiguous@example.com',
			'9999-01-01T00:00:00Z'
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:ambiguous', 'NETTING');
		INSERT INTO identity.user_accounts (user_id, account_id, created_at)
		VALUES (
			'urn:xb:user:ambiguous',
			'urn:xb:account:ambiguous',
			'9999-01-01T00:00:00Z'
		);
		INSERT INTO identity.idempotency_responses (
			scope, idempotency_key, request_hash, response_status,
			response_body, expires_at, created_at
		) VALUES (
			'candidate', 'ambiguous', decode(repeat('aa', 32), 'hex'),
			201, '{"id":"urn:xb:account:ambiguous"}',
			'9999-01-02T00:00:00Z',
			'9999-01-01T00:00:00Z'
		)`); err != nil {
		t.Fatalf("seed ambiguous candidate identity data: %v", err)
	}
	err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("ambiguous candidate upgrade error=%v, want SQLSTATE 55000", err)
	}
	var users int
	var ownerships int
	var responses int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.users),
			(SELECT count(*) FROM identity.user_accounts),
			(SELECT count(*) FROM identity.idempotency_responses)`,
	).Scan(&users, &ownerships, &responses); err != nil {
		t.Fatal(err)
	}
	if users != 1 || ownerships != 1 || responses != 1 {
		t.Fatalf(
			"refused upgrade changed candidate data users=%d ownerships=%d responses=%d",
			users,
			ownerships,
			responses,
		)
	}
	if _, err := pool.Exec(ctx, `
		TRUNCATE identity.idempotency_responses,
		         identity.user_accounts,
		         identity.users
		CASCADE`); err != nil {
		t.Fatalf("owner-directed candidate reset: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx); err != nil {
		t.Fatalf("upgrade after owner-directed reset: %v", err)
	}
	assertFinalMigrationHistory(t, pool)
}

func TestPhase3UpgradeRejectsCandidateTimestampEqualToAuthorityCutover(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	throughThree := fstest.MapFS{}
	for _, name := range []string{
		"20260724000100_durable_execution_foundation.up.sql",
		"20260725000100_command_idempotency_authority.up.sql",
		"20260725000200_phase3_identity_compatibility.up.sql",
		"20260725000300_phase3_broker_mutation_compatibility.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(migrationDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		throughThree[name] = &fstest.MapFile{Data: raw}
	}
	if err := platformpostgres.NewMigrator(pool, throughThree).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, created_at
		) VALUES (
			'urn:xb:user:equal-cutover',
			'equal-cutover',
			'equal-cutover',
			'9999-01-01T00:00:00Z'
		)`); err != nil {
		t.Fatal(err)
	}
	throughFive := fstest.MapFS{}
	for name, file := range throughThree {
		throughFive[name] = file
	}
	for _, name := range []string{
		"20260725000400_phase3_authority_and_replay_hardening.up.sql",
		"20260725000500_phase3_broker_user_conflict_target.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(migrationDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		throughFive[name] = &fstest.MapFile{Data: raw}
	}
	if err := platformpostgres.NewMigrator(pool, throughFive).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var authorityAppliedAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT applied_at
		  FROM engine.schema_migrations
		 WHERE filename =
		       '20260725000400_phase3_authority_and_replay_hardening.up.sql'
	`).Scan(&authorityAppliedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE identity.users
		   SET created_at = $1
		 WHERE user_id = 'urn:xb:user:equal-cutover'`,
		authorityAppliedAt,
	); err != nil {
		t.Fatal(err)
	}
	err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("equal-cutover upgrade error=%v, want SQLSTATE 55000", err)
	}
	var createdAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT created_at
		  FROM identity.users
		 WHERE user_id = 'urn:xb:user:equal-cutover'
	`).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	if !createdAt.Equal(authorityAppliedAt) {
		t.Fatalf(
			"refused upgrade changed created_at=%s, want %s",
			createdAt,
			authorityAppliedAt,
		)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE identity.users CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx); err != nil {
		t.Fatalf("upgrade after owner-directed reset: %v", err)
	}
	assertFinalMigrationHistory(t, pool)
}

func TestPhase3UpgradeRejectsRuntimeRoleDriftBeforeApplyingPrivileges(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	phase2Files := fstest.MapFS{}
	for _, name := range []string{
		"20260724000100_durable_execution_foundation.up.sql",
		"20260725000100_command_idempotency_authority.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(migrationDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		phase2Files[name] = &fstest.MapFile{Data: raw}
	}
	if err := platformpostgres.NewMigrator(pool, phase2Files).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO engine.deployment_shard (shard_id) VALUES (17);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('role-drift-account', 'NETTING');
		GRANT platformgo_engine TO platformgo_api`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"REVOKE platformgo_engine FROM platformgo_api",
		)
	})
	err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
		t.Fatalf("unsafe role upgrade error = %v, want SQLSTATE 42501", err)
	}
	var migrationCount int
	var identityExists bool
	var accountExists bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.schema_migrations),
			to_regnamespace('identity') IS NOT NULL,
			EXISTS (
				SELECT 1
				  FROM trading.accounts
				 WHERE account_id = 'role-drift-account'
			)`,
	).Scan(
		&migrationCount,
		&identityExists,
		&accountExists,
	); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 2 || identityExists || !accountExists {
		t.Fatalf(
			"role drift changed upgrade state: migrations=%d identity=%t account=%t",
			migrationCount,
			identityExists,
			accountExists,
		)
	}
}

func TestPhase3UpgradeUsesBoundedLockAcquisitionAndRetriesCleanly(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	phase2Files := fstest.MapFS{}
	for _, name := range []string{
		"20260724000100_durable_execution_foundation.up.sql",
		"20260725000100_command_idempotency_authority.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(migrationDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		phase2Files[name] = &fstest.MapFile{Data: raw}
	}
	if err := platformpostgres.NewMigrator(pool, phase2Files).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(ctx, `
		LOCK TABLE trading.commands IN ROW EXCLUSIVE MODE;
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'lock-test', 'lock-test', decode(repeat('00', 32), 'hex'),
			'019f9519-ddf7-4b93-a9db-dae6ca7a6499',
			'in_progress', clock_timestamp() + interval '1 day'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019f9519-ddf7-4b93-a9db-dae6ca7a6499',
			'lock-test-account', 1, 'lock-test', 1, '{}', 'pending', 1
		)`); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now()
	err = platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx)
	elapsed := time.Since(startedAt)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("contended upgrade error = %v, want SQLSTATE 55P03", err)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("bounded lock wait = %s, want approximately 5s", elapsed)
	}
	var migrationCount int
	var identityExists bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.schema_migrations),
			to_regnamespace('identity') IS NOT NULL`,
	).Scan(&migrationCount, &identityExists); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 2 || identityExists {
		t.Fatalf(
			"contended upgrade partially applied: migrations=%d identity=%t",
			migrationCount,
			identityExists,
		)
	}
	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx); err != nil {
		t.Fatalf("retry uncontended Phase 3 upgrade: %v", err)
	}
	assertFinalMigrationHistory(t, pool)
}

func TestCommandIdempotencyAuthorityMigrationRejectsCorruptBaseline(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	baselineName := "20260724000100_durable_execution_foundation.up.sql"
	baseline, err := os.ReadFile(filepath.Join(migrationDirectory, baselineName))
	if err != nil {
		t.Fatalf("read baseline migration: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		fstest.MapFS{baselineName: {Data: baseline}},
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous baseline: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:orphan', 'orphan',
			decode(repeat('a2', 32), 'hex'),
			'019f9460-4b36-4e9b-8f44-682611f70062',
			'in_progress', '2026-07-26T00:00:00Z'
		)`); err != nil {
		t.Fatalf("seed corrupt previous baseline: %v", err)
	}
	err = platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(ctx)
	if !isPostgresCode(err, "23514") {
		t.Fatalf("corrupt baseline upgrade error = %v, want 23514", err)
	}
	var applied int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.schema_migrations
		 WHERE filename = '20260725000100_command_idempotency_authority.up.sql'`,
	).Scan(&applied); err != nil {
		t.Fatalf("inspect rejected correction migration: %v", err)
	}
	if applied != 0 {
		t.Fatal("corrupt baseline recorded correction migration")
	}
}

func TestFinalBaselineAcceptsRepresentativePopulatedGraph(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire baseline population connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(context.Background(), `
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260725001100_phase3_committed_realtime_outbox',
			false
		)`); err != nil {
		t.Fatalf("bind baseline runtime schema revision: %v", err)
	}
	_, err = connection.Exec(context.Background(), `
		INSERT INTO engine.deployment_shard (shard_id) VALUES (7);
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-USDC', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('account-netting', 'NETTING'), ('account-hedging', 'HEDGING');
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('account-netting', 7), ('account-hedging', 7);
		INSERT INTO trading.risk_configs (
			account_id, instrument_id, margin_mode, leverage
		) VALUES
			('account-netting', 'BTC-USDC', 'CROSS', 5),
			('account-hedging', 'BTC-USDC', 'ISOLATED', 2);
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, limit_price, triggered, reduce_only,
			has_rested, has_slippage_band, max_slippage_bps,
			slippage_reference, version
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70001',
			 'account-netting', 'BTC-USDC', 'BUY', 'LIMIT', 'GTC',
			 'WORKING', 2.000, 1.000, 100.00, 100.00, false, false,
			 true, true, 25, 99.50, 1),
			('019f9460-4b36-4e9b-8f44-682611f70002',
			 'account-hedging', 'BTC-USDC', 'SELL', 'MARKET', 'IOC',
			 'FILLED', 1.000, 1.000, 101.00, NULL, false, false,
			 false, false, 0, NULL, 1);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70011',
			 '019f9460-4b36-4e9b-8f44-682611f70001',
			 '019f9460-4b36-4e9b-8f44-682611f70021',
			 'account-netting', 'BTC-USDC', 'BUY', 100.00, 1.000,
			 '019f9460-4b36-4e9b-8f44-682611f70031', 'OPEN',
			 0.00, 'USDC', 'MAKER', -0.01, 'USDC',
			 1784894400000000000),
			('019f9460-4b36-4e9b-8f44-682611f70012',
			 '019f9460-4b36-4e9b-8f44-682611f70002',
			 '019f9460-4b36-4e9b-8f44-682611f70022',
			 'account-hedging', 'BTC-USDC', 'SELL', 101.00, 1.000,
			 '019f9460-4b36-4e9b-8f44-682611f70032', 'OPEN',
			 NULL, NULL, 'TAKER', NULL, NULL,
			 1784894401000000000);
		INSERT INTO trading.positions (
			position_id, account_id, instrument_id, side, status,
			signed_quantity, average_open_price, realized_pnl,
			settlement_currency, margin_mode, isolated_collateral, version
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70031',
			 'account-netting', 'BTC-USDC', 'LONG', 'OPEN',
			 1.000, 100.00, 0.00, 'USDC', 'CROSS', 0.00, 1),
			('019f9460-4b36-4e9b-8f44-682611f70032',
			 'account-hedging', 'BTC-USDC', 'SHORT', 'OPEN',
			 -1.000, 101.00, 0.00, 'USDC', 'ISOLATED', 50.50, 1),
			('019f9460-4b36-4e9b-8f44-682611f70033',
			 'account-netting', 'BTC-USDC', 'FLAT', 'CLOSED',
			 0.000, 0.00, 2.00, 'USDC', 'CROSS', 0.00, 1);
		INSERT INTO ledger.transactions (
			transaction_id, business_key, input_id, logical_time
		) VALUES (
			'019f9460-4b36-4e9b-8f44-682611f70041',
			'baseline-balanced', '019f9460-4b36-4e9b-8f44-682611f70021',
			1784894400000000000
		);
		INSERT INTO ledger.entries (
			entry_id, transaction_id, account_id, currency, amount
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70042',
			 '019f9460-4b36-4e9b-8f44-682611f70041',
			 'account-netting', 'USDC', 10.00),
			('019f9460-4b36-4e9b-8f44-682611f70043',
			 '019f9460-4b36-4e9b-8f44-682611f70041',
			 'system:clearing', 'USDC', -10.00);
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES ('account-netting', 'USDC', 10.00, 2.00, 8.00, 10.00, 1);
		INSERT INTO market.books (
			instrument_id, mark_price, bids, asks, stream_sequence
		) VALUES ('BTC-USDC', 100.50, '[]', '[]', 1);
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash, state_snapshot
		) VALUES
			(7, 5, false, decode(repeat('11', 32), 'hex'), '{}');
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, business_input_hash_version, business_input_hash,
			resulting_state_hash, envelope, decision
		) VALUES
			(7, '019f9460-4b36-4e9b-8f44-682611f70021', 1, 1,
			 1, decode(repeat('21', 32), 'hex'), 1,
			 decode(repeat('31', 32), 'hex'), 1,
			 decode(repeat('41', 32), 'hex'),
			 decode(repeat('51', 32), 'hex'), '{}', '{}'),
			(7, '019f9460-4b36-4e9b-8f44-682611f70022', 2, 1,
			 1, decode(repeat('22', 32), 'hex'), 1,
			 decode(repeat('32', 32), 'hex'), 1,
			 decode(repeat('42', 32), 'hex'),
			 decode(repeat('52', 32), 'hex'), '{}', '{}'),
			(7, '019f9460-4b36-4e9b-8f44-682611f70023', 3, 1,
			 1, decode(repeat('23', 32), 'hex'), 1,
			 decode(repeat('33', 32), 'hex'), 1,
			 decode(repeat('43', 32), 'hex'),
			 decode(repeat('53', 32), 'hex'), '{}', '{}');
		INSERT INTO engine.shard_faults (
			shard_id, resulting_state_hash, input_id, stream_sequence,
			error_kind, error_detail, envelope, supplied_action
		) VALUES (
			7, decode(repeat('61', 32), 'hex'),
			'019f9460-4b36-4e9b-8f44-682611f70024', 4,
			'durable_conflict', 'fixture', '{}', decode('00', 'hex')
		);
		INSERT INTO engine.duplicate_delivery_receipts (
			shard_id, stream_sequence, input_id, input_hash,
			original_decision_hash, decision_hash, resulting_state_hash,
			envelope, decision
		) VALUES (
			7, 3, '019f9460-4b36-4e9b-8f44-682611f70021',
			decode(repeat('21', 32), 'hex'),
			decode(repeat('31', 32), 'hex'),
			decode(repeat('71', 32), 'hex'),
			decode(repeat('51', 32), 'hex'), '{}', '{}'
		);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:account-netting', 'baseline-command',
			decode(repeat('81', 32), 'hex'),
			'019f9460-4b36-4e9b-8f44-682611f70051',
			'in_progress', '2026-07-25T12:00:00Z'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019f9460-4b36-4e9b-8f44-682611f70051',
			'account-netting', 1, 'adjust_balance', 1, '{}',
			'pending', 1784894400000000000
		);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload,
			published_at, publish_sequence
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70051',
			 'engine.input.7.command.v1', 1, '{}', NULL, NULL),
			('019f9460-4b36-4e9b-8f44-682611f70052',
			 'domain.v1.fixture', 1, '{}', clock_timestamp(), 1);
		INSERT INTO messaging.inbox (consumer, message_id)
		VALUES ('baseline-projector',
			'019f9460-4b36-4e9b-8f44-682611f70052');
	`)
	if err != nil {
		t.Fatalf("populate final baseline graph: %v", err)
	}

	for relation, want := range map[string]int{
		"engine.account_shards":              2,
		"engine.deployment_shard":            1,
		"engine.input_receipts":              3,
		"engine.shard_checkpoints":           1,
		"engine.shard_faults":                1,
		"engine.duplicate_delivery_receipts": 1,
		"trading.accounts":                   2,
		"trading.orders":                     2,
		"trading.fills":                      2,
		"trading.positions":                  3,
		"ledger.transactions":                1,
		"ledger.entries":                     2,
		"messaging.outbox":                   2,
		"messaging.inbox":                    1,
	} {
		var count int
		query := "SELECT count(*) FROM " + relation
		if err := pool.QueryRow(context.Background(), query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", relation, err)
		}
		if count != want {
			t.Fatalf("%s rows = %d, want %d", relation, count, want)
		}
	}
	if _, err := pool.Exec(
		context.Background(),
		"INSERT INTO trading.accounts (account_id, oms_mode) VALUES ('lowercase', 'netting')",
	); err == nil {
		t.Fatal("former lowercase enum spelling was accepted")
	}
}

func TestFinalBaselineRuntimeRolesEnforceTransactionOwnership(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 9); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	apiTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin API transaction: %v", err)
	}
	if _, err = apiTransaction.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_api",
	); err == nil {
		_, err = apiTransaction.Exec(context.Background(), `
			INSERT INTO engine.account_shards (account_id, shard_id)
			VALUES ('role-account', 9);
			INSERT INTO trading.idempotency_records (
				scope, idempotency_key, request_hash, command_id,
				state, expires_at
			) VALUES (
				'account:role-account', 'role-command',
				decode(repeat('91', 32), 'hex'),
				'019f9460-4b36-4e9b-8f44-682611f70101',
				'in_progress', '2026-07-25T12:00:00Z'
			);
			INSERT INTO trading.commands (
				command_id, account_id, account_sequence, command_type,
				schema_version, canonical_payload, status, logical_time
			) VALUES (
				'019f9460-4b36-4e9b-8f44-682611f70101',
				'role-account', 1, 'adjust_balance', 1, '{}',
				'pending', 1784894400000000000
			);
			INSERT INTO messaging.outbox (
				message_id, subject, schema_version, payload
			) VALUES (
				'019f9460-4b36-4e9b-8f44-682611f70101',
				'engine.input.9.command.v1', 1, '{}'
			)`)
	}
	if err != nil {
		_ = apiTransaction.Rollback(context.Background())
		t.Fatalf("API command transaction: %v", err)
	}
	if err := apiTransaction.Commit(context.Background()); err != nil {
		t.Fatalf("commit API transaction: %v", err)
	}

	assertRoleStatementDenied(t, pool, "platformgo_api", `
		INSERT INTO engine.deployment_shard (shard_id) VALUES (10)`)
	assertRoleStatementDenied(t, pool, "platformgo_api", `
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES ('role-account', 'USDC', 1, 0, 1, 1, 1)`)
	assertRoleStatementDenied(t, pool, "platformgo_api", `
		UPDATE trading.commands
		   SET status = 'completed', completed_at = clock_timestamp()
		 WHERE command_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)
	assertRoleStatementDenied(t, pool, "platformgo_api", `
		UPDATE trading.idempotency_records
		   SET request_hash = decode(repeat('ff', 32), 'hex')
		 WHERE scope = 'account:role-account'
		   AND idempotency_key = 'role-command'`)

	engineTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin engine transaction: %v", err)
	}
	if _, err = engineTransaction.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_engine",
	); err == nil {
		_, err = engineTransaction.Exec(context.Background(), `
			SELECT set_config(
				'platformgo.runtime_schema_revision',
				'20260725001100_phase3_committed_realtime_outbox',
				true
			)`)
	}
	if err == nil {
		_, err = engineTransaction.Exec(context.Background(), `
			INSERT INTO engine.shard_checkpoints (
				shard_id, next_stream_sequence, ready, state_hash, state_snapshot
			) VALUES (
				9, 2, true, decode(repeat('92', 32), 'hex'), '{}'
			);
			INSERT INTO engine.input_receipts (
				shard_id, input_id, stream_sequence, schema_version,
				input_hash_version, input_hash, decision_hash_version,
				decision_hash, business_input_hash_version,
				business_input_hash, resulting_state_hash, envelope, decision
			) VALUES (
				9, '019f9460-4b36-4e9b-8f44-682611f70101', 1, 1,
				1, decode(repeat('93', 32), 'hex'), 1,
				decode(repeat('94', 32), 'hex'), 1,
				decode(repeat('95', 32), 'hex'),
				decode(repeat('92', 32), 'hex'), '{}', '{}'
			);
			UPDATE trading.commands
			   SET status = 'completed', result = '{}',
			       completed_at = '2026-07-24T12:00:01Z'
			 WHERE command_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)
	}
	if err != nil {
		_ = engineTransaction.Rollback(context.Background())
		t.Fatalf("engine decision transaction: %v", err)
	}
	if err := engineTransaction.Commit(context.Background()); err != nil {
		t.Fatalf("commit engine transaction: %v", err)
	}
	assertRoleStatementDenied(t, pool, "platformgo_engine", `
		INSERT INTO engine.deployment_shard (shard_id) VALUES (10)`)
	assertRoleStatementDenied(t, pool, "platformgo_engine", `
		UPDATE trading.commands
		   SET canonical_payload = '{"tampered":true}'
		 WHERE command_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)
	assertRoleStatementDenied(t, pool, "platformgo_engine", `
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('engine-squatted-account', 10)`)

	assertRoleStatementDenied(t, pool, "platformgo_api", `
		UPDATE trading.idempotency_records
		   SET state = 'completed',
		       response_status = 200,
		       response_headers = '{}',
		       response_body = decode('7b7d', 'hex')
		 WHERE scope = 'account:role-account'
		   AND idempotency_key = 'role-command'`)

	outboxTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin outbox transaction: %v", err)
	}
	if _, err = outboxTransaction.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_outbox",
	); err == nil {
		_, err = outboxTransaction.Exec(context.Background(), `
			UPDATE messaging.outbox
			   SET attempts = attempts + 1, claimed_at = clock_timestamp()
			 WHERE message_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)
	}
	if err != nil {
		_ = outboxTransaction.Rollback(context.Background())
		t.Fatalf("outbox claim transaction: %v", err)
	}
	if err := outboxTransaction.Commit(context.Background()); err != nil {
		t.Fatalf("commit outbox transaction: %v", err)
	}
	assertRoleStatementDenied(t, pool, "platformgo_outbox", `
		UPDATE messaging.outbox
		   SET payload = '{"tampered":true}'
		 WHERE message_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)

	projectorTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin projector transaction: %v", err)
	}
	if _, err = projectorTransaction.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_projector",
	); err == nil {
		_, err = projectorTransaction.Exec(context.Background(), `
			INSERT INTO messaging.inbox (consumer, message_id)
			VALUES (
				'role-projector',
				'019f9460-4b36-4e9b-8f44-682611f70101'
			)`)
	}
	if err != nil {
		_ = projectorTransaction.Rollback(context.Background())
		t.Fatalf("projector inbox transaction: %v", err)
	}
	if err := projectorTransaction.Commit(context.Background()); err != nil {
		t.Fatalf("commit projector transaction: %v", err)
	}
	assertRoleStatementDenied(t, pool, "platformgo_projector", `
		UPDATE messaging.outbox
		   SET attempts = attempts + 1
		 WHERE message_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)

	var commandStatus string
	var receiptCount int
	var inboxCount int
	if err := pool.QueryRow(
		context.Background(),
		`SELECT status
		   FROM trading.commands
		  WHERE command_id = '019f9460-4b36-4e9b-8f44-682611f70101'`,
	).Scan(&commandStatus); err != nil {
		t.Fatalf("read role-owned command: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM engine.input_receipts",
	).Scan(&receiptCount); err != nil {
		t.Fatalf("count role-owned receipts: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM messaging.inbox",
	).Scan(&inboxCount); err != nil {
		t.Fatalf("count role-owned inbox rows: %v", err)
	}
	if commandStatus != "completed" || receiptCount != 1 || inboxCount != 1 {
		t.Fatalf(
			"role-owned effects = command %s receipts %d inbox %d",
			commandStatus,
			receiptCount,
			inboxCount,
		)
	}
}

func TestFinalBaselineMigratesWithNoCreateRole(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	dropTestMigratorRole(t, pool)

	var databaseName string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT current_database()",
	).Scan(&databaseName); err != nil {
		t.Fatalf("read test database name: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		CREATE ROLE platformgo_migrator_test
			LOGIN NOCREATEROLE PASSWORD 'platformgo-migrator-test'`); err != nil {
		t.Fatalf("create NOCREATEROLE migrator: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		"GRANT CREATE ON DATABASE "+
			pgx.Identifier{databaseName}.Sanitize()+
			" TO platformgo_migrator_test",
	); err != nil {
		t.Fatalf("grant test database create to migrator: %v", err)
	}
	t.Cleanup(func() {
		dropDurableSchemas(t, pool)
		dropTestMigratorRole(t, pool)
	})

	config, err := pgxpool.ParseConfig(os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatalf("parse test PostgreSQL configuration: %v", err)
	}
	config.ConnConfig.User = "platformgo_migrator_test"
	config.ConnConfig.Password = "platformgo-migrator-test"
	migratorPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open NOCREATEROLE migrator pool: %v", err)
	}
	defer migratorPool.Close()
	if err := migratorPool.Ping(context.Background()); err != nil {
		t.Fatalf("ping as NOCREATEROLE migrator: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		migratorPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate as NOCREATEROLE role: %v", err)
	}

	var canCreateRole bool
	if err := pool.QueryRow(context.Background(), `
		SELECT rolcreaterole
		  FROM pg_roles
		 WHERE rolname = 'platformgo_migrator_test'`,
	).Scan(&canCreateRole); err != nil {
		t.Fatalf("inspect migrator role: %v", err)
	}
	if canCreateRole {
		t.Fatal("test migrator unexpectedly has CREATEROLE")
	}
	assertFinalMigrationHistory(t, pool)
}

func TestFinalBaselineFailsWhenPreprovisionedRuntimeRoleIsMissing(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	dropDurableSchemas(t, pool)
	if _, err := pool.Exec(
		context.Background(),
		"DROP ROLE platformgo_projector",
	); err != nil {
		t.Fatalf("remove required runtime role: %v", err)
	}
	t.Cleanup(func() {
		dropDurableSchemas(t, pool)
		if _, err := pool.Exec(
			context.Background(),
			"CREATE ROLE platformgo_projector NOLOGIN",
		); err != nil {
			t.Errorf("restore required runtime role: %v", err)
		}
	})

	err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background())
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) ||
		postgresError.Code != "42501" ||
		!strings.Contains(err.Error(), "pre-provisioned runtime role") {
		t.Fatalf(
			"missing runtime role migration error = %v, want clear SQLSTATE 42501",
			err,
		)
	}
	var appliedCount int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM engine.schema_migrations",
	).Scan(&appliedCount); err != nil {
		t.Fatalf("count migrations after prerequisite failure: %v", err)
	}
	if appliedCount != 0 {
		t.Fatalf("prerequisite failure recorded %d migrations", appliedCount)
	}
}

func TestFinalBaselineRejectsUnsafeRuntimeRoleAttributes(t *testing.T) {
	for _, test := range []struct {
		name    string
		unsafe  string
		restore string
	}{
		{
			name:    "login capability",
			unsafe:  "ALTER ROLE platformgo_projector LOGIN",
			restore: "ALTER ROLE platformgo_projector NOLOGIN",
		},
		{
			name:    "role creation capability",
			unsafe:  "ALTER ROLE platformgo_projector CREATEROLE",
			restore: "ALTER ROLE platformgo_projector NOCREATEROLE",
		},
		{
			name: "privileged role membership",
			unsafe: `
				GRANT platformgo_engine TO platformgo_api`,
			restore: `
				REVOKE platformgo_engine FROM platformgo_api`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			dropDurableSchemas(t, pool)
			if _, err := pool.Exec(context.Background(), test.unsafe); err != nil {
				t.Fatalf("make runtime role unsafe: %v", err)
			}
			defer func() {
				dropDurableSchemas(t, pool)
				if _, err := pool.Exec(
					context.Background(),
					test.restore,
				); err != nil {
					t.Errorf("restore safe runtime role attributes: %v", err)
				}
			}()

			err := platformpostgres.NewMigrator(
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).Migrate(context.Background())
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) ||
				postgresError.Code != "42501" ||
				!strings.Contains(err.Error(), "missing or unsafe") {
				t.Fatalf(
					"unsafe runtime role migration error = %v, want clear SQLSTATE 42501",
					err,
				)
			}
			var appliedCount int
			var durableTableExists bool
			if err := pool.QueryRow(
				context.Background(),
				"SELECT count(*) FROM engine.schema_migrations",
			).Scan(&appliedCount); err != nil {
				t.Fatalf("count migrations after unsafe-role failure: %v", err)
			}
			if err := pool.QueryRow(
				context.Background(),
				"SELECT to_regclass('engine.shard_checkpoints') IS NOT NULL",
			).Scan(&durableTableExists); err != nil {
				t.Fatalf("inspect schema after unsafe-role failure: %v", err)
			}
			if appliedCount != 0 || durableTableExists {
				t.Fatalf(
					"unsafe role failure left applied=%d durableTable=%t",
					appliedCount,
					durableTableExists,
				)
			}
		})
	}
}

func dropTestMigratorRole(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			  FROM pg_roles
			 WHERE rolname = 'platformgo_migrator_test'
		)`,
	).Scan(&exists); err != nil {
		t.Fatalf("inspect test migrator role: %v", err)
	}
	if !exists {
		return
	}
	if _, err := pool.Exec(
		context.Background(),
		"DROP OWNED BY platformgo_migrator_test",
	); err != nil {
		t.Fatalf("drop test migrator ownership: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		"DROP ROLE platformgo_migrator_test",
	); err != nil {
		t.Fatalf("drop test migrator role: %v", err)
	}
}

func assertRoleStatementDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
	statement string,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin forbidden %s transaction: %v", role, err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+role,
	); err != nil {
		t.Fatalf("set role %s: %v", role, err)
	}
	if _, err := tx.Exec(context.Background(), statement); err == nil {
		t.Fatalf("role %s executed forbidden statement", role)
	} else {
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
			t.Fatalf(
				"role %s denial error = %v, want SQLSTATE 42501",
				role,
				err,
			)
		}
	}
}

func TestMigratorEnforcesMinimumPostgresVersion(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var versionNumber int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT current_setting('server_version_num')::integer",
	).Scan(&versionNumber); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}

	err := platformpostgres.NewMigrator(pool, fstest.MapFS{}).
		Migrate(context.Background())
	majorVersion := versionNumber / 10000
	if majorVersion < platformpostgres.MinimumPostgresMajorVersion {
		if !errors.Is(err, platformpostgres.ErrUnsupportedPostgresVersion) {
			t.Fatalf(
				"PostgreSQL %d migration error = %v, want ErrUnsupportedPostgresVersion",
				majorVersion,
				err,
			)
		}
		return
	}
	if err != nil {
		t.Fatalf("PostgreSQL %d migration error = %v, want nil", majorVersion, err)
	}
}

func TestMigratorTracksChecksumsAndRejectsHistoryDrift(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrations := fstest.MapFS{
		"20260724000100_test.up.sql": {
			Data: []byte("CREATE SCHEMA IF NOT EXISTS migration_probe;"),
		},
	}
	migrator := platformpostgres.NewMigrator(pool, migrations)
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("idempotent Migrate: %v", err)
	}

	var checksum []byte
	if err := pool.QueryRow(
		context.Background(),
		`SELECT checksum
		   FROM engine.schema_migrations
		  WHERE filename = $1`,
		"20260724000100_test.up.sql",
	).Scan(&checksum); err != nil {
		t.Fatalf("read migration checksum: %v", err)
	}
	if got := hex.EncodeToString(checksum); len(got) != 64 {
		t.Fatalf("checksum = %q, want 64 hex characters", got)
	}

	changed := fstest.MapFS{
		"20260724000100_test.up.sql": {
			Data: []byte("CREATE SCHEMA migration_probe_changed;"),
		},
	}
	err := platformpostgres.NewMigrator(pool, changed).Migrate(context.Background())
	if !errors.Is(err, platformpostgres.ErrMigrationChecksumMismatch) {
		t.Fatalf("changed migration error = %v, want ErrMigrationChecksumMismatch", err)
	}

	err = platformpostgres.NewMigrator(pool, fstest.MapFS{}).Migrate(context.Background())
	if !errors.Is(err, platformpostgres.ErrDatabaseSchemaAhead) {
		t.Fatalf("missing applied migration error = %v, want ErrDatabaseSchemaAhead", err)
	}
}

func TestMigratorFinalBaselineRerunPreservesPopulatedData(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	migrator := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	)
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("apply final baseline: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO engine.deployment_shard (shard_id)
		VALUES (41);
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('account-rerun', 41)`); err != nil {
		t.Fatalf("seed final baseline: %v", err)
	}
	var appliedAt time.Time
	if err := pool.QueryRow(
		context.Background(),
		`SELECT applied_at
		   FROM engine.schema_migrations
		  WHERE filename = '20260724000100_durable_execution_foundation.up.sql'`,
	).Scan(&appliedAt); err != nil {
		t.Fatalf("read baseline application time: %v", err)
	}
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("idempotent final baseline rerun: %v", err)
	}
	assertFinalMigrationHistory(t, pool)
	var assignedShard int64
	var appliedAtAfter time.Time
	if err := pool.QueryRow(
		context.Background(),
		"SELECT shard_id FROM engine.account_shards WHERE account_id = 'account-rerun'",
	).Scan(&assignedShard); err != nil {
		t.Fatalf("read populated row after rerun: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		`SELECT applied_at
		   FROM engine.schema_migrations
		  WHERE filename = '20260724000100_durable_execution_foundation.up.sql'`,
	).Scan(&appliedAtAfter); err != nil {
		t.Fatalf("read baseline application time after rerun: %v", err)
	}
	if assignedShard != 41 || !appliedAtAfter.Equal(appliedAt) {
		t.Fatalf(
			"rerun changed populated baseline: shard=%d applied_at=%s want 41 and %s",
			assignedShard,
			appliedAtAfter,
			appliedAt,
		)
	}
}

func TestMigratorRejectsDisposableEightFileHistoryWithoutChangingData(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	baselineName := "20260724000100_durable_execution_foundation.up.sql"
	baseline, err := os.ReadFile(filepath.Join(migrationDirectory, baselineName))
	if err != nil {
		t.Fatalf("read final baseline: %v", err)
	}
	staleHistory := fstest.MapFS{
		baselineName: {
			Data: append(append([]byte(nil), baseline...), []byte("\n-- stale development bytes\n")...),
		},
	}
	for sequence := 2; sequence <= 8; sequence++ {
		name := fmt.Sprintf(
			"20260724000%d00_stale_development_step.up.sql",
			sequence,
		)
		staleHistory[name] = &fstest.MapFile{Data: []byte("SELECT 1;\n")}
	}
	if err := platformpostgres.NewMigrator(pool, staleHistory).
		Migrate(context.Background()); err != nil {
		t.Fatalf("apply stale eight-file history: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO engine.deployment_shard (shard_id)
		VALUES (17);
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('stale-account', 17)`); err != nil {
		t.Fatalf("seed stale history: %v", err)
	}

	err = platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(context.Background())
	if !errors.Is(err, platformpostgres.ErrMigrationChecksumMismatch) &&
		!errors.Is(err, platformpostgres.ErrDatabaseSchemaAhead) {
		t.Fatalf(
			"final baseline over stale history error = %v, want history refusal",
			err,
		)
	}
	var migrationCount int
	var shardID int64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM engine.schema_migrations",
	).Scan(&migrationCount); err != nil {
		t.Fatalf("count stale history after refusal: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT shard_id FROM engine.account_shards WHERE account_id = 'stale-account'",
	).Scan(&shardID); err != nil {
		t.Fatalf("read stale data after refusal: %v", err)
	}
	if migrationCount != 8 || shardID != 17 {
		t.Fatalf(
			"refusal changed stale history: migrations=%d shard=%d",
			migrationCount,
			shardID,
		)
	}
}

func TestAccountSummaryMigrationUsesBoundedLockAndPreservesExistingAccounts(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(
		t,
		"20260726000300_phase3_fill_filter_read_model.up.sql",
	)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 43); err != nil {
		t.Fatalf("apply previous account schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:summary-upgrade', 'HEDGING')`,
	); err != nil {
		t.Fatalf("seed existing account: %v", err)
	}

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin account-summary lock: %v", err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(ctx, `
		SELECT account_id
		  FROM trading.accounts
		 WHERE account_id = 'urn:xb:account:summary-upgrade'`,
	); err != nil {
		t.Fatalf("hold account read lock: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	startedAt := time.Now()
	err = current.Migrate(ctx)
	elapsed := time.Since(startedAt)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf(
			"contended account-summary upgrade error = %v, want SQLSTATE 55P03",
			err,
		)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf(
			"bounded account-summary lock wait = %s, want approximately 5s",
			elapsed,
		)
	}
	var (
		lastMigration    string
		statusExists     bool
		marginModeExists bool
		accountCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			EXISTS (
				SELECT 1
				  FROM information_schema.columns
				 WHERE table_schema = 'trading'
				   AND table_name = 'accounts'
				   AND column_name = 'status'
			),
			EXISTS (
				SELECT 1
				  FROM information_schema.columns
				 WHERE table_schema = 'trading'
				   AND table_name = 'accounts'
				   AND column_name = 'margin_mode'
			),
			(SELECT count(*) FROM trading.accounts)`,
	).Scan(
		&lastMigration,
		&statusExists,
		&marginModeExists,
		&accountCount,
	); err != nil {
		t.Fatalf("inspect rolled-back account-summary migration: %v", err)
	}
	if lastMigration != "20260726000300_phase3_fill_filter_read_model.up.sql" ||
		statusExists ||
		marginModeExists ||
		accountCount != 1 {
		t.Fatalf(
			"contended account-summary state = last %q status %t margin %t accounts %d",
			lastMigration,
			statusExists,
			marginModeExists,
			accountCount,
		)
	}

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	columnsOnly := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(
			t,
			"20260726000400_phase3_account_summary_read_model.up.sql",
		),
	)
	if err := columnsOnly.Migrate(ctx); err != nil {
		t.Fatalf("retry account-summary column phase: %v", err)
	}
	var constraintCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM pg_constraint
		 WHERE conrelid = 'trading.accounts'::regclass
		   AND conname IN (
				'accounts_status_check',
				'accounts_margin_mode_check'
		   )`,
	).Scan(&constraintCount); err != nil {
		t.Fatalf("inspect account-summary column phase: %v", err)
	}
	if constraintCount != 0 {
		t.Fatalf(
			"column phase installed %d account-summary constraints",
			constraintCount,
		)
	}

	constraintsOnly := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(
			t,
			"20260726000500_phase3_account_summary_constraints.up.sql",
		),
	)
	if err := constraintsOnly.Migrate(ctx); err != nil {
		t.Fatalf("apply account-summary constraint phase: %v", err)
	}
	var unvalidatedCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM pg_constraint
		 WHERE conrelid = 'trading.accounts'::regclass
		   AND conname IN (
				'accounts_status_check',
				'accounts_margin_mode_check'
		   )
		   AND NOT convalidated`,
	).Scan(&unvalidatedCount); err != nil {
		t.Fatalf("inspect account-summary constraint phase: %v", err)
	}
	if unvalidatedCount != 2 {
		t.Fatalf(
			"unvalidated account-summary constraints = %d, want 2",
			unvalidatedCount,
		)
	}

	validationProbe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin account-summary validation lock probe: %v", err)
	}
	if _, err := validationProbe.Exec(ctx, `
		ALTER TABLE trading.accounts
		VALIDATE CONSTRAINT accounts_status_check`,
	); err != nil {
		_ = validationProbe.Rollback(ctx)
		t.Fatalf("validate status constraint in lock probe: %v", err)
	}
	var validationPID int32
	if err := validationProbe.QueryRow(
		ctx,
		"SELECT pg_backend_pid()",
	).Scan(&validationPID); err != nil {
		_ = validationProbe.Rollback(ctx)
		t.Fatalf("read validation lock probe PID: %v", err)
	}
	var validationLocks []string
	if err := pool.QueryRow(ctx, `
		SELECT array_agg(mode ORDER BY mode)
		  FROM pg_locks
		 WHERE pid = $1
		   AND relation = 'trading.accounts'::regclass
		   AND granted`,
		validationPID,
	).Scan(&validationLocks); err != nil {
		_ = validationProbe.Rollback(ctx)
		t.Fatalf("inspect validation locks: %v", err)
	}
	var hasShareUpdateExclusive bool
	for _, mode := range validationLocks {
		if mode == "AccessExclusiveLock" {
			_ = validationProbe.Rollback(ctx)
			t.Fatalf(
				"validation retained ACCESS EXCLUSIVE: %v",
				validationLocks,
			)
		}
		hasShareUpdateExclusive = hasShareUpdateExclusive ||
			mode == "ShareUpdateExclusiveLock"
	}
	if !hasShareUpdateExclusive {
		_ = validationProbe.Rollback(ctx)
		t.Fatalf(
			"validation locks = %v, want SHARE UPDATE EXCLUSIVE",
			validationLocks,
		)
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, 2*time.Second)
	var concurrentlyRead string
	if err := pool.QueryRow(writeCtx, `
		SELECT account_id
		  FROM trading.accounts
		 WHERE account_id = 'urn:xb:account:summary-upgrade'`,
	).Scan(&concurrentlyRead); err != nil {
		cancelWrite()
		_ = validationProbe.Rollback(ctx)
		t.Fatalf("ordinary read blocked by validation lock: %v", err)
	}
	var insertedStatus string
	var insertedMarginMode string
	if err := pool.QueryRow(writeCtx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:summary-old-binary', 'NETTING')
		RETURNING status, margin_mode`,
	).Scan(&insertedStatus, &insertedMarginMode); err != nil {
		cancelWrite()
		_ = validationProbe.Rollback(ctx)
		t.Fatalf("old-binary insert blocked by validation lock: %v", err)
	}
	cancelWrite()
	if concurrentlyRead != "urn:xb:account:summary-upgrade" ||
		insertedStatus != "ACTIVE" ||
		insertedMarginMode != "CROSS" {
		_ = validationProbe.Rollback(ctx)
		t.Fatalf(
			"concurrent validation access = read %q status %q margin %q",
			concurrentlyRead,
			insertedStatus,
			insertedMarginMode,
		)
	}
	if err := validationProbe.Rollback(ctx); err != nil {
		t.Fatalf("rollback validation lock probe: %v", err)
	}

	validationBlocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin account-summary validation blocker: %v", err)
	}
	if _, err := validationBlocker.Exec(
		ctx,
		"LOCK TABLE trading.accounts IN SHARE MODE",
	); err != nil {
		_ = validationBlocker.Rollback(ctx)
		t.Fatalf("lock account table against validation: %v", err)
	}
	validationStartedAt := time.Now()
	err = current.Migrate(ctx)
	validationElapsed := time.Since(validationStartedAt)
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		_ = validationBlocker.Rollback(ctx)
		t.Fatalf(
			"contended account-summary validation error = %v, want SQLSTATE 55P03",
			err,
		)
	}
	if validationElapsed < 4*time.Second || validationElapsed > 8*time.Second {
		_ = validationBlocker.Rollback(ctx)
		t.Fatalf(
			"bounded account-summary validation wait = %s, want approximately 5s",
			validationElapsed,
		)
	}
	var validationTip string
	if err := pool.QueryRow(ctx, `
		SELECT max(filename)
		  FROM engine.schema_migrations`,
	).Scan(&validationTip); err != nil {
		_ = validationBlocker.Rollback(ctx)
		t.Fatalf("inspect rolled-back validation history: %v", err)
	}
	if validationTip !=
		"20260726000500_phase3_account_summary_constraints.up.sql" {
		_ = validationBlocker.Rollback(ctx)
		t.Fatalf(
			"contended validation migration tip = %q",
			validationTip,
		)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM pg_constraint
		 WHERE conrelid = 'trading.accounts'::regclass
		   AND conname IN (
				'accounts_status_check',
				'accounts_margin_mode_check'
		   )
		   AND NOT convalidated`,
	).Scan(&unvalidatedCount); err != nil {
		_ = validationBlocker.Rollback(ctx)
		t.Fatalf("inspect rolled-back validation constraints: %v", err)
	}
	if unvalidatedCount != 2 {
		_ = validationBlocker.Rollback(ctx)
		t.Fatalf(
			"constraints after failed validation = %d unvalidated, want 2",
			unvalidatedCount,
		)
	}
	if err := validationBlocker.Rollback(ctx); err != nil {
		t.Fatalf("release account-summary validation blocker: %v", err)
	}

	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply account-summary validation phase: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried account-summary upgrade: %v", err)
	}
	assertFinalMigrationHistory(t, pool)
	var validatedCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM pg_constraint
		 WHERE conrelid = 'trading.accounts'::regclass
		   AND conname IN (
				'accounts_status_check',
				'accounts_margin_mode_check'
		   )
		   AND convalidated`,
	).Scan(&validatedCount); err != nil {
		t.Fatalf("inspect account-summary validation phase: %v", err)
	}
	if validatedCount != 2 {
		t.Fatalf(
			"validated account-summary constraints = %d, want 2",
			validatedCount,
		)
	}

	var statusValue string
	var marginMode string
	var omsMode string
	if err := pool.QueryRow(ctx, `
		SELECT status, margin_mode, oms_mode
		  FROM trading.accounts
		 WHERE account_id = 'urn:xb:account:summary-upgrade'`,
	).Scan(&statusValue, &marginMode, &omsMode); err != nil {
		t.Fatalf("read upgraded account: %v", err)
	}
	if statusValue != "ACTIVE" ||
		marginMode != "CROSS" ||
		omsMode != "HEDGING" {
		t.Fatalf(
			"upgraded account = status %q margin %q oms %q",
			statusValue,
			marginMode,
			omsMode,
		)
	}
	for name, statement := range map[string]string{
		"invalid status": `
			UPDATE trading.accounts
			   SET status = 'active'
			 WHERE account_id = 'urn:xb:account:summary-upgrade'`,
		"invalid margin mode": `
			UPDATE trading.accounts
			   SET margin_mode = 'cross'
			 WHERE account_id = 'urn:xb:account:summary-upgrade'`,
	} {
		if _, err := pool.Exec(ctx, statement); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestUserAPIKeyMigrationUsesBoundedLockAndPreservesExistingUsers(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(
		t,
		"20260726000600_phase3_account_summary_constraint_validation.up.sql",
	)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 47); err != nil {
		t.Fatalf("apply previous API-key schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login
		) VALUES (
			'urn:xb:user:key-upgrade', 'key-upgrade', 'key-upgrade'
		)`,
	); err != nil {
		t.Fatalf("seed existing API-key owner: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin API-key migration blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(
		ctx,
		"LOCK TABLE identity.users IN SHARE MODE",
	); err != nil {
		t.Fatalf("lock identity users against API-key migration: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	startedAt := time.Now()
	err = current.Migrate(ctx)
	elapsed := time.Since(startedAt)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf(
			"contended API-key migration error = %v, want SQLSTATE 55P03",
			err,
		)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf(
			"bounded API-key migration wait = %s, want approximately 5s",
			elapsed,
		)
	}
	var (
		lastMigration string
		auditExists   bool
		keysExist     bool
		userCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			EXISTS (
				SELECT 1
				  FROM information_schema.schemata
				 WHERE schema_name = 'audit'
			),
			to_regclass('identity.api_keys') IS NOT NULL,
			(
				SELECT count(*)
				  FROM identity.users
				 WHERE user_id = 'urn:xb:user:key-upgrade'
			)`,
	).Scan(
		&lastMigration,
		&auditExists,
		&keysExist,
		&userCount,
	); err != nil {
		t.Fatalf("inspect rolled-back API-key migration: %v", err)
	}
	if lastMigration !=
		"20260726000600_phase3_account_summary_constraint_validation.up.sql" ||
		auditExists ||
		keysExist ||
		userCount != 1 {
		t.Fatalf(
			"contended API-key state = last %q audit %t keys %t users %d",
			lastMigration,
			auditExists,
			keysExist,
			userCount,
		)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry API-key migration: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried API-key migration: %v", err)
	}
	if err := platformpostgres.NewMigrator(pool, previous).
		VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf(
			"prior binary verification after API-key migration = %v, want schema-ahead",
			err,
		)
	}
	assertFinalMigrationHistory(t, pool)

	var (
		apiCanInsert       bool
		apiCanExecute      bool
		apiCanReplay       bool
		apiCanVerifyPolicy bool
		apiCanPurgeReplay  bool
		apiCanReadCoverage bool
		apiCanUpdatePolicy bool
		apiCanReadReplay   bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			has_table_privilege(
				'platformgo_api',
				'identity.api_keys',
				'INSERT'
			),
				has_function_privilege(
					'platformgo_api',
					'identity.create_user_api_key(text,uuid,text,bytea,text,text[],uuid,text,bytea,bytea,text,bytea,bytea)',
					'EXECUTE'
				),
				has_function_privilege(
					'platformgo_api',
					'identity.replay_user_api_key(text,bytea,bytea)',
					'EXECUTE'
				),
				has_function_privilege(
					'platformgo_api',
					'identity.verify_api_key_policy(bigint,bigint,numeric,numeric)',
					'EXECUTE'
				),
				has_function_privilege(
					'platformgo_api',
					'identity.purge_expired_api_key_replays(integer)',
					'EXECUTE'
				),
				has_function_privilege(
					'platformgo_api',
					'identity.api_key_replay_coverage()',
					'EXECUTE'
				),
				has_table_privilege(
				'platformgo_api',
				'identity.api_key_policy',
				'UPDATE'
			),
			has_table_privilege(
				'platformgo_api',
				'identity.api_key_replays',
				'SELECT'
			)`,
	).Scan(
		&apiCanInsert,
		&apiCanExecute,
		&apiCanReplay,
		&apiCanVerifyPolicy,
		&apiCanPurgeReplay,
		&apiCanReadCoverage,
		&apiCanUpdatePolicy,
		&apiCanReadReplay,
	); err != nil {
		t.Fatalf("inspect API-key role boundary: %v", err)
	}
	if apiCanInsert ||
		!apiCanExecute ||
		!apiCanReplay ||
		!apiCanVerifyPolicy ||
		!apiCanPurgeReplay ||
		!apiCanReadCoverage ||
		apiCanUpdatePolicy ||
		apiCanReadReplay {
		t.Fatalf(
			"API-key role boundary = insert %t create %t replay %t verify %t purge %t coverage %t policy-update %t replay-read %t",
			apiCanInsert,
			apiCanExecute,
			apiCanReplay,
			apiCanVerifyPolicy,
			apiCanPurgeReplay,
			apiCanReadCoverage,
			apiCanUpdatePolicy,
			apiCanReadReplay,
		)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE identity.api_key_policy
		   SET max_active_per_owner = 26,
		       client_rate_limit_max_requests = 4294967295,
		       client_rate_limit_window_seconds = 18446744073709551615,
		       idempotency_ttl_seconds = 18446744073709551615
		 WHERE singleton`); err != nil {
		t.Fatalf("set source-domain boundary policy: %v", err)
	}
	var boundaryPolicyMatches bool
	if err := pool.QueryRow(ctx, `
		SELECT identity.verify_api_key_policy(
			26,
			4294967295,
			18446744073709551615,
			18446744073709551615
		)`,
	).Scan(&boundaryPolicyMatches); err != nil || !boundaryPolicyMatches {
		t.Fatalf(
			"source-domain boundary policy = %t, error = %v",
			boundaryPolicyMatches,
			err,
		)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE identity.api_key_policy
		   SET version = 2,
		       max_active_per_owner = 1,
		       client_rate_limit_max_requests = 600,
		       client_rate_limit_window_seconds = 60,
		       idempotency_ttl_seconds = 86400
		 WHERE singleton`); err != nil {
		t.Fatalf("set durable API-key test policy: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		SELECT identity.create_user_api_key(
			'urn:xb:user:key-upgrade',
			'00000000-0000-4000-8000-000000000701',
			'upgrade-key',
			decode(repeat('01', 32), 'hex'),
			'000000000701',
			ARRAY['orders:write'],
			'00000000-0000-4000-8000-000000000702',
			'request-upgrade-key',
			decode(repeat('09', 32), 'hex'),
			decode(repeat('02', 32), 'hex'),
			'test-v1',
			decode(repeat('03', 12), 'hex'),
			decode(repeat('04', 17), 'hex')
		)`,
	); err != nil {
		t.Fatalf("create API key through authority function: %v", err)
	}
	_, missingIdempotencyErr := pool.Exec(ctx, `
		SELECT identity.create_user_api_key(
			'urn:xb:user:key-upgrade',
			'00000000-0000-4000-8000-000000000705',
			'missing-idempotency',
			decode(repeat('0f', 32), 'hex'),
			'000000000705',
			ARRAY[]::text[],
			'00000000-0000-4000-8000-000000000706',
			'request-missing-idempotency',
			''::bytea,
			decode(repeat('10', 32), 'hex'),
			'test-v1',
			decode(repeat('11', 12), 'hex'),
			decode(repeat('12', 17), 'hex')
		)`)
	var missingIdempotencyPostgresError *pgconn.PgError
	if !errors.As(
		missingIdempotencyErr,
		&missingIdempotencyPostgresError,
	) ||
		missingIdempotencyPostgresError.Code != "22023" {
		t.Fatalf(
			"missing-idempotency authority error = %v, want SQLSTATE 22023",
			missingIdempotencyErr,
		)
	}
	var capOutcome string
	if err := pool.QueryRow(ctx, `
			SELECT outcome
			  FROM identity.create_user_api_key(
				'urn:xb:user:key-upgrade',
			'00000000-0000-4000-8000-000000000703',
			'over-cap-key',
			decode(repeat('05', 32), 'hex'),
			'000000000703',
			ARRAY[]::text[],
			'00000000-0000-4000-8000-000000000704',
			'request-over-cap-key',
			decode(repeat('0a', 32), 'hex'),
			decode(repeat('06', 32), 'hex'),
			'test-v1',
			decode(repeat('07', 12), 'hex'),
				decode(repeat('08', 17), 'hex')
			)`).Scan(&capOutcome); err != nil {
		t.Fatalf("durable owner-cap outcome: %v", err)
	}
	if capOutcome != "cap_conflict" {
		t.Fatalf("durable owner-cap outcome = %q", capOutcome)
	}
	var (
		auditConfigurationVersion int
		auditEffectiveMax         int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(detail->>'configurationVersion')::integer,
			(detail->>'effectiveMaxActive')::integer
		  FROM audit.events
		 WHERE event_id = '00000000-0000-4000-8000-000000000702'`,
	).Scan(
		&auditConfigurationVersion,
		&auditEffectiveMax,
	); err != nil {
		t.Fatal(err)
	}
	if auditConfigurationVersion != 2 || auditEffectiveMax != 1 {
		t.Fatalf(
			"audit policy = version %d max %d",
			auditConfigurationVersion,
			auditEffectiveMax,
		)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE identity.api_keys
		   SET revoked_at = created_at + interval '1 microsecond'
		 WHERE api_key_id = '00000000-0000-4000-8000-000000000701'`,
	); err != nil {
		t.Fatalf("first API-key revocation: %v", err)
	}
	for name, statement := range map[string]string{
		"second revocation": `
			UPDATE identity.api_keys
			   SET revoked_at = '2026-07-26T07:03:00Z'
			 WHERE api_key_id = '00000000-0000-4000-8000-000000000701'`,
		"key deletion": `
			DELETE FROM identity.api_keys
			 WHERE api_key_id = '00000000-0000-4000-8000-000000000701'`,
		"audit mutation": `
			UPDATE audit.events
			   SET outcome = 'failure'
			 WHERE event_id = '00000000-0000-4000-8000-000000000702'`,
	} {
		if _, err := pool.Exec(ctx, statement); err == nil {
			t.Fatalf("%s was accepted", name)
		}
		var policyMatches bool
		if err := pool.QueryRow(ctx, `
			SELECT identity.verify_api_key_policy(1,600,60,86400)`,
		).Scan(&policyMatches); err != nil || !policyMatches {
			t.Fatalf(
				"matching legacy policy = %t, error = %v",
				policyMatches,
				err,
			)
		}
		if err := pool.QueryRow(ctx, `
			SELECT identity.verify_api_key_policy(2,600,60,86400)`,
		).Scan(&policyMatches); err != nil || policyMatches {
			t.Fatalf(
				"mismatched legacy policy = %t, error = %v",
				policyMatches,
				err,
			)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO identity.api_key_replays (
				owner_user_id,
				idempotency_key_hash,
				request_hash,
				response_status,
				replay_key_id,
				response_nonce,
				response_ciphertext,
				created_at,
				expires_at
			) VALUES
			(
				'urn:xb:user:key-upgrade',
				decode(repeat('0c', 32), 'hex'),
				decode(repeat('0c', 32), 'hex'),
				201,
				'test-v1',
				decode(repeat('0d', 12), 'hex'),
				decode(repeat('0e', 17), 'hex'),
				statement_timestamp() - interval '2 seconds',
				statement_timestamp() - interval '1 second'
			),
			(
				'urn:xb:user:key-upgrade',
				decode(repeat('1c', 32), 'hex'),
				decode(repeat('1d', 32), 'hex'),
				201,
				'test-v1',
				decode(repeat('1e', 12), 'hex'),
				decode(repeat('1f', 17), 'hex'),
				statement_timestamp() - interval '3 seconds',
				statement_timestamp() - interval '2 seconds'
			),
			(
				'urn:xb:user:key-upgrade',
				decode(repeat('2c', 32), 'hex'),
				decode(repeat('2d', 32), 'hex'),
				201,
				'test-v1',
				decode(repeat('2e', 12), 'hex'),
				decode(repeat('2f', 17), 'hex'),
				statement_timestamp() - interval '4 seconds',
				statement_timestamp() - interval '3 seconds'
			),
			(
				'urn:xb:user:key-upgrade',
				decode(repeat('09', 32), 'hex'),
				decode(repeat('09', 32), 'hex'),
				201,
				'test-v1',
				decode(repeat('0a', 12), 'hex'),
				decode(repeat('0b', 17), 'hex'),
				statement_timestamp(),
				statement_timestamp() + interval '1 hour'
			)
			ON CONFLICT (owner_user_id, idempotency_key_hash) DO UPDATE
			   SET created_at = EXCLUDED.created_at,
			       expires_at = EXCLUDED.expires_at`); err != nil {
			t.Fatalf("seed replay cleanup rows: %v", err)
		}
		var deleted int
		if err := pool.QueryRow(ctx, `
			SELECT identity.purge_expired_api_key_replays(2)`,
		).Scan(&deleted); err != nil || deleted != 2 {
			t.Fatalf("first replay cleanup = %d, error = %v", deleted, err)
		}
		if err := pool.QueryRow(ctx, `
			SELECT identity.purge_expired_api_key_replays(2)`,
		).Scan(&deleted); err != nil || deleted != 1 {
			t.Fatalf("second replay cleanup = %d, error = %v", deleted, err)
		}
		if err := pool.QueryRow(ctx, `
			SELECT identity.purge_expired_api_key_replays(2)`,
		).Scan(&deleted); err != nil || deleted != 0 {
			t.Fatalf("repeated replay cleanup = %d, error = %v", deleted, err)
		}
		var liveReplayCount int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			 FROM identity.api_key_replays
			 WHERE idempotency_key_hash =
			       decode(repeat('09', 32), 'hex')`,
		).Scan(&liveReplayCount); err != nil || liveReplayCount != 1 {
			t.Fatalf(
				"live replay count = %d, error = %v",
				liveReplayCount,
				err,
			)
		}
		var (
			coverageKey    string
			coverageCount  int64
			coverageOldest string
		)
		if err := pool.QueryRow(ctx, `
			SELECT replay_key_id, live_count, oldest_expires_at
			  FROM identity.api_key_replay_coverage()
			 WHERE replay_key_id = 'test-v1'`,
		).Scan(
			&coverageKey,
			&coverageCount,
			&coverageOldest,
		); err != nil {
			t.Fatal(err)
		}
		if coverageKey != "test-v1" ||
			coverageCount != 1 ||
			coverageOldest == "" {
			t.Fatalf(
				"replay coverage = key %q count %d oldest %q",
				coverageKey,
				coverageCount,
				coverageOldest,
			)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.api_key_replays (
			owner_user_id,
			idempotency_key_hash,
			request_hash,
			response_status,
			replay_key_id,
			response_nonce,
			response_ciphertext,
			created_at,
			expires_at
		)
		SELECT
			'urn:xb:user:key-upgrade',
			decode(lpad(to_hex(series), 64, '0'), 'hex'),
			decode(repeat('5a', 32), 'hex'),
			201,
			'concurrent-cleanup-v1',
			decode(repeat('5b', 12), 'hex'),
			decode(repeat('5c', 17), 'hex'),
			statement_timestamp() - interval '2 seconds',
			statement_timestamp() - interval '1 second'
		  FROM generate_series(101, 104) AS series`); err != nil {
		t.Fatal(err)
	}
	cleanupResults := make(chan int, 2)
	cleanupErrors := make(chan error, 2)
	var cleanupWait sync.WaitGroup
	for range 2 {
		cleanupWait.Add(1)
		go func() {
			defer cleanupWait.Done()
			var deleted int
			err := pool.QueryRow(
				context.Background(),
				`SELECT identity.purge_expired_api_key_replays(2)`,
			).Scan(&deleted)
			cleanupResults <- deleted
			cleanupErrors <- err
		}()
	}
	cleanupWait.Wait()
	close(cleanupResults)
	close(cleanupErrors)
	for err := range cleanupErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var concurrentlyDeleted int
	for deleted := range cleanupResults {
		concurrentlyDeleted += deleted
	}
	if concurrentlyDeleted != 4 {
		t.Fatalf(
			"concurrent cleanup deleted %d rows, want 4",
			concurrentlyDeleted,
		)
	}
}

func assertFinalMigrationHistory(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var count int
	var first string
	var last string
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*), min(filename), max(filename)
		  FROM engine.schema_migrations`,
	).Scan(&count, &first, &last); err != nil {
		t.Fatalf("inspect final migration history: %v", err)
	}
	if count != 19 ||
		first != "20260724000100_durable_execution_foundation.up.sql" ||
		last != "20260726000700_phase3_user_api_keys.up.sql" {
		t.Fatalf(
			"final migration history = count %d first %q last %q",
			count,
			first,
			last,
		)
	}
}

func assertRealtimeRoleBoundary(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:role-realtime', 'NETTING');
		INSERT INTO identity.users (user_id, login, normalized_login)
		VALUES ('urn:xb:user:role-realtime', 'role-realtime', 'role-realtime');
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES (
			'urn:xb:user:role-realtime',
			'urn:xb:account:role-realtime'
		)`,
	); err != nil {
		t.Fatalf("seed realtime role boundary: %v", err)
	}
	engineTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engineTransaction.Exec(ctx, "SET LOCAL ROLE platformgo_engine"); err == nil {
		_, err = engineTransaction.Exec(ctx, `
			SELECT realtime.allocate_channel_sequence('user:role-realtime');
			INSERT INTO realtime.publications (
				channel, event_id, sequence, schema_version, event_type,
				account_id, logical_time, data
			) VALUES (
				'user:role-realtime',
				'019f9460-4b36-4e9b-8f44-682611f71101',
				1,
				1,
				'order.updated',
				'urn:xb:account:role-realtime',
				1,
				'{"status":"working"}'
			)`)
	}
	if err != nil {
		_ = engineTransaction.Rollback(ctx)
		t.Fatalf("engine realtime insert: %v", err)
	}
	if err := engineTransaction.Commit(ctx); err != nil {
		t.Fatalf("commit engine realtime insert: %v", err)
	}

	realtimeTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = realtimeTransaction.Exec(
		ctx,
		"SET LOCAL ROLE platformgo_realtime",
	); err == nil {
		_, err = realtimeTransaction.Exec(ctx, `
			UPDATE realtime.publications
			   SET attempts = attempts + 1,
			       claimed_at = clock_timestamp()
			 WHERE channel = 'user:role-realtime'
			   AND event_id = '019f9460-4b36-4e9b-8f44-682611f71101'`)
	}
	if err != nil {
		_ = realtimeTransaction.Rollback(ctx)
		t.Fatalf("realtime role claim: %v", err)
	}
	if err := realtimeTransaction.Commit(ctx); err != nil {
		t.Fatalf("commit realtime role claim: %v", err)
	}

	assertRoleStatementDenied(t, pool, "platformgo_realtime", `
		UPDATE realtime.publications
		   SET data = '{"status":"tampered"}'
		 WHERE channel = 'user:role-realtime'`)
	assertRoleStatementDenied(t, pool, "platformgo_realtime", `
		INSERT INTO messaging.inbox (consumer, message_id)
		VALUES ('realtime-cross-authority', gen_random_uuid())`)
	assertRoleStatementDenied(t, pool, "platformgo_projector", `
		SELECT channel FROM realtime.publications`)
	if _, err := pool.Exec(ctx, `
		UPDATE realtime.publications
		   SET claimed_at = NULL,
		       last_error = 'invalid permanent failure',
		       failure_class = 'permanent',
		       quarantined_at = NULL
		 WHERE channel = 'user:role-realtime'`); err == nil {
		t.Fatal("invalid realtime failure/quarantine state was accepted")
	} else {
		var postgresErr *pgconn.PgError
		if !errors.As(err, &postgresErr) || postgresErr.Code != "23514" {
			t.Fatalf(
				"invalid realtime failure state error=%v, want SQLSTATE 23514",
				err,
			)
		}
	}
	if _, err := pool.Exec(ctx, `
		UPDATE realtime.publications
		   SET claimed_at = NULL,
		       last_error = 'permanent failure',
		       failure_class = 'permanent',
		       quarantined_at = clock_timestamp()
		 WHERE channel = 'user:role-realtime'`); err != nil {
		t.Fatalf("seed realtime quarantine: %v", err)
	}
	realtimeLogin := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_realtime_member_test",
		"platformgo_realtime",
	)
	if _, err := realtimeLogin.Exec(ctx, `
		UPDATE realtime.publications
		   SET quarantined_at = NULL
		 WHERE channel = 'user:role-realtime'`); err == nil {
		t.Fatal("inherited realtime login bypassed audited quarantine repair")
	} else {
		var postgresErr *pgconn.PgError
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42501" {
			t.Fatalf(
				"inherited realtime quarantine error=%v, want SQLSTATE 42501",
				err,
			)
		}
	}
	assertRoleStatementDenied(t, pool, "platformgo_realtime", `
		UPDATE realtime.publications
		   SET quarantined_at = NULL
		 WHERE channel = 'user:role-realtime'`)
	assertRoleStatementDenied(t, pool, "platformgo_realtime_repair", `
		UPDATE realtime.publications
		   SET quarantined_at = NULL
		 WHERE channel = 'user:role-realtime'`)
	assertRoleStatementDenied(t, pool, "platformgo_realtime_repair", `
		UPDATE realtime.publications
		   SET data = '{"status":"tampered"}'
		 WHERE channel = 'user:role-realtime'`)
	repairLogin := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_realtime_repair_test",
		"platformgo_realtime_repair",
	)
	var repairWait sync.WaitGroup
	repairErrors := make(chan error, 2)
	repairStart := make(chan struct{})
	for range 2 {
		repairWait.Add(1)
		go func() {
			defer repairWait.Done()
			<-repairStart
			_, repairErr := repairLogin.Exec(ctx, `
				SELECT realtime.requeue_publication(
					'019f9460-4b36-4e9b-8f44-682611f71102',
					'user:role-realtime',
					'019f9460-4b36-4e9b-8f44-682611f71101',
					'operator@example.test',
					'verified Centrifugo configuration repair'
				)`)
			repairErrors <- repairErr
		}()
	}
	close(repairStart)
	repairWait.Wait()
	close(repairErrors)
	for repairErr := range repairErrors {
		if repairErr != nil {
			t.Fatalf("concurrent audited realtime repair: %v", repairErr)
		}
	}
	if _, err := repairLogin.Exec(ctx, `
		SELECT realtime.requeue_publication(
			'019f9460-4b36-4e9b-8f44-682611f71102',
			'user:role-realtime',
			'019f9460-4b36-4e9b-8f44-682611f71101',
			'operator@example.test',
			'verified Centrifugo configuration repair'
		)`); err != nil {
		t.Fatalf("replay identical realtime repair: %v", err)
	}
	if _, err := repairLogin.Exec(ctx, `
		SELECT realtime.requeue_publication(
			'019f9460-4b36-4e9b-8f44-682611f71102',
			'user:role-realtime',
			'019f9460-4b36-4e9b-8f44-682611f71101',
			'other-operator@example.test',
			'changed request'
		)`); err == nil {
		t.Fatal("conflicting realtime repair request identity was accepted")
	}
	var (
		repairAuditCount      int
		repairAuthenticatedBy string
		repairClaimedActor    string
	)
	if err := pool.QueryRow(ctx, `
		SELECT count(*), min(authenticated_actor), min(claimed_actor)
		  FROM realtime.publication_requeues
		 WHERE request_id = '019f9460-4b36-4e9b-8f44-682611f71102'`,
	).Scan(
		&repairAuditCount,
		&repairAuthenticatedBy,
		&repairClaimedActor,
	); err != nil ||
		repairAuditCount != 1 ||
		repairAuthenticatedBy != "platformgo_realtime_repair_test" ||
		repairClaimedActor != "operator@example.test" {
		t.Fatalf(
			"realtime repair audit count=%d authenticated=%q claimed=%q error=%v",
			repairAuditCount,
			repairAuthenticatedBy,
			repairClaimedActor,
			err,
		)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE realtime.publications
		   SET claimed_at = clock_timestamp(),
		       published_at = clock_timestamp(),
		       last_error = NULL,
		       failure_class = NULL
		 WHERE channel = 'user:role-realtime'`); err != nil {
		t.Fatalf("seed immutable published delivery: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE realtime.publications
		   SET attempts = attempts + 1
		 WHERE channel = 'user:role-realtime'`); err == nil {
		t.Fatal("published realtime delivery state was mutable")
	} else {
		var postgresErr *pgconn.PgError
		if !errors.As(err, &postgresErr) || postgresErr.Code != "55000" {
			t.Fatalf(
				"published realtime immutability error=%v, want SQLSTATE 55000",
				err,
			)
		}
	}
	assertRoleStatementDenied(t, pool, "platformgo_engine", `
		UPDATE realtime.channel_sequences
		   SET last_sequence = 999
		 WHERE channel = 'user:role-realtime'`)
	assertRoleStatementDenied(t, pool, "platformgo_api", `
		SELECT * FROM realtime.publications`)
	assertRoleStatementDenied(t, pool, "platformgo_outbox", `
		UPDATE realtime.publications SET attempts = attempts + 1`)
}

func assertCommandIdempotencyAuthorityConstraints(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	ctx := context.Background()
	commandID := "019f9460-4b36-4e9b-8f44-682611f7ef01"
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin paired command transaction: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES ($1, 'authority-account', 1, 'probe', 1, '{}', 'pending', 1)`,
		commandID,
	); err == nil {
		_, err = tx.Exec(ctx, `
			INSERT INTO trading.idempotency_records (
				scope, idempotency_key, request_hash, command_id, state, expires_at
			) VALUES (
				'account:authority-account', 'probe',
				decode(repeat('91', 32), 'hex'), $1, 'in_progress',
				'2026-07-26T00:00:00Z'
			)`,
			commandID,
		)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert paired command authority: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit paired command authority: %v", err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin command-without-idempotency transaction: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019f9460-4b36-4e9b-8f44-682611f7ef02',
			'authority-account', 2, 'probe', 1, '{}', 'pending', 2
		)`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert deferred command probe: %v", err)
	}
	if err := tx.Commit(ctx); !isPostgresCode(err, "23503") {
		t.Fatalf("command without idempotency commit error = %v, want 23503", err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin orphan-idempotency transaction: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:authority-account', 'orphan',
			decode(repeat('92', 32), 'hex'),
			'019f9460-4b36-4e9b-8f44-682611f7ef03',
			'in_progress', '2026-07-26T00:00:00Z'
		)`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert deferred orphan idempotency probe: %v", err)
	}
	if err := tx.Commit(ctx); !isPostgresCode(err, "23503") {
		t.Fatalf("orphan idempotency commit error = %v, want 23503", err)
	}

	if _, err := pool.Exec(
		ctx,
		"DELETE FROM trading.idempotency_records WHERE command_id = $1",
		commandID,
	); err == nil {
		t.Fatal("idempotency record deletion succeeded")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE trading.idempotency_records
		   SET idempotency_key = 'changed'
		 WHERE command_id = $1`,
		commandID,
	); err == nil {
		t.Fatal("idempotency identity update succeeded")
	}
	if _, err := pool.Exec(
		ctx,
		"DELETE FROM trading.commands WHERE command_id = $1",
		commandID,
	); err == nil {
		t.Fatal("command deletion succeeded")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE trading.commands
		   SET canonical_payload = '{"changed":true}'
		 WHERE command_id = $1`,
		commandID,
	); err == nil {
		t.Fatal("command identity update succeeded")
	}
}

func assertAPIRoleCannotMutateEconomicTables(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	var canInsertCommand bool
	var canUpdateBalance bool
	var canInsertAccount bool
	var canProvisionAccount bool
	if err := pool.QueryRow(context.Background(), `
		SELECT
			has_table_privilege(
				'platformgo_api',
				'trading.commands',
				'INSERT'
			),
			has_table_privilege(
				'platformgo_api',
				'ledger.balances',
				'UPDATE'
			),
			has_table_privilege(
				'platformgo_api',
				'trading.accounts',
				'INSERT'
			),
			has_function_privilege(
				'platformgo_api',
				'identity.provision_broker_account(text,text,bigint,text,text,text[],timestamp with time zone)',
				'EXECUTE'
			)`,
	).Scan(
		&canInsertCommand,
		&canUpdateBalance,
		&canInsertAccount,
		&canProvisionAccount,
	); err != nil {
		t.Fatalf("inspect API role privileges: %v", err)
	}
	if !canInsertCommand || canUpdateBalance || canInsertAccount ||
		canProvisionAccount {
		t.Fatalf(
			"API privileges = insert command %t update balance %t "+
				"insert account %t provision account %t",
			canInsertCommand,
			canUpdateBalance,
			canInsertAccount,
			canProvisionAccount,
		)
	}
}

func assertAPIRoleIdentityBoundary(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	for _, test := range []struct {
		name      string
		statement string
	}{
		{name: "direct user insert", statement: `
			INSERT INTO identity.users (
				user_id, broker_subject, login, normalized_login
			) VALUES (
				'urn:xb:user:forged',
				'urn:xb:tenant:forged',
				'forged',
				'forged'
			)`},
		{name: "direct ownership insert", statement: `
			INSERT INTO identity.user_accounts (
				user_id, account_id, broker_subject
			) VALUES (
				'urn:xb:user:forged',
				'urn:xb:account:forged',
				'urn:xb:tenant:forged'
			)`},
		{name: "direct response insert", statement: `
			INSERT INTO identity.idempotency_responses (
				scope, idempotency_key, request_hash,
				response_status, response_body, expires_at
			) VALUES (
				'forged', 'forged', decode(repeat('00', 32), 'hex'),
				200, '{}', clock_timestamp() + interval '1 day'
			)`},
		{name: "bare account provisioning", statement: `
			SELECT identity.provision_broker_account(
				'urn:xb:account:forged',
				'urn:xb:user:forged',
				1,
				'USDC',
				'HYPERLIQUID',
				ARRAY['CRYPTOCURRENCY'],
				clock_timestamp()
			)`},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(
				context.Background(),
				"SET LOCAL ROLE platformgo_api",
			); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(context.Background(), test.statement)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) ||
				postgresError.Code != "42501" {
				t.Fatalf("statement error = %v, want SQLSTATE 42501", err)
			}
		})
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_api",
	); err != nil {
		t.Fatal(err)
	}
	var userID string
	var created bool
	if err := tx.QueryRow(context.Background(), `
		SELECT user_id, created
		  FROM identity.create_broker_user(
			'urn:xb:tenant:permission-test',
			'urn:xb:user:permission-test',
			'permission-test',
			'permission-test@example.com'
		  )`,
	).Scan(&userID, &created); err != nil {
		t.Fatalf("execute narrow broker-user function: %v", err)
	}
	if !created || userID != "urn:xb:user:permission-test" {
		t.Fatalf("narrow broker-user result = %q created=%t", userID, created)
	}
	var echoID string
	if err := tx.QueryRow(context.Background(), `
		SELECT identity.claim_broker_echo(
			'urn:xb:apikey:permission-test',
			'permission-test',
			decode(repeat('01', 32), 'hex'),
			'permission-result',
			clock_timestamp() + interval '1 day'
		)`,
	).Scan(&echoID); err != nil {
		t.Fatalf("execute narrow broker-echo function: %v", err)
	}
	if echoID != "permission-result" {
		t.Fatalf("narrow broker-echo result = %q", echoID)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	var userFunctionDefiner bool
	var userFunctionConfig []string
	var publicCanExecute bool
	if err := pool.QueryRow(context.Background(), `
		SELECT
			prosecdef,
			COALESCE(proconfig, ARRAY[]::text[]),
			has_function_privilege(
				'public',
				'identity.create_broker_user(text,text,text,text)',
				'EXECUTE'
			)
		  FROM pg_proc
		 WHERE oid = 'identity.create_broker_user(text,text,text,text)'::regprocedure`,
	).Scan(
		&userFunctionDefiner,
		&userFunctionConfig,
		&publicCanExecute,
	); err != nil {
		t.Fatal(err)
	}
	if !userFunctionDefiner ||
		!slices.Equal(userFunctionConfig, []string{"search_path=pg_catalog"}) ||
		publicCanExecute {
		t.Fatalf(
			"broker-user function security = definer %t config %v public %t",
			userFunctionDefiner,
			userFunctionConfig,
			publicCanExecute,
		)
	}
}

func postgresPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL pool config: %v", err)
	}
	// Readiness tests hold separate ownership and drain leases concurrently.
	// Pin the test capacity so their behavior does not depend on runner CPU count.
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func runtimeRoleLoginPool(
	t *testing.T,
	admin *pgxpool.Pool,
	login string,
	runtimeRole string,
) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	loginIdentifier := pgx.Identifier{login}.Sanitize()
	roleIdentifier := pgx.Identifier{runtimeRole}.Sanitize()
	const password = "platformgo-test-password"
	if _, err := admin.Exec(ctx, fmt.Sprintf(`
		CREATE ROLE %s LOGIN PASSWORD '%s'
			NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
		GRANT %s TO %s`,
		loginIdentifier,
		password,
		roleIdentifier,
		loginIdentifier,
	)); err != nil {
		t.Fatalf("create %s login: %v", runtimeRole, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(
			context.Background(),
			fmt.Sprintf("DROP ROLE IF EXISTS %s", loginIdentifier),
		)
	})
	config, err := pgxpool.ParseConfig(
		os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN"),
	)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.User = login
	config.ConnConfig.Password = password
	loginPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open %s login: %v", runtimeRole, err)
	}
	t.Cleanup(loginPool.Close)
	return loginPool
}

func resetDurableSchemas(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	dropDurableSchemas(t, pool)
	_, err := pool.Exec(
		context.Background(),
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_api'
			) THEN
				CREATE ROLE platformgo_api NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_engine'
			) THEN
				CREATE ROLE platformgo_engine NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_outbox'
			) THEN
				CREATE ROLE platformgo_outbox NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_projector'
			) THEN
				CREATE ROLE platformgo_projector NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_realtime'
			) THEN
				CREATE ROLE platformgo_realtime NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles
				 WHERE rolname = 'platformgo_realtime_repair'
			) THEN
				CREATE ROLE platformgo_realtime_repair NOLOGIN;
			END IF;
		END;
		$$`,
	)
	if err != nil {
		t.Fatalf("provision test runtime roles: %v", err)
	}
}

func dropDurableSchemas(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	err := postgresfixture.ResetDurableSchemas(
		context.Background(),
		pool,
	)
	if err != nil {
		t.Fatalf("reset durable schemas: %v", err)
	}
}

func migrationFilesThrough(t *testing.T, last string) fstest.MapFS {
	t.Helper()
	directory := filepath.Join("..", "..", "..", "migrations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".sql" || name > last {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: raw}
	}
	return files
}

func assertReceiptIdentityConstraints(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire receipt test connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(context.Background(), `
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260725001100_phase3_committed_realtime_outbox',
			false
		);
		INSERT INTO engine.deployment_shard (shard_id)
		VALUES (7)`); err != nil {
		t.Fatalf("bind receipt test deployment shard: %v", err)
	}
	const insertReceipt = `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, business_input_hash,
			business_input_hash_version, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision
		) VALUES (7, $1, $2, 1, 1, decode(repeat('01', 32), 'hex'),
		          decode(repeat('04', 32), 'hex'), 1, 2,
		          decode(repeat('02', 32), 'hex'), decode(repeat('03', 32), 'hex'),
		          '{}'::jsonb, '{}'::jsonb)`
	inputID := "019f9460-4b36-4e9b-8f44-682611f7ee01"
	if _, err := connection.Exec(
		context.Background(),
		insertReceipt,
		inputID,
		1,
	); err != nil {
		t.Fatalf("insert first receipt: %v", err)
	}
	if _, err := connection.Exec(
		context.Background(),
		insertReceipt,
		inputID,
		2,
	); !isUniqueViolation(err) {
		t.Fatalf("duplicate input ID error = %v, want unique violation", err)
	}
	otherID := "019f9460-4b36-4e9b-8f44-682611f7ee02"
	if _, err := connection.Exec(
		context.Background(),
		insertReceipt,
		otherID,
		1,
	); !isUniqueViolation(err) {
		t.Fatalf("duplicate stream sequence error = %v, want unique violation", err)
	}
}

func assertLedgerBalanceConstraint(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin unbalanced ledger transaction: %v", err)
	}
	_, err = tx.Exec(
		context.Background(),
		`INSERT INTO ledger.transactions (transaction_id, business_key, input_id, logical_time)
		 VALUES ($1, 'unbalanced', $2, 1784887200000000000)`,
		"019f9460-4b36-4e9b-8f44-682611f7ee10",
		"019f9460-4b36-4e9b-8f44-682611f7ee01",
	)
	if err == nil {
		_, err = tx.Exec(
			context.Background(),
			`INSERT INTO ledger.entries (
				entry_id, transaction_id, account_id, currency, amount
			 ) VALUES ($1, $2, 'account-1', 'USDC', 1)`,
			"019f9460-4b36-4e9b-8f44-682611f7ee11",
			"019f9460-4b36-4e9b-8f44-682611f7ee10",
		)
	}
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("prepare unbalanced ledger transaction: %v", err)
	}
	if err := tx.Commit(context.Background()); err == nil {
		t.Fatal("unbalanced ledger transaction committed")
	}
}

func assertOutboxProducerAuthorityConstraints(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload, producer_class
		) VALUES (
			'019f9460-4b36-4e9b-8f44-682611f7ee31',
			'domain.v1.probe', 1, '{}'::jsonb, 'engine'
		)`); !isPostgresCode(err, "23514") {
		t.Fatalf("engine outbox without receipt authority error = %v, want 23514", err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin unknown engine receipt probe: %v", err)
	}
	if _, err = tx.Exec(context.Background(), `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload, producer_class,
			engine_shard_id, engine_input_id
		) VALUES (
			'019f9460-4b36-4e9b-8f44-682611f7ee32',
			'domain.v1.probe', 1,
			'{"messageId":"019f9460-4b36-4e9b-8f44-682611f7ee32",
			  "correlationId":"019f9460-4b36-4e9b-8f44-682611f7ee33"}'::jsonb,
			'engine', 7, '019f9460-4b36-4e9b-8f44-682611f7ee33'
		)`); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("insert deferred unknown engine receipt probe: %v", err)
	}
	err = tx.Commit(context.Background())
	if !isPostgresCode(err, "23503") {
		t.Fatalf("unknown engine receipt commit error = %v, want 23503", err)
	}
}

func assertImmutableLedgerFacts(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	transactionID := "019f9460-4b36-4e9b-8f44-682611f7ee20"
	_, err := pool.Exec(
		context.Background(),
		`INSERT INTO ledger.transactions (transaction_id, business_key, input_id, logical_time)
		 VALUES ($1, 'balanced', $2, 1784887200000000000)`,
		transactionID,
		"019f9460-4b36-4e9b-8f44-682611f7ee01",
	)
	if err == nil {
		_, err = pool.Exec(
			context.Background(),
			`INSERT INTO ledger.entries (
				entry_id, transaction_id, account_id, currency, amount
			 ) VALUES
			 ($1, $2, 'account-1', 'USDC', 1),
			 ($3, $2, 'system:clearing', 'USDC', -1)`,
			"019f9460-4b36-4e9b-8f44-682611f7ee21",
			transactionID,
			"019f9460-4b36-4e9b-8f44-682611f7ee22",
		)
	}
	if err != nil {
		t.Fatalf("insert balanced ledger facts: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		`UPDATE ledger.entries SET amount = 2 WHERE transaction_id = $1`,
		transactionID,
	); err == nil {
		t.Fatal("ledger entry update succeeded")
	}
	if _, err := pool.Exec(
		context.Background(),
		`DELETE FROM ledger.transactions WHERE transaction_id = $1`,
		transactionID,
	); err == nil {
		t.Fatal("ledger transaction delete succeeded")
	}
}

func isUniqueViolation(err error) bool {
	return isPostgresCode(err, "23505")
}

func isPostgresCode(err error, code string) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == code
}
