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

func fillReportConfig(
	side order.OrderSide,
	liquidity order.LiquiditySide,
	venueOrderID, tradeID, commission string,
) FillReportConfig {
	return FillReportConfig{
		AccountID:     ids.MustAccountID("SIM-001"),
		InstrumentID:  ids.MustInstrumentID("AUDUSD.SIM"),
		VenueOrderID:  ids.MustVenueOrderID(venueOrderID),
		TradeID:       ids.MustTradeID(tradeID),
		OrderSide:     side,
		LastQuantity:  decimal.MustQuantity("100"),
		LastPrice:     decimal.MustPrice("0.80000"),
		Commission:    money.MustNew(commission, currency.USD()),
		LiquiditySide: liquidity,
		TsEvent:       1_000_000_000,
		TsInit:        2_000_000_000,
	}
}

func testFillReport() FillReport {
	config := fillReportConfig(
		order.OrderSideBuy,
		order.LiquiditySideTaker,
		"1",
		"1",
		"5",
	)
	clientOrderID := ids.MustClientOrderID("O-19700101-000000-001-001-1")
	positionID := ids.MustPositionID("P-001")
	config.ClientOrderID = &clientOrderID
	config.VenuePositionID = &positionID
	return NewFillReport(config)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/fill.rs:173
//	test: test_fill_report_new
func TestFillReportNew(t *testing.T) {
	report := testFillReport()

	if report.AccountID != ids.MustAccountID("SIM-001") ||
		report.InstrumentID != ids.MustInstrumentID("AUDUSD.SIM") ||
		report.VenueOrderID != ids.MustVenueOrderID("1") ||
		report.TradeID != ids.MustTradeID("1") {
		t.Fatalf("report IDs = %#v", report)
	}
	if report.OrderSide != order.OrderSideBuy ||
		!report.LastQuantity.Equal(decimal.MustQuantity("100")) ||
		!report.LastPrice.Equal(decimal.MustPrice("0.80000")) ||
		!report.Commission.Equal(money.MustNew("5", currency.USD())) ||
		report.LiquiditySide != order.LiquiditySideTaker {
		t.Fatalf("report economics = %#v", report)
	}
	if report.ClientOrderID == nil ||
		*report.ClientOrderID != ids.MustClientOrderID("O-19700101-000000-001-001-1") ||
		report.VenuePositionID == nil ||
		*report.VenuePositionID != ids.MustPositionID("P-001") {
		t.Fatalf("optional IDs = %#v", report)
	}
	if report.TsEvent != 1_000_000_000 || report.TsInit != 2_000_000_000 {
		t.Fatalf("timestamps = %d/%d", report.TsEvent, report.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/fill.rs:195
//	test: test_fill_report_new_with_generated_report_id
func TestFillReportNewWithGeneratedReportID(t *testing.T) {
	report := NewFillReport(fillReportConfig(
		order.OrderSideBuy,
		order.LiquiditySideTaker,
		"1",
		"1",
		"5",
	))

	if report.ReportID == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("generated report ID was the nil UUID")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/fill.rs:221
//	test: test_has_client_order_id
func TestFillReportHasClientOrderID(t *testing.T) {
	report := testFillReport()
	if !report.HasClientOrderID() {
		t.Fatal("client order ID was not detected")
	}

	report.ClientOrderID = nil
	if report.HasClientOrderID() {
		t.Fatal("absent client order ID was detected")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/fill.rs:230
//	test: test_has_venue_position_id
func TestFillReportHasVenuePositionID(t *testing.T) {
	report := testFillReport()
	if !report.HasVenuePositionID() {
		t.Fatal("venue position ID was not detected")
	}

	report.VenuePositionID = nil
	if report.HasVenuePositionID() {
		t.Fatal("absent venue position ID was detected")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/fill.rs:239
//	test: test_display
func TestFillReportDisplay(t *testing.T) {
	display := testFillReport().String()

	for _, expected := range []string{
		"FillReport", "AUDUSD.SIM", "BUY", "100", "0.80000", "5.00 USD", "TAKER",
	} {
		if !strings.Contains(display, expected) {
			t.Fatalf("String() = %q, missing %q", display, expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/fill.rs:253
//	test: test_clone_and_equality
func TestFillReportCloneAndEquality(t *testing.T) {
	report1 := testFillReport()
	report2 := report1.Clone()

	if !report1.Equal(report2) {
		t.Fatal("cloned fill report compared unequal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/fill.rs:261
//	test: test_serialization_roundtrip
func TestFillReportSerializationRoundtrip(t *testing.T) {
	original := testFillReport()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded FillReport
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
//	source: crates/model/src/reports/fill.rs:271
//	test: test_fill_report_with_different_liquidity_sides
func TestFillReportWithDifferentLiquiditySides(t *testing.T) {
	maker := NewFillReport(fillReportConfig(
		order.OrderSideBuy,
		order.LiquiditySideMaker,
		"1",
		"1",
		"2",
	))
	taker := NewFillReport(fillReportConfig(
		order.OrderSideSell,
		order.LiquiditySideTaker,
		"2",
		"2",
		"5",
	))

	if maker.LiquiditySide != order.LiquiditySideMaker ||
		taker.LiquiditySide != order.LiquiditySideTaker {
		t.Fatalf("liquidity sides = %s/%s", maker.LiquiditySide, taker.LiquiditySide)
	}
	if maker.Equal(taker) {
		t.Fatal("different liquidity reports compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/fill.rs:312
//	test: test_fill_report_with_different_order_sides
func TestFillReportWithDifferentOrderSides(t *testing.T) {
	buy := NewFillReport(fillReportConfig(
		order.OrderSideBuy,
		order.LiquiditySideTaker,
		"1",
		"1",
		"5",
	))
	sell := NewFillReport(fillReportConfig(
		order.OrderSideSell,
		order.LiquiditySideTaker,
		"1",
		"1",
		"5",
	))

	if buy.OrderSide != order.OrderSideBuy || sell.OrderSide != order.OrderSideSell {
		t.Fatalf("order sides = %s/%s", buy.OrderSide, sell.OrderSide)
	}
	if buy.Equal(sell) {
		t.Fatal("different-side fill reports compared equal")
	}
}
