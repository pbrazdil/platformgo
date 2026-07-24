package position

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

const nilEventID EventID = "00000000-0000-0000-0000-000000000000"

func testPositionOpenedEvent() PositionOpened {
	return PositionOpened{
		TraderID:       ids.MustTraderID("TRADER-001"),
		StrategyID:     ids.MustStrategyID("EMA-CROSS"),
		InstrumentID:   ids.MustInstrumentID("EURUSD.SIM"),
		PositionID:     ids.MustPositionID("P-001"),
		AccountID:      ids.MustAccountID("SIM-001"),
		OpeningOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Entry:          Buy,
		Side:           Long,
		SignedQuantity: decimal.MustParse("100.0"),
		Quantity:       decimal.MustQuantity("100"),
		LastQuantity:   decimal.MustQuantity("100"),
		LastPrice:      decimal.MustPrice("1.0500"),
		Currency:       usd,
		AverageOpen:    decimal.MustParse("1.0500"),
		EventID:        nilEventID,
		TsEvent:        1_000_000_000,
		TsInit:         2_000_000_000,
	}
}

func testPositionChangedEvent() PositionChanged {
	return PositionChanged{
		TraderID:       ids.MustTraderID("TRADER-001"),
		StrategyID:     ids.MustStrategyID("EMA-CROSS"),
		InstrumentID:   ids.MustInstrumentID("EURUSD.SIM"),
		PositionID:     ids.MustPositionID("P-001"),
		AccountID:      ids.MustAccountID("SIM-001"),
		OpeningOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Entry:          Buy,
		Side:           Long,
		SignedQuantity: decimal.MustParse("150.0"),
		Quantity:       decimal.MustQuantity("150"),
		PeakQuantity:   decimal.MustQuantity("150"),
		LastQuantity:   decimal.MustQuantity("50"),
		LastPrice:      decimal.MustPrice("1.0550"),
		Currency:       usd,
		AverageOpen:    decimal.MustParse("1.0525"),
		AverageClose:   nil,
		RealizedReturn: decimal.MustParse("0.0"),
		RealizedPnL:    nil,
		UnrealizedPnL:  cash("75.0", usd),
		EventID:        nilEventID,
		TsOpened:       1_000_000_000,
		TsEvent:        1_500_000_000,
		TsInit:         2_500_000_000,
	}
}

func testPositionClosedEvent() PositionClosed {
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

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/mod.rs:153
//	test: test_position_event_opened_instrument_id
func TestPositionEventOpenedInstrumentID(t *testing.T) {
	opened := testPositionOpenedEvent()
	event := NewPositionOpenedEvent(opened)
	if got, want := event.InstrumentID(), ids.MustInstrumentID("EURUSD.SIM"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/mod.rs:161
//	test: test_position_event_changed_instrument_id
func TestPositionEventChangedInstrumentID(t *testing.T) {
	changed := testPositionChangedEvent()
	event := NewPositionChangedEvent(changed)
	if got, want := event.InstrumentID(), ids.MustInstrumentID("EURUSD.SIM"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/mod.rs:169
//	test: test_position_event_closed_instrument_id
func TestPositionEventClosedInstrumentID(t *testing.T) {
	closed := testPositionClosedEvent()
	event := NewPositionClosedEvent(closed)
	if got, want := event.InstrumentID(), ids.MustInstrumentID("EURUSD.SIM"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/mod.rs:177
//	test: test_position_event_opened_account_id
func TestPositionEventOpenedAccountID(t *testing.T) {
	opened := testPositionOpenedEvent()
	event := NewPositionOpenedEvent(opened)
	if got, want := event.AccountID(), ids.MustAccountID("SIM-001"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/mod.rs:185
//	test: test_position_event_changed_account_id
func TestPositionEventChangedAccountID(t *testing.T) {
	changed := testPositionChangedEvent()
	event := NewPositionChangedEvent(changed)
	if got, want := event.AccountID(), ids.MustAccountID("SIM-001"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/mod.rs:193
//	test: test_position_event_closed_account_id
func TestPositionEventClosedAccountID(t *testing.T) {
	closed := testPositionClosedEvent()
	event := NewPositionClosedEvent(closed)
	if got, want := event.AccountID(), ids.MustAccountID("SIM-001"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/mod.rs:201
//	test: test_position_event_enum_variants
func TestPositionEventEnumVariants(t *testing.T) {
	opened := testPositionOpenedEvent()
	changed := testPositionChangedEvent()
	closed := testPositionClosedEvent()

	eventOpened := NewPositionOpenedEvent(opened)
	eventChanged := NewPositionChangedEvent(changed)
	eventClosed := NewPositionClosedEvent(closed)

	if _, ok := eventOpened.Opened(); !ok {
		t.Fatal("expected PositionOpened variant")
	}
	if _, ok := eventChanged.Changed(); !ok {
		t.Fatal("expected PositionChanged variant")
	}
	if _, ok := eventClosed.Closed(); !ok {
		t.Fatal("expected PositionClosed variant")
	}
}
