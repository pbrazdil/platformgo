package tickmap

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"
)

func tickMathRequirePanic(t *testing.T, want string, operation func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), want) {
			t.Fatalf("panic = %v, want text %q", recovered, want)
		}
	}()
	operation()
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:262
//	test: test_get_sqrt_ratio_at_tick_zero
func TestGetSqrtRatioAtTickZero(t *testing.T) {
	got := GetSqrtRatioAtTick(0)
	want := new(big.Int).Lsh(big.NewInt(1), 96)
	if got.Cmp(want) != 0 {
		t.Fatalf("sqrt ratio = %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:270
//	test: test_get_tick_at_sqrt_ratio
func TestGetTickAtSqrtRatio(t *testing.T) {
	ratio := new(big.Int).Lsh(big.NewInt(1), 96)
	if got := GetTickAtSqrtRatio(ratio); got != 0 {
		t.Fatalf("tick = %d, want 0", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:278
//	test: test_get_sqrt_ratio_at_tick_panics_above_max
func TestGetSqrtRatioAtTickPanicsAboveMax(t *testing.T) {
	tickMathRequirePanic(t, "Tick 887273 out of bounds", func() {
		GetSqrtRatioAtTick(MaxTick + 1)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:284
//	test: test_get_sqrt_ratio_at_tick_panics_below_min
func TestGetSqrtRatioAtTickPanicsBelowMin(t *testing.T) {
	tickMathRequirePanic(t, "Tick -887273 out of bounds", func() {
		GetSqrtRatioAtTick(MinTick - 1)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:291
//	test: test_get_tick_at_sqrt_ratio_throws_for_too_low
func TestGetTickAtSqrtRatioThrowsForTooLow(t *testing.T) {
	tickMathRequirePanic(t, "Sqrt price out of bounds", func() {
		GetTickAtSqrtRatio(new(big.Int).Sub(MinSqrtRatio(), big.NewInt(1)))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:297
//	test: test_get_tick_at_sqrt_ratio_throws_for_too_high
func TestGetTickAtSqrtRatioThrowsForTooHigh(t *testing.T) {
	tickMathRequirePanic(t, "Sqrt price out of bounds", func() {
		GetTickAtSqrtRatio(MaxSqrtRatio())
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:302
//	test: test_get_tick_at_sqrt_ratio_min_tick
func TestGetTickAtSqrtRatioMinTick(t *testing.T) {
	if got := GetTickAtSqrtRatio(MinSqrtRatio()); got != MinTick {
		t.Fatalf("tick = %d, want %d", got, MinTick)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:308
//	test: test_get_tick_at_sqrt_ration_various_values
func TestGetTickAtSqrtRationVariousValues(t *testing.T) {
	tests := []struct {
		ratio string
		tick  int32
	}{
		{"511495728837967332084595714", -100_860},
		{"14464772219441977173490711849216", 104_148},
		{"17148448136625419841777674413284", 107_552},
	}
	for _, test := range tests {
		if got := GetTickAtSqrtRatio(tickMathBig(test.ratio, 10)); got != test.tick {
			t.Fatalf("ratio %s: tick = %d, want %d", test.ratio, got, test.tick)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:324
//	test: test_get_tick_at_sqrt_ratio_min_tick_plus_one
func TestGetTickAtSqrtRatioMinTickPlusOne(t *testing.T) {
	if got := GetTickAtSqrtRatio(big.NewInt(4_295_343_490)); got != MinTick+1 {
		t.Fatalf("tick = %d, want %d", got, MinTick+1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:330
//	test: test_get_tick_at_sqrt_ratio_max_tick_minus_one
func TestGetTickAtSqrtRatioMaxTickMinusOne(t *testing.T) {
	ratio := tickMathBig("fffa429fbf7baeed2496f0a9f5ccf2bb4abf52f9", 16)
	if got := GetTickAtSqrtRatio(ratio); got != MaxTick-1 {
		t.Fatalf("tick = %d, want %d", got, MaxTick-1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:348
//	test: test_get_tick_at_sqrt_ratio_closest_to_max_tick
func TestGetTickAtSqrtRatioClosestToMaxTick(t *testing.T) {
	ratio := new(big.Int).Sub(MaxSqrtRatio(), big.NewInt(1))
	got := GetTickAtSqrtRatio(ratio)
	if got <= 0 || got >= MaxTick {
		t.Fatalf("tick = %d, want 0 < tick < %d", got, MaxTick)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:374
//	test: test_get_tick_at_sqrt_ratio_accuracy
func TestGetTickAtSqrtRatioAccuracy(t *testing.T) {
	tests := []struct {
		name  string
		ratio *big.Int
	}{
		{"min_sqrt_ratio", MinSqrtRatio()},
		{"price_10_12_to_1", EncodeSqrtRatioX96(big.NewInt(1), tickMathBig("1000000000000", 10))},
		{"price_10_6_to_1", EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1_000_000))},
		{"price_1_to_64", EncodeSqrtRatioX96(big.NewInt(64), big.NewInt(1))},
		{"price_1_to_8", EncodeSqrtRatioX96(big.NewInt(8), big.NewInt(1))},
		{"price_1_to_2", EncodeSqrtRatioX96(big.NewInt(2), big.NewInt(1))},
		{"price_1_to_1", EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))},
		{"price_2_to_1", EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(2))},
		{"price_8_to_1", EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(8))},
		{"price_64_to_1", EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(64))},
		{"price_1_to_10_6", EncodeSqrtRatioX96(big.NewInt(1_000_000), big.NewInt(1))},
		{"price_1_to_10_12", EncodeSqrtRatioX96(tickMathBig("1000000000000", 10), big.NewInt(1))},
		{"max_sqrt_ratio_minus_one", new(big.Int).Sub(MaxSqrtRatio(), big.NewInt(1))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tick := GetTickAtSqrtRatio(test.ratio)
			ratioFloat, _ := new(big.Float).SetInt(test.ratio).Float64()
			price := math.Pow(ratioFloat/math.Ldexp(1, 96), 2)
			theoreticalTick := int32(math.Floor(math.Log(price) / math.Log(1.0001)))
			difference := tick - theoreticalTick
			if difference < 0 {
				difference = -difference
			}
			if difference > 1 {
				t.Fatalf("tick %d differs from theoretical %d by more than 1", tick, theoreticalTick)
			}

			ratioAtTick := GetSqrtRatioAtTick(tick)
			ratioAtNextTick := GetSqrtRatioAtTick(tick + 1)
			if test.ratio.Cmp(ratioAtTick) < 0 {
				t.Fatalf("ratio %s should be >= ratio at tick %s", test.ratio, ratioAtTick)
			}
			if test.ratio.Cmp(ratioAtNextTick) >= 0 {
				t.Fatalf("ratio %s should be < ratio at tick+1 %s", test.ratio, ratioAtNextTick)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:403
//	test: test_get_tick_at_sqrt_ratio_specific_values
func TestGetTickAtSqrtRatioSpecificValues(t *testing.T) {
	tests := []struct {
		ratio *big.Int
		tick  int32
	}{
		{MinSqrtRatio(), MinTick},
		{new(big.Int).Lsh(big.NewInt(1), 96), 0},
	}
	for _, test := range tests {
		if got := GetTickAtSqrtRatio(test.ratio); got != test.tick {
			t.Fatalf("ratio %s: tick = %d, want %d", test.ratio, got, test.tick)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:417
//	test: test_round_trip_tick_sqrt_ratio
func TestRoundTripTickSqrtRatio(t *testing.T) {
	ticks := []int32{-887_272, -100_000, -1000, -100, -1, 0, 1, 100, 1000, 100_000, 700_000}
	for _, originalTick := range ticks {
		ratio := GetSqrtRatioAtTick(originalTick)
		if ratio.Cmp(MaxSqrtRatio()) < 0 {
			if got := GetTickAtSqrtRatio(ratio); got != originalTick {
				t.Fatalf("round trip %d -> %s -> %d", originalTick, ratio, got)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_math.rs:448
//	test: test_extreme_ticks_behavior
func TestExtremeTicksBehavior(t *testing.T) {
	minimum := GetSqrtRatioAtTick(MinTick)
	if minimum.Cmp(MinSqrtRatio()) != 0 {
		t.Fatalf("minimum ratio = %s, want %s", minimum, MinSqrtRatio())
	}
	if got := GetTickAtSqrtRatio(minimum); got != MinTick {
		t.Fatalf("minimum tick = %d, want %d", got, MinTick)
	}

	maximum := GetSqrtRatioAtTick(MaxTick)
	if maximum.Cmp(MaxSqrtRatio()) != 0 {
		t.Fatalf("maximum ratio = %s, want %s", maximum, MaxSqrtRatio())
	}
	maximumValid := new(big.Int).Sub(MaxSqrtRatio(), big.NewInt(1))
	if got := GetTickAtSqrtRatio(maximumValid); got != MaxTick-1 {
		t.Fatalf("maximum valid tick = %d, want %d", got, MaxTick-1)
	}
}
