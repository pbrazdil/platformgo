package postgres

import (
	"context"
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
	MessageID     engine.ID
	Subject       string
	SchemaVersion uint32
	Payload       []byte
	Attempts      uint32
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
		SELECT message_id::text, subject, schema_version, payload, attempts
		  FROM messaging.outbox
		 WHERE published_at IS NULL
		   AND next_attempt_at <= $1
		   AND (claimed_at IS NULL OR claimed_at <= $2)
		 ORDER BY next_attempt_at, created_at, message_id
		 LIMIT $3
		 FOR UPDATE SKIP LOCKED`,
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
		if scanErr := rows.Scan(
			&messageIDText,
			&message.Subject,
			&message.SchemaVersion,
			&message.Payload,
			&message.Attempts,
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
