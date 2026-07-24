package order

import (
	"fmt"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:647
//	test: test_initialize
func TestStopLimitInitialize(t *testing.T) {
	order, err := NewStopLimit(StopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:        decimal.MustPrice("0.68100"),
		TriggerPrice: decimal.MustPrice("0.68000"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.TriggerPrice().String() != "0.68000" ||
		order.Price().String() != "0.68100" ||
		order.TimeInForce() != TimeInForceGTC || order.IsTriggered() {
		t.Fatalf("initialized stop-limit = %+v", order)
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
//	source: crates/model/src/orders/stop_limit.rs:673
//	test: test_display
func TestMarketToLimitDisplay(t *testing.T) {
	order := MarketToLimitDisplay{
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("1"),
	}
	const expected = "MarketToLimitOrder(BUY 1 AUD/USD.SIM MARKET_TO_LIMIT GTC, status=INITIALIZED, client_order_id=O-19700101-000000-001-001-1, venue_order_id=None, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=None)"
	if order.String() != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:688
//	test: test_display_qty_gt_quantity_err
func TestStopLimitDisplayQuantityGreaterThanQuantityError(t *testing.T) {
	display := decimal.MustQuantity("2")
	_, err := NewStopLimit(StopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:        decimal.MustPrice("30100"),
		TriggerPrice: decimal.MustPrice("30300"),
		TriggerType:  TriggerTypeLastPrice, DisplayQuantity: &display,
	})
	if err == nil || !strings.Contains(err.Error(), "display_qty` may not exceed `quantity") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:702
//	test: test_display_qty_negative_err
//	adaptation: Go rejects a negative Quantity before it can reach the order constructor.
func TestStopLimitNegativeDisplayQuantityError(t *testing.T) {
	_, err := decimal.ParseQuantity("-1")
	if err == nil || !strings.Contains(err.Error(), "Quantity must be non-negative") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:716
//	test: test_gtd_without_expire_time_err
func TestStopLimitGTDWithoutExpireTimeError(t *testing.T) {
	_, err := NewStopLimit(StopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:        decimal.MustPrice("30100"),
		TriggerPrice: decimal.MustPrice("30300"),
		TriggerType:  TriggerTypeLastPrice, TimeInForce: TimeInForceGTD,
	})
	if err == nil || !strings.Contains(err.Error(), "expire_time` is required for `GTD` order") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:728
//	test: test_stop_limit_order_update
func TestStopLimitOrderUpdate(t *testing.T) {
	order, err := NewStopLimit(StopLimitConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"),
		Price:        decimal.MustPrice("100.00"),
		TriggerPrice: decimal.MustPrice("95.00"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 1); err != nil {
		t.Fatal(err)
	}
	price := decimal.MustPrice("105.00")
	triggerPrice := decimal.MustPrice("90.00")
	if err := order.ApplyUpdate(StopLimitUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		Price: &price, TriggerPrice: &triggerPrice,
		Quantity: decimal.MustQuantity("5"), Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.Quantity(), "5")
	if order.Price().String() != "105.00" || order.TriggerPrice().String() != "90.00" {
		t.Fatalf("updated prices = %s/%s", order.Price(), order.TriggerPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:762
//	test: test_stop_limit_order_expire_time
func TestStopLimitOrderExpireTime(t *testing.T) {
	expireTime := uint64(1_234_567_890)
	order, err := NewStopLimit(StopLimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"),
		Price:        decimal.MustPrice("100.00"),
		TriggerPrice: decimal.MustPrice("95.00"),
		ExpireTime:   &expireTime,
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
//	source: crates/model/src/orders/stop_limit.rs:778
//	test: test_stop_limit_order_post_only
func TestStopLimitOrderPostOnly(t *testing.T) {
	order, err := NewStopLimit(StopLimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"),
		Price:        decimal.MustPrice("100.00"),
		TriggerPrice: decimal.MustPrice("95.00"), PostOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !order.IsPostOnly() {
		t.Fatal("post-only flag not retained")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:793
//	test: test_stop_limit_order_reduce_only
func TestStopLimitOrderReduceOnly(t *testing.T) {
	order, err := NewStopLimit(StopLimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"),
		Price:        decimal.MustPrice("100.00"),
		TriggerPrice: decimal.MustPrice("95.00"), ReduceOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !order.IsReduceOnly() {
		t.Fatal("reduce-only flag not retained")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:808
//	test: test_stop_limit_order_trigger_instrument_id
func TestStopLimitOrderTriggerInstrumentID(t *testing.T) {
	triggerInstrumentID := ids.MustInstrumentID("ETH-USDT.BINANCE")
	order, err := NewStopLimit(StopLimitConfig{
		InstrumentID:        ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:            decimal.MustQuantity("10"),
		Price:               decimal.MustPrice("100.00"),
		TriggerPrice:        decimal.MustPrice("95.00"),
		TriggerInstrumentID: &triggerInstrumentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.TriggerInstrumentID() == nil ||
		order.TriggerInstrumentID().String() != triggerInstrumentID.String() {
		t.Fatalf("trigger instrument = %v", order.TriggerInstrumentID())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:824
//	test: test_stop_limit_order_would_reduce_only
func TestStopLimitOrderWouldReduceOnly(t *testing.T) {
	order, err := NewStopLimit(StopLimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Side:         OrderSideSell, Quantity: decimal.MustQuantity("10"),
		Price:        decimal.MustPrice("100.00"),
		TriggerPrice: decimal.MustPrice("95.00"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !order.WouldReduceOnly(PositionSideLong, decimal.MustQuantity("15")) ||
		order.WouldReduceOnly(PositionSideShort, decimal.MustQuantity("15")) ||
		order.WouldReduceOnly(PositionSideLong, decimal.MustQuantity("5")) {
		t.Fatal("reduce-only projection differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:841
//	test: test_stop_limit_order_display_string
func TestStopLimitOrderDisplayString(t *testing.T) {
	order, err := NewStopLimit(StopLimitConfig{
		InstrumentID:  ids.MustInstrumentID("BTC-USDT.BINANCE"),
		ClientOrderID: ids.MustClientOrderID("ORDER-001"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("10"),
		Price:        decimal.MustPrice("100.00"),
		TriggerPrice: decimal.MustPrice("95.00"),
	})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "StopLimitOrder(BUY 10 BTC-USDT.BINANCE STOP_LIMIT @ 95.00-STOP[DEFAULT] 100.00-LIMIT GTC, status=INITIALIZED, client_order_id=ORDER-001, venue_order_id=None, position_id=None, tags=None)"
	if order.String() != expected || fmt.Sprint(order) != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stop_limit.rs:861
//	test: test_stop_limit_order_from_order_initialized
func TestStopLimitOrderFromOrderInitialized(t *testing.T) {
	price := decimal.MustPrice("100.00")
	triggerPrice := decimal.MustPrice("95.00")
	triggerType := TriggerTypeDefault
	expireTime := uint64(1_234_567_890)
	display := decimal.MustQuantity("5")
	event := StopLimitInitialization{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("10"),
		Price: &price, TriggerPrice: &triggerPrice, TriggerType: &triggerType,
		ExpireTime: &expireTime, PostOnly: true, ReduceOnly: true,
		DisplayQuantity: &display,
	}
	order, err := StopLimitFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if order.TraderID() != event.TraderID || order.StrategyID() != event.StrategyID ||
		order.InstrumentID().String() != event.InstrumentID.String() ||
		order.ClientOrderID() != event.ClientOrderID || order.Side() != event.Side ||
		order.Quantity().Cmp(event.Quantity) != 0 ||
		order.Price().Cmp(*event.Price) != 0 ||
		order.TriggerPrice().Cmp(*event.TriggerPrice) != 0 ||
		order.TriggerType() != *event.TriggerType ||
		order.ExpireTime() == nil || *order.ExpireTime() != *event.ExpireTime ||
		!order.IsPostOnly() || !order.IsReduceOnly() ||
		order.DisplayQuantity() == nil || order.DisplayQuantity().Cmp(*event.DisplayQuantity) != 0 ||
		order.OrderType() != OrderTypeStopLimit || order.IsTriggered() {
		t.Fatalf("converted stop-limit = %+v", order)
	}
}
