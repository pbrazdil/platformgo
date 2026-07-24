package decimal

import (
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"
)

func requireFixedPanic(t *testing.T, message string, fn func()) {
	t.Helper()
	defer func() {
		got := recover()
		if got == nil || !strings.Contains(got.(string), message) {
			t.Fatalf("panic = %v, want containing %q", got, message)
		}
	}()
	fn()
}

func checkPrecisionBoundary(t *testing.T, maximum uint8, name string) {
	t.Helper()
	if err := checkFixedPrecisionLimit(0, maximum, name); err != nil {
		t.Fatal(err)
	}
	if err := checkFixedPrecisionLimit(maximum, maximum, name); err != nil {
		t.Fatal(err)
	}
	if err := checkFixedPrecisionLimit(maximum+1, maximum, name); err == nil {
		t.Fatal("expected excessive precision error")
	}
}

func checkSignedRoundTrip(t *testing.T, fixedPrecision uint8, bits uint, epsilon float64, values []float64) {
	t.Helper()
	for _, value := range values {
		for precision := uint8(0); precision <= fixedPrecision; precision++ {
			raw := float64ToFixedBig(value, precision, fixedPrecision, true, bits, "signed")
			got := fixedBigToFloat64(raw, fixedPrecision)
			if math.Abs(got-value) > epsilon {
				t.Fatalf("round trip %v at %d = %v", value, precision, got)
			}
		}
	}
}

func checkUnsignedRoundTrip(t *testing.T, fixedPrecision uint8, bits uint) {
	t.Helper()
	for _, value := range []float64{0, 1, 1_000_000} {
		for precision := uint8(0); precision <= fixedPrecision; precision++ {
			raw := float64ToFixedBig(value, precision, fixedPrecision, false, bits, "unsigned")
			got := fixedBigToFloat64(raw, fixedPrecision)
			if math.Abs(got-value) > 0.001 {
				t.Fatalf("round trip %v at %d = %v", value, precision, got)
			}
		}
	}
}

func checkExactFloatRoundTrip(t *testing.T, signed bool, fixedPrecision uint8, bits uint) {
	t.Helper()
	values := []struct {
		precision uint8
		value     float64
	}{
		{0, 0}, {1, 1}, {1, 1.1}, {9, 0.000000001},
	}
	if fixedPrecision == 16 {
		values = append(values, struct {
			precision uint8
			value     float64
		}{16, 0.0000000000000001})
	}
	if signed {
		values = append(values,
			struct {
				precision uint8
				value     float64
			}{1, -1},
			struct {
				precision uint8
				value     float64
			}{1, -1.1},
			struct {
				precision uint8
				value     float64
			}{9, -0.000000001},
		)
	}
	for _, test := range values {
		raw := float64ToFixedBig(test.value, test.precision, fixedPrecision, signed, bits, "raw")
		if got := fixedBigToFloat64(raw, fixedPrecision); got != test.value {
			t.Fatalf("round trip %v at %d = %v", test.value, test.precision, got)
		}
	}
}

func checkFloatFormula(t *testing.T, signed bool, fixedPrecision uint8, bits uint, precisions []uint8, value float64) {
	t.Helper()
	for _, precision := range precisions {
		got := float64ToFixedBig(value, precision, fixedPrecision, signed, bits, "raw")
		rounded := math.Round(value * math.Pow10(int(precision)))
		expected, _ := new(big.Float).SetFloat64(rounded).Int(nil)
		expected.Mul(expected, powerOfTen(uint32(fixedPrecision-precision)))
		if got.Cmp(expected) != 0 {
			t.Fatalf("fixed %v at %d = %s, want %s", value, precision, got, expected)
		}
	}
}

func checkRawValues(t *testing.T, fixedPrecision uint8, valid bool, signed bool) {
	t.Helper()
	var tests []struct {
		precision uint8
		raw       string
	}
	if fixedPrecision == 16 {
		if valid {
			tests = []struct {
				precision uint8
				raw       string
			}{{0, "0"}, {0, "10000000000000000"}, {0, "1200000000000000000"}, {8, "12345678900000000"}, {15, "1234567890123450"}}
			if signed {
				tests = append(tests,
					struct {
						precision uint8
						raw       string
					}{0, "-10000000000000000"},
					struct {
						precision uint8
						raw       string
					}{8, "-12345678900000000"},
				)
			}
		} else {
			tests = []struct {
				precision uint8
				raw       string
			}{{0, "1"}, {0, "9999999999999999"}, {0, "10000000000000001"}, {8, "12345678900000001"}, {15, "1234567890123451"}}
			if signed {
				tests = append(tests,
					struct {
						precision uint8
						raw       string
					}{0, "-1"},
					struct {
						precision uint8
						raw       string
					}{0, "-9999999999999999"},
				)
			}
		}
	} else if valid {
		tests = []struct {
			precision uint8
			raw       string
		}{{0, "0"}, {0, "1000000000"}, {0, "120000000000"}, {2, "123450000000"}, {8, "1234567890"}}
		if signed {
			tests = append(tests,
				struct {
					precision uint8
					raw       string
				}{0, "-1000000000"},
				struct {
					precision uint8
					raw       string
				}{2, "-123450000000"},
			)
		}
	} else {
		tests = []struct {
			precision uint8
			raw       string
		}{{0, "1"}, {0, "999999999"}, {0, "1000000001"}, {0, "119582001968421736"}, {2, "123456789000"}, {8, "1234567891"}}
		if signed {
			tests = append(tests,
				struct {
					precision uint8
					raw       string
				}{0, "-1"},
				struct {
					precision uint8
					raw       string
				}{0, "-999999999"},
			)
		}
	}
	for _, test := range tests {
		err := checkFixedRawAt(integer(test.raw), test.precision, fixedPrecision)
		if valid && err != nil {
			t.Fatalf("valid raw %s at %d: %v", test.raw, test.precision, err)
		}
		if !valid && err == nil {
			t.Fatalf("invalid raw %s at %d passed", test.raw, test.precision)
		}
	}
}

func checkRawAtMaximum(t *testing.T, fixedPrecision uint8, signed bool, bits uint) {
	t.Helper()
	minimum, maximum := unsignedBounds(bits)
	if signed {
		minimum, maximum = signedBounds(bits)
	}
	values := []*big.Int{big.NewInt(0), big.NewInt(1), maximum}
	if signed {
		values = append(values, big.NewInt(-1), minimum)
	}
	for _, value := range values {
		if err := checkFixedRawAt(value, fixedPrecision, fixedPrecision); err != nil {
			t.Fatalf("raw %s at maximum: %v", value, err)
		}
	}
}

func checkStandardPrecisionValues(t *testing.T) {
	t.Helper()
	tests := []struct {
		precision uint8
		value     float64
		expected  string
	}{
		{0, 123456, "123456000000000"},
		{0, 123456.7, "123457000000000"},
		{1, 123456.7, "123456700000000"},
		{2, 123456.78, "123456780000000"},
		{8, 123456.12345678, "123456123456780"},
		{9, 123456.123456789, "123456123456789"},
	}
	for _, test := range tests {
		got := float64ToFixedBig(test.value, test.precision, 9, true, 64, "i64")
		if got.String() != test.expected {
			t.Fatalf("%v at %d = %s, want %s", test.value, test.precision, got, test.expected)
		}
	}
}

func checkStandardRoundingVectors(t *testing.T, signed bool) {
	t.Helper()
	tests := []struct {
		precision uint8
		value     float64
		expected  string
	}{
		{0, 5.5, "6000000000"}, {1, 5.55, "5600000000"}, {2, 5.555, "5560000000"},
		{3, 5.5555, "5556000000"}, {4, 5.55555, "5555600000"},
		{5, 5.555555, "5555560000"}, {6, 5.5555555, "5555556000"},
		{7, 5.55555555, "5555555600"}, {8, 5.555555555, "5555555560"},
		{9, 5.5555555555, "5555555556"},
	}
	for _, test := range tests {
		got := float64ToFixedBig(test.value, test.precision, 9, signed, 64, "raw")
		if got.String() != test.expected {
			t.Fatalf("%v at %d = %s, want %s", test.value, test.precision, got, test.expected)
		}
		if signed {
			got = float64ToFixedBig(-test.value, test.precision, 9, true, 64, "i64")
			if got.String() != "-"+test.expected {
				t.Fatalf("negative %v at %d = %s", test.value, test.precision, got)
			}
		}
	}
}

func checkFixedToFloat(t *testing.T, fixedPrecision uint8, signed bool, bits uint) {
	t.Helper()
	values := []int64{
		0, 1, 2, 3, 10, 100, 1_000, 10_000, 100_000, 1_000_000,
		10_000_000, 100_000_000, 1_000_000_000, 10_000_000_000,
		100_000_000_000, 1_000_000_000_000, 10_000_000_000_000,
		100_000_000_000_000, 1_000_000_000_000_000,
	}
	if signed {
		values = []int64{1, -1, 2, -2, 10, -10, 100, -100, 1_000, -1_000, -10_000, -100_000}
	}
	for _, value := range values {
		raw := big.NewInt(value)
		got := fixedBigToFloat64(raw, fixedPrecision)
		want := float64(value) / math.Pow10(int(fixedPrecision))
		if got != want {
			t.Fatalf("%d to float = %v, want %v", value, got, want)
		}
	}
	if !signed && bits == 128 {
		for _, value := range []string{"10000000000000000", "100000000000000000", "1000000000000000000", "10000000000000000000", "100000000000000000000"} {
			raw := integer(value)
			got := fixedBigToFloat64(raw, fixedPrecision)
			numerator, _ := new(big.Float).SetInt(raw).Float64()
			want := numerator / math.Pow10(int(fixedPrecision))
			if got != want {
				t.Fatalf("%s to float = %v, want %v", value, got, want)
			}
		}
	}
	_ = bits
}

func checkStandardMulDivProperties(t *testing.T, mode string) {
	t.Helper()
	random := rand.New(rand.NewPCG(0x1885, 0x1909))
	scalar := big.NewInt(1_000_000_000)
	_, maximum := unsignedBounds(64)
	for range 4096 {
		var lhs, rhs *big.Int
		switch mode {
		case "full":
			lhs = new(big.Int).SetUint64(random.Uint64())
			rhs = new(big.Int).SetUint64(random.Uint64())
		case "final_fit":
			lhs = new(big.Int).Add(
				new(big.Int).Mul(new(big.Int).SetUint64(random.Uint64N(1001)), scalar),
				new(big.Int).SetUint64(random.Uint64N(1_000_000_000)),
			)
			rhs = new(big.Int).Add(
				new(big.Int).Mul(new(big.Int).SetUint64(random.Uint64N(1001)), scalar),
				new(big.Int).SetUint64(random.Uint64N(1_000_000_000)),
			)
		case "phantom":
			lhs = new(big.Int).Mul(
				new(big.Int).SetUint64(8_000_000_000+random.Uint64N(1_000_000_001)),
				scalar,
			)
			rhs = new(big.Int).Add(scalar, new(big.Int).SetUint64(random.Uint64N(1_000_000_000)))
			if new(big.Int).Mul(lhs, rhs).Cmp(maximum) <= 0 {
				t.Fatal("phantom strategy product did not overflow u64")
			}
		}
		product := new(big.Int).Mul(lhs, rhs)
		want := new(big.Int).Quo(product, scalar)
		got, ok := checkedMulDivScaled(lhs, rhs, scalar, maximum)
		wantOK := want.Cmp(maximum) <= 0
		if ok != wantOK || (ok && got.Cmp(want) != 0) {
			t.Fatalf("%s mul/div (%s,%s) = (%v,%v), want (%s,%v)", mode, lhs, rhs, got, ok, want, wantOK)
		}
	}
}

func checkHighFullRangeMulDiv(t *testing.T) {
	t.Helper()
	random := rand.New(rand.NewPCG(0x1972, 0x256))
	_, maximum := unsignedBounds(128)
	for range 4096 {
		lhs := randomUint128(random)
		rhs := randomUint128(random)
		want := new(big.Int).Quo(new(big.Int).Mul(lhs, rhs), fixedScalar)
		got, ok := checkedMulDivFixed(lhs, rhs, maximum)
		wantOK := want.Cmp(maximum) <= 0
		if ok != wantOK || (ok && got.Cmp(want) != 0) {
			t.Fatalf("full-range mul/div mismatch")
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:897
//	test: test_precision_boundaries
func TestFixedHighPrecisionBoundaries(t *testing.T) { checkPrecisionBoundary(t, 16, "FIXED_PRECISION") }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:905
//	test: test_precision_boundaries
func TestFixedDefiPrecisionBoundaries(t *testing.T) { checkPrecisionBoundary(t, 18, "WEI_PRECISION") }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:917
//	test: test_basic_roundtrip
func TestFixedHighBasicRoundTrip(t *testing.T) {
	checkSignedRoundTrip(t, 16, 128, 0.001, []float64{0, 1, -1})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:928
//	test: test_large_value_roundtrip
func TestFixedHighLargeValueRoundTrip(t *testing.T) {
	checkSignedRoundTrip(t, 16, 128, 0.0001, []float64{1_000_000, -1_000_000})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:942
//	test: test_precision_specific_values_basic
func TestFixedHighPrecisionSpecificValuesBasic(t *testing.T) {
	for _, test := range []struct {
		precision uint8
		value     float64
	}{{0, 123456}, {0, 123456.7}, {1, 123456.7}, {2, 123456.78}, {8, 123456.12345678}} {
		raw := float64ToFixedBig(test.value, test.precision, 16, true, 128, "i128")
		got := fixedBigToFloat64(raw, 16)
		scale := math.Pow10(int(test.precision))
		want := math.Round(test.value*scale) / scale
		if math.Abs(got-want) >= 1e-10 {
			t.Fatalf("%v at %d = %v, want %v", test.value, test.precision, got, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:952
//	test: test_max_precision_values
func TestFixedHighMaxPrecisionValues(t *testing.T) {
	value := 123456.123456789
	got := fixedBigToFloat64(float64ToFixedBig(value, 16, 16, true, 128, "i128"), 16)
	if math.Abs(got-value) >= 1e-6 {
		t.Fatalf("round trip = %v, want %v", got, value)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:965
//	test: test_unsigned_basic_roundtrip
func TestFixedHighUnsignedBasicRoundTrip(t *testing.T) { checkUnsignedRoundTrip(t, 16, 128) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:976
//	test: test_valid_precision
func TestFixedHighValidPrecision(t *testing.T) {
	if CheckFixedPrecision(0) != nil || CheckFixedPrecision(16) != nil {
		t.Fatal("valid precision rejected")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:983
//	test: test_invalid_precision
func TestFixedHighInvalidPrecision(t *testing.T) {
	if CheckFixedPrecision(17) == nil {
		t.Fatal("invalid precision accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:991
//	test: test_invalid_precision
func TestFixedDefiInvalidPrecision(t *testing.T) {
	if checkFixedPrecisionLimit(19, 18, "WEI_PRECISION") == nil {
		t.Fatal("invalid precision accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1000
//	test: test_check_fixed_precision_returns_typed_error_with_stable_display
func TestFixedHighPrecisionTypedStableError(t *testing.T) {
	err := CheckFixedPrecision(17)
	var fixedErr *FixedError
	if !errors.As(err, &fixedErr) || fixedErr.Kind != "predicate_violation" {
		t.Fatalf("error = %#v", err)
	}
	const want = "`precision` exceeded maximum `FIXED_PRECISION` (16), was 17"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1023
//	test: test_check_fixed_precision_returns_typed_error_with_stable_display
func TestFixedDefiPrecisionTypedStableError(t *testing.T) {
	err := checkFixedPrecisionLimit(19, 18, "WEI_PRECISION")
	var fixedErr *FixedError
	if !errors.As(err, &fixedErr) || fixedErr.Kind != "predicate_violation" {
		t.Fatalf("error = %#v", err)
	}
	const want = "`precision` exceeded maximum `WEI_PRECISION` (18), was 19"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1057
//	test: test_f64_to_fixed_i128_to_fixed
func TestFloat64ToFixedInt128ToFixed(t *testing.T) { checkExactFloatRoundTrip(t, true, 16, 128) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1069
//	test: test_f64_to_fixed_u128_to_fixed
func TestFloat64ToFixedUint128ToFixed(t *testing.T) { checkExactFloatRoundTrip(t, false, 16, 128) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1085
//	test: test_f64_to_fixed_i128_with_precision
func TestFloat64ToFixedInt128WithPrecision(t *testing.T) {
	for _, value := range []float64{123456, 123456.7, 123456.4} {
		checkFloatFormula(t, true, 16, 128, []uint8{0, 1, 2}, value)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1133
//	test: test_f64_to_fixed_i128
func TestFloat64ToFixedInt128(t *testing.T) {
	precisions := make([]uint8, 16)
	for i := range precisions {
		precisions[i] = uint8(i)
	}
	checkFloatFormula(t, true, 16, 128, precisions, 5.555555555555555)
	checkFloatFormula(t, true, 16, 128, precisions, -5.555555555555555)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1171
//	test: test_f64_to_fixed_u64
func TestFloat64ToFixedUint128HighVectors(t *testing.T) {
	precisions := make([]uint8, 17)
	for i := range precisions {
		precisions[i] = uint8(i)
	}
	checkFloatFormula(t, false, 16, 128, precisions, 5.555555555555555)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1192
//	test: test_fixed_i128_to_f64
func TestFixedInt128ToFloat64(t *testing.T) { checkFixedToFloat(t, 16, true, 128) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1199
//	test: test_fixed_u128_to_f64
func TestFixedUint128ToFloat64(t *testing.T) { checkFixedToFloat(t, 16, false, 128) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1242
//	test: test_check_fixed_raw_u128_valid
func TestCheckFixedRawUint128Valid(t *testing.T) { checkRawValues(t, 16, true, false) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1252
//	test: test_check_fixed_raw_u128_invalid
func TestCheckFixedRawUint128Invalid(t *testing.T) { checkRawValues(t, 16, false, false) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1257
//	test: test_check_fixed_raw_u128_at_max_precision
func TestCheckFixedRawUint128AtMaxPrecision(t *testing.T) { checkRawAtMaximum(t, 16, false, 128) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1271
//	test: test_check_fixed_raw_i128_valid
func TestCheckFixedRawInt128Valid(t *testing.T) { checkRawValues(t, 16, true, true) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1280
//	test: test_check_fixed_raw_i128_invalid
func TestCheckFixedRawInt128Invalid(t *testing.T) { checkRawValues(t, 16, false, true) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1285
//	test: test_check_fixed_raw_i128_at_max_precision
func TestCheckFixedRawInt128AtMaxPrecision(t *testing.T) { checkRawAtMaximum(t, 16, true, 128) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1295
//	test: test_f64_to_fixed_i128_overflow_panics
func TestFloat64ToFixedInt128OverflowPanics(t *testing.T) {
	requireFixedPanic(t, "Overflow when scaling f64 to fixed-point i128", func() {
		Float64ToFixedInt128(1e30, 0)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1301
//	test: test_f64_to_fixed_u128_overflow_panics
func TestFloat64ToFixedUint128OverflowPanics(t *testing.T) {
	requireFixedPanic(t, "Overflow when scaling f64 to fixed-point u128", func() {
		Float64ToFixedUint128(1e30, 0)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1315
//	test: test_precision_boundaries
func TestFixedStandardPrecisionBoundaries(t *testing.T) {
	checkPrecisionBoundary(t, 9, "FIXED_PRECISION")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1325
//	test: test_basic_roundtrip
func TestFixedStandardBasicRoundTrip(t *testing.T) {
	checkSignedRoundTrip(t, 9, 64, 0.001, []float64{0, 1, -1})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1336
//	test: test_large_value_roundtrip
func TestFixedStandardLargeValueRoundTrip(t *testing.T) {
	checkSignedRoundTrip(t, 9, 64, 0.0001, []float64{1_000_000, -1_000_000})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1351
//	test: test_precision_specific_values
func TestFixedStandardPrecisionSpecificValues(t *testing.T) { checkStandardPrecisionValues(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1363
//	test: test_unsigned_basic_roundtrip
func TestFixedStandardUnsignedBasicRoundTrip(t *testing.T) { checkUnsignedRoundTrip(t, 9, 64) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1381
//	test: test_rounding
func TestFixedStandardRounding(t *testing.T) {
	for _, test := range []struct {
		precision uint8
		input     float64
		expected  float64
	}{{0, 1.4, 1}, {0, 1.5, 2}, {0, 1.6, 2}, {1, 1.44, 1.4}, {1, 1.45, 1.5}, {1, 1.46, 1.5}, {2, 1.444, 1.44}, {2, 1.445, 1.45}, {2, 1.446, 1.45}} {
		raw := float64ToFixedBig(test.input, test.precision, 9, true, 128, "i128")
		if got := fixedBigToFloat64(raw, 9); math.Abs(got-test.expected) >= 1e-9 {
			t.Fatalf("%v at %d = %v, want %v", test.input, test.precision, got, test.expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1392
//	test: test_special_values
func TestFixedStandardSpecialValues(t *testing.T) {
	if got := float64ToFixedBig(0, 9, 9, true, 128, "i128"); got.Sign() != 0 {
		t.Fatalf("zero = %s", got)
	}
	if got := float64ToFixedBig(math.Copysign(0, -1), 9, 9, true, 128, "i128"); got.Sign() != 0 {
		t.Fatalf("negative zero = %s", got)
	}
	if got := float64ToFixedBig(1e-9, 9, 9, true, 128, "i128"); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("smallest = %s", got)
	}
	raw := float64ToFixedBig(1_000_000_000, 0, 9, true, 128, "i128")
	if got := fixedBigToFloat64(raw, 9); got != 1_000_000_000 {
		t.Fatalf("large = %v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1411
//	test: test_valid_precision
func TestFixedStandardValidPrecision(t *testing.T) {
	if checkFixedPrecisionLimit(0, 9, "FIXED_PRECISION") != nil ||
		checkFixedPrecisionLimit(9, 9, "FIXED_PRECISION") != nil {
		t.Fatal("valid precision rejected")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1417
//	test: test_invalid_precision
func TestFixedStandardInvalidPrecision(t *testing.T) {
	if checkFixedPrecisionLimit(10, 9, "FIXED_PRECISION") == nil {
		t.Fatal("invalid precision accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1432
//	test: test_f64_to_fixed_i64_to_fixed
func TestFloat64ToFixedInt64ToFixed(t *testing.T) { checkExactFloatRoundTrip(t, true, 9, 64) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1443
//	test: test_f64_to_fixed_u64_to_fixed
func TestFloat64ToFixedUint64ToFixed(t *testing.T) { checkExactFloatRoundTrip(t, false, 9, 64) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1459
//	test: test_f64_to_fixed_i64_with_precision
func TestFloat64ToFixedInt64WithPrecision(t *testing.T) {
	tests := []struct {
		precision uint8
		value     float64
		expected  string
	}{
		{0, 123456, "123456000000000"}, {0, 123456.7, "123457000000000"}, {0, 123456.4, "123456000000000"},
		{1, 123456, "123456000000000"}, {1, 123456.7, "123456700000000"}, {1, 123456.4, "123456400000000"},
		{2, 123456, "123456000000000"}, {2, 123456.7, "123456700000000"}, {2, 123456.4, "123456400000000"},
	}
	for _, test := range tests {
		got := float64ToFixedBig(test.value, test.precision, 9, true, 64, "i64")
		if got.String() != test.expected {
			t.Fatalf("%v at %d = %s, want %s", test.value, test.precision, got, test.expected)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1488
//	test: test_f64_to_fixed_i64
func TestFloat64ToFixedInt64(t *testing.T) { checkStandardRoundingVectors(t, true) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1503
//	test: test_f64_to_fixed_u64
func TestFloat64ToFixedUint64(t *testing.T) { checkStandardRoundingVectors(t, false) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1508
//	test: test_fixed_i64_to_f64
func TestFixedInt64ToFloat64(t *testing.T) { checkFixedToFloat(t, 9, true, 64) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1515
//	test: test_fixed_u64_to_f64
func TestFixedUint64ToFloat64(t *testing.T) { checkFixedToFloat(t, 9, false, 64) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1549
//	test: test_check_fixed_raw_u64_valid
func TestCheckFixedRawUint64Valid(t *testing.T) { checkRawValues(t, 9, true, false) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1560
//	test: test_check_fixed_raw_u64_invalid
func TestCheckFixedRawUint64Invalid(t *testing.T) { checkRawValues(t, 9, false, false) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1565
//	test: test_check_fixed_raw_u64_at_max_precision
func TestCheckFixedRawUint64AtMaxPrecision(t *testing.T) { checkRawAtMaximum(t, 9, false, 64) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1579
//	test: test_check_fixed_raw_i64_valid
func TestCheckFixedRawInt64Valid(t *testing.T) { checkRawValues(t, 9, true, true) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1588
//	test: test_check_fixed_raw_i64_invalid
func TestCheckFixedRawInt64Invalid(t *testing.T) { checkRawValues(t, 9, false, true) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1593
//	test: test_check_fixed_raw_i64_at_max_precision
func TestCheckFixedRawInt64AtMaxPrecision(t *testing.T) { checkRawAtMaximum(t, 9, true, 64) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1603
//	test: test_f64_to_fixed_i64_overflow_panics
func TestFloat64ToFixedInt64OverflowPanics(t *testing.T) {
	requireFixedPanic(t, "Overflow when scaling f64 to fixed-point i64", func() {
		float64ToFixedBig(2e18, 0, 9, true, 64, "i64")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1609
//	test: test_f64_to_fixed_u64_overflow_panics
func TestFloat64ToFixedUint64OverflowPanics(t *testing.T) {
	requireFixedPanic(t, "Overflow when scaling f64 to fixed-point u64", func() {
		float64ToFixedBig(2e19, 0, 9, false, 64, "u64")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1885
//	test: prop_checked_mul_div_fixed_matches_u128_full_range
func TestCheckedMulDivFixedMatchesUint128FullRange(t *testing.T) {
	checkStandardMulDivProperties(t, "full")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1898
//	test: prop_checked_mul_div_fixed_matches_u128_final_fit
func TestCheckedMulDivFixedMatchesUint128FinalFit(t *testing.T) {
	checkStandardMulDivProperties(t, "final_fit")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1909
//	test: prop_checked_mul_div_fixed_avoids_u64_phantom_overflow
func TestCheckedMulDivFixedAvoidsUint64PhantomOverflow(t *testing.T) {
	checkStandardMulDivProperties(t, "phantom")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/fixed.rs:1972
//	test: prop_checked_mul_div_fixed_matches_u256_full_range
func TestCheckedMulDivFixedMatchesUint256FullRange(t *testing.T) { checkHighFullRangeMulDiv(t) }
