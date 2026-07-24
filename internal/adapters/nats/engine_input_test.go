package nats

import (
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
