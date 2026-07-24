package engine

import (
	"testing"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_fill_taxonomy.rs:159
//	test: fill_type_is_classified_open_increase_reduce_flip_close
//
// Adaptations:
//   - Database polling is replaced by synchronous position snapshots.
//   - Decimal assertions are exact and include the cumulative realized result.
//
// Assertions preserved:
//   - Fills classify open, increase, reduce, flip, and close in order.
//   - Net signed quantity follows +2, +3, +2, -2, zero.
//   - Per-fill realized deltas telescope to the position total.
func TestTradingFillTypeIsClassifiedOpenIncreaseReduceFlipClose(t *testing.T) {
	fixture := newTradingFixture(t)
	accountID := "account-1"
	var positionID ID
	var realizedTotal string
	var realizedSum decimal.Decimal
	steps := []struct {
		sequence uint64
		side     Side
		quantity string
		bid      string
		ask      string
		effect   PositionEffect
		signed   string
	}{
		{700, SideBuy, "2", "99", "100", PositionEffectOpen, "2"},
		{701, SideBuy, "1", "109", "110", PositionEffectIncrease, "3"},
		{702, SideSell, "1", "120", "121", PositionEffectReduce, "2"},
		{703, SideSell, "4", "120", "121", PositionEffectFlip, "-2"},
		{704, SideBuy, "2", "119", "120", PositionEffectClose, "0"},
	}
	for index, step := range steps {
		fixture.updateBook(t, step.ask,
			[]BookLevel{{Price: step.bid, Quantity: "10"}},
			[]BookLevel{{Price: step.ask, Quantity: "10"}},
		)
		command := marketOrder(
			fixture.id(step.sequence),
			accountID,
			step.side,
			step.quantity,
			nil,
		)
		_, decision := fixture.submitDecision(t, command)
		if len(decision.Fills) != 1 {
			t.Fatalf("step %d fills = %+v, want one", index, decision.Fills)
		}
		fill := decision.Fills[0]
		if fill.RealizedPnL != "" {
			delta, err := decimal.Parse(fill.RealizedPnL)
			if err != nil {
				t.Fatalf("step %d realized PnL %q: %v", index, fill.RealizedPnL, err)
			}
			realizedSum, err = realizedSum.Add(delta)
			if err != nil {
				t.Fatalf("step %d sum realized PnL: %v", index, err)
			}
		}
		if fill.PositionEffect != step.effect {
			t.Fatalf("step %d effect = %s, want %s", index, fill.PositionEffect, step.effect)
		}
		if index == 0 {
			positionID = fill.PositionID
		} else if fill.PositionID != positionID {
			t.Fatalf("step %d position = %s, want stable %s", index, fill.PositionID, positionID)
		}
		position, ok := fixture.state.Position(positionID)
		if !ok || position.SignedQuantity != step.signed {
			t.Fatalf("step %d position = %+v, want signed %s", index, position, step.signed)
		}
		if (index == 1 || index == 2) && position.AverageOpenPrice != "103.33" {
			t.Fatalf(
				"step %d average open price = %s, want stable rounded 103.33",
				index,
				position.AverageOpenPrice,
			)
		}
		realizedTotal = position.RealizedPnL
	}
	if realizedTotal != "50.01" {
		t.Fatalf("closed position realized PnL = %s, want 50.01", realizedTotal)
	}
	if realizedSum.String() != realizedTotal {
		t.Fatalf("per-fill realized sum = %s, position total = %s", realizedSum, realizedTotal)
	}
	position, _ := fixture.state.Position(positionID)
	if position.Status != PositionStatusClosed {
		t.Fatalf("final position status = %s, want closed", position.Status)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_hedging.rs:77
//	test: hedging_keeps_every_leg_separate_across_both_fill_paths
//
// Adaptations:
//   - Explicit account OMS configuration replaces live-stack boot options.
//   - Market and marketable-limit paths use deterministic B-book inputs.
//
// Assertions preserved:
//   - Four fills create four distinct open position IDs.
//   - Two long and two short legs coexist.
func TestTradingHedgingKeepsEveryLegSeparateAcrossBothFillPaths(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.configureAccount(t, "account-1", OmsModeHedging)

	commands := []SubmitOrder{
		marketableOrder(fixture.id(710), "account-1", SideBuy, "1", "1000"),
		marketableOrder(fixture.id(711), "account-1", SideSell, "1", "1"),
		marketOrder(fixture.id(712), "account-1", SideBuy, "1", nil),
		marketOrder(fixture.id(713), "account-1", SideSell, "1", nil),
	}
	for _, command := range commands {
		fixture.submit(t, command)
	}

	open := fixture.state.OpenPositions("account-1")
	if len(open) != 4 {
		t.Fatalf("open hedging positions = %+v, want four", open)
	}
	ids := make(map[ID]struct{}, len(open))
	longs := 0
	shorts := 0
	for _, position := range open {
		ids[position.PositionID] = struct{}{}
		switch position.Side {
		case PositionSideLong:
			longs++
		case PositionSideShort:
			shorts++
		}
	}
	if len(ids) != 4 || longs != 2 || shorts != 2 {
		t.Fatalf("hedging positions distinct=%d longs=%d shorts=%d", len(ids), longs, shorts)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_close_hedged_leg.rs:63
//	test: close_by_position_id_closes_only_that_leg
//
// Adaptations:
//   - ClosePosition is represented by a targeted reduce-only market order.
//
// Assertions preserved:
//   - Closing the long position ID closes only that leg.
//   - The short leg ID and signed quantity remain unchanged.
func TestTradingCloseByPositionIDClosesOnlyThatLeg(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.configureAccount(t, "account-1", OmsModeHedging)
	longOrder := fixture.submit(t, marketOrder(fixture.id(720), "account-1", SideBuy, "1", nil))
	fixture.submit(t, marketOrder(fixture.id(721), "account-1", SideSell, "1", nil))
	longPositionID := fixture.state.FillsForOrder(longOrder.OrderID)[0].PositionID
	before := fixture.state.OpenPositions("account-1")
	var short PositionSnapshot
	for _, position := range before {
		if position.Side == PositionSideShort {
			short = position
		}
	}

	closeCommand := marketOrder(fixture.id(722), "account-1", SideSell, "2", nil)
	closeCommand.ReduceOnly = true
	closeCommand.PositionID = longPositionID
	fixture.submit(t, closeCommand)

	after := fixture.state.OpenPositions("account-1")
	if len(after) != 1 || after[0].PositionID != short.PositionID ||
		after[0].SignedQuantity != short.SignedQuantity {
		t.Fatalf("remaining hedged legs = %+v, want unchanged short %+v", after, short)
	}
	closed, ok := fixture.state.Position(longPositionID)
	if !ok || closed.Status != PositionStatusClosed {
		t.Fatalf("target long = %+v, want closed", closed)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_reduce_only_clamp.rs:69
//	test: reduce_only_close_larger_than_position_clamps_to_flat_never_flips
//
// Adaptations:
//   - Retry loops are replaced by two synchronous market decisions.
//
// Assertions preserved:
//   - Oversized reduce-only quantity is clamped to the open quantity.
//   - The position becomes flat and never flips short.
func TestTradingReduceOnlyCloseLargerThanPositionClampsToFlatNeverFlips(t *testing.T) {
	fixture := newTradingFixture(t)
	open := fixture.submit(t, marketOrder(fixture.id(730), "account-1", SideBuy, "1", nil))
	positionID := fixture.state.FillsForOrder(open.OrderID)[0].PositionID

	closeCommand := marketOrder(fixture.id(731), "account-1", SideSell, "2", nil)
	closeCommand.ReduceOnly = true
	closed := fixture.submit(t, closeCommand)
	if closed.Quantity != "1" || closed.FilledQuantity != "1" {
		t.Fatalf("clamped order = %+v, want 1 filled of effective quantity 1", closed)
	}
	if open := fixture.state.OpenPositions("account-1"); len(open) != 0 {
		t.Fatalf("open positions after reduce-only clamp = %+v, want none", open)
	}
	position, _ := fixture.state.Position(positionID)
	if position.Status != PositionStatusClosed || position.SignedQuantity != "0" {
		t.Fatalf("clamped position = %+v, want closed flat", position)
	}
}

func TestTradingRestingReduceOnlyReclampsAtExecution(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.submit(t, marketOrder(fixture.id(735), "account-1", SideBuy, "2", nil))

	restingCommand := marketableOrder(
		fixture.id(736),
		"account-1",
		SideSell,
		"2",
		"200",
	)
	restingCommand.ReduceOnly = true
	resting := fixture.submit(t, restingCommand)
	assertOrderStatus(t, resting, OrderStatusWorking)

	fixture.submit(t, marketOrder(fixture.id(737), "account-1", SideSell, "1", nil))
	open := fixture.state.OpenPositions("account-1")
	if len(open) != 1 || open[0].SignedQuantity != "1" {
		t.Fatalf("position before resting execution = %+v, want long 1", open)
	}

	fixture.updateBook(t, "201",
		[]BookLevel{{Price: "200", Quantity: "10"}},
		[]BookLevel{{Price: "201", Quantity: "10"}},
	)
	resting, _ = fixture.state.Order(resting.OrderID)
	if resting.Status != OrderStatusFilled ||
		resting.Quantity != "1" ||
		resting.FilledQuantity != "1" {
		t.Fatalf("reclamped resting order = %+v, want filled effective quantity 1", resting)
	}
	if open := fixture.state.OpenPositions("account-1"); len(open) != 0 {
		t.Fatalf("resting reduce-only order flipped position: %+v", open)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_quote_injection.rs:48
//	test: injected_quote_fills_resting_limit
//
// Adaptations:
//   - Quote injection is one ordered deterministic book update.
//   - Precision is strengthened from a digit-count bound to exact price text.
//
// Assertions preserved:
//   - A crossing quote fills the resting limit.
//   - The projected position average is instrument-precision-safe.
func TestTradingInjectedQuoteFillsRestingLimitWithExactPositionAverage(t *testing.T) {
	fixture := newTradingFixture(t)
	order := fixture.submit(t, marketableOrder(
		fixture.id(740),
		"account-1",
		SideBuy,
		"1",
		"20",
	))
	assertOrderStatus(t, order, OrderStatusWorking)
	fixture.updateBook(t, "10",
		[]BookLevel{{Price: "9", Quantity: "10"}},
		[]BookLevel{{Price: "10", Quantity: "10"}},
	)
	order, _ = fixture.state.Order(order.OrderID)
	assertOrderStatus(t, order, OrderStatusFilled)
	positions := fixture.state.OpenPositions("account-1")
	if len(positions) != 1 || positions[0].AverageOpenPrice != "10" {
		t.Fatalf("position after injected quote = %+v, want exact average 10", positions)
	}
}

func TestTradingNettingPositionsAreIsolatedPerAccount(t *testing.T) {
	fixture := newTradingFixture(t)
	first := fixture.submit(t, marketOrder(fixture.id(750), "account-1", SideBuy, "1", nil))
	second := fixture.submit(t, marketOrder(fixture.id(751), "account-2", SideSell, "2", nil))
	firstPosition := fixture.state.FillsForOrder(first.OrderID)[0].PositionID
	secondPosition := fixture.state.FillsForOrder(second.OrderID)[0].PositionID
	if firstPosition == secondPosition {
		t.Fatal("different accounts shared a position ID")
	}
	if got := fixture.state.OpenPositions("account-1"); len(got) != 1 ||
		got[0].SignedQuantity != "1" {
		t.Fatalf("account-1 positions = %+v", got)
	}
	if got := fixture.state.OpenPositions("account-2"); len(got) != 1 ||
		got[0].SignedQuantity != "-2" {
		t.Fatalf("account-2 positions = %+v", got)
	}
}

func marketableOrder(
	orderID ID,
	accountID string,
	side Side,
	quantity string,
	price string,
) SubmitOrder {
	return SubmitOrder{
		OrderID:      orderID,
		AccountID:    accountID,
		InstrumentID: "BTC-PERP",
		Side:         side,
		Type:         OrderTypeLimit,
		TimeInForce:  TimeInForceGTC,
		Quantity:     quantity,
		Price:        price,
	}
}

func (fixture *tradingFixture) configureAccount(
	t *testing.T,
	accountID string,
	mode OmsMode,
) {
	t.Helper()
	fixture.apply(t, TradingAction{
		Kind: TradingActionConfigureAccount,
		ConfigureAccount: &ConfigureAccount{
			AccountID: accountID,
			OmsMode:   mode,
		},
	})
}
