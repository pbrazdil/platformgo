package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// InputMessage is the stable producer representation of an engine input.
// JetStream's assigned stream sequence is delivery metadata and is excluded.
type InputMessage struct {
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

// EncodeInputMessage produces the stable envelope stored in a producer outbox.
func EncodeInputMessage(input InputEnvelope) ([]byte, error) {
	kind, err := EncodeInputKind(input.Kind)
	if err != nil {
		return nil, err
	}
	if input.InputID.IsZero() {
		return nil, errors.New("encode engine input: input ID is required")
	}
	message := InputMessage{
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

// DecodeInputMessage restores and validates a stable producer envelope.
func DecodeInputMessage(
	encoded []byte,
) (InputEnvelope, TradingAction, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var message InputMessage
	if err := decoder.Decode(&message); err != nil {
		return InputEnvelope{}, TradingAction{}, fmt.Errorf(
			"decode engine input message: %w",
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return InputEnvelope{}, TradingAction{}, errors.New(
				"decode engine input message: multiple JSON values",
			)
		}
		return InputEnvelope{}, TradingAction{}, fmt.Errorf(
			"decode engine input message trailing data: %w",
			err,
		)
	}
	messageID, err := ParseID(message.MessageID)
	if err != nil {
		return InputEnvelope{}, TradingAction{}, fmt.Errorf(
			"decode engine input message ID: %w",
			err,
		)
	}
	kind, err := DecodeInputKind(message.Kind)
	if err != nil {
		return InputEnvelope{}, TradingAction{}, err
	}
	logicalTime, err := time.Parse(time.RFC3339Nano, message.LogicalTime)
	if err != nil {
		return InputEnvelope{}, TradingAction{}, fmt.Errorf(
			"decode engine input logical time: %w",
			err,
		)
	}
	action, payload, err := DecodeTradingActionPayload(
		message.CanonicalActionPayload,
	)
	if err != nil {
		return InputEnvelope{}, TradingAction{}, err
	}
	return InputEnvelope{
		InputID:              messageID,
		SchemaVersion:        message.SchemaVersion,
		ShardID:              ShardID(message.ShardID),
		Kind:                 kind,
		SourceID:             message.SourceID,
		SourceSequence:       message.SourceSequence,
		MarketSequence:       message.MarketSequence,
		LogicalTime:          NewLogicalTime(logicalTime),
		ConfigurationVersion: message.ConfigurationVersion,
		InstrumentVersion:    message.InstrumentVersion,
		Payload:              payload,
	}, action, nil
}

// EncodeInputKind returns the stable textual input-family identifier.
func EncodeInputKind(kind InputKind) (string, error) {
	switch kind {
	case InputKindCommand:
		return "command", nil
	case InputKindMarket:
		return "market", nil
	case InputKindTimer:
		return "timer", nil
	case InputKindConfiguration:
		return "configuration", nil
	case InputKindControl:
		return "control", nil
	default:
		return "", fmt.Errorf("encode engine input: unknown kind %d", kind)
	}
}

// DecodeInputKind restores a stable textual input-family identifier.
func DecodeInputKind(kind string) (InputKind, error) {
	switch kind {
	case "command":
		return InputKindCommand, nil
	case "market":
		return InputKindMarket, nil
	case "timer":
		return InputKindTimer, nil
	case "configuration":
		return InputKindConfiguration, nil
	case "control":
		return InputKindControl, nil
	default:
		return 0, fmt.Errorf("decode engine input: unknown kind %q", kind)
	}
}
