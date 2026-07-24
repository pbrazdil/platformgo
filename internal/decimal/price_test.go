package decimal

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:877
//	test: test_invalid_precision_new
//
// Adaptations:
//   - The forbidden f64 constructor is replaced by an exact string input.
func TestPriceRejectsInvalidPrecision(t *testing.T) {
	if _, err := NewPrice("1", 50); err == nil {
		t.Fatal("NewPrice accepted precision 50")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:893
//	test: test_invalid_precision_max
func TestMaxPriceRejectsInvalidPrecision(t *testing.T) {
	if _, err := MaxPrice(MaxPrecision + 1); err == nil {
		t.Fatal("MaxPrice accepted excessive precision")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:901
//	test: test_invalid_precision_min
func TestMinPriceRejectsInvalidPrecision(t *testing.T) {
	if _, err := MinPrice(MaxPrecision + 1); err == nil {
		t.Fatal("MinPrice accepted excessive precision")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:909
//	test: test_invalid_precision_zero
func TestZeroPriceRejectsInvalidPrecision(t *testing.T) {
	if _, err := ZeroPrice(MaxPrecision + 1); err == nil {
		t.Fatal("ZeroPrice accepted excessive precision")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:916
//	test: test_max_value_exceeded
func TestPriceRejectsValueAboveMaximum(t *testing.T) {
	if _, err := ParsePrice("17014118346046.0000000000000001"); err == nil {
		t.Fatal("ParsePrice accepted value above maximum")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:922
//	test: test_min_value_exceeded
func TestPriceRejectsValueBelowMinimum(t *testing.T) {
	if _, err := ParsePrice("-17014118346046.0000000000000001"); err == nil {
		t.Fatal("ParsePrice accepted value below minimum")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:927
//	test: test_is_positive_ok
func TestPriceIsPositive(t *testing.T) {
	price := MustPrice("42.00")
	if !price.IsPositive() {
		t.Fatal("positive price reported non-positive")
	}
	if err := price.RequirePositive("price"); err != nil {
		t.Fatalf("RequirePositive: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:937
//	test: test_is_positive_rejects_non_positive
func TestPricePositiveValidationRejectsZero(t *testing.T) {
	zero, err := ZeroPrice(2)
	if err != nil {
		t.Fatal(err)
	}
	err = zero.RequirePositive("price")
	if err == nil || err.Error() != `invalid Price for "price": not positive, was 0.00` {
		t.Fatalf("RequirePositive(zero) = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:977
//	test: test_construction
//
// Adaptations:
//   - Exact string input makes the source rounding result directly assertable.
func TestPriceConstruction(t *testing.T) {
	price, err := NewPrice("1.23456", 4)
	if err != nil {
		t.Fatal(err)
	}
	if price.Precision() != 4 || price.String() != "1.2346" {
		t.Fatalf("NewPrice = %s precision %d, want 1.2346 precision 4", price, price.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:986
//	test: test_negative_price_in_range
func TestNegativePriceInRange(t *testing.T) {
	price := MustPrice("-8507059173023.0000000000000000")
	if price.Decimal().Sign() >= 0 {
		t.Fatal("negative price lost its sign")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1084
//	test: test_string_parsing
func TestPriceStringParsing(t *testing.T) {
	price := MustPrice("1.23456")
	if price.Precision() != 5 || price.String() != "1.23456" {
		t.Fatalf("parsed price = %s precision %d", price, price.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1091
//	test: test_negative_price_from_str
func TestNegativePriceFromString(t *testing.T) {
	price := MustPrice("-123.45")
	if price.Precision() != 2 || price.String() != "-123.45" {
		t.Fatalf("parsed price = %s precision %d", price, price.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1098
//	test: test_string_parsing_errors
func TestPriceStringParsingErrors(t *testing.T) {
	for _, input := range []string{"invalid", "", ".", "1.2.3"} {
		if _, err := ParsePrice(input); err == nil {
			t.Errorf("ParsePrice(%q) succeeded", input)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1107
//	test: test_from_str_scientific_notation
func TestPriceScientificNotation(t *testing.T) {
	tests := []struct {
		input, expected string
		precision       uint8
	}{
		{"1e7", "10000000", 0},
		{"1.5e3", "1500", 0},
		{"1.234e-2", "0.01234", 5},
		{"5E-3", "0.005", 3},
	}
	for _, test := range tests {
		price := MustPrice(test.input)
		if price.String() != test.expected || price.Precision() != test.precision {
			t.Errorf("ParsePrice(%q) = %s precision %d", test.input, price, price.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1126
//	test: test_from_str_with_underscores
func TestPriceStringWithUnderscores(t *testing.T) {
	tests := []struct {
		input, expected string
		precision       uint8
	}{
		{"1_234.56", "1234.56", 2},
		{"1000000", "1000000", 0},
		{"99_999.999_99", "99999.99999", 5},
	}
	for _, test := range tests {
		price := MustPrice(test.input)
		if price.String() != test.expected || price.Precision() != test.precision {
			t.Errorf("ParsePrice(%q) = %s precision %d", test.input, price, price.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1160
//	test: test_from_decimal_dp_rounding
func TestPriceDecimalRounding(t *testing.T) {
	tests := map[string]string{"1.005": "1.00", "1.015": "1.02"}
	for input, expected := range tests {
		price, err := NewPrice(input, 2)
		if err != nil {
			t.Fatal(err)
		}
		if price.String() != expected {
			t.Errorf("NewPrice(%s, 2) = %s, want %s", input, price, expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1172
//	test: test_from_decimal_infers_precision
func TestPriceInfersDecimalPrecision(t *testing.T) {
	tests := []struct {
		input string
		scale uint8
	}{
		{"123.456", 3}, {"100", 0}, {"1.23456789", 8},
	}
	for _, test := range tests {
		price := MustPrice(test.input)
		if price.Precision() != test.scale || price.String() != test.input {
			t.Errorf("ParsePrice(%s) = %s precision %d", test.input, price, price.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1198
//	test: test_from_decimal_trailing_zeros
func TestPriceDecimalTrailingZeros(t *testing.T) {
	decimal := MustParse("1.230")
	price, err := priceFromDecimal(decimal)
	if err != nil {
		t.Fatal(err)
	}
	if price.Precision() != 3 || price.String() != "1.230" {
		t.Fatalf("price = %s precision %d", price, price.Precision())
	}
	normalized, err := priceFromDecimal(decimal.Normalize())
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Precision() != 2 || normalized.String() != "1.23" {
		t.Fatalf("normalized price = %s precision %d", normalized, normalized.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1222
//	test: test_from_str_preserves_trailing_zeros
func TestPriceStringPreservesTrailingZeros(t *testing.T) {
	tests := map[string]uint8{
		"1.00": 2, "1.0": 1, "1.000": 3, "100.00": 2, "0.10": 2, "0.100": 3,
	}
	for input, expected := range tests {
		price := MustPrice(input)
		if price.Precision() != expected {
			t.Errorf("ParsePrice(%s) precision = %d, want %d", input, price.Precision(), expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1228
//	test: test_from_decimal_excessive_precision_inference
func TestPriceRejectsExcessiveInferredPrecision(t *testing.T) {
	if _, err := ParsePrice("1.1234567890123456789012345678"); err == nil {
		t.Fatal("ParsePrice accepted excessive precision")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1240
//	test: test_from_decimal_dp_out_of_range_returns_typed_error_with_stable_display
func TestPriceDecimalOutOfRangeReturnsError(t *testing.T) {
	_, err := NewPrice("99999999999999999999.99", 2)
	if err == nil || !strings.Contains(err.Error(), "outside valid range") {
		t.Fatalf("NewPrice out-of-range error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1255
//	test: test_from_decimal_negative_price
func TestPriceFromNegativeDecimal(t *testing.T) {
	price := MustPrice("-123.45")
	if price.Precision() != 2 || price.Decimal().Sign() >= 0 {
		t.Fatalf("price = %s precision %d", price, price.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1265
//	test: test_string_formatting
func TestPriceFormatting(t *testing.T) {
	price := MustPrice("1234.5678")
	if price.String() != "1234.5678" {
		t.Fatalf("String() = %s", price)
	}
	if got := fmt.Sprintf("%#v", price); got != "Price(1234.5678)" {
		t.Fatalf("diagnostic format = %s", got)
	}
	if price.FormattedString() != "1_234.5678" {
		t.Fatalf("FormattedString() = %s", price.FormattedString())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1316
//	test: test_basic_arithmetic
func TestPriceBasicArithmetic(t *testing.T) {
	first := MustPrice("10.50")
	second := MustPrice("5.25")
	sum, ok := first.Add(second)
	if !ok || !sum.Equal(MustPrice("15.75")) {
		t.Fatalf("sum = %s, %v", sum, ok)
	}
	difference, ok := first.Sub(second)
	if !ok || !difference.Equal(MustPrice("5.25")) {
		t.Fatalf("difference = %s, %v", difference, ok)
	}
	if first.Neg().String() != "-10.50" {
		t.Fatalf("negative = %s", first.Neg())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1325
//	test: test_price_checked_add_within_bounds
func TestPriceCheckedAddWithinBounds(t *testing.T) {
	tests := []struct{ left, right, expected string }{
		{"10.00", "5.00", "15.00"},
		{"10.00", "-3.00", "7.00"},
	}
	for _, test := range tests {
		got, ok := MustPrice(test.left).Add(MustPrice(test.right))
		if !ok || !got.Equal(MustPrice(test.expected)) {
			t.Errorf("%s + %s = %s, %v", test.left, test.right, got, ok)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1335
//	test: test_price_checked_add_above_max_returns_none
func TestPriceCheckedAddAboveMaxFails(t *testing.T) {
	maximum, _ := MaxPrice(0)
	if _, ok := maximum.Add(MustPrice("1")); ok {
		t.Fatal("addition above maximum succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1342
//	test: test_price_checked_sub_within_bounds
func TestPriceCheckedSubWithinBounds(t *testing.T) {
	first := MustPrice("10.00")
	second := MustPrice("3.00")
	got, ok := first.Sub(second)
	if !ok || !got.Equal(MustPrice("7.00")) {
		t.Fatalf("10.00 - 3.00 = %s, %v", got, ok)
	}
	got, ok = second.Sub(first)
	if !ok || !got.Equal(MustPrice("-7.00")) {
		t.Fatalf("3.00 - 10.00 = %s, %v", got, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1350
//	test: test_price_checked_sub_below_min_returns_none
func TestPriceCheckedSubBelowMinFails(t *testing.T) {
	minimum, _ := MinPrice(0)
	if _, ok := minimum.Sub(MustPrice("1")); ok {
		t.Fatal("subtraction below minimum succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1357
//	test: test_price_checked_arith_uses_max_precision
func TestPriceCheckedArithmeticUsesMaxPrecision(t *testing.T) {
	sum, ok := MustPrice("10.5").Add(MustPrice("5.25"))
	if !ok || sum.Precision() != 2 || sum.String() != "15.75" {
		t.Fatalf("sum = %s precision %d, ok=%v", sum, sum.Precision(), ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1418
//	test: test_mixed_precision_add
func TestPriceMixedPrecisionAdd(t *testing.T) {
	sum, ok := MustPrice("10.5").Add(MustPrice("5.25"))
	if !ok || sum.Precision() != 2 || sum.String() != "15.75" {
		t.Fatalf("sum = %s precision %d", sum, sum.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1427
//	test: test_mixed_precision_sub
func TestPriceMixedPrecisionSub(t *testing.T) {
	difference, ok := MustPrice("10.5").Sub(MustPrice("5.25"))
	if !ok || difference.Precision() != 2 || difference.String() != "5.25" {
		t.Fatalf("difference = %s precision %d", difference, difference.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1445
//	test: test_equality_and_comparisons
func TestPriceEqualityAndComparisons(t *testing.T) {
	first := MustPrice("10.0")
	second := MustPrice("20.0")
	equal := MustPrice("10.0")
	if first.Cmp(second) >= 0 || second.Cmp(first) <= 0 || !first.Equal(equal) || first.Equal(second) {
		t.Fatal("price comparison invariant failed")
	}
	if MustPrice("0.9").Cmp(MustPrice("1.0")) >= 0 {
		t.Fatal("0.9 should be less than 1.0")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1512
//	test: test_price_serde_json_round_trip
func TestPriceJSONRoundTrip(t *testing.T) {
	price := MustPrice("1.0500")
	data, err := json.Marshal(price)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Price
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(price) {
		t.Fatalf("decoded price = %s precision %d", decoded, decoded.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1530
//	test: test_price_deserialize_invalid_string_returns_error
func TestPriceJSONRejectsInvalidString(t *testing.T) {
	var price Price
	if err := json.Unmarshal([]byte(`"not-a-price"`), &price); err == nil {
		t.Fatal("invalid JSON price was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1540
//	test: test_price_deserialize_out_of_range_returns_error
func TestPriceJSONRejectsOutOfRangeValue(t *testing.T) {
	var price Price
	if err := json.Unmarshal([]byte(`"99999999999999999999.99"`), &price); err == nil {
		t.Fatal("out-of-range JSON price was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1546
//	test: test_from_mantissa_exponent_exact_precision
func TestPriceFromMantissaExponentExactPrecision(t *testing.T) {
	assertMantissaPrice(t, 12345, -2, 2, "123.45")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1552
//	test: test_from_mantissa_exponent_excess_rounds_down
func TestPriceFromMantissaExponentExcessRoundsDown(t *testing.T) {
	assertMantissaPrice(t, 12345, -3, 2, "12.34")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1559
//	test: test_from_mantissa_exponent_excess_rounds_up
func TestPriceFromMantissaExponentExcessRoundsUp(t *testing.T) {
	assertMantissaPrice(t, 12355, -3, 2, "12.36")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1566
//	test: test_from_mantissa_exponent_positive_exponent
func TestPriceFromMantissaExponentPositiveExponent(t *testing.T) {
	assertMantissaPrice(t, 5, 2, 0, "500")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1572
//	test: test_from_mantissa_exponent_negative_mantissa
func TestPriceFromMantissaExponentNegativeMantissa(t *testing.T) {
	assertMantissaPrice(t, -12345, -2, 2, "-123.45")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1578
//	test: test_from_mantissa_exponent_zero
func TestPriceFromMantissaExponentZero(t *testing.T) {
	assertMantissaPrice(t, 0, 2, 2, "0.00")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1590
//	test: test_from_mantissa_exponent_checked_zero_with_large_exponent
func TestPriceFromMantissaExponentZeroWithLargeExponentChecked(t *testing.T) {
	assertMantissaPrice(t, 0, 119, 2, "0.00")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1596
//	test: test_from_mantissa_exponent_checked_invalid_precision
func TestPriceFromMantissaExponentRejectsInvalidPrecision(t *testing.T) {
	if _, err := PriceFromMantissaExponent(1, 0, MaxPrecision+1); err == nil {
		t.Fatal("invalid precision was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1607
//	test: test_from_mantissa_exponent_checked_overflow_returns_error
func TestPriceFromMantissaExponentOverflowReturnsError(t *testing.T) {
	if _, err := PriceFromMantissaExponent(9223372036854775807, 100, 0); err == nil {
		t.Fatal("overflowing mantissa/exponent was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1629
//	test: test_from_mantissa_exponent_zero_with_large_exponent
func TestPriceFromMantissaExponentZeroWithLargeExponent(t *testing.T) {
	assertMantissaPrice(t, 0, 119, 0, "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1635
//	test: test_from_mantissa_exponent_very_negative_exponent_rounds_to_zero
func TestPriceFromMantissaExponentVeryNegativeRoundsToZero(t *testing.T) {
	assertMantissaPrice(t, 12345, -120, 2, "0.00")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1641
//	test: test_decimal_arithmetic_operations
func TestPriceDecimalArithmetic(t *testing.T) {
	price := MustPrice("100.00")
	if got := price.AddDecimal(MustParse("50.25")); !got.Equal(MustParse("150.25")) {
		t.Fatalf("addition = %s", got)
	}
	if got := price.SubDecimal(MustParse("30.50")); !got.Equal(MustParse("69.50")) {
		t.Fatalf("subtraction = %s", got)
	}
	if got := price.MulDecimal(MustParse("1.5")); !got.Equal(MustParse("150.00")) {
		t.Fatalf("multiplication = %s", got)
	}
	got, err := price.QuoDecimal(MustParse("4"))
	if err != nil || !got.Equal(MustParse("25.00")) {
		t.Fatalf("division = %s, %v", got, err)
	}
}

func assertMantissaPrice(t *testing.T, mantissa int64, exponent int, precision uint8, expected string) {
	t.Helper()
	price, err := PriceFromMantissaExponent(mantissa, exponent, precision)
	if err != nil {
		t.Fatal(err)
	}
	if price.String() != expected {
		t.Fatalf("PriceFromMantissaExponent(%d, %d, %d) = %s, want %s", mantissa, exponent, precision, price, expected)
	}
}
