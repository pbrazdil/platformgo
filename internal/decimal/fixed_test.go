package decimal

import (
	"math/big"
	"math/rand/v2"
	"testing"
)

const (
	nautilusRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"
	fixedSource      = "crates/model/src/types/fixed.rs"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1702
//	test: test_bankers_round
//
// Adaptations:
//   - i128 values are supplied as base-10 strings to math/big.Int.
func TestBankersRound(t *testing.T) {
	tests := []struct {
		mantissa string
		excess   uint32
		expected string
	}{
		{"0", 0, "0"}, {"1", 0, "1"}, {"5", 0, "5"}, {"99", 0, "99"}, {"-7", 0, "-7"},
		{"12345", 39, "0"}, {"9223372036854775807", 100, "0"}, {"-99999", 50, "0"},
		{"15", 1, "2"}, {"25", 1, "2"}, {"35", 1, "4"}, {"45", 1, "4"},
		{"55", 1, "6"}, {"65", 1, "6"}, {"75", 1, "8"}, {"85", 1, "8"},
		{"95", 1, "10"}, {"105", 1, "10"},
		{"14", 1, "1"}, {"16", 1, "2"}, {"24", 1, "2"}, {"26", 1, "3"},
		{"11", 1, "1"}, {"19", 1, "2"},
		{"150", 2, "2"}, {"250", 2, "2"}, {"350", 2, "4"}, {"450", 2, "4"},
		{"550", 2, "6"}, {"1050", 2, "10"}, {"1150", 2, "12"},
		{"149", 2, "1"}, {"151", 2, "2"}, {"199", 2, "2"}, {"101", 2, "1"},
		{"1500", 3, "2"}, {"2500", 3, "2"}, {"3500", 3, "4"},
		{"10500", 3, "10"}, {"11500", 3, "12"}, {"1499", 3, "1"}, {"1501", 3, "2"},
		{"-15", 1, "-2"}, {"-25", 1, "-2"}, {"-35", 1, "-4"}, {"-45", 1, "-4"},
		{"-55", 1, "-6"}, {"-65", 1, "-6"}, {"-150", 2, "-2"},
		{"-250", 2, "-2"}, {"-350", 2, "-4"},
		{"-14", 1, "-1"}, {"-16", 1, "-2"}, {"-24", 1, "-2"}, {"-26", 1, "-3"},
		{"0", 1, "0"}, {"0", 2, "0"}, {"0", 5, "0"},
		{"123456789", 3, "123457"}, {"123456500", 3, "123456"},
		{"123457500", 3, "123458"}, {"100005", 1, "10000"}, {"100015", 1, "10002"},
		{"999999999999999995", 1, "100000000000000000"},
		{"1000000000000000005", 1, "100000000000000000"},
	}

	for _, test := range tests {
		got := BankersRound(integer(test.mantissa), test.excess)
		if got.String() != test.expected {
			t.Errorf("BankersRound(%s, %d) = %s, want %s", test.mantissa, test.excess, got, test.expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1722
//	test: test_bankers_round_negative_symmetry
func TestBankersRoundNegativeSymmetry(t *testing.T) {
	tests := []struct {
		mantissa string
		excess   uint32
	}{
		{"15", 1}, {"25", 1}, {"35", 1}, {"150", 2}, {"250", 2},
		{"1500", 3}, {"2500", 3}, {"123456789", 3}, {"14", 1}, {"16", 1},
	}
	for _, test := range tests {
		positive := integer(test.mantissa)
		negative := new(big.Int).Neg(new(big.Int).Set(positive))
		got := BankersRound(negative, test.excess)
		want := new(big.Int).Neg(BankersRound(positive, test.excess))
		if got.Cmp(want) != 0 {
			t.Errorf("negative symmetry failed for %s, %d: got %s, want %s", positive, test.excess, got, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1744
//	test: test_bankers_round_matches_decimal
//
// Adaptations:
//   - The Go exact Decimal is the direct subject; no Rust Decimal cross-check is needed.
func TestBankersRoundMatchesDecimal(t *testing.T) {
	tests := []struct {
		input    string
		scale    uint8
		expected string
	}{
		{"1.005", 2, "1.00"}, {"1.015", 2, "1.02"}, {"1.025", 2, "1.02"},
		{"1.035", 2, "1.04"}, {"1.045", 2, "1.04"}, {"2.5", 0, "2"},
		{"3.5", 0, "4"}, {"-2.5", 0, "-2"}, {"-3.5", 0, "-4"},
		{"123.456", 2, "123.46"}, {"123.455", 2, "123.46"}, {"123.445", 2, "123.44"},
	}
	for _, test := range tests {
		got := MustParse(test.input).Quantize(test.scale, RoundHalfEven)
		if got.String() != test.expected {
			t.Errorf("%s quantized to %d = %s, want %s", test.input, test.scale, got, test.expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1797
//	test: test_correct_raw_u64
func TestCorrectRawUint64(t *testing.T) {
	minimum, maximum := unsignedBounds(64)
	tests := [][2]string{
		{"0", "0"}, {"10", "10"}, {"14", "10"}, {"15", "20"}, {"16", "20"},
		{"18446744073709551615", "18446744073709551610"},
	}
	assertCorrectRaw(t, tests, MaxPrecision-1, minimum, maximum)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1810
//	test: test_correct_raw_i64
func TestCorrectRawInt64(t *testing.T) {
	minimum, maximum := signedBounds(64)
	tests := [][2]string{
		{"0", "0"}, {"14", "10"}, {"15", "20"}, {"-14", "-10"},
		{"-15", "-20"}, {"-16", "-20"},
		{"9223372036854775807", "9223372036854775800"},
		{"-9223372036854775808", "-9223372036854775800"},
	}
	assertCorrectRaw(t, tests, MaxPrecision-1, minimum, maximum)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1819
//	test: test_correct_raw_u128
func TestCorrectRawUint128(t *testing.T) {
	minimum, maximum := unsignedBounds(128)
	tests := [][2]string{
		{"0", "0"}, {"14", "10"}, {"15", "20"},
		{"340282366920938463463374607431768211455", "340282366920938463463374607431768211450"},
	}
	assertCorrectRaw(t, tests, MaxPrecision-1, minimum, maximum)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1830
//	test: test_correct_raw_i128
func TestCorrectRawInt128(t *testing.T) {
	minimum, maximum := signedBounds(128)
	tests := [][2]string{
		{"0", "0"}, {"14", "10"}, {"15", "20"}, {"-15", "-20"},
		{"170141183460469231731687303715884105727", "170141183460469231731687303715884105720"},
		{"-170141183460469231731687303715884105728", "-170141183460469231731687303715884105720"},
	}
	assertCorrectRaw(t, tests, MaxPrecision-1, minimum, maximum)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1835
//	test: test_correct_raw_identity_at_max_precision
func TestCorrectRawIdentityAtMaxPrecision(t *testing.T) {
	unsignedMin, unsignedMax := unsignedBounds(128)
	signedMin, signedMax := signedBounds(128)
	assertCorrectRaw(t, [][2]string{{"12345", "12345"}}, MaxPrecision, unsignedMin, unsignedMax)
	assertCorrectRaw(t, [][2]string{{"-12345", "-12345"}}, MaxPrecision, signedMin, signedMax)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1854
//	test: test_checked_mul_div_fixed_exact_boundaries
func TestCheckedMulDivFixedExactBoundaries(t *testing.T) {
	_, maximum := unsignedBounds(128)
	scalar := new(big.Int).Set(fixedScalar)
	one := big.NewInt(1)
	tests := []struct {
		left, right *big.Int
		want        *big.Int
		ok          bool
	}{
		{new(big.Int), maximum, new(big.Int), true},
		{maximum, new(big.Int), new(big.Int), true},
		{scalar, scalar, scalar, true},
		{new(big.Int).Sub(scalar, one), new(big.Int).Sub(scalar, one), new(big.Int).Sub(scalar, big.NewInt(2)), true},
		{new(big.Int).Add(scalar, one), new(big.Int).Add(scalar, one), new(big.Int).Add(scalar, big.NewInt(2)), true},
		{maximum, scalar, maximum, true},
		{scalar, maximum, maximum, true},
		{maximum, new(big.Int).Add(scalar, one), nil, false},
		{new(big.Int).Add(scalar, one), maximum, nil, false},
	}
	for _, test := range tests {
		got, ok := checkedMulDivFixed(test.left, test.right, maximum)
		if ok != test.ok || (ok && got.Cmp(test.want) != 0) {
			t.Errorf("checkedMulDivFixed(%s, %s) = (%v, %v), want (%v, %v)", test.left, test.right, got, ok, test.want, test.ok)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1922
//	test: prop_checked_mul_div_fixed_matches_u128_ordinary
func TestCheckedMulDivFixedMatchesUint128Ordinary(t *testing.T) {
	random := rand.New(rand.NewPCG(0x50141367, 0x116c9b51))
	_, maximum := unsignedBounds(128)
	for range 4096 {
		left := ordinaryRaw(random)
		right := ordinaryRaw(random)
		product := new(big.Int).Mul(left, right)
		if product.Cmp(maximum) > 0 {
			t.Fatal("ordinary strategy generated an overflowing product")
		}
		want := new(big.Int).Quo(product, fixedScalar)
		got, ok := checkedMulDivFixed(left, right, maximum)
		if !ok || got.Cmp(want) != 0 {
			t.Fatalf("checkedMulDivFixed(%s, %s) = (%v, %v), want %s", left, right, got, ok, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1934
//	test: prop_checked_mul_div_fixed_avoids_u128_phantom_overflow
func TestCheckedMulDivFixedAvoidsUint128PhantomOverflow(t *testing.T) {
	random := rand.New(rand.NewPCG(0x1934, 0x128))
	_, maximum := unsignedBounds(128)
	for range 4096 {
		leftWhole := uint64(10_000 + random.Uint64N(990_001))
		rightWhole := uint64(1_000 + random.Uint64N(9_001))
		remainder := new(big.Int).SetUint64(random.Uint64())
		remainder.Mod(remainder, fixedScalar)

		left := new(big.Int).Mul(new(big.Int).SetUint64(leftWhole), fixedScalar)
		right := new(big.Int).Add(
			new(big.Int).Mul(new(big.Int).SetUint64(rightWhole), fixedScalar),
			remainder,
		)
		product := new(big.Int).Mul(left, right)
		if product.Cmp(maximum) <= 0 {
			t.Fatal("phantom-overflow strategy did not overflow the intermediate product")
		}
		want := new(big.Int).Mul(new(big.Int).SetUint64(leftWhole), right)
		got, ok := checkedMulDivFixed(left, right, maximum)
		if !ok || got.Cmp(want) != 0 {
			t.Fatalf("checkedMulDivFixed phantom overflow = (%v, %v), want %s", got, ok, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1942
//	test: prop_checked_mul_div_fixed_handles_remainders_after_u128_overflow
func TestCheckedMulDivFixedHandlesRemaindersAfterUint128Overflow(t *testing.T) {
	random := rand.New(rand.NewPCG(0x1942, 0x128))
	_, maximum := unsignedBounds(128)
	left := new(big.Int).Sub(new(big.Int).Mul(big.NewInt(2), fixedScalar), big.NewInt(1))
	minimum := new(big.Int).Add(new(big.Int).Quo(maximum, left), big.NewInt(1))
	span := new(big.Int).Sub(new(big.Int).Quo(maximum, big.NewInt(2)), minimum)

	for range 4096 {
		offset := randomUint128(random)
		offset.Mod(offset, span)
		right := new(big.Int).Add(minimum, offset)
		if new(big.Int).Mod(new(big.Int).Set(right), fixedScalar).Sign() == 0 {
			right.Add(right, big.NewInt(1))
		}

		product := new(big.Int).Mul(left, right)
		if product.Cmp(maximum) <= 0 {
			t.Fatal("remainder strategy did not overflow the intermediate product")
		}
		want := new(big.Int).Quo(product, fixedScalar)
		got, ok := checkedMulDivFixed(left, right, maximum)
		if !ok || got.Cmp(want) != 0 {
			t.Fatalf("checkedMulDivFixed remainder overflow = (%v, %v), want %s", got, ok, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1956
//	test: prop_checked_mul_div_fixed_is_commutative
func TestCheckedMulDivFixedIsCommutative(t *testing.T) {
	random := rand.New(rand.NewPCG(0x1956, 0x128))
	_, maximum := unsignedBounds(128)
	for range 4096 {
		left := randomUint128(random)
		right := randomUint128(random)
		forward, forwardOK := checkedMulDivFixed(left, right, maximum)
		reverse, reverseOK := checkedMulDivFixed(right, left, maximum)
		if forwardOK != reverseOK || (forwardOK && forward.Cmp(reverse) != 0) {
			t.Fatalf("commutativity failed for %s and %s", left, right)
		}
	}
}

func assertCorrectRaw(
	t *testing.T,
	tests [][2]string,
	precision uint8,
	minimum, maximum *big.Int,
) {
	t.Helper()
	for _, test := range tests {
		got := correctRaw(integer(test[0]), precision, minimum, maximum)
		if got.String() != test[1] {
			t.Errorf("correctRaw(%s, %d) = %s, want %s", test[0], precision, got, test[1])
		}
	}
}

func integer(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid integer test constant: " + value)
	}
	return result
}

func ordinaryRaw(random *rand.Rand) *big.Int {
	whole := new(big.Int).SetUint64(random.Uint64N(1_001))
	remainder := new(big.Int).SetUint64(random.Uint64())
	remainder.Mod(remainder, fixedScalar)
	return new(big.Int).Add(new(big.Int).Mul(whole, fixedScalar), remainder)
}

func randomUint128(random *rand.Rand) *big.Int {
	high := new(big.Int).SetUint64(random.Uint64())
	high.Lsh(high, 64)
	return high.Or(high, new(big.Int).SetUint64(random.Uint64()))
}
