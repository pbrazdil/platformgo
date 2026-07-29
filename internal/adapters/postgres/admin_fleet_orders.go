package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminFleetOrdersReader checks whether the empty-only fleet view is supported.
type AdminFleetOrdersReader struct {
	pool *pgxpool.Pool
}

// NewAdminFleetOrdersReader binds the fleet orders reader to PostgreSQL.
func NewAdminFleetOrdersReader(pool *pgxpool.Pool) *AdminFleetOrdersReader {
	return &AdminFleetOrdersReader{pool: pool}
}

// AdminFleetOrdersExist reads no order values and executes exactly one statement.
func (reader *AdminFleetOrdersReader) AdminFleetOrdersExist(
	ctx context.Context,
) (bool, error) {
	if reader == nil || reader.pool == nil {
		return false, errors.New(
			"admin fleet orders reader: PostgreSQL pool is required",
		)
	}
	var exists bool
	if err := reader.pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM trading.orders) OR EXISTS (SELECT 1 FROM trading.order_intents)`,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("read admin fleet orders existence: %w", err)
	}
	return exists, nil
}
