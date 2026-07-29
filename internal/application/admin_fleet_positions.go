package application

import (
	"context"
	"errors"

	"github.com/upcomers-org/platformgo/internal/edge"
)

// ErrAdminFleetPositionsReaderRequired means the read boundary is not configured.
var ErrAdminFleetPositionsReaderRequired = errors.New(
	"admin fleet positions: reader is required",
)

// AdminFleetPositionsNonEmptyStateError fails closed until position DTO mapping
// exists.
type AdminFleetPositionsNonEmptyStateError struct{}

func (*AdminFleetPositionsNonEmptyStateError) Error() string {
	return "admin fleet positions non-empty projection is not implemented"
}

// AdminFleetPosition is intentionally empty while only fresh-state reads exist.
type AdminFleetPosition struct{}

// AdminFleetPositionsPage is the empty-only application result.
type AdminFleetPositionsPage struct {
	Items []AdminFleetPosition
	Total *int64
}

// AdminFleetPositionsExistenceReader exposes only whether position state exists.
type AdminFleetPositionsExistenceReader interface {
	AdminFleetPositionsExist(context.Context) (bool, error)
}

// AdminFleetPositionsHandler authorizes and serves the empty-only fleet view.
type AdminFleetPositionsHandler struct {
	reader AdminFleetPositionsExistenceReader
}

// NewAdminFleetPositionsHandler binds the application boundary to its reader.
func NewAdminFleetPositionsHandler(
	reader AdminFleetPositionsExistenceReader,
) *AdminFleetPositionsHandler {
	return &AdminFleetPositionsHandler{reader: reader}
}

// Handle returns an exact empty page or fails closed when position state exists.
func (handler *AdminFleetPositionsHandler) Handle(
	ctx context.Context,
	principal edge.Principal,
) (AdminFleetPositionsPage, error) {
	if principal.Audience != edge.AudienceAdmin {
		return AdminFleetPositionsPage{}, edge.ErrForbidden
	}
	if handler == nil || handler.reader == nil {
		return AdminFleetPositionsPage{}, ErrAdminFleetPositionsReaderRequired
	}
	exists, err := handler.reader.AdminFleetPositionsExist(ctx)
	if err != nil {
		return AdminFleetPositionsPage{}, err
	}
	if exists {
		return AdminFleetPositionsPage{},
			&AdminFleetPositionsNonEmptyStateError{}
	}
	total := int64(0)
	return AdminFleetPositionsPage{
		Items: make([]AdminFleetPosition, 0),
		Total: &total,
	}, nil
}
