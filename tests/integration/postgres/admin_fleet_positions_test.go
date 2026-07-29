package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
)

type countedAdminFleetPositionsReader struct {
	reader application.AdminFleetPositionsExistenceReader
	calls  int
}

func (reader *countedAdminFleetPositionsReader) AdminFleetPositionsExist(
	ctx context.Context,
) (bool, error) {
	reader.calls++
	return reader.reader.AdminFleetPositionsExist(ctx)
}

// TestAdminFleetPositionsBlotterReadsAndIsGated ports the source test's fresh
// database read and authorization denial through the real least-privilege
// runtime role and production application/PostgreSQL boundary.
//
// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_admin_fleet_blotter.rs:108
//	test: admin_fleet_positions_blotter_reads_and_is_gated
//
// Adaptations:
//   - The source composition's migrated database is replaced by the native
//     migrator and a real login inheriting platformgo_api.
//   - The source dispatcher is represented by the production application
//     handler over the production PostgreSQL existence reader.
//
// Assertions preserved:
//   - A fresh database returns an empty fleet positions page.
//   - The capped page carries a present total equal to exactly zero.
//   - A non-admin principal is forbidden.
//
// Strengthening:
//   - The successful empty items slice is non-nil.
//   - The real platformgo_api boundary can SELECT positions and fills but
//     cannot mutate either economic relation.
//   - Client wildcard denial occurs before the PostgreSQL reader is invoked.
//   - Any committed position, or a fill without a matching position row, fails
//     closed without exposing a partial page.
//   - Separate-session reads see only committed state, and reconstruction does
//     not weaken the fail-closed behavior.
func TestAdminFleetPositionsBlotterReadsAndIsGated(t *testing.T) {
	ctx := context.Background()

	t.Run("both relations empty succeeds and is gated", func(t *testing.T) {
		adminPool, apiPool := migratedAdminFleetPositionsPools(t)
		assertAdminFleetPositionsAPIPrivileges(t, apiPool)
		assertAdminFleetPositionsAPIMutationsDenied(t, apiPool)
		assertAdminFleetPositionsReaderDependencies(t, adminPool)

		reader := platformpostgres.NewAdminFleetPositionsReader(apiPool)
		counted := &countedAdminFleetPositionsReader{reader: reader}
		handler := application.NewAdminFleetPositionsHandler(counted)
		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		page, err := handler.Handle(ctx, adminPrincipal)
		if err != nil {
			t.Fatalf("read fresh fleet positions through platformgo_api: %v", err)
		}
		if page.Items == nil || len(page.Items) != 0 {
			t.Fatalf("fresh fleet positions items = %#v, want non-nil empty", page.Items)
		}
		if page.Total == nil || *page.Total != 0 {
			t.Fatalf("fresh fleet positions total = %v, want present exact 0", page.Total)
		}
		if counted.calls != 1 {
			t.Fatalf("fresh fleet positions reader calls = %d, want 1", counted.calls)
		}

		forbiddenPage, err := handler.Handle(ctx, edge.Principal{
			Subject:  "client-1",
			Audience: edge.AudienceClient,
			Scopes:   []string{"*"},
			Accounts: []string{"*"},
		})
		if !errors.Is(err, edge.ErrForbidden) {
			t.Fatalf("client fleet positions error = %v, want forbidden", err)
		}
		if counted.calls != 1 {
			t.Fatalf("forbidden request reader calls = %d, want unchanged 1", counted.calls)
		}
		if forbiddenPage.Items != nil || forbiddenPage.Total != nil {
			t.Fatalf("forbidden request returned partial page %#v", forbiddenPage)
		}
	})

	t.Run("committed position fails closed after reconstruction", func(t *testing.T) {
		adminPool, apiPool := migratedAdminFleetPositionsPools(t)
		seedAdminFleetPositionAuthority(t, adminPool)
		insertAdminFleetPosition(t, adminPool)

		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		handler := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		requireAdminFleetPositionsNonEmpty(t, handler, adminPrincipal)

		reconstructed := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		requireAdminFleetPositionsNonEmpty(t, reconstructed, adminPrincipal)
	})

	t.Run("closed position fails closed after reconstruction", func(t *testing.T) {
		adminPool, apiPool := migratedAdminFleetPositionsPools(t)
		seedAdminFleetPositionAuthority(t, adminPool)
		insertAdminFleetPosition(t, adminPool)
		if _, err := adminPool.Exec(ctx, `
			UPDATE trading.positions
			   SET side = 'FLAT',
			       status = 'CLOSED',
			       signed_quantity = 0,
			       average_open_price = 0
		`); err != nil {
			t.Fatalf("close admin fleet position: %v", err)
		}

		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		handler := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		requireAdminFleetPositionsNonEmpty(t, handler, adminPrincipal)

		reconstructed := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		requireAdminFleetPositionsNonEmpty(t, reconstructed, adminPrincipal)
	})

	t.Run("fill-only anomaly fails closed after reconstruction", func(t *testing.T) {
		adminPool, apiPool := migratedAdminFleetPositionsPools(t)
		seedAdminFleetFill(t, adminPool)

		var (
			positionCount int64
			fillCount     int64
		)
		if err := adminPool.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM trading.positions),
				(SELECT count(*) FROM trading.fills)`,
		).Scan(&positionCount, &fillCount); err != nil {
			t.Fatalf("inspect fill-only anomaly: %v", err)
		}
		if positionCount != 0 || fillCount != 1 {
			t.Fatalf(
				"fill-only anomaly positions=%d fills=%d, want 0/1",
				positionCount,
				fillCount,
			)
		}

		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		handler := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		requireAdminFleetPositionsNonEmpty(t, handler, adminPrincipal)

		reconstructed := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		requireAdminFleetPositionsNonEmpty(t, reconstructed, adminPrincipal)
	})

	t.Run("matching position and fill fail closed after reconstruction", func(t *testing.T) {
		adminPool, apiPool := migratedAdminFleetPositionsPools(t)
		seedAdminFleetFill(t, adminPool)
		insertAdminFleetPositionForFill(t, adminPool)

		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		handler := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		requireAdminFleetPositionsNonEmpty(t, handler, adminPrincipal)

		reconstructed := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		requireAdminFleetPositionsNonEmpty(t, reconstructed, adminPrincipal)
	})

	t.Run("separate session sees position only after commit", func(t *testing.T) {
		adminPool, apiPool := migratedAdminFleetPositionsPools(t)
		seedAdminFleetPositionAuthority(t, adminPool)
		tx, err := adminPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin position transaction: %v", err)
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()
		insertAdminFleetPositionWithExecutor(t, tx)

		handler := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		page, err := handler.Handle(ctx, adminPrincipal)
		if err != nil {
			t.Fatalf("read while position is uncommitted: %v", err)
		}
		if page.Items == nil || len(page.Items) != 0 ||
			page.Total == nil || *page.Total != 0 {
			t.Fatalf("uncommitted position leaked into page %#v", page)
		}

		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit position transaction: %v", err)
		}
		requireAdminFleetPositionsNonEmpty(t, handler, adminPrincipal)
	})

	t.Run("rolled back position remains invisible", func(t *testing.T) {
		adminPool, apiPool := migratedAdminFleetPositionsPools(t)
		seedAdminFleetPositionAuthority(t, adminPool)
		tx, err := adminPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin rollback position transaction: %v", err)
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()
		insertAdminFleetPositionWithExecutor(t, tx)

		handler := application.NewAdminFleetPositionsHandler(
			platformpostgres.NewAdminFleetPositionsReader(apiPool),
		)
		adminPrincipal := edge.Principal{
			Subject:  "admin-system",
			Audience: edge.AudienceAdmin,
		}
		if _, err := handler.Handle(ctx, adminPrincipal); err != nil {
			t.Fatalf("read while rollback position is uncommitted: %v", err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("roll back position transaction: %v", err)
		}
		page, err := handler.Handle(ctx, adminPrincipal)
		if err != nil {
			t.Fatalf("read after position rollback: %v", err)
		}
		if page.Items == nil || len(page.Items) != 0 ||
			page.Total == nil || *page.Total != 0 {
			t.Fatalf("rolled-back position leaked into page %#v", page)
		}
	})
}

func migratedAdminFleetPositionsPools(
	t *testing.T,
) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	adminPool := postgresPool(t)
	resetDurableSchemas(t, adminPool)
	if err := platformpostgres.NewMigrator(
		adminPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate admin fleet positions database: %v", err)
	}
	apiPool := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_admin_fleet_positions_api_login",
		"platformgo_api",
	)
	return adminPool, apiPool
}

func assertAdminFleetPositionsAPIPrivileges(
	t *testing.T,
	apiPool *pgxpool.Pool,
) {
	t.Helper()
	var (
		inheritsAPIRole   bool
		canReadPositions  bool
		canInsertPosition bool
		canUpdatePosition bool
		canDeletePosition bool
		canReadFills      bool
		canInsertFill     bool
		canUpdateFill     bool
		canDeleteFill     bool
	)
	if err := apiPool.QueryRow(context.Background(), `
		SELECT
			pg_has_role(current_user, 'platformgo_api', 'USAGE'),
			has_table_privilege(current_user, 'trading.positions', 'SELECT'),
			has_table_privilege(current_user, 'trading.positions', 'INSERT'),
			has_table_privilege(current_user, 'trading.positions', 'UPDATE'),
			has_table_privilege(current_user, 'trading.positions', 'DELETE'),
			has_table_privilege(current_user, 'trading.fills', 'SELECT'),
			has_table_privilege(current_user, 'trading.fills', 'INSERT'),
			has_table_privilege(current_user, 'trading.fills', 'UPDATE'),
			has_table_privilege(current_user, 'trading.fills', 'DELETE')`,
	).Scan(
		&inheritsAPIRole,
		&canReadPositions,
		&canInsertPosition,
		&canUpdatePosition,
		&canDeletePosition,
		&canReadFills,
		&canInsertFill,
		&canUpdateFill,
		&canDeleteFill,
	); err != nil {
		t.Fatalf("inspect platformgo_api position privileges: %v", err)
	}
	if !inheritsAPIRole ||
		!canReadPositions ||
		canInsertPosition ||
		canUpdatePosition ||
		canDeletePosition ||
		!canReadFills ||
		canInsertFill ||
		canUpdateFill ||
		canDeleteFill {
		t.Fatalf(
			"platformgo_api inherited=%t positions=%t/%t/%t/%t fills=%t/%t/%t/%t, want true true/false/false/false true/false/false/false",
			inheritsAPIRole,
			canReadPositions,
			canInsertPosition,
			canUpdatePosition,
			canDeletePosition,
			canReadFills,
			canInsertFill,
			canUpdateFill,
			canDeleteFill,
		)
	}
}

func assertAdminFleetPositionsAPIMutationsDenied(
	t *testing.T,
	apiPool *pgxpool.Pool,
) {
	t.Helper()
	for _, mutation := range []struct {
		name      string
		statement string
	}{
		{name: "insert position", statement: `INSERT INTO trading.positions DEFAULT VALUES`},
		{name: "update position", statement: `UPDATE trading.positions SET status = status`},
		{name: "delete position", statement: `DELETE FROM trading.positions`},
		{name: "insert fill", statement: `INSERT INTO trading.fills DEFAULT VALUES`},
		{name: "update fill", statement: `UPDATE trading.fills SET side = side`},
		{name: "delete fill", statement: `DELETE FROM trading.fills`},
	} {
		_, err := apiPool.Exec(context.Background(), mutation.statement)
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
			t.Fatalf(
				"%s error = %v, want SQLSTATE 42501",
				mutation.name,
				err,
			)
		}
	}
}

func assertAdminFleetPositionsReaderDependencies(
	t *testing.T,
	adminPool *pgxpool.Pool,
) {
	t.Helper()
	const probeRole = "platformgo_admin_fleet_positions_reader_probe"
	probeID := pgx.Identifier{probeRole}.Sanitize()
	if _, err := adminPool.Exec(context.Background(), fmt.Sprintf(`
		CREATE ROLE %s NOLOGIN;
		GRANT USAGE ON SCHEMA trading TO %s;
		GRANT SELECT ON trading.positions, trading.fills TO %s`,
		probeID,
		probeID,
		probeID,
	)); err != nil {
		t.Fatalf("create admin fleet positions dependency probe: %v", err)
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
		"platformgo_admin_fleet_positions_reader_probe_login",
		probeRole,
	)
	exists, err := platformpostgres.NewAdminFleetPositionsReader(
		probePool,
	).AdminFleetPositionsExist(context.Background())
	if err != nil {
		t.Fatalf("read positions with only position/fill privileges: %v", err)
	}
	if exists {
		t.Fatalf("fresh dependency probe found position state")
	}
}

func seedAdminFleetPositionAuthority(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
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
		VALUES ('urn:xb:account:admin-positions', 'NETTING')`,
	); err != nil {
		t.Fatalf("seed admin fleet position authority: %v", err)
	}
}

type adminFleetPositionsExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func insertAdminFleetPosition(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	insertAdminFleetPositionWithExecutor(t, pool)
}

func insertAdminFleetPositionWithExecutor(
	t *testing.T,
	executor adminFleetPositionsExecutor,
) {
	t.Helper()
	if _, err := executor.Exec(context.Background(), `
		INSERT INTO trading.positions (
			position_id, account_id, instrument_id, side, status,
			signed_quantity, average_open_price, realized_pnl,
			settlement_currency, margin_mode, isolated_collateral, version
		) VALUES (
			'019fa920-0000-4000-8000-000000000001',
			'urn:xb:account:admin-positions',
			'BTC-PERP',
			'LONG',
			'OPEN',
			0.01,
			60000,
			0,
			'USDC',
			'CROSS',
			0,
			1
		)`,
	); err != nil {
		t.Fatalf("insert admin fleet position: %v", err)
	}
}

func insertAdminFleetPositionForFill(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.positions (
			position_id, account_id, instrument_id, side, status,
			signed_quantity, average_open_price, realized_pnl,
			settlement_currency, margin_mode, isolated_collateral, version
		) VALUES (
			'019fa900-0000-4000-8000-000000000004',
			'urn:xb:account:admin-fills-acl',
			'BTC-PERP',
			'LONG',
			'OPEN',
			0.01,
			60000,
			0,
			'USDC',
			'CROSS',
			0,
			1
		)`,
	); err != nil {
		t.Fatalf("insert position matching admin fleet fill: %v", err)
	}
}

func requireAdminFleetPositionsNonEmpty(
	t *testing.T,
	handler *application.AdminFleetPositionsHandler,
	principal edge.Principal,
) {
	t.Helper()
	page, err := handler.Handle(context.Background(), principal)
	var nonEmptyError *application.AdminFleetPositionsNonEmptyStateError
	if !errors.As(err, &nonEmptyError) {
		t.Fatalf("non-empty position state error = %v, want typed error", err)
	}
	if page.Items != nil || page.Total != nil {
		t.Fatalf("non-empty position state returned partial page %#v", page)
	}
}
