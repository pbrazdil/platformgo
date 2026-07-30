package postgres_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
)

const adminPermissionMigration = "20260730000500_phase3_admin_permission_authority.up.sql"

func TestAdminPermissionAuthorizerUsesDurableAllowDenyHierarchy(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate admin permission authority: %v", err)
	}
	seedAdminPermissionPolicies(t, admin)

	api := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_permission_api_login",
		"platformgo_api",
	)
	authorizer := platformpostgres.NewAdminPermissionAuthorizer(api)
	tests := []struct {
		name      string
		principal edge.Principal
		resource  string
		action    string
		want      bool
	}{
		{
			name: "direct allow",
			principal: adminPermissionPrincipal(
				"00000000-0000-4000-8000-000000000001",
			),
			resource: "roles",
			action:   "read",
			want:     true,
		},
		{
			name: "deny overrides direct allow",
			principal: adminPermissionPrincipal(
				"00000000-0000-4000-8000-000000000002",
			),
			resource: "roles",
			action:   "read",
		},
		{
			name: "parent allow",
			principal: adminPermissionPrincipal(
				"00000000-0000-4000-8000-000000000003",
			),
			resource: "roles",
			action:   "read",
			want:     true,
		},
		{
			name: "wildcard allow",
			principal: adminPermissionPrincipal(
				"00000000-0000-4000-8000-000000000004",
			),
			resource: "roles",
			action:   "read",
			want:     true,
		},
		{
			name: "different permission denied",
			principal: adminPermissionPrincipal(
				"00000000-0000-4000-8000-000000000005",
			),
			resource: "roles",
			action:   "read",
		},
		{
			name: "unknown admin denied despite forged scopes",
			principal: edge.Principal{
				Subject: "admin::urn:xb:admin:" +
					"00000000-0000-4000-8000-000000000099",
				Audience: edge.AudienceAdmin,
				Scopes:   []string{"*"},
			},
			resource: "roles",
			action:   "read",
		},
		{
			name: "client audience denied before PostgreSQL authority",
			principal: edge.Principal{
				Subject:  "admin::urn:xb:admin:00000000-0000-4000-8000-000000000001",
				Audience: edge.AudienceClient,
			},
			resource: "roles",
			action:   "read",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := authorizer.AuthorizeAdmin(
				ctx,
				test.principal,
				test.resource,
				test.action,
			)
			if err != nil {
				t.Fatalf("authorize admin: %v", err)
			}
			if got != test.want {
				t.Fatalf(
					"authorize %s/%s = %t, want %t",
					test.resource,
					test.action,
					got,
					test.want,
				)
			}
		})
	}

	if _, err := api.Exec(
		ctx,
		"SELECT role_id FROM identity.rbac_roles",
	); err == nil {
		t.Fatal("platformgo_api read raw RBAC role rows")
	}
	if _, err := api.Exec(
		ctx,
		"INSERT INTO identity.rbac_policies (role_id, resource, action, effect) VALUES (gen_random_uuid(), '*', '*', 'allow')",
	); err == nil {
		t.Fatal("platformgo_api mutated raw RBAC policy rows")
	}
}

func TestAdminPermissionAuthorityMigrationScrubsHostileDefaults(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)

	const hostile = "platformgo_admin_permission_hostile"
	hostileID := pgx.Identifier{hostile}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(
			context.Background(),
			"DROP ROLE IF EXISTS "+hostileID,
		)
	})
	if _, err := admin.Exec(
		ctx,
		"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO "+hostileID,
	); err != nil {
		t.Fatalf("install hostile table defaults: %v", err)
	}
	if _, err := admin.Exec(
		ctx,
		"ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO "+hostileID,
	); err != nil {
		t.Fatalf("install hostile function defaults: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(
			context.Background(),
			"ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM "+hostileID,
		)
		_, _ = admin.Exec(
			context.Background(),
			"ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM "+hostileID,
		)
	})

	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("upgrade admin permission authority: %v", err)
	}
	for _, relation := range []string{
		"identity.rbac_roles",
		"identity.rbac_role_parents",
		"identity.rbac_admin_roles",
		"identity.rbac_policies",
	} {
		var (
			apiCanRead     bool
			hostileCanRead bool
		)
		if err := admin.QueryRow(ctx, `
			SELECT
				has_table_privilege('platformgo_api', $1, 'SELECT'),
				has_table_privilege($2, $1, 'SELECT')`,
			relation,
			hostile,
		).Scan(&apiCanRead, &hostileCanRead); err != nil {
			t.Fatalf("inspect %s ACL: %v", relation, err)
		}
		if apiCanRead || hostileCanRead {
			t.Fatalf(
				"%s raw SELECT API/hostile = %t/%t, want false/false",
				relation,
				apiCanRead,
				hostileCanRead,
			)
		}
	}

	const function = "identity.admin_has_permission(text,text,text)"
	var (
		apiCanExecute     bool
		publicCanExecute  bool
		hostileCanExecute bool
		securityDefiner   bool
		searchPath        []string
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			has_function_privilege('platformgo_api', $1, 'EXECUTE'),
			has_function_privilege('public', $1, 'EXECUTE'),
			has_function_privilege($2, $1, 'EXECUTE'),
			procedure.prosecdef,
			COALESCE(procedure.proconfig, ARRAY[]::text[])
		  FROM pg_catalog.pg_proc AS procedure
		 WHERE procedure.oid = $1::pg_catalog.regprocedure`,
		function,
		hostile,
	).Scan(
		&apiCanExecute,
		&publicCanExecute,
		&hostileCanExecute,
		&securityDefiner,
		&searchPath,
	); err != nil {
		t.Fatalf("inspect admin permission function: %v", err)
	}
	if !apiCanExecute || publicCanExecute || hostileCanExecute ||
		!securityDefiner ||
		fmt.Sprint(searchPath) != "[search_path=pg_catalog]" {
		t.Fatalf(
			"function ACL/config API/public/hostile/definer/path = %t/%t/%t/%t/%v",
			apiCanExecute,
			publicCanExecute,
			hostileCanExecute,
			securityDefiner,
			searchPath,
		)
	}

	var (
		count int
		tip   string
	)
	if err := admin.QueryRow(ctx, `
		SELECT count(*), max(filename)
		  FROM engine.schema_migrations`,
	).Scan(&count, &tip); err != nil {
		t.Fatalf("read migration history: %v", err)
	}
	if count != 41 || tip != adminPermissionMigration {
		t.Fatalf(
			"migration history = %d/%q, want 41/%q",
			count,
			tip,
			adminPermissionMigration,
		)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration rerun: %v", err)
	}
}

func seedAdminPermissionPolicies(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.rbac_roles (role_id, name) VALUES
			('00000000-0000-4000-8000-000000000101', 'roles-reader'),
			('00000000-0000-4000-8000-000000000102', 'roles-denied'),
			('00000000-0000-4000-8000-000000000103', 'child'),
			('00000000-0000-4000-8000-000000000104', 'parent'),
			('00000000-0000-4000-8000-000000000105', 'super'),
			('00000000-0000-4000-8000-000000000106', 'diagnostics-reader');

		INSERT INTO identity.rbac_role_parents (role_id, parent_id) VALUES
			('00000000-0000-4000-8000-000000000103',
			 '00000000-0000-4000-8000-000000000104');

		INSERT INTO identity.rbac_policies (
			role_id, resource, action, effect
		) VALUES
			('00000000-0000-4000-8000-000000000101',
			 'roles', 'read', 'allow'),
			('00000000-0000-4000-8000-000000000102',
			 'roles', 'read', 'deny'),
			('00000000-0000-4000-8000-000000000104',
			 'roles', 'read', 'allow'),
			('00000000-0000-4000-8000-000000000105',
			 '*', '*', 'allow'),
			('00000000-0000-4000-8000-000000000106',
			 'diagnostics', 'read', 'allow');

		INSERT INTO identity.rbac_admin_roles (admin_subject, role_id) VALUES
			('admin::urn:xb:admin:00000000-0000-4000-8000-000000000001',
			 '00000000-0000-4000-8000-000000000101'),
			('admin::urn:xb:admin:00000000-0000-4000-8000-000000000002',
			 '00000000-0000-4000-8000-000000000101'),
			('admin::urn:xb:admin:00000000-0000-4000-8000-000000000002',
			 '00000000-0000-4000-8000-000000000102'),
			('admin::urn:xb:admin:00000000-0000-4000-8000-000000000003',
			 '00000000-0000-4000-8000-000000000103'),
			('admin::urn:xb:admin:00000000-0000-4000-8000-000000000004',
			 '00000000-0000-4000-8000-000000000105'),
			('admin::urn:xb:admin:00000000-0000-4000-8000-000000000005',
			 '00000000-0000-4000-8000-000000000106')`,
	); err != nil {
		t.Fatalf("seed admin permission policies: %v", err)
	}
}

func adminPermissionPrincipal(id string) edge.Principal {
	return edge.Principal{
		Subject:  "admin::urn:xb:admin:" + id,
		Audience: edge.AudienceAdmin,
	}
}
