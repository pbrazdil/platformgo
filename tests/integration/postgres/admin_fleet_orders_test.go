package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
)

type countedAdminFleetOrdersReader struct {
	reader application.AdminFleetOrdersExistenceReader
	calls  int
}

type adminFleetOrdersClock struct {
	value time.Time
}

func (clock adminFleetOrdersClock) Now() time.Time {
	return clock.value
}

func (reader *countedAdminFleetOrdersReader) AdminFleetOrdersExist(
	ctx context.Context,
) (bool, error) {
	reader.calls++
	return reader.reader.AdminFleetOrdersExist(ctx)
}

// TestAdminFleetOrdersBlotterReadsAndIsGated ports the source test's fresh
// database read and authorization denial through the real least-privilege
// runtime role and production application/PostgreSQL boundary.
//
// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_admin_fleet_blotter.rs:71
//	test: admin_fleet_orders_blotter_reads_and_is_gated
//
// Adaptations:
//   - The source composition's migrated database is replaced by the native
//     migrator and a real login inheriting platformgo_api.
//   - The source dispatcher is represented by the production application
//     handler over the production PostgreSQL existence reader.
//
// Assertions preserved:
//   - A fresh database returns an empty fleet orders page.
//   - The first page carries a present total equal to exactly zero.
//   - A client principal is forbidden.
//
// Strengthening:
//   - The successful empty items slice is non-nil.
//   - The real platformgo_api boundary can SELECT orders and order intents,
//     while its existing INSERT authority on order intents remains intact.
//   - Client wildcard denial occurs before the PostgreSQL reader is invoked.
//   - An admitted intent without a materialized order fails closed.
//   - A committed materialized order fails closed without exposing a partial
//     page, including after reconstructing the stateless handler and reader.
func TestAdminFleetOrdersBlotterReadsAndIsGated(t *testing.T) {
	ctx := context.Background()

	t.Run("fresh read authorization and admitted intent", func(t *testing.T) {
		adminPool := postgresPool(t)
		resetDurableSchemas(t, adminPool)
		if err := platformpostgres.NewMigrator(
			adminPool,
			os.DirFS(filepath.Join("..", "..", "..", "migrations")),
		).MigrateAndProvision(ctx, 7); err != nil {
			t.Fatalf("migrate admin fleet orders database: %v", err)
		}
		seedAdminFleetOrdersInstrument(t, adminPool)

		apiPool := runtimeRoleLoginPool(
			t,
			adminPool,
			"platformgo_admin_fleet_orders_intent_api_login",
			"platformgo_api",
		)
		assertAdminFleetOrdersAPIPrivileges(t, apiPool)
		assertAdminFleetOrdersReaderDependencies(t, adminPool)

		reader := platformpostgres.NewAdminFleetOrdersReader(apiPool)
		counted := &countedAdminFleetOrdersReader{reader: reader}
		handler := application.NewAdminFleetOrdersHandler(counted)
		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		page, err := handler.Handle(ctx, adminPrincipal)
		if err != nil {
			t.Fatalf("read fresh fleet orders through platformgo_api: %v", err)
		}
		if page.Items == nil || len(page.Items) != 0 {
			t.Fatalf("fresh fleet orders items = %#v, want non-nil empty", page.Items)
		}
		if page.Total == nil || *page.Total != 0 {
			t.Fatalf("fresh fleet orders total = %v, want present exact 0", page.Total)
		}
		if counted.calls != 1 {
			t.Fatalf("fresh fleet orders reader calls = %d, want 1", counted.calls)
		}

		forbiddenPage, err := handler.Handle(ctx, edge.Principal{
			Subject:  "client-1",
			Audience: edge.AudienceClient,
			Scopes:   []string{"*"},
			Accounts: []string{"*"},
		})
		if !errors.Is(err, edge.ErrForbidden) {
			t.Fatalf("client fleet orders error = %v, want forbidden", err)
		}
		if counted.calls != 1 {
			t.Fatalf("forbidden request reader calls = %d, want unchanged 1", counted.calls)
		}
		if forbiddenPage.Items != nil || forbiddenPage.Total != nil {
			t.Fatalf("forbidden request returned partial page %#v", forbiddenPage)
		}

		admitAdminFleetOrderIntent(t, apiPool)
		intentPage, err := handler.Handle(ctx, adminPrincipal)
		var nonEmptyError *application.AdminFleetOrdersNonEmptyStateError
		if !errors.As(err, &nonEmptyError) {
			t.Fatalf("admitted intent error = %v, want typed unsupported state", err)
		}
		if intentPage.Items != nil || intentPage.Total != nil {
			t.Fatalf("admitted intent returned partial page %#v", intentPage)
		}
	})

	t.Run("materialized order survives reader reconstruction", func(t *testing.T) {
		adminPool := postgresPool(t)
		resetDurableSchemas(t, adminPool)
		if err := platformpostgres.NewMigrator(
			adminPool,
			os.DirFS(filepath.Join("..", "..", "..", "migrations")),
		).Migrate(ctx); err != nil {
			t.Fatalf("migrate materialized fleet orders database: %v", err)
		}
		apiPool := runtimeRoleLoginPool(
			t,
			adminPool,
			"platformgo_admin_fleet_orders_materialized_api_login",
			"platformgo_api",
		)
		seedAdminFleetOrder(t, adminPool)

		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		handler := application.NewAdminFleetOrdersHandler(
			platformpostgres.NewAdminFleetOrdersReader(apiPool),
		)
		page, err := handler.Handle(ctx, adminPrincipal)
		var nonEmptyError *application.AdminFleetOrdersNonEmptyStateError
		if !errors.As(err, &nonEmptyError) {
			t.Fatalf("materialized order error = %v, want typed unsupported state", err)
		}
		if page.Items != nil || page.Total != nil {
			t.Fatalf("materialized order returned partial page %#v", page)
		}

		restartedHandler := application.NewAdminFleetOrdersHandler(
			platformpostgres.NewAdminFleetOrdersReader(apiPool),
		)
		restartedPage, err := restartedHandler.Handle(ctx, adminPrincipal)
		if !errors.As(err, &nonEmptyError) {
			t.Fatalf("restarted materialized order error = %v, want typed unsupported state", err)
		}
		if restartedPage.Items != nil || restartedPage.Total != nil {
			t.Fatalf("restarted materialized order returned partial page %#v", restartedPage)
		}
	})
}

func assertAdminFleetOrdersReaderDependencies(
	t *testing.T,
	adminPool *pgxpool.Pool,
) {
	t.Helper()
	const probeRole = "platformgo_admin_fleet_orders_reader_probe"
	probeID := pgx.Identifier{probeRole}.Sanitize()
	if _, err := adminPool.Exec(context.Background(), fmt.Sprintf(`
		CREATE ROLE %s NOLOGIN;
		GRANT USAGE ON SCHEMA trading TO %s;
		GRANT SELECT ON trading.orders, trading.order_intents TO %s`,
		probeID,
		probeID,
		probeID,
	)); err != nil {
		t.Fatalf("create admin fleet orders dependency probe: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), fmt.Sprintf(`
			DROP OWNED BY %s CASCADE;
			DROP ROLE IF EXISTS %s`,
			probeID,
			probeID,
		))
	})
	probePool := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_admin_fleet_orders_reader_probe_login",
		probeRole,
	)
	exists, err := platformpostgres.NewAdminFleetOrdersReader(
		probePool,
	).AdminFleetOrdersExist(context.Background())
	if err != nil {
		t.Fatalf("read orders without command-table privilege: %v", err)
	}
	if exists {
		t.Fatalf("fresh dependency probe found order state")
	}
}

func assertAdminFleetOrdersAPIPrivileges(
	t *testing.T,
	apiPool *pgxpool.Pool,
) {
	t.Helper()
	var (
		inheritsAPIRole      bool
		canReadOrders        bool
		canReadOrderIntents  bool
		canInsertOrderIntent bool
	)
	if err := apiPool.QueryRow(context.Background(), `
		SELECT
			pg_has_role(current_user, 'platformgo_api', 'USAGE'),
			has_table_privilege(current_user, 'trading.orders', 'SELECT'),
			has_table_privilege(
				current_user,
				'trading.order_intents',
				'SELECT'
			),
			has_table_privilege(
				current_user,
				'trading.order_intents',
				'INSERT'
			)`,
	).Scan(
		&inheritsAPIRole,
		&canReadOrders,
		&canReadOrderIntents,
		&canInsertOrderIntent,
	); err != nil {
		t.Fatalf("inspect platformgo_api order privileges: %v", err)
	}
	if !inheritsAPIRole ||
		!canReadOrders ||
		!canReadOrderIntents ||
		!canInsertOrderIntent {
		t.Fatalf(
			"platformgo_api boundary inherited=%t orders-select=%t intents-select=%t intents-insert=%t, want true/true/true/true",
			inheritsAPIRole,
			canReadOrders,
			canReadOrderIntents,
			canInsertOrderIntent,
		)
	}
}

func admitAdminFleetOrderIntent(t *testing.T, apiPool *pgxpool.Pool) {
	t.Helper()
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	submission, err := application.NewOrderSubmission(
		platformpostgres.NewCommandJournal(apiPool),
		application.OrderSubmissionConfig{
			ShardID:        7,
			IdempotencyTTL: 24 * time.Hour,
			Clock:          adminFleetOrdersClock{value: now},
		},
	)
	if err != nil {
		t.Fatalf("construct admin fleet order admission: %v", err)
	}
	accountID := "urn:xb:account:admin-orders-intent"
	admission, err := submission.SubmitOrder(
		context.Background(),
		edge.Principal{
			Subject:  "urn:xb:user:admin-orders-intent",
			Audience: edge.AudienceClient,
			Accounts: []string{accountID},
		},
		accountID,
		"admin-orders-admission-gap",
		edge.SubmitOrderRequest{
			IntentID: "admin-orders-admission-gap",
			Symbol:   "BTC-PERP",
			Side:     "buy",
			Type:     "MARKET",
			Quantity: "0.01",
		},
	)
	if err != nil {
		t.Fatalf("admit admin fleet order through production API boundary: %v", err)
	}
	if admission.Response.Status != 202 {
		t.Fatalf(
			"admitted admin fleet order status = %d, want 202",
			admission.Response.Status,
		)
	}
}

func seedAdminFleetOrdersInstrument(t *testing.T, adminPool *pgxpool.Pool) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(), `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		)`,
	); err != nil {
		t.Fatalf("seed admin fleet orders instrument: %v", err)
	}
}

func seedAdminFleetOrder(t *testing.T, adminPool *pgxpool.Pool) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(), `
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
		VALUES ('urn:xb:account:admin-orders-materialized', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa911-0000-4000-8000-000000000001',
			'urn:xb:account:admin-orders-materialized',
			'BTC-PERP',
			'BUY',
			'LIMIT',
			'GTC',
			'WORKING',
			0.01,
			0,
			0,
			false,
			false,
			true,
			1
		)`,
	); err != nil {
		t.Fatalf("seed materialized admin fleet order: %v", err)
	}
}
