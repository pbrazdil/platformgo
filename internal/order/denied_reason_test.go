package order

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied_reason.rs:386
//	test: renders_subject_led_messages
func TestOrderDeniedReasonRendersSubjectLedMessages(t *testing.T) {
	exceeds := OrderDeniedReason{
		Code:              DeniedQuantityExceedsMaximum,
		EffectiveQuantity: decimal.MustQuantity("15"),
		LimitQuantity:     decimal.MustQuantity("10"),
	}
	below := OrderDeniedReason{
		Code:              DeniedQuantityBelowMinimum,
		EffectiveQuantity: decimal.MustQuantity("1"),
		LimitQuantity:     decimal.MustQuantity("5"),
	}
	notional := OrderDeniedReason{
		Code:        DeniedNotionalBelowMinimum,
		FirstMoney:  money.MustNew("1.00", currency.USD()),
		SecondMoney: money.MustNew("0.90", currency.USD()),
	}

	if got, want := exceeds.String(), "QUANTITY_EXCEEDS_MAXIMUM: effective_quantity=15, max_quantity=10"; got != want {
		t.Fatalf("exceeds = %q, want %q", got, want)
	}
	if got, want := below.String(), "QUANTITY_BELOW_MINIMUM: effective_quantity=1, min_quantity=5"; got != want {
		t.Fatalf("below = %q, want %q", got, want)
	}
	if got, want := notional.String(), "NOTIONAL_BELOW_MINIMUM: min_notional=Money(1.00, USD), notional=Money(0.90, USD)"; got != want {
		t.Fatalf("notional = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied_reason.rs:415
//	test: renders_lifecycle_and_state_messages
func TestOrderDeniedReasonRendersLifecycleAndStateMessages(t *testing.T) {
	instrumentID := ids.MustInstrumentID("AUD/USD.SIM")
	notFound := OrderDeniedReason{Code: DeniedInstrumentNotFound, InstrumentID: instrumentID}
	badSide := OrderDeniedReason{Code: DeniedInvalidOrderSide, OrderSide: OrderSideNoOrderSide}
	reducing := OrderDeniedReason{
		Code:         DeniedTradingStateReducing,
		OrderSide:    OrderSideBuy,
		InstrumentID: instrumentID,
	}

	cases := []struct {
		reason OrderDeniedReason
		want   string
	}{
		{notFound, "INSTRUMENT_NOT_FOUND: instrument_id=AUD/USD.SIM"},
		{badSide, "INVALID_ORDER_SIDE: NO_ORDER_SIDE"},
		{OrderDeniedReason{Code: DeniedTradingHalted}, "TRADING_HALTED"},
		{OrderDeniedReason{Code: DeniedRateLimitExceeded}, "RATE_LIMIT_EXCEEDED"},
		{reducing, "TRADING_STATE_REDUCING: order_side=BUY, instrument_id=AUD/USD.SIM"},
	}
	for _, testCase := range cases {
		if got := testCase.reason.String(); got != testCase.want {
			t.Fatalf("String() = %q, want %q", got, testCase.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied_reason.rs:447
//	test: renders_routing_messages
func TestOrderDeniedReasonRendersRoutingMessages(t *testing.T) {
	simClient := ids.MustClientID("SIM")
	ibClient := ids.MustClientID("IB")
	missing := OrderDeniedReason{
		Code:           DeniedNoExecutionClient,
		ClientID:       &simClient,
		RoutingContext: "venue=SIM",
	}
	mismatch := OrderDeniedReason{
		Code:        DeniedClientVenueMismatch,
		ClientID:    &ibClient,
		OrderVenue:  ids.MustVenue("XCME"),
		ClientVenue: ids.MustVenue("IB"),
	}
	submit := OrderDeniedReason{Code: DeniedSubmitFailed, Text: "transport closed"}
	invalidPosition := OrderDeniedReason{
		Code:       DeniedInvalidPositionID,
		PositionID: ids.MustPositionID("P-1"),
		Text:       "not valid for NETTING OMS",
	}

	cases := []struct {
		reason OrderDeniedReason
		want   string
	}{
		{missing, `NO_EXECUTION_CLIENT: client_id=Some("SIM"), routing_context=venue=SIM`},
		{mismatch, "CLIENT_VENUE_MISMATCH: client_id=IB, order_venue=XCME, client_venue=IB"},
		{submit, "SUBMIT_FAILED: transport closed"},
		{invalidPosition, "INVALID_POSITION_ID: position_id=P-1, detail=not valid for NETTING OMS"},
	}
	for _, testCase := range cases {
		if got := testCase.reason.String(); got != testCase.want {
			t.Fatalf("String() = %q, want %q", got, testCase.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied_reason.rs:481
//	test: renders_condition_led_message
func TestOrderDeniedReasonRendersConditionLedMessage(t *testing.T) {
	reason := OrderDeniedReason{
		Code:        DeniedUnsupportedTimeInForce,
		TimeInForce: TimeInForceGTD,
	}

	if got, want := reason.String(), "UNSUPPORTED_TIME_IN_FORCE: GTD"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied_reason.rs:487
//	test: renders_adapter_messages
func TestOrderDeniedReasonRendersAdapterMessages(t *testing.T) {
	cases := []struct {
		reason OrderDeniedReason
		want   string
	}{
		{
			OrderDeniedReason{Code: DeniedInvalidClientOrderID, Text: "clOrdId must be alphanumeric"},
			"INVALID_CLIENT_ORDER_ID: clOrdId must be alphanumeric",
		},
		{
			OrderDeniedReason{Code: DeniedUnsupportedOrderList, Text: "spread instruments are not supported in order lists"},
			"UNSUPPORTED_ORDER_LIST: spread instruments are not supported in order lists",
		},
		{
			OrderDeniedReason{Code: DeniedUnsupportedOrderType, OrderType: OrderTypeTrailingStopMarket},
			"UNSUPPORTED_ORDER_TYPE: TRAILING_STOP_MARKET",
		},
		{
			OrderDeniedReason{Code: DeniedUnsupportedTPSL, Text: "TP/SL trigger prices are not supported in demo mode"},
			"UNSUPPORTED_TP_SL: TP/SL trigger prices are not supported in demo mode",
		},
		{
			OrderDeniedReason{Code: DeniedValidationFailed, Text: "`bbo_side_type` and `bbo_level` are only supported for linear products"},
			"VALIDATION_FAILED: `bbo_side_type` and `bbo_level` are only supported for linear products",
		},
		{
			OrderDeniedReason{Code: DeniedStreamReconciling},
			"STREAM_RECONCILING: post-reconnect reconciliation in progress, retry once it completes",
		},
	}
	for _, testCase := range cases {
		if got := testCase.reason.String(); got != testCase.want {
			t.Fatalf("String() = %q, want %q", got, testCase.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied_reason.rs:534
//	test: message_prefix_matches_code
func TestOrderDeniedReasonMessagePrefixMatchesCode(t *testing.T) {
	usd := money.MustNew("100.00", currency.USD())
	instrumentID := ids.MustInstrumentID("AUD/USD.SIM")
	positionID := ids.MustPositionID("P-1")
	orderListID := ids.MustOrderListID("OL-1")
	simClient := ids.MustClientID("SIM")
	ibClient := ids.MustClientID("IB")
	samples := []OrderDeniedReason{
		{Code: DeniedQuantityExceedsMaximum, EffectiveQuantity: decimal.MustQuantity("15"), LimitQuantity: decimal.MustQuantity("10")},
		{Code: DeniedQuantityBelowMinimum, EffectiveQuantity: decimal.MustQuantity("1"), LimitQuantity: decimal.MustQuantity("5")},
		{Code: DeniedNotionalExceedsMaxPerOrder, FirstMoney: usd, SecondMoney: usd},
		{Code: DeniedNotionalExceedsMaximum, FirstMoney: usd, SecondMoney: usd},
		{Code: DeniedNotionalBelowMinimum, FirstMoney: usd, SecondMoney: usd},
		{Code: DeniedNotionalExceedsFreeBalance, FirstMoney: usd, SecondMoney: usd},
		{Code: DeniedCumNotionalExceedsFreeBalance, FirstMoney: usd, SecondMoney: usd},
		{Code: DeniedMarginExceedsFreeBalance, FirstMoney: usd, SecondMoney: usd},
		{Code: DeniedCumMarginExceedsFreeBalance, FirstMoney: usd, SecondMoney: usd},
		{Code: DeniedInvalidMaxNotionalPerOrder, InstrumentID: instrumentID, Value: decimal.MustParse("1")},
		{Code: DeniedInvalidOrderSide, OrderSide: OrderSideNoOrderSide},
		{Code: DeniedMissingExpireTime},
		{Code: DeniedExpireTimeInPast, Text: "1970-01-01T00:00:00Z"},
		{Code: DeniedMissingTriggerType},
		{Code: DeniedMissingTrailingOffset},
		{Code: DeniedMissingTrailingOffsetType},
		{Code: DeniedUnsupportedTrailingOffsetType, TrailingOffsetType: TrailingOffsetTypePrice},
		{Code: DeniedTrailingStopCalcFailed, Text: "boom"},
		{Code: DeniedQuantityConversionFailed, Text: "boom"},
		{Code: DeniedInstrumentNotFound, InstrumentID: instrumentID},
		{Code: DeniedPositionNotFound, PositionID: positionID},
		{Code: DeniedReduceOnlyWouldIncreasePosition, PositionID: positionID},
		{Code: DeniedOrderListIncomplete, OrderListID: orderListID},
		{Code: DeniedOrderListDenied, OrderListID: orderListID},
		{Code: DeniedTradingHalted},
		{Code: DeniedTradingStateReducing, OrderSide: OrderSideBuy, InstrumentID: instrumentID},
		{Code: DeniedRateLimitExceeded},
		{Code: DeniedNoExecutionClient, ClientID: &simClient, RoutingContext: "venue=SIM"},
		{Code: DeniedClientVenueMismatch, ClientID: &ibClient, OrderVenue: ids.MustVenue("XCME"), ClientVenue: ids.MustVenue("IB")},
		{Code: DeniedSubmitFailed, Text: "boom"},
		{Code: DeniedInvalidPositionID, PositionID: positionID, Text: "boom"},
		{Code: DeniedUnsupportedTimeInForce, TimeInForce: TimeInForceGTD},
		{Code: DeniedInvalidClientOrderID, Text: "boom"},
		{Code: DeniedUnsupportedOrderList, Text: "boom"},
		{Code: DeniedUnsupportedOrderType, OrderType: OrderTypeTrailingStopMarket},
		{Code: DeniedUnsupportedTPSL, Text: "boom"},
		{Code: DeniedValidationFailed, Text: "boom"},
		{Code: DeniedStreamReconciling},
	}

	if len(samples) != len(allOrderDeniedCodes) {
		t.Fatalf("sample count = %d, code count = %d", len(samples), len(allOrderDeniedCodes))
	}
	for _, reason := range samples {
		if !strings.HasPrefix(reason.String(), string(reason.Code)) {
			t.Fatalf("message %q does not start with code %q", reason, reason.Code)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied_reason.rs:662
//	test: generated_table_is_in_sync
func TestOrderDeniedReasonGeneratedTableIsInSync(t *testing.T) {
	document := readPinnedExecutionDocument(t)

	if !OrderDeniedReasonsDocumentInSync(document) {
		t.Fatal("the order-denied-reasons table in docs/concepts/execution.md is stale")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/denied_reason.rs:673
//	test: regenerate_order_denied_reasons_doc
func TestOrderDeniedReasonRegenerateOrderDeniedReasonsDoc(t *testing.T) {
	document := readPinnedExecutionDocument(t)

	updated, err := RegenerateOrderDeniedReasonsDocument(document)
	if err != nil {
		t.Fatalf("RegenerateOrderDeniedReasonsDocument() error = %v", err)
	}
	if updated != document || !OrderDeniedReasonsDocumentInSync(updated) {
		t.Fatal("pure regeneration changed an already-current document")
	}
}

func readPinnedExecutionDocument(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	path := filepath.Join(
		filepath.Dir(filename),
		"..",
		"..",
		".sources",
		"nautilus_trader",
		"docs",
		"concepts",
		"execution.md",
	)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}
