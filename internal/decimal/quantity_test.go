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
//	source: crates/model/src/types/quantity.rs:920
//	test: test_max_quantity_round_trips_through_raw
//
// Adaptations:
//   - Raw representation is omitted; exact maximum construction and checked addition are asserted.
func TestMaxQuantityRoundTrip(t *testing.T) {
	quantity, err := MaxQuantity(0)
	if err != nil {
		t.Fatal(err)
	}
	if quantity.String() != "34028236692093" {
		t.Fatalf("maximum quantity = %s", quantity)
	}
	if _, ok := quantity.Add(MustQuantity("0")); !ok {
		t.Fatal("adding zero to maximum failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:931
//	test: test_check_quantity_positive
func TestQuantityPositiveValidationRejectsZero(t *testing.T) {
	quantity, _ := ZeroQuantity(0)
	err := quantity.RequirePositive("qty")
	if err == nil || err.Error() != `invalid Quantity for "qty": not positive, was 0` {
		t.Fatalf("RequirePositive(zero) = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:960
//	test: test_invalid_precision_new
//
// Adaptations:
//   - The forbidden f64 constructor is replaced by an exact string input.
func TestQuantityRejectsInvalidPrecision(t *testing.T) {
	if _, err := NewQuantity("1", 17); err == nil {
		t.Fatal("NewQuantity accepted precision 17")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:976
//	test: test_invalid_precision_zero
func TestZeroQuantityRejectsInvalidPrecision(t *testing.T) {
	if _, err := ZeroQuantity(MaxPrecision + 1); err == nil {
		t.Fatal("ZeroQuantity accepted excessive precision")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:982
//	test: test_mixed_precision_add
func TestQuantityMixedPrecisionAdd(t *testing.T) {
	result, ok := MustQuantity("1.0").Add(MustQuantity("1.00"))
	if !ok || result.Precision() != 2 || result.String() != "2.00" {
		t.Fatalf("sum = %s precision %d, ok=%v", result, result.Precision(), ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:991
//	test: test_mixed_precision_sub
func TestQuantityMixedPrecisionSub(t *testing.T) {
	result, ok := MustQuantity("2.0").Sub(MustQuantity("1.00"))
	if !ok || result.Precision() != 2 || result.String() != "1.00" {
		t.Fatalf("difference = %s precision %d, ok=%v", result, result.Precision(), ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1000
//	test: test_mixed_precision_mul
func TestQuantityMixedPrecisionMul(t *testing.T) {
	result, ok := MustQuantity("2.0").Mul(MustQuantity("3.00"))
	if !ok || result.Precision() != 2 || result.String() != "6.00" {
		t.Fatalf("product = %s precision %d, ok=%v", result, result.Precision(), ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1009
//	test: test_new_non_zero_ok
func TestNonZeroQuantity(t *testing.T) {
	quantity, err := NonZeroQuantity("123.456", 3)
	if err != nil || !quantity.IsPositive() || quantity.String() != "123.456" {
		t.Fatalf("NonZeroQuantity = %s, %v", quantity, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1016
//	test: test_new_non_zero_zero_input
func TestNonZeroQuantityRejectsZero(t *testing.T) {
	if _, err := NonZeroQuantity("0", 0); err == nil {
		t.Fatal("NonZeroQuantity accepted zero")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1021
//	test: test_new_non_zero_rounds_to_zero
func TestNonZeroQuantityRejectsRoundedZero(t *testing.T) {
	if _, err := NonZeroQuantity("0.0004", 3); err == nil {
		t.Fatal("NonZeroQuantity accepted a value that rounds to zero")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1027
//	test: test_new_non_zero_negative
func TestNonZeroQuantityRejectsNegative(t *testing.T) {
	if _, err := NonZeroQuantity("-1", 0); err == nil {
		t.Fatal("NonZeroQuantity accepted a negative value")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1032
//	test: test_new_non_zero_exceeds_max
func TestNonZeroQuantityRejectsAboveMaximum(t *testing.T) {
	if _, err := NonZeroQuantity("340282366920930", 0); err == nil {
		t.Fatal("NonZeroQuantity accepted a value above maximum")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1037
//	test: test_new_non_zero_invalid_precision
func TestNonZeroQuantityRejectsInvalidPrecision(t *testing.T) {
	if _, err := NonZeroQuantity("1", MaxPrecision+1); err == nil {
		t.Fatal("NonZeroQuantity accepted excessive precision")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1042
//	test: test_new
func TestQuantityConstruction(t *testing.T) {
	quantity, err := NewQuantity("0.00812", 8)
	if err != nil {
		t.Fatal(err)
	}
	if quantity.Precision() != 8 || quantity.String() != "0.00812000" ||
		quantity.IsZero() || !quantity.IsPositive() {
		t.Fatalf("quantity = %s precision %d", quantity, quantity.Precision())
	}
	if !quantity.Equal(MustQuantity("0.00812000")) {
		t.Fatal("equivalent quantity values compare unequal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1057
//	test: test_check_quantity_positive_ok
func TestQuantityPositiveValidationAcceptsPositive(t *testing.T) {
	if err := MustQuantity("10").RequirePositive("qty"); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1063
//	test: test_negative_quantity_validation
func TestQuantityRejectsNegativeValue(t *testing.T) {
	if _, err := NewQuantity("-1", MaxPrecision); err == nil {
		t.Fatal("NewQuantity accepted negative value")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1105
//	test: test_zero
func TestZeroQuantity(t *testing.T) {
	quantity, err := ZeroQuantity(8)
	if err != nil || !quantity.IsZero() || quantity.Precision() != 8 || quantity.String() != "0.00000000" {
		t.Fatalf("ZeroQuantity = %s precision %d, %v", quantity, quantity.Precision(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1172
//	test: test_is_zero
func TestQuantityIsZero(t *testing.T) {
	if !MustQuantity("0").IsZero() || MustQuantity("0.1").IsZero() {
		t.Fatal("IsZero invariant failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1184
//	test: test_precision
func TestQuantityPrecision(t *testing.T) {
	if got := MustQuantity("1.23400").Precision(); got != 5 {
		t.Fatalf("precision = %d, want 5", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1203
//	test: test_from_str_valid_input
func TestQuantityStringValidInput(t *testing.T) {
	tests := map[string]uint8{"0": 0, "1.1": 1, "1.123456789": 9}
	for input, precision := range tests {
		quantity := MustQuantity(input)
		if quantity.Precision() != precision || !quantity.Decimal().Equal(MustParse(input)) {
			t.Errorf("ParseQuantity(%s) = %s precision %d", input, quantity, quantity.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1217
//	test: test_from_str_errors
func TestQuantityStringErrors(t *testing.T) {
	for _, input := range []string{"invalid", "12.34.56", "", "-1", "-0.001"} {
		if _, err := ParseQuantity(input); err == nil {
			t.Errorf("ParseQuantity(%q) succeeded", input)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1231
//	test: test_from_str_scientific_notation
func TestQuantityScientificNotation(t *testing.T) {
	tests := []struct {
		input, expected string
		precision       uint8
	}{
		{"1e7", "10000000", 0}, {"2.5e3", "2500", 0},
		{"1.234e-2", "0.01234", 5}, {"5E-3", "0.005", 3}, {"1.0e6", "1000000", 0},
	}
	for _, test := range tests {
		quantity := MustQuantity(test.input)
		if quantity.String() != test.expected || quantity.Precision() != test.precision {
			t.Errorf("ParseQuantity(%q) = %s precision %d", test.input, quantity, quantity.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1250
//	test: test_from_str_with_underscores
func TestQuantityStringWithUnderscores(t *testing.T) {
	tests := map[string]string{
		"1_234.56": "1234.56", "1000000": "1000000", "99_999.999_99": "99999.99999",
	}
	for input, expected := range tests {
		if got := MustQuantity(input).String(); got != expected {
			t.Errorf("ParseQuantity(%q) = %s, want %s", input, got, expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1266
//	test: test_from_decimal_dp_preservation
func TestQuantityDecimalPrecisionPreservation(t *testing.T) {
	quantity, err := NewQuantity("123.456789", 6)
	if err != nil || quantity.String() != "123.456789" || quantity.Precision() != 6 {
		t.Fatalf("NewQuantity = %s precision %d, %v", quantity, quantity.Precision(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1279
//	test: test_from_decimal_dp_rounding
func TestQuantityDecimalRounding(t *testing.T) {
	tests := map[string]string{"1.005": "1.00", "1.015": "1.02"}
	for input, expected := range tests {
		quantity, err := NewQuantity(input, 2)
		if err != nil || quantity.String() != expected {
			t.Errorf("NewQuantity(%s, 2) = %s, %v", input, quantity, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1291
//	test: test_from_decimal_infers_precision
func TestQuantityInfersDecimalPrecision(t *testing.T) {
	tests := map[string]uint8{"123.456": 3, "100": 0, "1.23456789": 8}
	for input, precision := range tests {
		quantity := MustQuantity(input)
		if quantity.Precision() != precision || quantity.String() != input {
			t.Errorf("ParseQuantity(%s) = %s precision %d", input, quantity, quantity.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1312
//	test: test_from_decimal_trailing_zeros
func TestQuantityDecimalTrailingZeros(t *testing.T) {
	decimal := MustParse("5.670")
	quantity, err := quantityFromDecimal(decimal)
	if err != nil || quantity.Precision() != 3 || quantity.String() != "5.670" {
		t.Fatalf("quantity = %s precision %d, %v", quantity, quantity.Precision(), err)
	}
	normalized, err := quantityFromDecimal(decimal.Normalize())
	if err != nil || normalized.Precision() != 2 || normalized.String() != "5.67" {
		t.Fatalf("normalized = %s precision %d, %v", normalized, normalized.Precision(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1336
//	test: test_from_str_preserves_trailing_zeros
func TestQuantityStringPreservesTrailingZeros(t *testing.T) {
	tests := map[string]uint8{
		"1.00": 2, "1.0": 1, "1.000": 3, "100.00": 2, "0.10": 2, "0.100": 3,
	}
	for input, precision := range tests {
		if got := MustQuantity(input).Precision(); got != precision {
			t.Errorf("ParseQuantity(%s) precision = %d, want %d", input, got, precision)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1342
//	test: test_from_decimal_excessive_precision_inference
func TestQuantityRejectsExcessiveInferredPrecision(t *testing.T) {
	if _, err := ParseQuantity("1.1234567890123456789012345678"); err == nil {
		t.Fatal("ParseQuantity accepted excessive precision")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1354
//	test: test_from_decimal_negative_quantity_errors
func TestQuantityFromNegativeDecimalErrors(t *testing.T) {
	if _, err := ParseQuantity("-123.45"); err == nil {
		t.Fatal("ParseQuantity accepted negative decimal")
	}
	if _, err := NewQuantity("-123.45", 2); err == nil {
		t.Fatal("NewQuantity accepted negative decimal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1366
//	test: test_from_decimal_dp_negative_returns_typed_error_with_stable_display
func TestQuantityNegativeDecimalStableError(t *testing.T) {
	_, err := NewQuantity("-1.5", 2)
	if err == nil || err.Error() != `decimal value "-1.50" is negative, Quantity must be non-negative` {
		t.Fatalf("negative quantity error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1382
//	test: test_add
func TestQuantityAdd(t *testing.T) {
	result, ok := MustQuantity("1").Add(MustQuantity("2"))
	if !ok || result.String() != "3" {
		t.Fatalf("1 + 2 = %s, %v", result, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1392
//	test: test_sub
func TestQuantitySub(t *testing.T) {
	result, ok := MustQuantity("3").Sub(MustQuantity("2"))
	if !ok || result.String() != "1" {
		t.Fatalf("3 - 2 = %s, %v", result, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1402
//	test: test_quantity_checked_add_within_bounds
func TestQuantityCheckedAddWithinBounds(t *testing.T) {
	result, ok := MustQuantity("10.00").Add(MustQuantity("5.00"))
	if !ok || !result.Equal(MustQuantity("15.00")) {
		t.Fatalf("sum = %s, %v", result, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1409
//	test: test_quantity_checked_add_above_max_returns_none
func TestQuantityCheckedAddAboveMaxFails(t *testing.T) {
	maximum, _ := MaxQuantity(0)
	if _, ok := maximum.Add(MustQuantity("1")); ok {
		t.Fatal("addition above maximum succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1416
//	test: test_quantity_checked_sub_within_bounds
func TestQuantityCheckedSubWithinBounds(t *testing.T) {
	result, ok := MustQuantity("10.00").Sub(MustQuantity("3.00"))
	if !ok || !result.Equal(MustQuantity("7.00")) {
		t.Fatalf("difference = %s, %v", result, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1423
//	test: test_quantity_checked_sub_underflow_returns_none
func TestQuantityCheckedSubUnderflowFails(t *testing.T) {
	if _, ok := MustQuantity("3.00").Sub(MustQuantity("10.00")); ok {
		t.Fatal("quantity subtraction underflow succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1430
//	test: test_quantity_checked_sub_to_zero
func TestQuantityCheckedSubToZero(t *testing.T) {
	quantity := MustQuantity("5.00")
	result, ok := quantity.Sub(quantity)
	if !ok || !result.Equal(MustQuantity("0.00")) || result.Precision() != 2 {
		t.Fatalf("self subtraction = %s precision %d, %v", result, result.Precision(), ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1456
//	test: test_quantity_checked_arith_uses_max_precision
func TestQuantityCheckedArithmeticUsesMaxPrecision(t *testing.T) {
	left := MustQuantity("10.5")
	right := MustQuantity("2.25")
	sum, sumOK := left.Add(right)
	difference, differenceOK := left.Sub(right)
	if !sumOK || sum.Precision() != 2 || sum.String() != "12.75" {
		t.Fatalf("sum = %s precision %d", sum, sum.Precision())
	}
	if !differenceOK || difference.Precision() != 2 || difference.String() != "8.25" {
		t.Fatalf("difference = %s precision %d", difference, difference.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1469
//	test: test_mul
func TestQuantityMul(t *testing.T) {
	result, ok := MustQuantity("2.0").Mul(MustQuantity("2.0"))
	if !ok || !result.Equal(MustQuantity("4")) {
		t.Fatalf("2.0 * 2.0 = %s, %v", result, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1478
//	test: test_mul_avoids_intermediate_raw_overflow
func TestQuantityMulAvoidsIntermediateOverflow(t *testing.T) {
	left, err := NewQuantity("100000", MaxPrecision)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewQuantity("100", MaxPrecision)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := left.Mul(right)
	if !ok || result.String() != "10000000.0000000000000000" || result.Precision() != MaxPrecision {
		t.Fatalf("product = %s precision %d, ok=%v", result, result.Precision(), ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1500
//	test: test_mul_panics_when_scaled_result_exceeds_quantity_max
//
// Adaptations:
//   - Go checked arithmetic reports failure instead of panicking.
func TestQuantityMulFailsWhenResultExceedsMax(t *testing.T) {
	maximum, _ := MaxQuantity(MaxPrecision)
	if _, ok := maximum.Mul(MustQuantity("2")); ok {
		t.Fatal("overflowing multiplication succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1508
//	test: test_comparisons
func TestQuantityComparisons(t *testing.T) {
	if !MustQuantity("1.0").Equal(MustQuantity("1.00")) {
		t.Fatal("equal numeric quantities with different precision compare unequal")
	}
	if MustQuantity("1.1").Cmp(MustQuantity("1.0")) <= 0 ||
		MustQuantity("0.9").Cmp(MustQuantity("1.0")) >= 0 {
		t.Fatal("quantity ordering invariant failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1523
//	test: test_debug
func TestQuantityDebugFormat(t *testing.T) {
	if got := fmt.Sprintf("%#v", MustQuantity("44.12")); got != "Quantity(44.12)" {
		t.Fatalf("debug format = %s", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1530
//	test: test_display
func TestQuantityDisplay(t *testing.T) {
	if got := MustQuantity("44.12").String(); got != "44.12" {
		t.Fatalf("display = %s", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1566
//	test: test_to_formatted_string
func TestQuantityFormattedString(t *testing.T) {
	quantity := MustQuantity("1234.5678")
	if quantity.FormattedString() != "1_234.5678" || quantity.String() != "1234.5678" {
		t.Fatalf("formatted = %s, raw = %s", quantity.FormattedString(), quantity)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1574
//	test: test_saturating_sub
func TestQuantitySaturatingSub(t *testing.T) {
	quantity := MustQuantity("100.00")
	if got := quantity.SaturatingSub(MustQuantity("50.00")); !got.Equal(MustQuantity("50.00")) {
		t.Fatalf("100 - 50 = %s", got)
	}
	got := quantity.SaturatingSub(MustQuantity("150.00"))
	if !got.IsZero() || got.Precision() != 2 {
		t.Fatalf("saturated result = %s precision %d", got, got.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1588
//	test: test_saturating_sub_overflow_bug
func TestQuantitySaturatingSubUnderflowRegression(t *testing.T) {
	peak := MustQuantity("0.079")
	order := MustQuantity("0.080")
	result := peak.SaturatingSub(order)
	if !result.IsZero() || result.Precision() != 3 {
		t.Fatalf("saturated result = %s precision %d", result, result.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1637
//	test: test_quantity_serde_json_round_trip
func TestQuantityJSONRoundTrip(t *testing.T) {
	original := MustQuantity("123.456")
	data, err := json.Marshal(original)
	if err != nil || string(data) != `"123.456"` {
		t.Fatalf("Marshal = %s, %v", data, err)
	}
	var decoded Quantity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(original) || decoded.Precision() != 3 {
		t.Fatalf("decoded = %s precision %d", decoded, decoded.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1659
//	test: test_quantity_deserialize_invalid_string_returns_error
func TestQuantityJSONRejectsInvalidString(t *testing.T) {
	var quantity Quantity
	if err := json.Unmarshal([]byte(`"not-a-quantity"`), &quantity); err == nil {
		t.Fatal("invalid quantity was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1669
//	test: test_quantity_deserialize_negative_returns_error
func TestQuantityJSONRejectsNegative(t *testing.T) {
	var quantity Quantity
	err := json.Unmarshal([]byte(`"-1.5"`), &quantity)
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative JSON quantity error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1679
//	test: test_from_mantissa_exponent_exact_precision
func TestQuantityFromMantissaExponentExactPrecision(t *testing.T) {
	assertMantissaQuantity(t, 12345, -2, 2, "123.45")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1685
//	test: test_from_mantissa_exponent_excess_rounds_down
func TestQuantityFromMantissaExponentExcessRoundsDown(t *testing.T) {
	assertMantissaQuantity(t, 12345, -3, 2, "12.34")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1693
//	test: test_from_mantissa_exponent_excess_rounds_up
func TestQuantityFromMantissaExponentExcessRoundsUp(t *testing.T) {
	assertMantissaQuantity(t, 12355, -3, 2, "12.36")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1700
//	test: test_from_mantissa_exponent_positive_exponent
func TestQuantityFromMantissaExponentPositiveExponent(t *testing.T) {
	assertMantissaQuantity(t, 5, 2, 0, "500")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1706
//	test: test_from_mantissa_exponent_zero
func TestQuantityFromMantissaExponentZero(t *testing.T) {
	assertMantissaQuantity(t, 0, 2, 2, "0.00")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1718
//	test: test_from_mantissa_exponent_checked_zero_with_large_exponent
func TestQuantityFromMantissaExponentZeroLargeExponent(t *testing.T) {
	assertMantissaQuantity(t, 0, 119, 2, "0.00")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1724
//	test: test_from_mantissa_exponent_checked_invalid_precision
func TestQuantityFromMantissaExponentRejectsInvalidPrecision(t *testing.T) {
	if _, err := QuantityFromMantissaExponent(1, 0, MaxPrecision+1); err == nil {
		t.Fatal("invalid precision was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1735
//	test: test_from_mantissa_exponent_checked_overflow_returns_error
func TestQuantityFromMantissaExponentOverflowReturnsError(t *testing.T) {
	if _, err := QuantityFromMantissaExponent(^uint64(0), 100, 0); err == nil {
		t.Fatal("overflowing mantissa/exponent was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1757
//	test: test_from_mantissa_exponent_zero_with_large_exponent
func TestQuantityFromMantissaExponentZeroLargeExponentNoPrecision(t *testing.T) {
	assertMantissaQuantity(t, 0, 119, 0, "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1763
//	test: test_from_mantissa_exponent_very_negative_exponent_rounds_to_zero
func TestQuantityFromMantissaExponentVeryNegativeRoundsToZero(t *testing.T) {
	assertMantissaQuantity(t, 12345, -120, 2, "0.00")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1778
//	test: test_decimal_arithmetic_operations
func TestQuantityDecimalArithmetic(t *testing.T) {
	quantity := MustQuantity("100.00")
	if got := quantity.AddDecimal(MustParse("50.25")); !got.Equal(MustParse("150.25")) {
		t.Fatalf("addition = %s", got)
	}
	if got := quantity.SubDecimal(MustParse("30.50")); !got.Equal(MustParse("69.50")) {
		t.Fatalf("subtraction = %s", got)
	}
	if got := quantity.MulDecimal(MustParse("1.5")); !got.Equal(MustParse("150.00")) {
		t.Fatalf("multiplication = %s", got)
	}
	got, err := quantity.QuoDecimal(MustParse("4"))
	if err != nil || !got.Equal(MustParse("25.00")) {
		t.Fatalf("division = %s, %v", got, err)
	}
}

func assertMantissaQuantity(t *testing.T, mantissa uint64, exponent int, precision uint8, expected string) {
	t.Helper()
	quantity, err := QuantityFromMantissaExponent(mantissa, exponent, precision)
	if err != nil {
		t.Fatal(err)
	}
	if quantity.String() != expected {
		t.Fatalf("QuantityFromMantissaExponent(%d, %d, %d) = %s, want %s", mantissa, exponent, precision, quantity, expected)
	}
}
