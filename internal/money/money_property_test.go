package money

import (
	"math/big"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
)

var propertyAmounts = []string{
	"-1000000.00",
	"-1000.25",
	"-1.00",
	"0.00",
	"1.00",
	"999.75",
	"1000000.00",
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1488
//	test: prop_money_construction_roundtrip
//
// Adaptations:
//   - Random f64 inputs become deterministic exact decimal vectors.
func TestPropertyMoneyConstructionRoundtrip(t *testing.T) {
	for _, amount := range propertyAmounts {
		value := MustNew(amount, currency.USD())
		requireDecimal(t, value.Decimal(), amount)
		if !value.Currency().Equal(currency.USD()) {
			t.Fatalf("%s changed currency", amount)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1509
//	test: prop_money_addition_commutative
func TestPropertyMoneyAdditionCommutative(t *testing.T) {
	for _, firstText := range propertyAmounts {
		for _, secondText := range propertyAmounts {
			first := MustNew(firstText, currency.USD())
			second := MustNew(secondText, currency.USD())
			if !first.Add(second).Equal(second.Add(first)) {
				t.Fatalf("%s + %s is not commutative", firstText, secondText)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1527
//	test: prop_money_addition_associative
func TestPropertyMoneyAdditionAssociative(t *testing.T) {
	for _, firstText := range propertyAmounts {
		first := MustNew(firstText, currency.USD())
		second := MustNew("31.25", currency.USD())
		third := MustNew("-12.50", currency.USD())
		if !first.Add(second).Add(third).Equal(first.Add(second.Add(third))) {
			t.Fatalf("addition is not associative for %s", firstText)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1552
//	test: prop_money_subtraction_inverse
func TestPropertyMoneySubtractionInverse(t *testing.T) {
	for _, firstText := range propertyAmounts {
		first := MustNew(firstText, currency.USD())
		second := MustNew("27.75", currency.USD())
		if !first.Add(second).Sub(second).Equal(first) {
			t.Fatalf("subtraction is not addition's inverse for %s", firstText)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1569
//	test: prop_money_checked_add_matches_spec
func TestPropertyMoneyCheckedAddMatchesSpec(t *testing.T) {
	rawCases := []*big.Int{MinRaw(), big.NewInt(-1), big.NewInt(0), big.NewInt(1), MaxRaw()}
	for _, firstRaw := range rawCases {
		for _, secondRaw := range rawCases {
			first := MustFromRaw(firstRaw, currency.USD())
			second := MustFromRaw(secondRaw, currency.USD())
			expectedRaw := new(big.Int).Add(firstRaw, secondRaw)
			expectedOK := expectedRaw.Cmp(MinRaw()) >= 0 && expectedRaw.Cmp(MaxRaw()) <= 0
			got, ok := first.CheckedAdd(second)
			if ok != expectedOK || (ok && got.Raw().Cmp(expectedRaw) != 0) {
				t.Fatalf("%s + %s: raw=%s ok=%v, expected raw=%s ok=%v", firstRaw, secondRaw, got.Raw(), ok, expectedRaw, expectedOK)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1586
//	test: prop_money_checked_sub_matches_spec
func TestPropertyMoneyCheckedSubMatchesSpec(t *testing.T) {
	rawCases := []*big.Int{MinRaw(), big.NewInt(-1), big.NewInt(0), big.NewInt(1), MaxRaw()}
	for _, firstRaw := range rawCases {
		for _, secondRaw := range rawCases {
			first := MustFromRaw(firstRaw, currency.USD())
			second := MustFromRaw(secondRaw, currency.USD())
			expectedRaw := new(big.Int).Sub(firstRaw, secondRaw)
			expectedOK := expectedRaw.Cmp(MinRaw()) >= 0 && expectedRaw.Cmp(MaxRaw()) <= 0
			got, ok := first.CheckedSub(second)
			if ok != expectedOK || (ok && got.Raw().Cmp(expectedRaw) != 0) {
				t.Fatalf("%s - %s: raw=%s ok=%v, expected raw=%s ok=%v", firstRaw, secondRaw, got.Raw(), ok, expectedRaw, expectedOK)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1601
//	test: prop_money_zero_identity
func TestPropertyMoneyZeroIdentity(t *testing.T) {
	zero := Zero(currency.USD())
	for _, amount := range propertyAmounts {
		value := MustNew(amount, currency.USD())
		if !value.Add(zero).Equal(value) || !zero.Add(value).Equal(value) || !zero.IsZero() {
			t.Fatalf("zero identity failed for %s", amount)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1609
//	test: prop_money_negation_inverse
func TestPropertyMoneyNegationInverse(t *testing.T) {
	for _, amount := range propertyAmounts {
		value := MustNew(amount, currency.USD())
		negated := value.Neg()
		if !negated.Neg().Equal(value) || !negated.Currency().Equal(value.Currency()) ||
			!value.Add(negated).IsZero() {
			t.Fatalf("negation inverse failed for %s", amount)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1623
//	test: prop_money_comparison_consistency
func TestPropertyMoneyComparisonConsistency(t *testing.T) {
	for _, firstText := range propertyAmounts {
		for _, secondText := range propertyAmounts {
			first := MustNew(firstText, currency.USD())
			second := MustNew(secondText, currency.USD())
			comparison := first.Cmp(second)
			exclusive := 0
			for _, predicate := range []bool{comparison == 0, comparison < 0, comparison > 0} {
				if predicate {
					exclusive++
				}
			}
			if exclusive != 1 || comparison != -second.Cmp(first) {
				t.Fatalf("comparison inconsistent for %s and %s", firstText, secondText)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1645
//	test: prop_money_decimal_conversion
func TestPropertyMoneyDecimalConversion(t *testing.T) {
	for _, amount := range propertyAmounts {
		value := MustNew(amount, currency.USD())
		converted := value.Decimal()
		if converted.Scale() != uint8(value.Currency().Precision) {
			t.Fatalf("%s scale = %d", amount, converted.Scale())
		}
		requireDecimal(t, converted, amount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/money.rs:1675
//	test: prop_money_arithmetic_with_f64
//
// Adaptations:
//   - Random f64 factors become deterministic exact Decimal factors.
func TestPropertyMoneyArithmeticWithF64(t *testing.T) {
	factors := []string{"-1000", "-1.25", "0.5", "1", "17.75", "999.99"}
	value := MustNew("123.45", currency.USD())
	for _, factorText := range factors {
		factor := decimal.MustParse(factorText)
		requireDecimal(t, value.MulDecimal(factor), value.Decimal().Mul(factor).String())
		got, err := value.DivDecimal(factor)
		if err != nil {
			t.Fatalf("divide by %s: %v", factorText, err)
		}
		expected, _ := value.Decimal().Quo(factor, value.Currency().Precision, decimal.RoundHalfEven)
		if !got.Equal(expected) {
			t.Fatalf("divide by %s = %s, want %s", factorText, got, expected)
		}
		requireDecimal(t, value.AddDecimal(factor), value.Decimal().Add(factor).String())
		requireDecimal(t, value.SubDecimal(factor), value.Decimal().Sub(factor).String())
	}
}
