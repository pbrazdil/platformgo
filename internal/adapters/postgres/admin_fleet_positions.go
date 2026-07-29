package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminFleetPositionsReader checks whether the empty-only fleet view is
// supported.
type AdminFleetPositionsReader struct {
	pool *pgxpool.Pool
}

// NewAdminFleetPositionsReader binds the fleet positions reader to PostgreSQL.
func NewAdminFleetPositionsReader(
	pool *pgxpool.Pool,
) *AdminFleetPositionsReader {
	return &AdminFleetPositionsReader{pool: pool}
}

// AdminFleetPositionsExist reads no economic values and executes exactly one
// statement.
func (reader *AdminFleetPositionsReader) AdminFleetPositionsExist(
	ctx context.Context,
) (bool, error) {
	if reader == nil || reader.pool == nil {
		return false, errors.New(
			"admin fleet positions reader: PostgreSQL pool is required",
		)
	}
	var exists bool
	if err := reader.pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM trading.positions) OR EXISTS (SELECT 1 FROM trading.fills)`,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("read admin fleet positions existence: %w", err)
	}
	return exists, nil
}
