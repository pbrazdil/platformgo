package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/engine"
)

var (
	// ErrIdempotencyConflict means a key was reused for different request bytes.
	ErrIdempotencyConflict = errors.New("idempotency key request hash conflict")
	// ErrCommandNotFound means completion referenced no durable command.
	ErrCommandNotFound = errors.New("durable command not found")
	// ErrCommandCompletionConflict means a completed command was given a
	// different terminal result.
	ErrCommandCompletionConflict = errors.New("command completion conflict")
	// ErrCommandSequenceGap means a new command did not use the next contiguous
	// sequence for its account.
	ErrCommandSequenceGap = errors.New("command account sequence gap")
)

const commandAccountLockNamespace = 0x5047434d

// IdempotencyState is the durable lifecycle of a request key.
type IdempotencyState string

const (
	IdempotencyInProgress IdempotencyState = "in_progress"
	IdempotencyCompleted  IdempotencyState = "completed"
)

// CommandStatus is the durable command outcome.
type CommandStatus string

const (
	CommandAccepted  CommandStatus = "accepted"
	CommandRejected  CommandStatus = "rejected"
	CommandCompleted CommandStatus = "completed"
)

// StoredResponse is the exact replayable response body plus canonical headers.
type StoredResponse struct {
	Status  int
	Headers []byte
	Body    []byte
}

// BeginCommandRequest contains one canonical command and its stable identity.
type BeginCommandRequest struct {
	Scope            string
	IdempotencyKey   string
	RequestHash      [32]byte
	CommandID        engine.ID
	AccountID        string
	AccountSequence  uint64
	CommandType      string
	SchemaVersion    uint32
	CanonicalPayload []byte
	OutboxSubject    string
	OutboxPayload    []byte
	LogicalTime      time.Time
	ExpiresAt        time.Time
}

// BeginCommandResult reports either a newly-created command or a replay.
type BeginCommandResult struct {
	Created   bool
	CommandID engine.ID
	State     IdempotencyState
	Response  StoredResponse
}

// CompleteCommandRequest atomically finalizes the command and replay record.
type CompleteCommandRequest struct {
	CommandID engine.ID
	Status    CommandStatus
	Result    []byte
	Response  StoredResponse
}

// CommandJournal owns durable command and idempotency records.
type CommandJournal struct {
	pool *pgxpool.Pool
}

// NewCommandJournal binds a command journal to PostgreSQL.
func NewCommandJournal(pool *pgxpool.Pool) *CommandJournal {
	return &CommandJournal{pool: pool}
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
	if err := validateBeginCommand(request); err != nil {
		return BeginCommandResult{}, err
	}
	tx, err := journal.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.Serializable,
	})
	if err != nil {
		return BeginCommandResult{}, fmt.Errorf("begin command transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

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
				schema_version, canonical_payload, status, logical_time
			) VALUES ($1,$2,$3,$4,$5,$6,'pending',$7)`,
			request.CommandID.String(),
			request.AccountID,
			request.AccountSequence,
			request.CommandType,
			request.SchemaVersion,
			request.CanonicalPayload,
			request.LogicalTime,
		); insertErr != nil {
			return BeginCommandResult{}, fmt.Errorf("insert durable command: %w", insertErr)
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
		}, nil
	}

	result, recordedHash, err := loadIdempotencyForUpdate(
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

func validateBeginCommand(request BeginCommandRequest) error {
	switch {
	case request.Scope == "":
		return errors.New("begin command: scope is required")
	case request.IdempotencyKey == "":
		return errors.New("begin command: idempotency key is required")
	case request.CommandID.IsZero():
		return errors.New("begin command: command ID is required")
	case request.AccountID == "":
		return errors.New("begin command: account ID is required")
	case request.AccountSequence == 0:
		return errors.New("begin command: account sequence is required")
	case request.CommandType == "":
		return errors.New("begin command: command type is required")
	case request.SchemaVersion == 0:
		return errors.New("begin command: schema version is required")
	case !json.Valid(request.CanonicalPayload):
		return errors.New("begin command: canonical payload is not JSON")
	case request.OutboxSubject == "":
		return errors.New("begin command: outbox subject is required")
	case !json.Valid(request.OutboxPayload):
		return errors.New("begin command: outbox payload is not JSON")
	case request.ExpiresAt.IsZero():
		return errors.New("begin command: expiration is required")
	}
	input, action, err := engine.DecodeInputMessage(request.OutboxPayload)
	if err != nil {
		return fmt.Errorf("begin command: decode outbox envelope: %w", err)
	}
	expectedSubject := fmt.Sprintf(
		"engine.input.%d.command.v%d",
		input.ShardID,
		input.SchemaVersion,
	)
	canonicalCommand, err := canonicalJSON(request.CanonicalPayload)
	if err != nil {
		return fmt.Errorf("begin command: canonicalize command payload: %w", err)
	}
	canonicalInput, err := canonicalJSON(input.Payload.Bytes())
	if err != nil {
		return fmt.Errorf("begin command: canonicalize outbox payload: %w", err)
	}
	if input.InputID != request.CommandID ||
		input.Kind != engine.InputKindCommand ||
		!engine.TradingActionAllowedForInputKind(input.Kind, action.Kind) ||
		input.SchemaVersion != request.SchemaVersion ||
		input.SourceSequence != request.AccountSequence ||
		input.LogicalTime.UnixNano() != request.LogicalTime.UnixNano() ||
		request.OutboxSubject != expectedSubject ||
		string(action.Kind) != request.CommandType ||
		!bytes.Equal(canonicalCommand, canonicalInput) {
		return fmt.Errorf(
			"%w: begin command %s redundant metadata differs from outbox",
			ErrCommandInputConflict,
			request.CommandID,
		)
	}
	if actionAccountID, scoped := engine.TradingActionAccountID(action); scoped &&
		actionAccountID != request.AccountID {
		return fmt.Errorf(
			"%w: begin command %s account lane %q differs from payload account %q",
			ErrCommandInputConflict,
			request.CommandID,
			request.AccountID,
			actionAccountID,
		)
	}
	return nil
}

func loadIdempotencyForUpdate(
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
		SELECT request_hash, command_id::text, state, response_status,
		       response_headers, response_body
		  FROM trading.idempotency_records
		 WHERE scope = $1 AND idempotency_key = $2
		 FOR UPDATE`,
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
