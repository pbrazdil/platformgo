package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/edge"
)

// AdminPermissionAuthorizer evaluates administrative permissions through the
// least-privilege PostgreSQL authority function.
type AdminPermissionAuthorizer struct {
	pool *pgxpool.Pool
}

// NewAdminPermissionAuthorizer binds the durable RBAC authority.
func NewAdminPermissionAuthorizer(
	pool *pgxpool.Pool,
) *AdminPermissionAuthorizer {
	return &AdminPermissionAuthorizer{pool: pool}
}

// AuthorizeAdmin fails closed for every non-admin principal and delegates the
// authenticated subject's current role graph to PostgreSQL.
func (authorizer *AdminPermissionAuthorizer) AuthorizeAdmin(
	ctx context.Context,
	principal edge.Principal,
	resource string,
	action string,
) (bool, error) {
	if principal.Audience != edge.AudienceAdmin {
		return false, nil
	}
	if authorizer == nil || authorizer.pool == nil {
		return false, errors.New(
			"admin permission authorizer: PostgreSQL pool is required",
		)
	}
	var allowed bool
	if err := authorizer.pool.QueryRow(
		ctx,
		`SELECT identity.admin_has_permission($1, $2, $3)`,
		principal.Subject,
		resource,
		action,
	).Scan(&allowed); err != nil {
		return false, fmt.Errorf("authorize admin permission: %w", err)
	}
	return allowed, nil
}
