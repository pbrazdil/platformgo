package defi

import (
	"math/big"
	"strconv"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:71
//	test: test_from_wei_basic
func TestQuantityFromWeiBasic(t *testing.T) {
	quantity := MustQuantityFromWei(wei("1000000000000000000"))
	if quantity.Precision() != 18 || !quantity.Decimal().Equal(decimal.MustParse("1.0")) {
		t.Fatalf("quantity = %s precision %d", quantity.Decimal(), quantity.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:78
//	test: test_as_wei_basic
func TestQuantityAsWeiBasic(t *testing.T) {
	quantity := QuantityFromRaw(wei("1000000000000000000"), 18)
	if quantity.Wei().Cmp(wei("1000000000000000000")) != 0 {
		t.Fatalf("wei = %s", quantity.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:88
//	test: test_as_wei_wrong_precision
func TestQuantityAsWeiWrongPrecision(t *testing.T) {
	quantity, err := StandardDeFiQuantity("1.23", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer expectPanicContains(t, "Failed to convert quantity with precision 2 to wei (requires precision 18)")
	_ = quantity.Wei()
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:94
//	test: test_wei_round_trip
func TestQuantityWeiRoundTrip(t *testing.T) {
	original := wei("1500000000000000000")
	quantity := MustQuantityFromWei(original)
	if quantity.Wei().Cmp(original) != 0 || !quantity.Decimal().Equal(decimal.MustParse("1.5")) {
		t.Fatalf("quantity/wei = %s/%s", quantity.Decimal(), quantity.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:103
//	test: test_checked_arith_accepts_wei_precision
func TestQuantityCheckedArithAcceptsWeiPrecision(t *testing.T) {
	a := MustQuantityFromWei(wei("1000000000000000000"))
	b := MustQuantityFromWei(wei("2000000000000000000"))
	sum, ok := a.Add(b)
	if !ok || !sum.Decimal().Equal(decimal.MustParse("3")) {
		t.Fatalf("sum = %s, %v", sum.Decimal(), ok)
	}
	diff, ok := b.Sub(a)
	if !ok || !diff.Decimal().Equal(decimal.MustParse("1")) {
		t.Fatalf("diff = %s, %v", diff.Decimal(), ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:117
//	test: test_checked_arith_rejects_mixed_scale
func TestQuantityCheckedArithRejectsMixedScale(t *testing.T) {
	weiQuantity := MustQuantityFromWei(wei("1000000000000000000"))
	standard, _ := StandardDeFiQuantity("1.0", 0)
	for name, ok := range map[string]bool{
		"wei+standard": func() bool { _, ok := weiQuantity.Add(standard); return ok }(),
		"standard+wei": func() bool { _, ok := standard.Add(weiQuantity); return ok }(),
		"wei-standard": func() bool { _, ok := weiQuantity.Sub(standard); return ok }(),
		"standard-wei": func() bool { _, ok := standard.Sub(weiQuantity); return ok }(),
	} {
		if ok {
			t.Errorf("%s unexpectedly succeeded", name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:129
//	test: test_checked_arith_rejects_mixed_defi_scales
func TestQuantityCheckedArithRejectsMixedDeFiScales(t *testing.T) {
	q17 := QuantityFromRaw(wei("100000000000000000"), 17)
	q18 := MustQuantityFromWei(wei("1000000000000000000"))
	for name, ok := range map[string]bool{
		"q17+q18": func() bool { _, ok := q17.Add(q18); return ok }(),
		"q18+q17": func() bool { _, ok := q18.Add(q17); return ok }(),
		"q17-q18": func() bool { _, ok := q17.Sub(q18); return ok }(),
		"q18-q17": func() bool { _, ok := q18.Sub(q17); return ok }(),
	} {
		if ok {
			t.Errorf("%s unexpectedly succeeded", name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:141
//	test: test_from_wei_large_value
func TestQuantityFromWeiLargeValue(t *testing.T) {
	quantity := MustQuantityFromWei(wei("1000000000000000000000"))
	if quantity.Precision() != 18 || !quantity.Decimal().Equal(decimal.MustParse("1000.0")) {
		t.Fatalf("quantity = %s", quantity.Decimal())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:150
//	test: test_from_wei_small_value
func TestQuantityFromWeiSmallValue(t *testing.T) {
	quantity := MustQuantityFromWei(wei("1000000"))
	if quantity.Precision() != 18 || !quantity.Decimal().Equal(decimal.MustParse("0.000000000001")) {
		t.Fatalf("quantity = %s", quantity.Decimal())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:160
//	test: test_from_wei_zero
func TestQuantityFromWeiZero(t *testing.T) {
	quantity := MustQuantityFromWei(new(big.Int))
	if quantity.Precision() != 18 || !quantity.Decimal().IsZero() || quantity.Wei().Sign() != 0 {
		t.Fatalf("zero = %s/%s", quantity.Decimal(), quantity.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:168
//	test: test_from_wei_very_large_value
func TestQuantityFromWeiVeryLargeValue(t *testing.T) {
	raw := wei("1000000000000000000000000000")
	quantity := MustQuantityFromWei(raw)
	if quantity.Precision() != 18 || quantity.Wei().Cmp(raw) != 0 ||
		!quantity.Decimal().Equal(decimal.MustParse("1000000000")) {
		t.Fatalf("quantity = %s/%s", quantity.Decimal(), quantity.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:179
//	test: test_from_wei_overflow
func TestQuantityFromWeiOverflow(t *testing.T) {
	overflow := new(big.Int).Add(maxUnsigned128, big.NewInt(1))
	defer expectPanicContains(t, "raw wei value exceeds unsigned 128-bit range")
	_ = MustQuantityFromWei(overflow)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:185
//	test: test_from_wei_various_amounts
func TestQuantityFromWeiVariousAmounts(t *testing.T) {
	tests := []struct{ raw, value string }{
		{"1", "0.000000000000000001"},
		{"1000", "0.000000000000001"},
		{"1000000", "0.000000000001"},
		{"1000000000", "0.000000001"},
		{"1000000000000", "0.000001"},
		{"1000000000000000", "0.001"},
		{"1000000000000000000", "1"},
		{"10000000000000000000", "10"},
	}
	for _, test := range tests {
		quantity := MustQuantityFromWei(wei(test.raw))
		if quantity.Precision() != 18 || !quantity.Decimal().Equal(decimal.MustParse(test.value)) ||
			quantity.Wei().Cmp(wei(test.raw)) != 0 {
			t.Errorf("%s wei = %s", test.raw, quantity.Decimal())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:207
//	test: test_as_wei_precision_validation
func TestQuantityAsWeiPrecisionValidation(t *testing.T) {
	for _, precision := range []uint8{2, 6, 8, 16} {
		t.Run(strconv.Itoa(int(precision)), func(t *testing.T) {
			quantity, err := StandardDeFiQuantity("123.45", precision)
			if err != nil {
				t.Fatal(err)
			}
			defer expectPanicContains(t, "requires precision 18")
			_ = quantity.Wei()
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:220
//	test: test_arithmetic_operations_with_wei
func TestQuantityArithmeticOperationsWithWei(t *testing.T) {
	quantity1 := MustQuantityFromWei(wei("1000000000000000000"))
	quantity2 := MustQuantityFromWei(wei("500000000000000000"))
	sum, ok := quantity1.Add(quantity2)
	if !ok || sum.Precision() != 18 || !sum.Decimal().Equal(decimal.MustParse("1.5")) ||
		sum.Wei().Cmp(wei("1500000000000000000")) != 0 {
		t.Fatalf("sum = %s/%s", sum.Decimal(), sum.Wei())
	}
	diff, ok := quantity1.Sub(quantity2)
	if !ok || diff.Precision() != 18 || !diff.Decimal().Equal(decimal.MustParse("0.5")) ||
		diff.Wei().Cmp(wei("500000000000000000")) != 0 {
		t.Fatalf("diff = %s/%s", diff.Decimal(), diff.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/quantity.rs:238
//	test: test_comparison_operations_with_wei
func TestQuantityComparisonOperationsWithWei(t *testing.T) {
	quantity1 := MustQuantityFromWei(wei("1000000000000000000"))
	quantity2 := MustQuantityFromWei(wei("2000000000000000000"))
	quantity3 := MustQuantityFromWei(wei("1000000000000000000"))
	if quantity1.Cmp(quantity2) >= 0 || quantity2.Cmp(quantity1) <= 0 ||
		!quantity1.Equal(quantity3) || quantity1.Cmp(quantity3) != 0 {
		t.Fatal("wei quantity ordering is incorrect")
	}
}
