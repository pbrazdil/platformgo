package order

import (
	"errors"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func anyBaseConfig(orderType OrderType) Config {
	return Config{
		ClientOrderID: ids.MustClientOrderID("ORDER-001"),
		InstrumentID:  ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Side:          OrderSideBuy,
		Type:          orderType,
		Quantity:      decimal.MustQuantity("10"),
	}
}

func requireReplayError(t *testing.T, err error, kind ReplayErrorKind, message string) *ReplayError {
	t.Helper()
	var replayError *ReplayError
	if !errors.As(err, &replayError) || replayError.Kind != kind {
		t.Fatalf("error = %#v, want replay kind %q", err, kind)
	}
	if replayError.Error() != message {
		t.Fatalf("error = %q, want %q", replayError, message)
	}
	return replayError
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:387
//	test: test_order_any_equality
func TestOrderAnyEquality(t *testing.T) {
	market, err := NewAny(anyBaseConfig(OrderTypeMarket))
	if err != nil {
		t.Fatal(err)
	}
	limitConfig := anyBaseConfig(OrderTypeLimit)
	price := decimal.MustPrice("100.00")
	limitConfig.Price = &price
	limit, err := NewAny(limitConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !market.Equal(limit) {
		t.Fatal("orders with the same client order ID are not equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:409
//	test: test_order_any_conversion_from_events
func TestOrderAnyConversionFromEvents(t *testing.T) {
	initialization := Initialization{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Type:         OrderTypeMarket, Quantity: decimal.MustQuantity("10"),
	}
	order, err := FromEvents([]AnyEvent{InitializationEvent(initialization)})
	if err != nil {
		t.Fatal(err)
	}
	if order.OrderType() != OrderTypeMarket ||
		order.InstrumentID().String() != initialization.InstrumentID.String() ||
		order.Quantity().Cmp(initialization.Quantity) != 0 {
		t.Fatalf("replayed order = %+v", order)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:430
//	test: test_order_any_from_events_empty_error
func TestOrderAnyFromEventsEmptyError(t *testing.T) {
	_, err := FromEvents(nil)
	requireReplayError(t, err, ReplayErrorEmptyInput, "No order events provided to create OrderAny")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:442
//	test: test_order_any_from_events_invalid_init_returns_error
func TestOrderAnyFromEventsInvalidInitializationReturnsError(t *testing.T) {
	initialization := Initialization{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Type:         OrderTypeLimit, Quantity: decimal.MustQuantity("10"),
	}
	_, err := FromEvents([]AnyEvent{InitializationEvent(initialization)})
	replayError := requireReplayError(
		t, err, ReplayErrorInvalidInitialization,
		"Invalid `OrderInitialized` event: `price` is required for `LimitOrder` initialization",
	)
	if replayError.Source.Error() != "`price` is required for `LimitOrder` initialization" {
		t.Fatalf("source error = %q", replayError.Source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:483
//	test: test_order_any_from_events_invalid_predicate_returns_error
func TestOrderAnyFromEventsInvalidPredicateReturnsError(t *testing.T) {
	triggerType := TriggerTypeLastPrice
	for _, tc := range []struct {
		side        OrderSide
		price       string
		trigger     string
		messagePart string
	}{
		{OrderSideBuy, "100.00", "101.00", "BUY Limit-If-Touched"},
		{OrderSideSell, "100.00", "99.00", "SELL Limit-If-Touched"},
	} {
		price := decimal.MustPrice(tc.price)
		trigger := decimal.MustPrice(tc.trigger)
		initialization := Initialization{
			InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
			Side:         tc.side, Type: OrderTypeLimitIfTouched,
			Quantity: decimal.MustQuantity("10"), Price: &price,
			TriggerPrice: &trigger, TriggerType: &triggerType,
		}
		_, err := FromEvents([]AnyEvent{InitializationEvent(initialization)})
		var replayError *ReplayError
		if !errors.As(err, &replayError) ||
			replayError.Kind != ReplayErrorInvalidInitialization ||
			!replayError.Contains("Invalid `OrderInitialized` event", tc.messagePart) {
			t.Fatalf("side %v error = %#v", tc.side, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:720
//	test: test_order_any_from_events_missing_required_field_returns_error
func TestOrderAnyFromEventsMissingRequiredFieldReturnsError(t *testing.T) {
	price := decimal.MustPrice("100.00")
	triggerPrice := decimal.MustPrice("99.00")
	triggerType := TriggerTypeLastPrice
	offset := decimal.MustParse("1")
	offsetType := TrailingOffsetTypePrice
	base := func(orderType OrderType) Initialization {
		return Initialization{
			InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
			Type:         orderType, Quantity: decimal.MustQuantity("10"),
			Price: &price, TriggerPrice: &triggerPrice, TriggerType: &triggerType,
			LimitOffset: &offset, TrailingOffset: &offset, TrailingOffsetType: &offsetType,
		}
	}
	type testCase struct {
		name    string
		init    Initialization
		message string
	}
	cases := make([]testCase, 0, 17)
	add := func(name string, init Initialization, message string) {
		cases = append(cases, testCase{name, init, message})
	}
	init := base(OrderTypeLimitIfTouched)
	init.Price = nil
	add("lit missing price", init, "`price` is required for `LimitIfTouchedOrder`")
	init = base(OrderTypeLimitIfTouched)
	init.TriggerPrice = nil
	add("lit missing trigger price", init, "`trigger_price` is required for `LimitIfTouchedOrder`")
	init = base(OrderTypeLimitIfTouched)
	init.TriggerType = nil
	add("lit missing trigger type", init, "`trigger_type` is required for `LimitIfTouchedOrder`")
	init = base(OrderTypeStopLimit)
	init.Price = nil
	add("stop limit missing price", init, "`price` is required for `StopLimitOrder`")
	init = base(OrderTypeStopLimit)
	init.TriggerPrice = nil
	add("stop limit missing trigger price", init, "`trigger_price` is required for `StopLimitOrder`")
	init = base(OrderTypeStopLimit)
	init.TriggerType = nil
	add("stop limit missing trigger type", init, "`trigger_type` is required for `StopLimitOrder`")
	init = base(OrderTypeStopMarket)
	init.TriggerPrice = nil
	add("stop market missing trigger price", init, "`trigger_price` is required for `StopMarketOrder`")
	init = base(OrderTypeStopMarket)
	init.TriggerType = nil
	add("stop market missing trigger type", init, "`trigger_type` is required for `StopMarketOrder`")
	init = base(OrderTypeMarketIfTouched)
	init.TriggerPrice = nil
	add("market if touched missing trigger price", init, "`trigger_price` is required for `MarketIfTouchedOrder`")
	init = base(OrderTypeMarketIfTouched)
	init.TriggerType = nil
	add("market if touched missing trigger type", init, "`trigger_type` is required for `MarketIfTouchedOrder`")
	init = base(OrderTypeTrailingStopLimit)
	init.TriggerType = nil
	add("trailing stop limit missing trigger type", init, "`trigger_type` is required for `TrailingStopLimitOrder`")
	init = base(OrderTypeTrailingStopLimit)
	init.LimitOffset = nil
	add("trailing stop limit missing limit offset", init, "`limit_offset` is required for `TrailingStopLimitOrder`")
	init = base(OrderTypeTrailingStopLimit)
	init.TrailingOffset = nil
	add("trailing stop limit missing trailing offset", init, "`trailing_offset` is required for `TrailingStopLimitOrder`")
	init = base(OrderTypeTrailingStopLimit)
	init.TrailingOffsetType = nil
	add("trailing stop limit missing trailing offset type", init, "`trailing_offset_type` is required for `TrailingStopLimitOrder`")
	init = base(OrderTypeTrailingStopMarket)
	init.TriggerType = nil
	add("trailing stop market missing trigger type", init, "`trigger_type` is required for `TrailingStopMarketOrder`")
	init = base(OrderTypeTrailingStopMarket)
	init.TrailingOffset = nil
	add("trailing stop market missing trailing offset", init, "`trailing_offset` is required for `TrailingStopMarketOrder`")
	init = base(OrderTypeTrailingStopMarket)
	init.TrailingOffsetType = nil
	add("trailing stop market missing trailing offset type", init, "`trailing_offset_type` is required for `TrailingStopMarketOrder`")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromEvents([]AnyEvent{InitializationEvent(tc.init)})
			var replayError *ReplayError
			if !errors.As(err, &replayError) ||
				replayError.Kind != ReplayErrorInvalidInitialization ||
				!strings.Contains(replayError.Error(), "Invalid `OrderInitialized` event") ||
				!strings.Contains(replayError.Error(), tc.message) {
				t.Fatalf("error = %#v, want message containing %q", err, tc.message)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:742
//	test: test_order_any_from_events_wrong_first_event
func TestOrderAnyFromEventsWrongFirstEvent(t *testing.T) {
	quantity := decimal.MustQuantity("20")
	_, err := FromEvents([]AnyEvent{UpdateEvent(Update{Quantity: &quantity})})
	requireReplayError(t, err, ReplayErrorWrongFirstEvent, "First event must be `OrderInitialized`")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:764
//	test: test_order_any_from_events_apply_failure
func TestOrderAnyFromEventsApplyFailure(t *testing.T) {
	initialization := Initialization{
		InstrumentID: ids.MustInstrumentID("BTC-USDT.BINANCE"),
		Type:         OrderTypeMarket, Quantity: decimal.MustQuantity("10"),
	}
	event := InitializationEvent(initialization)
	_, err := FromEvents([]AnyEvent{event, event})
	replayError := requireReplayError(t, err, ReplayErrorApplyFailed, "Invalid order state transition")
	var orderError *Error
	if !errors.As(replayError.Source, &orderError) ||
		orderError.Kind != ErrorInvalidStateTransition {
		t.Fatalf("source = %#v", replayError.Source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:788
//	test: test_passive_order_any_conversion
func TestPassiveOrderAnyConversion(t *testing.T) {
	config := anyBaseConfig(OrderTypeLimit)
	price := decimal.MustPrice("100.00")
	config.Price = &price
	order, err := NewAny(config)
	if err != nil {
		t.Fatal(err)
	}
	passive, err := ToPassiveAny(order)
	if err != nil {
		t.Fatal(err)
	}
	converted := passive.ToAny()
	if converted.OrderType() != OrderTypeLimit ||
		converted.Quantity().Cmp(decimal.MustQuantity("10")) != 0 {
		t.Fatalf("converted order = %+v", converted)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:806
//	test: test_stop_order_any_conversion
func TestStopOrderAnyConversion(t *testing.T) {
	config := anyBaseConfig(OrderTypeStopMarket)
	trigger := decimal.MustPrice("100.00")
	config.TriggerPrice = &trigger
	order, err := NewAny(config)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := ToStopAny(order)
	if err != nil {
		t.Fatal(err)
	}
	converted := stop.ToAny()
	if converted.OrderType() != OrderTypeStopMarket ||
		converted.Quantity().Cmp(decimal.MustQuantity("10")) != 0 ||
		converted.TriggerPrice() == nil || converted.TriggerPrice().String() != "100.00" {
		t.Fatalf("converted stop order = %+v", converted)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:825
//	test: test_limit_order_any_conversion
func TestLimitOrderAnyConversion(t *testing.T) {
	config := anyBaseConfig(OrderTypeLimit)
	price := decimal.MustPrice("100.00")
	config.Price = &price
	order, err := NewAny(config)
	if err != nil {
		t.Fatal(err)
	}
	limit, err := ToLimitAny(order)
	if err != nil {
		t.Fatal(err)
	}
	converted := limit.ToAny()
	if converted.OrderType() != OrderTypeLimit ||
		converted.Quantity().Cmp(decimal.MustQuantity("10")) != 0 {
		t.Fatalf("converted limit order = %+v", converted)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:843
//	test: test_limit_order_any_limit_price
func TestLimitOrderAnyLimitPrice(t *testing.T) {
	config := anyBaseConfig(OrderTypeLimit)
	price := decimal.MustPrice("100.00")
	config.Price = &price
	order, err := NewAny(config)
	if err != nil {
		t.Fatal(err)
	}
	limit, err := ToLimitAny(order)
	if err != nil {
		t.Fatal(err)
	}
	if limit.LimitPrice().String() != "100.00" {
		t.Fatalf("limit price = %s", limit.LimitPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:860
//	test: test_stop_order_any_stop_price
func TestStopOrderAnyStopPrice(t *testing.T) {
	config := anyBaseConfig(OrderTypeStopMarket)
	trigger := decimal.MustPrice("100.00")
	config.TriggerPrice = &trigger
	order, err := NewAny(config)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := ToStopAny(order)
	if err != nil {
		t.Fatal(err)
	}
	if stop.StopPrice() == nil || stop.StopPrice().String() != "100.00" {
		t.Fatalf("stop price = %v", stop.StopPrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:877
//	test: test_trailing_stop_market_order_conversion
func TestTrailingStopMarketOrderConversion(t *testing.T) {
	config := anyBaseConfig(OrderTypeTrailingStopMarket)
	trigger := decimal.MustPrice("100.00")
	offset := decimal.MustParse("0.5")
	config.TriggerPrice = &trigger
	config.TrailingOffset = &offset
	config.TrailingOffsetType = TrailingOffsetTypeNone
	order, err := NewAny(config)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := ToStopAny(order)
	if err != nil {
		t.Fatal(err)
	}
	converted := stop.ToAny()
	if converted.OrderType() != OrderTypeTrailingStopMarket ||
		converted.Quantity().Cmp(decimal.MustQuantity("10")) != 0 ||
		converted.TriggerPrice() == nil || converted.TriggerPrice().String() != "100.00" ||
		converted.TrailingOffset() == nil || converted.TrailingOffset().String() != "0.5" ||
		converted.TrailingOffsetType() == nil ||
		*converted.TrailingOffsetType() != TrailingOffsetTypeNone {
		t.Fatalf("converted trailing market order = %+v", converted)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:905
//	test: test_trailing_stop_limit_order_conversion
func TestTrailingStopLimitOrderConversion(t *testing.T) {
	config := anyBaseConfig(OrderTypeTrailingStopLimit)
	price := decimal.MustPrice("99.00")
	trigger := decimal.MustPrice("100.00")
	limitOffset := decimal.MustParse("1.0")
	trailingOffset := decimal.MustParse("0.5")
	config.Price, config.TriggerPrice = &price, &trigger
	config.LimitOffset, config.TrailingOffset = &limitOffset, &trailingOffset
	config.TrailingOffsetType = TrailingOffsetTypeNone
	order, err := NewAny(config)
	if err != nil {
		t.Fatal(err)
	}
	limit, err := ToLimitAny(order)
	if err != nil {
		t.Fatal(err)
	}
	if limit.LimitPrice().String() != "99.00" {
		t.Fatalf("limit price = %s", limit.LimitPrice())
	}
	converted := limit.ToAny()
	if converted.OrderType() != OrderTypeTrailingStopLimit ||
		converted.Quantity().Cmp(decimal.MustQuantity("10")) != 0 ||
		converted.Price() == nil || converted.Price().String() != "99.00" ||
		converted.TriggerPrice() == nil || converted.TriggerPrice().String() != "100.00" ||
		converted.TrailingOffset() == nil || converted.TrailingOffset().String() != "0.5" {
		t.Fatalf("converted trailing limit order = %+v", converted)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/any.rs:935
//	test: test_passive_order_any_to_any
func TestPassiveOrderAnyToAny(t *testing.T) {
	config := anyBaseConfig(OrderTypeLimit)
	price := decimal.MustPrice("100.00")
	config.Price = &price
	order, err := NewAny(config)
	if err != nil {
		t.Fatal(err)
	}
	passive, err := ToPassiveAny(order)
	if err != nil {
		t.Fatal(err)
	}
	converted := passive.ToAny()
	if converted.OrderType() != OrderTypeLimit ||
		converted.Quantity().Cmp(decimal.MustQuantity("10")) != 0 ||
		converted.Price() == nil || converted.Price().String() != "100.00" {
		t.Fatalf("passive to-any result = %+v", converted)
	}
}
