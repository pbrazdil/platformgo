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
//	source: crates/model/src/orders/limit_if_touched.rs:653
//	test: test_initialize
func TestLimitIfTouchedInitialize(t *testing.T) {
	order, err := NewLimitIfTouched(LimitIfTouchedConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:        decimal.MustPrice("0.68000"),
		TriggerPrice: decimal.MustPrice("0.68000"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.TriggerPrice().String() != "0.68000" ||
		order.Price().String() != "0.68000" ||
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
//	source: crates/model/src/orders/limit_if_touched.rs:678
//	test: test_display
func TestLimitIfTouchedDisplay(t *testing.T) {
	order, err := NewLimitIfTouched(LimitIfTouchedConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:        decimal.MustPrice("30200"),
		TriggerPrice: decimal.MustPrice("30200"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "LimitIfTouchedOrder(BUY 1 AUD/USD.SIM @ 30200 / trigger 30200 (LastPrice) GTC, status=INITIALIZED)"
	if order.String() != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit_if_touched.rs:698
//	test: test_quantity_zero
func TestLimitIfTouchedQuantityZero(t *testing.T) {
	_, err := NewLimitIfTouched(LimitIfTouchedConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("0"),
		Price:        decimal.MustPrice("30000"),
		TriggerPrice: decimal.MustPrice("30200"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err == nil || !strings.Contains(err.Error(), "not positive, was 0") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit_if_touched.rs:711
//	test: test_gtd_without_expire
func TestLimitIfTouchedGTDWithoutExpire(t *testing.T) {
	_, err := NewLimitIfTouched(LimitIfTouchedConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:        decimal.MustPrice("30000"),
		TriggerPrice: decimal.MustPrice("30200"),
		TriggerType:  TriggerTypeLastPrice, TimeInForce: TimeInForceGTD,
	})
	if err == nil || !strings.Contains(err.Error(), "expire_time` is required for `GTD` order") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit_if_touched.rs:725
//	test: test_buy_trigger_gt_price
func TestLimitIfTouchedBuyTriggerGreaterThanPrice(t *testing.T) {
	_, err := NewLimitIfTouched(LimitIfTouchedConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:        decimal.MustPrice("30200"),
		TriggerPrice: decimal.MustPrice("30300"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err == nil || err.Error() != "BUY Limit-If-Touched must have `trigger_price` <= `price`" {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit_if_touched.rs:738
//	test: test_sell_trigger_lt_price
func TestLimitIfTouchedSellTriggerLessThanPrice(t *testing.T) {
	_, err := NewLimitIfTouched(LimitIfTouchedConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideSell, Quantity: decimal.MustQuantity("1"),
		Price:        decimal.MustPrice("30200"),
		TriggerPrice: decimal.MustPrice("30100"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err == nil || err.Error() != "SELL Limit-If-Touched must have `trigger_price` >= `price`" {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit_if_touched.rs:750
//	test: test_limit_if_touched_order_update
func TestLimitIfTouchedOrderUpdate(t *testing.T) {
	order, err := NewLimitIfTouched(LimitIfTouchedConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:     decimal.MustQuantity("10"), Price: decimal.MustPrice("100.00"),
		TriggerPrice: decimal.MustPrice("95.00"), TriggerType: TriggerTypeDefault,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 1); err != nil {
		t.Fatal(err)
	}
	price := decimal.MustPrice("105.00")
	triggerPrice := decimal.MustPrice("97.00")
	if err := order.ApplyUpdate(LimitIfTouchedUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		Price: &price, TriggerPrice: &triggerPrice,
		Quantity: decimal.MustQuantity("5"), Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if order.Price().String() != "105.00" ||
		order.TriggerPrice().String() != "97.00" {
		t.Fatalf("updated prices = %s/%s", order.Price(), order.TriggerPrice())
	}
	requireQuantity(t, order.Quantity(), "5")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit_if_touched.rs:785
//	test: test_limit_if_touched_order_from_order_initialized
func TestLimitIfTouchedOrderFromOrderInitialized(t *testing.T) {
	price := decimal.MustPrice("100.00")
	triggerPrice := decimal.MustPrice("95.00")
	triggerType := TriggerTypeDefault
	event := LimitIfTouchedInitialization{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("100000"),
		Price: &price, TriggerPrice: &triggerPrice, TriggerType: &triggerType,
	}
	order, err := LimitIfTouchedFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if order.TraderID() != event.TraderID || order.StrategyID() != event.StrategyID ||
		order.InstrumentID().String() != event.InstrumentID.String() ||
		order.ClientOrderID() != event.ClientOrderID || order.Side() != event.Side ||
		order.Quantity().Cmp(event.Quantity) != 0 ||
		order.Price().Cmp(*event.Price) != 0 ||
		order.TriggerPrice().Cmp(*event.TriggerPrice) != 0 ||
		order.TriggerType() != *event.TriggerType {
		t.Fatalf("converted order = %+v", order)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/limit_if_touched.rs:815
//	test: test_limit_if_touched_order_sets_slippage_when_filled
func TestLimitIfTouchedOrderSetsSlippageWhenFilled(t *testing.T) {
	order, err := NewLimitIfTouched(LimitIfTouchedConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("10"),
		Price:        decimal.MustPrice("95.00"),
		TriggerPrice: decimal.MustPrice("90.00"),
		TriggerType:  TriggerTypeDefault,
	})
	if err != nil {
		t.Fatal(err)
	}
	venue := ids.MustVenueOrderID("TEST-001")
	if err := order.Accept(testAccount, venue, 1); err != nil {
		t.Fatal(err)
	}
	fill := Fill{
		TradeID:  ids.MustTradeID("TRADE-001"),
		Quantity: order.Quantity(), Price: decimal.MustPrice("98.50"),
		VenueOrderID: &venue, Timestamp: 2,
	}
	if err := order.Fill(fill); err != nil {
		t.Fatal(err)
	}
	if order.Slippage() == nil || order.Slippage().String() != "3.5" {
		t.Fatalf("slippage = %v, want 3.5", order.Slippage())
	}
}
