package order

import (
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market.rs:576
//	test: test_positive_quantity_condition
func TestMarketOrderPositiveQuantityCondition(t *testing.T) {
	_, err := NewMarketOrder(MarketOrderConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("0"),
	})
	if err == nil || !strings.Contains(err.Error(), "not positive, was 0") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market.rs:586
//	test: test_gtd_condition
func TestMarketOrderGTDCondition(t *testing.T) {
	_, err := NewMarketOrder(MarketOrderConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("100"),
		TimeInForce: TimeInForceGTD,
	})
	if err == nil || err.Error() != "GTD not supported for Market orders" {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market.rs:595
//	test: test_market_order_creation
func TestMarketOrderCreation(t *testing.T) {
	order, err := NewMarketOrder(MarketOrderConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("10"),
		TimeInForce: TimeInForceIOC,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.TimeInForce() != TimeInForceIOC ||
		order.OrderType() != OrderTypeMarket || order.Price() != nil {
		t.Fatalf("created order = %+v", order)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market.rs:611
//	test: test_market_order_update
func TestMarketOrderUpdate(t *testing.T) {
	order, err := NewMarketOrder(MarketOrderConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 1); err != nil {
		t.Fatal(err)
	}
	quantity := decimal.MustQuantity("5")
	if err := order.ApplyUpdate(MarketOrderUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		Quantity: &quantity, Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.Quantity(), "5")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market.rs:638
//	test: test_market_order_from_order_initialized
func TestMarketOrderFromOrderInitialized(t *testing.T) {
	event := MarketOrderInitialization{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("10"),
	}
	order, err := MarketOrderFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if order.TraderID() != event.TraderID || order.StrategyID() != event.StrategyID ||
		order.InstrumentID().String() != event.InstrumentID.String() ||
		order.ClientOrderID() != event.ClientOrderID || order.Side() != event.Side ||
		order.Quantity().Cmp(event.Quantity) != 0 {
		t.Fatalf("converted order = %+v", order)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market.rs:663
//	test: test_market_order_invalid_quantity
func TestMarketOrderInvalidQuantity(t *testing.T) {
	_, err := NewMarketOrder(MarketOrderConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("0"),
	})
	if err == nil || !strings.Contains(err.Error(), "not positive, was 0") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market.rs:672
//	test: test_display
func TestMarketOrderDisplay(t *testing.T) {
	order, err := NewMarketOrder(MarketOrderConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "MarketOrder(BUY 10 AUD/USD.SIM @ MARKET GTC, status=INITIALIZED, client_order_id=O-19700101-000000-001-001-1, venue_order_id=None, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=None)"
	if order.String() != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market.rs:695
//	test: test_stop_market_order_protection_price_update
func TestMarketOrderProtectionPriceUpdate(t *testing.T) {
	order, err := NewMarketOrder(MarketOrderConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 1); err != nil {
		t.Fatal(err)
	}
	if order.Price() != nil || order.HasPrice() {
		t.Fatalf("initial price/has-price = %v/%t", order.Price(), order.HasPrice())
	}
	protectionPrice := decimal.MustPrice("95.00")
	if err := order.ApplyUpdate(MarketOrderUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		ProtectionPrice: &protectionPrice, Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if order.Price() == nil || order.Price().String() != "95.00" || !order.HasPrice() {
		t.Fatalf("updated price/has-price = %v/%t", order.Price(), order.HasPrice())
	}
}
