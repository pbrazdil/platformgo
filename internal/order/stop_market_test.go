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
//	source: crates/model/src/orders/stop_market.rs:640
//	test: test_initialize
func TestStopMarketInitialize(t *testing.T) {
	order, err := NewStopMarket(StopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice: decimal.MustPrice("0.68000"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.TriggerPrice().String() != "0.68000" || order.Price() != nil ||
		order.TimeInForce() != TimeInForceGTC || order.IsTriggered() {
		t.Fatalf("initialized order = %+v", order)
	}
	requireQuantity(t, order.FilledQuantity(), "0")
	requireQuantity(t, order.LeavesQuantity(), "1")
	if order.DisplayQuantity() != nil || order.TriggerInstrumentID() != nil ||
		order.core.config.OrderListID != nil {
		t.Fatal("optional fields should be unset")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_market.rs:664
//	test: test_display
func TestStopMarketDisplay(t *testing.T) {
	order, err := NewStopMarket(StopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice: decimal.MustPrice("0.68000"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "StopMarketOrder(BUY 1 AUD/USD.SIM STOP_MARKET GTC, status=INITIALIZED, client_order_id=O-19700101-000000-001-001-1, venue_order_id=None, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=None)"
	if order.String() != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_market.rs:681
//	test: test_display_qty_gt_quantity_err
func TestStopMarketDisplayQuantityGreaterThanQuantityError(t *testing.T) {
	display := decimal.MustQuantity("2")
	_, err := NewStopMarket(StopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice: decimal.MustPrice("0.68000"),
		TriggerType:  TriggerTypeLastPrice, DisplayQuantity: &display,
	})
	if err == nil || !strings.Contains(err.Error(), "display_qty` may not exceed `quantity") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_market.rs:696
//	test: test_quantity_zero_err
func TestStopMarketQuantityZeroError(t *testing.T) {
	_, err := NewStopMarket(StopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("0"),
		TriggerPrice: decimal.MustPrice("0.68000"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err == nil || !strings.Contains(err.Error(), "not positive, was 0") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_market.rs:708
//	test: test_gtd_without_expire_err
func TestStopMarketGTDWithoutExpireError(t *testing.T) {
	_, err := NewStopMarket(StopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice: decimal.MustPrice("0.68000"),
		TriggerType:  TriggerTypeLastPrice, TimeInForce: TimeInForceGTD,
	})
	if err == nil || !strings.Contains(err.Error(), "expire_time` is required for `GTD` order") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_market.rs:720
//	test: test_stop_market_order_update
func TestStopMarketOrderUpdate(t *testing.T) {
	order, err := NewStopMarket(StopMarketConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"),
		TriggerPrice: decimal.MustPrice("100.00"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 1); err != nil {
		t.Fatal(err)
	}
	triggerPrice := decimal.MustPrice("95.00")
	quantity := decimal.MustQuantity("5")
	if err := order.ApplyUpdate(StopMarketUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		TriggerPrice: &triggerPrice, Quantity: &quantity, Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.Quantity(), "5")
	if order.TriggerPrice().String() != "95.00" {
		t.Fatalf("trigger price = %s", order.TriggerPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_market.rs:750
//	test: test_stop_market_order_expire_time
func TestStopMarketOrderExpireTime(t *testing.T) {
	expireTime := uint64(1_234_567_890)
	order, err := NewStopMarket(StopMarketConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"),
		TriggerPrice: decimal.MustPrice("100.00"), ExpireTime: &expireTime,
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
//	source: crates/model/src/orders/stop_market.rs:765
//	test: test_stop_market_order_trigger_instrument_id
func TestStopMarketOrderTriggerInstrumentID(t *testing.T) {
	triggerInstrumentID := ids.MustInstrumentID("ETH-USDT.BINANCE")
	order, err := NewStopMarket(StopMarketConfig{
		InstrumentID:        ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:            decimal.MustQuantity("10"),
		TriggerPrice:        decimal.MustPrice("100.00"),
		TriggerInstrumentID: &triggerInstrumentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.TriggerInstrumentID() == nil ||
		order.TriggerInstrumentID().String() != triggerInstrumentID.String() {
		t.Fatalf("trigger instrument ID = %v", order.TriggerInstrumentID())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_market.rs:780
//	test: test_stop_market_order_from_order_initialized
func TestStopMarketOrderFromOrderInitialized(t *testing.T) {
	triggerPrice := decimal.MustPrice("100.00")
	triggerType := TriggerTypeDefault
	event := StopMarketInitialization{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("10"),
		TriggerPrice: &triggerPrice, TriggerType: &triggerType,
	}
	order, err := StopMarketFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if order.TraderID() != event.TraderID || order.StrategyID() != event.StrategyID ||
		order.InstrumentID().String() != event.InstrumentID.String() ||
		order.ClientOrderID() != event.ClientOrderID ||
		order.Quantity().Cmp(event.Quantity) != 0 ||
		order.TriggerPrice().Cmp(*event.TriggerPrice) != 0 ||
		order.TriggerType() != *event.TriggerType {
		t.Fatalf("converted order = %+v", order)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_market.rs:803
//	test: test_stop_market_order_is_triggered
func TestStopMarketOrderIsTriggered(t *testing.T) {
	order, err := NewStopMarket(StopMarketConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"),
		TriggerPrice: decimal.MustPrice("100.00"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.IsTriggered() {
		t.Fatal("new stop-market order is triggered")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_market.rs:816
//	test: test_stop_market_order_protection_price_update
func TestStopMarketOrderProtectionPriceUpdate(t *testing.T) {
	order, err := NewStopMarket(StopMarketConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"),
		TriggerPrice: decimal.MustPrice("100.00"),
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
	if err := order.ApplyUpdate(StopMarketUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		ProtectionPrice: &protectionPrice, Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if order.Price() == nil || order.Price().String() != "95.00" || !order.HasPrice() {
		t.Fatalf("updated price/has-price = %v/%t", order.Price(), order.HasPrice())
	}
}
