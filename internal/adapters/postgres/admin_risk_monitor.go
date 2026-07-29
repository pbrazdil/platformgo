package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminRiskMonitorReader checks whether the empty-only risk view is supported.
type AdminRiskMonitorReader struct {
	pool *pgxpool.Pool
}

// NewAdminRiskMonitorReader binds the risk reader to PostgreSQL.
func NewAdminRiskMonitorReader(pool *pgxpool.Pool) *AdminRiskMonitorReader {
	return &AdminRiskMonitorReader{pool: pool}
}

// AdminRiskStateExists reads no economic values and executes exactly one
// statement.
func (reader *AdminRiskMonitorReader) AdminRiskStateExists(
	ctx context.Context,
) (bool, error) {
	if reader == nil || reader.pool == nil {
		return false, errors.New(
			"admin risk monitor reader: PostgreSQL pool is required",
		)
	}
	var exists bool
	if err := reader.pool.QueryRow(
		ctx,
		`SELECT trading.admin_risk_state_exists()`,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("read admin risk state existence: %w", err)
	}
	return exists, nil
}
