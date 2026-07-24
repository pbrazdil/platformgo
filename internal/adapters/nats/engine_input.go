package nats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

// EngineInputMessage is the durable transport representation published before
// JetStream assigns the shard consumer sequence.
type EngineInputMessage struct {
	MessageID              string `json:"messageId"`
	SchemaVersion          uint32 `json:"schemaVersion"`
	ShardID                uint32 `json:"shardId"`
	Kind                   string `json:"kind"`
	SourceID               string `json:"sourceId"`
	SourceSequence         uint64 `json:"sourceSequence"`
	MarketSequence         uint64 `json:"marketSequence"`
	LogicalTime            string `json:"logicalTime"`
	ConfigurationVersion   uint64 `json:"configurationVersion"`
	InstrumentVersion      uint64 `json:"instrumentVersion"`
	CanonicalActionPayload []byte `json:"canonicalActionPayload"`
}

// EncodeEngineInputMessage produces the transport envelope stored in the
// command or producer outbox. StreamSequence is intentionally not encoded.
func EncodeEngineInputMessage(input engine.InputEnvelope) ([]byte, error) {
	kind, err := encodeInputKind(input.Kind)
	if err != nil {
		return nil, err
	}
	if input.InputID.IsZero() {
		return nil, errors.New("encode engine input: input ID is required")
	}
	message := EngineInputMessage{
		MessageID:              input.InputID.String(),
		SchemaVersion:          input.SchemaVersion,
		ShardID:                uint32(input.ShardID),
		Kind:                   kind,
		SourceID:               input.SourceID,
		SourceSequence:         input.SourceSequence,
		MarketSequence:         input.MarketSequence,
		LogicalTime:            input.LogicalTime.String(),
		ConfigurationVersion:   input.ConfigurationVersion,
		InstrumentVersion:      input.InstrumentVersion,
		CanonicalActionPayload: input.Payload.Bytes(),
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode engine input message: %w", err)
	}
	return encoded, nil
}

func decodeEngineInputMessage(
	inbound InboundMessage,
) (engine.InputEnvelope, engine.TradingAction, error) {
	decoder := json.NewDecoder(bytes.NewReader(inbound.Data))
	decoder.DisallowUnknownFields()
	var message EngineInputMessage
	if err := decoder.Decode(&message); err != nil {
		return engine.InputEnvelope{}, engine.TradingAction{}, fmt.Errorf(
			"decode engine input message: %w",
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return engine.InputEnvelope{}, engine.TradingAction{}, errors.New(
				"decode engine input message: multiple JSON values",
			)
		}
		return engine.InputEnvelope{}, engine.TradingAction{}, fmt.Errorf(
			"decode engine input message trailing data: %w",
			err,
		)
	}
	messageID, err := engine.ParseID(message.MessageID)
	if err != nil {
		return engine.InputEnvelope{}, engine.TradingAction{}, fmt.Errorf(
			"decode engine input message ID: %w",
			err,
		)
	}
	if messageID != inbound.MessageID {
		return engine.InputEnvelope{}, engine.TradingAction{}, errors.New(
			"decode engine input message: header and envelope IDs differ",
		)
	}
	kind, err := decodeInputKind(message.Kind)
	if err != nil {
		return engine.InputEnvelope{}, engine.TradingAction{}, err
	}
	logicalTime, err := time.Parse(time.RFC3339Nano, message.LogicalTime)
	if err != nil {
		return engine.InputEnvelope{}, engine.TradingAction{}, fmt.Errorf(
			"decode engine input logical time: %w",
			err,
		)
	}
	action, payload, err := engine.DecodeTradingActionPayload(
		message.CanonicalActionPayload,
	)
	if err != nil {
		return engine.InputEnvelope{}, engine.TradingAction{}, err
	}
	input := engine.InputEnvelope{
		InputID:              messageID,
		SchemaVersion:        message.SchemaVersion,
		ShardID:              engine.ShardID(message.ShardID),
		Kind:                 kind,
		SourceID:             message.SourceID,
		SourceSequence:       message.SourceSequence,
		StreamSequence:       inbound.StreamSequence,
		MarketSequence:       message.MarketSequence,
		LogicalTime:          engine.NewLogicalTime(logicalTime),
		ConfigurationVersion: message.ConfigurationVersion,
		InstrumentVersion:    message.InstrumentVersion,
		Payload:              payload,
	}
	return input, action, nil
}

func encodeInputKind(kind engine.InputKind) (string, error) {
	switch kind {
	case engine.InputKindCommand:
		return "command", nil
	case engine.InputKindMarket:
		return "market", nil
	case engine.InputKindTimer:
		return "timer", nil
	case engine.InputKindConfiguration:
		return "configuration", nil
	case engine.InputKindControl:
		return "control", nil
	default:
		return "", fmt.Errorf("encode engine input: unknown kind %d", kind)
	}
}

func decodeInputKind(kind string) (engine.InputKind, error) {
	switch kind {
	case "command":
		return engine.InputKindCommand, nil
	case "market":
		return engine.InputKindMarket, nil
	case "timer":
		return engine.InputKindTimer, nil
	case "configuration":
		return engine.InputKindConfiguration, nil
	case "control":
		return engine.InputKindControl, nil
	default:
		return 0, fmt.Errorf("decode engine input: unknown kind %q", kind)
	}
}

// EngineProcessor is the single-owner bridge from one shard pull consumer into
// the PostgreSQL transaction and deterministic core.
type EngineProcessor struct {
	store *platformpostgres.EngineStore
	state engine.State
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
	state, err := store.RecoverTradingState(ctx, shardID)
	if err != nil {
		return nil, fmt.Errorf("create engine processor: recover shard %d: %w", shardID, err)
	}
	return &EngineProcessor{store: store, state: state}, nil
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
	input, action, err := decodeEngineInputMessage(inbound)
	if err != nil {
		return err
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
		platformpostgres.ApplyOptions{},
	)
	processor.state = next
	return err
}

// State returns the current single-owner value for readiness and testing.
func (processor *EngineProcessor) State() engine.State {
	if processor == nil {
		return engine.State{}
	}
	return processor.state
}
