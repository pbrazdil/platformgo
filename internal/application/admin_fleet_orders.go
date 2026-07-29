package application

import (
	"context"
	"errors"

	"github.com/upcomers-org/platformgo/internal/edge"
)

// ErrAdminFleetOrdersReaderRequired means the read boundary is not configured.
var ErrAdminFleetOrdersReaderRequired = errors.New(
	"admin fleet orders: reader is required",
)

// AdminFleetOrdersNonEmptyStateError fails closed until order DTO mapping exists.
type AdminFleetOrdersNonEmptyStateError struct{}

func (*AdminFleetOrdersNonEmptyStateError) Error() string {
	return "admin fleet orders non-empty projection is not implemented"
}

// AdminFleetOrder is intentionally empty while only fresh-state reads exist.
type AdminFleetOrder struct{}

// AdminFleetOrdersPage is the empty-only application result.
type AdminFleetOrdersPage struct {
	Items []AdminFleetOrder
	Total *int64
}

// AdminFleetOrdersExistenceReader exposes only whether any order state exists.
type AdminFleetOrdersExistenceReader interface {
	AdminFleetOrdersExist(context.Context) (bool, error)
}

// AdminFleetOrdersHandler authorizes and serves the empty-only fleet view.
type AdminFleetOrdersHandler struct {
	reader AdminFleetOrdersExistenceReader
}

// NewAdminFleetOrdersHandler binds the application boundary to its reader.
func NewAdminFleetOrdersHandler(
	reader AdminFleetOrdersExistenceReader,
) *AdminFleetOrdersHandler {
	return &AdminFleetOrdersHandler{reader: reader}
}

// Handle returns an exact empty page or fails closed when any order state exists.
func (handler *AdminFleetOrdersHandler) Handle(
	ctx context.Context,
	principal edge.Principal,
) (AdminFleetOrdersPage, error) {
	if principal.Audience != edge.AudienceAdmin {
		return AdminFleetOrdersPage{}, edge.ErrForbidden
	}
	if handler == nil || handler.reader == nil {
		return AdminFleetOrdersPage{}, ErrAdminFleetOrdersReaderRequired
	}
	exists, err := handler.reader.AdminFleetOrdersExist(ctx)
	if err != nil {
		return AdminFleetOrdersPage{}, err
	}
	if exists {
		return AdminFleetOrdersPage{}, &AdminFleetOrdersNonEmptyStateError{}
	}
	total := int64(0)
	return AdminFleetOrdersPage{
		Items: make([]AdminFleetOrder, 0),
		Total: &total,
	}, nil
}
