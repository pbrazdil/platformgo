package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
)

type countedAdminFleetFillsReader struct {
	reader application.AdminFleetFillsExistenceReader
	calls  int
}

func (reader *countedAdminFleetFillsReader) AdminFleetFillsExist(
	ctx context.Context,
) (bool, error) {
	reader.calls++
	return reader.reader.AdminFleetFillsExist(ctx)
}

// TestAdminFleetFillsBlotterReadsAndIsGated ports the source test's fresh
// database read and authorization denial through the real least-privilege
// runtime role and production application/PostgreSQL boundary.
//
// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_admin_fleet_blotter.rs:39
//	test: admin_fleet_fills_blotter_reads_and_is_gated
//
// Adaptations:
//   - The source composition's migrated database is replaced by the native
//     migrator and a real login inheriting platformgo_api.
//   - The source dispatcher is represented by the production application
//     handler over the production PostgreSQL existence reader.
//
// Assertions preserved:
//   - A fresh database returns an empty fleet fills page.
//   - The first page carries a present total equal to exactly zero.
//
// Strengthening:
//   - The successful empty items slice is non-nil.
//   - The real platformgo_api boundary can SELECT immutable fills but cannot
//     INSERT them.
//   - Client denial occurs before the PostgreSQL reader is invoked.
//   - A committed fill fails closed without exposing a partial page, including
//     after reconstructing the stateless handler and reader.
func TestAdminFleetFillsBlotterReadsAndIsGated(t *testing.T) {
	ctx := context.Background()
	adminPool := postgresPool(t)
	resetDurableSchemas(t, adminPool)
	if err := platformpostgres.NewMigrator(
		adminPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate admin fleet fills database: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_admin_fleet_fills_api_login",
		"platformgo_api",
	)
	var (
		inheritsAPIRole bool
		canReadFills    bool
		canInsertFills  bool
	)
	if err := apiPool.QueryRow(ctx, `
		SELECT
			pg_has_role(current_user, 'platformgo_api', 'USAGE'),
			has_table_privilege(current_user, 'trading.fills', 'SELECT'),
			has_table_privilege(current_user, 'trading.fills', 'INSERT')`,
	).Scan(
		&inheritsAPIRole,
		&canReadFills,
		&canInsertFills,
	); err != nil {
		t.Fatalf("inspect platformgo_api fill privileges: %v", err)
	}
	if !inheritsAPIRole || !canReadFills || canInsertFills {
		t.Fatalf(
			"platformgo_api boundary inherited=%t select=%t insert=%t, want true/true/false",
			inheritsAPIRole,
			canReadFills,
			canInsertFills,
		)
	}

	_, insertErr := apiPool.Exec(ctx, `INSERT INTO trading.fills DEFAULT VALUES`)
	var postgresError *pgconn.PgError
	if !errors.As(insertErr, &postgresError) || postgresError.Code != "42501" {
		t.Fatalf("platformgo_api fill insert error = %v, want SQLSTATE 42501", insertErr)
	}

	reader := platformpostgres.NewAdminFleetFillsReader(apiPool)
	counted := &countedAdminFleetFillsReader{reader: reader}
	handler := application.NewAdminFleetFillsHandler(counted)
	adminPrincipal := edge.Principal{
		Subject:  "admin-system",
		Audience: edge.AudienceAdmin,
	}
	page, err := handler.Handle(ctx, adminPrincipal)
	if err != nil {
		t.Fatalf("read fresh fleet fills through platformgo_api: %v", err)
	}
	if page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("fresh fleet fills items = %#v, want non-nil empty", page.Items)
	}
	if page.Total == nil || *page.Total != 0 {
		t.Fatalf("fresh fleet fills total = %v, want present exact 0", page.Total)
	}
	if counted.calls != 1 {
		t.Fatalf("fresh fleet fills reader calls = %d, want 1", counted.calls)
	}

	forbiddenPage, err := handler.Handle(ctx, edge.Principal{
		Subject:  "client-1",
		Audience: edge.AudienceClient,
		Scopes:   []string{"*"},
		Accounts: []string{"*"},
	})
	if !errors.Is(err, edge.ErrForbidden) {
		t.Fatalf("client fleet fills error = %v, want forbidden", err)
	}
	if counted.calls != 1 {
		t.Fatalf("forbidden request reader calls = %d, want unchanged 1", counted.calls)
	}
	if forbiddenPage.Items != nil || forbiddenPage.Total != nil {
		t.Fatalf("forbidden request returned partial page %#v", forbiddenPage)
	}

	seedAdminFleetFill(t, adminPool)
	nonEmptyPage, err := handler.Handle(ctx, adminPrincipal)
	var nonEmptyError *application.AdminFleetFillsNonEmptyStateError
	if !errors.As(err, &nonEmptyError) {
		t.Fatalf("committed fill error = %v, want typed unsupported state", err)
	}
	if nonEmptyPage.Items != nil || nonEmptyPage.Total != nil {
		t.Fatalf("committed fill returned partial page %#v", nonEmptyPage)
	}

	restartedHandler := application.NewAdminFleetFillsHandler(
		platformpostgres.NewAdminFleetFillsReader(apiPool),
	)
	restartedPage, err := restartedHandler.Handle(ctx, adminPrincipal)
	if !errors.As(err, &nonEmptyError) {
		t.Fatalf("restarted committed fill error = %v, want typed unsupported state", err)
	}
	if restartedPage.Items != nil || restartedPage.Total != nil {
		t.Fatalf("restarted committed fill returned partial page %#v", restartedPage)
	}
}
