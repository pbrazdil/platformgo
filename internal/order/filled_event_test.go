package order

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func displayedOrderFilledEvent() OrderFilledEvent {
	usdt := currency.USDT()
	commission := money.MustNew("12.2", usdt)
	return NewOrderFilledEvent(OrderFilledEventConfig{
		TraderID:      ids.MustTraderID("TRADER-001"),
		StrategyID:    ids.MustStrategyID("EMACross-001"),
		InstrumentID:  ids.MustInstrumentID("BTCUSDT.COINBASE"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		VenueOrderID:  ids.MustVenueOrderID("123456"),
		AccountID:     ids.MustAccountID("SIM-001"),
		TradeID:       ids.MustTradeID("1"),
		OrderSide:     OrderSideBuy,
		OrderType:     OrderTypeLimit,
		LastQuantity:  decimal.MustQuantity("0.561"),
		LastPrice:     decimal.MustPrice("22000"),
		Currency:      usdt,
		LiquiditySide: LiquiditySideTaker,
		EventID:       "16578139-a945-4b65-b46c-bc131a15d8e7",
		Commission:    &commission,
	})
}

func createdOrderFilledEvent() OrderFilledEvent {
	usd := currency.USD()
	positionID := ids.MustPositionID("P-001")
	commission := money.MustNew("2.5", usd)
	return NewOrderFilledEvent(OrderFilledEventConfig{
		TraderID:      ids.MustTraderID("TRADER-001"),
		StrategyID:    ids.MustStrategyID("EMA-CROSS"),
		InstrumentID:  ids.MustInstrumentID("EURUSD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		VenueOrderID:  ids.MustVenueOrderID("V-001"),
		AccountID:     ids.MustAccountID("SIM-001"),
		TradeID:       ids.MustTradeID("T-001"),
		OrderSide:     OrderSideBuy,
		OrderType:     OrderTypeMarket,
		LastQuantity:  decimal.MustQuantity("100"),
		LastPrice:     decimal.MustPrice("1.0500"),
		Currency:      usd,
		LiquiditySide: LiquiditySideTaker,
		EventID:       "00000000-0000-0000-0000-000000000000",
		TsEvent:       1_000_000_000,
		TsInit:        2_000_000_000,
		PositionID:    &positionID,
		Commission:    &commission,
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/filled.rs:464
//	test: test_order_filled_display
func TestOrderFilledEventDisplay(t *testing.T) {
	event := displayedOrderFilledEvent()

	want := "OrderFilled(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, venue_order_id=123456, account_id=SIM-001, trade_id=1, position_id=None, order_side=BUY, order_type=LIMIT, last_qty=0.561, last_px=22_000 USDT, commission=12.20000000 USDT, liquidity_side=TAKER, ts_event=0)"
	if got := event.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/filled.rs:476
//	test: test_order_filled_is_buy
func TestOrderFilledEventIsBuy(t *testing.T) {
	event := displayedOrderFilledEvent()

	if !event.IsBuy() || event.IsSell() {
		t.Fatalf("IsBuy()/IsSell() = %v/%v", event.IsBuy(), event.IsSell())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/filled.rs:482
//	test: test_order_filled_info_round_trips_through_serde
func TestOrderFilledEventInfoRoundTripsThroughSerde(t *testing.T) {
	original := displayedOrderFilledEvent()
	original.Info = []FillEventInfo{
		{Key: "liquidation", Value: "true"},
		{Key: "maker_order_id", Value: "ABC-123"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded OrderFilledEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !reflectFillInfoEqual(decoded.Info, original.Info) || !decoded.Equal(original) {
		t.Fatalf("roundtrip changed event: %#v != %#v", decoded, original)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/filled.rs:499
//	test: test_order_filled_is_sell
func TestOrderFilledEventIsSell(t *testing.T) {
	event := createdOrderFilledEvent()
	event.OrderSide = OrderSideSell

	if !event.IsSell() || event.IsBuy() {
		t.Fatalf("IsSell()/IsBuy() = %v/%v", event.IsSell(), event.IsBuy())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/filled.rs:508
//	test: test_order_filled_specified_side
func TestOrderFilledEventSpecifiedSide(t *testing.T) {
	buy := createdOrderFilledEvent()
	if buy.SpecifiedSide() != SpecifiedOrderSideBuy {
		t.Fatalf("buy SpecifiedSide() = %d", buy.SpecifiedSide())
	}

	sell := createdOrderFilledEvent()
	sell.OrderSide = OrderSideSell
	if sell.SpecifiedSide() != SpecifiedOrderSideSell {
		t.Fatalf("sell SpecifiedSide() = %d", sell.SpecifiedSide())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/filled.rs:518
//	test: test_order_filled_without_position_id_display
func TestOrderFilledEventWithoutPositionIDDisplay(t *testing.T) {
	event := createdOrderFilledEvent()
	event.PositionID = nil

	display := event.String()
	if !strings.Contains(display, "position_id=None") ||
		strings.Contains(display, "position_id=P-001") {
		t.Fatalf("String() = %q", display)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/filled.rs:528
//	test: test_order_filled_without_commission_serialization
func TestOrderFilledEventWithoutCommissionSerialization(t *testing.T) {
	original := createdOrderFilledEvent()
	original.Commission = nil

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded OrderFilledEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.Commission != nil || decoded.TradeID != original.TradeID {
		t.Fatalf("decoded commission/trade = %#v/%s", decoded.Commission, decoded.TradeID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/filled.rs:540
//	test: test_order_filled_serialization
func TestOrderFilledEventSerialization(t *testing.T) {
	original := createdOrderFilledEvent()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded OrderFilledEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !original.Equal(decoded) {
		t.Fatalf("roundtrip changed event: %#v != %#v", original, decoded)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/filled.rs:550
//	test: test_order_filled_serialization_with_causation_id
func TestOrderFilledEventSerializationWithCausationID(t *testing.T) {
	original := createdOrderFilledEvent()
	causationID := "00000000-0000-4000-8000-000000000002"
	original.CausationID = &causationID

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded OrderFilledEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"causation_id"`) ||
		decoded.CausationID == nil || *decoded.CausationID != causationID ||
		!original.Equal(decoded) {
		t.Fatalf("causation roundtrip = %s / %#v", data, decoded)
	}
}

func reflectFillInfoEqual(left, right []FillEventInfo) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
