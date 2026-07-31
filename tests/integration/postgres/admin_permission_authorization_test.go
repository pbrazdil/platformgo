package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
)

const (
	adminPermissionPreviousMigration = "20260730000400_phase3_broker_funding_acl.up.sql"
	adminPermissionMigration         = "20260730000500_phase3_admin_permission_authority.up.sql"
)

func TestAdminPermissionAuthorizerUsesDurableAllowDenyHierarchy(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := newCurrentTestMigrator(
		t,
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
			name: "cyclic hierarchy terminates with inherited allow",
			principal: adminPermissionPrincipal(
				"00000000-0000-4000-8000-000000000006",
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

type adminPermissionClock struct {
	now time.Time
}

func (clock adminPermissionClock) Now() time.Time {
	return clock.now
}

func TestAdminPermissionEdgeComposesExactCatalogWithPostgreSQLAuthority(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := newCurrentTestMigrator(
		t,
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate admin permission composition: %v", err)
	}
	seedAdminPermissionPolicies(t, admin)
	api := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_permission_composition_login",
		"platformgo_api",
	)
	now := time.Date(2026, time.July, 30, 22, 30, 0, 0, time.UTC)
	auth, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		AdminTokenSecret:  []byte("admin-secret-0123456789abcdef012345"),
		Clock:             adminPermissionClock{now: now},
	})
	if err != nil {
		t.Fatalf("construct admin authenticator: %v", err)
	}
	token, err := auth.SignAdminToken(edge.AdminClaims{
		Subject:  "urn:xb:admin:00000000-0000-4000-8000-000000000001",
		Audience: "admin",
		Expires:  now.Add(time.Minute).Unix(),
		Roles:    []string{"untrusted-token-superuser"},
	})
	if err != nil {
		t.Fatalf("sign admin token: %v", err)
	}
	handler := edge.NewServer(edge.ServerConfig{
		AdminAuthenticator: auth,
		AdminPermissionAuthorizer: platformpostgres.NewAdminPermissionAuthorizer(
			api,
		),
		AdminPermissionCatalog: application.AdminPermissionCatalogHandler{},
		RequestID:              func() string { return "admin-permission-request" },
	}).Handler()
	request := httptest.NewRequest(
		http.MethodGet,
		"/admin/v1/permissions",
		nil,
	)
	request.Header.Set("authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"permission composition status/body = %d/%s",
			response.Code,
			response.Body.String(),
		)
	}

	var wire map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatalf("decode permission catalog response: %v", err)
	}
	if len(wire) != 2 || wire["resources"] == nil || wire["actions"] == nil {
		t.Fatalf("permission catalog fields = %v", wire)
	}
	for field, raw := range wire {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatalf("decode %s: %v", field, err)
		}
		for index, item := range items {
			if len(item) != 2 || item["id"] == nil || item["label"] == nil {
				t.Fatalf("%s item %d fields = %v", field, index, item)
			}
		}
	}
	var got edge.AdminPermissionCatalog
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode typed permission catalog: %v", err)
	}
	want, err := (application.AdminPermissionCatalogHandler{}).Handle(
		ctx,
		edge.Principal{
			Subject:  "admin::urn:xb:admin:00000000-0000-4000-8000-000000000001",
			Audience: edge.AudienceAdmin,
		},
	)
	if err != nil {
		t.Fatalf("read accepted catalog: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("permission catalog = %#v, want %#v", got, want)
	}
}

func TestAdminPermissionAuthorityMigrationScrubsHostileDefaults(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionPreviousMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id,
			login,
			normalized_login,
			email,
			normalized_email
		) VALUES (
			'urn:xb:user:admin-permission-preserved',
			'permission-preserved',
			'permission-preserved',
			'permission-preserved@example.com',
			'permission-preserved@example.com'
		)`); err != nil {
		t.Fatalf("seed preserved identity row: %v", err)
	}
	var beforeIdentity string
	if err := admin.QueryRow(ctx, `
		SELECT pg_catalog.md5(
			pg_catalog.concat_ws(
				'|',
				user_id,
				login,
				normalized_login,
				COALESCE(email, ''),
				COALESCE(normalized_email, '')
			)
		)
		  FROM identity.users
		 WHERE user_id = 'urn:xb:user:admin-permission-preserved'`,
	).Scan(&beforeIdentity); err != nil {
		t.Fatalf("read preserved identity digest: %v", err)
	}
	var beforeCount int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("read current-main migration count: %v", err)
	}
	if beforeCount != 40 {
		t.Fatalf("current-main migration count = %d, want 40", beforeCount)
	}

	const hostile = "platformgo_admin_permission_hostile"
	const dependent = "platformgo_admin_permission_dependent"
	hostileID := pgx.Identifier{hostile}.Sanitize()
	dependentID := pgx.Identifier{dependent}.Sanitize()
	_, _ = admin.Exec(ctx, "DROP OWNED BY "+hostileID)
	_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+hostileID)
	_, _ = admin.Exec(ctx, "DROP OWNED BY "+dependentID)
	_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+dependentID)
	if _, err := admin.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile role: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE ROLE "+dependentID+" NOLOGIN"); err != nil {
		t.Fatalf("create dependent role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(
			context.Background(),
			"DROP EVENT TRIGGER IF EXISTS "+
				"platformgo_admin_permission_acl_injector",
		)
		_, _ = admin.Exec(
			context.Background(),
			"DROP FUNCTION IF EXISTS "+
				"public.platformgo_admin_permission_acl_injector()",
		)
		_, _ = admin.Exec(
			context.Background(),
			"DROP TABLE IF EXISTS "+
				"public.platformgo_admin_permission_acl_injections",
		)
		_, _ = admin.Exec(
			context.Background(),
			"DROP OWNED BY "+hostileID,
		)
		_, _ = admin.Exec(
			context.Background(),
			"DROP ROLE IF EXISTS "+hostileID,
		)
		_, _ = admin.Exec(
			context.Background(),
			"DROP OWNED BY "+dependentID,
		)
		_, _ = admin.Exec(
			context.Background(),
			"DROP ROLE IF EXISTS "+dependentID,
		)
	})
	if _, err := admin.Exec(
		ctx,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA identity "+
			"GRANT SELECT ON TABLES TO "+
			hostileID+" WITH GRANT OPTION",
	); err != nil {
		t.Fatalf("install hostile table defaults: %v", err)
	}
	if _, err := admin.Exec(
		ctx,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA identity "+
			"GRANT EXECUTE ON FUNCTIONS TO "+
			hostileID+" WITH GRANT OPTION",
	); err != nil {
		t.Fatalf("install hostile function defaults: %v", err)
	}
	if _, err := admin.Exec(
		ctx,
		"GRANT USAGE ON SCHEMA identity TO "+hostileID,
	); err != nil {
		t.Fatalf("grant hostile identity schema usage: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE public.platformgo_admin_permission_acl_injections (
			object_identity text PRIMARY KEY
		);
		CREATE FUNCTION public.platformgo_admin_permission_acl_injector()
		RETURNS event_trigger
		LANGUAGE plpgsql
		AS $trigger$
		DECLARE
			command record;
			column_name text;
		BEGIN
			FOR command IN
				SELECT *
				  FROM pg_catalog.pg_event_trigger_ddl_commands()
			LOOP
				IF command.schema_name = 'identity'
				   AND command.object_type = 'table'
				   AND command.object_identity = ANY (ARRAY[
					'identity.rbac_roles',
					'identity.rbac_role_parents',
					'identity.rbac_admin_roles',
					'identity.rbac_policies'
				   ])
				THEN
					SELECT attribute.attname
					  INTO STRICT column_name
					  FROM pg_catalog.pg_attribute AS attribute
					 WHERE attribute.attrelid = command.objid
					   AND attribute.attnum > 0
					   AND NOT attribute.attisdropped
					 ORDER BY attribute.attnum
					 LIMIT 1;
					EXECUTE
						'GRANT SELECT ON TABLE ' ||
						command.objid::pg_catalog.regclass ||
						' TO %[1]s WITH GRANT OPTION';
					EXECUTE
						'GRANT UPDATE (' ||
						pg_catalog.quote_ident(column_name) ||
						') ON TABLE ' ||
						command.objid::pg_catalog.regclass ||
						' TO PUBLIC';
					INSERT INTO
						public.platformgo_admin_permission_acl_injections (
							object_identity
						)
					VALUES (command.object_identity)
					ON CONFLICT (object_identity) DO NOTHING;
					EXECUTE 'SET LOCAL ROLE %[1]s';
					EXECUTE
						'GRANT SELECT ON TABLE ' ||
						command.objid::pg_catalog.regclass ||
						' TO %[2]s';
					EXECUTE 'RESET ROLE';
				ELSIF command.schema_name = 'identity'
				   AND command.object_type = 'function'
				   AND command.objid = pg_catalog.to_regprocedure(
					'identity.admin_has_permission(text,text,text)'
				   )
				THEN
					EXECUTE
						'GRANT EXECUTE ON FUNCTION ' ||
						command.objid::pg_catalog.regprocedure ||
						' TO %[1]s WITH GRANT OPTION';
					INSERT INTO
						public.platformgo_admin_permission_acl_injections (
							object_identity
						)
					VALUES (command.object_identity)
					ON CONFLICT (object_identity) DO NOTHING;
					EXECUTE 'SET LOCAL ROLE %[1]s';
					EXECUTE
						'GRANT EXECUTE ON FUNCTION ' ||
						command.objid::pg_catalog.regprocedure ||
						' TO %[2]s';
					EXECUTE 'RESET ROLE';
				END IF;
			END LOOP;
		END
		$trigger$;
		CREATE EVENT TRIGGER platformgo_admin_permission_acl_injector
		ON ddl_command_end
		WHEN TAG IN ('CREATE TABLE', 'CREATE FUNCTION')
		EXECUTE FUNCTION
			public.platformgo_admin_permission_acl_injector()`,
		hostileID,
		dependentID,
	)); err != nil {
		t.Fatalf("install hostile ACL event trigger: %v", err)
	}
	beforeDefaults := adminPermissionDefaultACLs(t, admin)
	t.Cleanup(func() {
		_, _ = admin.Exec(
			context.Background(),
			"ALTER DEFAULT PRIVILEGES IN SCHEMA identity "+
				"REVOKE SELECT ON TABLES FROM "+hostileID,
		)
		_, _ = admin.Exec(
			context.Background(),
			"ALTER DEFAULT PRIVILEGES IN SCHEMA identity "+
				"REVOKE EXECUTE ON FUNCTIONS FROM "+hostileID,
		)
	})

	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("upgrade admin permission authority: %v", err)
	}
	var injectedACLObjects int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM public.platformgo_admin_permission_acl_injections`,
	).Scan(&injectedACLObjects); err != nil {
		t.Fatalf("read hostile ACL injection evidence: %v", err)
	}
	if injectedACLObjects != 5 {
		t.Fatalf("hostile ACL injection count = %d, want 5", injectedACLObjects)
	}
	for _, relation := range []string{
		"identity.rbac_roles",
		"identity.rbac_role_parents",
		"identity.rbac_admin_roles",
		"identity.rbac_policies",
	} {
		var (
			apiCanRead       bool
			hostileCanRead   bool
			dependentCanRead bool
		)
		if err := admin.QueryRow(ctx, `
			SELECT
				has_table_privilege('platformgo_api', $1, 'SELECT'),
				has_table_privilege($2, $1, 'SELECT'),
				has_table_privilege($3, $1, 'SELECT')`,
			relation,
			hostile,
			dependent,
		).Scan(
			&apiCanRead,
			&hostileCanRead,
			&dependentCanRead,
		); err != nil {
			t.Fatalf("inspect %s ACL: %v", relation, err)
		}
		if apiCanRead || hostileCanRead || dependentCanRead {
			t.Fatalf(
				"%s raw SELECT API/hostile/dependent = %t/%t/%t",
				relation,
				apiCanRead,
				hostileCanRead,
				dependentCanRead,
			)
		}
		var (
			unexpectedTableACLs  int
			unexpectedColumnACLs int
		)
		if err := admin.QueryRow(ctx, `
			SELECT count(*)
			  FROM pg_catalog.pg_class AS relation
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				COALESCE(
					relation.relacl,
					pg_catalog.acldefault('r', relation.relowner)
				)
			  ) AS privilege
			 WHERE relation.oid = $1::pg_catalog.regclass
			   AND privilege.grantee <> relation.relowner`,
			relation,
		).Scan(&unexpectedTableACLs); err != nil {
			t.Fatalf("inspect raw %s ACL: %v", relation, err)
		}
		if err := admin.QueryRow(ctx, `
			SELECT count(*)
			  FROM pg_catalog.pg_attribute AS attribute
			  JOIN pg_catalog.pg_class AS relation
			    ON relation.oid = attribute.attrelid
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				attribute.attacl
			  ) AS privilege
			 WHERE attribute.attrelid = $1::pg_catalog.regclass
			   AND attribute.attnum > 0
			   AND NOT attribute.attisdropped
			   AND attribute.attacl IS NOT NULL
			   AND privilege.grantee <> relation.relowner`,
			relation,
		).Scan(&unexpectedColumnACLs); err != nil {
			t.Fatalf("inspect raw %s column ACL: %v", relation, err)
		}
		if unexpectedTableACLs != 0 || unexpectedColumnACLs != 0 {
			t.Fatalf(
				"%s unexpected table/column ACL rows = %d/%d",
				relation,
				unexpectedTableACLs,
				unexpectedColumnACLs,
			)
		}
	}

	const function = "identity.admin_has_permission(text,text,text)"
	var (
		apiCanExecute       bool
		publicCanExecute    bool
		hostileCanExecute   bool
		dependentCanExecute bool
		securityDefiner     bool
		searchPath          []string
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			has_function_privilege('platformgo_api', $1, 'EXECUTE'),
			has_function_privilege('public', $1, 'EXECUTE'),
			has_function_privilege($2, $1, 'EXECUTE'),
			has_function_privilege($3, $1, 'EXECUTE'),
			procedure.prosecdef,
			COALESCE(procedure.proconfig, ARRAY[]::text[])
		  FROM pg_catalog.pg_proc AS procedure
		 WHERE procedure.oid = $1::pg_catalog.regprocedure`,
		function,
		hostile,
		dependent,
	).Scan(
		&apiCanExecute,
		&publicCanExecute,
		&hostileCanExecute,
		&dependentCanExecute,
		&securityDefiner,
		&searchPath,
	); err != nil {
		t.Fatalf("inspect admin permission function: %v", err)
	}
	if !apiCanExecute || publicCanExecute || hostileCanExecute ||
		dependentCanExecute ||
		!securityDefiner ||
		fmt.Sprint(searchPath) != "[search_path=pg_catalog]" {
		t.Fatalf(
			"function ACL/config API/public/hostile/dependent/definer/path = %t/%t/%t/%t/%t/%v",
			apiCanExecute,
			publicCanExecute,
			hostileCanExecute,
			dependentCanExecute,
			securityDefiner,
			searchPath,
		)
	}
	var exactFunctionACL bool
	if err := admin.QueryRow(ctx, `
		SELECT
			count(*) = 1
			AND bool_and(role.rolname = 'platformgo_api')
			AND bool_and(privilege.privilege_type = 'EXECUTE')
			AND bool_and(NOT privilege.is_grantable)
		  FROM pg_catalog.pg_proc AS procedure
		  CROSS JOIN LATERAL pg_catalog.aclexplode(
			COALESCE(
				procedure.proacl,
				pg_catalog.acldefault('f', procedure.proowner)
			)
		  ) AS privilege
		  JOIN pg_catalog.pg_roles AS role
		    ON role.oid = privilege.grantee
		 WHERE procedure.oid = $1::pg_catalog.regprocedure
		   AND privilege.grantee <> procedure.proowner`,
		function,
	).Scan(&exactFunctionACL); err != nil {
		t.Fatalf("inspect raw authorization function ACL: %v", err)
	}
	if !exactFunctionACL {
		t.Fatal("authorization function raw ACL is not API execute-only")
	}
	afterDefaults := adminPermissionDefaultACLs(t, admin)
	if afterDefaults != beforeDefaults {
		t.Fatalf(
			"owner default ACLs changed from %q to %q",
			beforeDefaults,
			afterDefaults,
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
		migrationFilesThrough(t, adminPermissionPreviousMigration),
	).VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous artifact verification error = %v, want ahead", err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration rerun: %v", err)
	}
	var afterIdentity string
	if err := admin.QueryRow(ctx, `
		SELECT pg_catalog.md5(
			pg_catalog.concat_ws(
				'|',
				user_id,
				login,
				normalized_login,
				COALESCE(email, ''),
				COALESCE(normalized_email, '')
			)
		)
		  FROM identity.users
		 WHERE user_id = 'urn:xb:user:admin-permission-preserved'`,
	).Scan(&afterIdentity); err != nil {
		t.Fatalf("read upgraded identity digest: %v", err)
	}
	if afterIdentity != beforeIdentity {
		t.Fatalf(
			"identity digest changed from %q to %q",
			beforeIdentity,
			afterIdentity,
		)
	}
}

func TestAdminPermissionAuthorityMigrationRefusesDivergentObjectAtomically(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionPreviousMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE TABLE identity.rbac_policies (
			policy_id text PRIMARY KEY,
			foreign_shape boolean NOT NULL
		);
		INSERT INTO identity.rbac_policies (policy_id, foreign_shape)
		VALUES ('preserve-me', true)`); err != nil {
		t.Fatalf("install divergent object: %v", err)
	}

	err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx)
	if err == nil {
		t.Fatal("migration accepted divergent preexisting RBAC table")
	}

	var (
		historyCount int
		rowCount     int
		foreignShape bool
		addedObjects int
	)
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&historyCount); err != nil {
		t.Fatalf("read migration history after refusal: %v", err)
	}
	if err := admin.QueryRow(ctx, `
		SELECT count(*), bool_and(foreign_shape)
		  FROM identity.rbac_policies`,
	).Scan(&rowCount, &foreignShape); err != nil {
		t.Fatalf("read divergent object after refusal: %v", err)
	}
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM pg_catalog.pg_class
		 WHERE oid IN (
			to_regclass('identity.rbac_role_parents'),
			to_regclass('identity.rbac_admin_roles'),
			to_regclass('identity.rbac_roles')
		 )`,
	).Scan(&addedObjects); err != nil {
		t.Fatalf("inspect rolled-back RBAC objects: %v", err)
	}
	if historyCount != 40 || rowCount != 1 || !foreignShape ||
		addedObjects != 0 {
		t.Fatalf(
			"refusal state history/rows/shape/added = %d/%d/%t/%d",
			historyCount,
			rowCount,
			foreignShape,
			addedObjects,
		)
	}
	var functionExists bool
	if err := admin.QueryRow(ctx, `
		SELECT to_regprocedure(
			'identity.admin_has_permission(text,text,text)'
		) IS NOT NULL`,
	).Scan(&functionExists); err != nil {
		t.Fatalf("inspect rolled-back authorization function: %v", err)
	}
	if functionExists {
		t.Fatal("authorization function survived refused migration")
	}
}

func adminPermissionDefaultACLs(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(
			pg_catalog.string_agg(
				default_acl.defaclobjtype::text || ':' ||
					default_acl.defaclacl::text,
				'|' ORDER BY default_acl.defaclobjtype
			),
			''
		)
		  FROM pg_catalog.pg_default_acl AS default_acl
		 WHERE default_acl.defaclrole = (
			SELECT role.oid
			  FROM pg_catalog.pg_roles AS role
			 WHERE role.rolname = current_user
		 )
		   AND default_acl.defaclnamespace =
				'identity'::pg_catalog.regnamespace`,
	).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot identity default ACLs: %v", err)
	}
	return snapshot
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
			('00000000-0000-4000-8000-000000000106', 'diagnostics-reader'),
			('00000000-0000-4000-8000-000000000107', 'cycle-a'),
			('00000000-0000-4000-8000-000000000108', 'cycle-b');

		INSERT INTO identity.rbac_role_parents (role_id, parent_id) VALUES
			('00000000-0000-4000-8000-000000000103',
			 '00000000-0000-4000-8000-000000000104'),
			('00000000-0000-4000-8000-000000000107',
			 '00000000-0000-4000-8000-000000000108'),
			('00000000-0000-4000-8000-000000000108',
			 '00000000-0000-4000-8000-000000000107');

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
			 'diagnostics', 'read', 'allow'),
			('00000000-0000-4000-8000-000000000108',
			 'roles', 'read', 'allow');

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
			 '00000000-0000-4000-8000-000000000106'),
			('admin::urn:xb:admin:00000000-0000-4000-8000-000000000006',
			 '00000000-0000-4000-8000-000000000107')`,
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
