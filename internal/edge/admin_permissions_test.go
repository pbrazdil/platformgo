package edge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type testAdminAuthenticator struct {
	principal Principal
	err       error
	calls     int
}

func (auth *testAdminAuthenticator) AuthenticateAdmin(
	_ context.Context,
	token string,
) (Principal, error) {
	auth.calls++
	if token != "admin-token" {
		return Principal{}, ErrUnauthorized
	}
	return auth.principal, auth.err
}

type testAdminPermissionAuthorizer struct {
	allowed    bool
	err        error
	calls      int
	principals []Principal
	resources  []string
	actions    []string
}

func (authorizer *testAdminPermissionAuthorizer) AuthorizeAdmin(
	_ context.Context,
	principal Principal,
	resource string,
	action string,
) (bool, error) {
	authorizer.calls++
	authorizer.principals = append(authorizer.principals, principal)
	authorizer.resources = append(authorizer.resources, resource)
	authorizer.actions = append(authorizer.actions, action)
	return authorizer.allowed, authorizer.err
}

type testAdminPermissionCatalog struct {
	catalog AdminPermissionCatalog
	err     error
	calls   int
}

func (catalog *testAdminPermissionCatalog) AdminPermissionCatalog(
	_ context.Context,
	_ Principal,
) (AdminPermissionCatalog, error) {
	catalog.calls++
	return catalog.catalog, catalog.err
}

func TestAdminPermissionCatalogAuthenticatesAndAuthorizesBeforeReading(t *testing.T) {
	want := AdminPermissionCatalog{
		Resources: []PermissionCatalogItem{
			{ID: "roles", Label: "Roles & Permissions"},
		},
		Actions: []PermissionCatalogItem{
			{ID: "read", Label: "Read"},
		},
	}
	auth := &testAdminAuthenticator{principal: Principal{
		Subject:  "admin::urn:xb:admin:00000000-0000-4000-8000-000000000001",
		Audience: AudienceAdmin,
	}}
	authorizer := &testAdminPermissionAuthorizer{allowed: true}
	catalog := &testAdminPermissionCatalog{catalog: want}
	handler := NewServer(ServerConfig{
		AdminAuthenticator:        auth,
		AdminPermissionAuthorizer: authorizer,
		AdminPermissionCatalog:    catalog,
	}).Handler()

	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/admin/v1/permissions",
		nil,
		map[string]string{"authorization": "Bearer admin-token"},
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"permission catalog status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode permission catalog: %v", err)
	}
	if len(got) != 2 || got["resources"] == nil || got["actions"] == nil {
		t.Fatalf("permission catalog top-level fields = %v", got)
	}
	var gotResources []PermissionCatalogItem
	if err := json.Unmarshal(got["resources"], &gotResources); err != nil {
		t.Fatalf("decode permission resources: %v", err)
	}
	var gotActions []PermissionCatalogItem
	if err := json.Unmarshal(got["actions"], &gotActions); err != nil {
		t.Fatalf("decode permission actions: %v", err)
	}
	if !reflect.DeepEqual(gotResources, want.Resources) ||
		!reflect.DeepEqual(gotActions, want.Actions) {
		t.Fatalf(
			"permission catalog = resources %#v actions %#v, want %#v",
			gotResources,
			gotActions,
			want,
		)
	}
	if auth.calls != 1 || authorizer.calls != 1 || catalog.calls != 1 {
		t.Fatalf(
			"calls auth/authorizer/catalog = %d/%d/%d, want 1/1/1",
			auth.calls,
			authorizer.calls,
			catalog.calls,
		)
	}
	if authorizer.resources[0] != "roles" ||
		authorizer.actions[0] != "read" {
		t.Fatalf(
			"authorization = %s/%s, want roles/read",
			authorizer.resources[0],
			authorizer.actions[0],
		)
	}
}

func TestAdminPermissionCatalogFailsClosedBeforeReturningData(t *testing.T) {
	admin := Principal{
		Subject:  "admin::urn:xb:admin:00000000-0000-4000-8000-000000000001",
		Audience: AudienceAdmin,
	}
	tests := []struct {
		name             string
		token            string
		authPrincipal    Principal
		authErr          error
		allowed          bool
		authorizeErr     error
		catalogErr       error
		wantStatus       int
		wantCode         string
		wantAuthCalls    int
		wantPolicyCalls  int
		wantCatalogCalls int
	}{
		{
			name:          "missing bearer",
			authPrincipal: admin,
			allowed:       true,
			wantStatus:    http.StatusUnauthorized,
			wantCode:      "unauthorized",
		},
		{
			name:          "invalid bearer",
			token:         "invalid",
			authPrincipal: admin,
			allowed:       true,
			wantStatus:    http.StatusUnauthorized,
			wantCode:      "unauthorized",
			wantAuthCalls: 1,
		},
		{
			name:          "wrong returned audience",
			token:         "admin-token",
			authPrincipal: Principal{Subject: admin.Subject, Audience: AudienceClient},
			allowed:       true,
			wantStatus:    http.StatusUnauthorized,
			wantCode:      "unauthorized",
			wantAuthCalls: 1,
		},
		{
			name:            "missing roles read",
			token:           "admin-token",
			authPrincipal:   admin,
			wantStatus:      http.StatusForbidden,
			wantCode:        "forbidden",
			wantAuthCalls:   1,
			wantPolicyCalls: 1,
		},
		{
			name:            "authorization unavailable",
			token:           "admin-token",
			authPrincipal:   admin,
			authorizeErr:    errors.New("database unavailable"),
			wantStatus:      http.StatusServiceUnavailable,
			wantCode:        "unavailable",
			wantAuthCalls:   1,
			wantPolicyCalls: 1,
		},
		{
			name:             "catalog unavailable",
			token:            "admin-token",
			authPrincipal:    admin,
			allowed:          true,
			catalogErr:       errors.New("catalog unavailable"),
			wantStatus:       http.StatusServiceUnavailable,
			wantCode:         "unavailable",
			wantAuthCalls:    1,
			wantPolicyCalls:  1,
			wantCatalogCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			auth := &testAdminAuthenticator{
				principal: test.authPrincipal,
				err:       test.authErr,
			}
			authorizer := &testAdminPermissionAuthorizer{
				allowed: test.allowed,
				err:     test.authorizeErr,
			}
			catalog := &testAdminPermissionCatalog{
				catalog: AdminPermissionCatalog{
					Resources: []PermissionCatalogItem{{
						ID: "secret", Label: "must not escape",
					}},
				},
				err: test.catalogErr,
			}
			handler := NewServer(ServerConfig{
				AdminAuthenticator:        auth,
				AdminPermissionAuthorizer: authorizer,
				AdminPermissionCatalog:    catalog,
			}).Handler()
			headers := map[string]string{}
			if test.token != "" {
				headers["authorization"] = "Bearer " + test.token
			}
			response := performRequest(
				t,
				handler,
				http.MethodGet,
				"/admin/v1/permissions",
				nil,
				headers,
			)
			if response.Code != test.wantStatus ||
				!strings.Contains(
					response.Body.String(),
					`"code":"`+test.wantCode+`"`,
				) {
				t.Fatalf(
					"status/body = %d/%q, want %d with code %q",
					response.Code,
					response.Body.String(),
					test.wantStatus,
					test.wantCode,
				)
			}
			if strings.Contains(response.Body.String(), "must not escape") {
				t.Fatalf(
					"failed request leaked catalog: %s",
					response.Body.String(),
				)
			}
			if auth.calls != test.wantAuthCalls ||
				authorizer.calls != test.wantPolicyCalls ||
				catalog.calls != test.wantCatalogCalls {
				t.Fatalf(
					"calls auth/authorizer/catalog = %d/%d/%d, want %d/%d/%d",
					auth.calls,
					authorizer.calls,
					catalog.calls,
					test.wantAuthCalls,
					test.wantPolicyCalls,
					test.wantCatalogCalls,
				)
			}
		})
	}
}
