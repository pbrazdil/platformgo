package application

import (
	"context"

	"github.com/upcomers-org/platformgo/internal/edge"
)

// PermissionCatalogItem is one stable authorization resource or action.
type PermissionCatalogItem struct {
	ID    string
	Label string
}

// AdminPermissionCatalog lists the authorization vocabulary shown to trusted
// administrative callers.
type AdminPermissionCatalog struct {
	Resources []PermissionCatalogItem
	Actions   []PermissionCatalogItem
}

// AdminPermissionCatalogHandler returns the static administrative permission
// vocabulary. Authentication and the source Roles:Read dispatcher policy are
// separate boundaries; this handler enforces the established admin audience.
type AdminPermissionCatalogHandler struct{}

func (AdminPermissionCatalogHandler) Handle(
	_ context.Context,
	principal edge.Principal,
) (AdminPermissionCatalog, error) {
	if principal.Audience != edge.AudienceAdmin {
		return AdminPermissionCatalog{}, edge.ErrForbidden
	}

	return AdminPermissionCatalog{
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
	}, nil
}
