package engine

import (
	"errors"
	"testing"
	"time"
)

func TestTradingActionFailsClosedOnWrongInputKind(t *testing.T) {
	action := TradingAction{
		Kind: TradingActionConfigureAccount,
		ConfigureAccount: &ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   OmsModeNetting,
		},
	}
	payload, err := EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	input := InputEnvelope{
		InputID:              IDFromSequence(ID{}, 601),
		SchemaVersion:        CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 InputKindMarket,
		SourceID:             "hyperliquid",
		SourceSequence:       1,
		StreamSequence:       1,
		MarketSequence:       1,
		LogicalTime:          NewLogicalTime(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}

	state := NewState(7)
	next, _, err := ApplyTrading(state, input, action)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("ApplyTrading error = %v, want ErrInvalidEnvelope", err)
	}
	if next.Ready() || next.Hash() == state.Hash() {
		t.Fatalf("wrong input kind did not halt state: %+v", next)
	}
}

func TestCommandInputCannotCarryMarketTimerOrControlAction(t *testing.T) {
	tests := []TradingAction{
		{
			Kind: TradingActionUpdateBook,
			UpdateBook: &UpdateBook{
				InstrumentID: "BTC-PERP",
				MarkPrice:    "100",
				Bids:         []BookLevel{{Price: "99", Quantity: "1"}},
				Asks:         []BookLevel{{Price: "101", Quantity: "1"}},
			},
		},
		{
			Kind: TradingActionSettleFunding,
			SettleFunding: &SettleFunding{
				SettlementID: IDFromSequence(ID{}, 611),
				InstrumentID: "BTC-PERP",
				OraclePrice:  "100",
				Rate:         "0.001",
			},
		},
		{
			Kind: TradingActionLiquidateAccount,
			LiquidateAccount: &LiquidateAccount{
				AccountID: "account-1",
			},
		},
	}
	for _, action := range tests {
		t.Run(string(action.Kind), func(t *testing.T) {
			payload, err := EncodeTradingAction(action)
			if err != nil {
				t.Fatalf("EncodeTradingAction: %v", err)
			}
			input := InputEnvelope{
				InputID:              IDFromSequence(ID{}, 612),
				SchemaVersion:        CurrentSchemaVersion,
				ShardID:              7,
				Kind:                 InputKindCommand,
				SourceID:             "command-journal",
				SourceSequence:       1,
				StreamSequence:       1,
				LogicalTime:          NewLogicalTime(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)),
				ConfigurationVersion: 1,
				InstrumentVersion:    1,
				Payload:              payload,
			}
			state := NewState(7)
			next, _, err := ApplyTrading(state, input, action)
			if !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("ApplyTrading error = %v, want ErrInvalidEnvelope", err)
			}
			if next.Ready() || next.Hash() == state.Hash() {
				t.Fatalf("command action %q did not halt state", action.Kind)
			}
		})
	}
}

func TestTradingActionInputKindMatrix(t *testing.T) {
	inputKinds := []InputKind{
		InputKindCommand,
		InputKindMarket,
		InputKindTimer,
		InputKindConfiguration,
		InputKindControl,
	}
	actionKinds := []TradingActionKind{
		TradingActionConfigureInstrument,
		TradingActionConfigureAccount,
		TradingActionConfigureRisk,
		TradingActionAdjustBalance,
		TradingActionSettleFunding,
		TradingActionLiquidateAccount,
		TradingActionUpdateBook,
		TradingActionSubmitOrder,
		TradingActionPlaceBracket,
		TradingActionAmendOrder,
		TradingActionCancelOrder,
	}
	allowed := map[InputKind]map[TradingActionKind]bool{
		InputKindCommand: {
			TradingActionConfigureInstrument: true,
			TradingActionConfigureAccount:    true,
			TradingActionConfigureRisk:       true,
			TradingActionAdjustBalance:       true,
			TradingActionSubmitOrder:         true,
			TradingActionPlaceBracket:        true,
			TradingActionAmendOrder:          true,
			TradingActionCancelOrder:         true,
		},
		InputKindMarket: {
			TradingActionUpdateBook: true,
		},
		InputKindTimer: {
			TradingActionSettleFunding:    true,
			TradingActionLiquidateAccount: true,
		},
		InputKindConfiguration: {
			TradingActionConfigureInstrument: true,
			TradingActionConfigureAccount:    true,
			TradingActionConfigureRisk:       true,
		},
		InputKindControl: {
			TradingActionLiquidateAccount: true,
		},
	}
	for _, inputKind := range inputKinds {
		for _, actionKind := range actionKinds {
			got := TradingActionAllowedForInputKind(inputKind, actionKind)
			want := allowed[inputKind][actionKind]
			if got != want {
				t.Fatalf(
					"TradingActionAllowedForInputKind(%d, %q) = %t, want %t",
					inputKind,
					actionKind,
					got,
					want,
				)
			}
		}
	}
}
