package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
)

func registry() *currency.Registry {
	return currency.NewRegistry(
		currency.USD(),
		currency.AUD(),
		currency.BTC(),
		currency.USDT(),
		currency.MustNew("EUR", 2, 978, "Euro", currency.Fiat),
		currency.MustNew("GBP", 2, 826, "Pound sterling", currency.Fiat),
		currency.MustNew("JPY", 0, 392, "Japanese yen", currency.Fiat),
	)
}

func requireDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	expected := decimal.MustParse(want)
	if !got.Equal(expected) {
		t.Fatalf("decimal = %s, want %s", got, expected)
	}
}

func requireMoney(t *testing.T, got Money, want string) {
	t.Helper()
	expected := MustParse(want, registry())
	if !got.Equal(expected) {
		t.Fatalf("money = %s (raw %s), want %s (raw %s)", got, got.Raw(), expected, expected.Raw())
	}
}

func requirePanicContains(t *testing.T, want string, action func()) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(fmt.Sprint(value), want) {
			t.Fatalf("panic = %v, want substring %q", value, want)
		}
	}()
	action()
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:767
//	test: test_extreme_money_round_trips_through_raw
func TestExtremeMoneyRoundTripsThroughRaw(t *testing.T) {
	max := MustNew("17014118346046", currency.USD())
	min := MustNew("-17014118346046", currency.USD())
	if max.Raw().Cmp(MaxRaw()) != 0 || min.Raw().Cmp(MinRaw()) != 0 {
		t.Fatalf("extreme raw values = %s, %s", max.Raw(), min.Raw())
	}
	if _, err := FromRawChecked(max.Raw(), currency.USD()); err != nil {
		t.Fatalf("maximum raw: %v", err)
	}
	if _, err := FromRawChecked(min.Raw(), currency.USD()); err != nil {
		t.Fatalf("minimum raw: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:780
//	test: test_debug
func TestMoneyDebug(t *testing.T) {
	if got := MustNew("1010.12", currency.USD()).DebugString(); got != "Money(1010.12, USD)" {
		t.Fatalf("DebugString() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:788
//	test: test_display
func TestMoneyDisplay(t *testing.T) {
	if got := MustNew("1010.12", currency.USD()).String(); got != "1010.12 USD" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:798
//	test: test_formatting_normal_precision
func TestMoneyFormattingNormalPrecision(t *testing.T) {
	tests := []struct {
		amount, debug, display string
		denomination           currency.Currency
	}{
		{"1010.12", "Money(1010.12, USD)", "1010.12 USD", currency.USD()},
		{"123.456789", "Money(123.45678900, BTC)", "123.45678900 BTC", currency.BTC()},
	}
	for _, test := range tests {
		value := MustNew(test.amount, test.denomination)
		if value.DebugString() != test.debug || value.String() != test.display {
			t.Fatalf("%s: debug=%q display=%q", test.amount, value.DebugString(), value.String())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:835
//	test: test_formatting_high_precision
func TestMoneyFormattingHighPrecision(t *testing.T) {
	tests := []struct {
		raw, code, debug, display string
	}{
		{"1000000000000000000", "wei", "Money(1000000000000000000, wei)", "1000000000000000000 wei"},
		{"2500000000000000000", "ETH", "Money(2500000000000000000, ETH)", "2500000000000000000 ETH"},
	}
	for _, test := range tests {
		denomination := currency.Currency{Code: test.code, Precision: 18, Name: test.code, Type: currency.Crypto}
		raw, _ := new(big.Int).SetString(test.raw, 10)
		value := MustFromRaw(raw, denomination)
		if value.DebugString() != test.debug || value.String() != test.display {
			t.Fatalf("%s: debug=%q display=%q", test.code, value.DebugString(), value.String())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:857
//	test: test_zero_constructor
func TestMoneyZeroConstructor(t *testing.T) {
	value := Zero(currency.USD())
	if !value.IsZero() || !value.Currency().Equal(currency.USD()) {
		t.Fatalf("zero = %s, currency = %+v", value, value.Currency())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:866
//	test: test_money_different_currency_addition
func TestMoneyDifferentCurrencyAddition(t *testing.T) {
	requirePanicContains(t, "Currency mismatch", func() {
		MustNew("1000", currency.USD()).Add(MustNew("1", currency.BTC()))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:873
//	test: test_with_maximum_value
func TestMoneyWithMaximumValue(t *testing.T) {
	if _, err := New("17014118346046", currency.USD()); err != nil {
		t.Fatalf("maximum rejected: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:879
//	test: test_with_minimum_value
func TestMoneyWithMinimumValue(t *testing.T) {
	if _, err := New("-17014118346046", currency.USD()); err != nil {
		t.Fatalf("minimum rejected: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:885
//	test: test_new_checked_returns_typed_error_with_stable_display
func TestMoneyNewReturnsTypedRangeErrorWithStableDisplay(t *testing.T) {
	_, err := New("17014118346047", currency.USD())
	var typed OutOfRangeError
	if !errors.As(err, &typed) {
		t.Fatalf("error = %T %v, want OutOfRangeError", err, err)
	}
	want := "invalid decimal for 'amount' not in range [-17014118346046, 17014118346046], was 17014118346047.00"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:900
//	test: test_new_checked_invalid_currency_precision_returns_error
func TestMoneyNewRejectsInvalidCurrencyPrecision(t *testing.T) {
	invalid := currency.Currency{Code: "USD", Precision: 17, ISO4217: 840, Name: "United States dollar", Type: currency.Fiat}
	_, err := New("1", invalid)
	if err == nil || !strings.Contains(err.Error(), "`precision` exceeded maximum `FIXED_PRECISION`") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:914
//	test: test_money_is_zero
func TestMoneyIsZero(t *testing.T) {
	zero := MustNew("0", currency.USD())
	if !zero.IsZero() || !zero.Equal(MustParse("0.0 USD", registry())) {
		t.Fatal("zero was not recognized")
	}
	if MustNew("100", currency.USD()).IsZero() {
		t.Fatal("non-zero was recognized as zero")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:924
//	test: test_money_is_positive
func TestMoneyIsPositive(t *testing.T) {
	if !MustNew("100", currency.USD()).IsPositive() ||
		MustNew("0", currency.USD()).IsPositive() ||
		MustNew("-100", currency.USD()).IsPositive() {
		t.Fatal("unexpected positivity result")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:932
//	test: test_money_comparisons
func TestMoneyComparisons(t *testing.T) {
	first := MustNew("100", currency.USD())
	second := MustNew("200", currency.USD())
	equal := MustNew("100", currency.USD())
	if first.Cmp(second) >= 0 || second.Cmp(first) <= 0 || first.Cmp(equal) != 0 {
		t.Fatal("comparison ordering is inconsistent")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:948
//	test: test_add
func TestMoneyAdd(t *testing.T) {
	got := MustNew("1000", currency.USD()).Add(MustNew("500", currency.USD()))
	requireMoney(t, got, "1500 USD")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:958
//	test: test_sub
func TestMoneySub(t *testing.T) {
	got := MustNew("1000", currency.USD()).Sub(MustNew("250", currency.USD()))
	requireMoney(t, got, "750 USD")
	if !got.Currency().Equal(currency.USD()) {
		t.Fatal("subtraction changed currency")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:968
//	test: test_money_checked_add_within_bounds
func TestMoneyCheckedAddWithinBounds(t *testing.T) {
	got, ok := MustNew("100", currency.USD()).CheckedAdd(MustNew("50", currency.USD()))
	if !ok {
		t.Fatal("checked addition rejected in-range result")
	}
	requireMoney(t, got, "150 USD")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:976
//	test: test_money_checked_add_above_max_returns_none
func TestMoneyCheckedAddAboveMaxReturnsNone(t *testing.T) {
	_, ok := MustFromRaw(MaxRaw(), currency.USD()).CheckedAdd(MustNew("1", currency.USD()))
	if ok {
		t.Fatal("checked addition accepted result above maximum")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:984
//	test: test_money_checked_sub_within_bounds
func TestMoneyCheckedSubWithinBounds(t *testing.T) {
	got, ok := MustNew("100", currency.USD()).CheckedSub(MustNew("40", currency.USD()))
	if !ok {
		t.Fatal("checked subtraction rejected in-range result")
	}
	requireMoney(t, got, "60 USD")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:992
//	test: test_money_checked_sub_below_min_returns_none
func TestMoneyCheckedSubBelowMinReturnsNone(t *testing.T) {
	_, ok := MustFromRaw(MinRaw(), currency.USD()).CheckedSub(MustNew("1", currency.USD()))
	if ok {
		t.Fatal("checked subtraction accepted result below minimum")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1001
//	test: test_money_checked_add_currency_mismatch_panics
func TestMoneyCheckedAddCurrencyMismatchPanics(t *testing.T) {
	requirePanicContains(t, "Currency mismatch", func() {
		MustNew("100", currency.USD()).CheckedAdd(MustNew("50", currency.AUD()))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1009
//	test: test_money_checked_sub_currency_mismatch_panics
func TestMoneyCheckedSubCurrencyMismatchPanics(t *testing.T) {
	requirePanicContains(t, "Currency mismatch", func() {
		MustNew("100", currency.USD()).CheckedSub(MustNew("50", currency.AUD()))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1016
//	test: test_money_checked_add_at_exact_max_returns_some
func TestMoneyCheckedAddAtExactMaxReturnsSome(t *testing.T) {
	near := new(big.Int).Sub(MaxRaw(), big.NewInt(1))
	got, ok := MustFromRaw(near, currency.USD()).CheckedAdd(MustFromRaw(big.NewInt(1), currency.USD()))
	if !ok || got.Raw().Cmp(MaxRaw()) != 0 {
		t.Fatalf("got raw %s, ok=%v", got.Raw(), ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1027
//	test: test_money_checked_sub_at_exact_min_returns_some
func TestMoneyCheckedSubAtExactMinReturnsSome(t *testing.T) {
	near := new(big.Int).Add(MinRaw(), big.NewInt(1))
	got, ok := MustFromRaw(near, currency.USD()).CheckedSub(MustFromRaw(big.NewInt(1), currency.USD()))
	if !ok || got.Raw().Cmp(MinRaw()) != 0 {
		t.Fatalf("got raw %s, ok=%v", got.Raw(), ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1038
//	test: test_money_negation
func TestMoneyNegation(t *testing.T) {
	got := MustNew("100", currency.USD()).Neg()
	requireMoney(t, got, "-100 USD")
	if !got.Currency().Equal(currency.USD()) {
		t.Fatal("negation changed currency")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1046
//	test: test_money_addition_decimal
func TestMoneyAdditionDecimal(t *testing.T) {
	requireDecimal(t, MustNew("100", currency.USD()).AddDecimal(decimal.MustParse("50.25")), "150.25")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1053
//	test: test_money_subtraction_decimal
func TestMoneySubtractionDecimal(t *testing.T) {
	requireDecimal(t, MustNew("100", currency.USD()).SubDecimal(decimal.MustParse("30.50")), "69.50")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1060
//	test: test_money_multiplication_decimal
func TestMoneyMultiplicationDecimal(t *testing.T) {
	requireDecimal(t, MustNew("100", currency.USD()).MulDecimal(decimal.MustParse("1.5")), "150.00")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1067
//	test: test_money_division_decimal
func TestMoneyDivisionDecimal(t *testing.T) {
	got, err := MustNew("100", currency.USD()).DivDecimal(decimal.MustParse("4"))
	if err != nil {
		t.Fatalf("DivDecimal: %v", err)
	}
	requireDecimal(t, got, "25.00")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1074
//	test: test_money_addition_f64
//
// Adaptations:
//   - f64 money arithmetic becomes exact Decimal arithmetic.
func TestMoneyAdditionF64(t *testing.T) {
	requireDecimal(t, MustNew("100", currency.USD()).AddDecimal(decimal.MustParse("50.25")), "150.25")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1081
//	test: test_money_subtraction_f64
//
// Adaptations:
//   - f64 money arithmetic becomes exact Decimal arithmetic.
func TestMoneySubtractionF64(t *testing.T) {
	requireDecimal(t, MustNew("100", currency.USD()).SubDecimal(decimal.MustParse("30.50")), "69.50")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1088
//	test: test_money_multiplication_f64
//
// Adaptations:
//   - f64 money arithmetic becomes exact Decimal arithmetic.
func TestMoneyMultiplicationF64(t *testing.T) {
	requireDecimal(t, MustNew("100", currency.USD()).MulDecimal(decimal.MustParse("1.5")), "150.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1095
//	test: test_money_division_f64
//
// Adaptations:
//   - f64 money arithmetic becomes exact Decimal arithmetic.
func TestMoneyDivisionF64(t *testing.T) {
	got, err := MustNew("100", currency.USD()).DivDecimal(decimal.MustParse("4.0"))
	if err != nil {
		t.Fatal(err)
	}
	requireDecimal(t, got, "25.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1102
//	test: test_money_new_usd
func TestMoneyNewUSD(t *testing.T) {
	value := MustNew("1000", currency.USD())
	if value.Currency().Code != "USD" || value.Currency().Precision != 2 ||
		value.String() != "1000.00 USD" || value.FormattedString() != "1_000.00 USD" {
		t.Fatalf("money = %s, formatted = %s", value, value.FormattedString())
	}
	requireDecimal(t, value.Decimal(), "1000.00")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1113
//	test: test_money_new_btc
func TestMoneyNewBTC(t *testing.T) {
	value := MustNew("10.3", currency.BTC())
	if value.Currency().Code != "BTC" || value.Currency().Precision != 8 ||
		value.String() != "10.30000000 BTC" || value.FormattedString() != "10.30000000 BTC" {
		t.Fatalf("money = %s, formatted = %s", value, value.FormattedString())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1122
//	test: test_to_formatted_string_preserves_digits_beyond_f64_precision
func TestFormattedStringPreservesDigitsBeyondF64Precision(t *testing.T) {
	denomination := currency.MustNew("TST9", 9, 0, "Test 9dp", currency.Crypto)
	value, err := FromDecimal(decimal.MustParse("1234567890.123456789"), denomination)
	if err != nil {
		t.Fatal(err)
	}
	if got := value.FormattedString(); got != "1_234_567_890.123456789 TST9" {
		t.Fatalf("FormattedString() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1139
//	test: test_from_str_invalid_input
func TestMoneyFromStringInvalidInput(t *testing.T) {
	for _, input := range []string{"0USD", "0x00 USD", "0 US", "0 USD USD"} {
		t.Run(input, func(t *testing.T) {
			requirePanicContains(t, "Condition failed", func() { MustParse(input, registry()) })
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1148
//	test: test_from_str_valid_input
func TestMoneyFromStringValidInput(t *testing.T) {
	tests := []struct{ input, amount, code string }{
		{"0 USD", "0.00", "USD"},
		{"1.1 AUD", "1.10", "AUD"},
		{"1.12345678 BTC", "1.12345678", "BTC"},
		{"10_000.10 USD", "10000.10", "USD"},
	}
	for _, test := range tests {
		value := MustParse(test.input, registry())
		if value.Currency().Code != test.code {
			t.Fatalf("%s currency = %s", test.input, value.Currency().Code)
		}
		requireDecimal(t, value.Decimal(), test.amount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1159
//	test: test_money_from_str_negative
func TestMoneyFromStringNegative(t *testing.T) {
	value := MustParse("-123.45 USD", registry())
	requireDecimal(t, value.Decimal(), "-123.45")
	if !value.Currency().Equal(currency.USD()) {
		t.Fatal("currency mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1170
//	test: test_from_str_scientific_notation
func TestMoneyFromStringScientificNotation(t *testing.T) {
	tests := []struct{ input, want string }{
		{"1e7 USD", "10000000.00"},
		{"2.5e3 EUR", "2500.00"},
		{"1.234e-2 GBP", "0.01"},
		{"5E-3 JPY", "0"},
	}
	for _, test := range tests {
		value, err := Parse(test.input, registry())
		if err != nil {
			t.Fatalf("%s: %v", test.input, err)
		}
		requireDecimal(t, value.Decimal(), test.want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1184
//	test: test_from_str_with_underscores
func TestMoneyFromStringWithUnderscores(t *testing.T) {
	tests := []struct{ input, want string }{
		{"1_234.56 USD", "1234.56"},
		{"1_000_000 EUR", "1000000.00"},
		{"99_999.99 GBP", "99999.99"},
	}
	for _, test := range tests {
		value, err := Parse(test.input, registry())
		if err != nil {
			t.Fatalf("%s: %v", test.input, err)
		}
		requireDecimal(t, value.Decimal(), test.want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1195
//	test: test_from_decimal_precision_preservation
func TestMoneyFromDecimalPrecisionPreservation(t *testing.T) {
	value, err := FromDecimal(decimal.MustParse("123.45"), currency.USD())
	if err != nil {
		t.Fatal(err)
	}
	if value.Currency().Precision != 2 || value.Raw().String() != "1234500000000000000" {
		t.Fatalf("money precision=%d raw=%s", value.Currency().Precision, value.Raw())
	}
	requireDecimal(t, value.Decimal(), "123.45")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1209
//	test: test_from_decimal_rounding
func TestMoneyFromDecimalRounding(t *testing.T) {
	first, _ := FromDecimal(decimal.MustParse("1.005"), currency.USD())
	second, _ := FromDecimal(decimal.MustParse("1.015"), currency.USD())
	requireDecimal(t, first.Decimal(), "1.00")
	requireDecimal(t, second.Decimal(), "1.02")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1223
//	test: test_money_hash
func TestMoneyHash(t *testing.T) {
	hashValue := func(value Money) uint64 {
		h := fnv.New64a()
		value.WriteHash(h)
		return h.Sum64()
	}
	first := MustNew("100", currency.USD())
	second := MustNew("100", currency.USD())
	third := MustNew("100", currency.AUD())
	if hashValue(first) != hashValue(second) || hashValue(first) == hashValue(third) {
		t.Fatal("money hashing does not preserve identity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1254
//	test: test_money_serialization_deserialization
func TestMoneySerializationDeserialization(t *testing.T) {
	value := MustNew("123.45", currency.USD())
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FromJSON(data, registry())
	if err != nil || !got.Equal(value) {
		t.Fatalf("round trip = %s, %v", got, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1262
//	test: test_money_deserialize_from_owned_value
func TestMoneyDeserializeFromOwnedValue(t *testing.T) {
	value := MustNew("123.45", currency.USD())
	data, _ := json.Marshal(value)
	var owned json.RawMessage
	if err := json.Unmarshal(data, &owned); err != nil {
		t.Fatal(err)
	}
	got, err := FromJSON(owned, registry())
	if err != nil || !got.Equal(value) {
		t.Fatalf("round trip = %s, %v", got, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1271
//	test: test_money_deserialize_invalid_format_returns_error
func TestMoneyDeserializeInvalidFormatReturnsError(t *testing.T) {
	_, err := FromJSON([]byte(`"100.00"`), registry())
	if err == nil || !strings.Contains(err.Error(), "Expected '<amount> <currency>'") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1281
//	test: test_money_deserialize_unknown_currency_returns_error
func TestMoneyDeserializeUnknownCurrencyReturnsError(t *testing.T) {
	_, err := FromJSON([]byte(`"100.00 ZZZZ"`), registry())
	if err == nil || !strings.Contains(err.Error(), "Unknown currency") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1292
//	test: test_money_from_raw_out_of_range_panics
func TestMoneyFromRawOutOfRangePanics(t *testing.T) {
	raw := new(big.Int).Add(MaxRaw(), big.NewInt(1))
	requirePanicContains(t, "`raw` value", func() { MustFromRaw(raw, currency.USD()) })
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1299
//	test: test_money_from_raw_checked_valid
func TestMoneyFromRawCheckedValid(t *testing.T) {
	value, err := FromRawChecked(big.NewInt(123_450_000_000), currency.USD())
	if err != nil || !value.Currency().Equal(currency.USD()) {
		t.Fatalf("money = %s, error = %v", value, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1306
//	test: test_money_from_raw_checked_above_max_returns_error
func TestMoneyFromRawCheckedAboveMaxReturnsError(t *testing.T) {
	raw := new(big.Int).Add(MaxRaw(), big.NewInt(1))
	_, err := FromRawChecked(raw, currency.USD())
	var typed PredicateViolationError
	if !errors.As(err, &typed) {
		t.Fatalf("error = %T %v", err, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1314
//	test: test_money_from_raw_checked_below_min_returns_error
func TestMoneyFromRawCheckedBelowMinReturnsError(t *testing.T) {
	raw := new(big.Int).Sub(MinRaw(), big.NewInt(1))
	_, err := FromRawChecked(raw, currency.USD())
	var typed PredicateViolationError
	if !errors.As(err, &typed) {
		t.Fatalf("error = %T %v", err, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1322
//	test: test_from_decimal_rejects_out_of_range
func TestMoneyFromDecimalRejectsOutOfRange(t *testing.T) {
	if _, err := FromDecimal(decimal.MustParse("99999999999999999999.99"), currency.USD()); err == nil {
		t.Fatal("out-of-range decimal accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1329
//	test: test_from_decimal_out_of_range_returns_typed_error_with_stable_display
func TestMoneyFromDecimalOutOfRangeReturnsTypedErrorWithStableDisplay(t *testing.T) {
	_, err := FromDecimal(decimal.MustParse("99999999999999999999.99"), currency.USD())
	var typed OutOfRangeError
	if !errors.As(err, &typed) || !strings.Contains(err.Error(), "amount") {
		t.Fatalf("error = %T %v", err, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1344
//	test: test_from_mantissa_exponent_exact_precision
func TestMoneyFromMantissaExponentExactPrecision(t *testing.T) {
	requireMoney(t, FromMantissaExponent(12345, -2, currency.USD()), "123.45 USD")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1350
//	test: test_from_mantissa_exponent_excess_rounds_down
func TestMoneyFromMantissaExponentExcessRoundsDown(t *testing.T) {
	requireMoney(t, FromMantissaExponent(12345, -3, currency.USD()), "12.34 USD")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1357
//	test: test_from_mantissa_exponent_excess_rounds_up
func TestMoneyFromMantissaExponentExcessRoundsUp(t *testing.T) {
	requireMoney(t, FromMantissaExponent(12355, -3, currency.USD()), "12.36 USD")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1364
//	test: test_from_mantissa_exponent_positive_exponent
func TestMoneyFromMantissaExponentPositiveExponent(t *testing.T) {
	requireMoney(t, FromMantissaExponent(5, 2, currency.USD()), "500 USD")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1371
//	test: test_from_mantissa_exponent_overflow_panics
func TestMoneyFromMantissaExponentOverflowPanics(t *testing.T) {
	requirePanicContains(t, "Money::from_mantissa_exponent", func() {
		FromMantissaExponent(math.MaxInt64, 9, currency.USD())
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1377
//	test: test_from_mantissa_exponent_large_exponent_panics
func TestMoneyFromMantissaExponentLargeExponentPanics(t *testing.T) {
	requirePanicContains(t, "exceeds i128 range", func() {
		FromMantissaExponent(1, 119, currency.USD())
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1382
//	test: test_from_mantissa_exponent_zero_with_large_exponent
func TestMoneyFromMantissaExponentZeroWithLargeExponent(t *testing.T) {
	if !FromMantissaExponent(0, 119, currency.USD()).IsZero() {
		t.Fatal("zero mantissa did not produce zero")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1388
//	test: test_from_mantissa_exponent_very_negative_exponent_rounds_to_zero
func TestMoneyFromMantissaExponentVeryNegativeExponentRoundsToZero(t *testing.T) {
	if !FromMantissaExponent(12345, -120, currency.USD()).IsZero() {
		t.Fatal("very small amount did not round to zero")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1402
//	test: test_check_positive_money
func TestCheckPositiveMoney(t *testing.T) {
	tests := []struct {
		amount string
		ok     bool
	}{
		{"42", true},
		{"0", false},
		{"-13.5", false},
	}
	for _, test := range tests {
		err := CheckPositive(MustNew(test.amount, currency.USD()), "money")
		if (err == nil) != test.ok {
			t.Fatalf("%s: error = %v", test.amount, err)
		}
		if err != nil && !strings.Contains(err.Error(), "not positive") {
			t.Fatalf("%s: error = %v", test.amount, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1424
//	test: test_check_positive_money_returns_typed_error_with_stable_display
func TestCheckPositiveMoneyReturnsTypedErrorWithStableDisplay(t *testing.T) {
	err := CheckPositive(MustNew("0", currency.USD()), "money")
	want := NotPositiveError{Param: "money", Value: "0.00 USD"}
	if !errors.Is(err, want) {
		t.Fatalf("error = %#v, want %#v", err, want)
	}
	if err.Error() != "invalid `Money` for 'money' not positive, was 0.00 USD" {
		t.Fatalf("error = %q", err)
	}
}
