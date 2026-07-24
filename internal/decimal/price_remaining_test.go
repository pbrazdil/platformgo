package decimal

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func requirePricePanicContains(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(toPanicString(value), want) {
			t.Fatalf("panic = %v, want substring %q", value, want)
		}
	}()
	fn()
}

func toPanicString(value any) string {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:854
//	test: test_extreme_prices_round_trip_through_raw
//
// Adaptations:
//   - The native exact decimal representation replaces exposed fixed-point raw integers.
func TestPriceExtremePricesRoundTripThroughRaw(t *testing.T) {
	maximum, err := MaxPrice(0)
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := MinPrice(0)
	if err != nil {
		t.Fatal(err)
	}
	roundTripMax, err := ParsePrice(maximum.String())
	if err != nil || !roundTripMax.Equal(maximum) {
		t.Fatalf("maximum round trip = %s, %v", roundTripMax, err)
	}
	roundTripMin, err := ParsePrice(minimum.String())
	if err != nil || !roundTripMin.Equal(minimum) {
		t.Fatalf("minimum round trip = %s, %v", roundTripMin, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:869
//	test: test_invalid_precision_new
//
// Adaptations:
//   - Exact string construction replaces the source floating-point constructor.
func TestPriceInvalidPrecisionNewFixedMode(t *testing.T) {
	if _, err := NewPrice("1.0", 50); err == nil {
		t.Fatal("NewPrice accepted precision 50")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:885
//	test: test_invalid_precision_from_raw
//
// Adaptations:
//   - Legacy raw decoding is checked rather than panicking.
func TestPriceInvalidPrecisionFromRaw(t *testing.T) {
	if _, err := DecodeRawPriceI64(1, MaxPrecision+1); err == nil {
		t.Fatal("raw decoding accepted excessive precision")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:957
//	test: test_is_positive_rejects_undefined
func TestPriceIsPositiveRejectsUndefined(t *testing.T) {
	undefined := UndefinedPrice()
	if undefined.IsPositive() {
		t.Fatal("undefined price reported positive")
	}
	err := undefined.RequirePositive("price")
	const want = "invalid `Price` for 'price', was PRICE_UNDEF"
	if err == nil || err.Error() != want {
		t.Fatalf("RequirePositive(undefined) = %v, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:993
//	test: test_new_checked
//
// Adaptations:
//   - Textual non-finite inputs exercise validation without floating-point money.
func TestPriceNewChecked(t *testing.T) {
	if _, err := NewPrice("1.0", MaxPrecision); err != nil {
		t.Fatalf("valid price rejected: %v", err)
	}
	for _, input := range []string{"NaN", "Inf"} {
		if _, err := NewPrice(input, MaxPrecision); err == nil {
			t.Errorf("NewPrice(%q) succeeded", input)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1001
//	test: test_new_checked_returns_typed_error_with_stable_display
//
// Adaptations:
//   - Exact decimal range validation replaces the source f64 boundary check.
func TestPriceNewCheckedReturnsTypedErrorWithStableDisplay(t *testing.T) {
	_, err := NewPrice("17014118346047", MaxPrecision)
	if err == nil {
		t.Fatal("expected error")
	}
	var validation *PriceValidationError
	if !errors.As(err, &validation) || validation.Kind != "out_of_range" {
		t.Fatalf("error type = %T, kind = %q", err, validation.Kind)
	}
	const want = "price 17014118346047.0000000000000000 outside valid range [-17014118346046, 17014118346046]"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1015
//	test: test_from_raw_checked_returns_typed_error_with_stable_display
func TestPriceFromRawCheckedReturnsTypedErrorWithStableDisplay(t *testing.T) {
	_, err := UndefinedPriceChecked(3)
	const want = "`precision` must be 0 when `raw` is PRICE_UNDEF"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1031
//	test: test_from_raw
//
// Adaptations:
//   - DecodeRawPriceI64 accepts the legacy 1e9-scaled wire representation.
func TestPriceFromRaw(t *testing.T) {
	price, err := DecodeRawPriceI64(100_000_000_000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if price.String() != "100.00" || price.Precision() != 2 {
		t.Fatalf("decoded = %s precision %d", price, price.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1039
//	test: test_zero_constructor
func TestPriceZeroConstructor(t *testing.T) {
	zero, err := ZeroPrice(3)
	if err != nil {
		t.Fatal(err)
	}
	if !zero.IsZero() || zero.Precision() != 3 {
		t.Fatalf("zero = %s precision %d", zero, zero.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1046
//	test: test_max_constructor
func TestPriceMaxConstructor(t *testing.T) {
	price, err := MaxPrice(4)
	if err != nil {
		t.Fatal(err)
	}
	if price.Precision() != 4 || !price.Equal(MustPrice("17014118346046")) {
		t.Fatalf("maximum = %s precision %d", price, price.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1053
//	test: test_min_constructor
func TestPriceMinConstructor(t *testing.T) {
	price, err := MinPrice(4)
	if err != nil {
		t.Fatal(err)
	}
	if price.Precision() != 4 || !price.Equal(MustPrice("-17014118346046")) {
		t.Fatalf("minimum = %s precision %d", price, price.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1060
//	test: test_nan_validation
//
// Adaptations:
//   - The textual non-finite representation replaces an f64 input.
func TestPriceNaNValidation(t *testing.T) {
	if _, err := NewPrice("NaN", MaxPrecision); err == nil {
		t.Fatal("NaN was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1065
//	test: test_infinity_validation
//
// Adaptations:
//   - Textual non-finite representations replace f64 inputs.
func TestPriceInfinityValidation(t *testing.T) {
	for _, input := range []string{"Inf", "-Inf"} {
		if _, err := NewPrice(input, MaxPrecision); err == nil {
			t.Errorf("%s was accepted", input)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1071
//	test: test_special_values
func TestPriceSpecialValues(t *testing.T) {
	zero, err := ZeroPrice(5)
	if err != nil {
		t.Fatal(err)
	}
	if !zero.IsZero() || zero.String() != "0.00000" {
		t.Fatalf("zero = %s", zero)
	}
	if !UndefinedPrice().IsUndefined() {
		t.Fatal("undefined sentinel not recognized")
	}
	if ErrorPrice().Precision() != 255 {
		t.Fatalf("error sentinel precision = %d", ErrorPrice().Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1142
//	test: test_from_decimal_dp_preservation
func TestPriceFromDecimalDPPreservation(t *testing.T) {
	price, err := NewPrice("123.456789", 6)
	if err != nil {
		t.Fatal(err)
	}
	if price.Precision() != 6 || !price.Decimal().Equal(MustParse("123.456789")) {
		t.Fatalf("price = %s precision %d", price, price.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1286
//	test: test_string_formatting_precision_handling
//
// Adaptations:
//   - Exact decimal strings replace source f64 inputs.
func TestPriceStringFormattingPrecisionHandling(t *testing.T) {
	for _, test := range []struct {
		input, debug, display string
		precision             uint8
	}{
		{"1234.5678", "Price(1234.5678)", "1234.5678", 4},
		{"123.456789012345", "Price(123.45678901)", "123.45678901", 8},
	} {
		price, err := NewPrice(test.input, test.precision)
		if err != nil {
			t.Fatal(err)
		}
		if price.GoString() != test.debug || price.String() != test.display ||
			strings.ReplaceAll(price.FormattedString(), "_", "") != test.display {
			t.Errorf("%s formatted as %#v, %s, %s", test.input, price, price, price.FormattedString())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1307
//	test: test_decimal_conversions
func TestPriceDecimalConversions(t *testing.T) {
	for _, input := range []string{"123.456", "0.000001"} {
		price := MustPrice(input)
		if !price.Decimal().Equal(MustParse(input)) {
			t.Errorf("%s converted to %s", price, price.Decimal())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1366
//	test: test_price_checked_add_rejects_sentinel_undef
func TestPriceCheckedAddRejectsSentinelUndef(t *testing.T) {
	undefined, one := UndefinedPrice(), MustPrice("1")
	if _, ok := undefined.Add(one); ok {
		t.Fatal("undefined + one succeeded")
	}
	if _, ok := one.Add(undefined); ok {
		t.Fatal("one + undefined succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1374
//	test: test_price_checked_sub_rejects_sentinel_undef
func TestPriceCheckedSubRejectsSentinelUndef(t *testing.T) {
	if _, ok := UndefinedPrice().Sub(MustPrice("-1")); ok {
		t.Fatal("undefined - negative one succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1381
//	test: test_price_checked_arith_rejects_error_price
func TestPriceCheckedArithmeticRejectsErrorPrice(t *testing.T) {
	one := MustPrice("1")
	if _, ok := ErrorPrice().Add(one); ok {
		t.Fatal("error + one succeeded")
	}
	if _, ok := one.Sub(ErrorPrice()); ok {
		t.Fatal("one - error succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1388
//	test: test_price_checked_arith_rejects_raw_error
func TestPriceCheckedArithmeticRejectsRawError(t *testing.T) {
	one, rawError := MustPrice("1"), RawErrorPrice()
	for name, ok := range map[string]bool{
		"error + one": func() bool { _, ok := rawError.Add(one); return ok }(),
		"one + error": func() bool { _, ok := one.Add(rawError); return ok }(),
		"error - one": func() bool { _, ok := rawError.Sub(one); return ok }(),
		"one - error": func() bool { _, ok := one.Sub(rawError); return ok }(),
	} {
		if ok {
			t.Errorf("%s succeeded", name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1398
//	test: test_price_checked_add_at_exact_max_returns_some
func TestPriceCheckedAddAtExactMaxReturnsSome(t *testing.T) {
	nearMax := MustPrice("17014118346045.9999999999999999")
	unit := MustPrice("0.0000000000000001")
	got, ok := nearMax.Add(unit)
	maximum, _ := MaxPrice(0)
	if !ok || !got.Equal(maximum) {
		t.Fatalf("sum = %s, %v", got, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1408
//	test: test_price_checked_sub_at_exact_min_returns_some
func TestPriceCheckedSubAtExactMinReturnsSome(t *testing.T) {
	nearMin := MustPrice("-17014118346045.9999999999999999")
	unit := MustPrice("0.0000000000000001")
	got, ok := nearMin.Sub(unit)
	minimum, _ := MinPrice(0)
	if !ok || !got.Equal(minimum) {
		t.Fatalf("difference = %s, %v", got, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1436
//	test: test_f64_operations
//
// Adaptations:
//   - Exact Decimal operands replace floating-point arithmetic.
func TestPriceF64Operations(t *testing.T) {
	price := MustPrice("10.50")
	if got := price.AddDecimal(MustParse("1.0")); !got.Equal(MustParse("11.50")) {
		t.Errorf("addition = %s", got)
	}
	if got := price.SubDecimal(MustParse("1.0")); !got.Equal(MustParse("9.50")) {
		t.Errorf("subtraction = %s", got)
	}
	if got := price.MulDecimal(MustParse("2.0")); !got.Equal(MustParse("21.000")) {
		t.Errorf("multiplication = %s", got)
	}
	got, err := price.QuoDecimal(MustParse("2.0"))
	if err != nil || !got.Equal(MustParse("5.25")) {
		t.Errorf("division = %s, %v", got, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1470
//	test: test_deref
//
// Adaptations:
//   - Decimal exposes the exact native value instead of a fixed raw integer.
func TestPriceDeref(t *testing.T) {
	price := MustPrice("10.0")
	if !price.Decimal().Equal(MustParse("10.0")) {
		t.Fatalf("Decimal() = %s", price.Decimal())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1476
//	test: test_decode_raw_price_i64
func TestDecodeRawPriceI64(t *testing.T) {
	price, err := DecodeRawPriceI64(42_000_000_000, MaxPrecision)
	if err != nil {
		t.Fatal(err)
	}
	if !price.Equal(MustPrice("42.0")) {
		t.Fatalf("decoded = %s", price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1489
//	test: test_hash
func TestPriceHash(t *testing.T) {
	first, equal, different := MustPrice("1.00"), MustPrice("1.00"), MustPrice("1.10")
	if first.Hash64() != equal.Hash64() {
		t.Fatal("equal prices have different hashes")
	}
	if first.Hash64() == different.Hash64() {
		t.Fatal("different prices have equal hashes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1520
//	test: test_price_serde_json_from_value_round_trip
func TestPriceSerdeJSONFromValueRoundTrip(t *testing.T) {
	source, err := NewPrice("1.0500", 4)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	transcoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Price
	if err := json.Unmarshal(transcoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(source) || decoded.Precision() != 4 {
		t.Fatalf("decoded = %s precision %d", decoded, decoded.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1584
//	test: test_from_mantissa_exponent_checked_exact_precision
func TestPriceFromMantissaExponentCheckedExactPrecision(t *testing.T) {
	price, err := PriceFromMantissaExponent(12345, -2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !price.Decimal().Equal(MustParse("123.45")) {
		t.Fatalf("decimal = %s", price.Decimal())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1618
//	test: test_from_mantissa_exponent_overflow_panics
func TestPriceFromMantissaExponentOverflowPanics(t *testing.T) {
	requirePricePanicContains(t, "Price::from_mantissa_exponent", func() {
		MustPriceFromMantissaExponent(9223372036854775807, 9, 0)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1624
//	test: test_from_mantissa_exponent_large_exponent_panics
func TestPriceFromMantissaExponentLargeExponentPanics(t *testing.T) {
	requirePricePanicContains(t, "exceeds i128 range", func() {
		MustPriceFromMantissaExponent(1, 119, 0)
	})
}
