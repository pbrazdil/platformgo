package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2796
//	test: test_to_order_status_report_for_accepted_limit_order
func TestToOrderStatusReportForAcceptedLimitOrder(t *testing.T) {
	price := decimal.MustPrice("1.00000")
	order := acceptedOrder(t, Config{
		InstrumentID: ids.MustInstrumentID("AUDUSD.SIM"),
		Side:         OrderSideBuy, Type: OrderTypeLimit,
		Quantity: decimal.MustQuantity("100000"), Price: &price,
	})
	report := order.ToStatusReport("")
	if report == nil {
		t.Fatal("accepted order did not produce report")
	}
	if report.AccountID != testAccount || report.InstrumentID.String() != "AUDUSD.SIM" ||
		report.VenueOrderID != testVenue || report.OrderSide != OrderSideBuy ||
		report.OrderType != OrderTypeLimit || report.TimeInForce != TimeInForceGTC ||
		report.OrderStatus != OrderStatusAccepted || report.ClientOrderID == nil ||
		*report.ClientOrderID != order.config.ClientOrderID {
		t.Fatalf("identity/status fields = %+v", report)
	}
	requireQuantity(t, report.Quantity, "100000")
	requireQuantity(t, report.FilledQuantity, "0")
	if report.Price == nil || report.Price.String() != "1.00000" || report.AveragePrice != nil ||
		report.TimestampAccepted != 2 || report.TimestampLast != 2 ||
		report.TimestampInit != order.config.TimestampInit {
		t.Fatalf("price/timestamp fields = %+v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2825
//	test: test_to_order_status_report_for_filled_market_order
func TestToOrderStatusReportForFilledMarketOrder(t *testing.T) {
	order := acceptedOrder(t, Config{InstrumentID: ids.MustInstrumentID("AUDUSD.SIM")})
	positionID := ids.MustPositionID("1")
	fill := testFill(testTrade1, "100000", "1", 3)
	fill.VenuePositionID = &positionID
	if err := order.Fill(fill); err != nil {
		t.Fatal(err)
	}
	report := order.ToStatusReport("R-1")
	if report == nil || report.ReportID != "R-1" || report.OrderStatus != OrderStatusFilled ||
		report.Price != nil || report.AveragePrice == nil || report.AveragePrice.String() != "1" ||
		report.VenuePositionID == nil || *report.VenuePositionID != positionID {
		t.Fatalf("filled report = %+v", report)
	}
	requireQuantity(t, report.FilledQuantity, "100000")
	requireQuantity(t, report.Quantity, "100000")
	if report.TimestampAccepted != 2 || report.TimestampLast != 3 {
		t.Fatalf("filled report timestamps = %+v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2849
//	test: test_to_order_status_report_maps_optional_fields
func TestToOrderStatusReportMapsOptionalFields(t *testing.T) {
	price := decimal.MustPrice("0.99500")
	triggerPrice := decimal.MustPrice("1.00000")
	expireTime := uint64(5_000_000_000)
	display := decimal.MustQuantity("10000")
	orderList := ids.MustOrderListID("OL-001")
	child := ids.MustClientOrderID("O-CHILD")
	parent := ids.MustClientOrderID("O-PARENT")
	order := acceptedOrder(t, Config{
		InstrumentID: ids.MustInstrumentID("AUDUSD.SIM"),
		Side:         OrderSideSell, Type: OrderTypeStopLimit,
		Quantity: decimal.MustQuantity("100000"), Price: &price,
		TriggerPrice: &triggerPrice, TriggerType: TriggerTypeLastPrice,
		TimeInForce: TimeInForceGTD, ExpireTime: &expireTime,
		DisplayQuantity: &display, PostOnly: true, ReduceOnly: true,
		ContingencyType: ContingencyTypeOTO, OrderListID: &orderList,
		LinkedOrderIDs: []ids.ClientOrderID{child}, ParentOrderID: &parent,
	})
	report := order.ToStatusReport("")
	if report == nil || report.Price == nil || report.Price.String() != "0.99500" ||
		report.TriggerPrice == nil || report.TriggerPrice.String() != "1.00000" ||
		report.TriggerType == nil || *report.TriggerType != TriggerTypeLastPrice ||
		report.ExpireTime == nil || *report.ExpireTime != expireTime ||
		report.DisplayQuantity == nil || report.DisplayQuantity.Cmp(display) != 0 ||
		!report.PostOnly || !report.ReduceOnly ||
		report.ContingencyType != ContingencyTypeOTO ||
		report.OrderListID == nil || *report.OrderListID != orderList ||
		len(report.LinkedOrderIDs) != 1 || report.LinkedOrderIDs[0] != child ||
		report.ParentOrderID == nil || *report.ParentOrderID != parent {
		t.Fatalf("optional fields = %+v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2891
//	test: test_to_order_status_report_maps_ts_triggered
func TestToOrderStatusReportMapsTimestampTriggered(t *testing.T) {
	order := acceptedOrder(t, Config{Type: OrderTypeStopLimit})
	if err := order.Trigger(1_500_000_000); err != nil {
		t.Fatal(err)
	}
	report := order.ToStatusReport("")
	if report == nil || report.OrderStatus != OrderStatusTriggered ||
		report.TimestampTriggered == nil || *report.TimestampTriggered != 1_500_000_000 {
		t.Fatalf("trigger report = %+v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2913
//	test: test_to_order_status_report_maps_rejection_reason
func TestToOrderStatusReportMapsRejectionReason(t *testing.T) {
	order := acceptedOrder(t, Config{Type: OrderTypeLimit})
	if err := order.Reject("INSUFFICIENT_MARGIN", 3); err != nil {
		t.Fatal(err)
	}
	report := order.ToStatusReport("")
	if report == nil || report.OrderStatus != OrderStatusRejected ||
		report.CancelReason != "INSUFFICIENT_MARGIN" {
		t.Fatalf("rejection report = %+v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2936
//	test: test_to_order_status_report_maps_distinct_timestamps
func TestToOrderStatusReportMapsDistinctTimestamps(t *testing.T) {
	order := testOrder(t, Config{Type: OrderTypeLimit, TimestampInit: 1_000})
	if err := order.Submit(testAccount, 2_000); err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 3_000); err != nil {
		t.Fatal(err)
	}
	if err := order.Fill(testFill(testTrade1, "50000", "1", 4_000)); err != nil {
		t.Fatal(err)
	}
	report := order.ToStatusReport("")
	if report == nil || report.OrderStatus != OrderStatusPartiallyFilled ||
		report.TimestampAccepted != 3_000 || report.TimestampLast != 4_000 ||
		report.TimestampInit != 1_000 {
		t.Fatalf("timestamp report = %+v", report)
	}
	requireQuantity(t, report.FilledQuantity, "50000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2968
//	test: test_to_order_status_report_ts_accepted_falls_back_to_ts_last
func TestToOrderStatusReportTimestampAcceptedFallsBackToLast(t *testing.T) {
	order := testOrder(t, Config{Type: OrderTypeLimit})
	if err := order.Submit(testAccount, 2_000); err != nil {
		t.Fatal(err)
	}
	if err := order.Update(Update{VenueOrderID: &testVenue, Timestamp: 3_000}); err != nil {
		t.Fatal(err)
	}
	report := order.ToStatusReport("")
	if report == nil || report.OrderStatus != OrderStatusSubmitted ||
		report.VenueOrderID != testVenue || report.TimestampAccepted != 3_000 ||
		report.TimestampLast != 3_000 {
		t.Fatalf("fallback timestamp report = %+v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2995
//	test: test_to_order_status_report_maps_trailing_offsets
func TestToOrderStatusReportMapsTrailingOffsets(t *testing.T) {
	price := decimal.MustPrice("0.99500")
	activation := decimal.MustPrice("1.00500")
	trigger := decimal.MustPrice("1.00000")
	limitOffset := decimal.MustParse("0.0001")
	trailingOffset := decimal.MustParse("0.0002")
	order := acceptedOrder(t, Config{
		Type: OrderTypeTrailingStopLimit, Side: OrderSideSell,
		Price: &price, ActivationPrice: &activation, TriggerPrice: &trigger,
		TriggerType: TriggerTypeLastPrice, LimitOffset: &limitOffset,
		TrailingOffset: &trailingOffset, TrailingOffsetType: TrailingOffsetTypePrice,
	})
	report := order.ToStatusReport("")
	if report == nil || report.ActivationPrice == nil || report.ActivationPrice.String() != "1.00500" ||
		report.LimitOffset == nil || report.LimitOffset.String() != "0.0001" ||
		report.TrailingOffset == nil || report.TrailingOffset.String() != "0.0002" ||
		report.TrailingOffsetType != TrailingOffsetTypePrice {
		t.Fatalf("trailing fields = %+v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:3019
//	test: test_to_order_status_report_returns_none_before_venue_ack
func TestToOrderStatusReportReturnsNilBeforeVenueAcknowledgement(t *testing.T) {
	if report := testOrder(t, Config{Type: OrderTypeLimit}).ToStatusReport(""); report != nil {
		t.Fatalf("report before venue acknowledgement = %+v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:3031
//	test: test_to_order_status_report_returns_none_for_submitted_order
func TestToOrderStatusReportReturnsNilForSubmittedOrder(t *testing.T) {
	order := testOrder(t, Config{Type: OrderTypeLimit})
	if err := order.Submit(testAccount, 1); err != nil {
		t.Fatal(err)
	}
	if order.AccountID() == nil || order.ToStatusReport("") != nil {
		t.Fatal("submitted inflight order should have account but no report")
	}
}
