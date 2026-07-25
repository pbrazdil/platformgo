package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const commandSequenceRetries = 16

// CommandJournal is the durable admission boundary consumed by the edge
// application service.
type CommandJournal interface {
	NextAccountSequence(context.Context, string) (uint64, error)
	ConfigurationVersion(context.Context) (uint64, error)
	InstrumentVersion(context.Context, string) (uint64, error)
	Replay(
		context.Context,
		string,
		string,
		[sha256.Size]byte,
	) (BeginCommandResult, bool, error)
	Begin(
		context.Context,
		BeginCommandRequest,
	) (BeginCommandResult, error)
}

// Clock makes edge receipt time explicit and testable.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// OrderSubmissionConfig fixes the deterministic routing/version inputs.
type OrderSubmissionConfig struct {
	ShardID        engine.ShardID
	IdempotencyTTL time.Duration
	Clock          Clock
	Readiness      func(context.Context) error
}

// OrderSubmission durably admits client submit-order commands.
type OrderSubmission struct {
	journal CommandJournal
	config  OrderSubmissionConfig
}

// NewOrderSubmission builds the application boundary for HTTP and gRPC.
func NewOrderSubmission(
	journal CommandJournal,
	config OrderSubmissionConfig,
) (*OrderSubmission, error) {
	if journal == nil {
		return nil, errors.New("order submission: command journal is required")
	}
	if config.IdempotencyTTL <= 0 {
		return nil, errors.New("order submission: positive idempotency TTL is required")
	}
	if config.Clock == nil {
		config.Clock = wallClock{}
	}
	return &OrderSubmission{journal: journal, config: config}, nil
}

// SubmitOrder implements edge.CommandSubmitter.
func (submission *OrderSubmission) SubmitOrder(
	ctx context.Context,
	principal edge.Principal,
	accountID string,
	idempotencyKey string,
	request edge.SubmitOrderRequest,
) (edge.OrderAdmission, error) {
	if submission == nil || submission.journal == nil {
		return edge.OrderAdmission{}, errors.New("submit order: service is not configured")
	}
	if principal.Subject == "" || accountID == "" || idempotencyKey == "" {
		return edge.OrderAdmission{}, errors.New("submit order: principal, account, and idempotency key are required")
	}
	request, err := normalizeSubmitOrderRequest(request)
	if err != nil {
		return edge.OrderAdmission{}, err
	}
	canonicalRequest, err := json.Marshal(request)
	if err != nil {
		return edge.OrderAdmission{}, fmt.Errorf("submit order: encode request: %w", err)
	}
	requestHash := sha256.Sum256(canonicalRequest)
	scope := strings.Join([]string{
		string(principal.Audience),
		principal.Subject,
		"POST",
		"/v1/accounts/" + accountID + "/orders",
	}, "\x1f")
	commandID := stableID("platformgo.command.v1", scope, idempotencyKey, requestHash)
	orderID := stableID("platformgo.order.v1", scope, idempotencyKey, requestHash)
	logicalTime := submission.config.Clock.Now().UTC()
	action, err := tradingAction(accountID, orderID, request)
	if err != nil {
		return edge.OrderAdmission{}, err
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		return edge.OrderAdmission{}, fmt.Errorf("submit order: encode action: %w", err)
	}
	accepted := edge.OrderAccepted{
		OrderID:  "urn:xb:order:" + orderID.String(),
		IntentID: request.IntentID,
	}
	responseBody, err := json.Marshal(accepted)
	if err != nil {
		return edge.OrderAdmission{}, fmt.Errorf(
			"submit order: encode accepted response: %w",
			err,
		)
	}
	responseBody = append(responseBody, '\n')
	response := StoredResponse{
		Status:  202,
		Headers: []byte(`{"content-type":["application/json"]}`),
		Body:    responseBody,
	}
	replay, found, err := submission.journal.Replay(
		ctx,
		scope,
		idempotencyKey,
		requestHash,
	)
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return edge.OrderAdmission{}, edge.ErrIdempotencyConflict
	case err != nil:
		return edge.OrderAdmission{}, fmt.Errorf(
			"submit order: load replay: %w",
			err,
		)
	case found:
		return orderAdmissionFromStored(accepted, replay.Response)
	}
	if submission.config.Readiness != nil {
		if readinessErr := submission.config.Readiness(ctx); readinessErr != nil {
			replay, found, replayErr := submission.journal.Replay(
				ctx,
				scope,
				idempotencyKey,
				requestHash,
			)
			switch {
			case errors.Is(replayErr, ErrIdempotencyConflict):
				return edge.OrderAdmission{}, edge.ErrIdempotencyConflict
			case replayErr != nil:
				return edge.OrderAdmission{}, fmt.Errorf(
					"submit order: recheck replay after readiness loss: %w",
					replayErr,
				)
			case found:
				return orderAdmissionFromStored(accepted, replay.Response)
			}
			return edge.OrderAdmission{}, fmt.Errorf(
				"submit order: runtime is not ready: %w",
				readinessErr,
			)
		}
	}

	for attempt := 0; attempt < commandSequenceRetries; attempt++ {
		configurationVersion, versionErr :=
			submission.journal.ConfigurationVersion(ctx)
		if versionErr != nil {
			return edge.OrderAdmission{}, fmt.Errorf(
				"submit order: resolve configuration version: %w",
				versionErr,
			)
		}
		if configurationVersion == 0 {
			return edge.OrderAdmission{}, errors.New(
				"submit order: configuration version is zero",
			)
		}
		instrumentVersion, versionErr :=
			submission.journal.InstrumentVersion(ctx, request.Symbol)
		if versionErr != nil {
			return edge.OrderAdmission{}, fmt.Errorf(
				"submit order: resolve instrument version: %w",
				versionErr,
			)
		}
		if instrumentVersion == 0 {
			return edge.OrderAdmission{}, errors.New(
				"submit order: instrument version is zero",
			)
		}
		sequence, sequenceErr := submission.journal.NextAccountSequence(ctx, accountID)
		if sequenceErr != nil {
			return edge.OrderAdmission{}, sequenceErr
		}
		input := engine.InputEnvelope{
			InputID:              commandID,
			SchemaVersion:        engine.CurrentSchemaVersion,
			ShardID:              submission.config.ShardID,
			Kind:                 engine.InputKindCommand,
			SourceID:             principal.Subject,
			SourceSequence:       sequence,
			LogicalTime:          engine.NewLogicalTime(logicalTime),
			ConfigurationVersion: configurationVersion,
			InstrumentVersion:    instrumentVersion,
			Payload:              payload,
		}
		outboxPayload, encodeErr := engine.EncodeInputMessage(input)
		if encodeErr != nil {
			return edge.OrderAdmission{}, fmt.Errorf("submit order: encode outbox message: %w", encodeErr)
		}
		result, beginErr := submission.journal.Begin(ctx, BeginCommandRequest{
			Scope:            scope,
			IdempotencyKey:   idempotencyKey,
			RequestHash:      requestHash,
			CommandID:        commandID,
			OrderID:          orderID,
			IntentID:         request.IntentID,
			AccountID:        accountID,
			AccountSequence:  sequence,
			CommandType:      string(engine.TradingActionSubmitOrder),
			SchemaVersion:    engine.CurrentSchemaVersion,
			CanonicalPayload: payload.Bytes(),
			OutboxSubject: fmt.Sprintf(
				"engine.input.%d.command.v%d",
				submission.config.ShardID,
				engine.CurrentSchemaVersion,
			),
			OutboxPayload:       outboxPayload,
			LogicalTime:         logicalTime,
			ExpiresAt:           logicalTime.Add(submission.config.IdempotencyTTL),
			Response:            response,
			RequireRuntimeReady: submission.config.Readiness != nil,
		})
		if errors.Is(beginErr, ErrCommandSequenceGap) ||
			errors.Is(beginErr, ErrEconomicRevisionChanged) {
			continue
		}
		if errors.Is(beginErr, ErrIdempotencyConflict) {
			return edge.OrderAdmission{}, edge.ErrIdempotencyConflict
		}
		if beginErr != nil {
			return edge.OrderAdmission{}, fmt.Errorf("submit order: admit command: %w", beginErr)
		}
		return orderAdmissionFromStored(accepted, result.Response)
	}
	return edge.OrderAdmission{}, errors.New(
		"submit order: concurrent account sequence did not converge",
	)
}

func orderAdmissionFromStored(
	accepted edge.OrderAccepted,
	response StoredResponse,
) (edge.OrderAdmission, error) {
	var stored edge.OrderAccepted
	if response.Status != 202 ||
		json.Unmarshal(response.Body, &stored) != nil ||
		stored != accepted {
		return edge.OrderAdmission{}, errors.New(
			"submit order: stored idempotency response is inconsistent",
		)
	}
	return edge.OrderAdmission{
		OrderAccepted: accepted,
		Response: StoredResponse{
			Status:  response.Status,
			Headers: append([]byte(nil), response.Headers...),
			Body:    append([]byte(nil), response.Body...),
		},
	}, nil
}

func normalizeSubmitOrderRequest(
	request edge.SubmitOrderRequest,
) (edge.SubmitOrderRequest, error) {
	clone := func(value *string) *string {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	request.Price = clone(request.Price)
	request.TriggerPrice = clone(request.TriggerPrice)
	request.TrailingOffset = clone(request.TrailingOffset)
	request.Symbol = strings.TrimSpace(request.Symbol)
	request.Side = strings.ToUpper(strings.TrimSpace(request.Side))
	request.Type = strings.ToUpper(strings.TrimSpace(request.Type))
	if request.Type == "" {
		request.Type = string(engine.OrderTypeMarket)
	}
	timeInForce := string(engine.TimeInForceGTC)
	if request.TimeInForce != nil {
		timeInForce = strings.ToUpper(strings.TrimSpace(*request.TimeInForce))
	}
	request.TimeInForce = &timeInForce
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "quantity", value: &request.Quantity},
		{name: "price", value: request.Price},
		{name: "trigger price", value: request.TriggerPrice},
		{name: "trailing offset", value: request.TrailingOffset},
	} {
		if field.value == nil {
			continue
		}
		parsed, err := decimal.Parse(*field.value)
		if err != nil {
			return edge.SubmitOrderRequest{}, fmt.Errorf(
				"submit order: invalid %s: %w",
				field.name,
				err,
			)
		}
		*field.value = parsed.String()
	}
	return request, nil
}

func tradingAction(
	accountID string,
	orderID engine.ID,
	request edge.SubmitOrderRequest,
) (engine.TradingAction, error) {
	if request.Type == "TRAILING_STOP_MARKET" || request.TrailingOffset != nil {
		return engine.TradingAction{}, errors.New(
			"submit order: trailing-stop execution is not implemented by the deterministic engine",
		)
	}
	side := engine.Side(strings.ToUpper(request.Side))
	orderType := engine.OrderType(request.Type)
	if orderType == "" {
		orderType = engine.OrderTypeMarket
	}
	timeInForce := engine.TimeInForceGTC
	if request.TimeInForce != nil {
		timeInForce = engine.TimeInForce(strings.ToUpper(*request.TimeInForce))
	}
	value := func(optional *string) string {
		if optional == nil {
			return ""
		}
		return *optional
	}
	action := engine.TradingAction{
		Kind: engine.TradingActionSubmitOrder,
		SubmitOrder: &engine.SubmitOrder{
			OrderID:        orderID,
			AccountID:      accountID,
			InstrumentID:   request.Symbol,
			Side:           side,
			Type:           orderType,
			TimeInForce:    timeInForce,
			Quantity:       request.Quantity,
			Price:          value(request.Price),
			TriggerPrice:   value(request.TriggerPrice),
			ReduceOnly:     request.ReduceOnly,
			MaxSlippageBPS: request.MaxSlippageBPS,
		},
	}
	if _, err := engine.EncodeTradingAction(action); err != nil {
		return engine.TradingAction{}, fmt.Errorf("submit order: invalid action: %w", err)
	}
	return action, nil
}

func stableID(label, scope, key string, requestHash [sha256.Size]byte) engine.ID {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(label))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(scope))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(key))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(requestHash[:])
	sum := hasher.Sum(nil)
	var id engine.ID
	copy(id[:], sum[:len(id)])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}
