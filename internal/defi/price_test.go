package defi

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func wei(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid wei fixture")
	}
	return result
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:80
//	test: test_from_wei_basic
func TestPriceFromWeiBasic(t *testing.T) {
	price := MustPriceFromWei(wei("1000000000000000000"))
	if price.Precision() != 18 || price.Decimal().String() != "1.000000000000000000" {
		t.Fatalf("price = %s precision %d", price.Decimal(), price.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:87
//	test: test_precision_18_requires_from_wei
func TestPricePrecision18RequiresFromWei(t *testing.T) {
	if _, err := StandardDeFiPrice("1.0", 18); err == nil ||
		!strings.Contains(err.Error(), "use `Price::from_wei()`") {
		t.Fatalf("standard precision 18 error = %v", err)
	}
	price := MustPriceFromWei(wei("1000000000000000000"))
	if price.Precision() != 18 || price.Decimal().Normalize().String() != "1" {
		t.Fatalf("price = %s", price.Decimal())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:105
//	test: test_as_wei_basic
func TestPriceAsWeiBasic(t *testing.T) {
	price := PriceFromRaw(wei("1000000000000000000"), 18)
	if price.Wei().Cmp(wei("1000000000000000000")) != 0 {
		t.Fatalf("wei = %s", price.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:115
//	test: test_as_wei_wrong_precision
func TestPriceAsWeiWrongPrecision(t *testing.T) {
	price, _ := StandardDeFiPrice("1.23", 2)
	defer expectPanicContains(t, "requires precision 18")
	_ = price.Wei()
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:122
//	test: test_as_wei_negative_price
func TestPriceAsWeiNegativePrice(t *testing.T) {
	price := PriceFromRaw(wei("-1000000000000000000"), 18)
	defer expectPanicContains(t, "Failed to convert negative price")
	_ = price.Wei()
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:128
//	test: test_wei_round_trip
func TestPriceWeiRoundTrip(t *testing.T) {
	original := wei("1500000000000000000")
	price := MustPriceFromWei(original)
	if price.Wei().Cmp(original) != 0 || price.Decimal().Normalize().String() != "1.5" {
		t.Fatalf("price/wei = %s/%s", price.Decimal(), price.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:137
//	test: test_from_wei_zero
func TestPriceFromWeiZero(t *testing.T) {
	price := MustPriceFromWei(new(big.Int))
	if price.Precision() != 18 || !price.Decimal().IsZero() || price.Wei().Sign() != 0 {
		t.Fatalf("zero = %s/%s", price.Decimal(), price.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:145
//	test: test_from_wei_very_large_value
func TestPriceFromWeiVeryLargeValue(t *testing.T) {
	raw := wei("1000000000000000000000000000")
	price := MustPriceFromWei(raw)
	if price.Precision() != 18 || price.Wei().Cmp(raw) != 0 ||
		price.Decimal().Normalize().String() != "1000000000" {
		t.Fatalf("price = %s/%s", price.Decimal(), price.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:156
//	test: test_from_wei_overflow
func TestPriceFromWeiOverflow(t *testing.T) {
	overflow := new(big.Int).Lsh(big.NewInt(1), 128)
	if _, err := PriceFromWei(overflow); err == nil {
		t.Fatal("overflow wei accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:162
//	test: test_checked_arith_accepts_wei_precision
func TestPriceCheckedArithmeticAcceptsWeiPrecision(t *testing.T) {
	one := MustPriceFromWei(wei("1000000000000000000"))
	two := MustPriceFromWei(wei("2000000000000000000"))
	sum, sumOK := one.Add(two)
	diff, diffOK := two.Sub(one)
	if !sumOK || !diffOK || sum.Decimal().Normalize().String() != "3" ||
		diff.Decimal().Normalize().String() != "1" {
		t.Fatalf("sum/diff = %s/%s %v/%v", sum.Decimal(), diff.Decimal(), sumOK, diffOK)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:177
//	test: test_checked_arith_rejects_mixed_scale
func TestPriceCheckedArithmeticRejectsMixedScale(t *testing.T) {
	weiPrice := MustPriceFromWei(wei("1000000000000000000"))
	standard, _ := StandardDeFiPrice("1", 0)
	if _, ok := weiPrice.Add(standard); ok {
		t.Fatal("wei + standard succeeded")
	}
	if _, ok := standard.Add(weiPrice); ok {
		t.Fatal("standard + wei succeeded")
	}
	if _, ok := weiPrice.Sub(standard); ok {
		t.Fatal("wei - standard succeeded")
	}
	if _, ok := standard.Sub(weiPrice); ok {
		t.Fatal("standard - wei succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:190
//	test: test_checked_arith_rejects_mixed_defi_scales
func TestPriceCheckedArithmeticRejectsMixedDeFiScales(t *testing.T) {
	p17 := PriceFromRaw(wei("100000000000000000"), 17)
	p18 := MustPriceFromWei(wei("1000000000000000000"))
	if _, ok := p17.Add(p18); ok {
		t.Fatal("p17 + p18 succeeded")
	}
	if _, ok := p18.Add(p17); ok {
		t.Fatal("p18 + p17 succeeded")
	}
	if _, ok := p17.Sub(p18); ok {
		t.Fatal("p17 - p18 succeeded")
	}
	if _, ok := p18.Sub(p17); ok {
		t.Fatal("p18 - p17 succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:202
//	test: test_from_wei_small_amounts
func TestPriceFromWeiSmallAmounts(t *testing.T) {
	tests := map[string]string{
		"1": "0.000000000000000001", "1000": "0.000000000000001000",
		"1000000": "0.000000000001000000", "1000000000": "0.000000001000000000",
	}
	for raw, want := range tests {
		price := MustPriceFromWei(wei(raw))
		if price.Decimal().String() != want || price.Wei().String() != raw {
			t.Errorf("%s wei = %s/%s", raw, price.Decimal(), price.Wei())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:220
//	test: test_from_wei_large_amounts
func TestPriceFromWeiLargeAmounts(t *testing.T) {
	tests := map[string]string{
		"1000000000000000000": "1", "10000000000000000000": "10",
		"100000000000000000000": "100", "1000000000000000000000": "1000",
	}
	for raw, want := range tests {
		price := MustPriceFromWei(wei(raw))
		if price.Decimal().Normalize().String() != want || price.Wei().String() != raw {
			t.Errorf("%s wei = %s/%s", raw, price.Decimal(), price.Wei())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:238
//	test: test_as_wei_precision_validation
func TestPriceAsWeiPrecisionValidation(t *testing.T) {
	for _, precision := range []uint8{2, 6, 8, 16} {
		price, _ := StandardDeFiPrice("123.45", precision)
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("precision %d did not panic", precision)
				}
			}()
			_ = price.Wei()
		}()
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:251
//	test: test_arithmetic_operations_with_wei
func TestPriceArithmeticOperationsWithWei(t *testing.T) {
	one := MustPriceFromWei(wei("1000000000000000000"))
	half := MustPriceFromWei(wei("500000000000000000"))
	sum, _ := one.Add(half)
	diff, _ := one.Sub(half)
	if sum.Precision() != 18 || sum.Decimal().Normalize().String() != "1.5" ||
		diff.Precision() != 18 || diff.Decimal().Normalize().String() != "0.5" {
		t.Fatalf("sum/diff = %s/%s", sum.Decimal(), diff.Decimal())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/price.rs:267
//	test: test_comparison_operations_with_wei
func TestPriceComparisonOperationsWithWei(t *testing.T) {
	one := MustPriceFromWei(wei("1000000000000000000"))
	two := MustPriceFromWei(wei("2000000000000000000"))
	same := MustPriceFromWei(wei("1000000000000000000"))
	if one.Cmp(two) >= 0 || two.Cmp(one) <= 0 || !one.Equal(same) ||
		one.Cmp(same) > 0 || one.Cmp(same) < 0 {
		t.Fatal("wei comparison invariant failed")
	}
}

func expectPanicContains(t *testing.T, want string) {
	t.Helper()
	value := recover()
	if value == nil || !strings.Contains(fmt.Sprint(value), want) {
		t.Fatalf("panic = %v, want substring %q", value, want)
	}
}
