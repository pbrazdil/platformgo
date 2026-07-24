package order

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func requireEventJSONRoundTrip[T any](t *testing.T, original T) {
	t.Helper()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded T
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round trip differs:\noriginal=%#v\ndecoded=%#v", original, decoded)
	}
}

func directOptionalIDs() (*ids.VenueOrderID, *ids.AccountID) {
	venue := ids.MustVenueOrderID("001")
	account := ids.MustAccountID("SIM-001")
	return &venue, &account
}

func directIdentity() (ids.TraderID, ids.StrategyID, ids.InstrumentID, ids.ClientOrderID) {
	return ids.DefaultTraderID(), ids.MustStrategyID("S-001"),
		ids.MustInstrumentID("BTCUSDT.COINBASE"),
		ids.MustClientOrderID("O-19700101-000000-001-001-1")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/accepted.rs:340
//	test: test_order_accepted_display
func TestDirectOrderAcceptedDisplay(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	event := OrderAcceptedEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument, ClientOrderID: client,
		VenueOrderID: ids.MustVenueOrderID("001"), AccountID: ids.MustAccountID("SIM-001"),
	}
	const want = "OrderAccepted(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, venue_order_id=001, account_id=SIM-001, ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/accepted.rs:349
//	test: test_order_accepted_serialization
func TestDirectOrderAcceptedSerialization(t *testing.T) {
	event := NewOrderAcceptedSpec(NewAcceptedEventIDSequence()).Build()
	requireEventJSONRoundTrip(t, event)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/cancel_rejected.rs:337
//	test: test_order_cancel_rejected
func TestDirectOrderCancelRejected(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	venue, account := directOptionalIDs()
	event := OrderCancelRejectedEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument, ClientOrderID: client,
		VenueOrderID: venue, AccountID: account, Reason: "ORDER_DOES_NOT_EXIST",
	}
	const want = "OrderCancelRejected(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, venue_order_id=001, account_id=SIM-001, reason='ORDER_DOES_NOT_EXIST', ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/cancel_rejected.rs:346
//	test: test_order_cancel_rejected_serialization
func TestDirectOrderCancelRejectedSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderCancelRejectedSpec(NewOrderSpecEventIDSequence()).Build())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/canceled.rs:329
//	test: test_serialization_roundtrip
func TestDirectOrderCanceledSerializationRoundTrip(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderCanceledSpec(NewCanceledEventIDSequence()).Build())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied.rs:313
//	test: test_order_denied_display
func TestDirectOrderDeniedDisplay(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	event := OrderDeniedDirectEvent{OrderDeniedEvent: OrderDeniedEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, Reason: "RATE_LIMIT_EXCEEDED",
	}}
	const want = "OrderDenied(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, reason='RATE_LIMIT_EXCEEDED')"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied.rs:322
//	test: test_order_denied_serialization
func TestDirectOrderDeniedSerialization(t *testing.T) {
	event := OrderDeniedDirectEvent{
		OrderDeniedEvent: NewOrderDeniedSpec(NewDeniedEventIDSequence()).Build(),
	}
	requireEventJSONRoundTrip(t, event)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied.rs:330
//	test: test_order_denied_serialization_with_causation_id
func TestDirectOrderDeniedSerializationWithCausationID(t *testing.T) {
	causationID := "00000000-0000-4000-8000-000000000999"
	event := OrderDeniedDirectEvent{
		OrderDeniedEvent: NewOrderDeniedSpec(NewDeniedEventIDSequence()).Build(),
		CausationID:      &causationID,
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"causation_id"`) {
		t.Fatalf("JSON lacks causation_id: %s", data)
	}
	var decoded OrderDeniedDirectEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CausationID == nil || *decoded.CausationID != causationID ||
		!reflect.DeepEqual(event, decoded) {
		t.Fatalf("decoded = %#v", decoded)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/emulated.rs:302
//	test: test_order_emulated
func TestDirectOrderEmulated(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	event := OrderEmulatedEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument, ClientOrderID: client,
	}
	const want = "OrderEmulated(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/emulated.rs:311
//	test: test_order_emulated_serialization
func TestDirectOrderEmulatedSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderEmulatedSpec(NewOrderSpecEventIDSequence()).Build())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/expired.rs:330
//	test: test_order_expired_display
func TestDirectOrderExpiredDisplay(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	venue, account := directOptionalIDs()
	event := OrderExpiredEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, VenueOrderID: venue, AccountID: account,
	}
	const want = "OrderExpired(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, venue_order_id=001, account_id=SIM-001, ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/expired.rs:339
//	test: test_order_expired_serialization
func TestDirectOrderExpiredSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderExpiredSpec(NewExpiredEventIDSequence()).Build())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/initialized.rs:587
//	test: test_order_initialized
func TestDirectOrderInitialized(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	price := decimal.MustPrice("22000")
	emulation := TriggerTypeBidAsk
	contingency := ContingencyTypeOTO
	listID := ids.MustOrderListID("1")
	event := OrderInitializedSpecEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, OrderSide: OrderSideBuy, OrderType: OrderTypeLimit,
		Quantity: decimal.MustQuantity("0.561"), TimeInForce: TimeInForceDay,
		PostOnly: true, ReduceOnly: true, Price: &price, EmulationTrigger: &emulation,
		TriggerInstrumentID: &instrument, ContingencyType: &contingency,
		OrderListID:    &listID,
		LinkedOrderIDs: []ids.ClientOrderID{ids.MustClientOrderID("O-2020872378424")},
	}
	const want = "OrderInitialized(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, side=BUY, type=LIMIT, quantity=0.561, time_in_force=DAY, post_only=true, reduce_only=true, quote_quantity=false, price=22_000, emulation_trigger=BID_ASK, trigger_instrument_id=BTCUSDT.COINBASE, contingency_type=OTO, order_list_id=1, linked_order_ids=[O-2020872378424], parent_order_id=None, exec_algorithm_id=None, exec_algorithm_params=None, exec_spawn_id=None, tags=None)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/initialized.rs:600
//	test: test_order_initialized_serialization
func TestDirectOrderInitializedSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderInitializedSpec(NewOrderSpecEventIDSequence()).Build())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/initialized.rs:608
//	test: test_order_initialized_serialization_preserves_activation_price
func TestDirectOrderInitializedSerializationPreservesActivationPrice(t *testing.T) {
	event := NewOrderInitializedSpec(NewOrderSpecEventIDSequence()).Build()
	activation := decimal.MustPrice("0.68500")
	event.ActivationPrice = &activation
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var decoded OrderInitializedSpecEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ActivationPrice == nil || decoded.ActivationPrice.String() != "0.68500" ||
		!reflect.DeepEqual(event, decoded) {
		t.Fatalf("decoded = %#v", decoded)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/modify_rejected.rs:336
//	test: test_order_modified_rejected
func TestDirectOrderModifiedRejected(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	venue, account := directOptionalIDs()
	event := OrderModifyRejectedEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, VenueOrderID: venue, AccountID: account,
		Reason: "ORDER_DOES_NOT_EXIST",
	}
	const want = "OrderModifyRejected(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, venue_order_id=001, account_id=SIM-001, reason='ORDER_DOES_NOT_EXIST', ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/modify_rejected.rs:346
//	test: test_order_modify_rejected_serialization
func TestDirectOrderModifyRejectedSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderModifyRejectedSpec(NewOrderSpecEventIDSequence()).Build())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/pending_cancel.rs:330
//	test: test_order_pending_cancel_display
func TestDirectOrderPendingCancelDisplay(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	venue, account := directOptionalIDs()
	event := OrderPendingCancelEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, VenueOrderID: venue, AccountID: account,
	}
	const want = "OrderPendingCancel(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, venue_order_id=001, account_id=SIM-001, ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/pending_cancel.rs:339
//	test: test_order_pending_cancel_serialization
func TestDirectOrderPendingCancelSerialization(t *testing.T) {
	account := ids.MustAccountID("SIM-001")
	venue := ids.MustVenueOrderID("001")
	event := NewOrderPendingCancelSpec(NewPendingCancelEventIDSequence()).
		WithAccountID(account).WithVenueOrderID(venue).Build()
	requireEventJSONRoundTrip(t, event)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/pending_cancel.rs:347
//	test: test_order_pending_cancel_none_account_serialization
func TestDirectOrderPendingCancelNoneAccountSerialization(t *testing.T) {
	event := NewOrderPendingCancelSpec(NewPendingCancelEventIDSequence()).
		WithOptionalAccountID(nil).Build()
	requireEventJSONRoundTrip(t, event)
	if event.AccountID != nil {
		t.Fatalf("account ID = %v", event.AccountID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/pending_update.rs:330
//	test: test_order_pending_update_display
func TestDirectOrderPendingUpdateDisplay(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	venue, account := directOptionalIDs()
	event := OrderPendingUpdateEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, VenueOrderID: venue, AccountID: account,
	}
	const want = "OrderPendingUpdate(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, venue_order_id=001, account_id=SIM-001, ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/pending_update.rs:339
//	test: test_order_pending_update_serialization
func TestDirectOrderPendingUpdateSerialization(t *testing.T) {
	event := NewOrderPendingUpdateSpec(NewPendingUpdateEventIDSequence()).
		WithAccountID(ids.MustAccountID("SIM-001")).
		WithVenueOrderID(ids.MustVenueOrderID("001")).Build()
	requireEventJSONRoundTrip(t, event)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/pending_update.rs:347
//	test: test_order_pending_update_none_account_serialization
func TestDirectOrderPendingUpdateNoneAccountSerialization(t *testing.T) {
	event := NewOrderPendingUpdateSpec(NewPendingUpdateEventIDSequence()).
		WithOptionalAccountID(nil).Build()
	requireEventJSONRoundTrip(t, event)
	if event.AccountID != nil {
		t.Fatalf("account ID = %v", event.AccountID)
	}
}

func directRejected(reason string, duePostOnly bool) OrderRejectedEvent {
	trader, strategy, instrument, client := directIdentity()
	return OrderRejectedEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, AccountID: ids.MustAccountID("SIM-001"),
		Reason: reason, DuePostOnly: duePostOnly,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/rejected.rs:346
//	test: test_order_rejected_display
func TestDirectOrderRejectedDisplay(t *testing.T) {
	event := directRejected("INSUFFICIENT_MARGIN", false)
	const want = "OrderRejected(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, account_id=SIM-001, reason='INSUFFICIENT_MARGIN', due_post_only=false, ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/rejected.rs:356
//	test: test_order_rejected_different_reasons
func TestDirectOrderRejectedDifferentReasons(t *testing.T) {
	insufficientMargin := directRejected("INSUFFICIENT_MARGIN", false)
	invalidPrice := directRejected("INVALID_PRICE", false)
	marketClosed := directRejected("MARKET_CLOSED", false)
	if reflect.DeepEqual(insufficientMargin, invalidPrice) ||
		reflect.DeepEqual(invalidPrice, marketClosed) ||
		insufficientMargin.Reason != "INSUFFICIENT_MARGIN" ||
		invalidPrice.Reason != "INVALID_PRICE" || marketClosed.Reason != "MARKET_CLOSED" {
		t.Fatalf("reasons did not distinguish events")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/rejected.rs:377
//	test: test_order_rejected_serialization
func TestDirectOrderRejectedSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, directRejected("INSUFFICIENT_MARGIN", false))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/rejected.rs:387
//	test: test_order_rejected_with_due_post_only
func TestDirectOrderRejectedWithDuePostOnly(t *testing.T) {
	event := directRejected("POST_ONLY_WOULD_EXECUTE", true)
	if !event.DuePostOnly || event.Reason != "POST_ONLY_WOULD_EXECUTE" {
		t.Fatalf("event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/released.rs:307
//	test: test_order_released_display
func TestDirectOrderReleasedDisplay(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	event := OrderReleasedEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, ReleasedPrice: decimal.MustPrice("22000"),
	}
	const want = "OrderReleased(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, released_price=22_000)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/released.rs:316
//	test: test_order_released_serialization
func TestDirectOrderReleasedSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderReleasedSpec(NewOrderSpecEventIDSequence()).Build())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/submitted.rs:327
//	test: test_order_rejected_display
func TestDirectOrderSubmittedDisplay(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	event := OrderSubmittedEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, AccountID: ids.MustAccountID("SIM-001"),
	}
	const want = "OrderSubmitted(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, account_id=SIM-001, ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/submitted.rs:336
//	test: test_order_submitted_serialization
func TestDirectOrderSubmittedSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderSubmittedSpec(NewOrderSpecEventIDSequence()).Build())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/triggered.rs:331
//	test: test_order_triggered_display
func TestDirectOrderTriggeredDisplay(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	venue, account := directOptionalIDs()
	event := OrderTriggeredEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, VenueOrderID: venue, AccountID: account,
	}
	const want = "OrderTriggered(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, venue_order_id=001, account_id=SIM-001, ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/triggered.rs:341
//	test: test_order_triggered_serialization
func TestDirectOrderTriggeredSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderTriggeredSpec(NewOrderSpecEventIDSequence()).Build())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/updated.rs:368
//	test: test_order_updated_display
func TestDirectOrderUpdatedDisplay(t *testing.T) {
	trader, strategy, instrument, client := directIdentity()
	venue, account := directOptionalIDs()
	price := decimal.MustPrice("22000")
	event := OrderUpdatedEvent{
		TraderID: trader, StrategyID: strategy, InstrumentID: instrument,
		ClientOrderID: client, VenueOrderID: venue, AccountID: account,
		Quantity: decimal.MustQuantity("100"), Price: &price,
	}
	const want = "OrderUpdated(instrument_id=BTCUSDT.COINBASE, client_order_id=O-19700101-000000-001-001-1, venue_order_id=001, account_id=SIM-001, quantity=100, price=22_000, trigger_price=None, protection_price=None, ts_event=0)"
	if event.String() != want {
		t.Fatalf("display = %q, want %q", event.String(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/updated.rs:377
//	test: test_order_updated_serialization
func TestDirectOrderUpdatedSerialization(t *testing.T) {
	requireEventJSONRoundTrip(t, NewOrderUpdatedSpec(NewOrderSpecEventIDSequence()).Build())
}
