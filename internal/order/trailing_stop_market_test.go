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
//	source: crates/model/src/orders/trailing_stop_market.rs:669
//	test: test_initialize
func TestTrailingStopMarketInitialize(t *testing.T) {
	order, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice:   copyPointer(decimal.MustPrice("0.68000")),
		TrailingOffset: decimal.MustParse("10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.TriggerPrice() == nil || order.TriggerPrice().String() != "0.68000" ||
		order.Price() != nil || order.TimeInForce() != TimeInForceGTC ||
		order.IsTriggered() {
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
//	source: crates/model/src/orders/trailing_stop_market.rs:693
//	test: test_display
func TestTrailingStopMarketDisplay(t *testing.T) {
	order, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice:       copyPointer(decimal.MustPrice("0.68000")),
		TriggerType:        TriggerTypeLastPrice,
		TrailingOffset:     decimal.MustParse("10"),
		TrailingOffsetType: TrailingOffsetTypePrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "TrailingStopMarketOrder(BUY 1 AUD/USD.SIM TRAILING_STOP_MARKET GTC, status=INITIALIZED, client_order_id=O-19700101-000000-001-001-1, venue_order_id=None, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=None, activation_price=None, is_activated=false)"
	if order.String() != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_market.rs:712
//	test: test_display_qty_gt_quantity_err
func TestTrailingStopMarketDisplayQuantityGreaterThanQuantityError(t *testing.T) {
	display := decimal.MustQuantity("2")
	_, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice:       copyPointer(decimal.MustPrice("0.68000")),
		TriggerType:        TriggerTypeLastPrice,
		TrailingOffset:     decimal.MustParse("10"),
		TrailingOffsetType: TrailingOffsetTypePrice,
		DisplayQuantity:    &display,
	})
	if err == nil || !strings.Contains(err.Error(), "display_qty` may not exceed `quantity") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_market.rs:729
//	test: test_quantity_zero_err
func TestTrailingStopMarketQuantityZeroError(t *testing.T) {
	_, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("0"),
		TriggerPrice:       copyPointer(decimal.MustPrice("0.68000")),
		TriggerType:        TriggerTypeLastPrice,
		TrailingOffset:     decimal.MustParse("10"),
		TrailingOffsetType: TrailingOffsetTypePrice,
	})
	if err == nil || !strings.Contains(err.Error(), "not positive, was 0") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_market.rs:743
//	test: test_gtd_without_expire_err
func TestTrailingStopMarketGTDWithoutExpireError(t *testing.T) {
	_, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		TriggerPrice:       copyPointer(decimal.MustPrice("0.68000")),
		TriggerType:        TriggerTypeLastPrice,
		TrailingOffset:     decimal.MustParse("10"),
		TrailingOffsetType: TrailingOffsetTypePrice,
		TimeInForce:        TimeInForceGTD,
	})
	if err == nil || !strings.Contains(err.Error(), "expire_time` is required for `GTD` order") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_market.rs:756
//	test: test_trailing_stop_market_order_update
func TestTrailingStopMarketOrderUpdate(t *testing.T) {
	order, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		StrategyID:         ids.MustStrategyID("S-001"),
		InstrumentID:       ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:           decimal.MustQuantity("10"),
		TriggerPrice:       copyPointer(decimal.MustPrice("100.00")),
		TrailingOffset:     decimal.MustParse("0.5"),
		TrailingOffsetType: TrailingOffsetTypeNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 1); err != nil {
		t.Fatal(err)
	}
	triggerPrice := decimal.MustPrice("95.00")
	if err := order.ApplyUpdate(TrailingStopMarketUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		TriggerPrice: &triggerPrice, Quantity: decimal.MustQuantity("5"),
		Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.Quantity(), "5")
	if order.TriggerPrice() == nil || order.TriggerPrice().String() != "95.00" {
		t.Fatalf("trigger price = %v", order.TriggerPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_market.rs:788
//	test: test_trailing_stop_market_order_expire_time
func TestTrailingStopMarketOrderExpireTime(t *testing.T) {
	expireTime := uint64(1_234_567_890)
	order, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		InstrumentID:       ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:           decimal.MustQuantity("10"),
		TriggerPrice:       copyPointer(decimal.MustPrice("100.00")),
		TrailingOffset:     decimal.MustParse("0.5"),
		TrailingOffsetType: TrailingOffsetTypeNone,
		ExpireTime:         &expireTime,
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
//	source: crates/model/src/orders/trailing_stop_market.rs:805
//	test: test_trailing_stop_market_order_trigger_instrument_id
func TestTrailingStopMarketOrderTriggerInstrumentID(t *testing.T) {
	triggerInstrumentID := ids.MustInstrumentID("ETH-USDT.BINANCE")
	order, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		InstrumentID:        ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:            decimal.MustQuantity("10"),
		TriggerPrice:        copyPointer(decimal.MustPrice("100.00")),
		TrailingOffset:      decimal.MustParse("0.5"),
		TrailingOffsetType:  TrailingOffsetTypeNone,
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
//	source: crates/model/src/orders/trailing_stop_market.rs:822
//	test: test_trailing_stop_market_order_from_order_initialized
func TestTrailingStopMarketOrderFromOrderInitialized(t *testing.T) {
	triggerPrice := decimal.MustPrice("100.00")
	triggerType := TriggerTypeDefault
	offset := decimal.MustParse("0.5")
	offsetType := TrailingOffsetTypeNone
	event := TrailingStopMarketInitialization{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("100000"),
		TriggerPrice: &triggerPrice, TriggerType: &triggerType,
		TrailingOffset: &offset, TrailingOffsetType: &offsetType,
	}
	order, err := TrailingStopMarketFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if order.TraderID() != event.TraderID || order.StrategyID() != event.StrategyID ||
		order.InstrumentID().String() != event.InstrumentID.String() ||
		order.ClientOrderID() != event.ClientOrderID || order.Side() != event.Side ||
		order.Quantity().Cmp(event.Quantity) != 0 ||
		order.TriggerPrice() == nil || order.TriggerPrice().Cmp(*event.TriggerPrice) != 0 ||
		order.TriggerType() != *event.TriggerType ||
		order.TrailingOffset().Cmp(*event.TrailingOffset) != 0 ||
		order.TrailingOffsetType() != *event.TrailingOffsetType {
		t.Fatalf("converted order = %+v", order)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_market.rs:857
//	test: test_activation_price_round_trips_through_event
func TestTrailingStopMarketActivationPriceRoundTripsThroughEvent(t *testing.T) {
	order, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		ActivationPrice: copyPointer(decimal.MustPrice("0.68500")),
		TriggerPrice:    copyPointer(decimal.MustPrice("0.68000")),
		TrailingOffset:  decimal.MustParse("10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.ActivationPrice() == nil || order.ActivationPrice().String() != "0.68500" {
		t.Fatalf("activation price = %v", order.ActivationPrice())
	}
	event := order.Initialization()
	if event.ActivationPrice == nil || event.ActivationPrice.String() != "0.68500" {
		t.Fatalf("event activation price = %v", event.ActivationPrice)
	}
	rebuilt, err := TrailingStopMarketFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.ActivationPrice() == nil || rebuilt.ActivationPrice().String() != "0.68500" ||
		rebuilt.TriggerPrice() == nil || rebuilt.TriggerPrice().String() != "0.68000" {
		t.Fatalf("rebuilt prices = %v/%v", rebuilt.ActivationPrice(), rebuilt.TriggerPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_market.rs:879
//	test: test_reconstruct_with_trigger_and_activation_none
func TestTrailingStopMarketReconstructWithTriggerAndActivationNil(t *testing.T) {
	triggerType := TriggerTypeDefault
	offset := decimal.MustParse("10")
	offsetType := TrailingOffsetTypePrice
	event := TrailingStopMarketInitialization{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Quantity:     decimal.MustQuantity("100000"),
		TriggerType:  &triggerType, TrailingOffset: &offset,
		TrailingOffsetType: &offsetType,
	}
	order, err := TrailingStopMarketFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if order.TriggerPrice() != nil || order.ActivationPrice() != nil {
		t.Fatalf("optional prices = %v/%v", order.TriggerPrice(), order.ActivationPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_market.rs:894
//	test: test_trailing_stop_market_order_sets_slippage_when_filled
func TestTrailingStopMarketOrderSetsSlippageWhenFilled(t *testing.T) {
	order, err := NewTrailingStopMarket(TrailingStopMarketConfig{
		StrategyID:   ids.MustStrategyID("S-001"),
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("10"),
		TriggerPrice:       copyPointer(decimal.MustPrice("90.00")),
		TrailingOffset:     decimal.MustParse("0.5"),
		TrailingOffsetType: TrailingOffsetTypeNone,
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
	if order.Slippage() == nil || order.Slippage().String() != "8.5" {
		t.Fatalf("slippage = %v, want 8.5", order.Slippage())
	}
}
