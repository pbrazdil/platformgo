package nats

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

// EngineInputMessage is the durable transport representation published before
// JetStream assigns the shard consumer sequence.
type EngineInputMessage = engine.InputMessage

// EncodeEngineInputMessage produces the transport envelope stored in the
// command or producer outbox. StreamSequence is intentionally not encoded.
func EncodeEngineInputMessage(input engine.InputEnvelope) ([]byte, error) {
	return engine.EncodeInputMessage(input)
}

func decodeEngineInputMessage(
	inbound InboundMessage,
) (engine.InputEnvelope, engine.TradingAction, error) {
	input, action, err := engine.DecodeInputMessage(inbound.Data)
	if err != nil {
		return engine.InputEnvelope{}, engine.TradingAction{}, err
	}
	input.StreamSequence = inbound.StreamSequence
	if inbound.MessageIDError != nil {
		return input, action, inbound.MessageIDError
	}
	if input.InputID != inbound.MessageID {
		return input, action, errors.New(
			"decode engine input message: header and envelope IDs differ",
		)
	}
	if err := validateEngineInputSubject(inbound.Subject, input); err != nil {
		return input, action, err
	}
	return input, action, nil
}

func validateEngineInputSubject(
	subject string,
	input engine.InputEnvelope,
) error {
	parts := strings.Split(subject, ".")
	if len(parts) < 5 ||
		parts[0] != "engine" ||
		parts[1] != "input" ||
		parts[2] != strconv.FormatUint(uint64(input.ShardID), 10) {
		return fmt.Errorf(
			"decode engine input message: subject %q does not match shard %d",
			subject,
			input.ShardID,
		)
	}
	version := "v" + strconv.FormatUint(uint64(input.SchemaVersion), 10)
	switch input.Kind {
	case engine.InputKindCommand:
		if len(parts) == 5 && parts[3] == "command" && parts[4] == version {
			return nil
		}
	case engine.InputKindMarket:
		if len(parts) == 6 &&
			parts[3] == "market" &&
			parts[4] == input.SourceID &&
			parts[5] == version {
			return nil
		}
	case engine.InputKindTimer:
		if len(parts) == 5 && parts[3] == "timer" && parts[4] == version {
			return nil
		}
	case engine.InputKindConfiguration:
		if len(parts) == 5 && parts[3] == "config" && parts[4] == version {
			return nil
		}
	case engine.InputKindControl:
		if len(parts) == 5 && parts[3] == "control" && parts[4] == version {
			return nil
		}
	}
	kind, _ := engine.EncodeInputKind(input.Kind)
	return fmt.Errorf(
		"decode engine input message: subject %q does not match %s input",
		subject,
		kind,
	)
}

// EngineProcessor is the single-owner bridge from one shard pull consumer into
// the PostgreSQL transaction and deterministic core.
type EngineProcessor struct {
	store          *platformpostgres.EngineStore
	ownership      *platformpostgres.ShardOwnership
	state          engine.State
	transportReady bool
}

// NewEngineProcessor restores the PostgreSQL-authoritative shard state before
// consuming any new JetStream input.
func NewEngineProcessor(
	ctx context.Context,
	store *platformpostgres.EngineStore,
	shardID engine.ShardID,
) (*EngineProcessor, error) {
	if store == nil {
		return nil, errors.New("create engine processor: store is required")
	}
	ownership, err := store.AcquireShardOwnership(ctx, shardID)
	if err != nil {
		return nil, fmt.Errorf(
			"create engine processor: acquire shard %d ownership: %w",
			shardID,
			err,
		)
	}
	state, err := store.RecoverTradingState(ctx, shardID)
	if err != nil {
		_ = ownership.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("create engine processor: recover shard %d: %w", shardID, err)
	}
	return &EngineProcessor{
		store:          store,
		ownership:      ownership,
		state:          state,
		transportReady: state.Ready(),
	}, nil
}

// Handle applies and commits one message. The processor advances its local
// state after PostgreSQL commit, including a durably committed terminal halt.
func (processor *EngineProcessor) Handle(
	ctx context.Context,
	inbound InboundMessage,
) error {
	if processor == nil || processor.store == nil {
		return errors.New("process engine input: processor is not configured")
	}
	if err := processor.ownership.Check(ctx); err != nil {
		processor.transportReady = false
		return fmt.Errorf("process engine input: %w", err)
	}
	if !processor.transportReady {
		return fmt.Errorf(
			"%w: shard %d transport is halted",
			engine.ErrShardNotReady,
			processor.state.ShardID(),
		)
	}
	input, action, err := decodeEngineInputMessage(inbound)
	if err != nil {
		if !input.InputID.IsZero() &&
			input.ShardID == processor.state.ShardID() {
			halted, haltErr := processor.store.HaltTradingInput(
				ctx,
				processor.state,
				input,
				action,
				malformedTransportError(inbound, err),
				processor.ownership,
			)
			if haltErr == nil {
				processor.state = halted
			}
			processor.transportReady = false
			return errors.Join(err, haltErr)
		}
		halted, haltErr := processor.store.HaltTradingInput(
			ctx,
			processor.state,
			malformedTransportInput(processor.state.ShardID(), inbound),
			engine.TradingAction{},
			malformedTransportError(inbound, err),
			processor.ownership,
		)
		if haltErr == nil {
			processor.state = halted
		}
		processor.transportReady = false
		return errors.Join(err, haltErr)
	}
	if input.ShardID != processor.state.ShardID() {
		return fmt.Errorf(
			"process engine input: message shard %d does not match processor shard %d",
			input.ShardID,
			processor.state.ShardID(),
		)
	}
	next, _, _, err := processor.store.ApplyTrading(
		ctx,
		processor.state,
		input,
		action,
		platformpostgres.ApplyOptions{Ownership: processor.ownership},
	)
	processor.state = next
	processor.transportReady = next.Ready()
	return err
}

func malformedTransportInput(
	shardID engine.ShardID,
	inbound InboundMessage,
) engine.InputEnvelope {
	bodyHash := sha256.Sum256(inbound.Data)
	payload, _ := engine.EncodeTradingAction(engine.TradingAction{})
	return engine.InputEnvelope{
		InputID:              inbound.MessageID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              shardID,
		Kind:                 engine.InputKindControl,
		SourceID:             fmt.Sprintf("transport-envelope:%x", bodyHash),
		SourceSequence:       inbound.StreamSequence,
		StreamSequence:       inbound.StreamSequence,
		LogicalTime:          engine.NewLogicalTime(time.Unix(0, 0).UTC()),
		ConfigurationVersion: 0,
		InstrumentVersion:    0,
		Payload:              payload,
	}
}

func malformedTransportError(inbound InboundMessage, decodeErr error) error {
	headerError := ""
	if inbound.MessageIDError != nil {
		headerError = inbound.MessageIDError.Error()
	}
	return fmt.Errorf(
		"%w [subject=%q body_sha256=%x message_id=%s header_error=%q]",
		decodeErr,
		inbound.Subject,
		sha256.Sum256(inbound.Data),
		inbound.MessageID,
		headerError,
	)
}

// State returns the current single-owner value for readiness and testing.
func (processor *EngineProcessor) State() engine.State {
	if processor == nil {
		return engine.State{}
	}
	return processor.state
}

// Ready reports both durable engine readiness and transport-envelope readiness.
func (processor *EngineProcessor) Ready() bool {
	if processor == nil || !processor.transportReady || !processor.state.Ready() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := processor.ownership.Check(ctx); err != nil {
		processor.transportReady = false
		return false
	}
	return true
}

// Close releases the process-lifetime single-writer ownership.
func (processor *EngineProcessor) Close(ctx context.Context) error {
	if processor == nil {
		return nil
	}
	return processor.ownership.Close(ctx)
}
