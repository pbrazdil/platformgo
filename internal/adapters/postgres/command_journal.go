package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/engine"
)

var (
	ErrIdempotencyConflict       = application.ErrIdempotencyConflict
	ErrCommandNotFound           = application.ErrCommandNotFound
	ErrCommandCompletionConflict = application.ErrCommandCompletionConflict
	ErrCommandSequenceGap        = application.ErrCommandSequenceGap
	ErrAccountShardConflict      = application.ErrAccountShardConflict
	ErrEconomicRevisionChanged   = application.ErrEconomicRevisionChanged
	ErrRuntimeNotReady           = application.ErrRuntimeNotReady
)

const (
	commandAccountLockNamespace     = 0x5047434d
	commandIdempotencyLockNamespace = 0x50474944
)

type IdempotencyState = application.IdempotencyState

const (
	IdempotencyInProgress = application.IdempotencyInProgress
	IdempotencyCompleted  = application.IdempotencyCompleted
)

type CommandStatus = application.CommandStatus

const (
	CommandAccepted  = application.CommandAccepted
	CommandRejected  = application.CommandRejected
	CommandCompleted = application.CommandCompleted
)

type StoredResponse = application.StoredResponse
type BeginCommandRequest = application.BeginCommandRequest
type BeginCommandResult = application.BeginCommandResult
type CompleteCommandRequest = application.CompleteCommandRequest

// CommandJournal owns durable command and idempotency records.
type CommandJournal struct {
	pool *pgxpool.Pool
}

// NewCommandJournal binds a command journal to PostgreSQL.
func NewCommandJournal(pool *pgxpool.Pool) *CommandJournal {
	return &CommandJournal{pool: pool}
}

// NextAccountSequence returns the next observed durable sequence for an
// account. Begin remains the authority: concurrent callers must retry when it
// returns ErrCommandSequenceGap.
func (journal *CommandJournal) NextAccountSequence(
	ctx context.Context,
	accountID string,
) (uint64, error) {
	if journal == nil || journal.pool == nil {
		return 0, errors.New(
			"read account command sequence: PostgreSQL pool is required",
		)
	}
	if accountID == "" {
		return 0, errors.New("read account command sequence: account ID is required")
	}
	var next uint64
	if err := journal.pool.QueryRow(ctx, `
		SELECT COALESCE(max(account_sequence), 0) + 1
		  FROM trading.commands
		 WHERE account_id = $1`,
		accountID,
	).Scan(&next); err != nil {
		return 0, fmt.Errorf(
			"read account %q next command sequence: %w",
			accountID,
			err,
		)
	}
	return next, nil
}

// ConfigurationVersion returns the durable effective economic configuration
// revision. Begin revalidates it under row locks before committing a command.
func (journal *CommandJournal) ConfigurationVersion(
	ctx context.Context,
) (uint64, error) {
	if journal == nil || journal.pool == nil {
		return 0, errors.New(
			"read configuration version: PostgreSQL pool is required",
		)
	}
	var version uint64
	if err := journal.pool.QueryRow(ctx, `
		SELECT version
		  FROM engine.runtime_configuration
		 WHERE singleton`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read configuration version: %w", err)
	}
	return version, nil
}

// InstrumentVersion returns the immutable economic revision currently
// authoritative for a submitted instrument.
func (journal *CommandJournal) InstrumentVersion(
	ctx context.Context,
	instrumentID string,
) (uint64, error) {
	if journal == nil || journal.pool == nil {
		return 0, errors.New(
			"read instrument version: PostgreSQL pool is required",
		)
	}
	if instrumentID == "" {
		return 0, errors.New("read instrument version: instrument ID is required")
	}
	var revision uint64
	if err := journal.pool.QueryRow(ctx, `
		SELECT revision
		  FROM trading.instruments
		 WHERE instrument_id = $1`,
		instrumentID,
	).Scan(&revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf(
				"read instrument version: unknown instrument %q",
				instrumentID,
			)
		}
		return 0, fmt.Errorf(
			"read instrument version %q: %w",
			instrumentID,
			err,
		)
	}
	return revision, nil
}

// Replay returns an existing response without requiring command-path
// readiness. A changed request under the same scope/key still conflicts.
func (journal *CommandJournal) Replay(
	ctx context.Context,
	scope string,
	key string,
	requestHash [sha256.Size]byte,
) (BeginCommandResult, bool, error) {
	if journal == nil || journal.pool == nil {
		return BeginCommandResult{}, false, errors.New(
			"replay command: PostgreSQL pool is required",
		)
	}
	var result BeginCommandResult
	var commandIDText string
	var recordedHash []byte
	var state string
	var responseStatus *int32
	var responseHeaders []byte
	var responseBody []byte
	err := journal.pool.QueryRow(ctx, `
		SELECT
			idempotency.request_hash,
			idempotency.command_id::text,
			idempotency.state,
			COALESCE(idempotency.response_status, replay.response_status),
			COALESCE(idempotency.response_headers, replay.response_headers),
			COALESCE(idempotency.response_body, replay.response_body)
		  FROM trading.idempotency_records AS idempotency
		  LEFT JOIN trading.command_replay_responses AS replay
		    ON replay.command_id = idempotency.command_id
		 WHERE idempotency.scope = $1
		   AND idempotency.idempotency_key = $2`,
		scope,
		key,
	).Scan(
		&recordedHash,
		&commandIDText,
		&state,
		&responseStatus,
		&responseHeaders,
		&responseBody,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return BeginCommandResult{}, false, nil
	}
	if err != nil {
		return BeginCommandResult{}, false, fmt.Errorf(
			"load command replay: %w",
			err,
		)
	}
	if !bytes.Equal(recordedHash, requestHash[:]) {
		return BeginCommandResult{}, true, ErrIdempotencyConflict
	}
	commandID, err := engine.ParseID(commandIDText)
	if err != nil {
		return BeginCommandResult{}, true, fmt.Errorf(
			"parse replay command ID: %w",
			err,
		)
	}
	result.CommandID = commandID
	result.State = IdempotencyState(state)
	if responseStatus != nil {
		result.Response.Status = int(*responseStatus)
		result.Response.Headers, err = canonicalJSON(responseHeaders)
		if err != nil {
			return BeginCommandResult{}, true, fmt.Errorf(
				"decode replay response headers: %w",
				err,
			)
		}
		result.Response.Body = append([]byte(nil), responseBody...)
	}
	return result, true, nil
}

// Begin inserts a key and command atomically or returns the existing identical
// request. A reused key with a different canonical request hash is rejected.
func (journal *CommandJournal) Begin(
	ctx context.Context,
	request BeginCommandRequest,
) (BeginCommandResult, error) {
	if journal == nil || journal.pool == nil {
		return BeginCommandResult{}, errors.New(
			"begin command: PostgreSQL pool is required",
		)
	}
	input, err := validateBeginCommand(request)
	if err != nil {
		return BeginCommandResult{}, err
	}
	tx, err := journal.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return BeginCommandResult{}, fmt.Errorf("begin command transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	if request.RequireRuntimeReady {
		if _, lockErr := tx.Exec(
			ctx,
			"SELECT pg_advisory_xact_lock_shared($1, $2)",
			commandAdmissionGateLockNamespace,
			int64(0),
		); lockErr != nil {
			return BeginCommandResult{}, fmt.Errorf(
				"acquire command admission gate: %w",
				lockErr,
			)
		}
	}
	if _, lockErr := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock($1, hashtext($2))",
		commandIdempotencyLockNamespace,
		request.Scope+"\x1f"+request.IdempotencyKey,
	); lockErr != nil {
		return BeginCommandResult{}, fmt.Errorf(
			"lock command idempotency key: %w",
			lockErr,
		)
	}
	replay, recordedHash, replayErr := loadIdempotency(
		ctx,
		tx,
		request.Scope,
		request.IdempotencyKey,
	)
	if replayErr == nil {
		if !bytes.Equal(recordedHash, request.RequestHash[:]) {
			return BeginCommandResult{}, ErrIdempotencyConflict
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return BeginCommandResult{}, fmt.Errorf(
				"commit command replay before readiness: %w",
				commitErr,
			)
		}
		return replay, nil
	}
	if !errors.Is(replayErr, pgx.ErrNoRows) {
		return BeginCommandResult{}, replayErr
	}
	if request.RequireRuntimeReady {
		if readyErr := runtimeCommandReady(
			ctx,
			tx,
			input.ShardID,
		); readyErr != nil {
			return BeginCommandResult{}, readyErr
		}
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES ($1,$2,$3,$4,'in_progress',$5)
		ON CONFLICT (scope, idempotency_key) DO NOTHING`,
		request.Scope,
		request.IdempotencyKey,
		request.RequestHash[:],
		request.CommandID.String(),
		request.ExpiresAt,
	)
	if err != nil {
		return BeginCommandResult{}, fmt.Errorf("insert idempotency record: %w", err)
	}
	if tag.RowsAffected() == 1 {
		if _, lockErr := tx.Exec(
			ctx,
			"SELECT pg_advisory_xact_lock($1, hashtext($2))",
			commandAccountLockNamespace,
			request.AccountID,
		); lockErr != nil {
			return BeginCommandResult{}, fmt.Errorf(
				"lock account %q command sequence: %w",
				request.AccountID,
				lockErr,
			)
		}
		if input.Kind == engine.InputKindCommand &&
			request.CommandType == string(engine.TradingActionSubmitOrder) {
			_, action, decodeErr := engine.DecodeInputMessage(
				request.OutboxPayload,
			)
			if decodeErr != nil || action.SubmitOrder == nil {
				return BeginCommandResult{}, fmt.Errorf(
					"%w: submit-order action is unavailable",
					ErrCommandInputConflict,
				)
			}
			var configurationVersion uint64
			var instrumentVersion uint64
			if revisionErr := tx.QueryRow(ctx, `
				SELECT
					configuration_version,
					instrument_version
				  FROM engine.lock_command_economic_revisions($1)`,
				action.SubmitOrder.InstrumentID,
			).Scan(
				&configurationVersion,
				&instrumentVersion,
			); revisionErr != nil {
				return BeginCommandResult{}, fmt.Errorf(
					"lock economic revisions: %w",
					revisionErr,
				)
			}
			if configurationVersion != input.ConfigurationVersion ||
				instrumentVersion != input.InstrumentVersion {
				return BeginCommandResult{}, ErrEconomicRevisionChanged
			}
		}
		if shardErr := ensureDeploymentShard(
			ctx,
			tx,
			input.ShardID,
		); shardErr != nil {
			return BeginCommandResult{}, shardErr
		}
		if bindErr := bindAccountShard(
			ctx,
			tx,
			request.AccountID,
			input.ShardID,
		); bindErr != nil {
			return BeginCommandResult{}, bindErr
		}
		var lastSequence uint64
		if sequenceErr := tx.QueryRow(ctx, `
			SELECT COALESCE(max(account_sequence), 0)
			  FROM trading.commands
			 WHERE account_id = $1`,
			request.AccountID,
		).Scan(&lastSequence); sequenceErr != nil {
			return BeginCommandResult{}, fmt.Errorf(
				"read account %q command sequence: %w",
				request.AccountID,
				sequenceErr,
			)
		}
		expectedSequence := lastSequence + 1
		if request.AccountSequence != expectedSequence {
			return BeginCommandResult{}, fmt.Errorf(
				"%w: account %q got %d, want %d",
				ErrCommandSequenceGap,
				request.AccountID,
				request.AccountSequence,
				expectedSequence,
			)
		}
		if _, insertErr := tx.Exec(ctx, `
			INSERT INTO trading.commands (
				command_id, account_id, account_sequence, command_type,
				schema_version, canonical_payload, status, logical_time,
				market_sequence_binding
			) VALUES ($1,$2,$3,$4,$5,$6,'pending',$7,'ordered')`,
			request.CommandID.String(),
			request.AccountID,
			request.AccountSequence,
			request.CommandType,
			request.SchemaVersion,
			request.CanonicalPayload,
			request.LogicalTime.UnixNano(),
		); insertErr != nil {
			return BeginCommandResult{}, fmt.Errorf("insert durable command: %w", insertErr)
		}
		if request.Response.Status != 0 {
			headers, headerErr := canonicalJSON(request.Response.Headers)
			if headerErr != nil {
				return BeginCommandResult{}, fmt.Errorf(
					"insert command replay response headers: %w",
					headerErr,
				)
			}
			if _, insertErr := tx.Exec(ctx, `
				INSERT INTO trading.command_replay_responses (
					command_id,
					response_status,
					response_headers,
					response_body
				) VALUES ($1,$2,$3,$4)`,
				request.CommandID.String(),
				request.Response.Status,
				headers,
				request.Response.Body,
			); insertErr != nil {
				return BeginCommandResult{}, fmt.Errorf(
					"insert command replay response: %w",
					insertErr,
				)
			}
		}
		if provisioning := request.AccountProvisioning; provisioning != nil {
			if _, insertErr := tx.Exec(ctx, `
				INSERT INTO identity.account_provisioning_intents (
					command_id,
					account_id,
					broker_subject,
					user_id,
					login,
					base_currency,
					market_venue,
					permitted_classes,
					created_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				request.CommandID.String(),
				request.AccountID,
				provisioning.BrokerTenant,
				provisioning.UserID,
				provisioning.Login,
				provisioning.BaseCurrency,
				provisioning.MarketVenue,
				provisioning.PermittedClasses,
				provisioning.CreatedAt,
			); insertErr != nil {
				return BeginCommandResult{}, fmt.Errorf(
					"insert account provisioning intent: %w",
					insertErr,
				)
			}
		}
		if !request.OrderID.IsZero() {
			if _, insertErr := tx.Exec(ctx, `
				INSERT INTO trading.order_intents (
					order_id, command_id, account_id, intent_id
				) VALUES ($1,$2,$3,$4)`,
				request.OrderID.String(),
				request.CommandID.String(),
				request.AccountID,
				request.IntentID,
			); insertErr != nil {
				return BeginCommandResult{}, fmt.Errorf(
					"insert durable order intent: %w",
					insertErr,
				)
			}
		}
		if _, insertErr := tx.Exec(ctx, `
			INSERT INTO messaging.outbox (
				message_id, subject, schema_version, payload
			) VALUES ($1,$2,$3,$4)`,
			request.CommandID.String(),
			request.OutboxSubject,
			request.SchemaVersion,
			request.OutboxPayload,
		); insertErr != nil {
			return BeginCommandResult{}, fmt.Errorf("insert command outbox: %w", insertErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return BeginCommandResult{}, fmt.Errorf("commit new command: %w", commitErr)
		}
		return BeginCommandResult{
			Created:   true,
			CommandID: request.CommandID,
			State:     IdempotencyInProgress,
			Response:  request.Response,
		}, nil
	}

	result, recordedHash, err := loadIdempotency(
		ctx,
		tx,
		request.Scope,
		request.IdempotencyKey,
	)
	if err != nil {
		return BeginCommandResult{}, err
	}
	if !bytes.Equal(recordedHash, request.RequestHash[:]) {
		return BeginCommandResult{}, ErrIdempotencyConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return BeginCommandResult{}, fmt.Errorf("commit command replay: %w", err)
	}
	return result, nil
}

func validateBeginCommand(
	request BeginCommandRequest,
) (engine.InputEnvelope, error) {
	switch {
	case request.Scope == "":
		return engine.InputEnvelope{}, errors.New("begin command: scope is required")
	case request.IdempotencyKey == "":
		return engine.InputEnvelope{}, errors.New(
			"begin command: idempotency key is required",
		)
	case request.CommandID.IsZero():
		return engine.InputEnvelope{}, errors.New("begin command: command ID is required")
	case request.OrderID.IsZero() != (request.IntentID == ""):
		return engine.InputEnvelope{}, errors.New(
			"begin command: order ID and intent ID must be supplied together",
		)
	case request.AccountID == "":
		return engine.InputEnvelope{}, errors.New("begin command: account ID is required")
	case request.AccountSequence == 0:
		return engine.InputEnvelope{}, errors.New(
			"begin command: account sequence is required",
		)
	case request.CommandType == "":
		return engine.InputEnvelope{}, errors.New("begin command: command type is required")
	case request.SchemaVersion == 0:
		return engine.InputEnvelope{}, errors.New(
			"begin command: schema version is required",
		)
	case !json.Valid(request.CanonicalPayload):
		return engine.InputEnvelope{}, errors.New(
			"begin command: canonical payload is not JSON",
		)
	case request.OutboxSubject == "":
		return engine.InputEnvelope{}, errors.New(
			"begin command: outbox subject is required",
		)
	case !json.Valid(request.OutboxPayload):
		return engine.InputEnvelope{}, errors.New(
			"begin command: outbox payload is not JSON",
		)
	case request.ExpiresAt.IsZero():
		return engine.InputEnvelope{}, errors.New("begin command: expiration is required")
	case request.Response.Status == 0 &&
		(len(request.Response.Headers) != 0 ||
			len(request.Response.Body) != 0):
		return engine.InputEnvelope{}, errors.New(
			"begin command: replay response status is required",
		)
	case request.Response.Status != 0 &&
		(request.Response.Status < 100 ||
			request.Response.Status > 599 ||
			!json.Valid(request.Response.Headers)):
		return engine.InputEnvelope{}, errors.New(
			"begin command: replay response is invalid",
		)
	case request.AccountProvisioning != nil &&
		(request.CommandType != string(engine.TradingActionConfigureAccount) ||
			request.Response.Status != 201 ||
			request.AccountProvisioning.BrokerTenant == "" ||
			request.AccountProvisioning.UserID == "" ||
			request.AccountProvisioning.Login <= 0 ||
			request.AccountProvisioning.BaseCurrency != "USDC" ||
			request.AccountProvisioning.MarketVenue != "HYPERLIQUID" ||
			len(request.AccountProvisioning.PermittedClasses) != 1 ||
			request.AccountProvisioning.PermittedClasses[0] !=
				"CRYPTOCURRENCY" ||
			request.AccountProvisioning.CreatedAt.IsZero()):
		return engine.InputEnvelope{}, errors.New(
			"begin command: account provisioning intent is invalid",
		)
	}
	input, action, err := engine.DecodeInputMessage(request.OutboxPayload)
	if err != nil {
		return engine.InputEnvelope{}, fmt.Errorf(
			"begin command: decode outbox envelope: %w",
			err,
		)
	}
	expectedSubject := fmt.Sprintf(
		"engine.input.%d.command.v%d",
		input.ShardID,
		input.SchemaVersion,
	)
	canonicalCommand, err := canonicalJSON(request.CanonicalPayload)
	if err != nil {
		return engine.InputEnvelope{}, fmt.Errorf(
			"begin command: canonicalize command payload: %w",
			err,
		)
	}
	canonicalInput, err := canonicalJSON(input.Payload.Bytes())
	if err != nil {
		return engine.InputEnvelope{}, fmt.Errorf(
			"begin command: canonicalize outbox payload: %w",
			err,
		)
	}
	if input.InputID != request.CommandID ||
		input.Kind != engine.InputKindCommand ||
		input.MarketSequence != 0 ||
		!engine.TradingActionAllowedForInputKind(input.Kind, action.Kind) ||
		input.SchemaVersion != request.SchemaVersion ||
		input.SourceSequence != request.AccountSequence ||
		input.LogicalTime.UnixNano() != request.LogicalTime.UnixNano() ||
		request.OutboxSubject != expectedSubject ||
		string(action.Kind) != request.CommandType ||
		!bytes.Equal(canonicalCommand, canonicalInput) {
		return engine.InputEnvelope{}, fmt.Errorf(
			"%w: begin command %s requires an unresolved market sequence and matching redundant metadata",
			ErrCommandInputConflict,
			request.CommandID,
		)
	}
	if actionAccountID, scoped := engine.TradingActionAccountID(action); scoped &&
		actionAccountID != request.AccountID {
		return engine.InputEnvelope{}, fmt.Errorf(
			"%w: begin command %s account lane %q differs from payload account %q",
			ErrCommandInputConflict,
			request.CommandID,
			request.AccountID,
			actionAccountID,
		)
	}
	return input, nil
}

func bindAccountShard(
	ctx context.Context,
	tx pgx.Tx,
	accountID string,
	shardID engine.ShardID,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ($1, $2)
		ON CONFLICT (account_id) DO NOTHING`,
		accountID,
		int64(shardID),
	); err != nil {
		return fmt.Errorf("bind account %q to shard %d: %w", accountID, shardID, err)
	}
	var assignedShardID int64
	if err := tx.QueryRow(ctx, `
		SELECT shard_id
		  FROM engine.account_shards
		 WHERE account_id = $1`,
		accountID,
	).Scan(&assignedShardID); err != nil {
		return fmt.Errorf(
			"read account %q shard assignment: %w",
			accountID,
			err,
		)
	}
	if assignedShardID != int64(shardID) {
		return fmt.Errorf(
			"%w: account %q is assigned to shard %d, got %d",
			ErrAccountShardConflict,
			accountID,
			assignedShardID,
			shardID,
		)
	}
	return nil
}

func requireAccountShard(
	ctx context.Context,
	tx pgx.Tx,
	accountID string,
	shardID engine.ShardID,
) error {
	var assignedShardID int64
	err := tx.QueryRow(ctx, `
		SELECT shard_id
		  FROM engine.account_shards
		 WHERE account_id = $1`,
		accountID,
	).Scan(&assignedShardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf(
			"%w: account %q has no durable shard assignment",
			ErrAccountShardConflict,
			accountID,
		)
	}
	if err != nil {
		return fmt.Errorf(
			"read account %q shard assignment: %w",
			accountID,
			err,
		)
	}
	if assignedShardID != int64(shardID) {
		return fmt.Errorf(
			"%w: account %q is assigned to shard %d, got %d",
			ErrAccountShardConflict,
			accountID,
			assignedShardID,
			shardID,
		)
	}
	return nil
}

func loadIdempotency(
	ctx context.Context,
	tx pgx.Tx,
	scope string,
	key string,
) (BeginCommandResult, []byte, error) {
	var result BeginCommandResult
	var commandIDText string
	var requestHash []byte
	var state string
	var responseStatus *int32
	var responseHeaders []byte
	var responseBody []byte
	if err := tx.QueryRow(ctx, `
		SELECT
			idempotency.request_hash,
			idempotency.command_id::text,
			idempotency.state,
			COALESCE(idempotency.response_status, replay.response_status),
			COALESCE(idempotency.response_headers, replay.response_headers),
			COALESCE(idempotency.response_body, replay.response_body)
		  FROM trading.idempotency_records AS idempotency
		  LEFT JOIN trading.command_replay_responses AS replay
		    ON replay.command_id = idempotency.command_id
		 WHERE idempotency.scope = $1
		   AND idempotency.idempotency_key = $2`,
		scope,
		key,
	).Scan(
		&requestHash,
		&commandIDText,
		&state,
		&responseStatus,
		&responseHeaders,
		&responseBody,
	); err != nil {
		return BeginCommandResult{}, nil, fmt.Errorf(
			"load idempotency record: %w",
			err,
		)
	}
	commandID, err := engine.ParseID(commandIDText)
	if err != nil {
		return BeginCommandResult{}, nil, fmt.Errorf(
			"parse idempotency command ID: %w",
			err,
		)
	}
	result.CommandID = commandID
	result.State = IdempotencyState(state)
	if responseStatus != nil {
		result.Response.Status = int(*responseStatus)
		result.Response.Headers, err = canonicalJSON(responseHeaders)
		if err != nil {
			return BeginCommandResult{}, nil, fmt.Errorf(
				"decode stored response headers: %w",
				err,
			)
		}
		result.Response.Body = append([]byte(nil), responseBody...)
	}
	return result, requestHash, nil
}

// Complete atomically writes a terminal command result and replay response.
// Repeating the exact completion is harmless; a different one is rejected.
func (journal *CommandJournal) Complete(
	ctx context.Context,
	request CompleteCommandRequest,
) error {
	if journal == nil || journal.pool == nil {
		return errors.New("complete command: PostgreSQL pool is required")
	}
	if err := validateCompletion(request); err != nil {
		return err
	}
	headers, err := canonicalJSON(request.Response.Headers)
	if err != nil {
		return fmt.Errorf("complete command response headers: %w", err)
	}
	result, err := canonicalJSON(request.Result)
	if err != nil {
		return fmt.Errorf("complete command result: %w", err)
	}

	tx, err := journal.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.Serializable,
	})
	if err != nil {
		return fmt.Errorf("complete command transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	var currentState string
	var currentStatus *int32
	var currentHeaders []byte
	var currentBody []byte
	err = tx.QueryRow(ctx, `
		SELECT state, response_status, response_headers, response_body
		  FROM trading.idempotency_records
		 WHERE command_id = $1
		 FOR UPDATE`,
		request.CommandID.String(),
	).Scan(&currentState, &currentStatus, &currentHeaders, &currentBody)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCommandNotFound
	}
	if err != nil {
		return fmt.Errorf("load command completion: %w", err)
	}
	if IdempotencyState(currentState) == IdempotencyCompleted {
		canonicalCurrentHeaders, headerErr := canonicalJSON(currentHeaders)
		if headerErr != nil {
			return fmt.Errorf("decode completed response headers: %w", headerErr)
		}
		if currentStatus != nil &&
			int(*currentStatus) == request.Response.Status &&
			bytes.Equal(canonicalCurrentHeaders, headers) &&
			bytes.Equal(currentBody, request.Response.Body) {
			return nil
		}
		return ErrCommandCompletionConflict
	}

	var commandState string
	var currentResult []byte
	if commandErr := tx.QueryRow(ctx, `
		SELECT status, result
		  FROM trading.commands
		 WHERE command_id = $1
		 FOR UPDATE`,
		request.CommandID.String(),
	).Scan(&commandState, &currentResult); commandErr != nil {
		return fmt.Errorf("load durable command completion: %w", commandErr)
	}
	switch commandState {
	case "pending":
		return ErrCommandCompletionConflict
	case string(request.Status):
		canonicalCurrentResult, canonicalErr := canonicalJSON(currentResult)
		if canonicalErr != nil {
			return fmt.Errorf("decode durable command result: %w", canonicalErr)
		}
		if !bytes.Equal(canonicalCurrentResult, result) {
			return ErrCommandCompletionConflict
		}
	default:
		return ErrCommandCompletionConflict
	}
	tag, err := tx.Exec(ctx, `
		UPDATE trading.idempotency_records
		   SET state = 'completed',
		       response_status = $2,
		       response_headers = $3,
		       response_body = $4
		 WHERE command_id = $1 AND state = 'in_progress'`,
		request.CommandID.String(),
		request.Response.Status,
		headers,
		request.Response.Body,
	)
	if err != nil {
		return fmt.Errorf("complete idempotency response: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrCommandCompletionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit command completion: %w", err)
	}
	return nil
}

func validateCompletion(request CompleteCommandRequest) error {
	switch request.Status {
	case CommandAccepted, CommandRejected, CommandCompleted:
	default:
		return fmt.Errorf("complete command: invalid status %q", request.Status)
	}
	if request.CommandID.IsZero() {
		return errors.New("complete command: command ID is required")
	}
	if request.Response.Status < 100 || request.Response.Status > 599 {
		return errors.New("complete command: response status is invalid")
	}
	if !json.Valid(request.Result) {
		return errors.New("complete command: result is not JSON")
	}
	if !json.Valid(request.Response.Headers) {
		return errors.New("complete command: response headers are not JSON")
	}
	return nil
}

func canonicalJSON(encoded []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
