package market

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func requireBarPanicContains(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(barPanicString(value), want) {
			t.Fatalf("panic = %v, want substring %q", value, want)
		}
	}()
	fn()
}

func barPanicString(value any) string {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func testBarType() BarType {
	return MustBarType("AAPL.XNAS-1-MINUTE-LAST-INTERNAL")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1175
//	test: test_bar_specification_new_invalid
func TestBarSpecificationNewInvalid(t *testing.T) {
	_, err := NewBarSpecification(0, BarAggregationTick, PriceTypeLast)
	if err == nil || !strings.Contains(err.Error(), "Invalid step: 0 (must be non-zero)") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1187
//	test: test_bar_specification_new_checked_with_invalid_step_panics
func TestBarSpecificationNewCheckedWithInvalidStepPanics(t *testing.T) {
	requireBarPanicContains(t, "Invalid step: 0 (must be non-zero)", func() {
		MustBarSpecification(0, BarAggregationTick, PriceTypeLast)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1196
//	test: test_bar_specification_new_with_invalid_periodic_step_panics
func TestBarSpecificationNewWithInvalidPeriodicStepPanics(t *testing.T) {
	requireBarPanicContains(t, "Invalid step in bar_type.spec.step: 7", func() {
		MustBarSpecification(7, BarAggregationMinute, PriceTypeLast)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1251
//	test: test_bar_specification_new_checked_invalid_periodic_step
func TestBarSpecificationNewCheckedInvalidPeriodicStep(t *testing.T) {
	tests := []struct {
		aggregation BarAggregation
		step        uint64
		want        string
	}{
		{BarAggregationMillisecond, 12, "Invalid step in bar_type.spec.step: 12 for aggregation=MILLISECOND. step must evenly divide 1000"},
		{BarAggregationMillisecond, 1000, "Invalid step in bar_type.spec.step: 1000 for aggregation=MILLISECOND. step must not be 1000"},
		{BarAggregationSecond, 50, "Invalid step in bar_type.spec.step: 50 for aggregation=SECOND. step must evenly divide 60"},
		{BarAggregationSecond, 60, "Invalid step in bar_type.spec.step: 60 for aggregation=SECOND. step must not be 60"},
		{BarAggregationMinute, 40, "Invalid step in bar_type.spec.step: 40 for aggregation=MINUTE. step must evenly divide 60"},
		{BarAggregationMinute, 60, "Invalid step in bar_type.spec.step: 60 for aggregation=MINUTE. step must not be 60"},
		{BarAggregationHour, 5, "Invalid step in bar_type.spec.step: 5 for aggregation=HOUR. step must evenly divide 24"},
		{BarAggregationHour, 13, "Invalid step in bar_type.spec.step: 13 for aggregation=HOUR. step must evenly divide 24"},
		{BarAggregationHour, 24, "Invalid step in bar_type.spec.step: 24 for aggregation=HOUR. step must not be 24"},
		{BarAggregationMonth, 5, "Invalid step in bar_type.spec.step: 5 for aggregation=MONTH. step must evenly divide 12"},
	}
	for _, test := range tests {
		_, err := NewBarSpecification(test.step, test.aggregation, PriceTypeLast)
		if err == nil || !strings.HasPrefix(err.Error(), test.want) {
			t.Errorf("%s/%d: error = %v", test.aggregation, test.step, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1275
//	test: test_bar_specification_new_checked_allows_non_periodic_steps
func TestBarSpecificationNewCheckedAllowsNonPeriodicSteps(t *testing.T) {
	for _, aggregation := range []BarAggregation{
		BarAggregationDay, BarAggregationWeek, BarAggregationYear, BarAggregationTick,
		BarAggregationTickImbalance, BarAggregationTickRuns, BarAggregationVolume,
		BarAggregationVolumeImbalance, BarAggregationVolumeRuns, BarAggregationValue,
		BarAggregationValueImbalance, BarAggregationValueRuns, BarAggregationRenko,
	} {
		if _, err := NewBarSpecification(7, aggregation, PriceTypeLast); err != nil {
			t.Errorf("%s rejected: %v", aggregation, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1302
//	test: test_get_bar_interval
func TestGetBarInterval(t *testing.T) {
	tests := []struct {
		aggregation BarAggregation
		step        uint64
		want        time.Duration
	}{
		{BarAggregationMillisecond, 1, time.Millisecond}, {BarAggregationMillisecond, 10, 10 * time.Millisecond},
		{BarAggregationSecond, 1, time.Second}, {BarAggregationSecond, 15, 15 * time.Second},
		{BarAggregationMinute, 1, time.Minute}, {BarAggregationMinute, 30, 30 * time.Minute},
		{BarAggregationHour, 1, time.Hour}, {BarAggregationHour, 4, 4 * time.Hour},
		{BarAggregationDay, 1, 24 * time.Hour}, {BarAggregationDay, 2, 48 * time.Hour},
		{BarAggregationWeek, 1, 7 * 24 * time.Hour}, {BarAggregationWeek, 2, 14 * 24 * time.Hour},
		{BarAggregationMonth, 1, 30 * 24 * time.Hour}, {BarAggregationMonth, 3, 90 * 24 * time.Hour},
		{BarAggregationYear, 1, 365 * 24 * time.Hour}, {BarAggregationYear, 2, 730 * 24 * time.Hour},
	}
	for _, test := range tests {
		barType := NewBarType("BTCUSDT-PERP.BINANCE", MustBarSpecification(test.step, test.aggregation, PriceTypeLast), AggregationSourceInternal)
		if got := GetBarInterval(barType); got != test.want {
			t.Errorf("%s/%d = %s, want %s", test.aggregation, test.step, got, test.want)
		}
	}
	requireBarPanicContains(t, "Aggregation not time based", func() {
		GetBarInterval(NewBarType("BTCUSDT-PERP.BINANCE", MustBarSpecification(1, BarAggregationTick, PriceTypeLast), AggregationSourceInternal))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1336
//	test: test_get_bar_interval_ns
func TestGetBarIntervalNS(t *testing.T) {
	tests := []struct {
		aggregation BarAggregation
		step, want  uint64
	}{
		{BarAggregationMillisecond, 1, 1_000_000}, {BarAggregationMillisecond, 10, 10_000_000},
		{BarAggregationSecond, 1, 1_000_000_000}, {BarAggregationSecond, 10, 10_000_000_000},
		{BarAggregationMinute, 1, 60_000_000_000}, {BarAggregationMinute, 30, 1_800_000_000_000},
		{BarAggregationHour, 1, 3_600_000_000_000}, {BarAggregationHour, 4, 14_400_000_000_000},
		{BarAggregationDay, 1, 86_400_000_000_000}, {BarAggregationDay, 2, 172_800_000_000_000},
		{BarAggregationWeek, 1, 604_800_000_000_000}, {BarAggregationWeek, 2, 1_209_600_000_000_000},
		{BarAggregationMonth, 1, 2_592_000_000_000_000}, {BarAggregationMonth, 3, 7_776_000_000_000_000},
		{BarAggregationYear, 1, 31_536_000_000_000_000}, {BarAggregationYear, 2, 63_072_000_000_000_000},
	}
	for _, test := range tests {
		barType := NewBarType("BTCUSDT-PERP.BINANCE", MustBarSpecification(test.step, test.aggregation, PriceTypeLast), AggregationSourceInternal)
		if got := GetBarIntervalNanos(barType); got != UnixNanos(test.want) {
			t.Errorf("%s/%d = %d, want %d", test.aggregation, test.step, got, test.want)
		}
	}
	requireBarPanicContains(t, "Aggregation not time based", func() {
		GetBarIntervalNanos(NewBarType("BTCUSDT-PERP.BINANCE", MustBarSpecification(1, BarAggregationTick, PriceTypeLast), AggregationSourceInternal))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1367
//	test: test_get_bar_interval_step_exceeds_i64_panics
func TestGetBarIntervalStepExceedsI64Panics(t *testing.T) {
	requireBarPanicContains(t, "`step` exceeds i64 range", func() {
		GetBarInterval(NewBarType("BTCUSDT-PERP.BINANCE", BarSpecification{math.MaxUint64, BarAggregationSecond, PriceTypeLast}, AggregationSourceInternal))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1374
//	test: test_get_bar_interval_week_step_overflow_panics
func TestGetBarIntervalWeekStepOverflowPanics(t *testing.T) {
	requireBarPanicContains(t, "`step` overflows i64 days", func() {
		GetBarInterval(NewBarType("BTCUSDT-PERP.BINANCE", BarSpecification{math.MaxInt64, BarAggregationWeek, PriceTypeLast}, AggregationSourceInternal))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1382
//	test: test_timedelta_year_step_overflow_panics
func TestTimedeltaYearStepOverflowPanics(t *testing.T) {
	requireBarPanicContains(t, "`step` overflows i64 days", func() {
		GetBarInterval(NewBarType("BTCUSDT-PERP.BINANCE", BarSpecification{math.MaxInt64, BarAggregationYear, PriceTypeLast}, AggregationSourceInternal))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1390
//	test: test_get_time_bar_start_month_step_exceeds_u32_panics
func TestGetTimeBarStartMonthStepExceedsU32Panics(t *testing.T) {
	requireBarPanicContains(t, "`step` exceeds u32 range for month arithmetic", func() {
		barType := NewBarType("BTCUSDT-PERP.BINANCE", BarSpecification{1 << 40, BarAggregationMonth, PriceTypeLast}, AggregationSourceInternal)
		GetTimeBarStart(time.Date(2024, 7, 21, 12, 0, 0, 0, time.UTC), barType, 0)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1398
//	test: test_get_time_bar_start_year_step_exceeds_i32_panics
func TestGetTimeBarStartYearStepExceedsI32Panics(t *testing.T) {
	requireBarPanicContains(t, "`step` exceeds i32 range for year arithmetic", func() {
		barType := NewBarType("BTCUSDT-PERP.BINANCE", BarSpecification{1 << 40, BarAggregationYear, PriceTypeLast}, AggregationSourceInternal)
		GetTimeBarStart(time.Date(2024, 7, 21, 12, 0, 0, 0, time.UTC), barType, 0)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1460
//	test: test_get_time_bar_start
func TestGetTimeBarStart(t *testing.T) {
	date := func(hour, minute, second, nanos int) time.Time {
		return time.Date(2024, 7, 21, hour, minute, second, nanos, time.UTC)
	}
	tests := []struct {
		now, want   time.Time
		aggregation BarAggregation
		step        uint64
	}{
		{date(12, 34, 56, 123_000_000), date(12, 34, 56, 123_000_000), BarAggregationMillisecond, 1},
		{date(12, 34, 56, 123_000_000), date(12, 34, 56, 120_000_000), BarAggregationMillisecond, 10},
		{date(12, 34, 56, 0), date(12, 34, 56, 0), BarAggregationSecond, 1},
		{date(12, 34, 56, 0), date(12, 34, 55, 0), BarAggregationSecond, 5},
		{date(12, 34, 56, 0), date(12, 34, 0, 0), BarAggregationMinute, 1},
		{date(12, 34, 56, 0), date(12, 30, 0, 0), BarAggregationMinute, 5},
		{date(12, 34, 56, 0), date(12, 0, 0, 0), BarAggregationHour, 1},
		{date(12, 34, 56, 0), date(12, 0, 0, 0), BarAggregationHour, 2},
		{date(12, 34, 56, 0), date(0, 0, 0, 0), BarAggregationDay, 1},
	}
	for _, test := range tests {
		barType := NewBarType("BTCUSDT-PERP.BINANCE", MustBarSpecification(test.step, test.aggregation, PriceTypeLast), AggregationSourceInternal)
		if got := GetTimeBarStart(test.now, barType, 0); !got.Equal(test.want) {
			t.Errorf("%s/%d: got %s, want %s", test.aggregation, test.step, got, test.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1477
//	test: test_bar_spec_string_reprs
func TestBarSpecStringRepresentations(t *testing.T) {
	spec := MustBarSpecification(1, BarAggregationMinute, PriceTypeBid)
	if spec.String() != "1-MINUTE-BID" || fmt.Sprint(spec) != "1-MINUTE-BID" {
		t.Fatalf("spec = %s", spec)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1484
//	test: test_bar_type_parse_valid
func TestBarTypeParseValid(t *testing.T) {
	const input = "BTCUSDT-PERP.BINANCE-1-MINUTE-LAST-EXTERNAL"
	barType, err := ParseBarType(input)
	if err != nil {
		t.Fatal(err)
	}
	wantSpec := MustBarSpecification(1, BarAggregationMinute, PriceTypeLast)
	if barType.InstrumentID != "BTCUSDT-PERP.BINANCE" || barType.Spec != wantSpec ||
		barType.AggregationSource != AggregationSourceExternal || barType != MustBarType(input) {
		t.Fatalf("unexpected bar type: %+v", barType)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1513
//	test: test_bar_type_aggregation_source_predicates
func TestBarTypeAggregationSourcePredicates(t *testing.T) {
	tests := []struct {
		input              string
		external, internal bool
	}{
		{"BTCUSDT-PERP.BINANCE-1-MINUTE-LAST-EXTERNAL", true, false},
		{"BTCUSDT-PERP.BINANCE-1-MINUTE-LAST-INTERNAL", false, true},
		{"BTCUSDT-PERP.BINANCE-2-MINUTE-LAST-INTERNAL@1-MINUTE-EXTERNAL", false, true},
		{"BTCUSDT-PERP.BINANCE-2-MINUTE-LAST-EXTERNAL@1-MINUTE-INTERNAL", true, false},
	}
	for _, test := range tests {
		barType := MustBarType(test.input)
		if barType.IsExternallyAggregated() != test.external || barType.IsInternallyAggregated() != test.internal {
			t.Errorf("%s predicates = %v/%v", test.input, barType.IsExternallyAggregated(), barType.IsInternallyAggregated())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1524
//	test: test_bar_type_composite_aggregation_source_predicates_track_inner
func TestBarTypeCompositeAggregationSourcePredicatesTrackInner(t *testing.T) {
	barType := MustBarType("BTCUSDT-PERP.BINANCE-2-MINUTE-LAST-INTERNAL@1-MINUTE-EXTERNAL")
	if !barType.IsInternallyAggregated() || barType.IsExternallyAggregated() {
		t.Fatal("outer source predicates incorrect")
	}
	composite := barType.CompositeType()
	if !composite.IsExternallyAggregated() || composite.IsInternallyAggregated() {
		t.Fatal("inner source predicates incorrect")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1537
//	test: test_bar_type_from_str_with_utf8_symbol
func TestBarTypeFromStringWithUTF8Symbol(t *testing.T) {
	const input = "TËST-PÉRP.BINANCE-1-MINUTE-LAST-EXTERNAL"
	barType, err := ParseBarType(input)
	if err != nil {
		t.Fatal(err)
	}
	if barType.InstrumentID != "TËST-PÉRP.BINANCE" ||
		barType.Spec != MustBarSpecification(1, BarAggregationMinute, PriceTypeLast) ||
		barType.AggregationSource != AggregationSourceExternal || barType.String() != input {
		t.Fatalf("unexpected bar type: %+v", barType)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1556
//	test: test_bar_type_composite_parse_valid
func TestBarTypeCompositeParseValid(t *testing.T) {
	const input = "BTCUSDT-PERP.BINANCE-2-MINUTE-LAST-INTERNAL@1-MINUTE-EXTERNAL"
	barType := MustBarType(input)
	standard := barType.Standard()
	composite := barType.CompositeType()
	if barType.InstrumentID != "BTCUSDT-PERP.BINANCE" ||
		barType.Spec != MustBarSpecification(2, BarAggregationMinute, PriceTypeLast) ||
		barType.AggregationSource != AggregationSourceInternal || barType != MustBarType(input) ||
		!barType.IsComposite() {
		t.Fatalf("unexpected composite bar type: %+v", barType)
	}
	if standard.InstrumentID != barType.InstrumentID || standard.Spec != barType.Spec ||
		standard.AggregationSource != AggregationSourceInternal || !standard.IsStandard() {
		t.Fatalf("unexpected standard component: %+v", standard)
	}
	wantComposite := MustBarType("BTCUSDT-PERP.BINANCE-1-MINUTE-LAST-EXTERNAL")
	if composite != wantComposite || !composite.IsStandard() {
		t.Fatalf("unexpected composite component: %+v", composite)
	}
}

func assertBarTypeParseError(t *testing.T, input, token string, position int) {
	t.Helper()
	_, err := ParseBarType(input)
	want := fmt.Sprintf("Error parsing `BarType` from '%s', invalid token: '%s' at position %d", input, token, position)
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1601
//	test: test_bar_type_parse_invalid_token_pos_0
func TestBarTypeParseInvalidTokenPosition0(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP-1-MINUTE-LAST-INTERNAL", "BTCUSDT-PERP", 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1614
//	test: test_bar_type_parse_invalid_token_pos_1
func TestBarTypeParseInvalidTokenPosition1(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-INVALID-MINUTE-LAST-INTERNAL", "INVALID", 1)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1627
//	test: test_bar_type_parse_invalid_spec_step
func TestBarTypeParseInvalidSpecStep(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-60-MINUTE-LAST-INTERNAL", "60", 1)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1638
//	test: test_bar_type_parse_invalid_token_pos_2
func TestBarTypeParseInvalidTokenPosition2(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-1-INVALID-LAST-INTERNAL", "INVALID", 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1651
//	test: test_bar_type_parse_invalid_token_pos_3
func TestBarTypeParseInvalidTokenPosition3(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-1-MINUTE-INVALID-INTERNAL", "INVALID", 3)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1664
//	test: test_bar_type_parse_invalid_token_pos_4
func TestBarTypeParseInvalidTokenPosition4(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-1-MINUTE-BID-INVALID", "INVALID", 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1678
//	test: test_bar_type_parse_invalid_token_pos_5
func TestBarTypeParseInvalidTokenPosition5(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-2-MINUTE-LAST-INTERNAL@INVALID-MINUTE-EXTERNAL", "INVALID", 5)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1692
//	test: test_bar_type_parse_invalid_composite_spec_step
func TestBarTypeParseInvalidCompositeSpecStep(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-2-MINUTE-LAST-INTERNAL@60-MINUTE-EXTERNAL", "60", 5)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1704
//	test: test_bar_type_parse_invalid_token_pos_6
func TestBarTypeParseInvalidTokenPosition6(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-2-MINUTE-LAST-INTERNAL@1-INVALID-EXTERNAL", "INVALID", 6)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1718
//	test: test_bar_type_parse_invalid_token_pos_7
func TestBarTypeParseInvalidTokenPosition7(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-2-MINUTE-LAST-INTERNAL@1-MINUTE-INVALID", "INVALID", 7)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1732
//	test: test_bar_type_parse_rejects_extra_composite_segment
func TestBarTypeParseRejectsExtraCompositeSegment(t *testing.T) {
	assertBarTypeParseError(t, "BTCUSDT-PERP.BINANCE-2-MINUTE-LAST-INTERNAL@1-MINUTE-EXTERNAL@1-HOUR-EXTERNAL", "1-HOUR-EXTERNAL", 5)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1745
//	test: test_bar_type_equality
func TestBarTypeEquality(t *testing.T) {
	spec := MustBarSpecification(1, BarAggregationMinute, PriceTypeBid)
	first := NewBarType("AUD/USD.SIM", spec, AggregationSourceExternal)
	equal := NewBarType("AUD/USD.SIM", spec, AggregationSourceExternal)
	different := NewBarType("GBP/USD.SIM", spec, AggregationSourceExternal)
	if first != first || first != equal || first == different {
		t.Fatal("bar type equality invariant failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1776
//	test: test_bar_type_id_spec_key_ignores_aggregation_source
func TestBarTypeIDSpecKeyIgnoresAggregationSource(t *testing.T) {
	external := MustBarType("ESM4.XCME-1-MINUTE-LAST-EXTERNAL")
	internal := MustBarType("ESM4.XCME-1-MINUTE-LAST-INTERNAL")
	if external == internal {
		t.Fatal("full bar types compare equal")
	}
	if external.IDSpecKey() != internal.IDSpecKey() {
		t.Fatal("ID/spec keys differ")
	}
	key := external.IDSpecKey()
	if key.InstrumentID != external.InstrumentID || key.Spec != external.Spec {
		t.Fatalf("unexpected key: %+v", key)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1796
//	test: test_bar_type_comparison
func TestBarTypeComparison(t *testing.T) {
	spec := MustBarSpecification(1, BarAggregationMinute, PriceTypeBid)
	first := NewBarType("AUD/USD.SIM", spec, AggregationSourceExternal)
	equal := NewBarType("AUD/USD.SIM", spec, AggregationSourceExternal)
	third := NewBarType("GBP/USD.SIM", spec, AggregationSourceExternal)
	fourth := MustCompositeBarType(
		"GBP/USD.SIM", MustBarSpecification(2, BarAggregationMinute, PriceTypeBid),
		AggregationSourceInternal, 1, BarAggregationMinute, AggregationSourceExternal,
	)
	if first.Compare(equal) > 0 || first.Compare(third) >= 0 || third.Compare(first) <= 0 ||
		third.Compare(first) < 0 || fourth.Compare(first) < 0 {
		t.Fatal("bar type comparison invariant failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1841
//	test: test_bar_new
func TestBarNew(t *testing.T) {
	barType := testBarType()
	open, high := decimal.MustPrice("100.0"), decimal.MustPrice("105.0")
	low, close := decimal.MustPrice("95.0"), decimal.MustPrice("102.0")
	volume := decimal.MustQuantity("1000")
	bar, err := NewBar(barType, open, high, low, close, volume, 1_000_000, 2_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if bar.BarType != barType || !bar.Open.Equal(open) || !bar.High.Equal(high) ||
		!bar.Low.Equal(low) || !bar.Close.Equal(close) || !bar.Volume.Equal(volume) ||
		bar.TsEvent != 1_000_000 || bar.TsInit != 2_000_000 {
		t.Fatalf("unexpected bar: %+v", bar)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1870
//	test: test_bar_new_checked_conditions
func TestBarNewCheckedConditions(t *testing.T) {
	tests := []struct {
		open, high, low, close, want string
	}{
		{"100.0", "90.0", "95.0", "92.0", "high >= open"},
		{"100.0", "105.0", "110.0", "102.0", "high >= low"},
		{"100.0", "105.0", "95.0", "110.0", "high >= close"},
		{"100.0", "105.0", "95.0", "90.0", "low <= close"},
		{"100.0", "110.0", "105.0", "108.0", "low <= open"},
		{"100.0", "90.0", "110.0", "120.0", "high >= open"},
	}
	for _, test := range tests {
		_, err := NewBar(
			testBarType(), decimal.MustPrice(test.open), decimal.MustPrice(test.high),
			decimal.MustPrice(test.low), decimal.MustPrice(test.close),
			decimal.MustQuantity("1000"), 1_000_000, 2_000_000,
		)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%v: error = %v", test, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1896
//	test: test_bar_equality
func TestBarEquality(t *testing.T) {
	barType := NewBarType(
		"AUDUSD.SIM",
		MustBarSpecification(1, BarAggregationMinute, PriceTypeBid),
		AggregationSourceExternal,
	)
	first := Bar{
		BarType: barType, Open: decimal.MustPrice("1.00001"), High: decimal.MustPrice("1.00004"),
		Low: decimal.MustPrice("1.00002"), Close: decimal.MustPrice("1.00003"),
		Volume: decimal.MustQuantity("100000"), TsEvent: 0, TsInit: 1,
	}
	different := first
	different.Open = decimal.MustPrice("1.00000")
	if !first.Equal(first) || first.Equal(different) {
		t.Fatal("bar equality invariant failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1933
//	test: test_json_serialization
func TestBarJSONSerialization(t *testing.T) {
	source := DefaultBar()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Bar
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(source) {
		t.Fatalf("round trip differs: %s", data)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1941
//	test: test_msgpack_serialization
//
// Adaptations:
//   - Rust MessagePack plumbing is replaced by a deterministic native Go binary codec.
func TestBarMsgpackSerialization(t *testing.T) {
	source := DefaultBar()
	data, err := source.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var decoded Bar
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(source) {
		t.Fatal("binary round trip differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1949
//	test: test_bar_deserialization_rejects_invalid_ohlc
func TestBarDeserializationRejectsInvalidOHLC(t *testing.T) {
	data := []byte(`{
		"type":"Bar",
		"bar_type":"AUD/USD.SIM-1-MINUTE-BID-EXTERNAL",
		"open":"1.00010","high":"1.00000","low":"1.00020","close":"1.00010",
		"volume":"100000","ts_event":0,"ts_init":0
	}`)
	var bar Bar
	if err := json.Unmarshal(data, &bar); err == nil {
		t.Fatalf("invalid OHLC was accepted: %+v", bar)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1970
//	test: test_bar_specification_deserialization_rejects_invalid_step
func TestBarSpecificationDeserializationRejectsInvalidStep(t *testing.T) {
	var spec BarSpecification
	if err := json.Unmarshal([]byte(`{"step":7,"aggregation":"MINUTE","price_type":"LAST"}`), &spec); err == nil {
		t.Fatalf("invalid specification was accepted: %+v", spec)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1981
//	test: test_bar_specification_builder_rejects_invalid_step
//
// Adaptations:
//   - The checked Go constructor replaces the Rust builder.
func TestBarSpecificationBuilderRejectsInvalidStep(t *testing.T) {
	if _, err := NewBarSpecification(7, BarAggregationMinute, PriceTypeLast); err == nil {
		t.Fatal("checked constructor accepted non-periodic step")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:1995
//	test: test_bar_spec_12_month_round_trips
func TestBarSpec12MonthRoundTrips(t *testing.T) {
	spec, err := NewBarSpecification(12, BarAggregationMonth, PriceTypeLast)
	if err != nil {
		t.Fatal(err)
	}
	barType := NewBarType("BTC-USDT.OKX", spec, AggregationSourceExternal)
	parsed, err := ParseBarType(barType.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != barType || spec != MustBarSpecification(12, BarAggregationMonth, PriceTypeLast) {
		t.Fatalf("round trip differs: %+v", parsed)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:2013
//	test: test_bar_type_new_composite_checked_invalid_step
func TestBarTypeNewCompositeCheckedInvalidStep(t *testing.T) {
	spec := MustBarSpecification(5, BarAggregationMinute, PriceTypeBid)
	if _, err := NewCompositeBarType(
		"AUD/USD.SIM", spec, AggregationSourceInternal,
		0, BarAggregationMinute, AggregationSourceExternal,
	); err == nil {
		t.Fatal("zero composite step was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:2118
//	test: prop_bar_type_string_round_trip
//
// Adaptations:
//   - The source property domain is covered deterministically without randomness.
func TestPropertyBarTypeStringRoundTrip(t *testing.T) {
	symbols := []string{"AAPL", "BTC-PERP", "EUR/USD", "ES-MINI-4", "MSFT.OQ", "6E"}
	venues := []string{"SIM", "XNAS", "GLBX", "BINANCE"}
	specs := []BarSpecification{
		MustBarSpecification(1, BarAggregationMillisecond, PriceTypeBid),
		MustBarSpecification(15, BarAggregationSecond, PriceTypeAsk),
		MustBarSpecification(30, BarAggregationMinute, PriceTypeMid),
		MustBarSpecification(4, BarAggregationHour, PriceTypeLast),
		MustBarSpecification(2, BarAggregationDay, PriceTypeLast),
		MustBarSpecification(1, BarAggregationWeek, PriceTypeLast),
		MustBarSpecification(12, BarAggregationMonth, PriceTypeLast),
		MustBarSpecification(17, BarAggregationTick, PriceTypeLast),
		MustBarSpecification(23, BarAggregationVolume, PriceTypeLast),
		MustBarSpecification(29, BarAggregationValue, PriceTypeLast),
	}
	for _, symbol := range symbols {
		for _, venue := range venues {
			for _, spec := range specs {
				for _, source := range []AggregationSource{AggregationSourceInternal, AggregationSourceExternal} {
					standard := NewBarType(InstrumentID(symbol+"."+venue), spec, source)
					assertBarTypeRoundTrip(t, standard)
					composite := MustCompositeBarType(
						standard.InstrumentID, spec, source, 5,
						BarAggregationMinute, AggregationSourceExternal,
					)
					assertBarTypeRoundTrip(t, composite)
				}
			}
		}
	}
}

func assertBarTypeRoundTrip(t *testing.T, source BarType) {
	t.Helper()
	parsed, err := ParseBarType(source.String())
	if err != nil {
		t.Fatalf("parse %q: %v", source, err)
	}
	if parsed != source {
		t.Fatalf("round trip differs: got %+v, want %+v", parsed, source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bar.rs:2149
//	test: prop_get_time_bar_start_alignment
//
// Adaptations:
//   - Representative boundary timestamps replace random generation.
func TestPropertyGetTimeBarStartAlignment(t *testing.T) {
	specs := []BarSpecification{
		MustBarSpecification(1, BarAggregationMillisecond, PriceTypeLast),
		MustBarSpecification(10, BarAggregationSecond, PriceTypeLast),
		MustBarSpecification(15, BarAggregationMinute, PriceTypeLast),
		MustBarSpecification(4, BarAggregationHour, PriceTypeLast),
		MustBarSpecification(3, BarAggregationDay, PriceTypeLast),
		MustBarSpecification(1, BarAggregationWeek, PriceTypeLast),
	}
	times := []time.Time{
		time.Unix(946_684_800, 0).UTC(),
		time.Date(2024, 7, 21, 12, 34, 56, 123_456_789, time.UTC),
		time.Unix(2_524_607_999, 999_999_999).UTC(),
	}
	for _, spec := range specs {
		barType := NewBarType("AAPL.XNAS", spec, AggregationSourceInternal)
		interval := GetBarInterval(barType)
		for _, now := range times {
			start := GetTimeBarStart(now, barType, 0)
			if start.After(now) || now.Sub(start) >= interval {
				t.Errorf("%s at %s: start %s, interval %s", spec, now, start, interval)
			}
		}
	}
}
