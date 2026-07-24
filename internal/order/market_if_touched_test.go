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
//	source: crates/model/src/orders/market_if_touched.rs:615
//	test: test_initialize
func TestMarketIfTouchedInitialize(t *testing.T) {
	order, err := NewMarketIfTouched(MarketIfTouchedConfig{
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
		order.OrderListID() != nil {
		t.Fatal("optional fields should be unset")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_if_touched.rs:638
//	test: test_display
func TestMarketIfTouchedDisplay(t *testing.T) {
	order, err := NewMarketIfTouched(MarketIfTouchedConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice: decimal.MustPrice("30000"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "MarketIfTouchedOrder { side: BUY, qty: 1, instrument: AUD/USD.SIM, tif: GTC, trigger_price: 30000, trigger_type: LAST_PRICE, status: INITIALIZED }"
	if order.String() != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_if_touched.rs:665
//	test: test_quantity_zero
func TestMarketIfTouchedQuantityZero(t *testing.T) {
	_, err := NewMarketIfTouched(MarketIfTouchedConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("0"),
		TriggerPrice: decimal.MustPrice("30000"),
		TriggerType:  TriggerTypeLastPrice,
	})
	if err == nil || !strings.Contains(err.Error(), "not positive, was 0") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_if_touched.rs:677
//	test: test_gtd_without_expire
func TestMarketIfTouchedGTDWithoutExpire(t *testing.T) {
	_, err := NewMarketIfTouched(MarketIfTouchedConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice: decimal.MustPrice("30000"),
		TriggerType:  TriggerTypeLastPrice, TimeInForce: TimeInForceGTD,
	})
	if err == nil || !strings.Contains(err.Error(), "expire_time` is required for `GTD` order") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_if_touched.rs:689
//	test: test_market_if_touched_order_update
func TestMarketIfTouchedOrderUpdate(t *testing.T) {
	order, err := NewMarketIfTouched(MarketIfTouchedConfig{
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
	if err := order.ApplyUpdate(MarketIfTouchedUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		TriggerPrice: &triggerPrice, Quantity: decimal.MustQuantity("5"), Timestamp: 2,
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
//	source: crates/model/src/orders/market_if_touched.rs:719
//	test: test_market_if_touched_order_from_order_initialized
func TestMarketIfTouchedOrderFromOrderInitialized(t *testing.T) {
	triggerPrice := decimal.MustPrice("100.00")
	triggerType := TriggerTypeDefault
	event := MarketIfTouchedInitialization{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice: &triggerPrice, TriggerType: &triggerType,
	}
	order, err := MarketIfTouchedFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if order.TraderID() != event.TraderID || order.StrategyID() != event.StrategyID ||
		order.InstrumentID().String() != event.InstrumentID.String() ||
		order.ClientOrderID() != event.ClientOrderID || order.Side() != event.Side ||
		order.Quantity().Cmp(event.Quantity) != 0 ||
		order.TriggerPrice().Cmp(*event.TriggerPrice) != 0 ||
		order.TriggerType() != *event.TriggerType {
		t.Fatalf("converted order = %+v", order)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/market_if_touched.rs:747
//	test: test_market_if_touched_order_sets_slippage_when_filled
func TestMarketIfTouchedOrderSetsSlippageWhenFilled(t *testing.T) {
	order, err := NewMarketIfTouched(MarketIfTouchedConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("10"),
		TriggerPrice: decimal.MustPrice("90.00"),
	})
	if err != nil {
		t.Fatal(err)
	}
	venue := ids.MustVenueOrderID("TEST-001")
	if err := order.Accept(testAccount, venue, 1); err != nil {
		t.Fatal(err)
	}
	if err := order.Fill(Fill{
		TradeID: ids.MustTradeID("TRADE-001"), Quantity: order.Quantity(),
		Price: decimal.MustPrice("98.50"), VenueOrderID: &venue, Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if order.Slippage() == nil || order.Slippage().String() != "8.5" {
		t.Fatalf("slippage = %v, want 8.5", order.Slippage())
	}
}
