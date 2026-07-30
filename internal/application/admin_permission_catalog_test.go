package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/upcomers-org/platformgo/internal/edge"
)

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_config_settings.rs:231
//	test: permission_catalog_lists_the_full_scope_and_is_gated
//
// The exact ordered catalog and copy-isolation checks strengthen the source
// test from its static Resource::ALL and Action::ALL helpers. AudienceAdmin
// preserves the source test's trusted-system success versus client denial; it
// does not claim the source query dispatcher's separate Roles:Read policy.
func TestPermissionCatalogListsTheFullScopeAndIsGated(t *testing.T) {
	handler := AdminPermissionCatalogHandler{}
	want := AdminPermissionCatalog{
		Resources: []PermissionCatalogItem{
			{ID: "diagnostics", Label: "Diagnostics & Settings"},
			{ID: "admins", Label: "Administrators"},
			{ID: "users", Label: "Users"},
			{ID: "roles", Label: "Roles & Permissions"},
			{ID: "api-keys", Label: "API Keys"},
			{ID: "schedules", Label: "Schedules"},
			{ID: "accounts", Label: "Trading Accounts"},
			{ID: "orders", Label: "Orders"},
			{ID: "instruments", Label: "Instruments & Feeds"},
			{ID: "collections", Label: "Instrument Collections"},
			{ID: "tenants", Label: "Tenants (Brands)"},
		},
		Actions: []PermissionCatalogItem{
			{ID: "read", Label: "Read"},
			{ID: "write", Label: "Write"},
			{ID: "create", Label: "Create"},
			{ID: "delete", Label: "Delete"},
		},
	}

	got, err := handler.Handle(context.Background(), edge.Principal{
		Subject:  "admin::urn:xb:admin:test",
		Audience: edge.AudienceAdmin,
	})
	if err != nil {
		t.Fatalf("handle admin permission catalog: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog = %#v, want %#v", got, want)
	}

	got.Resources[0].ID = "mutated"
	got.Actions[0], got.Actions[1] = got.Actions[1], got.Actions[0]
	again, err := handler.Handle(context.Background(), edge.Principal{
		Subject:  "admin::urn:xb:admin:test",
		Audience: edge.AudienceAdmin,
	})
	if err != nil {
		t.Fatalf("handle admin permission catalog again: %v", err)
	}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("catalog after caller mutation = %#v, want %#v", again, want)
	}

	denied := map[string]edge.Principal{
		"zero": {},
		"client wildcard": {
			Subject:  "user::urn:xb:user:test",
			Audience: edge.AudienceClient,
			Scopes:   []string{"*"},
			Accounts: []string{"*"},
		},
		"broker wildcard": {
			Subject:  "broker::urn:xb:broker:test",
			Audience: edge.AudienceBroker,
			Scopes:   []string{"*"},
			Accounts: []string{"*"},
		},
	}
	for name, principal := range denied {
		t.Run(name, func(t *testing.T) {
			catalog, err := handler.Handle(context.Background(), principal)
			if !errors.Is(err, edge.ErrForbidden) {
				t.Fatalf("error = %v, want %v", err, edge.ErrForbidden)
			}
			if catalog.Resources != nil || catalog.Actions != nil {
				t.Fatalf("catalog = %#v, want literal zero", catalog)
			}
		})
	}
}
