package decimal

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1548
//	test: test_debug_display_precision_handling
func TestQuantityDebugDisplayPrecisionHandling(t *testing.T) {
	tests := []struct {
		value, debug, display string
		precision             uint8
	}{
		{"44.12", "Quantity(44.12)", "44.12", 2},
		{"1234.567", "Quantity(1234.56700000)", "1234.56700000", 8},
	}
	for _, tt := range tests {
		quantity, err := NewQuantity(tt.value, tt.precision)
		if err != nil {
			t.Fatal(err)
		}
		if got := quantity.GoString(); got != tt.debug {
			t.Errorf("GoString() = %q, want %q", got, tt.debug)
		}
		if got := quantity.String(); got != tt.display {
			t.Errorf("String() = %q, want %q", got, tt.display)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1769
//	test: test_f64_operations
//
// Adaptations:
//   - Floating-point money operands are represented as exact decimals.
func TestQuantityScalarOperations(t *testing.T) {
	quantity := MustQuantity("10.50")
	if got := quantity.AddDecimal(MustParse("1.0")); !got.Equal(MustParse("11.5")) {
		t.Fatalf("addition = %s, want 11.5", got)
	}
	if got := quantity.SubDecimal(MustParse("1.0")); !got.Equal(MustParse("9.5")) {
		t.Fatalf("subtraction = %s, want 9.5", got)
	}
	if got := quantity.MulDecimal(MustParse("2.0")); !got.Equal(MustParse("21.0")) {
		t.Fatalf("multiplication = %s, want 21.0", got)
	}
	got, err := quantity.QuoDecimal(MustParse("2.0"))
	if err != nil || !got.Equal(MustParse("5.25")) {
		t.Fatalf("division = %s, %v; want 5.25", got, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1114
//	test: test_from_i32
func TestQuantityFromInt32(t *testing.T) {
	quantity, err := QuantityFromInt32(100_000)
	if err != nil || quantity.String() != "100000" || quantity.Precision() != 0 {
		t.Fatalf("QuantityFromInt32() = %s precision %d, %v", quantity, quantity.Precision(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1131
//	test: test_from_i64
func TestQuantityFromInt64(t *testing.T) {
	quantity, err := QuantityFromInt64(100_000)
	if err != nil || quantity.String() != "100000" || quantity.Precision() != 0 {
		t.Fatalf("QuantityFromInt64() = %s precision %d, %v", quantity, quantity.Precision(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1712
//	test: test_from_mantissa_exponent_checked_exact_precision
func TestQuantityFromMantissaExponentCheckedExactPrecision(t *testing.T) {
	quantity, err := QuantityFromMantissaExponent(12345, -2, 2)
	if err != nil || !quantity.Decimal().Equal(MustParse("123.45")) {
		t.Fatalf("QuantityFromMantissaExponent() = %s, %v", quantity, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1752
//	test: test_from_mantissa_exponent_large_exponent_panics
func TestQuantityFromMantissaExponentLargeExponentPanics(t *testing.T) {
	requireQuantityPanicContains(t, "exceeds i128 range", func() {
		MustQuantityFromMantissaExponent(1, 119, 0)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1746
//	test: test_from_mantissa_exponent_overflow_panics
func TestQuantityFromMantissaExponentOverflowPanics(t *testing.T) {
	requireQuantityPanicContains(t, "Quantity::from_mantissa_exponent", func() {
		MustQuantityFromMantissaExponent(^uint64(0), 9, 0)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1082
//	test: test_from_raw_checked_returns_typed_error_with_stable_display
func TestQuantityFromRawCheckedReturnsTypedErrorWithStableDisplay(t *testing.T) {
	const want = "`precision` must be 0 when `raw` is QUANTITY_UNDEF"
	_, err := QuantityFromRawChecked(QuantityUndefinedRaw(), 3)
	assertQuantityError(t, err, "predicate_violation", want)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1211
//	test: test_from_str_invalid_input
//
// Adaptations:
//   - The forbidden f64 parse is replaced by the exact string constructor.
func TestQuantityFromStringInvalidInputPanics(t *testing.T) {
	requireQuantityPanicContains(t, "invalid", func() {
		MustQuantity("invalid")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1828
//	test: test_from_u256_invalid_precision_returns_typed_error
func TestQuantityFromU256InvalidPrecisionReturnsTypedError(t *testing.T) {
	_, err := QuantityFromU256(big.NewInt(1), 19)
	var quantityErr *QuantityError
	if !errors.As(err, &quantityErr) || quantityErr.Kind != "predicate_violation" {
		t.Fatalf("error = %#v, want predicate_violation", err)
	}
	if !strings.Contains(quantityErr.Message, "WEI_PRECISION") {
		t.Fatalf("error = %q, want WEI_PRECISION", quantityErr.Message)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1813
//	test: test_from_u256_overflow_returns_typed_error_with_stable_display
func TestQuantityFromU256OverflowReturnsTypedErrorWithStableDisplay(t *testing.T) {
	maxU256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	_, err := QuantityFromU256(maxU256, 0)
	var quantityErr *QuantityError
	if !errors.As(err, &quantityErr) || quantityErr.Kind != "predicate_violation" {
		t.Fatalf("error = %#v, want predicate_violation", err)
	}
	if !strings.Contains(quantityErr.Message, "Amount overflow during scaling to fixed precision") {
		t.Fatalf("error = %q, want scaling overflow", quantityErr.Message)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1843
//	test: test_from_u256_raw_above_max_returns_typed_error
func TestQuantityFromU256RawAboveMaxReturnsTypedError(t *testing.T) {
	amount := new(big.Int).Add(QuantityRawMax(), big.NewInt(1))
	_, err := QuantityFromU256(amount, MaxPrecision)
	var quantityErr *QuantityError
	if !errors.As(err, &quantityErr) || quantityErr.Kind != "predicate_violation" {
		t.Fatalf("error = %#v, want predicate_violation", err)
	}
	if !strings.Contains(quantityErr.Message, "QUANTITY_RAW_MAX") {
		t.Fatalf("error = %q, want QUANTITY_RAW_MAX", quantityErr.Message)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1801
//	test: test_from_u256_real_swap_data
func TestQuantityFromU256RealSwapData(t *testing.T) {
	tests := []struct {
		amount, expected string
	}{
		{"42193532365637161405123", "42193.532365637161405123"},
		{"112633187203033110", "0.112633187203033110"},
	}
	for _, tt := range tests {
		amount, ok := new(big.Int).SetString(tt.amount, 10)
		if !ok {
			t.Fatalf("invalid test amount %q", tt.amount)
		}
		quantity, err := QuantityFromU256(amount, 18)
		if err != nil {
			t.Fatal(err)
		}
		if quantity.Precision() != 18 || quantity.Decimal().String() != tt.expected {
			t.Fatalf("quantity = %s precision %d, want %s", quantity, quantity.Precision(), tt.expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1123
//	test: test_from_u32
func TestQuantityFromUint32(t *testing.T) {
	quantity, err := QuantityFromUint32(5000)
	if err != nil || quantity.String() != "5000" || quantity.Precision() != 0 {
		t.Fatalf("QuantityFromUint32() = %s precision %d, %v", quantity, quantity.Precision(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1140
//	test: test_from_u64
func TestQuantityFromUint64(t *testing.T) {
	quantity, err := QuantityFromUint64(100_000)
	if err != nil || quantity.String() != "100000" || quantity.Precision() != 0 {
		t.Fatalf("QuantityFromUint64() = %s precision %d, %v", quantity, quantity.Precision(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1606
//	test: test_hash
func TestQuantityHash(t *testing.T) {
	first := MustQuantity("100.0")
	equal := MustQuantity("100.0")
	different := MustQuantity("200.0")
	if first.Hash64() != equal.Hash64() {
		t.Fatal("equal quantities have different hashes")
	}
	if first.Hash64() == different.Hash64() {
		t.Fatal("different quantities have equal hashes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:968
//	test: test_invalid_precision_from_raw
func TestQuantityFromRawInvalidPrecisionPanics(t *testing.T) {
	requireQuantityPanicContains(t, "precision", func() {
		MustQuantityFromRaw(big.NewInt(1), MaxPrecision+1)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:952
//	test: test_invalid_precision_new
//
// Adaptations:
//   - Go's checked constructor returns an error; this test applies the source's must semantics.
func TestQuantityNewInvalidStandardPrecisionPanics(t *testing.T) {
	requireQuantityPanicContains(t, "precision", func() {
		quantity, err := NewQuantity("1", 17)
		if err != nil {
			panic(err)
		}
		_ = quantity
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1068
//	test: test_new_checked_returns_typed_error_with_stable_display
func TestQuantityNewCheckedReturnsTypedErrorWithStableDisplay(t *testing.T) {
	const want = "quantity 34028236692094.0000000000000000 outside valid range [0, 34028236692093]"
	_, err := NewQuantity("34028236692094", MaxPrecision)
	assertQuantityError(t, err, "out_of_range", want)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1191
//	test: test_new_from_str
func TestQuantityNewFromString(t *testing.T) {
	quantity, err := NewQuantity("0.00812000", 8)
	if err != nil || quantity.Precision() != 8 ||
		!quantity.Equal(MustQuantity("0.00812000")) ||
		quantity.String() != "0.00812000" {
		t.Fatalf("NewQuantity() = %s precision %d, %v", quantity, quantity.Precision(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1446
//	test: test_quantity_checked_add_at_exact_max_returns_some
func TestQuantityCheckedAddAtExactMaxReturnsValue(t *testing.T) {
	nearMaxRaw := new(big.Int).Sub(QuantityRawMax(), big.NewInt(1))
	nearMax := MustQuantityFromRaw(nearMaxRaw, 0)
	oneRawUnit := MustQuantityFromRaw(big.NewInt(1), 0)
	result, ok := nearMax.Add(oneRawUnit)
	if !ok || result.rawValue().Cmp(QuantityRawMax()) != 0 {
		t.Fatalf("checked add = %s, %v; raw = %s", result, ok, result.rawValue())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1436
//	test: test_quantity_checked_arith_rejects_undef
func TestQuantityCheckedArithmeticRejectsUndefined(t *testing.T) {
	undefined := UndefinedQuantity()
	one := MustQuantity("1")
	if _, ok := undefined.Add(one); ok {
		t.Fatal("undefined + one succeeded")
	}
	if _, ok := one.Add(undefined); ok {
		t.Fatal("one + undefined succeeded")
	}
	if _, ok := undefined.Sub(one); ok {
		t.Fatal("undefined - one succeeded")
	}
	if _, ok := one.Sub(undefined); ok {
		t.Fatal("one - undefined succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1648
//	test: test_quantity_serde_json_from_value_round_trip
func TestQuantityJSONValueRoundTrip(t *testing.T) {
	original := MustQuantity("123.456")
	data, err := json.Marshal(original)
	if err != nil || string(data) != `"123.456"` {
		t.Fatalf("JSON value = %s, %v", data, err)
	}
	var deserialized Quantity
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatal(err)
	}
	if !deserialized.Equal(original) || deserialized.Precision() != 3 {
		t.Fatalf("deserialized = %s precision %d", deserialized, deserialized.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1098
//	test: test_undefined
func TestQuantityUndefined(t *testing.T) {
	quantity := UndefinedQuantity()
	if !quantity.IsUndefined() {
		t.Fatal("undefined quantity reports defined")
	}
	if quantity.rawValue().Cmp(QuantityUndefinedRaw()) != 0 {
		t.Fatalf("raw = %s, want %s", quantity.rawValue(), QuantityUndefinedRaw())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1149
//	test: test_with_maximum_value
func TestQuantityWithMaximumValue(t *testing.T) {
	if _, err := NewQuantity("34028236692093", 0); err != nil {
		t.Fatalf("maximum quantity rejected: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1155
//	test: test_with_minimum_positive_value
func TestQuantityWithMinimumPositiveValue(t *testing.T) {
	quantity, err := NewQuantity("0.000000001", 9)
	if err != nil || quantity.rawValue().Sign() == 0 ||
		!quantity.Decimal().Equal(MustParse("0.000000001")) ||
		quantity.String() != "0.000000001" {
		t.Fatalf("minimum positive = %s, %v", quantity, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1164
//	test: test_with_minimum_value
func TestQuantityWithMinimumValue(t *testing.T) {
	quantity, err := NewQuantity("0", 9)
	if err != nil || quantity.rawValue().Sign() != 0 ||
		!quantity.Decimal().Equal(MustParse("0")) ||
		quantity.String() != "0.000000000" {
		t.Fatalf("minimum quantity = %s, %v", quantity, err)
	}
}

func requireQuantityPanicContains(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected panic containing %q", want)
		}
		if !strings.Contains(toPanicString(recovered), want) {
			t.Fatalf("panic = %v, want substring %q", recovered, want)
		}
	}()
	fn()
}

func assertQuantityError(t *testing.T, err error, kind, message string) {
	t.Helper()
	var quantityErr *QuantityError
	if !errors.As(err, &quantityErr) {
		t.Fatalf("error type = %T, want *QuantityError", err)
	}
	if quantityErr.Kind != kind {
		t.Fatalf("error kind = %q, want %q", quantityErr.Kind, kind)
	}
	if quantityErr.Error() != message {
		t.Fatalf("error = %q, want %q", quantityErr, message)
	}
}
