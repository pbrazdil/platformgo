package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/edge"
)

// ErrAdminRiskMonitorReaderRequired means the read boundary is not configured.
var ErrAdminRiskMonitorReaderRequired = errors.New(
	"admin risk monitor: reader is required",
)

// AdminRiskMonitorNonEmptyStateError fails closed until risk DTO mapping exists.
type AdminRiskMonitorNonEmptyStateError struct{}

func (*AdminRiskMonitorNonEmptyStateError) Error() string {
	return "admin risk monitor non-empty projection is not implemented"
}

// AdminRiskAccount is intentionally empty while only fresh-state reads exist.
type AdminRiskAccount struct{}

// AdminRiskStateReader exposes only whether supported durable risk state exists.
type AdminRiskStateReader interface {
	AdminRiskStateExists(context.Context) (bool, error)
}

// AdminRiskMonitorHandler authorizes and serves the empty-only risk view.
type AdminRiskMonitorHandler struct {
	reader AdminRiskStateReader
}

// NewAdminRiskMonitorHandler binds the application boundary to its reader.
func NewAdminRiskMonitorHandler(
	reader AdminRiskStateReader,
) *AdminRiskMonitorHandler {
	return &AdminRiskMonitorHandler{reader: reader}
}

// Handle returns an exact empty result or fails closed when risk state exists.
func (handler *AdminRiskMonitorHandler) Handle(
	ctx context.Context,
	principal edge.Principal,
) ([]AdminRiskAccount, error) {
	if principal.Audience != edge.AudienceAdmin {
		return nil, edge.ErrForbidden
	}
	if handler == nil || handler.reader == nil {
		return nil, ErrAdminRiskMonitorReaderRequired
	}
	exists, err := handler.reader.AdminRiskStateExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("read admin risk state: %w", err)
	}
	if exists {
		return nil, &AdminRiskMonitorNonEmptyStateError{}
	}
	return make([]AdminRiskAccount, 0), nil
}
