package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
	"github.com/upcomers-org/platformgo/internal/order"
)

func testExecutionMassStatus() ExecutionMassStatus {
	return NewExecutionMassStatus(
		ids.MustClientID("IB"),
		ids.MustAccountID("IB-DU123456"),
		ids.MustVenue("NASDAQ"),
		1_000_000_000,
		"",
	)
}

func massStatusOrderReport() OrderStatusReport {
	return NewOrderStatusReport(OrderStatusReportConfig{
		AccountID:      ids.MustAccountID("IB-DU123456"),
		InstrumentID:   ids.MustInstrumentID("AAPL.NASDAQ"),
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

func massStatusFillReport() FillReport {
	return NewFillReport(FillReportConfig{
		AccountID:     ids.MustAccountID("IB-DU123456"),
		InstrumentID:  ids.MustInstrumentID("AAPL.NASDAQ"),
		VenueOrderID:  ids.MustVenueOrderID("1"),
		TradeID:       ids.MustTradeID("T-001"),
		OrderSide:     order.OrderSideBuy,
		LastQuantity:  decimal.MustQuantity("50"),
		LastPrice:     decimal.MustPrice("150.00"),
		Commission:    money.MustNew("1", currency.USD()),
		LiquiditySide: order.LiquiditySideTaker,
		TsEvent:       1_500_000_000,
		TsInit:        2_500_000_000,
	})
}

func massStatusPositionReport() MassPositionStatusReport {
	positionID := ids.MustPositionID("P-001")
	return NewMassPositionStatusReport(
		ids.MustAccountID("IB-DU123456"),
		ids.MustInstrumentID("AAPL.NASDAQ"),
		order.PositionSideLong,
		decimal.MustQuantity("50"),
		2_000_000_000,
		3_000_000_000,
		&positionID,
		nil,
	)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:223
//	test: test_execution_mass_status_new
func TestExecutionMassStatusNew(t *testing.T) {
	status := testExecutionMassStatus()

	if status.ClientID != ids.MustClientID("IB") ||
		status.AccountID != ids.MustAccountID("IB-DU123456") ||
		status.Venue != ids.MustVenue("NASDAQ") ||
		status.TsInit != 1_000_000_000 {
		t.Fatalf("mass status identity = %#v", status)
	}
	if len(status.OrderReports()) != 0 ||
		len(status.FillReports()) != 0 ||
		len(status.PositionReports()) != 0 {
		t.Fatal("new mass status contains reports")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:236
//	test: test_execution_mass_status_with_generated_report_id
func TestExecutionMassStatusWithGeneratedReportID(t *testing.T) {
	status := NewExecutionMassStatus(
		ids.MustClientID("IB"),
		ids.MustAccountID("IB-DU123456"),
		ids.MustVenue("NASDAQ"),
		1_000_000_000,
		"",
	)

	if status.ReportID == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("generated report ID was the nil UUID")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:253
//	test: test_add_order_reports
func TestExecutionMassStatusAddOrderReports(t *testing.T) {
	status := testExecutionMassStatus()
	first := massStatusOrderReport()
	second := NewOrderStatusReport(OrderStatusReportConfig{
		AccountID:      ids.MustAccountID("IB-DU123456"),
		InstrumentID:   ids.MustInstrumentID("MSFT.NASDAQ"),
		VenueOrderID:   ids.MustVenueOrderID("2"),
		OrderSide:      order.OrderSideSell,
		OrderType:      order.OrderTypeMarket,
		TimeInForce:    order.TimeInForceIOC,
		OrderStatus:    order.OrderStatusFilled,
		Quantity:       decimal.MustQuantity("200"),
		FilledQuantity: decimal.MustQuantity("200"),
		TsAccepted:     1_000_000_000,
		TsLast:         2_000_000_000,
		TsInit:         3_000_000_000,
	})

	status.AddOrderReports([]OrderStatusReport{first, second})

	reports := status.OrderReports()
	if len(reports) != 2 ||
		!reports[ids.MustVenueOrderID("1")].Equal(first) ||
		!reports[ids.MustVenueOrderID("2")].Equal(second) {
		t.Fatalf("order reports = %#v", reports)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:288
//	test: test_add_fill_reports
func TestExecutionMassStatusAddFillReports(t *testing.T) {
	status := testExecutionMassStatus()
	first := massStatusFillReport()
	second := NewFillReport(FillReportConfig{
		AccountID:     ids.MustAccountID("IB-DU123456"),
		InstrumentID:  ids.MustInstrumentID("AAPL.NASDAQ"),
		VenueOrderID:  ids.MustVenueOrderID("1"),
		TradeID:       ids.MustTradeID("T-002"),
		OrderSide:     order.OrderSideBuy,
		LastQuantity:  decimal.MustQuantity("50"),
		LastPrice:     decimal.MustPrice("151.00"),
		Commission:    money.MustNew("1.5", currency.USD()),
		LiquiditySide: order.LiquiditySideMaker,
		TsEvent:       1_600_000_000,
		TsInit:        2_600_000_000,
	})

	status.AddFillReports([]FillReport{first, second})

	reports := status.FillReports()
	fills := reports[ids.MustVenueOrderID("1")]
	if len(reports) != 1 || len(fills) != 2 ||
		!fills[0].Equal(first) || !fills[1].Equal(second) {
		t.Fatalf("fill reports = %#v", reports)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:320
//	test: test_add_position_reports
func TestExecutionMassStatusAddPositionReports(t *testing.T) {
	status := testExecutionMassStatus()
	first := massStatusPositionReport()
	second := NewMassPositionStatusReport(
		ids.MustAccountID("IB-DU123456"),
		ids.MustInstrumentID("AAPL.NASDAQ"),
		order.PositionSideShort,
		decimal.MustQuantity("25"),
		2_100_000_000,
		3_100_000_000,
		nil,
		nil,
	)
	third := NewMassPositionStatusReport(
		ids.MustAccountID("IB-DU123456"),
		ids.MustInstrumentID("MSFT.NASDAQ"),
		order.PositionSideLong,
		decimal.MustQuantity("100"),
		2_200_000_000,
		3_200_000_000,
		nil,
		nil,
	)

	status.AddPositionReports([]MassPositionStatusReport{first, second, third})

	reports := status.PositionReports()
	aapl := reports[ids.MustInstrumentID("AAPL.NASDAQ")]
	msft := reports[ids.MustInstrumentID("MSFT.NASDAQ")]
	if len(reports) != 2 || len(aapl) != 2 || len(msft) != 1 ||
		!reflectMassPositionReportEqual(aapl[0], first) ||
		!reflectMassPositionReportEqual(aapl[1], second) ||
		!reflectMassPositionReportEqual(msft[0], third) {
		t.Fatalf("position reports = %#v", reports)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:372
//	test: test_add_multiple_fills_for_different_orders
func TestExecutionMassStatusAddMultipleFillsForDifferentOrders(t *testing.T) {
	status := testExecutionMassStatus()
	first := massStatusFillReport()
	second := NewFillReport(FillReportConfig{
		AccountID:     ids.MustAccountID("IB-DU123456"),
		InstrumentID:  ids.MustInstrumentID("MSFT.NASDAQ"),
		VenueOrderID:  ids.MustVenueOrderID("2"),
		TradeID:       ids.MustTradeID("T-003"),
		OrderSide:     order.OrderSideSell,
		LastQuantity:  decimal.MustQuantity("75"),
		LastPrice:     decimal.MustPrice("300.00"),
		Commission:    money.MustNew("2", currency.USD()),
		LiquiditySide: order.LiquiditySideTaker,
		TsEvent:       1_700_000_000,
		TsInit:        2_700_000_000,
	})

	status.AddFillReports([]FillReport{first, second})

	reports := status.FillReports()
	order1 := reports[ids.MustVenueOrderID("1")]
	order2 := reports[ids.MustVenueOrderID("2")]
	if len(reports) != 2 || len(order1) != 1 || len(order2) != 1 ||
		!order1[0].Equal(first) || !order2[0].Equal(second) {
		t.Fatalf("fill reports = %#v", reports)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:407
//	test: test_comprehensive_mass_status
func TestExecutionMassStatusComprehensiveMassStatus(t *testing.T) {
	status := testExecutionMassStatus()
	orderReport := massStatusOrderReport()
	fillReport := massStatusFillReport()
	positionReport := massStatusPositionReport()

	status.AddOrderReports([]OrderStatusReport{orderReport})
	status.AddFillReports([]FillReport{fillReport})
	status.AddPositionReports([]MassPositionStatusReport{positionReport})

	orders := status.OrderReports()
	fills := status.FillReports()
	positions := status.PositionReports()
	if len(orders) != 1 || len(fills) != 1 || len(positions) != 1 ||
		!orders[ids.MustVenueOrderID("1")].Equal(orderReport) ||
		!fills[ids.MustVenueOrderID("1")][0].Equal(fillReport) ||
		!reflectMassPositionReportEqual(
			positions[ids.MustInstrumentID("AAPL.NASDAQ")][0],
			positionReport,
		) {
		t.Fatal("comprehensive report content was not retained")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:446
//	test: test_display
func TestExecutionMassStatusDisplay(t *testing.T) {
	display := testExecutionMassStatus().String()

	for _, expected := range []string{"ExecutionMassStatus", "IB", "IB-DU123456", "NASDAQ"} {
		if !strings.Contains(display, expected) {
			t.Fatalf("String() = %q, missing %q", display, expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:457
//	test: test_clone_and_equality
func TestExecutionMassStatusCloneAndEquality(t *testing.T) {
	status1 := testExecutionMassStatus()
	status2 := status1.Clone()

	if !status1.Equal(status2) {
		t.Fatal("cloned mass status compared unequal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:465
//	test: test_serialization_roundtrip
func TestExecutionMassStatusSerializationRoundtrip(t *testing.T) {
	original := testExecutionMassStatus()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded ExecutionMassStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !original.Equal(decoded) {
		t.Fatalf("roundtrip changed mass status: %#v != %#v", original, decoded)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:475
//	test: test_empty_mass_status_accessors
func TestExecutionMassStatusEmptyMassStatusAccessors(t *testing.T) {
	status := testExecutionMassStatus()

	if len(status.OrderReports()) != 0 ||
		len(status.FillReports()) != 0 ||
		len(status.PositionReports()) != 0 {
		t.Fatal("empty mass status accessors returned reports")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:485
//	test: test_add_empty_reports
func TestExecutionMassStatusAddEmptyReports(t *testing.T) {
	status := testExecutionMassStatus()

	status.AddOrderReports([]OrderStatusReport{})
	status.AddFillReports([]FillReport{})
	status.AddPositionReports([]MassPositionStatusReport{})

	if len(status.OrderReports()) != 0 ||
		len(status.FillReports()) != 0 ||
		len(status.PositionReports()) != 0 {
		t.Fatal("adding empty reports changed mass status")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/mass_status.rs:500
//	test: test_overwrite_order_reports
func TestExecutionMassStatusOverwriteOrderReports(t *testing.T) {
	status := testExecutionMassStatus()
	venueOrderID := ids.MustVenueOrderID("1")
	first := massStatusOrderReport()
	status.AddOrderReports([]OrderStatusReport{first})
	second := NewOrderStatusReport(OrderStatusReportConfig{
		AccountID:      ids.MustAccountID("IB-DU123456"),
		InstrumentID:   ids.MustInstrumentID("AAPL.NASDAQ"),
		VenueOrderID:   venueOrderID,
		OrderSide:      order.OrderSideSell,
		OrderType:      order.OrderTypeMarket,
		TimeInForce:    order.TimeInForceIOC,
		OrderStatus:    order.OrderStatusFilled,
		Quantity:       decimal.MustQuantity("200"),
		FilledQuantity: decimal.MustQuantity("200"),
		TsAccepted:     1_000_000_000,
		TsLast:         2_000_000_000,
		TsInit:         3_000_000_000,
	})
	status.AddOrderReports([]OrderStatusReport{second})

	reports := status.OrderReports()
	if len(reports) != 1 || !reports[venueOrderID].Equal(second) ||
		reports[venueOrderID].Equal(first) {
		t.Fatalf("overwritten order reports = %#v", reports)
	}
}

func reflectMassPositionReportEqual(left, right MassPositionStatusReport) bool {
	return left.AccountID == right.AccountID &&
		left.InstrumentID == right.InstrumentID &&
		left.PositionSide == right.PositionSide &&
		left.Quantity.Equal(right.Quantity) &&
		left.SignedQuantity.Equal(right.SignedQuantity) &&
		left.ReportID == right.ReportID &&
		left.TsLast == right.TsLast &&
		left.TsInit == right.TsInit &&
		pointerPositionIDEqual(left.VenuePositionID, right.VenuePositionID) &&
		pointerDecimalEqual(left.AverageOpenPrice, right.AverageOpenPrice)
}

func pointerPositionIDEqual(left, right *ids.PositionID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func pointerDecimalEqual(left, right *decimal.Decimal) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
