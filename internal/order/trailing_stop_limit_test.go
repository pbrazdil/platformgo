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
//	source: crates/model/src/orders/trailing_stop_limit.rs:700
//	test: test_initialize
func TestTrailingStopLimitInitialize(t *testing.T) {
	order, err := NewTrailingStopLimit(TrailingStopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:          copyPointer(decimal.MustPrice("0.67500")),
		TriggerPrice:   copyPointer(decimal.MustPrice("0.68000")),
		LimitOffset:    decimal.MustParse("5"),
		TrailingOffset: decimal.MustParse("10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.TriggerPrice() == nil || order.TriggerPrice().String() != "0.68000" ||
		order.Price() == nil || order.Price().String() != "0.67500" ||
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
//	source: crates/model/src/orders/trailing_stop_limit.rs:724
//	test: test_display
func TestTrailingStopLimitDisplay(t *testing.T) {
	order, err := NewTrailingStopLimit(TrailingStopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:              copyPointer(decimal.MustPrice("0.67500")),
		TriggerPrice:       copyPointer(decimal.MustPrice("0.68000")),
		TriggerType:        TriggerTypeLastPrice,
		LimitOffset:        decimal.MustParse("5"),
		TrailingOffset:     decimal.MustParse("10"),
		TrailingOffsetType: TrailingOffsetTypePrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "TrailingStopLimitOrder(BUY 1 AUD/USD.SIM TRAILING_STOP_LIMIT GTC, status=INITIALIZED, client_order_id=O-19700101-000000-001-001-1, venue_order_id=None, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=None, activation_price=None, is_activated=false)"
	if order.String() != expected {
		t.Fatalf("display = %q, want %q", order.String(), expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_limit.rs:745
//	test: test_display_qty_gt_quantity_err
func TestTrailingStopLimitDisplayQuantityGreaterThanQuantityError(t *testing.T) {
	display := decimal.MustQuantity("2")
	_, err := NewTrailingStopLimit(TrailingStopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:              copyPointer(decimal.MustPrice("0.67500")),
		TriggerPrice:       copyPointer(decimal.MustPrice("0.68000")),
		TriggerType:        TriggerTypeLastPrice,
		LimitOffset:        decimal.MustParse("5"),
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
//	source: crates/model/src/orders/trailing_stop_limit.rs:764
//	test: test_quantity_zero_err
func TestTrailingStopLimitQuantityZeroError(t *testing.T) {
	_, err := NewTrailingStopLimit(TrailingStopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("0"),
		Price:              copyPointer(decimal.MustPrice("0.67500")),
		TriggerPrice:       copyPointer(decimal.MustPrice("0.68000")),
		TriggerType:        TriggerTypeLastPrice,
		LimitOffset:        decimal.MustParse("5"),
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
//	source: crates/model/src/orders/trailing_stop_limit.rs:780
//	test: test_gtd_without_expire_err
func TestTrailingStopLimitGTDWithoutExpireError(t *testing.T) {
	_, err := NewTrailingStopLimit(TrailingStopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		Price:              copyPointer(decimal.MustPrice("0.67500")),
		TriggerPrice:       copyPointer(decimal.MustPrice("0.68000")),
		TriggerType:        TriggerTypeLastPrice,
		LimitOffset:        decimal.MustParse("5"),
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
//	source: crates/model/src/orders/trailing_stop_limit.rs:796
//	test: test_trailing_stop_limit_order_update
func TestTrailingStopLimitOrderUpdate(t *testing.T) {
	order, err := NewTrailingStopLimit(TrailingStopLimitConfig{
		StrategyID:         ids.MustStrategyID("S-001"),
		InstrumentID:       ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:           decimal.MustQuantity("10"),
		Price:              copyPointer(decimal.MustPrice("100.00")),
		TriggerPrice:       copyPointer(decimal.MustPrice("95.00")),
		LimitOffset:        decimal.MustParse("2.0"),
		TrailingOffset:     decimal.MustParse("1.0"),
		TrailingOffsetType: TrailingOffsetTypePrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 1); err != nil {
		t.Fatal(err)
	}
	triggerPrice := decimal.MustPrice("90.00")
	if err := order.ApplyUpdate(TrailingStopLimitUpdate{
		ClientOrderID: order.ClientOrderID(), StrategyID: order.StrategyID(),
		TriggerPrice: &triggerPrice, Quantity: decimal.MustQuantity("5"),
		Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.Quantity(), "5")
	if order.TriggerPrice() == nil || order.TriggerPrice().String() != "90.00" {
		t.Fatalf("trigger price = %v", order.TriggerPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_limit.rs:827
//	test: test_trailing_stop_limit_order_trigger_instrument_id
func TestTrailingStopLimitOrderTriggerInstrumentID(t *testing.T) {
	triggerInstrumentID := ids.MustInstrumentID("ETH-USDT.BINANCE")
	order, err := NewTrailingStopLimit(TrailingStopLimitConfig{
		InstrumentID:        ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Quantity:            decimal.MustQuantity("10"),
		Price:               copyPointer(decimal.MustPrice("100.00")),
		TriggerPrice:        copyPointer(decimal.MustPrice("95.00")),
		LimitOffset:         decimal.MustParse("2.0"),
		TrailingOffset:      decimal.MustParse("1.0"),
		TrailingOffsetType:  TrailingOffsetTypePrice,
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
//	source: crates/model/src/orders/trailing_stop_limit.rs:844
//	test: test_activation_price_round_trips_through_event
func TestTrailingStopLimitActivationPriceRoundTripsThroughEvent(t *testing.T) {
	order, err := NewTrailingStopLimit(TrailingStopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		ActivationPrice: copyPointer(decimal.MustPrice("0.68500")),
		Price:           copyPointer(decimal.MustPrice("0.67500")),
		TriggerPrice:    copyPointer(decimal.MustPrice("0.68000")),
		LimitOffset:     decimal.MustParse("5"),
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
	rebuilt, err := TrailingStopLimitFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.ActivationPrice() == nil || rebuilt.ActivationPrice().String() != "0.68500" ||
		rebuilt.Price() == nil || rebuilt.Price().String() != "0.67500" ||
		rebuilt.TriggerPrice() == nil || rebuilt.TriggerPrice().String() != "0.68000" {
		t.Fatalf("rebuilt prices = %v/%v/%v", rebuilt.ActivationPrice(), rebuilt.Price(), rebuilt.TriggerPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_limit.rs:868
//	test: test_has_price_false_until_limit_materializes
func TestTrailingStopLimitHasPriceFalseUntilLimitMaterializes(t *testing.T) {
	order, err := NewTrailingStopLimit(TrailingStopLimitConfig{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Side:         OrderSideBuy, Quantity: decimal.MustQuantity("1"),
		LimitOffset:    decimal.MustParse("5"),
		TrailingOffset: decimal.MustParse("10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.Price() != nil || order.HasPrice() {
		t.Fatalf("price/has-price = %v/%t", order.Price(), order.HasPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_limit.rs:884
//	test: test_reconstruct_with_price_trigger_and_activation_none
func TestTrailingStopLimitReconstructWithPriceTriggerAndActivationNil(t *testing.T) {
	triggerType := TriggerTypeDefault
	limitOffset := decimal.MustParse("5")
	trailingOffset := decimal.MustParse("10")
	offsetType := TrailingOffsetTypePrice
	event := TrailingStopLimitInitialization{
		InstrumentID: ids.MustInstrumentID("AUD/USD.SIM"),
		Quantity:     decimal.MustQuantity("100000"),
		TriggerType:  &triggerType, LimitOffset: &limitOffset,
		TrailingOffset: &trailingOffset, TrailingOffsetType: &offsetType,
	}
	order, err := TrailingStopLimitFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if order.Price() != nil || order.TriggerPrice() != nil || order.ActivationPrice() != nil {
		t.Fatalf("optional prices = %v/%v/%v", order.Price(), order.TriggerPrice(), order.ActivationPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/trailing_stop_limit.rs:901
//	test: test_trailing_stop_limit_order_from_order_initialized
func TestTrailingStopLimitOrderFromOrderInitialized(t *testing.T) {
	price := decimal.MustPrice("100.00")
	triggerPrice := decimal.MustPrice("95.00")
	triggerType := TriggerTypeDefault
	limitOffset := decimal.MustParse("2.0")
	trailingOffset := decimal.MustParse("1.0")
	offsetType := TrailingOffsetTypePrice
	event := TrailingStopLimitInitialization{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Side:          OrderSideBuy, Quantity: decimal.MustQuantity("100000"),
		Price: &price, TriggerPrice: &triggerPrice, TriggerType: &triggerType,
		LimitOffset: &limitOffset, TrailingOffset: &trailingOffset,
		TrailingOffsetType: &offsetType, TimeInForce: TimeInForceGTC,
	}
	order, err := TrailingStopLimitFromInitialization(event)
	if err != nil {
		t.Fatal(err)
	}
	if order.TraderID() != event.TraderID || order.StrategyID() != event.StrategyID ||
		order.InstrumentID().String() != event.InstrumentID.String() ||
		order.ClientOrderID() != event.ClientOrderID || order.Side() != event.Side ||
		order.Quantity().Cmp(event.Quantity) != 0 ||
		order.Price() == nil || order.Price().Cmp(*event.Price) != 0 ||
		order.TriggerPrice() == nil || order.TriggerPrice().Cmp(*event.TriggerPrice) != 0 ||
		order.TriggerType() != *event.TriggerType ||
		order.LimitOffset().Cmp(*event.LimitOffset) != 0 ||
		order.TrailingOffset().Cmp(*event.TrailingOffset) != 0 ||
		order.TrailingOffsetType() != *event.TrailingOffsetType ||
		order.TimeInForce() != event.TimeInForce ||
		order.ExpireTime() != nil {
		t.Fatalf("converted order = %+v", order)
	}
}
