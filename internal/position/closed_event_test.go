package position

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func testClosedEventFixture() PositionClosed {
	closingOrderID := ids.MustClientOrderID("O-19700101-000000-001-001-2")
	averageClose := decimal.MustParse("1.0600")
	realizedPnL := cash("112.50", usd)
	tsClosed := uint64(4_600_000_000)
	return PositionClosed{
		TraderID:       ids.MustTraderID("TRADER-001"),
		StrategyID:     ids.MustStrategyID("EMA-CROSS"),
		InstrumentID:   ids.MustInstrumentID("EURUSD.SIM"),
		PositionID:     ids.MustPositionID("P-001"),
		AccountID:      ids.MustAccountID("SIM-001"),
		OpeningOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		ClosingOrderID: &closingOrderID,
		Entry:          Buy,
		Side:           Flat,
		SignedQuantity: decimal.MustParse("0.0"),
		Quantity:       decimal.MustQuantity("0"),
		PeakQuantity:   decimal.MustQuantity("150"),
		LastQuantity:   decimal.MustQuantity("150"),
		LastPrice:      decimal.MustPrice("1.0600"),
		Currency:       usd,
		AverageOpen:    decimal.MustParse("1.0525"),
		AverageClose:   &averageClose,
		RealizedReturn: decimal.MustParse("0.0071"),
		RealizedPnL:    &realizedPnL,
		UnrealizedPnL:  cash("0.0", usd),
		Duration:       3_600_000_000_000,
		EventID:        nilEventID,
		TsOpened:       1_000_000_000,
		TsClosed:       &tsClosed,
		TsEvent:        4_600_000_000,
		TsInit:         5_000_000_000,
	}
}

func testPositionCloseFill() PositionCloseFill {
	return PositionCloseFill{
		StrategyID:    ids.MustStrategyID("EMA-CROSS"),
		InstrumentID:  ids.MustInstrumentID("EURUSD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-2"),
		VenueOrderID:  ids.MustVenueOrderID("2"),
		TradeID:       ids.MustTradeID("T-002"),
		PositionID:    ids.MustPositionID("P-001"),
		Side:          Sell,
		LastQuantity:  decimal.MustQuantity("150"),
		LastPrice:     decimal.MustPrice("1.0600"),
		Commission:    cash("2.5", usd),
		TsEvent:       4_600_000_000,
		TsInit:        5_000_000_000,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/closed.rs:202
//	test: test_position_closed_create
func TestPositionClosedCreate(t *testing.T) {
	position := testSnapshotPosition(t)
	closingFill := testPositionCloseFill()
	eventID := nilEventID
	tsInit := uint64(6_000_000_000)

	closed := CreatePositionClosed(position, testSnapshotIdentity(), closingFill, eventID, tsInit)

	if closed.TraderID != ids.MustTraderID("TRADER-001") {
		t.Fatalf("trader ID = %s", closed.TraderID)
	}
	if closed.StrategyID != ids.MustStrategyID("EMA-CROSS") {
		t.Fatalf("strategy ID = %s", closed.StrategyID)
	}
	if closed.InstrumentID != ids.MustInstrumentID(position.Instrument.ID) {
		t.Fatalf("instrument ID = %s", closed.InstrumentID)
	}
	if closed.PositionID != ids.MustPositionID(position.ID) {
		t.Fatalf("position ID = %s", closed.PositionID)
	}
	if closed.AccountID != ids.MustAccountID("SIM-001") {
		t.Fatalf("account ID = %s", closed.AccountID)
	}
	if closed.OpeningOrderID != ids.MustClientOrderID(position.OpeningOrderID) ||
		closed.ClosingOrderID != nil {
		t.Fatalf("unexpected order IDs")
	}
	if closed.Entry != position.Entry || closed.Side != position.Side {
		t.Fatalf("entry/side = %s/%s", closed.Entry, closed.Side)
	}
	if !closed.SignedQuantity.Equal(position.SignedQuantity) ||
		closed.Quantity.String() != position.Quantity.String() ||
		closed.PeakQuantity.String() != position.PeakQuantity.String() {
		t.Fatalf("position quantities differ")
	}
	if !closed.LastQuantity.Equal(closingFill.LastQuantity) ||
		!closed.LastPrice.Equal(closingFill.LastPrice) {
		t.Fatalf("last fill values differ")
	}
	if !closed.Currency.Equal(position.Instrument.QuoteCurrency) ||
		!closed.AverageOpen.Equal(position.AverageOpen) ||
		closed.AverageClose != nil ||
		!closed.RealizedReturn.Equal(position.RealizedReturn) {
		t.Fatalf("price or return fields differ")
	}
	if closed.RealizedPnL == nil || position.RealizedPnL == nil ||
		!closed.RealizedPnL.Equal(*position.RealizedPnL) ||
		!closed.UnrealizedPnL.Equal(cash("0.0", position.Instrument.QuoteCurrency)) {
		t.Fatalf("PnL fields differ")
	}
	if closed.Duration != position.Duration || closed.EventID != eventID ||
		closed.TsOpened != position.TsOpened || closed.TsClosed != nil ||
		closed.TsEvent != closingFill.TsEvent || closed.TsInit != tsInit {
		t.Fatalf("event timing differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/closed.rs:257
//	test: test_position_closed_flat_position
func TestPositionClosedFlatPosition(t *testing.T) {
	closed := testClosedEventFixture()
	if closed.Side != Flat {
		t.Fatalf("side = %s, want FLAT", closed.Side)
	}
	if !closed.SignedQuantity.Equal(decimal.MustParse("0.0")) {
		t.Fatalf("signed quantity = %s", closed.SignedQuantity)
	}
	if !closed.Quantity.Equal(decimal.MustQuantity("0")) {
		t.Fatalf("quantity = %s", closed.Quantity)
	}
	if !closed.UnrealizedPnL.Equal(cash("0.0", usd)) {
		t.Fatalf("unrealized PnL = %s", closed.UnrealizedPnL)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/closed.rs:270
//	test: test_position_closed_loss_scenario
func TestPositionClosedLossScenario(t *testing.T) {
	closed := testClosedEventFixture()
	averageClose := decimal.MustParse("1.0400")
	realizedPnL := cash("-187.50", usd)
	closed.AverageClose = &averageClose
	closed.RealizedReturn = decimal.MustParse("-0.0119")
	closed.RealizedPnL = &realizedPnL

	if closed.AverageClose == nil || !closed.AverageClose.Equal(decimal.MustParse("1.0400")) {
		t.Fatalf("average close = %v", closed.AverageClose)
	}
	if closed.RealizedReturn.Sign() >= 0 {
		t.Fatalf("realized return = %s, want negative", closed.RealizedReturn)
	}
	if closed.RealizedPnL == nil || !closed.RealizedPnL.Equal(cash("-187.50", usd)) {
		t.Fatalf("realized PnL = %v", closed.RealizedPnL)
	}
}
