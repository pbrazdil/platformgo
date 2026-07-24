package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/order"
)

func testOrderStatusReport() OrderStatusReport {
	clientOrderID := ids.MustClientOrderID("O-19700101-000000-001-001-1")
	return NewOrderStatusReport(OrderStatusReportConfig{
		AccountID:      ids.MustAccountID("SIM-001"),
		InstrumentID:   ids.MustInstrumentID("AUDUSD.SIM"),
		ClientOrderID:  &clientOrderID,
		VenueOrderID:   ids.MustVenueOrderID("1"),
		OrderSide:      order.OrderSideBuy,
		OrderType:      order.OrderTypeLimit,
		TimeInForce:    order.TimeInForceGTC,
		OrderStatus:    order.OrderStatusAccepted,
		Quantity:       decimal.MustQuantity("100"),
		FilledQuantity: decimal.MustQuantity("0"),
		TsAccepted:     1_000_000_000,
		TsLast:         2_000_000_000,
		TsInit:         3_000_000_000,
	})
}

func orderReportConfig(
	orderType order.OrderType,
	side order.OrderSide,
	timeInForce order.TimeInForce,
	status order.OrderStatus,
	quantity, filledQuantity, venueOrderID string,
) OrderStatusReportConfig {
	return OrderStatusReportConfig{
		AccountID:      ids.MustAccountID("SIM-001"),
		InstrumentID:   ids.MustInstrumentID("AUDUSD.SIM"),
		VenueOrderID:   ids.MustVenueOrderID(venueOrderID),
		OrderSide:      side,
		OrderType:      orderType,
		TimeInForce:    timeInForce,
		OrderStatus:    status,
		Quantity:       decimal.MustQuantity(quantity),
		FilledQuantity: decimal.MustQuantity(filledQuantity),
		TsAccepted:     1_000_000_000,
		TsLast:         2_000_000_000,
		TsInit:         3_000_000_000,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:472
//	test: test_order_status_report_new
func TestOrderStatusReportNew(t *testing.T) {
	report := testOrderStatusReport()

	if report.AccountID != ids.MustAccountID("SIM-001") ||
		report.InstrumentID != ids.MustInstrumentID("AUDUSD.SIM") ||
		report.ClientOrderID == nil ||
		*report.ClientOrderID != ids.MustClientOrderID("O-19700101-000000-001-001-1") ||
		report.VenueOrderID != ids.MustVenueOrderID("1") {
		t.Fatalf("report IDs = %#v", report)
	}
	if report.OrderSide != order.OrderSideBuy ||
		report.OrderType != order.OrderTypeLimit ||
		report.TimeInForce != order.TimeInForceGTC ||
		report.OrderStatus != order.OrderStatusAccepted {
		t.Fatalf("report enums = %#v", report)
	}
	if !report.Quantity.Equal(decimal.MustQuantity("100")) ||
		!report.FilledQuantity.Equal(decimal.MustQuantity("0")) {
		t.Fatalf("quantities = %s/%s", report.Quantity, report.FilledQuantity)
	}
	if report.TsAccepted != 1_000_000_000 ||
		report.TsLast != 2_000_000_000 ||
		report.TsInit != 3_000_000_000 {
		t.Fatalf("timestamps = %d/%d/%d", report.TsAccepted, report.TsLast, report.TsInit)
	}
	if report.OrderListID != nil || report.VenuePositionID != nil ||
		report.LinkedOrderIDs != nil || report.ParentOrderID != nil ||
		report.ContingencyType != order.ContingencyTypeNoContingency ||
		report.ExpireTime != nil || report.Price != nil ||
		report.TriggerPrice != nil || report.TriggerType != nil ||
		report.LimitOffset != nil || report.TrailingOffset != nil ||
		report.TrailingOffsetType != TrailingOffsetTypeNone ||
		report.AveragePrice != nil || report.DisplayQuantity != nil ||
		report.PostOnly || report.ReduceOnly || report.CancelReason != nil ||
		report.TsTriggered != nil {
		t.Fatalf("non-default optional fields = %#v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:514
//	test: test_order_status_report_with_generated_report_id
func TestOrderStatusReportWithGeneratedReportID(t *testing.T) {
	report := NewOrderStatusReport(orderReportConfig(
		order.OrderTypeMarket,
		order.OrderSideBuy,
		order.TimeInForceIOC,
		order.OrderStatusFilled,
		"100",
		"100",
		"1",
	))

	if report.ReportID == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("generated report ID was the nil UUID")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:541
//	test: test_order_status_report_builder_methods
func TestOrderStatusReportBuilderMethods(t *testing.T) {
	report := testOrderStatusReport().
		WithClientOrderID(ids.MustClientOrderID("O-19700101-000000-001-001-2")).
		WithOrderListID(ids.MustOrderListID("OL-001")).
		WithVenuePositionID(ids.MustPositionID("P-001")).
		WithParentOrderID(ids.MustClientOrderID("O-PARENT")).
		WithPrice(decimal.MustPrice("1.00000")).
		WithAveragePrice(decimal.MustParse("1.00001")).
		WithTriggerPrice(decimal.MustPrice("0.99000")).
		WithTriggerType(order.TriggerTypeDefault).
		WithLimitOffset(decimal.MustParse("0.0001")).
		WithTrailingOffset(decimal.MustParse("0.0002")).
		WithTrailingOffsetType(TrailingOffsetTypeBasisPoints).
		WithDisplayQuantity(decimal.MustQuantity("50")).
		WithExpireTime(4_000_000_000).
		WithPostOnly(true).
		WithReduceOnly(true).
		WithCancelReason("User requested").
		WithTsTriggered(1_500_000_000).
		WithContingencyType(order.ContingencyTypeOCO)

	if report.ClientOrderID == nil ||
		*report.ClientOrderID != ids.MustClientOrderID("O-19700101-000000-001-001-2") ||
		report.OrderListID == nil || *report.OrderListID != ids.MustOrderListID("OL-001") ||
		report.VenuePositionID == nil || *report.VenuePositionID != ids.MustPositionID("P-001") ||
		report.ParentOrderID == nil || *report.ParentOrderID != ids.MustClientOrderID("O-PARENT") {
		t.Fatalf("builder IDs = %#v", report)
	}
	if report.Price == nil || !report.Price.Equal(decimal.MustPrice("1.00000")) ||
		report.AveragePrice == nil || !report.AveragePrice.Equal(decimal.MustParse("1.00001")) ||
		report.TriggerPrice == nil || !report.TriggerPrice.Equal(decimal.MustPrice("0.99000")) ||
		report.TriggerType == nil || *report.TriggerType != order.TriggerTypeDefault {
		t.Fatalf("builder prices = %#v", report)
	}
	if report.LimitOffset == nil || !report.LimitOffset.Equal(decimal.MustParse("0.0001")) ||
		report.TrailingOffset == nil || !report.TrailingOffset.Equal(decimal.MustParse("0.0002")) ||
		report.TrailingOffsetType != TrailingOffsetTypeBasisPoints ||
		report.DisplayQuantity == nil ||
		!report.DisplayQuantity.Equal(decimal.MustQuantity("50")) {
		t.Fatalf("builder offsets/quantity = %#v", report)
	}
	if report.ExpireTime == nil || *report.ExpireTime != 4_000_000_000 ||
		!report.PostOnly || !report.ReduceOnly ||
		report.CancelReason == nil || *report.CancelReason != "User requested" ||
		report.TsTriggered == nil || *report.TsTriggered != 1_500_000_000 ||
		report.ContingencyType != order.ContingencyTypeOCO {
		t.Fatalf("builder flags/times = %#v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:590
//	test: test_display
func TestOrderStatusReportDisplay(t *testing.T) {
	display := testOrderStatusReport().String()

	for _, expected := range []string{
		"OrderStatusReport", "SIM-001", "AUDUSD.SIM", "BUY",
		"LIMIT", "GTC", "ACCEPTED", "100",
	} {
		if !strings.Contains(display, expected) {
			t.Fatalf("String() = %q, missing %q", display, expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:605
//	test: test_clone_and_equality
func TestOrderStatusReportCloneAndEquality(t *testing.T) {
	report1 := testOrderStatusReport()
	report2 := report1.Clone()

	if !report1.Equal(report2) {
		t.Fatal("cloned report compared unequal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:613
//	test: test_serialization_roundtrip
func TestOrderStatusReportSerializationRoundtrip(t *testing.T) {
	original := testOrderStatusReport()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded OrderStatusReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !original.Equal(decoded) {
		t.Fatalf("roundtrip changed report: %#v != %#v", original, decoded)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:623
//	test: test_order_status_report_different_order_types
func TestOrderStatusReportDifferentOrderTypes(t *testing.T) {
	market := NewOrderStatusReport(orderReportConfig(
		order.OrderTypeMarket, order.OrderSideBuy, order.TimeInForceIOC,
		order.OrderStatusFilled, "100", "100", "1",
	))
	stop := NewOrderStatusReport(orderReportConfig(
		order.OrderTypeStopMarket, order.OrderSideSell, order.TimeInForceGTC,
		order.OrderStatusAccepted, "50", "0", "2",
	))

	if market.OrderType != order.OrderTypeMarket || stop.OrderType != order.OrderTypeStopMarket {
		t.Fatalf("order types = %s/%s", market.OrderType, stop.OrderType)
	}
	if market.Equal(stop) {
		t.Fatal("different order reports compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:664
//	test: test_order_status_report_different_statuses
func TestOrderStatusReportDifferentStatuses(t *testing.T) {
	accepted := testOrderStatusReport()
	config := orderReportConfig(
		order.OrderTypeLimit, order.OrderSideBuy, order.TimeInForceGTC,
		order.OrderStatusFilled, "100", "100", "1",
	)
	clientOrderID := ids.MustClientOrderID("O-19700101-000000-001-001-1")
	config.ClientOrderID = &clientOrderID
	filled := NewOrderStatusReport(config)

	if accepted.OrderStatus != order.OrderStatusAccepted ||
		filled.OrderStatus != order.OrderStatusFilled {
		t.Fatalf("statuses = %s/%s", accepted.OrderStatus, filled.OrderStatus)
	}
	if accepted.Equal(filled) {
		t.Fatal("different status reports compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:691
//	test: test_order_status_report_with_optional_fields
func TestOrderStatusReportWithOptionalFields(t *testing.T) {
	report := testOrderStatusReport()
	if report.Price != nil || report.AveragePrice != nil || report.PostOnly || report.ReduceOnly {
		t.Fatalf("initial optional fields = %#v", report)
	}

	report = report.
		WithPrice(decimal.MustPrice("1.00000")).
		WithAveragePrice(decimal.MustParse("1.00001")).
		WithPostOnly(true).
		WithReduceOnly(true)

	if report.Price == nil || !report.Price.Equal(decimal.MustPrice("1.00000")) ||
		report.AveragePrice == nil || !report.AveragePrice.Equal(decimal.MustParse("1.00001")) ||
		!report.PostOnly || !report.ReduceOnly {
		t.Fatalf("optional fields = %#v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:715
//	test: test_order_status_report_partial_fill
func TestOrderStatusReportPartialFill(t *testing.T) {
	config := orderReportConfig(
		order.OrderTypeLimit, order.OrderSideBuy, order.TimeInForceGTC,
		order.OrderStatusPartiallyFilled, "100", "30", "1",
	)
	clientOrderID := ids.MustClientOrderID("O-19700101-000000-001-001-1")
	config.ClientOrderID = &clientOrderID
	report := NewOrderStatusReport(config)

	if !report.Quantity.Equal(decimal.MustQuantity("100")) ||
		!report.FilledQuantity.Equal(decimal.MustQuantity("30")) ||
		report.OrderStatus != order.OrderStatusPartiallyFilled {
		t.Fatalf("partial fill = %#v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:742
//	test: test_order_status_report_with_all_timestamp_fields
func TestOrderStatusReportWithAllTimestampFields(t *testing.T) {
	report := NewOrderStatusReport(orderReportConfig(
		order.OrderTypeStopLimit, order.OrderSideBuy, order.TimeInForceGTC,
		order.OrderStatusTriggered, "100", "0", "1",
	)).WithTsTriggered(1_500_000_000)

	if report.TsAccepted != 1_000_000_000 ||
		report.TsLast != 2_000_000_000 ||
		report.TsInit != 3_000_000_000 ||
		report.TsTriggered == nil || *report.TsTriggered != 1_500_000_000 {
		t.Fatalf("timestamps = %#v", report)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:768
//	test: test_is_order_updated_returns_true_when_price_differs
func TestOrderStatusReportIsOrderUpdatedReturnsTrueWhenPriceDiffers(t *testing.T) {
	orderPrice := decimal.MustPrice("1.00000")
	snapshot := OrderSnapshot{
		Quantity: decimal.MustQuantity("100"),
		Price:    &orderPrice,
	}
	report := NewOrderStatusReport(orderReportConfig(
		order.OrderTypeLimit, order.OrderSideBuy, order.TimeInForceGTC,
		order.OrderStatusAccepted, "100", "0", "1",
	)).WithPrice(decimal.MustPrice("1.00100"))

	if !report.IsOrderUpdated(snapshot) {
		t.Fatal("different limit price was not detected")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:797
//	test: test_is_order_updated_returns_true_when_trigger_price_differs
func TestOrderStatusReportIsOrderUpdatedReturnsTrueWhenTriggerPriceDiffers(t *testing.T) {
	orderTrigger := decimal.MustPrice("0.99000")
	snapshot := OrderSnapshot{
		Quantity:     decimal.MustQuantity("100"),
		TriggerPrice: &orderTrigger,
	}
	report := NewOrderStatusReport(orderReportConfig(
		order.OrderTypeStopMarket, order.OrderSideBuy, order.TimeInForceGTC,
		order.OrderStatusAccepted, "100", "0", "1",
	)).WithTriggerPrice(decimal.MustPrice("0.99100"))

	if !report.IsOrderUpdated(snapshot) {
		t.Fatal("different trigger price was not detected")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:826
//	test: test_is_order_updated_returns_true_when_quantity_differs
func TestOrderStatusReportIsOrderUpdatedReturnsTrueWhenQuantityDiffers(t *testing.T) {
	orderPrice := decimal.MustPrice("1.00000")
	snapshot := OrderSnapshot{
		Quantity: decimal.MustQuantity("100"),
		Price:    &orderPrice,
	}
	report := NewOrderStatusReport(orderReportConfig(
		order.OrderTypeLimit, order.OrderSideBuy, order.TimeInForceGTC,
		order.OrderStatusAccepted, "200", "0", "1",
	)).WithPrice(decimal.MustPrice("1.00000"))

	if !report.IsOrderUpdated(snapshot) {
		t.Fatal("different quantity was not detected")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:855
//	test: test_is_order_updated_returns_false_when_all_match
func TestOrderStatusReportIsOrderUpdatedReturnsFalseWhenAllMatch(t *testing.T) {
	orderPrice := decimal.MustPrice("1.00000")
	snapshot := OrderSnapshot{
		Quantity: decimal.MustQuantity("100"),
		Price:    &orderPrice,
	}
	report := NewOrderStatusReport(orderReportConfig(
		order.OrderTypeLimit, order.OrderSideBuy, order.TimeInForceGTC,
		order.OrderStatusAccepted, "100", "0", "1",
	)).WithPrice(decimal.MustPrice("1.00000"))

	if report.IsOrderUpdated(snapshot) {
		t.Fatal("matching order was reported updated")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:884
//	test: test_is_order_updated_returns_false_when_order_has_no_price
func TestOrderStatusReportIsOrderUpdatedReturnsFalseWhenOrderHasNoPrice(t *testing.T) {
	snapshot := OrderSnapshot{Quantity: decimal.MustQuantity("100")}
	report := NewOrderStatusReport(orderReportConfig(
		order.OrderTypeMarket, order.OrderSideBuy, order.TimeInForceIOC,
		order.OrderStatusAccepted, "100", "0", "1",
	)).WithPrice(decimal.MustPrice("1.00000"))

	if report.IsOrderUpdated(snapshot) {
		t.Fatal("report price updated a market order without a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/order.rs:913
//	test: test_is_order_updated_stop_limit_order_with_both_prices
func TestOrderStatusReportIsOrderUpdatedStopLimitOrderWithBothPrices(t *testing.T) {
	orderPrice := decimal.MustPrice("1.00000")
	orderTrigger := decimal.MustPrice("0.99000")
	snapshot := OrderSnapshot{
		Quantity:     decimal.MustQuantity("100"),
		Price:        &orderPrice,
		TriggerPrice: &orderTrigger,
	}
	config := orderReportConfig(
		order.OrderTypeStopLimit, order.OrderSideBuy, order.TimeInForceGTC,
		order.OrderStatusAccepted, "100", "0", "1",
	)
	same := NewOrderStatusReport(config).
		WithPrice(decimal.MustPrice("1.00000")).
		WithTriggerPrice(decimal.MustPrice("0.99000"))
	if same.IsOrderUpdated(snapshot) {
		t.Fatal("matching stop-limit order was reported updated")
	}

	differentPrice := NewOrderStatusReport(config).
		WithPrice(decimal.MustPrice("1.00100")).
		WithTriggerPrice(decimal.MustPrice("0.99000"))
	if !differentPrice.IsOrderUpdated(snapshot) {
		t.Fatal("different stop-limit price was not detected")
	}

	differentTrigger := NewOrderStatusReport(config).
		WithPrice(decimal.MustPrice("1.00000")).
		WithTriggerPrice(decimal.MustPrice("0.99100"))
	if !differentTrigger.IsOrderUpdated(snapshot) {
		t.Fatal("different stop-limit trigger was not detected")
	}
}
