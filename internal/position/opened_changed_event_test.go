package position

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func lifecycleTestIdentity() PositionIdentity {
	return PositionIdentity{
		TraderID:   ids.MustTraderID("TRADER-001"),
		StrategyID: ids.MustStrategyID("EMA-CROSS"),
		AccountID:  ids.MustAccountID("SIM-001"),
	}
}

func lifecycleTestInstrument() Instrument {
	usd := currency.USD()
	aud := currency.AUD()
	return Instrument{
		ID: "AUD/USD.SIM", PricePrecision: 4, SizePrecision: 0,
		Multiplier: decimal.MustParse("1"), CurrencyPair: true,
		BaseCurrency: &aud, QuoteCurrency: usd, SettlementCurrency: usd,
	}
}

func lifecycleOpeningFill() Fill {
	commission := money.MustNew("2.0", currency.USD())
	return Fill{
		ClientOrderID: "O-19700101-000000-001-001-1", TradeID: "T-001",
		Side: Buy, Quantity: decimal.MustParse("100"), Price: decimal.MustParse("0.8000"),
		Commission: &commission, TsEvent: 1_000_000_000, TsInit: 2_000_000_000,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/opened.rs:162
//	test: test_position_opened_create
func TestPositionOpenedCreate(t *testing.T) {
	openingFill := lifecycleOpeningFill()
	position, err := New(lifecycleTestInstrument(), "P-001", openingFill)
	if err != nil {
		t.Fatal(err)
	}
	fill := PositionLifecycleFill{
		LastQuantity: decimal.MustQuantity("100"),
		LastPrice:    decimal.MustPrice("0.8000"), TsEvent: openingFill.TsEvent,
	}
	eventID := EventID("00000000-0000-0000-0000-000000000000")
	event := CreatePositionOpened(position, lifecycleTestIdentity(), fill, eventID, 3_000_000_000)

	if event.TraderID != lifecycleTestIdentity().TraderID ||
		event.StrategyID != lifecycleTestIdentity().StrategyID ||
		event.InstrumentID != ids.MustInstrumentID(position.Instrument.ID) ||
		event.PositionID != ids.MustPositionID(position.ID) ||
		event.AccountID != lifecycleTestIdentity().AccountID ||
		event.OpeningOrderID != ids.MustClientOrderID(position.OpeningOrderID) ||
		event.Entry != position.Entry || event.Side != position.Side ||
		event.SignedQuantity.Cmp(position.SignedQuantity) != 0 ||
		event.Quantity.Decimal().Cmp(position.Quantity) != 0 ||
		!event.LastQuantity.Equal(fill.LastQuantity) ||
		!event.LastPrice.Equal(fill.LastPrice) ||
		!event.Currency.Equal(position.Instrument.QuoteCurrency) ||
		event.AverageOpen.Cmp(position.AverageOpen) != 0 ||
		event.EventID != eventID || event.TsEvent != fill.TsEvent ||
		event.TsInit != 3_000_000_000 {
		t.Fatalf("opened event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/opened.rs:191
//	test: test_position_opened_with_different_sides
func TestPositionOpenedWithDifferentSides(t *testing.T) {
	longPosition := testPositionOpenedEvent()
	longPosition.Side, longPosition.Entry = Long, Buy
	longPosition.SignedQuantity = decimal.MustParse("100.0")
	shortPosition := testPositionOpenedEvent()
	shortPosition.Side, shortPosition.Entry = Short, Sell
	shortPosition.SignedQuantity = decimal.MustParse("-100.0")

	if longPosition.Side != Long || longPosition.Entry != Buy ||
		longPosition.SignedQuantity.Cmp(decimal.MustParse("100.0")) != 0 ||
		shortPosition.Side != Short || shortPosition.Entry != Sell ||
		shortPosition.SignedQuantity.Cmp(decimal.MustParse("-100.0")) != 0 {
		t.Fatalf("long/short = %#v/%#v", longPosition, shortPosition)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/changed.rs:186
//	test: test_position_changed_create
func TestPositionChangedCreate(t *testing.T) {
	position, err := New(lifecycleTestInstrument(), "P-001", lifecycleOpeningFill())
	if err != nil {
		t.Fatal(err)
	}
	changeFill := PositionLifecycleFill{
		LastQuantity: decimal.MustQuantity("50"),
		LastPrice:    decimal.MustPrice("0.8050"), TsEvent: 1_500_000_000,
	}
	eventID := EventID("00000000-0000-0000-0000-000000000000")
	event := CreatePositionChanged(position, lifecycleTestIdentity(), changeFill, eventID, 3_000_000_000)
	realizedPnLMatches := event.RealizedPnL == nil && position.RealizedPnL == nil
	if event.RealizedPnL != nil && position.RealizedPnL != nil {
		realizedPnLMatches = event.RealizedPnL.Equal(*position.RealizedPnL)
	}

	if event.TraderID != lifecycleTestIdentity().TraderID ||
		event.StrategyID != lifecycleTestIdentity().StrategyID ||
		event.InstrumentID != ids.MustInstrumentID(position.Instrument.ID) ||
		event.PositionID != ids.MustPositionID(position.ID) ||
		event.AccountID != lifecycleTestIdentity().AccountID ||
		event.OpeningOrderID != ids.MustClientOrderID(position.OpeningOrderID) ||
		event.Entry != position.Entry || event.Side != position.Side ||
		event.SignedQuantity.Cmp(position.SignedQuantity) != 0 ||
		event.Quantity.Decimal().Cmp(position.Quantity) != 0 ||
		event.PeakQuantity.Decimal().Cmp(position.PeakQuantity) != 0 ||
		!event.LastQuantity.Equal(changeFill.LastQuantity) ||
		!event.LastPrice.Equal(changeFill.LastPrice) ||
		!event.Currency.Equal(position.Instrument.QuoteCurrency) ||
		event.AverageOpen.Cmp(position.AverageOpen) != 0 ||
		event.AverageClose != nil || event.RealizedReturn.Cmp(position.RealizedReturn) != 0 ||
		!realizedPnLMatches || !event.UnrealizedPnL.IsZero() ||
		event.EventID != eventID || event.TsOpened != position.TsOpened ||
		event.TsEvent != changeFill.TsEvent || event.TsInit != 3_000_000_000 {
		t.Fatalf("changed event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/changed.rs:238
//	test: test_position_changed_different_sides
func TestPositionChangedDifferentSides(t *testing.T) {
	longPosition := testPositionChangedEvent()
	longPosition.Side = Long
	longPosition.SignedQuantity = decimal.MustParse("150.0")
	shortPosition := testPositionChangedEvent()
	shortPosition.Side = Short
	shortPosition.SignedQuantity = decimal.MustParse("-150.0")

	if longPosition.Side != Long ||
		longPosition.SignedQuantity.Cmp(decimal.MustParse("150.0")) != 0 ||
		shortPosition.Side != Short ||
		shortPosition.SignedQuantity.Cmp(decimal.MustParse("-150.0")) != 0 {
		t.Fatalf("long/short = %#v/%#v", longPosition, shortPosition)
	}
}
