package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/engine"
)

var (
	// ErrInjectedFault marks an explicitly armed test-only transaction boundary.
	ErrInjectedFault = errors.New("injected durable execution fault")
	// ErrWriterConflict means another transaction currently owns the shard.
	ErrWriterConflict = errors.New("another engine writer owns the shard")
	// ErrDeploymentShardConflict means the initial single-shard deployment was
	// already durably bound to a different shard.
	ErrDeploymentShardConflict = errors.New("deployment shard configuration conflict")
	// ErrDeploymentShardUnconfigured means the one-shot migrator has not
	// explicitly provisioned the deployment's engine shard.
	ErrDeploymentShardUnconfigured = errors.New("deployment shard is not configured")
	// ErrCheckpointMismatch means caller state and PostgreSQL authority disagree.
	ErrCheckpointMismatch = errors.New("engine checkpoint mismatch")
	// ErrCommandInputConflict means the delivered engine input does not match
	// the pending durable command with the same identity.
	ErrCommandInputConflict = errors.New("engine input conflicts with durable command")
	// ErrCommandPredecessorPending means a later account command reached the
	// engine before every lower account sequence was terminal.
	ErrCommandPredecessorPending = errors.New("command predecessor is pending")
)

const (
	// FailpointAfterPersistBeforeCommit proves that every durable effect rolls
	// back together before the transaction becomes externally visible.
	FailpointAfterPersistBeforeCommit = "postgres.after_persist_before_commit"
	engineWriterLockNamespace         = 0x50474f45
	engineOwnerLockNamespace          = 0x50474f4f
	engineRuntimeSchemaRevision       = "20260725001100_phase3_committed_realtime_outbox"
)

type faultSet interface {
	Reached(string) bool
}

// ApplyOptions carries deterministic, caller-owned execution controls.
type ApplyOptions struct {
	Faults    faultSet
	Ownership *ShardOwnership
}

// EngineStore is the PostgreSQL authority for one atomic engine decision.
type EngineStore struct {
	pool *pgxpool.Pool
}

// ShardOwnership holds the lifetime singleton lock for one active engine
// process. Close must be called during orderly shutdown.
type ShardOwnership struct {
	mu         sync.Mutex
	connection *pgxpool.Conn
	shardID    engine.ShardID
	epoch      uint64
}

// NewEngineStore binds the durable coordinator to PostgreSQL.
func NewEngineStore(pool *pgxpool.Pool) *EngineStore {
	return &EngineStore{pool: pool}
}

// HaltTradingInput durably records a deterministic transport or authority
// conflict without applying any decision effects.
func (store *EngineStore) HaltTradingInput(
	ctx context.Context,
	state engine.State,
	input engine.InputEnvelope,
	action engine.TradingAction,
	conflict error,
	ownership ...*ShardOwnership,
) (engine.State, error) {
	if store == nil || store.pool == nil {
		return state, errors.New("halt trading input: PostgreSQL pool is required")
	}
	if conflict == nil {
		return state, errors.New("halt trading input: conflict is required")
	}
	var owner *ShardOwnership
	if len(ownership) > 0 {
		owner = ownership[0]
	}
	tx, token, releaseOwnership, err := store.beginEngineTx(ctx, owner)
	if err != nil {
		return state, fmt.Errorf("halt trading input: begin transaction: %w", err)
	}
	defer releaseOwnership()
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	if err := ensureDeploymentShard(ctx, tx, state.ShardID()); err != nil {
		return state, err
	}
	if !token.present {
		if err := acquireOfflineShardOwnership(ctx, tx, state.ShardID()); err != nil {
			return state, err
		}
	}
	if err := acquireShardWriter(ctx, tx, state.ShardID()); err != nil {
		return state, err
	}
	if token.present {
		if err := verifyOwnershipEpoch(
			ctx,
			tx,
			state.ShardID(),
			token,
		); err != nil {
			return state, err
		}
	}
	if err := verifyCheckpoint(ctx, tx, state); err != nil {
		return state, err
	}
	halted, haltErr := engine.FailClosed(
		state,
		input,
		engine.ErrDurableInputConflict,
		conflict.Error(),
	)
	if err := persistEngineFault(
		ctx,
		tx,
		input,
		action,
		halted,
		haltErr,
	); err != nil {
		return state, err
	}
	if err := persistCheckpoint(ctx, tx, halted); err != nil {
		return state, err
	}
	if err := tx.Commit(ctx); err != nil {
		return state, fmt.Errorf("halt trading input: commit: %w", err)
	}
	return halted, nil
}

// AcquireShardOwnership fails immediately when another process owns the shard.
// The dedicated PostgreSQL session retains the lock until Close.
func (store *EngineStore) AcquireShardOwnership(
	ctx context.Context,
	shardID engine.ShardID,
) (*ShardOwnership, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("acquire shard ownership: PostgreSQL pool is required")
	}
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire shard %d ownership connection: %w", shardID, err)
	}
	if err := ensureDeploymentShard(ctx, connection, shardID); err != nil {
		connection.Release()
		return nil, err
	}
	var acquired bool
	if err := connection.QueryRow(
		ctx,
		"SELECT pg_try_advisory_lock($1, $2)",
		engineOwnerLockNamespace,
		int64(shardID),
	).Scan(&acquired); err != nil {
		connection.Release()
		return nil, fmt.Errorf("acquire shard %d ownership lock: %w", shardID, err)
	}
	if !acquired {
		connection.Release()
		return nil, fmt.Errorf("%w: shard %d process ownership", ErrWriterConflict, shardID)
	}
	var epoch uint64
	if err := connection.QueryRow(ctx, `
		INSERT INTO engine.shard_ownership_epochs (
			shard_id, epoch, acquired_at
		) VALUES ($1, 1, clock_timestamp())
		ON CONFLICT (shard_id) DO UPDATE SET
			epoch = engine.shard_ownership_epochs.epoch + 1,
			acquired_at = EXCLUDED.acquired_at
		RETURNING epoch`,
		int64(shardID),
	).Scan(&epoch); err != nil {
		var released bool
		_ = connection.QueryRow(
			context.WithoutCancel(ctx),
			"SELECT pg_advisory_unlock($1, $2)",
			engineOwnerLockNamespace,
			int64(shardID),
		).Scan(&released)
		connection.Release()
		return nil, fmt.Errorf("acquire shard %d ownership epoch: %w", shardID, err)
	}
	return &ShardOwnership{
		connection: connection,
		shardID:    shardID,
		epoch:      epoch,
	}, nil
}

// Check proves that the dedicated session still owns both the shard advisory
// lock and the durable ownership epoch. Reconciliation can advance the epoch
// to fence a live owner without depending on process-local notification.
func (ownership *ShardOwnership) Check(ctx context.Context) error {
	if ownership == nil {
		return errors.New("check shard ownership: ownership is required")
	}
	ownership.mu.Lock()
	defer ownership.mu.Unlock()
	if ownership.connection == nil {
		return fmt.Errorf(
			"%w: shard %d process ownership is closed",
			ErrWriterConflict,
			ownership.shardID,
		)
	}
	var held bool
	var currentEpoch uint64
	if err := ownership.connection.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM pg_locks
				 WHERE locktype = 'advisory'
				   AND pid = pg_backend_pid()
				   AND classid = $1::oid
				   AND objid = $2::oid
				   AND granted
			),
			COALESCE((
				SELECT epoch
				  FROM engine.shard_ownership_epochs
				 WHERE shard_id = $2
			), 0)`,
		engineOwnerLockNamespace,
		uint32(ownership.shardID),
	).Scan(&held, &currentEpoch); err != nil {
		return fmt.Errorf(
			"%w: check shard %d ownership session: %w",
			ErrWriterConflict,
			ownership.shardID,
			err,
		)
	}
	if !held {
		return fmt.Errorf(
			"%w: shard %d process ownership lock was lost",
			ErrWriterConflict,
			ownership.shardID,
		)
	}
	if currentEpoch != ownership.epoch {
		return fmt.Errorf(
			"%w: shard %d ownership epoch changed from %d to %d",
			ErrWriterConflict,
			ownership.shardID,
			ownership.epoch,
			currentEpoch,
		)
	}
	return nil
}

// Close releases the process-lifetime shard ownership lock.
func (ownership *ShardOwnership) Close(ctx context.Context) error {
	if ownership == nil {
		return nil
	}
	ownership.mu.Lock()
	defer ownership.mu.Unlock()
	if ownership.connection == nil {
		return nil
	}
	connection := ownership.connection
	ownership.connection = nil
	defer connection.Release()
	var released bool
	if err := connection.QueryRow(
		context.WithoutCancel(ctx),
		"SELECT pg_advisory_unlock($1, $2)",
		engineOwnerLockNamespace,
		int64(ownership.shardID),
	).Scan(&released); err != nil {
		return fmt.Errorf(
			"release shard %d ownership lock: %w",
			ownership.shardID,
			err,
		)
	}
	if !released {
		return fmt.Errorf(
			"release shard %d ownership lock: lock was not held",
			ownership.shardID,
		)
	}
	return nil
}

// ApplyTrading commits the receipt, normalized state, balanced ledger,
// checkpoint, and outbox in one transaction. It performs no network calls.
func (store *EngineStore) ApplyTrading(
	ctx context.Context,
	state engine.State,
	input engine.InputEnvelope,
	action engine.TradingAction,
	options ApplyOptions,
) (engine.State, engine.Decision, bool, error) {
	if store == nil || store.pool == nil {
		return state, engine.Decision{}, false, errors.New(
			"apply trading: PostgreSQL pool is required",
		)
	}
	tx, token, releaseOwnership, err := store.beginEngineTx(
		ctx,
		options.Ownership,
	)
	if err != nil {
		return state, engine.Decision{}, false, fmt.Errorf(
			"apply trading: begin transaction: %w",
			err,
		)
	}
	defer releaseOwnership()
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	if deploymentErr := ensureDeploymentShard(
		ctx,
		tx,
		state.ShardID(),
	); deploymentErr != nil {
		return state, engine.Decision{}, false, deploymentErr
	}
	if !token.present {
		if ownershipErr := acquireOfflineShardOwnership(
			ctx,
			tx,
			state.ShardID(),
		); ownershipErr != nil {
			return state, engine.Decision{}, false, ownershipErr
		}
	}
	if lockErr := acquireShardWriter(ctx, tx, state.ShardID()); lockErr != nil {
		return state, engine.Decision{}, false, lockErr
	}
	if token.present {
		if ownershipErr := verifyOwnershipEpoch(
			ctx,
			tx,
			state.ShardID(),
			token,
		); ownershipErr != nil {
			return state, engine.Decision{}, false, ownershipErr
		}
	}
	if checkpointErr := verifyCheckpoint(ctx, tx, state); checkpointErr != nil {
		return state, engine.Decision{}, false, checkpointErr
	}
	deliveryDecision, deliveryFound, deliveryMatches, err := loadDuplicateDelivery(
		ctx,
		tx,
		input,
	)
	if err != nil {
		return state, engine.Decision{}, false, err
	}
	if deliveryFound && deliveryMatches {
		return state, deliveryDecision, true, nil
	}
	receipts, err := loadRelevantReceipts(ctx, tx, input)
	if err != nil {
		return state, engine.Decision{}, false, err
	}
	if original, found := receipts.LookupByInputID(input.InputID); found &&
		original.StreamSequence != input.StreamSequence {
		next, decision, duplicateErr := engine.ApplyDuplicateDelivery(
			state,
			input,
			original,
		)
		if duplicateErr != nil {
			if next.Hash() != state.Hash() {
				if faultErr := persistEngineFault(
					ctx,
					tx,
					input,
					action,
					next,
					duplicateErr,
				); faultErr != nil {
					return state, engine.Decision{}, false, faultErr
				}
				if checkpointErr := persistCheckpoint(ctx, tx, next); checkpointErr != nil {
					return state, engine.Decision{}, false, checkpointErr
				}
				if commitErr := tx.Commit(ctx); commitErr != nil {
					return state, engine.Decision{}, false, fmt.Errorf(
						"commit duplicate-delivery fault: %w",
						commitErr,
					)
				}
			}
			return next, decision, false, duplicateErr
		}
		if persistErr := persistDuplicateDelivery(ctx, tx, input, decision); persistErr != nil {
			return state, engine.Decision{}, false, persistErr
		}
		if checkpointErr := persistCheckpoint(ctx, tx, next); checkpointErr != nil {
			return state, engine.Decision{}, false, checkpointErr
		}
		if options.Faults != nil &&
			options.Faults.Reached(FailpointAfterPersistBeforeCommit) {
			return state, engine.Decision{}, false, ErrInjectedFault
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return state, engine.Decision{}, false, fmt.Errorf(
				"commit duplicate delivery: %w",
				commitErr,
			)
		}
		return next, decision, true, nil
	}

	next, decision, err := engine.ApplyTradingWithReceipts(
		state,
		input,
		action,
		receipts,
	)
	if err != nil {
		if next.Hash() != state.Hash() {
			if faultErr := persistEngineFault(
				ctx,
				tx,
				input,
				action,
				next,
				err,
			); faultErr != nil {
				return state, engine.Decision{}, false, faultErr
			}
			if checkpointErr := persistCheckpoint(ctx, tx, next); checkpointErr != nil {
				return state, engine.Decision{}, false, checkpointErr
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return state, engine.Decision{}, false, fmt.Errorf(
					"commit durable engine fault: %w",
					commitErr,
				)
			}
		}
		return next, decision, false, err
	}
	duplicate := next.NextStreamSequence() == state.NextStreamSequence()
	if duplicate {
		return next, decision, true, nil
	}

	if err := persistDecision(ctx, tx, input, action, decision); err != nil {
		if isDeterministicDurableInputConflict(err) {
			halted, haltErr := engine.FailClosed(
				state,
				input,
				engine.ErrDurableInputConflict,
				err.Error(),
			)
			if faultErr := persistEngineFault(
				ctx,
				tx,
				input,
				action,
				halted,
				haltErr,
			); faultErr != nil {
				return state, engine.Decision{}, false, faultErr
			}
			if checkpointErr := persistCheckpoint(ctx, tx, halted); checkpointErr != nil {
				return state, engine.Decision{}, false, checkpointErr
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return state, engine.Decision{}, false, fmt.Errorf(
					"commit durable input conflict: %w",
					commitErr,
				)
			}
			return halted, engine.Decision{}, false, errors.Join(err, haltErr)
		}
		return state, engine.Decision{}, false, err
	}
	if err := persistReceipt(ctx, tx, input, decision); err != nil {
		return state, engine.Decision{}, false, err
	}
	if err := persistCheckpoint(ctx, tx, next); err != nil {
		return state, engine.Decision{}, false, err
	}
	if options.Faults != nil &&
		options.Faults.Reached(FailpointAfterPersistBeforeCommit) {
		return state, engine.Decision{}, false, ErrInjectedFault
	}
	if err := tx.Commit(ctx); err != nil {
		return state, engine.Decision{}, false, fmt.Errorf(
			"apply trading: commit transaction: %w",
			err,
		)
	}
	return next, decision, false, nil
}

func isDeterministicDurableInputConflict(err error) bool {
	return errors.Is(err, ErrCommandInputConflict) ||
		errors.Is(err, ErrAccountShardConflict) ||
		errors.Is(err, ErrCommandPredecessorPending) ||
		errors.Is(err, ErrCommandNotFound) ||
		errors.Is(err, ErrCommandCompletionConflict)
}

type deploymentShardExecutor interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ensureDeploymentShard(
	ctx context.Context,
	executor deploymentShardExecutor,
	shardID engine.ShardID,
) error {
	var configuredShard uint64
	if err := executor.QueryRow(ctx, `
		SELECT shard_id
		  FROM engine.deployment_shard
		 WHERE singleton`,
	).Scan(&configuredShard); errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf(
			"%w: requested shard %d",
			ErrDeploymentShardUnconfigured,
			shardID,
		)
	} else if err != nil {
		return fmt.Errorf("read deployment shard %d binding: %w", shardID, err)
	}
	if configuredShard != uint64(shardID) {
		return fmt.Errorf(
			"%w: configured shard %d, requested shard %d",
			ErrDeploymentShardConflict,
			configuredShard,
			shardID,
		)
	}
	return nil
}

func acquireShardWriter(
	ctx context.Context,
	tx pgx.Tx,
	shardID engine.ShardID,
) error {
	var acquired bool
	if err := tx.QueryRow(
		ctx,
		"SELECT pg_try_advisory_xact_lock($1, $2)",
		engineWriterLockNamespace,
		int64(shardID),
	).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire shard %d writer lock: %w", shardID, err)
	}
	if !acquired {
		return fmt.Errorf("%w: shard %d", ErrWriterConflict, shardID)
	}
	return nil
}

// acquireOfflineShardOwnership lets test and maintenance callers without a
// process-lifetime capability mutate a shard only while no live engine owner
// exists. The transaction-scoped lock uses the same namespace as the lifetime
// session lock, so it cannot write around an active processor.
func acquireOfflineShardOwnership(
	ctx context.Context,
	tx pgx.Tx,
	shardID engine.ShardID,
) error {
	var acquired bool
	if err := tx.QueryRow(
		ctx,
		"SELECT pg_try_advisory_xact_lock($1, $2)",
		engineOwnerLockNamespace,
		int64(shardID),
	).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire shard %d offline ownership: %w", shardID, err)
	}
	if !acquired {
		return fmt.Errorf("%w: shard %d process ownership", ErrWriterConflict, shardID)
	}
	return nil
}

type ownershipToken struct {
	shardID engine.ShardID
	epoch   uint64
	present bool
}

func (store *EngineStore) beginEngineTx(
	ctx context.Context,
	ownership *ShardOwnership,
) (pgx.Tx, ownershipToken, func(), error) {
	options := pgx.TxOptions{IsoLevel: pgx.Serializable}
	if ownership == nil {
		tx, err := store.pool.BeginTx(ctx, options)
		if err == nil {
			err = bindEngineRuntimeSchemaRevision(ctx, tx)
		}
		if err != nil && tx != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
		return tx, ownershipToken{}, func() {}, err
	}
	ownership.mu.Lock()
	if ownership.connection == nil || ownership.epoch == 0 {
		ownership.mu.Unlock()
		return nil, ownershipToken{}, func() {}, fmt.Errorf(
			"%w: shard %d ownership is inactive",
			ErrWriterConflict,
			ownership.shardID,
		)
	}
	tx, err := ownership.connection.BeginTx(ctx, options)
	if err != nil {
		ownership.mu.Unlock()
		return nil, ownershipToken{}, func() {}, fmt.Errorf(
			"%w: begin shard %d transaction on ownership session: %w",
			ErrWriterConflict,
			ownership.shardID,
			err,
		)
	}
	if err := bindEngineRuntimeSchemaRevision(ctx, tx); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		ownership.mu.Unlock()
		return nil, ownershipToken{}, func() {}, err
	}
	token := ownershipToken{
		shardID: ownership.shardID,
		epoch:   ownership.epoch,
		present: true,
	}
	return tx, token, ownership.mu.Unlock, nil
}

func bindEngineRuntimeSchemaRevision(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(
		ctx,
		"SELECT set_config('platformgo.runtime_schema_revision', $1, true)",
		engineRuntimeSchemaRevision,
	); err != nil {
		return fmt.Errorf("bind engine runtime schema revision: %w", err)
	}
	return nil
}

func verifyOwnershipEpoch(
	ctx context.Context,
	tx pgx.Tx,
	shardID engine.ShardID,
	token ownershipToken,
) error {
	if !token.present || token.shardID != shardID || token.epoch == 0 {
		return fmt.Errorf(
			"%w: shard %d ownership token is inactive or mismatched",
			ErrWriterConflict,
			shardID,
		)
	}
	var durableEpoch uint64
	var lockHeld bool
	if err := tx.QueryRow(ctx, `
		SELECT
			epoch,
			EXISTS (
				SELECT 1
				  FROM pg_locks
				 WHERE locktype = 'advisory'
				   AND pid = pg_backend_pid()
				   AND classid = $2::oid
				   AND objid = $1::oid
				   AND granted
			)
		  FROM engine.shard_ownership_epochs
		 WHERE shard_id = $1
		 FOR SHARE`,
		int64(shardID),
		engineOwnerLockNamespace,
	).Scan(&durableEpoch, &lockHeld); err != nil {
		return fmt.Errorf(
			"%w: verify shard %d ownership epoch: %w",
			ErrWriterConflict,
			shardID,
			err,
		)
	}
	if !lockHeld || durableEpoch != token.epoch {
		return fmt.Errorf(
			"%w: shard %d ownership lock=%t epoch %d was replaced by %d",
			ErrWriterConflict,
			shardID,
			lockHeld,
			token.epoch,
			durableEpoch,
		)
	}
	return nil
}

func verifyCheckpoint(
	ctx context.Context,
	tx pgx.Tx,
	state engine.State,
) error {
	var sequence uint64
	var ready bool
	var stateHash []byte
	err := tx.QueryRow(
		ctx,
		`SELECT next_stream_sequence, ready, state_hash
		   FROM engine.shard_checkpoints
		  WHERE shard_id = $1
		  FOR UPDATE`,
		int64(state.ShardID()),
	).Scan(&sequence, &ready, &stateHash)
	if errors.Is(err, pgx.ErrNoRows) {
		initial := engine.NewState(state.ShardID())
		if state.NextStreamSequence() != initial.NextStreamSequence() ||
			state.Ready() != initial.Ready() ||
			state.Hash() != initial.Hash() {
			return fmt.Errorf(
				"%w: shard %d has no durable checkpoint",
				ErrCheckpointMismatch,
				state.ShardID(),
			)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read shard %d checkpoint: %w", state.ShardID(), err)
	}
	if sequence != state.NextStreamSequence() ||
		ready != state.Ready() ||
		!equalHashBytes(state.Hash(), stateHash) {
		return fmt.Errorf(
			"%w: shard %d PostgreSQL=(%d,%t,%x) caller=(%d,%t,%s)",
			ErrCheckpointMismatch,
			state.ShardID(),
			sequence,
			ready,
			stateHash,
			state.NextStreamSequence(),
			state.Ready(),
			state.Hash(),
		)
	}
	return nil
}

func loadRelevantReceipts(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
) (*engine.MemoryReceiptIndex, error) {
	rows, err := tx.Query(
		ctx,
		`SELECT input_id::text, stream_sequence, schema_version,
		        input_hash_version, input_hash, business_input_hash_version,
		        business_input_hash, decision
		   FROM engine.input_receipts
		  WHERE shard_id = $1
		    AND (input_id = $2 OR stream_sequence = $3)
		  ORDER BY stream_sequence`,
		int64(input.ShardID),
		input.InputID.String(),
		input.StreamSequence,
	)
	if err != nil {
		return nil, fmt.Errorf("load relevant input receipts: %w", err)
	}
	defer rows.Close()

	index := engine.NewMemoryReceiptIndex()
	for rows.Next() {
		receipt, err := scanReceipt(rows)
		if err != nil {
			return nil, err
		}
		if err := index.Record(receipt); err != nil {
			return nil, fmt.Errorf("index durable receipt: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable receipts: %w", err)
	}
	return index, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanReceipt(row rowScanner) (engine.Receipt, error) {
	var inputIDText string
	var receipt engine.Receipt
	var inputHash []byte
	var businessInputHash []byte
	var decisionJSON []byte
	if err := row.Scan(
		&inputIDText,
		&receipt.StreamSequence,
		&receipt.SchemaVersion,
		&receipt.InputHashVersion,
		&inputHash,
		&receipt.BusinessHashVersion,
		&businessInputHash,
		&decisionJSON,
	); err != nil {
		return engine.Receipt{}, fmt.Errorf("scan durable receipt: %w", err)
	}
	inputID, err := engine.ParseID(inputIDText)
	if err != nil {
		return engine.Receipt{}, fmt.Errorf("parse durable receipt input ID: %w", err)
	}
	receipt.InputID = inputID
	if len(inputHash) != len(receipt.InputHash) {
		return engine.Receipt{}, fmt.Errorf(
			"durable receipt input hash length = %d",
			len(inputHash),
		)
	}
	copy(receipt.InputHash[:], inputHash)
	if len(businessInputHash) != len(receipt.BusinessInputHash) {
		return engine.Receipt{}, fmt.Errorf(
			"durable receipt business input hash length = %d",
			len(businessInputHash),
		)
	}
	copy(receipt.BusinessInputHash[:], businessInputHash)
	if err := json.Unmarshal(decisionJSON, &receipt.Decision); err != nil {
		return engine.Receipt{}, fmt.Errorf("decode durable decision: %w", err)
	}
	return receipt, nil
}

func loadDuplicateDelivery(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
) (engine.Decision, bool, bool, error) {
	var inputIDText string
	var inputHash []byte
	var decisionJSON []byte
	err := tx.QueryRow(ctx, `
		SELECT input_id::text, input_hash, decision
		  FROM engine.duplicate_delivery_receipts
		 WHERE shard_id = $1 AND stream_sequence = $2`,
		int64(input.ShardID),
		input.StreamSequence,
	).Scan(&inputIDText, &inputHash, &decisionJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.Decision{}, false, false, nil
	}
	if err != nil {
		return engine.Decision{}, false, false, fmt.Errorf(
			"load duplicate delivery receipt: %w",
			err,
		)
	}
	inputID, err := engine.ParseID(inputIDText)
	if err != nil {
		return engine.Decision{}, false, false, fmt.Errorf(
			"parse duplicate delivery input ID: %w",
			err,
		)
	}
	var decision engine.Decision
	if err := json.Unmarshal(decisionJSON, &decision); err != nil {
		return engine.Decision{}, false, false, fmt.Errorf(
			"decode duplicate delivery decision: %w",
			err,
		)
	}
	fingerprint := engine.InputHash(input)
	matches := inputID == input.InputID &&
		len(inputHash) == len(fingerprint) &&
		string(inputHash) == string(fingerprint[:])
	return decision, true, matches, nil
}

func persistDecision(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
	action engine.TradingAction,
	decision engine.Decision,
) error {
	if accountID, scoped := engine.TradingActionAccountID(action); scoped &&
		input.Kind != engine.InputKindCommand {
		if err := requireAccountShard(ctx, tx, accountID, input.ShardID); err != nil {
			return err
		}
	}
	if err := persistCommandResult(ctx, tx, input, decision.CommandResult); err != nil {
		return err
	}
	for _, change := range decision.InstrumentChanges {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.instruments (
				instrument_id, revision, price_scale, quantity_scale,
				settlement_currency, settlement_currency_scale,
				initial_margin_rate, maintenance_margin_rate, max_leverage,
				maker_fee_rate, taker_fee_rate, version
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1)
			ON CONFLICT (instrument_id) DO UPDATE SET
				revision = EXCLUDED.revision,
				price_scale = EXCLUDED.price_scale,
				quantity_scale = EXCLUDED.quantity_scale,
				settlement_currency = EXCLUDED.settlement_currency,
				settlement_currency_scale = EXCLUDED.settlement_currency_scale,
				initial_margin_rate = EXCLUDED.initial_margin_rate,
				maintenance_margin_rate = EXCLUDED.maintenance_margin_rate,
				max_leverage = EXCLUDED.max_leverage,
				maker_fee_rate = EXCLUDED.maker_fee_rate,
				taker_fee_rate = EXCLUDED.taker_fee_rate,
				version = trading.instruments.version + 1`,
			change.InstrumentID,
			change.Revision,
			change.PriceScale,
			change.QuantityScale,
			change.SettlementCurrency,
			change.SettlementCurrencyScale,
			change.InitialMarginRate,
			change.MaintenanceMarginRate,
			change.MaxLeverage,
			change.MakerFeeRate,
			change.TakerFeeRate,
		); err != nil {
			return fmt.Errorf("persist instrument %s: %w", change.InstrumentID, err)
		}
	}
	for _, change := range decision.AccountChanges {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.accounts (account_id, oms_mode, version)
			VALUES ($1,$2,1)
			ON CONFLICT (account_id) DO UPDATE SET
				oms_mode = EXCLUDED.oms_mode,
				version = trading.accounts.version + 1`,
			change.AccountID,
			string(change.OmsMode),
		); err != nil {
			return fmt.Errorf("persist account %s: %w", change.AccountID, err)
		}
	}
	if err := persistAccountProvisioning(
		ctx,
		tx,
		input.InputID,
		decision.AccountChanges,
	); err != nil {
		return err
	}
	for _, change := range decision.RiskChanges {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.risk_configs (
				account_id, instrument_id, margin_mode, leverage, version
			) VALUES ($1,$2,$3,$4,1)
			ON CONFLICT (account_id, instrument_id) DO UPDATE SET
				margin_mode = EXCLUDED.margin_mode,
				leverage = EXCLUDED.leverage,
				version = trading.risk_configs.version + 1`,
			change.AccountID,
			change.InstrumentID,
			string(change.MarginMode),
			change.Leverage,
		); err != nil {
			return fmt.Errorf(
				"persist risk %s/%s: %w",
				change.AccountID,
				change.InstrumentID,
				err,
			)
		}
	}
	for _, change := range decision.BalanceChanges {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger.balances (
				account_id, currency, total, used, free, equity, ledger_sequence
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (account_id, currency) DO UPDATE SET
				total = EXCLUDED.total,
				used = EXCLUDED.used,
				free = EXCLUDED.free,
				equity = EXCLUDED.equity,
				ledger_sequence = EXCLUDED.ledger_sequence,
				updated_at = clock_timestamp()`,
			change.AccountID,
			change.Currency,
			change.Total,
			change.Used,
			change.Free,
			change.Equity,
			input.StreamSequence,
		); err != nil {
			return fmt.Errorf(
				"persist balance %s/%s: %w",
				change.AccountID,
				change.Currency,
				err,
			)
		}
	}
	if err := persistLedger(ctx, tx, decision.LedgerChanges); err != nil {
		return err
	}
	if err := persistFunding(ctx, tx, input, decision.FundingChanges); err != nil {
		return err
	}
	if err := persistBooks(ctx, tx, input, decision.BookChanges); err != nil {
		return err
	}
	if err := persistRealtime(ctx, tx, action, decision); err != nil {
		return err
	}
	if err := persistOrders(ctx, tx, decision.OrderChanges); err != nil {
		return err
	}
	if err := persistFills(ctx, tx, input, decision.Fills); err != nil {
		return err
	}
	if err := persistPositions(ctx, tx, decision.PositionChanges); err != nil {
		return err
	}
	if err := persistOutbox(ctx, tx, input, decision.Events); err != nil {
		return err
	}
	return nil
}

func persistAccountProvisioning(
	ctx context.Context,
	tx pgx.Tx,
	commandID engine.ID,
	accountChanges []engine.AccountSnapshot,
) error {
	var accountID string
	var brokerTenant string
	var userID string
	var login int64
	var baseCurrency string
	var marketVenue string
	var permittedClasses []string
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT
			account_id,
			broker_subject,
			user_id,
			login,
			base_currency,
			market_venue,
			permitted_classes,
			created_at
		  FROM identity.account_provisioning_intents
		 WHERE command_id = $1`,
		commandID.String(),
	).Scan(
		&accountID,
		&brokerTenant,
		&userID,
		&login,
		&baseCurrency,
		&marketVenue,
		&permittedClasses,
		&createdAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"load account provisioning intent %s: %w",
			commandID,
			err,
		)
	}
	matched := false
	for _, change := range accountChanges {
		if change.AccountID == accountID {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf(
			"%w: account provisioning command %s produced no account change",
			ErrCommandCompletionConflict,
			commandID,
		)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO identity.user_accounts (
			user_id,
			account_id,
			broker_subject,
			created_at
		)
		SELECT $1,$2,$3,$4
		  FROM identity.users
		 WHERE user_id = $1
		   AND broker_subject = $3`,
		userID,
		accountID,
		brokerTenant,
		createdAt,
	)
	if err != nil {
		return fmt.Errorf("persist account ownership %s: %w", accountID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf(
			"persist account ownership %s: broker user is unavailable",
			accountID,
		)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id,
			broker_subject,
			login,
			base_currency,
			market_venue,
			permitted_classes,
			created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		accountID,
		brokerTenant,
		login,
		baseCurrency,
		marketVenue,
		permittedClasses,
		createdAt,
	); err != nil {
		return fmt.Errorf("persist account profile %s: %w", accountID, err)
	}
	return nil
}

func persistCommandResult(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
	result engine.CommandResult,
) error {
	if input.Kind != engine.InputKindCommand {
		var commandExists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM trading.commands
				 WHERE command_id = $1
			)`,
			input.InputID.String(),
		).Scan(&commandExists); err != nil {
			return fmt.Errorf(
				"check non-command input %s durable identity: %w",
				input.InputID,
				err,
			)
		}
		if commandExists {
			return fmt.Errorf(
				"%w: command %s delivered as non-command input",
				ErrCommandInputConflict,
				input.InputID,
			)
		}
		return nil
	}
	var commandStatus string
	var commandType string
	var schemaVersion uint32
	var accountID string
	var accountSequence uint64
	var storedPayload []byte
	var commandLogicalTime int64
	var outboxSubject string
	var outboxSchemaVersion uint32
	var outboxPayload []byte
	var assignedShardID *int64
	if err := tx.QueryRow(ctx, `
		SELECT c.status, c.command_type, c.schema_version, c.account_id,
		       c.account_sequence, c.canonical_payload, c.logical_time,
		       o.subject, o.schema_version, o.payload, a.shard_id
		  FROM trading.commands AS c
		  JOIN messaging.outbox AS o ON o.message_id = c.command_id
		  LEFT JOIN engine.account_shards AS a
		    ON a.account_id = c.account_id
		 WHERE c.command_id = $1
		 FOR UPDATE OF c`,
		input.InputID.String(),
	).Scan(
		&commandStatus,
		&commandType,
		&schemaVersion,
		&accountID,
		&accountSequence,
		&storedPayload,
		&commandLogicalTime,
		&outboxSubject,
		&outboxSchemaVersion,
		&outboxPayload,
		&assignedShardID,
	); errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrCommandNotFound, input.InputID)
	} else if err != nil {
		return fmt.Errorf("load command %s for engine commit: %w", input.InputID, err)
	}
	if commandStatus != "pending" {
		return fmt.Errorf(
			"%w: command %s is %s",
			ErrCommandCompletionConflict,
			input.InputID,
			commandStatus,
		)
	}
	if assignedShardID == nil || *assignedShardID != int64(input.ShardID) {
		assigned := "missing"
		if assignedShardID != nil {
			assigned = fmt.Sprint(*assignedShardID)
		}
		return fmt.Errorf(
			"%w: account %q is assigned to shard %s, input uses %d",
			ErrAccountShardConflict,
			accountID,
			assigned,
			input.ShardID,
		)
	}
	storedInput, storedAction, err := engine.DecodeInputMessage(outboxPayload)
	if err != nil {
		return fmt.Errorf(
			"%w: decode command %s outbox envelope: %w",
			ErrCommandInputConflict,
			input.InputID,
			err,
		)
	}
	canonicalStored, err := canonicalJSON(storedPayload)
	if err != nil {
		return fmt.Errorf("decode command %s canonical payload: %w", input.InputID, err)
	}
	canonicalInput, err := canonicalJSON(input.Payload.Bytes())
	if err != nil {
		return fmt.Errorf("decode command %s input payload: %w", input.InputID, err)
	}
	expectedSubject := fmt.Sprintf(
		"engine.input.%d.command.v%d",
		storedInput.ShardID,
		storedInput.SchemaVersion,
	)
	if storedInput.Kind != engine.InputKindCommand ||
		outboxSubject != expectedSubject ||
		outboxSchemaVersion != storedInput.SchemaVersion ||
		engine.BusinessInputHash(storedInput) != engine.BusinessInputHash(input) ||
		schemaVersion != input.SchemaVersion ||
		commandType != string(storedAction.Kind) ||
		accountSequence != input.SourceSequence ||
		commandLogicalTime != input.LogicalTime.UnixNano() ||
		!bytes.Equal(canonicalStored, canonicalInput) {
		return fmt.Errorf("%w: command %s", ErrCommandInputConflict, input.InputID)
	}
	if actionAccountID, scoped := engine.TradingActionAccountID(storedAction); scoped &&
		actionAccountID != accountID {
		return fmt.Errorf(
			"%w: command %s account lane %q differs from payload account %q",
			ErrCommandInputConflict,
			input.InputID,
			accountID,
			actionAccountID,
		)
	}
	var predecessorPending bool
	if predecessorErr := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM trading.commands
			 WHERE account_id = $1
			   AND account_sequence < $2
			   AND status = 'pending'
		)`,
		accountID,
		accountSequence,
	).Scan(&predecessorPending); predecessorErr != nil {
		return fmt.Errorf(
			"check command %s account predecessors: %w",
			input.InputID,
			predecessorErr,
		)
	}
	if predecessorPending {
		return fmt.Errorf(
			"%w: command %s account %q sequence %d",
			ErrCommandPredecessorPending,
			input.InputID,
			accountID,
			accountSequence,
		)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode command %s result: %w", input.InputID, err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE trading.commands
		   SET status = $2,
		       result = $3,
		       completed_at = clock_timestamp()
		 WHERE command_id = $1 AND status = 'pending'`,
		input.InputID.String(),
		string(result.Status),
		encoded,
	)
	if err != nil {
		return fmt.Errorf("persist command %s result: %w", input.InputID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf(
			"%w: command %s changed before engine commit",
			ErrCommandCompletionConflict,
			input.InputID,
		)
	}
	var replayResponseExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM trading.command_replay_responses
			 WHERE command_id = $1
		)`,
		input.InputID.String(),
	).Scan(&replayResponseExists); err != nil {
		return fmt.Errorf(
			"check command %s replay response: %w",
			input.InputID,
			err,
		)
	}
	if replayResponseExists {
		tag, err := tx.Exec(ctx, `
			UPDATE trading.idempotency_records AS idempotency
			   SET state = 'completed',
			       response_status = replay.response_status,
			       response_headers = replay.response_headers,
			       response_body = replay.response_body
			  FROM trading.command_replay_responses AS replay
			 WHERE idempotency.command_id = $1
			   AND replay.command_id = idempotency.command_id
			   AND idempotency.state = 'in_progress'`,
			input.InputID.String(),
		)
		if err != nil {
			return fmt.Errorf(
				"complete command %s replay response: %w",
				input.InputID,
				err,
			)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf(
				"%w: command %s replay response changed before engine commit",
				ErrCommandCompletionConflict,
				input.InputID,
			)
		}
	}
	return nil
}

func persistLedger(
	ctx context.Context,
	tx pgx.Tx,
	transactions []engine.LedgerTransactionSnapshot,
) error {
	for _, transaction := range transactions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger.transactions (
				transaction_id, business_key, input_id, logical_time
			) VALUES ($1,$2,$3,$4)`,
			transaction.TransactionID.String(),
			transaction.BusinessKey,
			transaction.InputID.String(),
			transaction.LogicalTime.UnixNano(),
		); err != nil {
			return fmt.Errorf(
				"persist ledger transaction %s: %w",
				transaction.TransactionID,
				err,
			)
		}
		for _, entry := range transaction.Entries {
			if _, err := tx.Exec(ctx, `
				INSERT INTO ledger.entries (
					entry_id, transaction_id, account_id, currency, amount
				) VALUES ($1,$2,$3,$4,$5)`,
				entry.EntryID.String(),
				transaction.TransactionID.String(),
				entry.AccountID,
				entry.Currency,
				entry.Amount,
			); err != nil {
				return fmt.Errorf(
					"persist ledger entry %s: %w",
					entry.EntryID,
					err,
				)
			}
		}
	}
	return nil
}

func persistFunding(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
	changes []engine.FundingSnapshot,
) error {
	for _, change := range changes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.funding_settlements (
				funding_id, settlement_id, position_id, input_id,
				account_id, instrument_id, signed_quantity, oracle_price,
				rate, amount, settlement_currency
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			change.FundingID.String(),
			change.SettlementID.String(),
			change.PositionID.String(),
			input.InputID.String(),
			change.AccountID,
			change.InstrumentID,
			change.SignedQuantity,
			change.OraclePrice,
			change.Rate,
			change.Amount,
			change.SettlementCurrency,
		); err != nil {
			return fmt.Errorf(
				"persist funding settlement %s: %w",
				change.FundingID,
				err,
			)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.funding_history_projection (
				funding_id, account_id, instrument_id, position_id, logical_time
			) VALUES ($1,$2,$3,$4,$5)`,
			change.FundingID.String(),
			change.AccountID,
			change.InstrumentID,
			change.PositionID.String(),
			input.LogicalTime.UnixNano(),
		); err != nil {
			return fmt.Errorf(
				"persist funding history projection %s: %w",
				change.FundingID,
				err,
			)
		}
	}
	return nil
}

func persistBooks(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
	changes []engine.BookSnapshot,
) error {
	for _, change := range changes {
		bids, err := json.Marshal(change.Bids)
		if err != nil {
			return fmt.Errorf("encode %s bids: %w", change.InstrumentID, err)
		}
		asks, err := json.Marshal(change.Asks)
		if err != nil {
			return fmt.Errorf("encode %s asks: %w", change.InstrumentID, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO market.books (
				instrument_id, mark_price, bids, asks, stream_sequence
			) VALUES ($1,NULLIF($2, '')::numeric,$3,$4,$5)
			ON CONFLICT (instrument_id) DO UPDATE SET
				mark_price = EXCLUDED.mark_price,
				bids = EXCLUDED.bids,
				asks = EXCLUDED.asks,
				stream_sequence = EXCLUDED.stream_sequence,
				updated_at = clock_timestamp()`,
			change.InstrumentID,
			change.MarkPrice,
			bids,
			asks,
			input.StreamSequence,
		); err != nil {
			return fmt.Errorf("persist book %s: %w", change.InstrumentID, err)
		}
	}
	return nil
}

func persistOrders(
	ctx context.Context,
	tx pgx.Tx,
	changes []engine.OrderSnapshot,
) error {
	for _, change := range changes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.orders (
				order_id, account_id, instrument_id, side, order_type,
				time_in_force, status, quantity, filled_quantity,
				average_fill_price, limit_price, trigger_price, triggered,
				triggered_at, reduce_only, position_id, bracket_id, bracket_leg,
				bracket_leg_index, has_rested, has_slippage_band,
				max_slippage_bps, slippage_reference, reject_reason, version
			) VALUES (
				$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
				$16,$17,$18,$19,$20,$21,$22,$23,$24,$25
			)
			ON CONFLICT (order_id) DO UPDATE SET
				status = EXCLUDED.status,
				quantity = EXCLUDED.quantity,
				filled_quantity = EXCLUDED.filled_quantity,
				average_fill_price = EXCLUDED.average_fill_price,
				limit_price = EXCLUDED.limit_price,
				trigger_price = EXCLUDED.trigger_price,
				triggered = EXCLUDED.triggered,
				triggered_at = EXCLUDED.triggered_at,
				has_rested = EXCLUDED.has_rested,
				reject_reason = EXCLUDED.reject_reason,
				version = EXCLUDED.version,
				updated_at = clock_timestamp()`,
			change.OrderID.String(),
			change.AccountID,
			change.InstrumentID,
			string(change.Side),
			string(change.Type),
			string(change.TimeInForce),
			string(change.Status),
			change.Quantity,
			change.FilledQuantity,
			decimalOrZero(change.AverageFillPrice),
			nullableText(change.Price),
			nullableText(change.TriggerPrice),
			change.Triggered,
			nullableLogicalTime(change.Triggered, change.TriggeredAt),
			change.ReduceOnly,
			nullableID(change.PositionID),
			nullableID(change.BracketID),
			nullableText(string(change.BracketLeg)),
			change.BracketLegIndex,
			change.HasRested,
			change.HasSlippageBand,
			change.MaxSlippageBPS,
			nullableText(change.SlippageReference),
			nullableText(string(change.RejectReason)),
			change.Version,
		); err != nil {
			return fmt.Errorf("persist order %s: %w", change.OrderID, err)
		}
	}
	return nil
}

func persistFills(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
	changes []engine.FillSnapshot,
) error {
	for _, change := range changes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.fills (
				fill_id, order_id, input_id, account_id, instrument_id,
				side, price, quantity, position_id, position_effect,
				realized_pnl, settlement_currency, liquidity_side, fee,
				fee_currency, logical_time
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			change.FillID.String(),
			change.OrderID.String(),
			input.InputID.String(),
			change.AccountID,
			change.InstrumentID,
			string(change.Side),
			change.Price,
			change.Quantity,
			change.PositionID.String(),
			string(change.PositionEffect),
			nullableText(change.RealizedPnL),
			nullableText(change.SettlementCurrency),
			string(change.LiquiditySide),
			nullableText(change.Fee),
			nullableText(change.FeeCurrency),
			change.LogicalTime.UnixNano(),
		); err != nil {
			return fmt.Errorf("persist fill %s: %w", change.FillID, err)
		}
	}
	return nil
}

func persistPositions(
	ctx context.Context,
	tx pgx.Tx,
	changes []engine.PositionSnapshot,
) error {
	for _, change := range changes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.positions (
				position_id, account_id, instrument_id, side, status,
				signed_quantity, average_open_price, realized_pnl,
				settlement_currency, margin_mode, isolated_collateral, version
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			ON CONFLICT (position_id) DO UPDATE SET
				side = EXCLUDED.side,
				status = EXCLUDED.status,
				signed_quantity = EXCLUDED.signed_quantity,
				average_open_price = EXCLUDED.average_open_price,
				realized_pnl = EXCLUDED.realized_pnl,
				margin_mode = EXCLUDED.margin_mode,
				isolated_collateral = EXCLUDED.isolated_collateral,
				version = EXCLUDED.version,
				updated_at = clock_timestamp()`,
			change.PositionID.String(),
			change.AccountID,
			change.InstrumentID,
			string(change.Side),
			string(change.Status),
			change.SignedQuantity,
			change.AverageOpenPrice,
			change.RealizedPnL,
			change.SettlementCurrency,
			string(change.MarginMode),
			change.IsolatedCollateral,
			change.Version,
		); err != nil {
			return fmt.Errorf("persist position %s: %w", change.PositionID, err)
		}
	}
	return nil
}

func persistOutbox(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
	events []engine.DomainEvent,
) error {
	for _, event := range events {
		payload, err := json.Marshal(domainEventEnvelope{
			MessageID:        event.EventID.String(),
			SchemaVersion:    engine.CurrentSchemaVersion,
			Kind:             event.Kind,
			CorrelationID:    input.InputID.String(),
			CausationID:      input.InputID.String(),
			AggregateID:      event.AggregateID.String(),
			AggregateVersion: event.AggregateVersion,
			LogicalTime:      event.LogicalTime.String(),
			Payload:          event,
		})
		if err != nil {
			return fmt.Errorf("encode event %s: %w", event.EventID, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO messaging.outbox (
				message_id, subject, schema_version, payload, producer_class,
				engine_shard_id, engine_input_id
			) VALUES ($1,$2,$3,$4,'engine',$5,$6)`,
			event.EventID.String(),
			"domain.v1."+event.Kind,
			engine.CurrentSchemaVersion,
			payload,
			int64(input.ShardID),
			input.InputID.String(),
		); err != nil {
			return fmt.Errorf("persist outbox event %s: %w", event.EventID, err)
		}
	}
	return nil
}

type domainEventEnvelope struct {
	MessageID        string             `json:"messageId"`
	SchemaVersion    uint32             `json:"schemaVersion"`
	Kind             string             `json:"kind"`
	CorrelationID    string             `json:"correlationId"`
	CausationID      string             `json:"causationId"`
	AggregateID      string             `json:"aggregateId"`
	AggregateVersion uint64             `json:"aggregateVersion"`
	LogicalTime      string             `json:"logicalTime"`
	Payload          engine.DomainEvent `json:"payload"`
}

type persistedEnvelope struct {
	InputID              string
	SchemaVersion        uint32
	ShardID              uint32
	Kind                 uint8
	SourceID             string
	SourceSequence       uint64
	StreamSequence       uint64
	MarketSequence       uint64
	LogicalTime          int64
	ConfigurationVersion uint64
	InstrumentVersion    uint64
	Payload              []byte
}

func encodeEnvelope(input engine.InputEnvelope) ([]byte, error) {
	return json.Marshal(persistedEnvelope{
		InputID:              input.InputID.String(),
		SchemaVersion:        input.SchemaVersion,
		ShardID:              uint32(input.ShardID),
		Kind:                 uint8(input.Kind),
		SourceID:             input.SourceID,
		SourceSequence:       input.SourceSequence,
		StreamSequence:       input.StreamSequence,
		MarketSequence:       input.MarketSequence,
		LogicalTime:          input.LogicalTime.UnixNano(),
		ConfigurationVersion: input.ConfigurationVersion,
		InstrumentVersion:    input.InstrumentVersion,
		Payload:              input.Payload.Bytes(),
	})
}

func decodeEnvelope(encoded []byte) (engine.InputEnvelope, engine.TradingAction, error) {
	var stored persistedEnvelope
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return engine.InputEnvelope{}, engine.TradingAction{}, fmt.Errorf(
			"decode durable envelope: %w",
			err,
		)
	}
	inputID, err := engine.ParseID(stored.InputID)
	if err != nil {
		return engine.InputEnvelope{}, engine.TradingAction{}, fmt.Errorf(
			"parse durable envelope input ID: %w",
			err,
		)
	}
	action, payload, err := engine.DecodeTradingActionPayload(stored.Payload)
	if err != nil {
		return engine.InputEnvelope{}, engine.TradingAction{}, err
	}
	return engine.InputEnvelope{
		InputID:              inputID,
		SchemaVersion:        stored.SchemaVersion,
		ShardID:              engine.ShardID(stored.ShardID),
		Kind:                 engine.InputKind(stored.Kind),
		SourceID:             stored.SourceID,
		SourceSequence:       stored.SourceSequence,
		StreamSequence:       stored.StreamSequence,
		MarketSequence:       stored.MarketSequence,
		LogicalTime:          engine.LogicalTime(stored.LogicalTime),
		ConfigurationVersion: stored.ConfigurationVersion,
		InstrumentVersion:    stored.InstrumentVersion,
		Payload:              payload,
	}, action, nil
}

func persistReceipt(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
	decision engine.Decision,
) error {
	envelopeJSON, err := encodeEnvelope(input)
	if err != nil {
		return fmt.Errorf("encode durable envelope: %w", err)
	}
	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode durable decision: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, business_input_hash,
			business_input_hash_version, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		int64(input.ShardID),
		input.InputID.String(),
		input.StreamSequence,
		input.SchemaVersion,
		decision.InputHashVersion,
		hashBytes(decision.InputHash),
		hashBytes(engine.BusinessInputHash(input)),
		engine.CurrentBusinessHashVersion,
		decision.DecisionHashVersion,
		hashBytes(decision.DecisionHash),
		hashBytes(decision.NextStateHash),
		envelopeJSON,
		decisionJSON,
	); err != nil {
		return fmt.Errorf("persist input receipt %s: %w", input.InputID, err)
	}
	return nil
}

func persistDuplicateDelivery(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
	decision engine.Decision,
) error {
	envelopeJSON, err := encodeEnvelope(input)
	if err != nil {
		return fmt.Errorf("encode duplicate delivery envelope: %w", err)
	}
	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode duplicate delivery decision: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO engine.duplicate_delivery_receipts (
			shard_id, stream_sequence, input_id, input_hash,
			original_decision_hash, decision_hash, resulting_state_hash,
			envelope, decision
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		int64(input.ShardID),
		input.StreamSequence,
		input.InputID.String(),
		hashBytes(decision.InputHash),
		hashBytes(decision.DuplicateOfDecisionHash),
		hashBytes(decision.DecisionHash),
		hashBytes(decision.NextStateHash),
		envelopeJSON,
		decisionJSON,
	); err != nil {
		return fmt.Errorf("persist duplicate delivery receipt: %w", err)
	}
	return nil
}

func persistEngineFault(
	ctx context.Context,
	tx pgx.Tx,
	input engine.InputEnvelope,
	action engine.TradingAction,
	halted engine.State,
	applyErr error,
) error {
	envelopeJSON, err := encodeEnvelope(input)
	if err != nil {
		return fmt.Errorf("encode fault envelope: %w", err)
	}
	actionPayload, err := engine.EncodeTradingAction(action)
	if err != nil {
		return fmt.Errorf("encode fault supplied action: %w", err)
	}
	errorKind := "engine_error"
	errorDetail := applyErr.Error()
	var engineError *engine.Error
	if errors.As(applyErr, &engineError) {
		errorKind = string(engineError.Kind)
		errorDetail = engineError.Detail
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO engine.shard_faults (
			shard_id, resulting_state_hash, input_id, stream_sequence,
			error_kind, error_detail, envelope, supplied_action
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		int64(input.ShardID),
		hashBytes(halted.Hash()),
		input.InputID.String(),
		input.StreamSequence,
		errorKind,
		errorDetail,
		envelopeJSON,
		actionPayload.Bytes(),
	); err != nil {
		return fmt.Errorf("persist shard %d engine fault: %w", input.ShardID, err)
	}
	return nil
}

func persistCheckpoint(
	ctx context.Context,
	tx pgx.Tx,
	state engine.State,
) error {
	const snapshot = `{"recovery":"receipt_replay","version":1}`
	if _, err := tx.Exec(ctx, `
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash, state_snapshot
		) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (shard_id) DO UPDATE SET
			next_stream_sequence = EXCLUDED.next_stream_sequence,
			ready = EXCLUDED.ready,
			state_hash = EXCLUDED.state_hash,
			state_snapshot = EXCLUDED.state_snapshot,
			updated_at = clock_timestamp()`,
		int64(state.ShardID()),
		state.NextStreamSequence(),
		state.Ready(),
		hashBytes(state.Hash()),
		snapshot,
	); err != nil {
		return fmt.Errorf("persist shard %d checkpoint: %w", state.ShardID(), err)
	}
	return nil
}

// RecoverTradingState deterministically replays committed canonical envelopes,
// verifies every stored decision hash, then checks the authoritative checkpoint.
func (store *EngineStore) RecoverTradingState(
	ctx context.Context,
	shardID engine.ShardID,
) (engine.State, error) {
	if store == nil || store.pool == nil {
		return engine.State{}, errors.New(
			"recover trading state: PostgreSQL pool is required",
		)
	}
	return recoverTradingState(ctx, store.pool, shardID)
}

type postgresQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func recoverTradingState(
	ctx context.Context,
	querier postgresQuerier,
	shardID engine.ShardID,
) (engine.State, error) {
	rows, err := querier.Query(ctx, `
		SELECT receipt_kind, envelope, decision_hash, resulting_state_hash,
		       decision_hash_version,
		       business_input_hash_version, business_input_hash
		  FROM (
			SELECT
				'business'::text AS receipt_kind,
				envelope,
				decision_hash,
				resulting_state_hash,
				decision_hash_version,
				business_input_hash_version,
				business_input_hash,
				stream_sequence
			  FROM engine.input_receipts
			 WHERE shard_id = $1
			UNION ALL
			SELECT
				'duplicate'::text AS receipt_kind,
				envelope,
				decision_hash,
				resulting_state_hash,
				COALESCE(
					(decision ->> 'DecisionHashVersion')::integer,
					2
				) AS decision_hash_version,
				NULL::integer AS business_input_hash_version,
				NULL::bytea AS business_input_hash,
				stream_sequence
			  FROM engine.duplicate_delivery_receipts
			 WHERE shard_id = $1
		  ) AS receipts
		 ORDER BY stream_sequence`,
		int64(shardID),
	)
	if err != nil {
		return engine.State{}, fmt.Errorf("load shard %d replay: %w", shardID, err)
	}
	defer rows.Close()

	state := engine.NewState(shardID)
	receipts := engine.NewMemoryReceiptIndex()
	for rows.Next() {
		var receiptKind string
		var envelopeJSON []byte
		var storedDecisionHash []byte
		var storedStateHash []byte
		var storedDecisionHashVersion uint32
		var storedBusinessHashVersion *uint32
		var storedBusinessHash []byte
		if scanErr := rows.Scan(
			&receiptKind,
			&envelopeJSON,
			&storedDecisionHash,
			&storedStateHash,
			&storedDecisionHashVersion,
			&storedBusinessHashVersion,
			&storedBusinessHash,
		); scanErr != nil {
			return engine.State{}, fmt.Errorf("scan shard %d replay: %w", shardID, scanErr)
		}
		input, action, decodeErr := decodeEnvelope(envelopeJSON)
		if decodeErr != nil {
			return engine.State{}, decodeErr
		}
		var next engine.State
		var decision engine.Decision
		var applyErr error
		switch receiptKind {
		case "business":
			next, decision, applyErr =
				engine.ApplyTradingWithReceiptsAtDecisionHashVersion(
					state,
					input,
					action,
					receipts,
					storedDecisionHashVersion,
				)
		case "duplicate":
			original, found := receipts.LookupByInputID(input.InputID)
			if !found {
				return engine.State{}, fmt.Errorf(
					"%w: shard %d duplicate sequence %d has no business receipt",
					ErrCheckpointMismatch,
					shardID,
					input.StreamSequence,
				)
			}
			next, decision, applyErr =
				engine.ApplyDuplicateDeliveryAtDecisionHashVersion(
					state,
					input,
					original,
					storedDecisionHashVersion,
				)
		default:
			return engine.State{}, fmt.Errorf(
				"unknown durable receipt kind %q",
				receiptKind,
			)
		}
		if applyErr != nil {
			return engine.State{}, fmt.Errorf(
				"replay shard %d sequence %d: %w",
				shardID,
				input.StreamSequence,
				applyErr,
			)
		}
		if !equalHashBytes(decision.DecisionHash, storedDecisionHash) ||
			!equalHashBytes(next.Hash(), storedStateHash) {
			return engine.State{}, fmt.Errorf(
				"%w: shard %d sequence %d replay hash differs",
				ErrCheckpointMismatch,
				shardID,
				input.StreamSequence,
			)
		}
		if receiptKind == "business" {
			if storedBusinessHashVersion == nil ||
				len(storedBusinessHash) != len(engine.Hash{}) {
				return engine.State{}, fmt.Errorf(
					"%w: shard %d sequence %d has invalid business hash metadata",
					ErrCheckpointMismatch,
					shardID,
					input.StreamSequence,
				)
			}
			computedBusinessHash, hashErr := engine.BusinessInputHashAtVersion(
				input,
				*storedBusinessHashVersion,
			)
			if hashErr != nil ||
				string(storedBusinessHash) != string(computedBusinessHash[:]) {
				return engine.State{}, fmt.Errorf(
					"%w: shard %d sequence %d business hash differs",
					ErrCheckpointMismatch,
					shardID,
					input.StreamSequence,
				)
			}
			receipt := engine.NewReceipt(input, decision)
			receipt.BusinessHashVersion = *storedBusinessHashVersion
			copy(receipt.BusinessInputHash[:], storedBusinessHash)
			if recordErr := receipts.Record(receipt); recordErr != nil {
				return engine.State{}, fmt.Errorf("record replay receipt: %w", recordErr)
			}
		}
		state = next
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return engine.State{}, fmt.Errorf("iterate shard %d replay: %w", shardID, rowsErr)
	}
	rows.Close()
	state, err = replayCommittedFaults(ctx, querier, state, receipts)
	if err != nil {
		return engine.State{}, err
	}
	if err := verifyRecoveredCheckpoint(ctx, querier, state); err != nil {
		return engine.State{}, err
	}
	return state, nil
}

func replayCommittedFaults(
	ctx context.Context,
	querier postgresQuerier,
	state engine.State,
	receipts *engine.MemoryReceiptIndex,
) (engine.State, error) {
	rows, err := querier.Query(ctx, `
		SELECT envelope, supplied_action, error_kind, error_detail,
		       resulting_state_hash
		  FROM engine.shard_faults
		 WHERE shard_id = $1
		 ORDER BY committed_at, resulting_state_hash`,
		int64(state.ShardID()),
	)
	if err != nil {
		return engine.State{}, fmt.Errorf(
			"load shard %d committed faults: %w",
			state.ShardID(),
			err,
		)
	}
	defer rows.Close()
	for rows.Next() {
		var envelopeJSON []byte
		var suppliedAction []byte
		var errorKind string
		var errorDetail string
		var storedStateHash []byte
		if err := rows.Scan(
			&envelopeJSON,
			&suppliedAction,
			&errorKind,
			&errorDetail,
			&storedStateHash,
		); err != nil {
			return engine.State{}, fmt.Errorf(
				"scan shard %d committed fault: %w",
				state.ShardID(),
				err,
			)
		}
		input, _, err := decodeEnvelope(envelopeJSON)
		if err != nil {
			return engine.State{}, err
		}
		action, _, err := engine.DecodeTradingActionPayload(suppliedAction)
		if err != nil {
			return engine.State{}, fmt.Errorf(
				"decode shard %d supplied fault action: %w",
				state.ShardID(),
				err,
			)
		}
		var next engine.State
		var applyErr error
		if engine.ErrorKind(errorKind) == engine.ErrDurableInputConflict {
			next, applyErr = engine.FailClosed(
				state,
				input,
				engine.ErrDurableInputConflict,
				errorDetail,
			)
		} else {
			next, _, applyErr = engine.ApplyTradingWithReceipts(
				state,
				input,
				action,
				receipts,
			)
		}
		if applyErr == nil || !errors.Is(applyErr, engine.ErrorKind(errorKind)) {
			return engine.State{}, fmt.Errorf(
				"%w: shard %d fault replay returned %w, want %s",
				ErrCheckpointMismatch,
				state.ShardID(),
				applyErr,
				errorKind,
			)
		}
		if !equalHashBytes(next.Hash(), storedStateHash) {
			return engine.State{}, fmt.Errorf(
				"%w: shard %d fault replay hash differs",
				ErrCheckpointMismatch,
				state.ShardID(),
			)
		}
		state = next
	}
	if err := rows.Err(); err != nil {
		return engine.State{}, fmt.Errorf(
			"iterate shard %d committed faults: %w",
			state.ShardID(),
			err,
		)
	}
	return state, nil
}

func verifyRecoveredCheckpoint(
	ctx context.Context,
	querier postgresQuerier,
	state engine.State,
) error {
	var sequence uint64
	var ready bool
	var stateHash []byte
	err := querier.QueryRow(ctx, `
		SELECT next_stream_sequence, ready, state_hash
		  FROM engine.shard_checkpoints
		 WHERE shard_id = $1`,
		int64(state.ShardID()),
	).Scan(&sequence, &ready, &stateHash)
	if errors.Is(err, pgx.ErrNoRows) &&
		state.NextStreamSequence() == 1 &&
		state.Hash() == engine.NewState(state.ShardID()).Hash() {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify recovered shard %d: %w", state.ShardID(), err)
	}
	if sequence != state.NextStreamSequence() ||
		ready != state.Ready() ||
		!equalHashBytes(state.Hash(), stateHash) {
		return fmt.Errorf(
			"%w: recovered shard %d does not match PostgreSQL",
			ErrCheckpointMismatch,
			state.ShardID(),
		)
	}
	return nil
}

func hashBytes(hash engine.Hash) []byte {
	return append([]byte(nil), hash[:]...)
}

func equalHashBytes(hash engine.Hash, encoded []byte) bool {
	return len(encoded) == len(hash) &&
		string(hash[:]) == string(encoded)
}

func nullableID(id engine.ID) any {
	if id.IsZero() {
		return nil
	}
	return id.String()
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func decimalOrZero(value string) string {
	if value == "" {
		return "0"
	}
	return value
}

func nullableLogicalTime(present bool, value engine.LogicalTime) any {
	if !present {
		return nil
	}
	return value.UnixNano()
}
