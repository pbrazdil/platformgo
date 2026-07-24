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
//	source: crates/model/src/orders/market_to_limit.rs:595
//	test: test_initialize
func TestMarketToLimitInitialize(t *testing.T) {
	order, err := NewMarketToLimit(MarketToLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		InitialPrice: copyPointer(decimal.MustPrice("0.68000")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.Price() != nil || order.IsTriggered() != nil ||
		order.TimeInForce() != TimeInForceGTC {
		t.Fatalf("initialized order = %+v", order)
	}
	requireQuantity(t, order.FilledQuantity(), "0")
	requireQuantity(t, order.LeavesQuantity(), "1")
	if order.TriggerInstrumentID() != nil || order.core.config.OrderListID != nil {
		t.Fatal("optional fields should be unset")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_to_limit.rs:614
//	test: test_display
func TestMarketToLimitOrderDisplay(t *testing.T) {
	order, err := NewMarketToLimit(MarketToLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "MarketToLimitOrder(BUY 1 AUD/USD.SIM MARKET_TO_LIMIT GTC, status=INITIALIZED, client_order_id=O-19700101-000000-001-001-1, venue_order_id=None, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=None)"
	if order.String() != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_to_limit.rs:631
//	test: test_quantity_zero
func TestMarketToLimitQuantityZero(t *testing.T) {
	_, err := NewMarketToLimit(MarketToLimitConfig{
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
//	source: crates/model/src/orders/market_to_limit.rs:641
//	test: test_gtd_without_expire
func TestMarketToLimitGTDWithoutExpire(t *testing.T) {
	_, err := NewMarketToLimit(MarketToLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TimeInForce: TimeInForceGTD,
	})
	if err == nil || !strings.Contains(err.Error(), "expire_time` is required for `GTD` order") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_to_limit.rs:652
//	test: test_display_qty_gt_quantity
func TestMarketToLimitDisplayQuantityGreaterThanQuantity(t *testing.T) {
	display := decimal.MustQuantity("2")
	_, err := NewMarketToLimit(MarketToLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		DisplayQuantity: &display,
	})
	if err == nil || !strings.Contains(err.Error(), "display_qty` may not exceed `quantity") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_to_limit.rs:662
//	test: test_market_to_limit_order_update
func TestMarketToLimitOrderUpdate(t *testing.T) {
	order, err := NewMarketToLimit(MarketToLimitConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 1); err != nil {
		t.Fatal(err)
	}
	price := decimal.MustPrice("95.00")
	if err := order.ApplyUpdate(MarketToLimitUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		Price: &price, Quantity: decimal.MustQuantity("5"), Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.Quantity(), "5")
	if order.Price() == nil || order.Price().String() != "95.00" {
		t.Fatalf("price = %v", order.Price())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_to_limit.rs:691
//	test: test_market_to_limit_order_expire_time
func TestMarketToLimitOrderExpireTime(t *testing.T) {
	expireTime := uint64(1_234_567_890)
	order, err := NewMarketToLimit(MarketToLimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"), ExpireTime: &expireTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.ExpireTime() == nil || *order.ExpireTime() != expireTime {
		t.Fatalf("expire time = %v", order.ExpireTime())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_to_limit.rs:705
//	test: test_market_to_limit_order_from_order_initialized
func TestMarketToLimitOrderFromOrderInitialized(t *testing.T) {
	event := MarketToLimitInitialization{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("100000"),
	}
	order, err := MarketToLimitFromInitialization(event)
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
//	source: crates/model/src/orders/market_to_limit.rs:724
//	test: test_market_to_limit_order_sets_slippage_when_filled
func TestMarketToLimitOrderSetsSlippageWhenFilled(t *testing.T) {
	order, err := NewMarketToLimit(MarketToLimitConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	venue := ids.MustVenueOrderID("TEST-001")
	if err := order.Accept(testAccount, venue, 1); err != nil {
		t.Fatal(err)
	}
	price := decimal.MustPrice("90.00")
	if err := order.ApplyUpdate(MarketToLimitUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		Price: &price, Quantity: order.Quantity(), Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if order.Price() == nil || order.Price().String() != "90.00" {
		t.Fatalf("materialized price = %v", order.Price())
	}
	fill := Fill{
		TradeID:  ids.MustTradeID("TRADE-001"),
		Quantity: order.Quantity(), Price: decimal.MustPrice("98.50"),
		VenueOrderID: &venue, Timestamp: 3,
	}
	if err := order.Fill(fill); err != nil {
		t.Fatal(err)
	}
	if order.Slippage() == nil || order.Slippage().String() != "8.5" {
		t.Fatalf("slippage = %v, want 8.5", order.Slippage())
	}
}
