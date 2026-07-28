package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminFleetFillsReader checks whether the empty-only fleet view is supported.
type AdminFleetFillsReader struct {
	pool *pgxpool.Pool
}

// NewAdminFleetFillsReader binds the fleet fills reader to PostgreSQL.
func NewAdminFleetFillsReader(pool *pgxpool.Pool) *AdminFleetFillsReader {
	return &AdminFleetFillsReader{pool: pool}
}

// AdminFleetFillsExist reads no fill values and executes exactly one statement.
func (reader *AdminFleetFillsReader) AdminFleetFillsExist(
	ctx context.Context,
) (bool, error) {
	if reader == nil || reader.pool == nil {
		return false, errors.New(
			"admin fleet fills reader: PostgreSQL pool is required",
		)
	}
	var exists bool
	if err := reader.pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM trading.fills)`,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("read admin fleet fills existence: %w", err)
	}
	return exists, nil
}
