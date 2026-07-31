package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
)

type countedAdminRiskStateReader struct {
	reader application.AdminRiskStateReader
	calls  int
}

func (reader *countedAdminRiskStateReader) AdminRiskStateExists(
	ctx context.Context,
) (bool, error) {
	reader.calls++
	return reader.reader.AdminRiskStateExists(ctx)
}

// TestAdminRiskMonitorReadsAndIsGated ports only the source test's fresh
// database read and authorization denial through the real least-privilege
// runtime role and production application/PostgreSQL boundary.
//
// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_admin_fleet_blotter.rs:136
//	test: admin_risk_monitor_reads_and_is_gated
//
// Adaptations:
//   - The source composition's migrated database is replaced by the native
//     migrator and a real login inheriting platformgo_api.
//   - The source dispatcher is represented by the production application
//     handler over the production PostgreSQL boolean authority reader.
//
// Assertions preserved:
//   - A fresh database returns no at-risk accounts.
//   - A non-admin principal is forbidden.
//
// Strengthening:
//   - The successful empty slice is non-nil.
//   - Client wildcard denial occurs before the PostgreSQL reader is invoked.
//   - The reader needs only EXECUTE on its narrow authority function and does
//     not expose raw ledger rows to the API role.
//   - Any supported durable economic root fails closed without inventing risk
//     values or exposing a partial result.
//   - Market-only state remains an exact empty result.
//   - Separate-session visibility follows PostgreSQL commit and rollback.
func TestAdminRiskMonitorReadsAndIsGated(t *testing.T) {
	ctx := context.Background()

	t.Run("fresh database succeeds and is gated", func(t *testing.T) {
		_, apiPool := migratedAdminRiskMonitorPools(t)
		assertAdminRiskMonitorAPIBoundary(t, apiPool)

		reader := platformpostgres.NewAdminRiskMonitorReader(apiPool)
		counted := &countedAdminRiskStateReader{reader: reader}
		handler := application.NewAdminRiskMonitorHandler(counted)
		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		accounts, err := handler.Handle(ctx, adminPrincipal)
		if err != nil {
			t.Fatalf("read fresh risk monitor through platformgo_api: %v", err)
		}
		if accounts == nil || len(accounts) != 0 {
			t.Fatalf("fresh at-risk accounts = %#v, want non-nil empty", accounts)
		}
		if counted.calls != 1 {
			t.Fatalf("fresh risk monitor reader calls = %d, want 1", counted.calls)
		}

		forbiddenAccounts, err := handler.Handle(ctx, edge.Principal{
			Subject:  "client-1",
			Audience: edge.AudienceClient,
			Scopes:   []string{"*"},
			Accounts: []string{"*"},
		})
		if !errors.Is(err, edge.ErrForbidden) {
			t.Fatalf("client risk monitor error = %v, want forbidden", err)
		}
		if counted.calls != 1 {
			t.Fatalf("forbidden request reader calls = %d, want unchanged 1", counted.calls)
		}
		if forbiddenAccounts != nil {
			t.Fatalf(
				"forbidden request returned partial accounts %#v",
				forbiddenAccounts,
			)
		}
	})

	t.Run("market-only state remains empty", func(t *testing.T) {
		adminPool, apiPool := migratedAdminRiskMonitorPools(t)
		if _, err := adminPool.Exec(ctx, `
			INSERT INTO trading.instruments (
				instrument_id, revision, price_scale, quantity_scale,
				settlement_currency, settlement_currency_scale,
				initial_margin_rate, maintenance_margin_rate, max_leverage,
				maker_fee_rate, taker_fee_rate
			) VALUES (
				'BTC-PERP', 1, 2, 3, 'USDC', 2,
				0.1, 0.05, 10, -0.0001, 0.0005
			);
			INSERT INTO market.books (
				instrument_id, mark_price, bids, asks, stream_sequence
			) VALUES (
				'BTC-PERP', 60000, '[[59999,1]]', '[[60001,1]]', 1
			)`); err != nil {
			t.Fatalf("seed market-only state: %v", err)
		}

		handler := application.NewAdminRiskMonitorHandler(
			platformpostgres.NewAdminRiskMonitorReader(apiPool),
		)
		requireAdminRiskMonitorEmpty(t, handler)
	})

	for _, root := range []struct {
		name string
		seed func(*testing.T, *pgxpool.Pool)
	}{
		{
			name: "account root",
			seed: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO trading.accounts (account_id, oms_mode)
					VALUES ('urn:xb:account:risk-account', 'NETTING')`); err != nil {
					t.Fatalf("seed account root: %v", err)
				}
			},
		},
		{
			name: "command root",
			seed: seedAdminRiskCommandRoot,
		},
		{
			name: "account shard root",
			seed: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO engine.account_shards (account_id, shard_id)
					VALUES ('urn:xb:account:risk-shard', 7)`); err != nil {
					t.Fatalf("seed account shard root: %v", err)
				}
			},
		},
		{
			name: "balance root",
			seed: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO ledger.balances (
						account_id, currency, total, used, free, equity,
						ledger_sequence
					) VALUES (
						'urn:xb:account:orphan-balance', 'USDC',
						10, 0, 10, 10, 1
					)`); err != nil {
					t.Fatalf("seed orphan balance root: %v", err)
				}
			},
		},
		{
			name: "transaction root",
			seed: func(t *testing.T, pool *pgxpool.Pool) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO ledger.transactions (
						transaction_id, business_key, input_id, logical_time
					) VALUES (
						'019fa92d-0000-4000-8000-000000000021',
						'admin-risk-transaction-only',
						'019fa92d-0000-4000-8000-000000000022',
						1785283200000000000
					)`); err != nil {
					t.Fatalf("seed transaction root: %v", err)
				}
			},
		},
		{
			name: "balanced ledger entries root",
			seed: seedAdminRiskBalancedLedgerRoot,
		},
	} {
		root := root
		t.Run(root.name+" fails closed after reconstruction", func(t *testing.T) {
			adminPool, apiPool := migratedAdminRiskMonitorPools(t)
			root.seed(t, adminPool)

			handler := application.NewAdminRiskMonitorHandler(
				platformpostgres.NewAdminRiskMonitorReader(apiPool),
			)
			requireAdminRiskMonitorNonEmpty(t, handler)

			reconstructed := application.NewAdminRiskMonitorHandler(
				platformpostgres.NewAdminRiskMonitorReader(apiPool),
			)
			requireAdminRiskMonitorNonEmpty(t, reconstructed)
		})
	}

	t.Run("legal provisioning gap fails closed", func(t *testing.T) {
		adminPool, apiPool := migratedAdminRiskMonitorPools(t)
		seedAdminRiskProvisioningGap(t, adminPool)

		var (
			accountCount int64
			shardCount   int64
			commandCount int64
			intentCount  int64
		)
		if err := adminPool.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM trading.accounts),
				(SELECT count(*) FROM engine.account_shards),
				(SELECT count(*) FROM trading.commands),
				(SELECT count(*) FROM identity.account_provisioning_intents)`,
		).Scan(
			&accountCount,
			&shardCount,
			&commandCount,
			&intentCount,
		); err != nil {
			t.Fatalf("inspect provisioning gap: %v", err)
		}
		if accountCount != 0 || shardCount != 1 ||
			commandCount != 1 || intentCount != 1 {
			t.Fatalf(
				"provisioning gap accounts/shards/commands/intents = %d/%d/%d/%d, want 0/1/1/1",
				accountCount,
				shardCount,
				commandCount,
				intentCount,
			)
		}

		handler := application.NewAdminRiskMonitorHandler(
			platformpostgres.NewAdminRiskMonitorReader(apiPool),
		)
		requireAdminRiskMonitorNonEmpty(t, handler)
	})

	t.Run("separate session sees account only after commit", func(t *testing.T) {
		adminPool, apiPool := migratedAdminRiskMonitorPools(t)
		tx, err := adminPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin account transaction: %v", err)
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()
		insertAdminRiskAccount(t, tx, "urn:xb:account:risk-commit")

		handler := application.NewAdminRiskMonitorHandler(
			platformpostgres.NewAdminRiskMonitorReader(apiPool),
		)
		requireAdminRiskMonitorEmpty(t, handler)

		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit account transaction: %v", err)
		}
		requireAdminRiskMonitorNonEmpty(t, handler)
	})

	t.Run("rolled back account remains invisible", func(t *testing.T) {
		adminPool, apiPool := migratedAdminRiskMonitorPools(t)
		tx, err := adminPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin rollback account transaction: %v", err)
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()
		insertAdminRiskAccount(t, tx, "urn:xb:account:risk-rollback")

		handler := application.NewAdminRiskMonitorHandler(
			platformpostgres.NewAdminRiskMonitorReader(apiPool),
		)
		requireAdminRiskMonitorEmpty(t, handler)

		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("roll back account transaction: %v", err)
		}
		requireAdminRiskMonitorEmpty(t, handler)
	})
}

func migratedAdminRiskMonitorPools(
	t *testing.T,
) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	adminPool := postgresPool(t)
	resetDurableSchemas(t, adminPool)
	if err := newCurrentTestMigrator(
		t,
		adminPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("migrate admin risk monitor database: %v", err)
	}
	apiPool := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_admin_risk_monitor_api_login",
		"platformgo_api",
	)
	return adminPool, apiPool
}

func assertAdminRiskMonitorAPIBoundary(
	t *testing.T,
	apiPool *pgxpool.Pool,
) {
	t.Helper()
	var (
		inheritsAPIRole  bool
		canExecuteReader bool
		canReadEntries   bool
	)
	if err := apiPool.QueryRow(context.Background(), `
		SELECT
			pg_has_role(current_user, 'platformgo_api', 'USAGE'),
			has_function_privilege(
				current_user,
				'trading.admin_risk_state_exists()',
				'EXECUTE'
			),
			has_table_privilege(current_user, 'ledger.entries', 'SELECT')`,
	).Scan(
		&inheritsAPIRole,
		&canExecuteReader,
		&canReadEntries,
	); err != nil {
		t.Fatalf("inspect platformgo_api risk monitor privileges: %v", err)
	}
	if !inheritsAPIRole || !canExecuteReader || canReadEntries {
		t.Fatalf(
			"platformgo_api inherited=%t function_execute=%t raw_entries_select=%t, want true/true/false",
			inheritsAPIRole,
			canExecuteReader,
			canReadEntries,
		)
	}
}

func seedAdminRiskCommandRoot(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:urn:xb:account:risk-command',
			'admin-risk-command',
			decode(repeat('2d', 32), 'hex'),
			'019fa92d-0000-4000-8000-000000000011',
			'in_progress',
			'2026-07-30T00:00:00Z'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019fa92d-0000-4000-8000-000000000011',
			'urn:xb:account:risk-command',
			1,
			'probe',
			1,
			'{}',
			'pending',
			1785283200000000000
		)`); err != nil {
		t.Fatalf("seed command root: %v", err)
	}
}

func seedAdminRiskBalancedLedgerRoot(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin balanced orphan ledger seed: %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO ledger.transactions (
			transaction_id, business_key, input_id, logical_time
		) VALUES (
			'019fa92d-0000-4000-8000-000000000031',
			'admin-risk-balanced-orphan',
			'019fa92d-0000-4000-8000-000000000032',
			1785283200000000000
		);
		INSERT INTO ledger.entries (
			entry_id, transaction_id, account_id, currency, amount
		) VALUES
			(
				'019fa92d-0000-4000-8000-000000000033',
				'019fa92d-0000-4000-8000-000000000031',
				'urn:xb:account:orphan-ledger',
				'USDC',
				10
			),
			(
				'019fa92d-0000-4000-8000-000000000034',
				'019fa92d-0000-4000-8000-000000000031',
				'system:clearing',
				'USDC',
				-10
			)`); err != nil {
		t.Fatalf("seed balanced orphan ledger: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit balanced orphan ledger: %v", err)
	}
}

func seedAdminRiskProvisioningGap(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			'urn:xb:user:admin-risk-gap',
			'admin-risk-gap',
			'admin-risk-gap',
			'urn:xb:tenant:admin-risk-gap'
		);
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('urn:xb:account:admin-risk-gap', 7);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'broker-accounturn:xb:apikey:admin-risk-gap',
			'admin-risk-gap',
			decode(repeat('3d', 32), 'hex'),
			'019fa92d-0000-4000-8000-000000000041',
			'in_progress',
			'2026-07-30T00:00:00Z'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019fa92d-0000-4000-8000-000000000041',
			'urn:xb:account:admin-risk-gap',
			1,
			'configure_account',
			1,
			'{"configureAccount":{"accountId":"urn:xb:account:admin-risk-gap","omsMode":"NETTING"}}',
			'pending',
			1785283200000000000
		);
		INSERT INTO identity.account_provisioning_intents (
			command_id, account_id, broker_subject, user_id, login,
			base_currency, market_venue, permitted_classes, created_at
		) VALUES (
			'019fa92d-0000-4000-8000-000000000041',
			'urn:xb:account:admin-risk-gap',
			'urn:xb:tenant:admin-risk-gap',
			'urn:xb:user:admin-risk-gap',
			2901,
			'USDC',
			'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'],
			'2026-07-29T00:00:00Z'
		)`); err != nil {
		t.Fatalf("seed legal provisioning gap: %v", err)
	}
}

type adminRiskAccountExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func insertAdminRiskAccount(
	t *testing.T,
	executor adminRiskAccountExecutor,
	accountID string,
) {
	t.Helper()
	if _, err := executor.Exec(context.Background(), `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ($1, 'NETTING')`,
		accountID,
	); err != nil {
		t.Fatalf("insert admin risk account: %v", err)
	}
}

func requireAdminRiskMonitorEmpty(
	t *testing.T,
	handler *application.AdminRiskMonitorHandler,
) {
	t.Helper()
	accounts, err := handler.Handle(context.Background(), edge.Principal{
		Subject:  "admin-system",
		Audience: edge.AudienceAdmin,
	})
	if err != nil {
		t.Fatalf("read empty admin risk monitor: %v", err)
	}
	if accounts == nil || len(accounts) != 0 {
		t.Fatalf("at-risk accounts = %#v, want non-nil empty", accounts)
	}
}

func requireAdminRiskMonitorNonEmpty(
	t *testing.T,
	handler *application.AdminRiskMonitorHandler,
) {
	t.Helper()
	accounts, err := handler.Handle(context.Background(), edge.Principal{
		Subject:  "admin-system",
		Audience: edge.AudienceAdmin,
	})
	var nonEmptyError *application.AdminRiskMonitorNonEmptyStateError
	if !errors.As(err, &nonEmptyError) {
		t.Fatalf("durable risk state error = %v, want typed non-empty error", err)
	}
	if accounts != nil {
		t.Fatalf("durable risk state returned partial accounts %#v", accounts)
	}
}
