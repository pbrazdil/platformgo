package engine

import "testing"

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket.rs:32
//	test: resting_limit_bracket_rests_working_with_held_sl_tp
func TestSourceRestingLimitBracketRestsWithHeldProtection(t *testing.T) {
	fixture := newTradingFixture(t)
	ids := bracketIDs(fixture, 800)
	fixture.placeBracket(t, PlaceBracket{
		BracketID: ids.bracket, EntryOrderID: ids.entry,
		AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideBuy, EntryType: OrderTypeLimit, TimeInForce: TimeInForceGTC,
		Quantity: "1", EntryPrice: "90",
		TakeProfits: []ProtectiveLeg{{
			OrderID: ids.tp1, Price: "110", Quantity: "1",
		}},
		StopLossOrderID: ids.stop, StopLoss: "80",
	})
	entry, _ := fixture.state.Order(ids.entry)
	takeProfit, _ := fixture.state.Order(ids.tp1)
	stop, _ := fixture.state.Order(ids.stop)
	assertOrderStatus(t, entry, OrderStatusWorking)
	assertOrderStatus(t, takeProfit, OrderStatusHeld)
	assertOrderStatus(t, stop, OrderStatusHeld)
	if entry.BracketLeg != BracketLegEntry ||
		takeProfit.BracketLeg != BracketLegTakeProfit ||
		stop.BracketLeg != BracketLegStopLoss ||
		takeProfit.Side != SideSell || stop.Side != SideSell ||
		takeProfit.Price != "110" || stop.TriggerPrice != "80" {
		t.Fatalf("bracket legs = entry %+v tp %+v stop %+v", entry, takeProfit, stop)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket.rs:101
//	test: market_entry_bracket_arms_reduce_only_exits
func TestSourceMarketEntryBracketArmsReduceOnlyExits(t *testing.T) {
	fixture := newTradingFixture(t)
	ids := bracketIDs(fixture, 810)
	fixture.placeBracket(t, basicMarketBracket(ids, "account-1"))
	entry, _ := fixture.state.Order(ids.entry)
	takeProfit, _ := fixture.state.Order(ids.tp1)
	stop, _ := fixture.state.Order(ids.stop)
	assertOrderStatus(t, entry, OrderStatusFilled)
	assertOrderStatus(t, takeProfit, OrderStatusWorking)
	assertOrderStatus(t, stop, OrderStatusWorking)
	if !takeProfit.ReduceOnly || !stop.ReduceOnly ||
		takeProfit.PositionID.IsZero() ||
		takeProfit.PositionID != stop.PositionID {
		t.Fatalf("protective targets = tp %+v stop %+v", takeProfit, stop)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket_ladder.rs:106
//	test: scale_out_ladder_tp1_fill_reduces_sl_then_sl_closes_remainder
func TestSourceScaleOutLadderReconcilesProtection(t *testing.T) {
	fixture := newTradingFixture(t)
	ids := bracketIDs(fixture, 820)
	fixture.placeBracket(t, ladderMarketBracket(ids, "account-1"))

	updateTriggerQuote(t, fixture, "110", "111")
	tp1, _ := fixture.state.Order(ids.tp1)
	tp2, _ := fixture.state.Order(ids.tp2)
	stop, _ := fixture.state.Order(ids.stop)
	assertOrderStatus(t, tp1, OrderStatusFilled)
	assertOrderStatus(t, tp2, OrderStatusWorking)
	assertOrderStatus(t, stop, OrderStatusWorking)
	if stop.Quantity != "2" || tp2.Quantity != "2" {
		t.Fatalf("remaining protection = tp2 %s stop %s, want 2 each", tp2.Quantity, stop.Quantity)
	}

	updateTriggerQuote(t, fixture, "79", "80")
	stop, _ = fixture.state.Order(ids.stop)
	tp2, _ = fixture.state.Order(ids.tp2)
	assertOrderStatus(t, stop, OrderStatusFilled)
	assertOrderStatus(t, tp2, OrderStatusCancelled)
	if got := fixture.state.OpenPositions("account-1"); len(got) != 0 {
		t.Fatalf("open positions after stop = %+v, want flat", got)
	}
	fills := fixture.state.FillsForOrder(ids.stop)
	if len(fills) != 1 || fills[0].Quantity != "2" {
		t.Fatalf("stop fills = %+v, want remaining 2", fills)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket_ladder.rs:259
//	test: hedging_ladder_reduces_only_its_own_position
func TestSourceHedgingLadderTargetsOnlyItsEntryPosition(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.apply(t, TradingAction{
		Kind: TradingActionConfigureAccount,
		ConfigureAccount: &ConfigureAccount{
			AccountID: "account-1", OmsMode: OmsModeHedging,
		},
	})
	unrelated := fixture.submit(t, SubmitOrder{
		OrderID: fixture.id(830), AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideBuy, Type: OrderTypeMarket, TimeInForce: TimeInForceGTC,
		Quantity: "2",
	})
	assertOrderStatus(t, unrelated, OrderStatusFilled)
	unrelatedPosition := fixture.state.OpenPositions("account-1")[0].PositionID

	ids := bracketIDs(fixture, 840)
	fixture.placeBracket(t, ladderMarketBracket(ids, "account-1"))
	updateTriggerQuote(t, fixture, "110", "111")
	positions := fixture.state.OpenPositions("account-1")
	if len(positions) != 2 ||
		positions[0].SignedQuantity != "2" ||
		positions[1].SignedQuantity != "2" {
		t.Fatalf("positions after TP1 = %+v, want two independent quantity-2 legs", positions)
	}
	updateTriggerQuote(t, fixture, "79", "80")
	positions = fixture.state.OpenPositions("account-1")
	if len(positions) != 1 ||
		positions[0].PositionID != unrelatedPosition ||
		positions[0].SignedQuantity != "2" {
		t.Fatalf("surviving positions = %+v, want unrelated leg", positions)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket_ladder.rs:451
//	test: full_coverage_bracket_sl_fill_oco_cancels_tp
func TestSourceFullCoverageStopOCOCancelsTakeProfit(t *testing.T) {
	fixture := newTradingFixture(t)
	ids := bracketIDs(fixture, 850)
	fixture.placeBracket(t, basicMarketBracket(ids, "account-1"))
	updateTriggerQuote(t, fixture, "79", "80")
	stop, _ := fixture.state.Order(ids.stop)
	takeProfit, _ := fixture.state.Order(ids.tp1)
	assertOrderStatus(t, stop, OrderStatusFilled)
	assertOrderStatus(t, takeProfit, OrderStatusCancelled)
}

type sourceBracketIDs struct {
	bracket ID
	entry   ID
	tp1     ID
	tp2     ID
	stop    ID
}

func bracketIDs(fixture *tradingFixture, base uint64) sourceBracketIDs {
	return sourceBracketIDs{
		bracket: fixture.id(base),
		entry:   fixture.id(base + 1),
		tp1:     fixture.id(base + 2),
		tp2:     fixture.id(base + 3),
		stop:    fixture.id(base + 4),
	}
}

func basicMarketBracket(ids sourceBracketIDs, accountID string) PlaceBracket {
	return PlaceBracket{
		BracketID: ids.bracket, EntryOrderID: ids.entry,
		AccountID: accountID, InstrumentID: "BTC-PERP",
		Side: SideBuy, EntryType: OrderTypeMarket, TimeInForce: TimeInForceGTC,
		Quantity: "1",
		TakeProfits: []ProtectiveLeg{{
			OrderID: ids.tp1, Price: "110", Quantity: "1",
		}},
		StopLossOrderID: ids.stop, StopLoss: "80",
	}
}

func ladderMarketBracket(ids sourceBracketIDs, accountID string) PlaceBracket {
	return PlaceBracket{
		BracketID: ids.bracket, EntryOrderID: ids.entry,
		AccountID: accountID, InstrumentID: "BTC-PERP",
		Side: SideBuy, EntryType: OrderTypeMarket, TimeInForce: TimeInForceGTC,
		Quantity: "3",
		TakeProfits: []ProtectiveLeg{
			{OrderID: ids.tp1, Price: "110", Quantity: "1"},
			{OrderID: ids.tp2, Price: "120", Quantity: "2"},
		},
		StopLossOrderID: ids.stop, StopLoss: "80",
	}
}

func (fixture *tradingFixture) placeBracket(t *testing.T, bracket PlaceBracket) {
	t.Helper()
	fixture.apply(t, TradingAction{
		Kind: TradingActionPlaceBracket, PlaceBracket: &bracket,
	})
}
