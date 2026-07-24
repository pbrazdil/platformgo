package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func positionReportLong() PositionStatusReport {
	venuePositionID := ids.MustPositionID("P-001")
	return NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideLong,
		decimal.MustQuantity("100"),
		1_000_000_000,
		2_000_000_000,
		"",
		&venuePositionID,
		nil,
	)
}

func positionReportShort() PositionStatusReport {
	return NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideShort,
		decimal.MustQuantity("50"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		nil,
	)
}

func positionReportFlat() PositionStatusReport {
	return NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideFlat,
		decimal.MustQuantity("0"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		nil,
	)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:198
//	test: test_position_status_report_new_long
func TestPositionStatusReportNewLong(t *testing.T) {
	report := positionReportLong()
	if report.AccountID != ids.MustAccountID("SIM-001") {
		t.Fatalf("account ID = %s", report.AccountID)
	}
	if report.InstrumentID != ids.MustInstrumentID("AUDUSD.SIM") {
		t.Fatalf("instrument ID = %s", report.InstrumentID)
	}
	if report.PositionSide != PositionSideLong {
		t.Fatalf("side = %s", report.PositionSide)
	}
	if !report.Quantity.Equal(decimal.MustQuantity("100")) {
		t.Fatalf("quantity = %s", report.Quantity)
	}
	if !report.SignedDecimalQty.Equal(decimal.MustParse("100")) {
		t.Fatalf("signed quantity = %s", report.SignedDecimalQty)
	}
	if report.VenuePositionID == nil || *report.VenuePositionID != ids.MustPositionID("P-001") {
		t.Fatalf("venue position ID = %v", report.VenuePositionID)
	}
	if report.TsLast != 1_000_000_000 || report.TsInit != 2_000_000_000 {
		t.Fatalf("timestamps = %d/%d", report.TsLast, report.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:212
//	test: test_position_status_report_new_short
func TestPositionStatusReportNewShort(t *testing.T) {
	report := positionReportShort()
	if report.PositionSide != PositionSideShort {
		t.Fatalf("side = %s", report.PositionSide)
	}
	if !report.Quantity.Equal(decimal.MustQuantity("50")) {
		t.Fatalf("quantity = %s", report.Quantity)
	}
	if !report.SignedDecimalQty.Equal(decimal.MustParse("-50")) {
		t.Fatalf("signed quantity = %s", report.SignedDecimalQty)
	}
	if report.VenuePositionID != nil {
		t.Fatalf("venue position ID = %v", report.VenuePositionID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:222
//	test: test_position_status_report_new_flat
func TestPositionStatusReportNewFlat(t *testing.T) {
	report := positionReportFlat()
	if report.PositionSide != PositionSideFlat {
		t.Fatalf("side = %s", report.PositionSide)
	}
	if !report.Quantity.Equal(decimal.MustQuantity("0")) {
		t.Fatalf("quantity = %s", report.Quantity)
	}
	if !report.SignedDecimalQty.IsZero() {
		t.Fatalf("signed quantity = %s", report.SignedDecimalQty)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:231
//	test: test_position_status_report_with_generated_report_id
func TestPositionStatusReportWithGeneratedReportID(t *testing.T) {
	report := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideLong,
		decimal.MustQuantity("100"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		nil,
	)
	if report.ReportID == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("generated report ID was the nil UUID")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:252
//	test: test_has_venue_position_id
func TestPositionStatusReportHasVenuePositionID(t *testing.T) {
	report := positionReportLong()
	if !report.HasVenuePositionID() {
		t.Fatal("expected venue position ID")
	}
	report.VenuePositionID = nil
	if report.HasVenuePositionID() {
		t.Fatal("unexpected venue position ID")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:261
//	test: test_is_flat
func TestPositionStatusReportIsFlat(t *testing.T) {
	longReport := positionReportLong()
	shortReport := positionReportShort()
	flatReport := positionReportFlat()
	noPositionReport := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideFlat,
		decimal.MustQuantity("0"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		nil,
	)
	if longReport.IsFlat() || shortReport.IsFlat() || !flatReport.IsFlat() || !noPositionReport.IsFlat() {
		t.Fatal("flat predicates differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:285
//	test: test_is_long
func TestPositionStatusReportIsLong(t *testing.T) {
	longReport := positionReportLong()
	shortReport := positionReportShort()
	flatReport := positionReportFlat()
	if !longReport.IsLong() || shortReport.IsLong() || flatReport.IsLong() {
		t.Fatal("long predicates differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:296
//	test: test_is_short
func TestPositionStatusReportIsShort(t *testing.T) {
	longReport := positionReportLong()
	shortReport := positionReportShort()
	flatReport := positionReportFlat()
	if longReport.IsShort() || !shortReport.IsShort() || flatReport.IsShort() {
		t.Fatal("short predicates differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:307
//	test: test_display
func TestPositionStatusReportDisplay(t *testing.T) {
	display := positionReportLong().String()
	for _, text := range []string{
		"PositionStatusReport",
		"SIM-001",
		"AUDUSD.SIM",
		"LONG",
		"100",
		"P-001",
		"avg_px_open=None",
	} {
		if !strings.Contains(display, text) {
			t.Fatalf("%q does not contain %q", display, text)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:321
//	test: test_clone_and_equality
func TestPositionStatusReportCloneAndEquality(t *testing.T) {
	report1 := positionReportLong()
	report2 := report1.Clone()
	if !report1.Equal(report2) {
		t.Fatal("clone differs from original")
	}
	if report1.VenuePositionID == report2.VenuePositionID {
		t.Fatal("clone retained optional-field pointer")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:329
//	test: test_serialization_roundtrip
func TestPositionStatusReportSerializationRoundtrip(t *testing.T) {
	original := positionReportLong()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored PositionStatusReport
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !original.Equal(restored) {
		t.Fatalf("round-trip differs: %+v != %+v", original, restored)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:339
//	test: test_signed_decimal_qty_calculation
func TestPositionStatusReportSignedDecimalQtyCalculation(t *testing.T) {
	long100 := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideLong,
		decimal.MustQuantity("100.5"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		nil,
	)
	short200 := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideShort,
		decimal.MustQuantity("200.75"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		nil,
	)
	if !long100.SignedDecimalQty.Equal(decimal.MustParse("100.5")) {
		t.Fatalf("long signed quantity = %s", long100.SignedDecimalQty)
	}
	if !short200.SignedDecimalQty.Equal(decimal.MustParse("-200.75")) {
		t.Fatalf("short signed quantity = %s", short200.SignedDecimalQty)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:370
//	test: test_different_position_sides_not_equal
func TestPositionStatusReportDifferentPositionSidesNotEqual(t *testing.T) {
	longReport := positionReportLong()
	venuePositionID := ids.MustPositionID("P-001")
	shortReport := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideShort,
		decimal.MustQuantity("100"),
		1_000_000_000,
		2_000_000_000,
		"",
		&venuePositionID,
		nil,
	)
	if longReport.Equal(shortReport) {
		t.Fatal("different sides compared equal")
	}
	if longReport.SignedDecimalQty.Equal(shortReport.SignedDecimalQty) {
		t.Fatal("different signed quantities compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:392
//	test: test_with_avg_px_open
func TestPositionStatusReportWithAvgPxOpen(t *testing.T) {
	venuePositionID := ids.MustPositionID("P-001")
	averagePriceOpen := decimal.MustParse("1.23456")
	report := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideLong,
		decimal.MustQuantity("100"),
		1_000_000_000,
		2_000_000_000,
		"",
		&venuePositionID,
		&averagePriceOpen,
	)
	if report.AveragePriceOpen == nil || !report.AveragePriceOpen.Equal(decimal.MustParse("1.23456")) {
		t.Fatalf("average open price = %v", report.AveragePriceOpen)
	}
	if !strings.Contains(report.String(), "avg_px_open=Some(1.23456)") {
		t.Fatalf("display = %q", report.String())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:413
//	test: test_avg_px_open_none_default
func TestPositionStatusReportAvgPxOpenNoneDefault(t *testing.T) {
	report := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideLong,
		decimal.MustQuantity("100"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		nil,
	)
	if report.AveragePriceOpen != nil {
		t.Fatalf("average open price = %v", report.AveragePriceOpen)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:430
//	test: test_avg_px_open_with_different_sides
func TestPositionStatusReportAvgPxOpenWithDifferentSides(t *testing.T) {
	longPrice := decimal.MustParse("1.50000")
	shortPrice := decimal.MustParse("1.60000")
	longWithPrice := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideLong,
		decimal.MustQuantity("100"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		&longPrice,
	)
	shortWithPrice := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideShort,
		decimal.MustQuantity("100"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		&shortPrice,
	)
	if longWithPrice.AveragePriceOpen == nil ||
		!longWithPrice.AveragePriceOpen.Equal(decimal.MustParse("1.50000")) {
		t.Fatalf("long average open = %v", longWithPrice.AveragePriceOpen)
	}
	if shortWithPrice.AveragePriceOpen == nil ||
		!shortWithPrice.AveragePriceOpen.Equal(decimal.MustParse("1.60000")) {
		t.Fatalf("short average open = %v", shortWithPrice.AveragePriceOpen)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/reports/position.rs:466
//	test: test_avg_px_open_serialization
func TestPositionStatusReportAvgPxOpenSerialization(t *testing.T) {
	averagePriceOpen := decimal.MustParse("1.99999")
	report := NewPositionStatusReport(
		ids.MustAccountID("SIM-001"),
		ids.MustInstrumentID("AUDUSD.SIM"),
		PositionSideLong,
		decimal.MustQuantity("100"),
		1_000_000_000,
		2_000_000_000,
		"",
		nil,
		&averagePriceOpen,
	)
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var restored PositionStatusReport
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.AveragePriceOpen == nil ||
		!restored.AveragePriceOpen.Equal(*report.AveragePriceOpen) {
		t.Fatalf("average price changed: %v != %v", restored.AveragePriceOpen, report.AveragePriceOpen)
	}
}
