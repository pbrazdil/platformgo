package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func directFilledEvent() OrderFilledEvent {
	return NewOrderFilledEvent(OrderFilledEventConfig{
		TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		VenueOrderID:  ids.MustVenueOrderID("001"), AccountID: ids.MustAccountID("SIM-001"),
		TradeID: ids.MustTradeID("1"), OrderSide: OrderSideBuy, OrderType: OrderTypeMarket,
		LastQuantity: decimal.MustQuantity("100000"), LastPrice: decimal.MustPrice("1.00000"),
		Currency: currency.USD(), LiquiditySide: LiquiditySideTaker,
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/any.rs:363
//	test: test_from_order_event_any_to_filled
func TestDirectFromOrderEventAnyToFilled(t *testing.T) {
	original := directFilledEvent()
	filled := AnyFilled(original).IntoFilled()
	if filled.TradeID != original.TradeID {
		t.Fatalf("trade ID = %s, want %s", filled.TradeID, original.TradeID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/any.rs:372
//	test: test_from_order_event_any_to_filled_panics_on_wrong_variant
func TestDirectFromOrderEventAnyToFilledPanicsOnWrongVariant(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil ||
			recovered != "Invalid `OrderEventAny` not `OrderFilled`" {
			t.Fatalf("panic = %v", recovered)
		}
	}()
	AnyAccepted(NewOrderAcceptedSpec(NewAcceptedEventIDSequence()).Build()).IntoFilled()
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/any.rs:378
//	test: test_display_delegates_to_inner
func TestDirectOrderEventAnyDisplayDelegatesToInner(t *testing.T) {
	filled := directFilledEvent()
	event := AnyFilled(filled)
	if event.String() != filled.String() {
		t.Fatalf("display = %q, want %q", event.String(), filled.String())
	}
}
