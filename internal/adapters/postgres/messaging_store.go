package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/engine"
)

// OutboxMessage is one claimed immutable publication.
type OutboxMessage struct {
	MessageID           engine.ID
	Subject             string
	SchemaVersion       uint32
	Payload             []byte
	Attempts            uint32
	orderedCommandClaim [sha256.Size]byte
	engineEventClaim    [sha256.Size]byte
}

// HasEngineEventClaim reports whether PostgreSQL admitted this domain event
// through the engine-only producer class.
func (message OutboxMessage) HasEngineEventClaim() bool {
	return message.engineEventClaim != [sha256.Size]byte{} &&
		message.engineEventClaim == engineEventPublicationFingerprint(message)
}

// HasOrderedCommandClaim reports whether PostgreSQL admitted this command
// through the account-ordered outbox claim boundary.
func (message OutboxMessage) HasOrderedCommandClaim() bool {
	return message.orderedCommandClaim != [sha256.Size]byte{} &&
		message.orderedCommandClaim == commandPublicationFingerprint(message)
}

func commandPublicationFingerprint(message OutboxMessage) [sha256.Size]byte {
	return publicationFingerprint(
		"platformgo.postgres.ordered-command-publication.v1",
		message,
	)
}

func engineEventPublicationFingerprint(message OutboxMessage) [sha256.Size]byte {
	return publicationFingerprint(
		"platformgo.postgres.engine-event-publication.v1",
		message,
	)
}

func engineEventAuthorityMatches(
	message OutboxMessage,
	producerClass string,
	engineShardID *int64,
	engineInputID *string,
	receiptExists bool,
) bool {
	if producerClass != "engine" ||
		engineShardID == nil ||
		engineInputID == nil ||
		!receiptExists ||
		!strings.HasPrefix(message.Subject, "domain.v1.") {
		return false
	}
	var envelope struct {
		MessageID     string `json:"messageId"`
		CorrelationID string `json:"correlationId"`
	}
	if err := json.Unmarshal(message.Payload, &envelope); err != nil {
		return false
	}
	return envelope.MessageID == message.MessageID.String() &&
		envelope.CorrelationID == *engineInputID
}

func commandAuthorityMatches(
	message OutboxMessage,
	producerClass string,
	commandID string,
	accountID string,
	accountSequence uint64,
	commandType string,
	commandSchema uint32,
	canonicalAction []byte,
	logicalTime int64,
) bool {
	if producerClass != "api" ||
		commandID != message.MessageID.String() ||
		commandSchema != message.SchemaVersion {
		return false
	}
	input, action, err := engine.DecodeInputMessage(message.Payload)
	if err != nil {
		return false
	}
	expectedSubject := fmt.Sprintf(
		"engine.input.%d.command.v%d",
		input.ShardID,
		input.SchemaVersion,
	)
	canonicalStored, err := canonicalJSON(canonicalAction)
	if err != nil {
		return false
	}
	canonicalInput, err := canonicalJSON(input.Payload.Bytes())
	if err != nil {
		return false
	}
	actionAccountID, scoped := engine.TradingActionAccountID(action)
	return input.InputID == message.MessageID &&
		input.Kind == engine.InputKindCommand &&
		engine.TradingActionAllowedForInputKind(input.Kind, action.Kind) &&
		input.SchemaVersion == commandSchema &&
		input.SourceSequence == accountSequence &&
		int64(input.LogicalTime) == logicalTime &&
		string(action.Kind) == commandType &&
		(!scoped || actionAccountID == accountID) &&
		message.Subject == expectedSubject &&
		bytes.Equal(canonicalStored, canonicalInput)
}

func publicationFingerprint(label string, message OutboxMessage) [sha256.Size]byte {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(label))
	_, _ = hasher.Write(message.MessageID[:])
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(len(message.Subject)))
	_, _ = hasher.Write(encoded[:])
	_, _ = hasher.Write([]byte(message.Subject))
	binary.BigEndian.PutUint32(encoded[:4], message.SchemaVersion)
	_, _ = hasher.Write(encoded[:4])
	binary.BigEndian.PutUint64(encoded[:], uint64(len(message.Payload)))
	_, _ = hasher.Write(encoded[:])
	_, _ = hasher.Write(message.Payload)
	var result [sha256.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

// DurablePublisher waits for a transport durability acknowledgment and returns
// its positive stream sequence. It must use MessageID as the deduplication ID.
type DurablePublisher interface {
	Publish(context.Context, OutboxMessage) (uint64, error)
}

// InboxEffect performs one consumer side effect inside the inbox transaction.
type InboxEffect func(context.Context, pgx.Tx) error

// MessagingStore owns PostgreSQL outbox claims and transactional inbox dedupe.
type MessagingStore struct {
	pool *pgxpool.Pool
}

// NewMessagingStore binds the messaging commit boundaries to PostgreSQL.
func NewMessagingStore(pool *pgxpool.Pool) *MessagingStore {
	return &MessagingStore{pool: pool}
}

// PublishOutboxBatch claims a bounded batch, commits the claim, performs every
// network publish outside PostgreSQL transactions, then records acknowledgments.
func (store *MessagingStore) PublishOutboxBatch(
	ctx context.Context,
	publisher DurablePublisher,
	now time.Time,
	limit int,
	claimLease time.Duration,
	retryDelay time.Duration,
) (int, error) {
	if store == nil || store.pool == nil {
		return 0, errors.New("publish outbox: PostgreSQL pool is required")
	}
	if publisher == nil {
		return 0, errors.New("publish outbox: durable publisher is required")
	}
	if limit <= 0 {
		return 0, errors.New("publish outbox: positive batch limit is required")
	}
	if claimLease <= 0 || retryDelay < 0 {
		return 0, errors.New("publish outbox: invalid lease or retry delay")
	}

	messages, err := store.claimOutbox(ctx, now, limit, claimLease)
	if err != nil {
		return 0, err
	}
	published := 0
	var firstErr error
	for _, message := range messages {
		sequence, publishErr := publisher.Publish(ctx, message)
		if publishErr != nil {
			if markErr := store.markOutboxFailed(
				ctx,
				message.MessageID,
				now.Add(retryDelay),
				publishErr,
			); markErr != nil && firstErr == nil {
				firstErr = markErr
			}
			if firstErr == nil {
				firstErr = publishErr
			}
			continue
		}
		if sequence == 0 {
			ackErr := errors.New("publish outbox: transport returned zero stream sequence")
			if markErr := store.markOutboxFailed(
				ctx,
				message.MessageID,
				now.Add(retryDelay),
				ackErr,
			); markErr != nil && firstErr == nil {
				firstErr = markErr
			}
			if firstErr == nil {
				firstErr = ackErr
			}
			continue
		}
		if markErr := store.markOutboxPublished(
			ctx,
			message.MessageID,
			sequence,
			now,
		); markErr != nil {
			if firstErr == nil {
				firstErr = markErr
			}
			continue
		}
		published++
	}
	return published, firstErr
}

// RepublishOutbox retries an already acknowledged immutable outbox message.
// A command can reach this path only after its first account-ordered publish
// established its JetStream position.
func (store *MessagingStore) RepublishOutbox(
	ctx context.Context,
	publisher DurablePublisher,
	messageID engine.ID,
) (uint64, error) {
	if store == nil || store.pool == nil {
		return 0, errors.New("republish outbox: PostgreSQL pool is required")
	}
	if publisher == nil {
		return 0, errors.New("republish outbox: durable publisher is required")
	}
	if messageID.IsZero() {
		return 0, errors.New("republish outbox: message ID is required")
	}
	var message OutboxMessage
	var messageIDText string
	var producerClass string
	var commandID string
	var accountID string
	var accountSequence uint64
	var commandType string
	var commandSchema uint32
	var canonicalAction []byte
	var commandLogicalTime int64
	var engineShardID *int64
	var engineInputID *string
	var receiptExists bool
	if err := store.pool.QueryRow(ctx, `
		SELECT outbox.message_id::text, outbox.subject,
		       outbox.schema_version, outbox.payload, outbox.attempts,
		       outbox.producer_class,
		       COALESCE(command.command_id::text, ''),
		       COALESCE(command.account_id, ''),
		       COALESCE(command.account_sequence, 0),
		       COALESCE(command.command_type, ''),
		       COALESCE(command.schema_version, 0),
		       COALESCE(command.canonical_payload, 'null'::jsonb),
		       COALESCE(command.logical_time, 0),
		       outbox.engine_shard_id, outbox.engine_input_id::text,
		       receipt.input_id IS NOT NULL
		  FROM messaging.outbox AS outbox
		  LEFT JOIN trading.commands AS command
		    ON command.command_id = outbox.message_id
		  LEFT JOIN engine.input_receipts AS receipt
		    ON receipt.shard_id = outbox.engine_shard_id
		   AND receipt.input_id = outbox.engine_input_id
		 WHERE outbox.message_id = $1
		   AND outbox.published_at IS NOT NULL`,
		messageID.String(),
	).Scan(
		&messageIDText,
		&message.Subject,
		&message.SchemaVersion,
		&message.Payload,
		&message.Attempts,
		&producerClass,
		&commandID,
		&accountID,
		&accountSequence,
		&commandType,
		&commandSchema,
		&canonicalAction,
		&commandLogicalTime,
		&engineShardID,
		&engineInputID,
		&receiptExists,
	); errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("republish outbox: published message %s not found", messageID)
	} else if err != nil {
		return 0, fmt.Errorf("republish outbox %s: %w", messageID, err)
	}
	parsedID, err := engine.ParseID(messageIDText)
	if err != nil {
		return 0, fmt.Errorf("republish outbox: parse message ID: %w", err)
	}
	message.MessageID = parsedID
	if commandAuthorityMatches(
		message,
		producerClass,
		commandID,
		accountID,
		accountSequence,
		commandType,
		commandSchema,
		canonicalAction,
		commandLogicalTime,
	) {
		message.orderedCommandClaim = commandPublicationFingerprint(message)
	}
	if engineEventAuthorityMatches(
		message,
		producerClass,
		engineShardID,
		engineInputID,
		receiptExists,
	) {
		message.engineEventClaim = engineEventPublicationFingerprint(message)
	}
	sequence, err := publisher.Publish(ctx, message)
	if err != nil {
		return 0, fmt.Errorf("republish outbox %s: %w", messageID, err)
	}
	if sequence == 0 {
		return 0, errors.New("republish outbox: transport returned zero stream sequence")
	}
	return sequence, nil
}

func (store *MessagingStore) claimOutbox(
	ctx context.Context,
	now time.Time,
	limit int,
	claimLease time.Duration,
) ([]OutboxMessage, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("claim outbox: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	rows, err := tx.Query(ctx, `
		SELECT outbox.message_id::text, outbox.subject,
		       outbox.schema_version, outbox.payload, outbox.attempts,
		       outbox.producer_class,
		       COALESCE(command.command_id::text, ''),
		       COALESCE(command.account_id, ''),
		       COALESCE(command.account_sequence, 0),
		       COALESCE(command.command_type, ''),
		       COALESCE(command.schema_version, 0),
		       COALESCE(command.canonical_payload, 'null'::jsonb),
		       COALESCE(command.logical_time, 0),
		       outbox.engine_shard_id, outbox.engine_input_id::text,
		       receipt.input_id IS NOT NULL
		  FROM messaging.outbox AS outbox
		  LEFT JOIN trading.commands AS command
		    ON command.command_id = outbox.message_id
		  LEFT JOIN engine.input_receipts AS receipt
		    ON receipt.shard_id = outbox.engine_shard_id
		   AND receipt.input_id = outbox.engine_input_id
		 WHERE outbox.published_at IS NULL
		   AND outbox.next_attempt_at <= $1
		   AND (outbox.claimed_at IS NULL OR outbox.claimed_at <= $2)
		   AND (
		       command.command_id IS NULL
		       OR NOT EXISTS (
		           SELECT 1
		             FROM trading.commands AS prior_command
		             LEFT JOIN messaging.outbox AS prior_outbox
		               ON prior_outbox.message_id = prior_command.command_id
		            WHERE prior_command.account_id = command.account_id
		              AND prior_command.account_sequence < command.account_sequence
		              AND (
							prior_outbox.message_id IS NULL
							OR prior_outbox.published_at IS NULL
		              )
		       )
		   )
		 ORDER BY outbox.next_attempt_at, outbox.created_at, outbox.message_id
		 LIMIT $3
		 FOR UPDATE OF outbox SKIP LOCKED`,
		now,
		now.Add(-claimLease),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("claim outbox: select: %w", err)
	}
	messages := make([]OutboxMessage, 0, limit)
	for rows.Next() {
		var message OutboxMessage
		var messageIDText string
		var producerClass string
		var commandID string
		var accountID string
		var accountSequence uint64
		var commandType string
		var commandSchema uint32
		var canonicalAction []byte
		var commandLogicalTime int64
		var engineShardID *int64
		var engineInputID *string
		var receiptExists bool
		if scanErr := rows.Scan(
			&messageIDText,
			&message.Subject,
			&message.SchemaVersion,
			&message.Payload,
			&message.Attempts,
			&producerClass,
			&commandID,
			&accountID,
			&accountSequence,
			&commandType,
			&commandSchema,
			&canonicalAction,
			&commandLogicalTime,
			&engineShardID,
			&engineInputID,
			&receiptExists,
		); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("claim outbox: scan: %w", scanErr)
		}
		messageID, parseErr := engine.ParseID(messageIDText)
		if parseErr != nil {
			rows.Close()
			return nil, fmt.Errorf("claim outbox: parse message ID: %w", parseErr)
		}
		message.MessageID = messageID
		message.Attempts++
		if commandAuthorityMatches(
			message,
			producerClass,
			commandID,
			accountID,
			accountSequence,
			commandType,
			commandSchema,
			canonicalAction,
			commandLogicalTime,
		) {
			message.orderedCommandClaim = commandPublicationFingerprint(message)
		}
		if engineEventAuthorityMatches(
			message,
			producerClass,
			engineShardID,
			engineInputID,
			receiptExists,
		) {
			message.engineEventClaim = engineEventPublicationFingerprint(message)
		}
		messages = append(messages, message)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return nil, fmt.Errorf("claim outbox: iterate: %w", rowsErr)
	}
	rows.Close()

	for _, message := range messages {
		if _, err := tx.Exec(ctx, `
			UPDATE messaging.outbox
			   SET claimed_at = $2,
			       attempts = attempts + 1,
			       last_error = NULL
			 WHERE message_id = $1 AND published_at IS NULL`,
			message.MessageID.String(),
			now,
		); err != nil {
			return nil, fmt.Errorf(
				"claim outbox message %s: %w",
				message.MessageID,
				err,
			)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("claim outbox: commit: %w", err)
	}
	return messages, nil
}

func (store *MessagingStore) markOutboxPublished(
	ctx context.Context,
	messageID engine.ID,
	sequence uint64,
	now time.Time,
) error {
	tag, err := store.pool.Exec(ctx, `
		UPDATE messaging.outbox
		   SET published_at = $2,
		       publish_sequence = $3,
		       claimed_at = NULL,
		       last_error = NULL
		 WHERE message_id = $1 AND published_at IS NULL`,
		messageID.String(),
		now,
		sequence,
	)
	if err != nil {
		return fmt.Errorf("mark outbox %s published: %w", messageID, err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	var recordedSequence *uint64
	if err := store.pool.QueryRow(ctx, `
		SELECT publish_sequence
		  FROM messaging.outbox
		 WHERE message_id = $1`,
		messageID.String(),
	).Scan(&recordedSequence); err != nil {
		return fmt.Errorf("read outbox %s publication: %w", messageID, err)
	}
	if recordedSequence != nil && *recordedSequence == sequence {
		return nil
	}
	return fmt.Errorf(
		"mark outbox %s published: conflicting stream sequence",
		messageID,
	)
}

func (store *MessagingStore) markOutboxFailed(
	ctx context.Context,
	messageID engine.ID,
	nextAttempt time.Time,
	publishErr error,
) error {
	detail := publishErr.Error()
	const maximumErrorLength = 2048
	if len(detail) > maximumErrorLength {
		detail = detail[:maximumErrorLength]
	}
	tag, err := store.pool.Exec(ctx, `
		UPDATE messaging.outbox
		   SET claimed_at = NULL,
		       next_attempt_at = $2,
		       last_error = $3
		 WHERE message_id = $1 AND published_at IS NULL`,
		messageID.String(),
		nextAttempt,
		detail,
	)
	if err != nil {
		return fmt.Errorf("mark outbox %s failed: %w", messageID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mark outbox %s failed: row is not pending", messageID)
	}
	return nil
}

// ApplyInbox inserts the durable deduplication record and runs the consumer
// effect in the same transaction. A duplicate never invokes effect.
func (store *MessagingStore) ApplyInbox(
	ctx context.Context,
	consumer string,
	messageID engine.ID,
	effect InboxEffect,
) (bool, error) {
	if store == nil || store.pool == nil {
		return false, errors.New("apply inbox: PostgreSQL pool is required")
	}
	if strings.TrimSpace(consumer) == "" {
		return false, errors.New("apply inbox: consumer is required")
	}
	if messageID.IsZero() {
		return false, errors.New("apply inbox: message ID is required")
	}
	if effect == nil {
		return false, errors.New("apply inbox: consumer effect is required")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("apply inbox: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	tag, err := tx.Exec(ctx, `
		INSERT INTO messaging.inbox (consumer, message_id)
		VALUES ($1,$2)
		ON CONFLICT (consumer, message_id) DO NOTHING`,
		consumer,
		messageID.String(),
	)
	if err != nil {
		return false, fmt.Errorf("apply inbox: insert receipt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("apply inbox duplicate: commit: %w", err)
		}
		return true, nil
	}
	if err := effect(ctx, tx); err != nil {
		return false, fmt.Errorf("apply inbox consumer effect: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("apply inbox: commit: %w", err)
	}
	return false, nil
}
