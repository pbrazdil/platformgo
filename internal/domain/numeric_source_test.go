package domain

import (
	"errors"
	"math/big"
	"testing"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

// TestEconomicPriceStringParsing is ported from:
//
//	repository: nautechsystems/nautilus_trader@116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1084
//	test: test_string_parsing
//
// Adaptations:
//   - Price precision is bound to an immutable instrument revision rather than inferred into a mutable field.
//   - Exact decimal equality replaces the source float-backed representation.
//
// Assertions preserved:
//   - Plain decimal text parses to the exact price value.
//   - The price carries precision three.
func TestEconomicPriceStringParsing(t *testing.T) {
	instrument := mustInstrument(t, "BTC-USD.HYPERLIQUID", 1, 3, 8)
	price, err := NewPrice("123.456", instrument)
	if err != nil {
		t.Fatal(err)
	}
	if got := price.Decimal().String(); got != "123.456" {
		t.Fatalf("price = %s, want 123.456", got)
	}
	if got := price.Scale(); got != 3 {
		t.Fatalf("price scale = %d, want 3", got)
	}
}

// TestEconomicPriceStringParsingErrors is ported from:
//
//	repository: nautechsystems/nautilus_trader@116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1098
//	test: test_string_parsing_errors
//
// Adaptations:
//   - The production constructor returns a typed strict-parser error.
//
// Assertions preserved:
//   - Invalid price text returns an error.
func TestEconomicPriceStringParsingErrors(t *testing.T) {
	instrument := mustInstrument(t, "BTC-USD.HYPERLIQUID", 1, 3, 8)
	if _, err := NewPrice("invalid", instrument); !errors.Is(err, decimal.ErrInvalidSyntax) {
		t.Fatalf("NewPrice(invalid) error = %v, want ErrInvalidSyntax", err)
	}
}

// TestEconomicPriceTrailingZeros is ported from:
//
//	repository: nautechsystems/nautilus_trader@116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1198
//	test: test_from_decimal_trailing_zeros
//
// Adaptations:
//   - Canonical decimals remove insignificant zeros under the production decimal policy.
//   - Required price precision is explicit immutable instrument metadata, not lexical decimal state.
//
// Assertions preserved:
//   - The price value is exactly 1.23.
//   - The first price carries precision three even though the canonical numeric value has scale two.
//   - The normalized price carries precision two.
func TestEconomicPriceTrailingZeros(t *testing.T) {
	threePlaceInstrument := mustInstrument(t, "BTC-USD.HYPERLIQUID", 1, 3, 8)
	price, err := NewPrice("1.230", threePlaceInstrument)
	if err != nil {
		t.Fatal(err)
	}
	if got := price.Decimal().String(); got != "1.23" {
		t.Fatalf("price = %s, want 1.23", got)
	}
	if got := price.Scale(); got != 3 {
		t.Fatalf("price scale = %d, want 3", got)
	}

	twoPlaceInstrument := mustInstrument(t, "BTC-USD.HYPERLIQUID", 2, 2, 8)
	normalized, err := NewPrice(price.Decimal().String(), twoPlaceInstrument)
	if err != nil {
		t.Fatal(err)
	}
	if got := normalized.Decimal().String(); got != "1.23" {
		t.Fatalf("normalized price = %s, want 1.23", got)
	}
	if got := normalized.Scale(); got != 2 {
		t.Fatalf("normalized price scale = %d, want 2", got)
	}
}

// TestEconomicBankersRoundMatchesDecimal is ported from:
//
//	repository: nautechsystems/nautilus_trader@116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1744
//	test: test_bankers_round_matches_decimal
//
// Adaptations:
//   - The source Decimal cross-check becomes direct assertions against the production exact decimal.
//   - Fixed-scale strings are compared as exact values because canonical output removes trailing zeros.
//
// Assertions preserved:
//   - Every source half-even rounding vector produces the same exact decimal.
func TestEconomicBankersRoundMatchesDecimal(t *testing.T) {
	tests := []struct {
		input string
		scale uint8
		want  string
	}{
		{"1.005", 2, "1"},
		{"1.015", 2, "1.02"},
		{"1.025", 2, "1.02"},
		{"1.035", 2, "1.04"},
		{"1.045", 2, "1.04"},
		{"2.5", 0, "2"},
		{"3.5", 0, "4"},
		{"-2.5", 0, "-2"},
		{"-3.5", 0, "-4"},
		{"123.456", 2, "123.46"},
		{"123.455", 2, "123.46"},
		{"123.445", 2, "123.44"},
	}
	for _, testCase := range tests {
		value, err := decimal.Parse(testCase.input)
		if err != nil {
			t.Fatal(err)
		}
		rounded, err := value.Quantize(
			testCase.scale,
			decimal.RoundHalfEven,
			"source.price",
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := rounded.String(); got != testCase.want {
			t.Fatalf(
				"Quantize(%s, %d) = %s, want %s",
				testCase.input,
				testCase.scale,
				got,
				testCase.want,
			)
		}
	}
}

// TestEconomicPriceScaledOverflowReturnsTypedError is ported from:
//
//	repository: nautechsystems/nautilus_trader@116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1607
//	test: test_from_mantissa_exponent_checked_overflow_returns_error
//
// Adaptations:
//   - Arbitrary-precision coefficient construction replaces the source i64 exponent helper.
//   - A stable typed precision error replaces matching an implementation-specific error string.
//
// Assertions preserved:
//   - A mantissa scaled far outside the supported price range returns an overflow error.
func TestEconomicPriceScaledOverflowReturnsTypedError(t *testing.T) {
	coefficient := new(big.Int).Mul(
		big.NewInt(9223372036854775807),
		new(big.Int).Exp(big.NewInt(10), big.NewInt(100), nil),
	)
	instrument := mustInstrument(t, "BTC-USD.HYPERLIQUID", 1, 0, 8)
	if _, err := NewPriceScaled(coefficient, 0, instrument); !errors.Is(err, decimal.ErrPrecision) {
		t.Fatalf("NewPriceScaled(overflow) error = %v, want ErrPrecision", err)
	}
}
