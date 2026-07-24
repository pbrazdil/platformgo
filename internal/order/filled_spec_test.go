package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/filled.rs:116
//	test: defaults_are_sensible
func TestOrderFilledSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderFilledSpec(NewOrderSpecEventIDSequence()).Build()
	requireDefaultSpecIdentity(t, event.TraderID, event.StrategyID, event.InstrumentID, event.ClientOrderID)
	if event.VenueOrderID != ids.MustVenueOrderID("001") ||
		event.AccountID != ids.MustAccountID("SIM-001") ||
		event.TradeID != ids.MustTradeID("1") ||
		event.OrderSide != OrderSideBuy || event.OrderType != OrderTypeMarket ||
		event.LastQuantity.String() != "100000" || event.LastPrice.String() != "1.00000" ||
		!event.Currency.Equal(currency.USD()) || event.LiquiditySide != LiquiditySideTaker ||
		event.TsEvent != 0 || event.TsInit != 0 || event.Reconciliation ||
		event.PositionID != nil || event.Commission != nil {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/filled.rs:141
//	test: overrides_apply_through_constructor
func TestOrderFilledSpecOverridesApplyThroughConstructor(t *testing.T) {
	commission := money.MustNew("0.5", currency.USD())
	event := NewOrderFilledSpec(NewOrderSpecEventIDSequence()).
		WithOrderSide(OrderSideSell).
		WithLastQuantity(decimal.MustQuantity("50")).
		WithLastPrice(decimal.MustPrice("1.25000")).
		WithCommission(commission).Build()
	if event.OrderSide != OrderSideSell || event.LastQuantity.String() != "50" ||
		event.LastPrice.String() != "1.25000" || event.Commission == nil ||
		!event.Commission.Equal(commission) || event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/filled.rs:157
//	test: event_ids_are_unique_within_a_run
func TestOrderFilledSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	requireUniqueSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderFilledSpec(s).Build().EventID
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/filled.rs:168
//	test: event_id_sequence_is_reproducible
func TestOrderFilledSpecEventIDSequenceIsReproducible(t *testing.T) {
	requireReproducibleSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderFilledSpec(s).Build().EventID
	})
}
