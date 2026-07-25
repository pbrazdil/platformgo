package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const (
	outboxPublisherLockNamespace      = 0x50474f42
	outboxReadyLockNamespace          = 0x50475242
	engineReadyLockNamespace          = 0x50475245
	commandAdmissionGateLockNamespace = 0x50474144
)

// RoleLease is a PostgreSQL-session lifetime proof that one deployment role is
// active. Closing it releases the advisory lock before the session is returned.
type RoleLease struct {
	connection              *pgxpool.Conn
	namespace               int64
	key                     int64
	drainsCommandAdmissions bool
}

// AcquireOutboxPublisher fails when another publisher already owns the role.
func (store *MessagingStore) AcquireOutboxPublisher(
	ctx context.Context,
) (*RoleLease, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New(
			"acquire outbox publisher: PostgreSQL pool is required",
		)
	}
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire outbox publisher connection: %w", err)
	}
	var acquired bool
	if err := connection.QueryRow(
		ctx,
		"SELECT pg_try_advisory_lock($1, $2)",
		outboxPublisherLockNamespace,
		int64(0),
	).Scan(&acquired); err != nil {
		connection.Release()
		return nil, fmt.Errorf("acquire outbox publisher lock: %w", err)
	}
	if !acquired {
		connection.Release()
		return nil, errors.New("another outbox publisher owns the role")
	}
	return &RoleLease{
		connection: connection,
		namespace:  outboxPublisherLockNamespace,
	}, nil
}

// AcquireOutboxReady publishes the narrower post-initialization readiness
// capability. It is released as soon as draining begins.
func (store *MessagingStore) AcquireOutboxReady(
	ctx context.Context,
) (*RoleLease, error) {
	if store == nil {
		return nil, errors.New(
			"acquire outbox readiness: PostgreSQL store is required",
		)
	}
	lease, err := acquireRoleLease(
		ctx,
		store.pool,
		outboxReadyLockNamespace,
		0,
		"outbox readiness",
	)
	if lease != nil {
		lease.drainsCommandAdmissions = true
	}
	return lease, err
}

// AcquireEngineReady publishes readiness only after recovery and durable
// consumer initialization have both completed.
func (store *EngineStore) AcquireEngineReady(
	ctx context.Context,
	shardID engine.ShardID,
) (*RoleLease, error) {
	if store == nil {
		return nil, errors.New(
			"acquire engine readiness: PostgreSQL store is required",
		)
	}
	lease, err := acquireRoleLease(
		ctx,
		store.pool,
		engineReadyLockNamespace,
		int64(shardID),
		"engine readiness",
	)
	if lease != nil {
		lease.drainsCommandAdmissions = true
	}
	return lease, err
}

func acquireRoleLease(
	ctx context.Context,
	pool *pgxpool.Pool,
	namespace int64,
	key int64,
	name string,
) (*RoleLease, error) {
	if pool == nil {
		return nil, fmt.Errorf("acquire %s: PostgreSQL pool is required", name)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire %s connection: %w", name, err)
	}
	var acquired bool
	if err := connection.QueryRow(
		ctx,
		"SELECT pg_try_advisory_lock($1, $2)",
		namespace,
		key,
	).Scan(&acquired); err != nil {
		connection.Release()
		return nil, fmt.Errorf("acquire %s lock: %w", name, err)
	}
	if !acquired {
		connection.Release()
		return nil, fmt.Errorf("another process owns %s", name)
	}
	return &RoleLease{
		connection: connection,
		namespace:  namespace,
		key:        key,
	}, nil
}

// Close releases one role lock and its dedicated PostgreSQL session.
func (lease *RoleLease) Close(ctx context.Context) error {
	if lease == nil || lease.connection == nil {
		return nil
	}
	connection := lease.connection
	lease.connection = nil
	defer connection.Release()
	var released bool
	if err := connection.QueryRow(
		context.WithoutCancel(ctx),
		"SELECT pg_advisory_unlock($1, $2)",
		lease.namespace,
		lease.key,
	).Scan(&released); err != nil {
		return fmt.Errorf("release runtime role lock: %w", err)
	}
	if !released {
		return errors.New("release runtime role lock: lock was not held")
	}
	if lease.drainsCommandAdmissions {
		if _, err := connection.Exec(
			context.WithoutCancel(ctx),
			"SELECT pg_advisory_lock($1, $2)",
			commandAdmissionGateLockNamespace,
			int64(0),
		); err != nil {
			return fmt.Errorf("drain command admission gate: %w", err)
		}
		var gateReleased bool
		if err := connection.QueryRow(
			context.WithoutCancel(ctx),
			"SELECT pg_advisory_unlock($1, $2)",
			commandAdmissionGateLockNamespace,
			int64(0),
		).Scan(&gateReleased); err != nil {
			return fmt.Errorf("release command admission gate: %w", err)
		}
		if !gateReleased {
			return errors.New(
				"release command admission gate: lock was not held",
			)
		}
	}
	return nil
}

// RuntimeCommandReady verifies current PostgreSQL authority for command
// admission: active engine/outbox roles, a non-halted checkpoint, no durable
// fault, and no stale unpublished command.
func (store *CompatibilityStore) RuntimeCommandReady(
	ctx context.Context,
	shardID engine.ShardID,
) error {
	if store == nil || store.pool == nil {
		return errors.New("runtime readiness: PostgreSQL pool is required")
	}
	return runtimeCommandReady(ctx, store.pool, shardID)
}

type runtimeReadinessQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func runtimeCommandReady(
	ctx context.Context,
	querier runtimeReadinessQuerier,
	shardID engine.ShardID,
) error {
	var engineActive bool
	var engineReady bool
	var outboxActive bool
	var outboxReady bool
	var checkpointReady bool
	var staleCommandOutbox bool
	if err := querier.QueryRow(ctx, `
		SELECT
			engine_active,
			engine_ready,
			outbox_active,
			outbox_ready,
			checkpoint_ready,
			stale_command_outbox
		FROM engine.runtime_command_ready_probe($1)`,
		uint32(shardID),
	).Scan(
		&engineActive,
		&engineReady,
		&outboxActive,
		&outboxReady,
		&checkpointReady,
		&staleCommandOutbox,
	); err != nil {
		return fmt.Errorf("runtime readiness: %w", err)
	}
	switch {
	case !engineActive:
		return fmt.Errorf("%w: engine shard owner is absent", ErrRuntimeNotReady)
	case !engineReady:
		return fmt.Errorf("%w: engine recovery is incomplete", ErrRuntimeNotReady)
	case !outboxActive:
		return fmt.Errorf("%w: outbox publisher is absent", ErrRuntimeNotReady)
	case !outboxReady:
		return fmt.Errorf("%w: outbox publisher is draining", ErrRuntimeNotReady)
	case !checkpointReady:
		return fmt.Errorf("%w: engine checkpoint is halted", ErrRuntimeNotReady)
	case staleCommandOutbox:
		return fmt.Errorf("%w: command outbox is stale", ErrRuntimeNotReady)
	default:
		return nil
	}
}
