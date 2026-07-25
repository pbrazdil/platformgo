package nats

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestEngineInputCodecAssignsDurableShardConsumerSequence(t *testing.T) {
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	inputID := engine.IDFromSequence(engine.ID{}, 111)
	input := engine.InputEnvelope{
		InputID:              inputID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 engine.InputKindCommand,
		SourceID:             "command-journal",
		SourceSequence:       4,
		MarketSequence:       3,
		LogicalTime:          engine.NewLogicalTime(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)),
		ConfigurationVersion: 2,
		InstrumentVersion:    5,
		Payload:              payload,
	}
	encoded, err := EncodeEngineInputMessage(input)
	if err != nil {
		t.Fatalf("EncodeEngineInputMessage: %v", err)
	}
	decoded, decodedAction, err := decodeEngineInputMessage(InboundMessage{
		MessageID:      inputID,
		Subject:        "engine.input.7.command.v1",
		Data:           encoded,
		StreamSequence: 19,
	})
	if err != nil {
		t.Fatalf("decodeEngineInputMessage: %v", err)
	}
	if decoded.StreamSequence != 19 ||
		decoded.InputID != inputID ||
		decoded.ShardID != 7 ||
		decodedAction.Kind != action.Kind ||
		decodedAction.ConfigureAccount == nil ||
		decodedAction.ConfigureAccount.AccountID != "account-1" {
		t.Fatalf("decoded input/action = %+v / %+v", decoded, decodedAction)
	}
}

func TestEngineInputCodecRejectsSubjectEnvelopeMismatch(t *testing.T) {
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	inputID := engine.IDFromSequence(engine.ID{}, 112)
	input := engine.InputEnvelope{
		InputID:              inputID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 engine.InputKindCommand,
		SourceID:             "command-journal",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	encoded, err := EncodeEngineInputMessage(input)
	if err != nil {
		t.Fatalf("EncodeEngineInputMessage: %v", err)
	}

	for _, subject := range []string{
		"engine.input.8.command.v1",
		"engine.input.7.market.hyperliquid.v1",
		"engine.input.7.command.v2",
		"engine.input.7.command.v1.extra",
	} {
		t.Run(strings.ReplaceAll(subject, ".", "_"), func(t *testing.T) {
			_, _, err := decodeEngineInputMessage(InboundMessage{
				MessageID:      inputID,
				Subject:        subject,
				Data:           encoded,
				StreamSequence: 1,
			})
			if err == nil {
				t.Fatalf("decodeEngineInputMessage subject %q succeeded", subject)
			}
		})
	}
}

func TestEngineInputCodecPreservesEnvelopeForMessageIDPoison(t *testing.T) {
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	inputID := engine.IDFromSequence(engine.ID{}, 113)
	input := engine.InputEnvelope{
		InputID:              inputID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 engine.InputKindCommand,
		SourceID:             "command-journal",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	encoded, err := EncodeEngineInputMessage(input)
	if err != nil {
		t.Fatalf("EncodeEngineInputMessage: %v", err)
	}
	tests := []InboundMessage{
		{
			MessageID:      engine.IDFromSequence(engine.ID{}, 114),
			Subject:        "engine.input.7.command.v1",
			Data:           encoded,
			StreamSequence: 1,
		},
		{
			MessageIDError: errors.New("missing message ID"),
			Subject:        "engine.input.7.command.v1",
			Data:           encoded,
			StreamSequence: 1,
		},
	}
	for index, inbound := range tests {
		decoded, decodedAction, err := decodeEngineInputMessage(inbound)
		if err == nil {
			t.Fatalf("decode poison %d succeeded", index)
		}
		if decoded.InputID != inputID ||
			decoded.StreamSequence != 1 ||
			decodedAction.Kind != action.Kind {
			t.Fatalf(
				"decode poison %d lost envelope/action: %+v / %+v",
				index,
				decoded,
				decodedAction,
			)
		}
	}
}
