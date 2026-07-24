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
//	source: crates/model/src/orders/limit.rs:618
//	test: test_initialize
func TestLimitInitialize(t *testing.T) {
	order, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price: decimal.MustPrice("0.68000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.TimeInForce() != TimeInForceGTC {
		t.Fatalf("time in force = %v", order.TimeInForce())
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
//	source: crates/model/src/orders/limit.rs:637
//	test: test_display
func TestLimitDisplay(t *testing.T) {
	order, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("100000"),
		Price: decimal.MustPrice("1.00000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "LimitOrder(BUY 100_000 AUD/USD.SIM LIMIT @ 1.00000 GTC, status=INITIALIZED, client_order_id=O-19700101-000000-001-001-1, venue_order_id=None, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=None)"
	if order.String() != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit.rs:658
//	test: test_positive_quantity_condition
func TestLimitPositiveQuantityCondition(t *testing.T) {
	_, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("0"),
		Price: decimal.MustPrice("0.8"),
	})
	if err == nil || !strings.Contains(err.Error(), "not positive, was 0") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit.rs:669
//	test: test_correct_expiration_with_time_in_force_gtd
func TestLimitCorrectExpirationWithTimeInForceGTD(t *testing.T) {
	_, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price: decimal.MustPrice("0.8"), TimeInForce: TimeInForceGTD,
	})
	if err == nil || !strings.Contains(err.Error(), "expire_time` is required for `GTD` order") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit.rs:680
//	test: test_limit_order_creation
func TestLimitOrderCreation(t *testing.T) {
	order, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("10"),
		Price: decimal.MustPrice("100.00"), TimeInForce: TimeInForceGTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.Price().String() != "100.00" ||
		order.Quantity().Cmp(decimal.MustQuantity("10")) != 0 ||
		order.TimeInForce() != TimeInForceGTC || order.Side() != OrderSideBuy {
		t.Fatalf("created order = %+v", order)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit.rs:696
//	test: test_limit_order_with_expire_time
func TestLimitOrderWithExpireTime(t *testing.T) {
	expireTime := uint64(1_700_000_000_000_000)
	order, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"), Price: decimal.MustPrice("100.00"),
		TimeInForce: TimeInForceGTD, ExpireTime: &expireTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.ExpireTime() == nil || *order.ExpireTime() != expireTime ||
		order.TimeInForce() != TimeInForceGTD {
		t.Fatalf("expire/TIF = %v/%v", order.ExpireTime(), order.TimeInForce())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit.rs:712
//	test: test_limit_order_missing_expire_time
func TestLimitOrderMissingExpireTime(t *testing.T) {
	_, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"), Price: decimal.MustPrice("100.00"),
		TimeInForce: TimeInForceGTD,
	})
	if err == nil || !strings.Contains(err.Error(), "expire_time` is required for `GTD` order") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit.rs:722
//	test: test_limit_order_post_only
func TestLimitOrderPostOnly(t *testing.T) {
	order, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"), Price: decimal.MustPrice("100.00"),
		PostOnly: true,
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
//	source: crates/model/src/orders/limit.rs:734
//	test: test_limit_order_display_quantity
func TestLimitOrderDisplayQuantity(t *testing.T) {
	display := decimal.MustQuantity("5")
	order, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"), Price: decimal.MustPrice("100.00"),
		DisplayQuantity: &display,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.DisplayQuantity() == nil || order.DisplayQuantity().Cmp(display) != 0 {
		t.Fatalf("display quantity = %v", order.DisplayQuantity())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit.rs:747
//	test: test_limit_order_update
func TestLimitOrderUpdate(t *testing.T) {
	order, err := NewLimit(LimitConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"), Price: decimal.MustPrice("100.00"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 1); err != nil {
		t.Fatal(err)
	}
	price := decimal.MustPrice("105.00")
	if err := order.ApplyUpdate(LimitUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		Price: &price, Quantity: decimal.MustQuantity("5"), Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.Quantity(), "5")
	if order.Price().String() != "105.00" {
		t.Fatalf("price = %s", order.Price())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit.rs:774
//	test: test_limit_order_expire_time
func TestLimitOrderExpireTime(t *testing.T) {
	expireTime := uint64(1_700_000_000_000_000)
	order, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"), Price: decimal.MustPrice("100.00"),
		TimeInForce: TimeInForceGTD, ExpireTime: &expireTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.ExpireTime() == nil || *order.ExpireTime() != expireTime {
		t.Fatalf("expire time = %v", order.ExpireTime())
	}
}
