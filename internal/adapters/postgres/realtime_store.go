package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

var ErrRealtimeClaimLost = errors.New("realtime publication claim was lost")

// RealtimePublication is one immutable, already-committed delivery attempt.
type RealtimePublication struct {
	Channel          string
	EventID          string
	EventType        string
	AccountID        string
	Timestamp        int64
	Data             json.RawMessage
	SchemaVersion    uint32
	Sequence         uint64
	Attempts         uint32
	RetryAttemptBase uint32
	ClaimedAt        time.Time
}

type RealtimeFailureClass string

const (
	RealtimeFailureTransient      RealtimeFailureClass = "transient"
	RealtimeFailurePermanent      RealtimeFailureClass = "permanent"
	RealtimeFailureRetryExhausted RealtimeFailureClass = "retry_exhausted"
)

type RealtimeRequeue struct {
	RequestID string
	Channel   string
	EventID   string
	Actor     string
	Reason    string
}

// RealtimeStore owns bounded claims and acknowledgments for committed
// Centrifugo publications. It performs no network calls.
type RealtimeStore struct {
	pool *pgxpool.Pool
}

func NewRealtimeStore(pool *pgxpool.Pool) *RealtimeStore {
	return &RealtimeStore{pool: pool}
}

// ClaimRealtimeBatch claims at most the oldest eligible publication per
// channel. An unpublished predecessor blocks every later channel sequence.
func (store *RealtimeStore) ClaimRealtimeBatch(
	ctx context.Context,
	limit int,
	claimLease time.Duration,
) ([]RealtimePublication, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("claim realtime: PostgreSQL pool is required")
	}
	if limit <= 0 || claimLease <= 0 {
		return nil, errors.New("claim realtime: positive limit and lease are required")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("claim realtime: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	var databaseNow time.Time
	if readTimeErr := tx.QueryRow(
		ctx,
		"SELECT clock_timestamp()",
	).Scan(&databaseNow); readTimeErr != nil {
		return nil, fmt.Errorf(
			"claim realtime: read database time: %w",
			readTimeErr,
		)
	}

	rows, err := tx.Query(ctx, `
		SELECT publication.channel,
		       publication.event_id::text,
		       publication.event_type,
		       publication.account_id,
		       publication.logical_time,
		       publication.data,
		       publication.schema_version,
		       publication.sequence,
		       publication.attempts,
		       publication.retry_attempt_base
		  FROM realtime.publications AS publication
		 WHERE publication.published_at IS NULL
		   AND publication.quarantined_at IS NULL
		   AND publication.next_attempt_at <= $1
		   AND (
		       publication.claimed_at IS NULL
		       OR publication.claimed_at <= $2
		   )
		   AND NOT EXISTS (
		       SELECT 1
		         FROM realtime.publications AS predecessor
		        WHERE predecessor.channel = publication.channel
		          AND predecessor.sequence < publication.sequence
		          AND predecessor.published_at IS NULL
		   )
		 ORDER BY publication.next_attempt_at,
		          publication.channel,
		          publication.sequence
		 FOR UPDATE OF publication SKIP LOCKED
		 LIMIT $3`,
		databaseNow,
		databaseNow.Add(-claimLease),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("claim realtime: select: %w", err)
	}
	publications := make([]RealtimePublication, 0, limit)
	for rows.Next() {
		var publication RealtimePublication
		if err := rows.Scan(
			&publication.Channel,
			&publication.EventID,
			&publication.EventType,
			&publication.AccountID,
			&publication.Timestamp,
			&publication.Data,
			&publication.SchemaVersion,
			&publication.Sequence,
			&publication.Attempts,
			&publication.RetryAttemptBase,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("claim realtime: scan: %w", err)
		}
		publications = append(publications, publication)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("claim realtime: rows: %w", err)
	}
	rows.Close()

	for index := range publications {
		if err := tx.QueryRow(ctx, `
			UPDATE realtime.publications
			   SET attempts = attempts + 1,
			       claimed_at = $3
			 WHERE channel = $1
			   AND event_id = $2
			RETURNING attempts, claimed_at`,
			publications[index].Channel,
			publications[index].EventID,
			databaseNow,
		).Scan(
			&publications[index].Attempts,
			&publications[index].ClaimedAt,
		); err != nil {
			return nil, fmt.Errorf(
				"claim realtime %s/%s: %w",
				publications[index].Channel,
				publications[index].EventID,
				err,
			)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("claim realtime: commit: %w", err)
	}
	return publications, nil
}

// Ready proves the projector can read its required relation and that no
// previously failed head publication is being silently retried.
func (store *RealtimeStore) Ready(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("realtime readiness: PostgreSQL pool is required")
	}
	var (
		canRead               bool
		canUpdateAttempts     bool
		canUpdateClaimedAt    bool
		canUpdatePublished    bool
		canUpdateRetry        bool
		canUpdateError        bool
		canUpdateQuarantined  bool
		canUpdateFailureClass bool
		failedHead            bool
		futureClaim           bool
		unresolvedClaim       bool
	)
	if err := store.pool.QueryRow(ctx, `
		SELECT
			COALESCE(
				has_table_privilege(
					current_user,
					to_regclass('realtime.publications'),
					'SELECT'
				),
				false
			),
			COALESCE(
				has_column_privilege(
					current_user,
					to_regclass('realtime.publications'),
					'attempts',
					'UPDATE'
				),
				false
			),
			COALESCE(
				has_column_privilege(
					current_user,
					to_regclass('realtime.publications'),
					'claimed_at',
					'UPDATE'
				),
				false
			),
			COALESCE(
				has_column_privilege(
					current_user,
					to_regclass('realtime.publications'),
					'published_at',
					'UPDATE'
				),
				false
			),
			COALESCE(
				has_column_privilege(
					current_user,
					to_regclass('realtime.publications'),
					'next_attempt_at',
					'UPDATE'
				),
				false
			),
			COALESCE(
				has_column_privilege(
					current_user,
					to_regclass('realtime.publications'),
					'last_error',
					'UPDATE'
				),
				false
			),
			COALESCE(
				has_column_privilege(
					current_user,
					to_regclass('realtime.publications'),
					'quarantined_at',
					'UPDATE'
				),
				false
			),
			COALESCE(
				has_column_privilege(
					current_user,
					to_regclass('realtime.publications'),
					'failure_class',
					'UPDATE'
				),
				false
			),
			EXISTS (
				SELECT 1
				  FROM realtime.publications AS publication
				 WHERE publication.published_at IS NULL
				   AND publication.last_error IS NOT NULL
				   AND NOT EXISTS (
				       SELECT 1
				         FROM realtime.publications AS predecessor
				        WHERE predecessor.channel = publication.channel
				          AND predecessor.sequence < publication.sequence
				          AND predecessor.published_at IS NULL
				   )
			),
			EXISTS (
				SELECT 1
				  FROM realtime.publications
				 WHERE published_at IS NULL
				   AND claimed_at > clock_timestamp()
			),
			EXISTS (
				SELECT 1
				  FROM realtime.publications
				 WHERE published_at IS NULL
				   AND claimed_at IS NOT NULL
			)`,
	).Scan(
		&canRead,
		&canUpdateAttempts,
		&canUpdateClaimedAt,
		&canUpdatePublished,
		&canUpdateRetry,
		&canUpdateError,
		&canUpdateQuarantined,
		&canUpdateFailureClass,
		&failedHead,
		&futureClaim,
		&unresolvedClaim,
	); err != nil {
		return fmt.Errorf("realtime readiness: %w", err)
	}
	if !canRead ||
		!canUpdateAttempts ||
		!canUpdateClaimedAt ||
		!canUpdatePublished ||
		!canUpdateRetry ||
		!canUpdateError ||
		!canUpdateQuarantined ||
		!canUpdateFailureClass {
		return errors.New("realtime readiness: realtime role privileges are incomplete")
	}
	if failedHead || futureClaim || unresolvedClaim {
		return errors.New("realtime readiness: a channel head publication failed")
	}
	return nil
}

func (store *RealtimeStore) MarkRealtimePublished(
	ctx context.Context,
	publication RealtimePublication,
) error {
	if store == nil || store.pool == nil {
		return errors.New("mark realtime published: PostgreSQL pool is required")
	}
	tag, err := store.pool.Exec(ctx, `
		UPDATE realtime.publications
		   SET published_at = clock_timestamp(),
		       last_error = NULL,
		       failure_class = NULL
		 WHERE channel = $1
		   AND event_id = $2
		   AND claimed_at = $3
		   AND published_at IS NULL`,
		publication.Channel,
		publication.EventID,
		publication.ClaimedAt,
	)
	if err != nil {
		return fmt.Errorf("mark realtime published: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf(
			"%w: %s/%s",
			ErrRealtimeClaimLost,
			publication.Channel,
			publication.EventID,
		)
	}
	return nil
}

func (store *RealtimeStore) MarkRealtimeFailed(
	ctx context.Context,
	publication RealtimePublication,
	retryAfter time.Duration,
	failureClass RealtimeFailureClass,
	quarantine bool,
	publishErr error,
) error {
	if store == nil || store.pool == nil {
		return errors.New("mark realtime failed: PostgreSQL pool is required")
	}
	if publishErr == nil {
		return errors.New("mark realtime failed: publish error is required")
	}
	if retryAfter < 0 {
		return errors.New("mark realtime failed: retry delay cannot be negative")
	}
	switch failureClass {
	case RealtimeFailureTransient:
		if quarantine {
			return errors.New(
				"mark realtime failed: transient failure cannot be quarantined",
			)
		}
	case RealtimeFailurePermanent, RealtimeFailureRetryExhausted:
		if !quarantine {
			return errors.New(
				"mark realtime failed: terminal failure must be quarantined",
			)
		}
	default:
		return errors.New("mark realtime failed: classified failure is required")
	}
	detail := realtimeFailureDetail(publishErr)
	tag, err := store.pool.Exec(ctx, `
		UPDATE realtime.publications
		   SET next_attempt_at =
		           clock_timestamp() + ($4 * interval '1 microsecond'),
		       claimed_at = NULL,
		       quarantined_at = CASE
		           WHEN $6 THEN clock_timestamp()
		           ELSE NULL
		       END,
		       failure_class = $7,
		       last_error = $5
		 WHERE channel = $1
		   AND event_id = $2
		   AND claimed_at = $3
		   AND published_at IS NULL`,
		publication.Channel,
		publication.EventID,
		publication.ClaimedAt,
		retryAfter.Microseconds(),
		detail,
		quarantine,
		string(failureClass),
	)
	if err != nil {
		return fmt.Errorf("mark realtime failed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf(
			"%w: %s/%s",
			ErrRealtimeClaimLost,
			publication.Channel,
			publication.EventID,
		)
	}
	return nil
}

func realtimeFailureDetail(err error) string {
	detail := strings.ToValidUTF8(err.Error(), "\uFFFD")
	detail = strings.ReplaceAll(detail, "\x00", "\uFFFD")
	if len(detail) <= 4096 {
		return detail
	}
	detail = detail[:4096]
	for !utf8.ValidString(detail) {
		detail = detail[:len(detail)-1]
	}
	return detail
}

// RequeueRealtimePublication records an operator repair request and starts a
// fresh bounded retry cycle without changing publication identity or order.
func (store *RealtimeStore) RequeueRealtimePublication(
	ctx context.Context,
	request RealtimeRequeue,
) error {
	if store == nil || store.pool == nil {
		return errors.New("requeue realtime: PostgreSQL pool is required")
	}
	if request.RequestID == "" || request.Channel == "" || request.EventID == "" ||
		strings.TrimSpace(request.Actor) == "" ||
		strings.TrimSpace(request.Reason) == "" {
		return errors.New("requeue realtime: request, publication, actor, and reason are required")
	}
	if _, err := store.pool.Exec(ctx, `
		SELECT realtime.requeue_publication($1,$2,$3,$4,$5)`,
		request.RequestID,
		request.Channel,
		request.EventID,
		request.Actor,
		request.Reason,
	); err != nil {
		return fmt.Errorf("requeue realtime publication: %w", err)
	}
	return nil
}

func persistRealtime(
	ctx context.Context,
	tx pgx.Tx,
	action engine.TradingAction,
	decision engine.Decision,
) error {
	projections, err := realtimeDecisionProjections(ctx, tx, action, decision)
	if err != nil {
		return err
	}
	for _, projection := range projections {
		eventType, accountID, data, ok, err := realtimeEvent(
			ctx,
			tx,
			projection.decision,
			projection.event,
		)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		rows, err := tx.Query(ctx, `
			SELECT user_id
			  FROM identity.user_accounts
			 WHERE account_id = $1
			 ORDER BY user_id`,
			accountID,
		)
		if err != nil {
			return fmt.Errorf("read realtime subscribers for %s: %w", accountID, err)
		}
		var userIDs []string
		for rows.Next() {
			var userID string
			if err := rows.Scan(&userID); err != nil {
				rows.Close()
				return fmt.Errorf("read realtime subscriber for %s: %w", accountID, err)
			}
			userIDs = append(userIDs, userID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("read realtime subscribers for %s: %w", accountID, err)
		}
		rows.Close()

		for _, userID := range userIDs {
			channel, err := realtimeUserChannel(userID)
			if err != nil {
				return err
			}
			var sequence uint64
			if err := tx.QueryRow(ctx, `
				SELECT realtime.allocate_channel_sequence($1)`,
				channel,
			).Scan(&sequence); err != nil {
				return fmt.Errorf("allocate realtime sequence for %s: %w", channel, err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO realtime.publications (
					channel,
					event_id,
					sequence,
					schema_version,
					event_type,
					account_id,
					logical_time,
					data
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				channel,
				projection.event.EventID.String(),
				sequence,
				engine.CurrentSchemaVersion,
				eventType,
				accountID,
				projection.event.LogicalTime.UnixNano()/int64(time.Millisecond),
				data,
			); err != nil {
				return fmt.Errorf(
					"persist realtime publication %s/%s: %w",
					channel,
					projection.event.EventID,
					err,
				)
			}
		}
	}
	return nil
}

type realtimeProjection struct {
	decision engine.Decision
	event    engine.DomainEvent
}

func realtimeDecisionProjections(
	ctx context.Context,
	tx pgx.Tx,
	action engine.TradingAction,
	decision engine.Decision,
) ([]realtimeProjection, error) {
	projections := make([]realtimeProjection, 0, len(decision.Events)+2)
	for _, orderEventsFirst := range []bool{true, false} {
		for _, event := range decision.Events {
			isOrderEvent := strings.HasPrefix(event.Kind, "order.")
			if isOrderEvent != orderEventsFirst {
				continue
			}
			previouslyTriggered := false
			previouslyFilledQuantity := "0"
			previousStatus := engine.OrderStatusWorking
			if isOrderEvent {
				err := tx.QueryRow(ctx, `
					SELECT triggered,
					       trim_scale(filled_quantity)::text,
					       status
					  FROM trading.orders
					 WHERE order_id = $1`,
					event.AggregateID.String(),
				).Scan(
					&previouslyTriggered,
					&previouslyFilledQuantity,
					&previousStatus,
				)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return nil, fmt.Errorf(
						"read previous realtime order trigger state %s: %w",
						event.AggregateID,
						err,
					)
				}
			}
			eventProjections, err := realtimeProjections(
				action,
				decision,
				event,
				previouslyTriggered,
				previouslyFilledQuantity,
				previousStatus,
			)
			if err != nil {
				return nil, err
			}
			projections = append(projections, eventProjections...)
		}
	}
	return projections, nil
}

func realtimeProjections(
	action engine.TradingAction,
	decision engine.Decision,
	event engine.DomainEvent,
	previouslyTriggered bool,
	previouslyFilledQuantity string,
	previousStatus engine.OrderStatus,
) ([]realtimeProjection, error) {
	if !strings.HasPrefix(event.Kind, "order.") {
		return []realtimeProjection{{decision: decision, event: event}}, nil
	}
	var order engine.OrderSnapshot
	found := false
	for _, candidate := range decision.OrderChanges {
		if candidate.OrderID == event.AggregateID &&
			candidate.Version == event.AggregateVersion {
			order = candidate
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf(
			"realtime order event %s has no matching snapshot version %d",
			event.AggregateID,
			event.AggregateVersion,
		)
	}
	type transition struct {
		kind     string
		snapshot engine.OrderSnapshot
	}
	transitions := make([]transition, 0, 3)
	submittedNow := (action.Kind == engine.TradingActionSubmitOrder &&
		action.SubmitOrder != nil &&
		action.SubmitOrder.OrderID == order.OrderID) ||
		(action.Kind == engine.TradingActionPlaceBracket &&
			action.PlaceBracket != nil &&
			action.PlaceBracket.EntryOrderID == order.OrderID)
	emitCreated := submittedNow && order.Status != engine.OrderStatusRejected
	triggeredNow := order.Triggered && !previouslyTriggered
	amendedNow := action.Kind == engine.TradingActionAmendOrder &&
		action.AmendOrder != nil &&
		action.AmendOrder.OrderID == order.OrderID
	if emitCreated {
		created := order
		if order.Version > 1 || triggeredNow {
			created.Status = engine.OrderStatusWorking
			created.FilledQuantity = "0"
		}
		transitions = append(transitions, transition{
			kind: "order.created", snapshot: created,
		})
	}
	finalType, finalAccepted := realtimeOrderEventType(event.Kind, order)
	if amendedNow && finalAccepted && finalType != "order.updated" {
		updated := order
		updated.Status = previousStatus
		updated.FilledQuantity = previouslyFilledQuantity
		transitions = append(transitions, transition{
			kind: "order.updated", snapshot: updated,
		})
	}
	if triggeredNow {
		triggered := order
		triggered.Status = engine.OrderStatusWorking
		triggered.FilledQuantity = "0"
		transitions = append(transitions, transition{
			kind: "order.triggered", snapshot: triggered,
		})
	}
	onlyCreated := emitCreated &&
		!triggeredNow &&
		finalAccepted &&
		finalType == "order.created"
	onlyTriggered := triggeredNow &&
		order.Status == engine.OrderStatusWorking
	if finalAccepted && !onlyCreated && !onlyTriggered {
		transitions = append(transitions, transition{
			kind: event.Kind, snapshot: order,
		})
	}
	if len(transitions) == 0 {
		return nil, nil
	}
	projections := make([]realtimeProjection, 0, len(transitions))
	for index, transition := range transitions {
		projectedEvent := event
		projectedEvent.Kind = transition.kind
		projectedEvent.AggregateVersion = transition.snapshot.Version
		if index < len(transitions)-1 {
			projectedEvent.EventID = engine.IDFromSequence(
				event.EventID,
				uint64(index+1),
			)
		}
		projectedDecision := decision
		projectedDecision.OrderChanges = []engine.OrderSnapshot{
			transition.snapshot,
		}
		projections = append(projections, realtimeProjection{
			decision: projectedDecision,
			event:    projectedEvent,
		})
	}
	return projections, nil
}

func realtimeUserChannel(userID string) (string, error) {
	const prefix = "urn:xb:user:"
	if !strings.HasPrefix(userID, prefix) ||
		!validRealtimeChannelSuffix(userID[len(prefix):]) {
		return "", fmt.Errorf("invalid realtime user ID %q", userID)
	}
	return "user:" + userID[len(prefix):], nil
}

func validRealtimeChannelSuffix(value string) bool {
	if len(value) == 0 || len(value) > 250 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' ||
			character == '_' ||
			character == '~' ||
			character == '-' {
			continue
		}
		return false
	}
	return true
}

func realtimeEvent(
	ctx context.Context,
	tx pgx.Tx,
	decision engine.Decision,
	event engine.DomainEvent,
) (string, string, []byte, bool, error) {
	if strings.HasPrefix(event.Kind, "order.") {
		for _, order := range decision.OrderChanges {
			if order.OrderID != event.AggregateID ||
				order.Version != event.AggregateVersion {
				continue
			}
			eventType, ok := realtimeOrderEventType(event.Kind, order)
			if !ok {
				return "", "", nil, false, nil
			}
			var intentID string
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE((
					SELECT intent_id
					  FROM trading.order_intents
					 WHERE order_id = $1
				), '')`,
				order.OrderID.String(),
			).Scan(&intentID); err != nil {
				return "", "", nil, false, fmt.Errorf(
					"read realtime order intent %s: %w",
					order.OrderID,
					err,
				)
			}
			data, err := json.Marshal(edge.OrderView{
				OrderID:        "urn:xb:order:" + order.OrderID.String(),
				IntentID:       intentID,
				Symbol:         order.InstrumentID,
				Side:           string(order.Side),
				Type:           string(order.Type),
				Quantity:       order.Quantity,
				Status:         string(order.Status),
				FilledQuantity: order.FilledQuantity,
				LimitPrice:     optionalRealtimeDecimal(order.Price),
				TriggerPrice:   optionalRealtimeDecimal(order.TriggerPrice),
				TimeInForce: optionalRealtimeString(
					string(order.TimeInForce),
				),
				ReduceOnly: order.ReduceOnly,
				AccountID:  order.AccountID,
			})
			if err != nil {
				return "", "", nil, false, fmt.Errorf(
					"encode realtime order %s: %w",
					order.OrderID,
					err,
				)
			}
			return eventType, order.AccountID, data, true, nil
		}
		return "", "", nil, false, fmt.Errorf(
			"realtime order event %s has no matching snapshot version %d",
			event.AggregateID,
			event.AggregateVersion,
		)
	}
	if strings.HasPrefix(event.Kind, "position.") {
		for _, position := range decision.PositionChanges {
			if position.PositionID != event.AggregateID ||
				position.Version != event.AggregateVersion {
				continue
			}
			eventType, ok := realtimePositionEventType(event.Kind)
			if !ok {
				return "", "", nil, false, nil
			}
			data, err := json.Marshal(edge.PositionView{
				PositionID: position.PositionID.String(),
				Symbol:     position.InstrumentID,
				Side:       string(position.Side),
				Quantity:   strings.TrimPrefix(position.SignedQuantity, "-"),
				Status:     string(position.Status),
				AccountID:  position.AccountID,
			})
			if err != nil {
				return "", "", nil, false, fmt.Errorf(
					"encode realtime position %s: %w",
					position.PositionID,
					err,
				)
			}
			return eventType, position.AccountID, data, true, nil
		}
		return "", "", nil, false, fmt.Errorf(
			"realtime position event %s has no matching snapshot version %d",
			event.AggregateID,
			event.AggregateVersion,
		)
	}
	return "", "", nil, false, nil
}

func optionalRealtimeDecimal(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalRealtimeString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func realtimeOrderEventType(
	kind string,
	order engine.OrderSnapshot,
) (string, bool) {
	switch kind {
	case "order.created":
		return "order.created", true
	case "order.triggered":
		return "order.triggered", true
	case "order.cancelled":
		return "order.cancelled", true
	case "order.filled":
		return "order.filled", true
	case "order.partially_filled":
		return "order.partially_filled", true
	case "order.held", "order.working", "order.rejected":
		if order.Version == 1 {
			return "order.created", true
		}
		return "order.updated", true
	default:
		return "", false
	}
}

func realtimePositionEventType(kind string) (string, bool) {
	switch kind {
	case "position.open":
		return "position.opened", true
	case "position.increase", "position.reduce", "position.flip":
		return "position.updated", true
	case "position.close":
		return "position.closed", true
	default:
		return "", false
	}
}
