package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

const (
	brokerFundingACLPreviousMigration = "20260730000300_phase3_broker_account_list_index.up.sql"
	brokerFundingACLMigration         = "20260730000400_phase3_broker_funding_acl.up.sql"
	brokerFundingACLObjectsMigration  = "20260726000100_phase3_funding_history_read_model.up.sql"
)

var (
	brokerFundingACLRelations = []string{
		"trading.funding_settlements",
		"trading.funding_history_projection",
		"trading.funding_instrument_provenance",
	}
	brokerFundingACLLegacyRelations = brokerFundingACLRelations[:2]
	brokerFundingACLLegacyFunctions = []string{
		"trading.require_funding_history_projection()",
		"trading.read_account_funding_history(text,bigint,uuid,boolean,integer,boolean)",
		"trading.read_symbol_funding_history(text,bigint,uuid,boolean,integer,boolean)",
		"trading.account_position_funding_total(text,uuid,bigint)",
		"trading.account_funding_history_count(text)",
		"trading.symbol_funding_history_count(text)",
	}
	brokerFundingACLTrustedFunctions = []string{
		"engine.reject_immutable_change()",
		"engine.require_authoritative_market_receipt()",
		"engine.require_balance_projection_hash_v3()",
		"engine.require_decision_hash_v4_runtime()",
		"engine.require_fill_effective_leverage_hash_v4()",
		"engine.require_runtime_schema_revision()",
		"trading.require_currency_scale_consistency()",
		"trading.require_funding_history_projection()",
	}
	brokerFundingACLFunctions = []string{
		"trading.require_funding_history_projection()",
		"trading.require_funding_instrument_provenance()",
		"trading.read_account_funding_history(text,bigint,uuid,boolean,integer,boolean)",
		"trading.read_broker_account_funding_history(text,bigint,uuid,boolean,integer,boolean)",
		"trading.read_symbol_funding_history(text,bigint,uuid,boolean,integer,boolean)",
		"trading.account_position_funding_total(text,uuid,bigint)",
		"trading.account_funding_history_count(text)",
		"trading.symbol_funding_history_count(text)",
	}
	brokerFundingACLAPIReadFunctions = []string{
		"trading.read_account_funding_history(text,bigint,uuid,boolean,integer,boolean)",
		"trading.read_broker_account_funding_history(text,bigint,uuid,boolean,integer,boolean)",
		"trading.read_symbol_funding_history(text,bigint,uuid,boolean,integer,boolean)",
		"trading.account_position_funding_total(text,uuid,bigint)",
		"trading.account_funding_history_count(text)",
		"trading.symbol_funding_history_count(text)",
	}
)

func TestBrokerFundingACLUpgradesHostileCurrentMain(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerFundingACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("broker_funding_hostile_%d", os.Getpid())
	dependentRole := "platformgo_projector"
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	dependentID := pgx.Identifier{dependentRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile role: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`
			ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
				REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
			ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
				REVOKE ALL PRIVILEGES ON FUNCTIONS FROM %[2]s;
			DROP OWNED BY %[2]s CASCADE;
			DROP ROLE %[2]s`,
			ownerID,
			hostileID,
		)); err != nil {
			t.Errorf("cleanup hostile broker-funding role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA trading;
		GRANT USAGE ON SCHEMA trading TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s WITH GRANT OPTION;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			GRANT ALL PRIVILEGES ON FUNCTIONS TO %[2]s WITH GRANT OPTION`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile funding owner defaults: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLObjectsMigration),
	).MigrateAndProvision(ctx, 41); err != nil {
		t.Fatalf("create funding objects under hostile defaults: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			REVOKE ALL PRIVILEGES ON FUNCTIONS FROM %[2]s;
		DO $scrub$
		DECLARE
			relation_oid pg_catalog.oid;
			function_oid pg_catalog.oid;
		BEGIN
			FOR relation_oid IN
				SELECT relation.oid
				  FROM pg_catalog.pg_class AS relation
				  JOIN pg_catalog.pg_namespace AS namespace
				    ON namespace.oid = relation.relnamespace
				 WHERE namespace.nspname = 'trading'
				   AND relation.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
				   AND relation.oid NOT IN (
					'trading.funding_settlements'::pg_catalog.regclass,
					'trading.funding_history_projection'::pg_catalog.regclass
				   )
			LOOP
				EXECUTE pg_catalog.format(
					'REVOKE ALL PRIVILEGES ON TABLE %%s FROM %%I CASCADE',
					relation_oid::pg_catalog.regclass,
					'%[3]s'
				);
			END LOOP;
			FOR function_oid IN
				SELECT procedure.oid
				  FROM pg_catalog.pg_proc AS procedure
				  JOIN pg_catalog.pg_namespace AS namespace
				    ON namespace.oid = procedure.pronamespace
				 WHERE namespace.nspname = 'trading'
				   AND procedure.proname NOT IN (
					'require_funding_history_projection',
					'read_account_funding_history',
					'read_symbol_funding_history',
					'account_position_funding_total',
					'account_funding_history_count',
					'symbol_funding_history_count'
				   )
			LOOP
				EXECUTE pg_catalog.format(
					'REVOKE ALL PRIVILEGES ON FUNCTION %%s FROM %%I CASCADE',
					function_oid::pg_catalog.regprocedure,
					'%[3]s'
				);
			END LOOP;
		END
		$scrub$`,
		ownerID,
		hostileID,
		hostileRole,
	)); err != nil {
		t.Fatalf("bound hostile defaults to funding objects: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLPreviousMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("advance hostile funding fixture to current main: %v", err)
	}
	seedFundingHistory(
		t,
		pool,
		time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT USAGE ON SCHEMA trading TO %[1]s;
		GRANT SELECT, DELETE ON
			trading.funding_settlements,
			trading.funding_history_projection
		TO PUBLIC;
		GRANT UPDATE (amount) ON trading.funding_settlements
			TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (logical_time) ON trading.funding_history_projection
			TO %[1]s WITH GRANT OPTION;
		GRANT EXECUTE ON FUNCTION
			trading.require_funding_history_projection(),
			trading.read_account_funding_history(
				text,bigint,uuid,boolean,integer,boolean
			),
			trading.read_symbol_funding_history(
				text,bigint,uuid,boolean,integer,boolean
			),
			trading.account_position_funding_total(text,uuid,bigint),
			trading.account_funding_history_count(text),
			trading.symbol_funding_history_count(text)
		TO %[1]s WITH GRANT OPTION;
		GRANT EXECUTE ON FUNCTION
			trading.require_funding_history_projection()
		TO PUBLIC`,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile direct funding ACLs: %v", err)
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
		GRANT SELECT, DELETE ON
			trading.funding_settlements,
			trading.funding_history_projection
		TO %[1]s;
		GRANT UPDATE (amount) ON trading.funding_settlements TO %[1]s;
		GRANT UPDATE (logical_time) ON trading.funding_history_projection
			TO %[1]s;
		GRANT EXECUTE ON FUNCTION
			trading.require_funding_history_projection(),
			trading.read_account_funding_history(
				text,bigint,uuid,boolean,integer,boolean
			),
			trading.read_symbol_funding_history(
				text,bigint,uuid,boolean,integer,boolean
			),
			trading.account_position_funding_total(text,uuid,bigint),
			trading.account_funding_history_count(text),
			trading.symbol_funding_history_count(text)
		TO %[1]s`,
		dependentID,
	)); err != nil {
		_ = delegation.Rollback(ctx)
		t.Fatalf("delegate hostile funding grants: %v", err)
	}
	if err := delegation.Commit(ctx); err != nil {
		t.Fatalf("commit dependent hostile funding grants: %v", err)
	}
	assertBrokerFundingACLFixtureVulnerable(
		t,
		pool,
		hostileRole,
		dependentRole,
	)

	before := readBrokerFundingACLPreservedState(t, pool)
	beforeDefaults := readAdminRiskOwnerDefaults(t, pool)
	beforeHistory := readBrokerFundingACLHistory(t, pool)
	files := migrationFilesThrough(t, brokerFundingACLMigration)
	if _, exists := files[brokerFundingACLMigration]; !exists {
		t.Fatalf(
			"RED: hostile named/default table, column, and function grants "+
				"remain at current tip %s; expected forward migration %s",
			brokerFundingACLPreviousMigration,
			brokerFundingACLMigration,
		)
	}

	current := platformpostgres.NewMigrator(pool, files)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply broker-funding ACL migration: %v", err)
	}
	assertBrokerFundingACLSourcePreserved(t, pool, before)
	if after := readAdminRiskOwnerDefaults(t, pool); after != beforeDefaults {
		t.Fatal("broker-funding ACL migration changed owner default privileges")
	}
	assertBrokerFundingACLHistoryAdvanced(t, pool, beforeHistory)
	assertBrokerFundingACLAllowlist(t, pool)
	assertBrokerFundingACLRuntimePrivileges(t, pool)
	assertBrokerFundingACLProvenance(t, pool, 4)
	for _, role := range []string{hostileRole, dependentRole} {
		assertBrokerFundingACLRoleHasNoPrivileges(t, pool, role)
	}

	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("idempotent broker-funding ACL rerun: %v", err)
	}
	assertBrokerFundingACLSourcePreserved(t, pool, before)
	assertBrokerFundingACLHistoryAdvanced(t, pool, beforeHistory)
	assertBrokerFundingACLAllowlist(t, pool)
	assertBrokerFundingACLProvenance(t, pool, 4)
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify current broker-funding ACL schema: %v", err)
	}
	previous := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLPreviousMigration),
	)
	if err := previous.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous binary verification = %v, want schema-ahead", err)
	}
}

func TestBrokerFundingACLRejectsDivergentReceiptBackedHistory(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		corrupt func(*testing.T, *pgxpool.Pool)
		message string
	}{
		{
			name: "hostile projection truncate",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `
					GRANT USAGE ON SCHEMA trading TO platformgo_projector;
					GRANT TRUNCATE ON trading.funding_history_projection
						TO platformgo_projector;
					SET ROLE platformgo_projector;
					TRUNCATE TABLE trading.funding_history_projection;
					RESET ROLE`); err != nil {
					t.Fatalf("truncate funding projection as hostile role: %v", err)
				}
			},
		},
		{
			name: "hostile forged settlement and projection",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `
					GRANT USAGE ON SCHEMA trading TO platformgo_projector;
					GRANT SELECT, INSERT ON
						trading.funding_settlements,
						trading.funding_history_projection
					TO platformgo_projector;
					SET ROLE platformgo_projector;
					INSERT INTO trading.funding_settlements (
						funding_id, settlement_id, position_id, input_id,
						account_id, instrument_id, signed_quantity,
						oracle_price, rate, amount, settlement_currency
					)
					SELECT
						'019f9b6d-3154-4db1-b639-57c246e92499',
						'019f9b6d-3154-4db1-b639-57c246e92599',
						position_id,
						input_id,
						account_id,
						instrument_id,
						signed_quantity,
						oracle_price,
						rate,
						999999,
						settlement_currency
					  FROM trading.funding_settlements
					 ORDER BY funding_id
					 LIMIT 1;
					INSERT INTO trading.funding_history_projection (
						funding_id, account_id, instrument_id, position_id,
						logical_time
					)
					SELECT
						'019f9b6d-3154-4db1-b639-57c246e92499',
						history.account_id,
						history.instrument_id,
						history.position_id,
						history.logical_time
					  FROM trading.funding_history_projection AS history
					 ORDER BY history.funding_id
					 LIMIT 1;
					RESET ROLE`); err != nil {
					t.Fatalf("forge funding history as hostile role: %v", err)
				}
			},
		},
		{
			name: "orphan settlement",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				corruptAsReplicationAuthority(t, pool, `
					INSERT INTO trading.funding_settlements (
						funding_id, settlement_id, position_id, input_id,
						account_id, instrument_id, signed_quantity,
						oracle_price, rate, amount, settlement_currency
					)
					SELECT
						'019f9b6d-3154-4db1-b639-57c246e92498',
						'019f9b6d-3154-4db1-b639-57c246e92598',
						position_id, input_id, account_id, instrument_id,
						signed_quantity, oracle_price, rate, amount,
						settlement_currency
					  FROM trading.funding_settlements
					 ORDER BY funding_id
					 LIMIT 1`)
			},
			message: "orphan",
		},
		{
			name: "orphan projection",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				corruptAsReplicationAuthority(t, pool, `
					INSERT INTO trading.funding_history_projection (
						funding_id, account_id, instrument_id, position_id,
						logical_time
					)
					SELECT
						'019f9b6d-3154-4db1-b639-57c246e92497',
						account_id, instrument_id, position_id, logical_time
					  FROM trading.funding_history_projection
					 ORDER BY funding_id
					 LIMIT 1`)
			},
			message: "orphan",
		},
		{
			name: "unexpected trigger",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `
					CREATE FUNCTION trading.hostile_funding_sidecar()
					RETURNS trigger
					LANGUAGE plpgsql
					AS $$
					BEGIN
						RETURN NEW;
					END
					$$;
					CREATE TRIGGER hostile_funding_sidecar
					BEFORE INSERT ON trading.funding_history_projection
					FOR EACH ROW
					EXECUTE FUNCTION trading.hostile_funding_sidecar()`); err != nil {
					t.Fatalf("install hostile funding trigger: %v", err)
				}
			},
			message: "unexpected pre-cutover trigger",
		},
		{
			name: "disabled expected trigger",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `
					ALTER TABLE trading.funding_history_projection
					DISABLE TRIGGER funding_history_projection_is_immutable`,
				); err != nil {
					t.Fatalf("disable funding projection trigger: %v", err)
				}
			},
			message: "unexpected pre-cutover trigger",
		},
		{
			name: "dropped ownership revision trigger",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `
					DROP TRIGGER
						shard_ownership_epochs_require_runtime_schema_revision
					ON engine.shard_ownership_epochs`,
				); err != nil {
					t.Fatalf("drop ownership revision trigger: %v", err)
				}
			},
			message: "unexpected pre-cutover trigger",
		},
		{
			name: "disabled ownership revision trigger",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `
					ALTER TABLE engine.shard_ownership_epochs
					DISABLE TRIGGER
						shard_ownership_epochs_require_runtime_schema_revision`,
				); err != nil {
					t.Fatalf("disable ownership revision trigger: %v", err)
				}
			},
			message: "unexpected pre-cutover trigger",
		},
		{
			name: "immediate replacement for deferred constraint trigger",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `
					DROP TRIGGER funding_settlement_requires_history_projection
					ON trading.funding_settlements;
					CREATE TRIGGER funding_settlement_requires_history_projection
					AFTER INSERT ON trading.funding_settlements
					FOR EACH ROW
					EXECUTE FUNCTION
						trading.require_funding_history_projection()`,
				); err != nil {
					t.Fatalf("replace deferred funding projection trigger: %v", err)
				}
			},
			message: "unexpected pre-cutover trigger",
		},
		{
			name: "replaced immutable guard body",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `
					CREATE OR REPLACE FUNCTION
						engine.reject_immutable_change()
					RETURNS trigger
					LANGUAGE plpgsql
					AS $$
					BEGIN
						RETURN NULL;
					END
					$$`,
				); err != nil {
					t.Fatalf("replace immutable guard body: %v", err)
				}
			},
			message: "trusted pre-cutover trigger function",
		},
		{
			name: "replaced history constraint body",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `
					CREATE OR REPLACE FUNCTION
						trading.require_funding_history_projection()
					RETURNS trigger
					LANGUAGE plpgsql
					SECURITY DEFINER
					SET search_path = pg_catalog
					AS $$
					BEGIN
						RETURN NEW;
					END
					$$`,
				); err != nil {
					t.Fatalf("replace history constraint body: %v", err)
				}
			},
			message: "trusted pre-cutover trigger function",
		},
		{
			name: "oversized receipt numeric",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				corruptAsReplicationAuthority(t, pool, `
					UPDATE engine.input_receipts
					   SET decision = pg_catalog.jsonb_set(
						   decision,
						   '{FundingChanges,0,Amount}',
						   pg_catalog.to_jsonb(
							   '999999999999999999999'::pg_catalog.text
						   )
					   )
					 WHERE input_id = (
						SELECT input_id
						  FROM engine.input_receipts
						 WHERE CASE
							WHEN pg_catalog.jsonb_typeof(
								decision -> 'FundingChanges'
							) = 'array'
							THEN pg_catalog.jsonb_array_length(
								decision -> 'FundingChanges'
							) > 0
							ELSE false
						 END
						 ORDER BY stream_sequence
						 LIMIT 1
					 )`)
			},
			message: "malformed or non-accepted effect",
		},
		{
			name: "overscale receipt numeric",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				corruptAsReplicationAuthority(t, pool, `
					UPDATE engine.input_receipts
					   SET decision = pg_catalog.jsonb_set(
						   decision,
						   '{FundingChanges,0,Rate}',
						   pg_catalog.to_jsonb(
							   '0.1234567890123456789'::pg_catalog.text
						   )
					   )
					 WHERE input_id = (
						SELECT input_id
						  FROM engine.input_receipts
						 WHERE CASE
							WHEN pg_catalog.jsonb_typeof(
								decision -> 'FundingChanges'
							) = 'array'
							THEN pg_catalog.jsonb_array_length(
								decision -> 'FundingChanges'
							) > 0
							ELSE false
						 END
						 ORDER BY stream_sequence
						 LIMIT 1
					 )`)
			},
			message: "malformed or non-accepted effect",
		},
		{
			name: "malformed instrument provenance receipt",
			corrupt: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				corruptAsReplicationAuthority(t, pool, `
					UPDATE engine.input_receipts
					   SET decision = decision #-
						   '{InstrumentChanges,0,QuantityScale}'
					 WHERE input_id = (
						SELECT input_id
						  FROM engine.input_receipts
						 WHERE CASE
							WHEN pg_catalog.jsonb_typeof(
								decision -> 'InstrumentChanges'
							) = 'array'
							THEN pg_catalog.jsonb_array_length(
								decision -> 'InstrumentChanges'
							) > 0
							ELSE false
						 END
						 ORDER BY stream_sequence DESC
						 LIMIT 1
					 )`)
			},
			message: "instrument change history",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			assertBrokerFundingACLPostgres19Beta2(t, pool)
			resetDurableSchemas(t, pool)
			if err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, brokerFundingACLPreviousMigration),
			).MigrateAndProvision(ctx, 41); err != nil {
				t.Fatalf("apply current-main schema: %v", err)
			}
			seedFundingHistory(
				t,
				pool,
				time.Date(2026, 7, 30, 4, 30, 0, 0, time.UTC),
			)
			seedBrokerFundingOwnershipEpoch(t, pool)
			testCase.corrupt(t, pool)

			beforeState := readBrokerFundingACLPreservedState(t, pool)
			beforeACL := readBrokerFundingACLRawACL(t, pool)
			beforeHistory := readBrokerFundingACLHistory(t, pool)
			err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, brokerFundingACLMigration),
			).Migrate(ctx)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) ||
				postgresError.Code != "55000" {
				t.Fatalf(
					"divergent funding migration error = %v, want SQLSTATE 55000",
					err,
				)
			}
			if testCase.message != "" &&
				!strings.Contains(postgresError.Message, testCase.message) {
				t.Fatalf(
					"divergent funding migration message = %q, want %q",
					postgresError.Message,
					testCase.message,
				)
			}
			if after := readBrokerFundingACLHistory(t, pool); !slices.Equal(
				after,
				beforeHistory,
			) {
				t.Fatalf(
					"rejected migration changed journal: before=%v after=%v",
					beforeHistory,
					after,
				)
			}
			if after := readBrokerFundingACLRawACL(t, pool); after != beforeACL {
				t.Fatal("rejected migration changed funding ACLs")
			}
			assertBrokerFundingACLPreservedState(t, pool, beforeState)
		})
	}
}

func seedBrokerFundingOwnershipEpoch(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ownership epoch seed: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260730000200_phase3_currency_scale_authority_fence',
			true
		);
		INSERT INTO engine.shard_ownership_epochs (
			shard_id,
			epoch,
			acquired_at
		) VALUES (
			41,
			7,
			TIMESTAMPTZ '2026-07-30 04:29:00+00'
		)`,
	); err != nil {
		t.Fatalf("seed ownership epoch: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit ownership epoch seed: %v", err)
	}
}

func TestBrokerFundingACLUsesLastSameReceiptInstrumentChangeDeterministically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerFundingACLPostgres19Beta2(t, pool)

	var firstDigest [sha256.Size]byte
	for attempt := 0; attempt < 2; attempt++ {
		resetDurableSchemas(t, pool)
		if err := platformpostgres.NewMigrator(
			pool,
			migrationFilesThrough(t, brokerFundingACLPreviousMigration),
		).MigrateAndProvision(ctx, 41); err != nil {
			t.Fatalf("attempt %d apply current-main schema: %v", attempt, err)
		}
		seedFundingHistory(
			t,
			pool,
			time.Date(2026, 7, 30, 5, 0, 0, 0, time.UTC),
		)
		corruptAsReplicationAuthority(t, pool, `
			UPDATE engine.input_receipts
			   SET decision = pg_catalog.jsonb_set(
				   decision,
				   '{InstrumentChanges}',
				   $changes$[
					   {
						   "InstrumentID":"BTC-PERP",
						   "Revision":1,
						   "PriceScale":2,
						   "QuantityScale":2,
						   "SettlementCurrency":"USDC",
						   "SettlementCurrencyScale":2,
						   "InitialMarginRate":"0.1",
						   "MaintenanceMarginRate":"0.05",
						   "MaxLeverage":"10",
						   "MakerFeeRate":"-0.0001",
						   "TakerFeeRate":"0.0005"
					   },
					   {
						   "InstrumentID":"BTC-PERP",
						   "Revision":1,
						   "PriceScale":2,
						   "QuantityScale":3,
						   "SettlementCurrency":"USDC",
						   "SettlementCurrencyScale":2,
						   "InitialMarginRate":"0.1",
						   "MaintenanceMarginRate":"0.05",
						   "MaxLeverage":"10",
						   "MakerFeeRate":"-0.0001",
						   "TakerFeeRate":"0.0005"
					   }
				   ]$changes$::pg_catalog.jsonb
			   )
			 WHERE input_id =
				   '019f9b6d-3154-4db1-b639-57c246e92300';

			UPDATE engine.input_receipts
			   SET decision = pg_catalog.jsonb_set(
				   decision,
				   '{FundingChanges,0,SignedQuantity}',
				   pg_catalog.to_jsonb('1.234'::pg_catalog.text)
			   )
			 WHERE input_id =
				   '019f9b6d-3154-4db1-b639-57c246e92301';

			UPDATE trading.funding_settlements
			   SET signed_quantity = 1.234
			 WHERE input_id =
				   '019f9b6d-3154-4db1-b639-57c246e92301'`)

		if err := platformpostgres.NewMigrator(
			pool,
			migrationFilesThrough(t, brokerFundingACLMigration),
		).Migrate(ctx); err != nil {
			t.Fatalf(
				"attempt %d migrate same-receipt instrument changes: %v",
				attempt,
				err,
			)
		}

		var (
			provenanceCount int
			scaleThreeCount int
			threeDecimal    bool
			digestInput     string
		)
		if err := pool.QueryRow(ctx, `
			SELECT
				count(*),
				count(*) FILTER (
					WHERE provenance.quantity_scale = 3
				),
				bool_and(
					CASE
						WHEN settlement.input_id =
							'019f9b6d-3154-4db1-b639-57c246e92301'
						THEN settlement.signed_quantity = 1.234
						ELSE true
					END
				),
				string_agg(
					provenance.funding_id::text || '|' ||
						provenance.instrument_id || '|' ||
						provenance.revision::text || '|' ||
						provenance.price_scale::text || '|' ||
						provenance.quantity_scale::text || '|' ||
						settlement.signed_quantity::text,
					E'\n'
					ORDER BY provenance.funding_id
				)
			  FROM trading.funding_instrument_provenance AS provenance
			  JOIN trading.funding_settlements AS settlement
			    ON settlement.funding_id = provenance.funding_id`,
		).Scan(
			&provenanceCount,
			&scaleThreeCount,
			&threeDecimal,
			&digestInput,
		); err != nil {
			t.Fatalf("attempt %d read deterministic provenance: %v", attempt, err)
		}
		if provenanceCount != 4 || scaleThreeCount != 4 || !threeDecimal {
			t.Fatalf(
				"attempt %d provenance count/scale-3/funding = %d/%d/%v,"+
					" want 4/4/true",
				attempt,
				provenanceCount,
				scaleThreeCount,
				threeDecimal,
			)
		}
		digest := sha256.Sum256([]byte(digestInput))
		if attempt == 0 {
			firstDigest = digest
		} else if digest != firstDigest {
			t.Fatalf(
				"same-receipt provenance digest changed: first=%x second=%x",
				firstDigest,
				digest,
			)
		}
		assertBrokerFundingACLProvenance(t, pool, 4)
	}
}

func TestBrokerFundingACLLaterLockRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerFundingACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLPreviousMigration),
	).MigrateAndProvision(ctx, 41); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedFundingHistory(
		t,
		pool,
		time.Date(2026, 7, 30, 5, 0, 0, 0, time.UTC),
	)
	if _, err := pool.Exec(ctx, `
		GRANT USAGE ON SCHEMA trading TO platformgo_projector;
		GRANT UPDATE
			ON trading.funding_history_projection
			TO platformgo_projector`); err != nil {
		t.Fatalf("grant pre-revocation projection writer: %v", err)
	}
	before := readBrokerFundingACLPreservedState(t, pool)
	beforeACL := readBrokerFundingACLRawACL(t, pool)
	beforeDefaults := readAdminRiskOwnerDefaults(t, pool)
	beforeHistory := readBrokerFundingACLHistory(t, pool)

	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin pre-revocation projection writer: %v", err)
	}
	if _, err := writer.Exec(ctx, `
		SET LOCAL ROLE platformgo_projector;
		LOCK TABLE trading.funding_history_projection
			IN ROW EXCLUSIVE MODE`); err != nil {
		_ = writer.Rollback(ctx)
		t.Fatalf("take pre-revocation projection writer lock: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLMigration),
	)
	result := make(chan error, 1)
	go func() {
		result <- current.Migrate(context.Background())
	}()
	waitAdminRiskRelationLock(
		t,
		pool,
		result,
		"trading.funding_history_projection",
		"ShareLock",
	)
	var firstGranted bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM pg_catalog.pg_locks
			 WHERE relation =
				'trading.funding_settlements'::pg_catalog.regclass
			   AND mode = 'ShareLock'
			   AND granted
		)`,
	).Scan(&firstGranted); err != nil {
		_ = writer.Rollback(ctx)
		t.Fatalf("inspect first funding migration lock: %v", err)
	}
	if !firstGranted {
		_ = writer.Rollback(ctx)
		t.Fatal("migration did not lock funding_settlements before projection")
	}

	var migrationErr error
	select {
	case migrationErr = <-result:
	case <-time.After(7 * time.Second):
		_ = writer.Rollback(ctx)
		t.Fatal("timed out waiting for bounded broker-funding ACL failure")
	}
	var postgresError *pgconn.PgError
	if !errors.As(migrationErr, &postgresError) ||
		postgresError.Code != "55P03" {
		_ = writer.Rollback(ctx)
		t.Fatalf(
			"pre-revocation writer migration error = %v, want SQLSTATE 55P03",
			migrationErr,
		)
	}
	if after := readBrokerFundingACLHistory(t, pool); !slices.Equal(
		after,
		beforeHistory,
	) {
		_ = writer.Rollback(ctx)
		t.Fatalf(
			"failed migration changed history: before=%v after=%v",
			beforeHistory,
			after,
		)
	}
	if after := readBrokerFundingACLRawACL(t, pool); after != beforeACL {
		_ = writer.Rollback(ctx)
		t.Fatal("failed migration changed broker-funding ACLs")
	}
	assertBrokerFundingACLPreservedState(t, pool, before)
	if after := readAdminRiskOwnerDefaults(t, pool); after != beforeDefaults {
		_ = writer.Rollback(ctx)
		t.Fatal("failed migration changed owner defaults")
	}

	if err := writer.Rollback(ctx); err != nil {
		t.Fatalf("rollback pre-revocation projection writer: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry broker-funding ACL migration after writer drain: %v", err)
	}
	assertBrokerFundingACLSourcePreserved(t, pool, before)
	assertBrokerFundingACLHistoryAdvanced(t, pool, beforeHistory)
	assertBrokerFundingACLAllowlist(t, pool)
	assertBrokerFundingACLRuntimePrivileges(t, pool)
	assertBrokerFundingACLProvenance(t, pool, 4)
}

func TestBrokerFundingACLMigrationWaitsForProductionOrderWriter(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerFundingACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLPreviousMigration),
	).MigrateAndProvision(ctx, 41); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedFundingHistory(
		t,
		pool,
		time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC),
	)

	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin production-order funding writer: %v", err)
	}
	if _, err := writer.Exec(ctx, `
		SET LOCAL ROLE platformgo_engine;
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260730000200_phase3_currency_scale_authority_fence',
				true
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				true
			);
		INSERT INTO trading.funding_settlements (
			funding_id, settlement_id, position_id, input_id,
			account_id, instrument_id, signed_quantity, oracle_price,
			rate, amount, settlement_currency
		) VALUES (
			'019f9b6d-3154-4db1-b639-57c246e92499',
			'019f9b6d-3154-4db1-b639-57c246e92599',
			'019f9b6d-3154-4db1-b639-57c246e92201',
			'019f9b6d-3154-4db1-b639-57c246e92399',
			'urn:xb:account:funding-one',
			'BTC-PERP',
			1,
			1000,
			0.0000125,
			-1,
			'USDC'
		);
		INSERT INTO trading.funding_history_projection (
			funding_id, account_id, instrument_id, position_id, logical_time
		) VALUES (
			'019f9b6d-3154-4db1-b639-57c246e92499',
			'urn:xb:account:funding-one',
			'BTC-PERP',
			'019f9b6d-3154-4db1-b639-57c246e92201',
			1785387600000000000
		);
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			41,
			'019f9b6d-3154-4db1-b639-57c246e92399',
			99,
			1,
			1,
			decode(repeat('99', 32), 'hex'),
			4,
			decode(repeat('98', 32), 'hex'),
			decode(repeat('97', 32), 'hex'),
			'{"LogicalTime":1785387600000000000}'::jsonb,
			'{
				"DecisionHashVersion":4,
				"CommandResult":{"Status":"accepted"},
				"FundingChanges":[{
					"FundingID":[1,159,155,109,49,84,77,177,182,57,87,194,70,233,36,153],
					"SettlementID":[1,159,155,109,49,84,77,177,182,57,87,194,70,233,37,153],
					"PositionID":[1,159,155,109,49,84,77,177,182,57,87,194,70,233,34,1],
					"AccountID":"urn:xb:account:funding-one",
					"InstrumentID":"BTC-PERP",
					"SignedQuantity":"1",
					"OraclePrice":"1000",
					"Rate":"0.0000125",
					"Amount":"-1",
					"SettlementCurrency":"USDC"
				}]
			}'::jsonb,
			decode(repeat('96', 32), 'hex'),
			1
		)`,
	); err != nil {
		_ = writer.Rollback(ctx)
		t.Fatalf("execute production-order funding writer: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLMigration),
	)
	result := make(chan error, 1)
	go func() {
		result <- current.Migrate(context.Background())
	}()
	waitAdminRiskRelationLock(
		t,
		pool,
		result,
		"trading.funding_settlements",
		"ShareLock",
	)
	if err := writer.Commit(ctx); err != nil {
		t.Fatalf("commit production-order funding writer: %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("migration after funding writer commit: %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("migration did not complete after funding writer commit")
	}

	var settlementCount, projectionCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			(
				SELECT count(*)
				  FROM trading.funding_settlements
				 WHERE funding_id =
					'019f9b6d-3154-4db1-b639-57c246e92499'
			),
			(
				SELECT count(*)
				  FROM trading.funding_history_projection
				 WHERE funding_id =
					'019f9b6d-3154-4db1-b639-57c246e92499'
			)`,
	).Scan(&settlementCount, &projectionCount); err != nil {
		t.Fatalf("read committed funding writer state: %v", err)
	}
	if settlementCount != 1 || projectionCount != 1 {
		t.Fatalf(
			"production funding writer state = %d/%d, want 1/1",
			settlementCount,
			projectionCount,
		)
	}
	assertBrokerFundingACLAllowlist(t, pool)
	assertBrokerFundingACLRuntimePrivileges(t, pool)
	assertBrokerFundingACLProvenance(t, pool, 5)
}

func TestBrokerFundingACLMigrationFencesActiveOwnerAndOldRuntime(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerFundingACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLMigration),
	).MigrateAndProvision(ctx, 41); err != nil {
		t.Fatalf("apply broker-funding schema for active owner: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	activeOwner, err := store.AcquireShardOwnership(ctx, 41)
	if err != nil {
		t.Fatalf("acquire active pre-cutover owner: %v", err)
	}

	// Keep the real process-lifetime owner lock while restoring the disposable
	// database to the previous schema. The migration must time out before any
	// catalog, ACL, or data change becomes visible.
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLPreviousMigration),
	).MigrateAndProvision(ctx, 41); err != nil {
		_ = activeOwner.Close(ctx)
		t.Fatalf("restore previous schema under active owner: %v", err)
	}
	beforeState := readBrokerFundingACLPreservedState(t, pool)
	beforeACL := readBrokerFundingACLRawACL(t, pool)
	beforeHistory := readBrokerFundingACLHistory(t, pool)

	migrator := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerFundingACLMigration),
	)
	started := time.Now()
	err = migrator.Migrate(ctx)
	if err == nil {
		_ = activeOwner.Close(ctx)
		t.Fatal("migration acquired configured shard while an EngineStore owner was active")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		_ = activeOwner.Close(ctx)
		t.Fatalf("active-owner migration error = %v, want SQLSTATE 55P03", err)
	}
	if elapsed := time.Since(started); elapsed < 4*time.Second ||
		elapsed > 8*time.Second {
		_ = activeOwner.Close(ctx)
		t.Fatalf("active-owner migration timeout = %s, want bounded 5s lock timeout", elapsed)
	}
	if after := readBrokerFundingACLHistory(t, pool); !slices.Equal(
		after,
		beforeHistory,
	) {
		_ = activeOwner.Close(ctx)
		t.Fatalf(
			"failed active-owner migration changed history: before %v after %v",
			beforeHistory,
			after,
		)
	}
	if after := readBrokerFundingACLRawACL(t, pool); after != beforeACL {
		_ = activeOwner.Close(ctx)
		t.Fatal("failed active-owner migration changed ACLs")
	}
	assertBrokerFundingACLPreservedState(t, pool, beforeState)

	if err := activeOwner.Close(ctx); err != nil {
		t.Fatalf("close active pre-cutover owner: %v", err)
	}
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after active owner drain: %v", err)
	}

	oldRuntime, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire old-runtime session: %v", err)
	}
	defer oldRuntime.Release()
	if _, err := oldRuntime.Exec(ctx, `
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260730000200_phase3_currency_scale_authority_fence',
			false
		);
		SET ROLE platformgo_engine;
		SELECT pg_advisory_lock(1346850639, 41)`,
	); err != nil {
		t.Fatalf("bind simulated old runtime: %v", err)
	}
	_, err = oldRuntime.Exec(ctx, `
		INSERT INTO engine.shard_ownership_epochs (
			shard_id, epoch, acquired_at
		) VALUES (41, 1, clock_timestamp())
		ON CONFLICT (shard_id) DO UPDATE SET
			epoch = engine.shard_ownership_epochs.epoch + 1,
			acquired_at = EXCLUDED.acquired_at`)
	if !errors.As(err, &pgErr) || pgErr.Code != "55000" ||
		!strings.Contains(pgErr.Message, "runtime schema revision") {
		_, _ = oldRuntime.Exec(ctx, "SELECT pg_advisory_unlock(1346850639, 41)")
		t.Fatalf("old-runtime ownership error = %v, want revision SQLSTATE 55000", err)
	}
	if _, err := oldRuntime.Exec(ctx, `
		SELECT pg_advisory_unlock(1346850639, 41);
		RESET ROLE`); err != nil {
		t.Fatalf("release simulated old runtime: %v", err)
	}

	currentStore := platformpostgres.NewEngineStore(pool)
	currentOwner, err := currentStore.AcquireShardOwnership(ctx, 41)
	if err != nil {
		t.Fatalf("acquire post-cutover owner: %v", err)
	}
	defer func() {
		if closeErr := currentOwner.Close(context.Background()); closeErr != nil {
			t.Fatalf("close post-cutover owner: %v", closeErr)
		}
	}()
	state := engine.NewState(41)
	ids := testkit.NewShardIDSequence(41)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)),
	)
	_, decision, _, duplicate := applyStoredTrading(
		t,
		pool,
		currentStore,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionConfigureInstrument,
			ConfigureInstrument: &engine.ConfigureInstrument{
				InstrumentID:            "ETH-PERP",
				Revision:                1,
				PriceScale:              2,
				QuantityScale:           3,
				SettlementCurrency:      "USDC",
				SettlementCurrencyScale: 2,
				InitialMarginRate:       "0.1",
				MaintenanceMarginRate:   "0.05",
				MaxLeverage:             "10",
				MakerFeeRate:            "0.001",
				TakerFeeRate:            "0.002",
			},
		},
		platformpostgres.ApplyOptions{Ownership: currentOwner},
	)
	if duplicate || decision.CommandResult.Status != engine.CommandStatusAccepted {
		t.Fatalf(
			"post-cutover writer result = status %q duplicate %v, want accepted/false",
			decision.CommandResult.Status,
			duplicate,
		)
	}
}

type brokerFundingACLPreservedState struct {
	FundingDigest    [sha256.Size]byte
	ProjectionDigest [sha256.Size]byte
	OwnershipRows    string
	OwnershipTrigger string
	Owners           string
	FileNodes        string
	Indexes          string
	Constraints      string
	Triggers         string
	Functions        string
	GuardFunctions   string
}

func assertBrokerFundingACLPostgres19Beta2(
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
		t.Fatalf(
			"PostgreSQL server version = %q, want PostgreSQL 19 Beta 2",
			version,
		)
	}
}

func readBrokerFundingACLPreservedState(
	t *testing.T,
	pool *pgxpool.Pool,
) brokerFundingACLPreservedState {
	t.Helper()
	ctx := context.Background()
	var (
		fundingRows    string
		projectionRows string
		state          brokerFundingACLPreservedState
	)
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			string_agg(
				funding_id::text || '|' ||
				settlement_id::text || '|' ||
				position_id::text || '|' ||
				input_id::text || '|' ||
				account_id || '|' ||
				instrument_id || '|' ||
				signed_quantity::text || '|' ||
				oracle_price::text || '|' ||
				rate::text || '|' ||
				amount::text || '|' ||
				settlement_currency,
				E'\n' ORDER BY funding_id
			),
			''
		)
		  FROM trading.funding_settlements`,
	).Scan(&fundingRows); err != nil {
		t.Fatalf("read funding settlement bytes: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			string_agg(
				funding_id::text || '|' ||
				account_id || '|' ||
				instrument_id || '|' ||
				position_id::text || '|' ||
				logical_time::text,
				E'\n' ORDER BY funding_id
			),
			''
		)
		  FROM trading.funding_history_projection`,
	).Scan(&projectionRows); err != nil {
		t.Fatalf("read funding projection bytes: %v", err)
	}
	state.FundingDigest = sha256.Sum256([]byte(fundingRows))
	state.ProjectionDigest = sha256.Sum256([]byte(projectionRows))
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			string_agg(
				shard_id::text || '|' ||
					epoch::text || '|' ||
					acquired_at::text,
				E'\n' ORDER BY shard_id
			),
			''
		)
		  FROM engine.shard_ownership_epochs`,
	).Scan(&state.OwnershipRows); err != nil {
		t.Fatalf("read ownership epoch rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			string_agg(
				trigger_record.tgname || '|' ||
					trigger_record.tgfoid::pg_catalog.regprocedure::text ||
					'|' || trigger_record.tgtype::text ||
					'|' || trigger_record.tgenabled::text ||
					'|' || trigger_record.tgdeferrable::text ||
					'|' || trigger_record.tginitdeferred::text ||
					'|' || trigger_record.tgconstraint::text ||
					'|' || COALESCE(trigger_record.tgqual::text, '') ||
					'|' || trigger_record.tgattr::text ||
					'|' || COALESCE(trigger_record.tgoldtable::text, '') ||
					'|' || COALESCE(trigger_record.tgnewtable::text, '') ||
					'|' || pg_catalog.octet_length(
						trigger_record.tgargs
					)::text ||
					'|' || trigger_record.tgnargs::text,
				E'\n' ORDER BY trigger_record.tgname
			),
			''
		)
		  FROM pg_catalog.pg_trigger AS trigger_record
		 WHERE trigger_record.tgrelid =
				   'engine.shard_ownership_epochs'::pg_catalog.regclass
		   AND NOT trigger_record.tgisinternal`,
	).Scan(&state.OwnershipTrigger); err != nil {
		t.Fatalf("read ownership revision trigger: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(
			relation.oid::pg_catalog.regclass::text || '=' ||
			pg_catalog.pg_get_userbyid(relation.relowner),
			',' ORDER BY relation.oid::pg_catalog.regclass::text
		)
		  FROM pg_catalog.pg_class AS relation
		 WHERE relation.oid = ANY (
			ARRAY[
				'trading.funding_settlements'::pg_catalog.regclass,
				'trading.funding_history_projection'::pg_catalog.regclass
			]::pg_catalog.oid[]
		)`,
	).Scan(&state.Owners); err != nil {
		t.Fatalf("read funding relation owners: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(
			relation.oid::pg_catalog.regclass::text || '=' ||
			relation.relfilenode::text,
			',' ORDER BY relation.oid::pg_catalog.regclass::text
		)
		  FROM pg_catalog.pg_class AS relation
		 WHERE relation.oid = ANY (
			ARRAY[
				'trading.funding_settlements'::pg_catalog.regclass,
				'trading.funding_history_projection'::pg_catalog.regclass
			]::pg_catalog.oid[]
		)`,
	).Scan(&state.FileNodes); err != nil {
		t.Fatalf("read funding relation filenodes: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(
			schemaname || '.' || tablename || '.' || indexname || '=' ||
			indexdef,
			E'\n' ORDER BY schemaname, tablename, indexname
		)
		  FROM pg_catalog.pg_indexes
		 WHERE (schemaname, tablename) IN (
			('trading', 'funding_settlements'),
			('trading', 'funding_history_projection')
		)`,
	).Scan(&state.Indexes); err != nil {
		t.Fatalf("read funding indexes: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(
			constraint_record.conrelid::pg_catalog.regclass::text || '.' ||
			constraint_record.conname || '=' ||
			pg_catalog.pg_get_constraintdef(
				constraint_record.oid,
				true
			) || ':' ||
			constraint_record.convalidated::text,
			E'\n' ORDER BY
				constraint_record.conrelid::pg_catalog.regclass::text,
				constraint_record.conname
		)
		  FROM pg_catalog.pg_constraint AS constraint_record
		 WHERE constraint_record.conrelid = ANY (
			ARRAY[
				'trading.funding_settlements'::pg_catalog.regclass,
				'trading.funding_history_projection'::pg_catalog.regclass
			]::pg_catalog.oid[]
		)`,
	).Scan(&state.Constraints); err != nil {
		t.Fatalf("read funding constraints: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(
			trigger_record.tgrelid::pg_catalog.regclass::text || '.' ||
			trigger_record.tgname || '=' ||
			pg_catalog.pg_get_triggerdef(trigger_record.oid, true) || ':' ||
			trigger_record.tgenabled::text,
			E'\n' ORDER BY
				trigger_record.tgrelid::pg_catalog.regclass::text,
				trigger_record.tgname
		)
		  FROM pg_catalog.pg_trigger AS trigger_record
		 WHERE trigger_record.tgrelid = ANY (
			ARRAY[
				'trading.funding_settlements'::pg_catalog.regclass,
				'trading.funding_history_projection'::pg_catalog.regclass
			]::pg_catalog.oid[]
		)
		   AND NOT trigger_record.tgisinternal`,
	).Scan(&state.Triggers); err != nil {
		t.Fatalf("read funding triggers: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(
			procedure.oid::text || '|' ||
			procedure.oid::pg_catalog.regprocedure::text || '|' ||
			language.lanname || '|' ||
			procedure.provolatile::text || '|' ||
			procedure.prosecdef::text || '|' ||
			COALESCE(
				pg_catalog.array_to_string(procedure.proconfig, ','),
				''
			) || '|' ||
			role.rolname || '|' ||
			pg_catalog.pg_get_function_result(procedure.oid) || '|' ||
			pg_catalog.pg_get_functiondef(procedure.oid),
			E'\n' ORDER BY procedure.oid::pg_catalog.regprocedure::text
		)
		  FROM pg_catalog.pg_proc AS procedure
		  JOIN pg_catalog.pg_namespace AS namespace
		    ON namespace.oid = procedure.pronamespace
		  JOIN pg_catalog.pg_language AS language
		    ON language.oid = procedure.prolang
		  JOIN pg_catalog.pg_roles AS role
		    ON role.oid = procedure.proowner
		 WHERE namespace.nspname = 'trading'
		   AND procedure.oid::pg_catalog.regprocedure::text = ANY ($1)`,
		brokerFundingACLLegacyFunctions,
	).Scan(&state.Functions); err != nil {
		t.Fatalf("read funding function metadata: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(
			procedure.oid::text || '|' ||
				procedure.oid::pg_catalog.regprocedure::text || '|' ||
				language.lanname || '|' ||
				procedure.provolatile::text || '|' ||
				procedure.prosecdef::text || '|' ||
				COALESCE(
					pg_catalog.array_to_string(procedure.proconfig, ','),
					''
				) || '|' ||
				role.rolname || '|' ||
				pg_catalog.pg_get_function_result(procedure.oid) || '|' ||
				pg_catalog.pg_get_functiondef(procedure.oid),
			E'\n' ORDER BY procedure.oid::pg_catalog.regprocedure::text
		)
		  FROM pg_catalog.pg_proc AS procedure
		  JOIN pg_catalog.pg_language AS language
		    ON language.oid = procedure.prolang
		  JOIN pg_catalog.pg_roles AS role
		    ON role.oid = procedure.proowner
		 WHERE procedure.oid::pg_catalog.regprocedure::text = ANY ($1)`,
		brokerFundingACLTrustedFunctions,
	).Scan(&state.GuardFunctions); err != nil {
		t.Fatalf("read trusted guard function metadata: %v", err)
	}
	return state
}

func assertBrokerFundingACLPreservedState(
	t *testing.T,
	pool *pgxpool.Pool,
	want brokerFundingACLPreservedState,
) {
	t.Helper()
	if got := readBrokerFundingACLPreservedState(t, pool); got != want {
		t.Fatalf(
			"broker-funding ACL migration changed durable/catalog state:"+
				"\nwant=%+v\ngot=%+v",
			want,
			got,
		)
	}
}

func assertBrokerFundingACLSourcePreserved(
	t *testing.T,
	pool *pgxpool.Pool,
	want brokerFundingACLPreservedState,
) {
	t.Helper()
	got := readBrokerFundingACLPreservedState(t, pool)
	got.Triggers = want.Triggers
	got.Constraints = want.Constraints
	got.GuardFunctions = want.GuardFunctions
	if got != want {
		t.Fatalf(
			"broker-funding migration changed receipt-backed source state:"+
				"\nwant=%+v\ngot=%+v",
			want,
			got,
		)
	}
}

func assertBrokerFundingACLProvenance(
	t *testing.T,
	pool *pgxpool.Pool,
	wantCount int,
) {
	t.Helper()
	ctx := context.Background()
	var count int
	var invalid int
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (
				WHERE provenance.instrument_id <> 'BTC-PERP'
				   OR provenance.revision <> 1
				   OR provenance.price_scale <> 2
				   OR provenance.quantity_scale <> 3
			)
		  FROM trading.funding_instrument_provenance AS provenance`,
	).Scan(&count, &invalid); err != nil {
		t.Fatalf("read funding instrument provenance: %v", err)
	}
	if count != wantCount || invalid != 0 {
		t.Fatalf(
			"funding provenance count/invalid = %d/%d, want %d/0",
			count,
			invalid,
			wantCount,
		)
	}
	var constraintTriggers string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			string_agg(
				trigger_record.tgname || '|' ||
				trigger_record.tgfoid::pg_catalog.regprocedure::text || '|' ||
				trigger_record.tgtype::text || '|' ||
				trigger_record.tgenabled::text || '|' ||
				trigger_record.tgdeferrable::text || '|' ||
				trigger_record.tginitdeferred::text || '|' ||
				CASE
					WHEN trigger_record.tgconstraint <> 0 THEN 'nonzero'
					ELSE 'zero'
				END || '|' ||
				COALESCE(constraint_record.conname, '') || '|' ||
				COALESCE(constraint_record.contype::text, '') || '|' ||
				COALESCE(
					constraint_record.conrelid::pg_catalog.regclass::text,
					''
				) || '|' ||
				COALESCE(constraint_record.condeferrable::text, '') || '|' ||
				COALESCE(constraint_record.condeferred::text, ''),
				E'\n'
				ORDER BY trigger_record.tgname
			),
			''
		)
		  FROM pg_catalog.pg_trigger AS trigger_record
		  LEFT JOIN pg_catalog.pg_constraint AS constraint_record
		    ON constraint_record.oid = trigger_record.tgconstraint
		 WHERE trigger_record.tgrelid =
				   'trading.funding_settlements'::pg_catalog.regclass
		   AND NOT trigger_record.tgisinternal
		   AND (
			   trigger_record.tgconstraint <> 0
			   OR trigger_record.tgdeferrable
			   OR trigger_record.tginitdeferred
		   )`,
	).Scan(&constraintTriggers); err != nil {
		t.Fatalf("read exact funding constraint-trigger catalog: %v", err)
	}
	const wantConstraintTriggers = "" +
		"funding_settlement_requires_history_projection|" +
		"trading.require_funding_history_projection()|5|O|true|true|" +
		"nonzero|funding_settlement_requires_history_projection|t|" +
		"trading.funding_settlements|true|true\n" +
		"funding_settlement_requires_instrument_provenance|" +
		"trading.require_funding_instrument_provenance()|5|O|true|true|" +
		"nonzero|funding_settlement_requires_instrument_provenance|t|" +
		"trading.funding_settlements|true|true"
	if constraintTriggers != wantConstraintTriggers {
		t.Fatalf(
			"funding constraint-trigger catalog =\n%s\nwant=\n%s",
			constraintTriggers,
			wantConstraintTriggers,
		)
	}
	for _, relation := range brokerFundingACLRelations {
		var guarded bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_trigger AS trigger_record
				 WHERE trigger_record.tgrelid = $1::pg_catalog.regclass
				   AND NOT trigger_record.tgisinternal
				   AND (trigger_record.tgtype & 32) = 32
				   AND trigger_record.tgenabled = 'O'
			)`,
			relation,
		).Scan(&guarded); err != nil {
			t.Fatalf("inspect funding truncate guard on %s: %v", relation, err)
		}
		if !guarded {
			t.Fatalf("%s has no enabled truncate guard", relation)
		}
		_, err := pool.Exec(ctx, "TRUNCATE TABLE "+relation+" CASCADE")
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) ||
			postgresError.Code != "55000" {
			t.Fatalf(
				"owner truncate %s error = %v, want SQLSTATE 55000",
				relation,
				err,
			)
		}
	}
	var functionResult string
	if err := pool.QueryRow(ctx, `
		SELECT pg_catalog.pg_get_function_result(
			'trading.read_broker_account_funding_history(text,bigint,uuid,boolean,integer,boolean)'
				::pg_catalog.regprocedure
		)`,
	).Scan(&functionResult); err != nil {
		t.Fatalf("read broker funding function result: %v", err)
	}
	const wantFunctionResult = "TABLE(funding_id uuid, instrument_id text, instrument_revision bigint, price_scale smallint, quantity_scale smallint, position_id uuid, signed_quantity numeric, oracle_price numeric, funding_rate numeric, funding_amount numeric, settlement_currency text, funding_logical_time bigint)"
	if functionResult != wantFunctionResult {
		t.Fatalf(
			"broker funding function result = %q, want %q",
			functionResult,
			wantFunctionResult,
		)
	}
}

func readBrokerFundingACLHistory(
	t *testing.T,
	pool *pgxpool.Pool,
) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT filename || ':' || pg_catalog.encode(checksum, 'hex')
		  FROM engine.schema_migrations
		 ORDER BY filename`)
	if err != nil {
		t.Fatalf("read broker-funding migration history: %v", err)
	}
	defer rows.Close()
	history := make([]string, 0, 40)
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			t.Fatalf("scan broker-funding migration history: %v", err)
		}
		history = append(history, entry)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate broker-funding migration history: %v", err)
	}
	return history
}

func assertBrokerFundingACLHistoryAdvanced(
	t *testing.T,
	pool *pgxpool.Pool,
	before []string,
) {
	t.Helper()
	after := readBrokerFundingACLHistory(t, pool)
	if len(after) != len(before)+1 {
		t.Fatalf(
			"migration history count = %d, want %d",
			len(after),
			len(before)+1,
		)
	}
	if !slices.Equal(after[:len(before)], before) {
		t.Fatalf(
			"broker-funding migration changed prior checksums:"+
				"\nbefore=%v\nafter=%v",
			before,
			after[:len(before)],
		)
	}
	if !strings.HasPrefix(
		after[len(after)-1],
		brokerFundingACLMigration+":",
	) {
		t.Fatalf(
			"migration tip = %q, want %s",
			after[len(after)-1],
			brokerFundingACLMigration,
		)
	}
}

func readBrokerFundingACLRawACL(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var raw string
	if err := pool.QueryRow(context.Background(), `
		WITH table_privileges AS (
			SELECT
				'T|' ||
				relation.oid::pg_catalog.regclass::text || '|' ||
				COALESCE(grantee.rolname, 'PUBLIC') || '|' ||
				privilege.privilege_type || '|' ||
				privilege.is_grantable::text AS entry
			  FROM pg_catalog.pg_class AS relation
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				COALESCE(
					relation.relacl,
					pg_catalog.acldefault('r', relation.relowner)
				)
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS grantee
			    ON grantee.oid = privilege.grantee
			 WHERE relation.oid::pg_catalog.regclass::text = ANY ($2)
			   AND privilege.grantee <> relation.relowner
		),
		column_privileges AS (
			SELECT
				'C|' ||
				attribute.attrelid::pg_catalog.regclass::text || '.' ||
				attribute.attname || '|' ||
				COALESCE(grantee.rolname, 'PUBLIC') || '|' ||
				privilege.privilege_type || '|' ||
				privilege.is_grantable::text AS entry
			  FROM pg_catalog.pg_attribute AS attribute
			  JOIN pg_catalog.pg_class AS relation
			    ON relation.oid = attribute.attrelid
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				attribute.attacl
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS grantee
			    ON grantee.oid = privilege.grantee
			 WHERE relation.oid::pg_catalog.regclass::text = ANY ($2)
			   AND attribute.attnum > 0
			   AND NOT attribute.attisdropped
			   AND privilege.grantee <> relation.relowner
		),
		function_privileges AS (
			SELECT
				'F|' ||
				procedure.oid::pg_catalog.regprocedure::text || '|' ||
				COALESCE(grantee.rolname, 'PUBLIC') || '|' ||
				privilege.privilege_type || '|' ||
				privilege.is_grantable::text AS entry
			  FROM pg_catalog.pg_proc AS procedure
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				COALESCE(
					procedure.proacl,
					pg_catalog.acldefault('f', procedure.proowner)
				)
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS grantee
			    ON grantee.oid = privilege.grantee
			 WHERE procedure.oid::pg_catalog.regprocedure::text = ANY ($1)
			   AND privilege.grantee <> procedure.proowner
		),
		entries AS (
			SELECT entry FROM table_privileges
			UNION ALL
			SELECT entry FROM column_privileges
			UNION ALL
			SELECT entry FROM function_privileges
		)
		SELECT COALESCE(string_agg(entry, E'\n' ORDER BY entry), '')
		  FROM entries`,
		brokerFundingACLFunctions,
		brokerFundingACLRelations,
	).Scan(&raw); err != nil {
		t.Fatalf("read raw broker-funding ACL: %v", err)
	}
	return raw
}

func assertBrokerFundingACLAllowlist(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	want := []string{
		"F|trading.account_funding_history_count(text)|platformgo_api|EXECUTE|false",
		"F|trading.account_position_funding_total(text,uuid,bigint)|platformgo_api|EXECUTE|false",
		"F|trading.read_account_funding_history(text,bigint,uuid,boolean,integer,boolean)|platformgo_api|EXECUTE|false",
		"F|trading.read_broker_account_funding_history(text,bigint,uuid,boolean,integer,boolean)|platformgo_api|EXECUTE|false",
		"F|trading.read_symbol_funding_history(text,bigint,uuid,boolean,integer,boolean)|platformgo_api|EXECUTE|false",
		"F|trading.symbol_funding_history_count(text)|platformgo_api|EXECUTE|false",
		"T|trading.funding_history_projection|platformgo_engine|INSERT|false",
		"T|trading.funding_history_projection|platformgo_engine|SELECT|false",
		"T|trading.funding_instrument_provenance|platformgo_engine|INSERT|false",
		"T|trading.funding_instrument_provenance|platformgo_engine|SELECT|false",
		"T|trading.funding_settlements|platformgo_engine|INSERT|false",
		"T|trading.funding_settlements|platformgo_engine|SELECT|false",
	}
	got := readBrokerFundingACLRawACL(t, pool)
	if got != strings.Join(want, "\n") {
		t.Fatalf(
			"broker-funding raw ACL =\n%s\nwant=\n%s",
			got,
			strings.Join(want, "\n"),
		)
	}
}

func assertBrokerFundingACLRuntimePrivileges(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	ctx := context.Background()
	for _, relation := range brokerFundingACLRelations {
		for _, privilege := range []string{"SELECT", "INSERT"} {
			var engineAllowed bool
			if err := pool.QueryRow(ctx, `
				SELECT has_table_privilege(
					'platformgo_engine',
					$1,
					$2
				)`,
				relation,
				privilege,
			).Scan(&engineAllowed); err != nil {
				t.Fatalf(
					"inspect engine %s on %s: %v",
					privilege,
					relation,
					err,
				)
			}
			if !engineAllowed {
				t.Fatalf(
					"platformgo_engine lacks %s on %s",
					privilege,
					relation,
				)
			}
		}
		for _, privilege := range []string{
			"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE",
		} {
			var apiAllowed bool
			if err := pool.QueryRow(ctx, `
				SELECT has_table_privilege(
					'platformgo_api',
					$1,
					$2
				)`,
				relation,
				privilege,
			).Scan(&apiAllowed); err != nil {
				t.Fatalf(
					"inspect API %s on %s: %v",
					privilege,
					relation,
					err,
				)
			}
			if apiAllowed {
				t.Fatalf(
					"platformgo_api retained %s on %s",
					privilege,
					relation,
				)
			}
		}
	}
	for _, function := range brokerFundingACLFunctions {
		var apiAllowed bool
		if err := pool.QueryRow(ctx, `
			SELECT has_function_privilege(
				'platformgo_api',
				$1,
				'EXECUTE'
			)`,
			function,
		).Scan(&apiAllowed); err != nil {
			t.Fatalf("inspect API EXECUTE on %s: %v", function, err)
		}
		want := slices.Contains(brokerFundingACLAPIReadFunctions, function)
		if apiAllowed != want {
			t.Fatalf(
				"platformgo_api EXECUTE on %s = %t, want %t",
				function,
				apiAllowed,
				want,
			)
		}
	}
}

func assertBrokerFundingACLFixtureVulnerable(
	t *testing.T,
	pool *pgxpool.Pool,
	hostileRole string,
	dependentRole string,
) {
	t.Helper()
	ctx := context.Background()
	for _, role := range []string{hostileRole, dependentRole} {
		for _, relation := range brokerFundingACLLegacyRelations {
			var allowed bool
			if err := pool.QueryRow(ctx, `
				SELECT has_table_privilege($1, $2, 'DELETE')`,
				role,
				relation,
			).Scan(&allowed); err != nil {
				t.Fatalf(
					"inspect vulnerable %s DELETE on %s: %v",
					role,
					relation,
					err,
				)
			}
			if !allowed {
				t.Fatalf(
					"hostile fixture did not grant %s DELETE on %s",
					role,
					relation,
				)
			}
		}
		for _, function := range brokerFundingACLLegacyFunctions {
			var allowed bool
			if err := pool.QueryRow(ctx, `
				SELECT has_function_privilege($1, $2, 'EXECUTE')`,
				role,
				function,
			).Scan(&allowed); err != nil {
				t.Fatalf(
					"inspect vulnerable %s EXECUTE on %s: %v",
					role,
					function,
					err,
				)
			}
			if !allowed {
				t.Fatalf(
					"hostile fixture did not grant %s EXECUTE on %s",
					role,
					function,
				)
			}
		}
	}
}

func assertBrokerFundingACLRoleHasNoPrivileges(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	ctx := context.Background()
	for _, relation := range brokerFundingACLRelations {
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
				t.Fatalf(
					"inspect scrubbed %s %s on %s: %v",
					role,
					privilege,
					relation,
					err,
				)
			}
			if allowed {
				t.Fatalf("%s retained %s on %s", role, privilege, relation)
			}
		}
	}
	for _, function := range brokerFundingACLFunctions {
		var allowed bool
		if err := pool.QueryRow(ctx, `
			SELECT has_function_privilege($1, $2, 'EXECUTE')`,
			role,
			function,
		).Scan(&allowed); err != nil {
			t.Fatalf(
				"inspect scrubbed %s EXECUTE on %s: %v",
				role,
				function,
				err,
			)
		}
		if allowed {
			t.Fatalf("%s retained EXECUTE on %s", role, function)
		}
	}
}
