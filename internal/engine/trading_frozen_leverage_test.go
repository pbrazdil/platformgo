package engine

import (
	"testing"
	"time"
)

const frozenLeverageDecisionHashVersion uint32 = 4

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:952
//	test: fill_surfaces_frozen_effective_leverage
//
// Adaptations:
//   - Direct PostgreSQL fill seeding and API querying are replaced by synchronous
//     deterministic engine execution and state snapshots.
//   - Explicit risk configuration supplies the source's stored leverage values;
//     the default case uses the configured instrument maximum.
//   - A later risk revision is applied after flattening to prove the historical
//     execution fact remains frozen.
//
// Assertions preserved:
//   - Frozen 10x leverage surfaces as 10.
//   - Frozen 5.00x leverage surfaces canonically as 5.
//   - Historical decision generations preserve absent leverage.
func TestTradingFillFreezesEffectiveLeverageV4(t *testing.T) {
	tests := []struct {
		name          string
		configureRisk bool
		inputLeverage string
		wantLeverage  string
		reconfigureTo string
	}{
		{
			name:          "explicit 10",
			configureRisk: true,
			inputLeverage: "10",
			wantLeverage:  "10",
		},
		{
			name:          "explicit 5.00 canonicalizes",
			configureRisk: true,
			inputLeverage: "5.00",
			wantLeverage:  "5",
			reconfigureTo: "10",
		},
		{
			name:         "default instrument maximum",
			wantLeverage: "10",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newFrozenLeverageFixture(t, 3)
			if testCase.configureRisk {
				fixture.apply(t, configureFrozenLeverage(testCase.inputLeverage))
			}

			fixture.version = frozenLeverageDecisionHashVersion
			openOrderID := fixture.id(100)
			open := fixture.apply(t, submitFrozenLeverageOrder(
				openOrderID,
				SideBuy,
				false,
				ID{},
			))
			requireFrozenLeverage(t, open.Fills, testCase.wantLeverage)
			requireFrozenLeverage(
				t,
				fixture.state.FillsForOrder(openOrderID),
				testCase.wantLeverage,
			)

			if testCase.reconfigureTo == "" {
				return
			}

			positionID := IDFromSequence(openOrderID, 0)
			fixture.apply(t, submitFrozenLeverageOrder(
				fixture.id(101),
				SideSell,
				true,
				positionID,
			))
			fixture.apply(t, configureFrozenLeverage(testCase.reconfigureTo))
			requireFrozenLeverage(
				t,
				fixture.state.FillsForOrder(openOrderID),
				testCase.wantLeverage,
			)
		})
	}
}

func TestTradingInstrumentMaxLeverageCannotInvalidateV4RiskAuthority(
	t *testing.T,
) {
	for _, afterFlatten := range []bool{false, true} {
		name := "before first fill"
		if afterFlatten {
			name = "after flatten"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newFrozenLeverageFixture(
				t,
				frozenLeverageDecisionHashVersion,
			)
			fixture.apply(t, configureFrozenLeverage("10"))
			if afterFlatten {
				openOrderID := fixture.id(110)
				open := fixture.apply(t, submitFrozenLeverageOrder(
					openOrderID,
					SideBuy,
					false,
					ID{},
				))
				fixture.apply(t, submitFrozenLeverageOrder(
					fixture.id(111),
					SideSell,
					true,
					IDFromSequence(openOrderID, 0),
				))
				requireFrozenLeverage(t, open.Fills, "10")
			}

			reconfigure := fixture.applyResult(
				t,
				configureFrozenLeverageInstrument(2, "5"),
			)
			if reconfigure.CommandResult.Status != CommandStatusRejected ||
				reconfigure.CommandResult.Reason != RejectionRiskConfigLocked ||
				len(reconfigure.InstrumentChanges) != 0 {
				t.Fatalf(
					"lower max-leverage result = %+v changes %+v, want risk-config-locked rejection",
					reconfigure.CommandResult,
					reconfigure.InstrumentChanges,
				)
			}

			fill := fixture.apply(t, submitFrozenLeverageOrder(
				fixture.id(112),
				SideBuy,
				false,
				ID{},
			))
			requireFrozenLeverage(t, fill.Fills, "10")
		})
	}
}

func TestTradingInstrumentMaxLeveragePreservesHistoricalV3Semantics(
	t *testing.T,
) {
	fixture := newFrozenLeverageFixture(t, 3)
	fixture.apply(t, configureFrozenLeverage("10"))
	reconfigure := fixture.applyResult(
		t,
		configureFrozenLeverageInstrument(2, "5"),
	)
	if reconfigure.CommandResult.Status != CommandStatusAccepted ||
		len(reconfigure.InstrumentChanges) != 1 ||
		reconfigure.InstrumentChanges[0].MaxLeverage != "5" {
		t.Fatalf(
			"historical v3 max-leverage result = %+v changes %+v, want accepted maximum 5",
			reconfigure.CommandResult,
			reconfigure.InstrumentChanges,
		)
	}
	requireFrozenLeverageHash(
		t,
		"historical v3 max-leverage decision",
		reconfigure.DecisionHash,
		"3ea67a84506e99b94670404ba046c0a5101a9a9876c5366c5368ca83bd369878",
	)
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:952
//	test: fill_surfaces_frozen_effective_leverage
//
// Adaptations:
//   - Historical v2/v3 execution and replay preserve the accepted Go behavior
//     from before effective leverage became a v4 fill fact.
//   - Fixed hashes protect historical decisions from the v4 effects encoder.
//
// Assertions preserved:
//   - Historical fills without frozen leverage continue to surface it absent.
func TestTradingHistoricalFillLeverageAndHashGoldens(t *testing.T) {
	tests := []struct {
		name         string
		version      uint32
		inputHash    string
		effectsHash  string
		decisionHash string
		stateHash    string
	}{
		{
			name:         "v2",
			version:      2,
			inputHash:    "3103ca298967dbc7cf493c09fbd698e323886a41b1d5a0849728a5b34f7bb114",
			effectsHash:  "08b98c30bff48f4d9af8afcec60e84c8e151acda1c060642cc5ac9c3a773ccc6",
			decisionHash: "d2857c1c28c3f757cc381609ab329b1fd028755ffa547c4bcf0ff3108940edac",
			stateHash:    "5375c74c5f98ffd5199bd737e6ec6a96a4266ded5cbacde74a4ebf35ffdb5034",
		},
		{
			name:         "v3",
			version:      3,
			inputHash:    "7f606a0d1e3f586fcd790db8531f8f6b9a455408155b2dba2abbfc30b182ee1e",
			effectsHash:  "82d6e34e5e88e0ecf28bdef47f008d85be1c31e829e2a37e184882af7c37d303",
			decisionHash: "6b5b426b6b2401ef8ec3233eb96f9ab70008152f45e255405b3d5c8e8fa37cc9",
			stateHash:    "5e4d7f203a96505c3c389fa7ed70967dcc045a21d4c5a5f1215224beade5ce3f",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			version := testCase.version
			fixture := newFrozenLeverageFixture(t, version)
			fixture.apply(t, configureFrozenLeverage("5.00"))
			orderID := fixture.id(200 + uint64(version))
			decision := fixture.apply(
				t,
				submitFrozenLeverageOrder(orderID, SideBuy, false, ID{}),
			)

			if decision.DecisionHashVersion != version {
				t.Fatalf(
					"decision hash version = %d, want %d",
					decision.DecisionHashVersion,
					version,
				)
			}
			requireFrozenLeverage(t, decision.Fills, "")
			requireFrozenLeverage(t, fixture.state.FillsForOrder(orderID), "")
			requireFrozenLeverageHash(
				t,
				"input",
				decision.InputHash,
				testCase.inputHash,
			)
			requireFrozenLeverageHash(
				t,
				"effects",
				decision.EffectsHash,
				testCase.effectsHash,
			)
			requireFrozenLeverageHash(
				t,
				"decision",
				decision.DecisionHash,
				testCase.decisionHash,
			)
			requireFrozenLeverageHash(
				t,
				"state",
				decision.NextStateHash,
				testCase.stateHash,
			)

			replayedState, replayed, err :=
				ApplyTradingWithReceiptsAtDecisionHashVersion(
					fixture.state,
					fixture.lastInput,
					fixture.lastAction,
					nil,
					version,
				)
			if err != nil {
				t.Fatalf("replay decision v%d: %v", version, err)
			}
			if replayedState.Hash() != fixture.state.Hash() ||
				!equalDecision(replayed, decision) {
				t.Fatalf(
					"replay v%d changed committed result:\nrecorded: %+v\nreplayed: %+v",
					version,
					decision,
					replayed,
				)
			}
			requireFrozenLeverage(t, replayed.Fills, "")

			restarted := NewState(fixture.state.ShardID())
			for index, input := range fixture.inputs {
				restarted, _, err =
					ApplyTradingWithReceiptsAtDecisionHashVersion(
						restarted,
						input,
						fixture.actions[index],
						nil,
						version,
					)
				if err != nil {
					t.Fatalf("restart replay v%d input %d: %v", version, index, err)
				}
			}
			if restarted.Hash() != fixture.state.Hash() ||
				restarted.NextStreamSequence() !=
					fixture.state.NextStreamSequence() {
				t.Fatalf(
					"restart replay v%d state = %s/%d, want %s/%d",
					version,
					restarted.Hash(),
					restarted.NextStreamSequence(),
					fixture.state.Hash(),
					fixture.state.NextStreamSequence(),
				)
			}
			requireFrozenLeverage(
				t,
				restarted.FillsForOrder(orderID),
				"",
			)
		})
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:952
//	test: fill_surfaces_frozen_effective_leverage
//
// Adaptations:
//   - The source's frozen 5.00 value is produced by deterministic v4 execution.
//   - Fixed hashes bind the canonical 5 value into the v4 effects/state chain.
//
// Assertions preserved:
//   - Frozen 5.00x leverage surfaces and serializes canonically as 5.
func TestTradingFillLeverageV4HashGolden(t *testing.T) {
	fixture := newFrozenLeverageFixture(t, 3)
	fixture.apply(t, configureFrozenLeverage("5.00"))
	fixture.version = frozenLeverageDecisionHashVersion
	decision := fixture.apply(
		t,
		submitFrozenLeverageOrder(fixture.id(304), SideBuy, false, ID{}),
	)
	if decision.DecisionHashVersion != frozenLeverageDecisionHashVersion {
		t.Fatalf(
			"decision hash version = %d, want %d",
			decision.DecisionHashVersion,
			frozenLeverageDecisionHashVersion,
		)
	}
	requireFrozenLeverage(t, decision.Fills, "5")
	requireFrozenLeverageHash(
		t,
		"input",
		decision.InputHash,
		"37460356d53c08a041e7fabaf1843d822bf83614f4d017908e471e3c2a02ec21",
	)
	requireFrozenLeverageHash(
		t,
		"effects",
		decision.EffectsHash,
		"72d541cda1c0ca9754efeec5d26c60d793611d8ee79b486a7c9a900d07a566e2",
	)
	requireFrozenLeverageHash(
		t,
		"decision",
		decision.DecisionHash,
		"23ed67b8c19c4411866d31aadaa3863e7d5fbe5bfaa8e06def1281973dc8eecc",
	)
	requireFrozenLeverageHash(
		t,
		"state",
		decision.NextStateHash,
		"ee350eaacc54e4b6e19d681feae2e895e1e0c45769621ce77c75c00d0d9a9c17",
	)
}

type frozenLeverageFixture struct {
	state          State
	namespace      ID
	sequence       uint64
	marketSequence uint64
	logicalTime    LogicalTime
	version        uint32
	inputs         []InputEnvelope
	actions        []TradingAction
	lastInput      InputEnvelope
	lastAction     TradingAction
}

func newFrozenLeverageFixture(
	t *testing.T,
	version uint32,
) *frozenLeverageFixture {
	t.Helper()

	fixture := &frozenLeverageFixture{
		state:     NewState(17),
		namespace: mustID(t, "019f9460-4b36-4e9b-8f44-682611f7ee01"),
		sequence:  1,
		logicalTime: NewLogicalTime(
			time.Date(2026, time.July, 26, 10, 0, 0, 0, time.UTC),
		),
		version: version,
	}
	fixture.apply(t, configureFrozenLeverageInstrument(1, "10"))
	fixture.apply(t, TradingAction{
		Kind: TradingActionAdjustBalance,
		AdjustBalance: &AdjustBalance{
			AccountID:     "account-1",
			Currency:      "USDC",
			CurrencyScale: 8,
			Operation:     BalanceOperationSet,
			Amount:        "1000000",
		},
	})
	fixture.apply(t, TradingAction{
		Kind: TradingActionUpdateBook,
		UpdateBook: &UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "100",
			Bids:         []BookLevel{{Price: "99", Quantity: "10"}},
			Asks:         []BookLevel{{Price: "100", Quantity: "10"}},
		},
	})
	return fixture
}

func (fixture *frozenLeverageFixture) id(sequence uint64) ID {
	return IDFromSequence(fixture.namespace, sequence)
}

func (fixture *frozenLeverageFixture) apply(
	t *testing.T,
	action TradingAction,
) Decision {
	t.Helper()
	decision := fixture.applyResult(t, action)
	if decision.CommandResult.Status != CommandStatusAccepted {
		t.Fatalf(
			"apply %s at decision v%d rejected: %s",
			action.Kind,
			fixture.version,
			decision.CommandResult.Reason,
		)
	}
	return decision
}

func (fixture *frozenLeverageFixture) applyResult(
	t *testing.T,
	action TradingAction,
) Decision {
	t.Helper()

	payload, err := EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("encode %s: %v", action.Kind, err)
	}
	kind := InputKindCommand
	switch action.Kind {
	case TradingActionUpdateBook:
		kind = InputKindMarket
		fixture.marketSequence++
	case TradingActionSettleFunding, TradingActionLiquidateAccount:
		kind = InputKindTimer
	case TradingActionConfigureInstrument,
		TradingActionConfigureAccount,
		TradingActionConfigureRisk,
		TradingActionAdjustBalance,
		TradingActionSubmitOrder,
		TradingActionPlaceBracket,
		TradingActionAmendOrder,
		TradingActionCancelOrder:
	}
	input := InputEnvelope{
		InputID:              fixture.id(1_000 + fixture.sequence),
		SchemaVersion:        CurrentSchemaVersion,
		ShardID:              fixture.state.ShardID(),
		Kind:                 kind,
		SourceID:             "frozen-leverage-test",
		SourceSequence:       fixture.sequence,
		StreamSequence:       fixture.sequence,
		MarketSequence:       fixture.marketSequence,
		LogicalTime:          fixture.logicalTime,
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}

	next, decision, err :=
		ApplyTradingWithReceiptsAtDecisionHashVersion(
			fixture.state,
			input,
			action,
			nil,
			fixture.version,
		)
	if err != nil {
		t.Fatalf(
			"apply %s at decision v%d: %v",
			action.Kind,
			fixture.version,
			err,
		)
	}
	fixture.state = next
	fixture.inputs = append(fixture.inputs, input)
	fixture.actions = append(fixture.actions, action)
	fixture.lastInput = input
	fixture.lastAction = action
	fixture.sequence++
	fixture.logicalTime++
	return decision
}

func configureFrozenLeverageInstrument(
	revision uint64,
	maxLeverage string,
) TradingAction {
	return TradingAction{
		Kind: TradingActionConfigureInstrument,
		ConfigureInstrument: &ConfigureInstrument{
			InstrumentID:            "BTC-PERP",
			Revision:                revision,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 8,
			InitialMarginRate:       "1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             maxLeverage,
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	}
}

func configureFrozenLeverage(leverage string) TradingAction {
	return TradingAction{
		Kind: TradingActionConfigureRisk,
		ConfigureRisk: &ConfigureRisk{
			AccountID:    "account-1",
			InstrumentID: "BTC-PERP",
			MarginMode:   MarginModeCross,
			Leverage:     leverage,
		},
	}
}

func submitFrozenLeverageOrder(
	orderID ID,
	side Side,
	reduceOnly bool,
	positionID ID,
) TradingAction {
	return TradingAction{
		Kind: TradingActionSubmitOrder,
		SubmitOrder: &SubmitOrder{
			OrderID:      orderID,
			AccountID:    "account-1",
			InstrumentID: "BTC-PERP",
			Side:         side,
			Type:         OrderTypeMarket,
			TimeInForce:  TimeInForceGTC,
			Quantity:     "1",
			ReduceOnly:   reduceOnly,
			PositionID:   positionID,
		},
	}
}

func requireFrozenLeverage(
	t *testing.T,
	fills []FillSnapshot,
	want string,
) {
	t.Helper()
	if len(fills) != 1 {
		t.Fatalf("fills = %d, want 1", len(fills))
	}
	if got := fills[0].EffectiveLeverage; got != want {
		t.Fatalf(
			"fill %s effective leverage = %q, want %q",
			fills[0].FillID,
			got,
			want,
		)
	}
}

func requireFrozenLeverageHash(
	t *testing.T,
	name string,
	got Hash,
	want string,
) {
	t.Helper()
	if got.String() != want {
		t.Fatalf("%s hash = %s, want %s", name, got, want)
	}
}
