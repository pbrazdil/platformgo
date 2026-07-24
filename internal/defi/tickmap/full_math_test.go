package tickmap

import (
	"math/big"
	"testing"
)

func bi(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid integer fixture")
	}
	return result
}

func assertBigEqual(t *testing.T, got, want *big.Int) {
	t.Helper()
	if got.Cmp(want) != 0 {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:224
//	test: test_mul_div_reverts_denominator_zero
func TestMulDivRevertsDenominatorZero(t *testing.T) {
	if _, err := MulDiv(Q128(), big.NewInt(5), new(big.Int)); err == nil {
		t.Fatal("zero denominator succeeded")
	}
	if _, err := MulDiv(Q128(), Q128(), new(big.Int)); err == nil {
		t.Fatal("overflowing numerator with zero denominator succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:233
//	test: test_mul_div_reverts_output_overflow
func TestMulDivRevertsOutputOverflow(t *testing.T) {
	max := MaxU256()
	cases := [][3]*big.Int{
		{Q128(), Q128(), big.NewInt(1)},
		{max, max, big.NewInt(1)},
		{max, max, big.NewInt(2)},
		{max, max, new(big.Int).Sub(max, big.NewInt(1))},
	}
	for _, test := range cases {
		if _, err := MulDiv(test[0], test[1], test[2]); err == nil {
			t.Errorf("MulDiv(%s,%s,%s) succeeded", test[0], test[1], test[2])
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:249
//	test: test_mul_div_all_max_inputs
func TestMulDivAllMaxInputs(t *testing.T) {
	got, err := MulDiv(MaxU256(), MaxU256(), MaxU256())
	if err != nil {
		t.Fatal(err)
	}
	assertBigEqual(t, got, MaxU256())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:256
//	test: test_mul_div_accurate_without_phantom_overflow
func TestMulDivAccurateWithoutPhantomOverflow(t *testing.T) {
	q := Q128()
	b := new(big.Int).Quo(new(big.Int).Mul(q, big.NewInt(50)), big.NewInt(100))
	d := new(big.Int).Quo(new(big.Int).Mul(q, big.NewInt(150)), big.NewInt(100))
	got, err := MulDiv(q, b, d)
	if err != nil {
		t.Fatal(err)
	}
	assertBigEqual(t, got, new(big.Int).Quo(q, big.NewInt(3)))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:267
//	test: test_mul_div_accurate_with_phantom_overflow
func TestMulDivAccurateWithPhantomOverflow(t *testing.T) {
	q := Q128()
	got, err := MulDiv(q, new(big.Int).Mul(big.NewInt(35), q), new(big.Int).Mul(big.NewInt(8), q))
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(4375), q), big.NewInt(1000))
	assertBigEqual(t, got, want)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:278
//	test: test_mul_div_accurate_with_phantom_overflow_repeating_decimal
func TestMulDivAccurateWithPhantomOverflowRepeatingDecimal(t *testing.T) {
	q := Q128()
	got, err := MulDiv(q, new(big.Int).Mul(big.NewInt(1000), q), new(big.Int).Mul(big.NewInt(3000), q))
	if err != nil {
		t.Fatal(err)
	}
	assertBigEqual(t, got, new(big.Int).Quo(q, big.NewInt(3)))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:289
//	test: test_mul_div_basic_cases
func TestMulDivBasicCases(t *testing.T) {
	for _, test := range []struct{ a, b, d, want int64 }{
		{100, 200, 50, 400}, {1000, 1, 4, 250}, {1, 1, 3, 0},
	} {
		got, err := MulDiv(big.NewInt(test.a), big.NewInt(test.b), big.NewInt(test.d))
		if err != nil {
			t.Fatal(err)
		}
		assertBigEqual(t, got, big.NewInt(test.want))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:311
//	test: test_mul_div_rounding_up_reverts_denominator_zero
func TestMulDivRoundingUpRevertsDenominatorZero(t *testing.T) {
	if _, err := MulDivRoundingUp(Q128(), big.NewInt(5), new(big.Int)); err == nil {
		t.Fatal("zero denominator succeeded")
	}
	if _, err := MulDivRoundingUp(Q128(), Q128(), new(big.Int)); err == nil {
		t.Fatal("overflowing numerator with zero denominator succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:320
//	test: test_mul_div_rounding_up_reverts_output_overflow
func TestMulDivRoundingUpRevertsOutputOverflow(t *testing.T) {
	max := MaxU256()
	for _, test := range [][3]*big.Int{
		{Q128(), Q128(), big.NewInt(1)},
		{max, max, big.NewInt(2)},
		{max, max, new(big.Int).Sub(max, big.NewInt(1))},
	} {
		if _, err := MulDivRoundingUp(test[0], test[1], test[2]); err == nil {
			t.Errorf("MulDivRoundingUp(%s,%s,%s) succeeded", test[0], test[1], test[2])
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:336
//	test: test_mul_div_rounding_up_reverts_overflow_after_rounding_case_1
func TestMulDivRoundingUpRevertsOverflowAfterRoundingCase1(t *testing.T) {
	a := bi("535006138814359")
	b := bi("432862656469423142931042426214547535783388063929571229938474969")
	if _, err := MulDivRoundingUp(a, b, big.NewInt(2)); err == nil {
		t.Fatal("rounding overflow succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:351
//	test: test_mul_div_rounding_up_reverts_overflow_after_rounding_case_2
func TestMulDivRoundingUpRevertsOverflowAfterRoundingCase2(t *testing.T) {
	a := bi("115792089237316195423570985008687907853269984659341747863450311749907997002549")
	b := bi("115792089237316195423570985008687907853269984659341747863450311749907997002550")
	d := bi("115792089237316195423570985008687907853269984653042931687443039491902864365164")
	if _, err := MulDivRoundingUp(a, b, d); err == nil {
		t.Fatal("rounding overflow succeeded")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:374
//	test: test_mul_div_rounding_up_all_max_inputs
func TestMulDivRoundingUpAllMaxInputs(t *testing.T) {
	got, err := MulDivRoundingUp(MaxU256(), MaxU256(), MaxU256())
	if err != nil {
		t.Fatal(err)
	}
	assertBigEqual(t, got, MaxU256())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:381
//	test: test_mul_div_rounding_up_accurate_without_phantom_overflow
func TestMulDivRoundingUpAccurateWithoutPhantomOverflow(t *testing.T) {
	q := Q128()
	b := new(big.Int).Quo(new(big.Int).Mul(q, big.NewInt(50)), big.NewInt(100))
	d := new(big.Int).Quo(new(big.Int).Mul(q, big.NewInt(150)), big.NewInt(100))
	got, err := MulDivRoundingUp(q, b, d)
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Add(new(big.Int).Quo(q, big.NewInt(3)), big.NewInt(1))
	assertBigEqual(t, got, want)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:392
//	test: test_mul_div_rounding_up_accurate_with_phantom_overflow
func TestMulDivRoundingUpAccurateWithPhantomOverflow(t *testing.T) {
	q := Q128()
	got, err := MulDivRoundingUp(q, new(big.Int).Mul(big.NewInt(35), q), new(big.Int).Mul(big.NewInt(8), q))
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(4375), q), big.NewInt(1000))
	assertBigEqual(t, got, want)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:404
//	test: test_mul_div_rounding_up_accurate_with_phantom_overflow_repeating_decimal
func TestMulDivRoundingUpAccurateWithPhantomOverflowRepeatingDecimal(t *testing.T) {
	q := Q128()
	got, err := MulDivRoundingUp(q, new(big.Int).Mul(big.NewInt(1000), q), new(big.Int).Mul(big.NewInt(3000), q))
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Add(new(big.Int).Quo(q, big.NewInt(3)), big.NewInt(1))
	assertBigEqual(t, got, want)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:415
//	test: test_mul_div_rounding_up_basic_cases
func TestMulDivRoundingUpBasicCases(t *testing.T) {
	for _, test := range []struct{ a, b, d, want int64 }{
		{100, 200, 50, 400}, {1, 1, 3, 1}, {7, 3, 4, 6}, {0, 100, 3, 0},
	} {
		got, err := MulDivRoundingUp(big.NewInt(test.a), big.NewInt(test.b), big.NewInt(test.d))
		if err != nil {
			t.Fatal(err)
		}
		assertBigEqual(t, got, big.NewInt(test.want))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:443
//	test: test_mul_div_rounding_up_overflow_at_max
func TestMulDivRoundingUpOverflowAtMax(t *testing.T) {
	max := MaxU256()
	got, err := MulDivRoundingUp(max, big.NewInt(2), big.NewInt(2))
	if err != nil {
		t.Fatal(err)
	}
	assertBigEqual(t, got, max)
	got, err = MulDivRoundingUp(max, big.NewInt(1), big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	assertBigEqual(t, got, max)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:457
//	test: test_truncate_to_u128_preserves_small_values
func TestTruncateToU128PreservesSmallValues(t *testing.T) {
	assertBigEqual(t, TruncateToU128(big.NewInt(12345)), big.NewInt(12345))
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	assertBigEqual(t, TruncateToU128(max), max)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/full_math.rs:468
//	test: test_truncate_to_u128_discards_upper_bits
func TestTruncateToU128DiscardsUpperBits(t *testing.T) {
	assertBigEqual(t, TruncateToU128(new(big.Int).Lsh(big.NewInt(1), 128)), new(big.Int))
	upper := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	value := new(big.Int).Or(new(big.Int).Lsh(upper, 128), big.NewInt(0x1234))
	assertBigEqual(t, TruncateToU128(value), big.NewInt(0x1234))
}
