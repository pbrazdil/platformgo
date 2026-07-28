package application

import (
	"context"
	"errors"

	"github.com/upcomers-org/platformgo/internal/edge"
)

// ErrAdminFleetFillsReaderRequired means the read boundary is not configured.
var ErrAdminFleetFillsReaderRequired = errors.New(
	"admin fleet fills: reader is required",
)

// AdminFleetFillsNonEmptyStateError fails closed until fill DTO mapping exists.
type AdminFleetFillsNonEmptyStateError struct{}

func (*AdminFleetFillsNonEmptyStateError) Error() string {
	return "admin fleet fills non-empty projection is not implemented"
}

// AdminFleetFill is intentionally empty while only fresh-state reads exist.
type AdminFleetFill struct{}

// AdminFleetFillsPage is the empty-only application result.
type AdminFleetFillsPage struct {
	Items []AdminFleetFill
	Total *int64
}

// AdminFleetFillsExistenceReader exposes only whether any fill exists.
type AdminFleetFillsExistenceReader interface {
	AdminFleetFillsExist(context.Context) (bool, error)
}

// AdminFleetFillsHandler authorizes and serves the empty-only fleet view.
type AdminFleetFillsHandler struct {
	reader AdminFleetFillsExistenceReader
}

// NewAdminFleetFillsHandler binds the application boundary to its reader.
func NewAdminFleetFillsHandler(
	reader AdminFleetFillsExistenceReader,
) *AdminFleetFillsHandler {
	return &AdminFleetFillsHandler{reader: reader}
}

// Handle returns an exact empty page or fails closed when any fill exists.
func (handler *AdminFleetFillsHandler) Handle(
	ctx context.Context,
	principal edge.Principal,
) (AdminFleetFillsPage, error) {
	if principal.Audience != edge.AudienceAdmin {
		return AdminFleetFillsPage{}, edge.ErrForbidden
	}
	if handler == nil || handler.reader == nil {
		return AdminFleetFillsPage{}, ErrAdminFleetFillsReaderRequired
	}
	exists, err := handler.reader.AdminFleetFillsExist(ctx)
	if err != nil {
		return AdminFleetFillsPage{}, err
	}
	if exists {
		return AdminFleetFillsPage{}, &AdminFleetFillsNonEmptyStateError{}
	}
	total := int64(0)
	return AdminFleetFillsPage{
		Items: make([]AdminFleetFill, 0),
		Total: &total,
	}, nil
}
