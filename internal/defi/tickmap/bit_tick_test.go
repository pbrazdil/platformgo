package tickmap

import (
	"math/big"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/bit_math.rs:44
//	test: test_most_significant_bit
func TestMostSignificantBit(t *testing.T) {
	for bit := 0; bit <= 255; bit++ {
		if got := MostSignificantBit(new(big.Int).Lsh(big.NewInt(1), uint(bit))); got != int32(bit) {
			t.Fatalf("bit %d = %d", bit, got)
		}
	}
	for bit := 1; bit <= 255; bit++ {
		value := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bit)), big.NewInt(1))
		if got := MostSignificantBit(value); got != int32(bit-1) {
			t.Fatalf("below bit %d = %d", bit, got)
		}
	}
	if MostSignificantBit(MaxU256()) != 255 {
		t.Fatal("maximum MSB is not 255")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/bit_math.rs:58
//	test: test_least_significant_bit
func TestLeastSignificantBit(t *testing.T) {
	for bit := 0; bit <= 255; bit++ {
		if got := LeastSignificantBit(new(big.Int).Lsh(big.NewInt(1), uint(bit))); got != int32(bit) {
			t.Fatalf("bit %d = %d", bit, got)
		}
	}
	for bit := 1; bit <= 255; bit++ {
		value := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bit)), big.NewInt(1))
		if got := LeastSignificantBit(value); got != 0 {
			t.Fatalf("below bit %d = %d", bit, got)
		}
	}
	if LeastSignificantBit(MaxU256()) != 0 {
		t.Fatal("maximum LSB is not zero")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick.rs:177
//	test: test_update_liquidity_add_remove
func TestPoolTickUpdateLiquidityAddRemove(t *testing.T) {
	tick := NewPoolTick(100)
	tick.Initialized = true
	for _, test := range []struct {
		delta  int64
		gross  int64
		net    int64
		active bool
	}{{1000, 1000, 1000, true}, {500, 1500, 1500, true},
		{-300, 1200, 1200, true}, {-1200, 0, 0, false}} {
		tick.UpdateLiquidity(big.NewInt(test.delta), false)
		if tick.LiquidityGross.Cmp(big.NewInt(test.gross)) != 0 ||
			tick.LiquidityNet.Cmp(big.NewInt(test.net)) != 0 || tick.IsActive() != test.active {
			t.Fatalf("tick after %d = gross %s net %s active %v", test.delta,
				tick.LiquidityGross, tick.LiquidityNet, tick.IsActive())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick.rs:207
//	test: test_update_liquidity_upper_tick
func TestPoolTickUpdateLiquidityUpperTick(t *testing.T) {
	tick := NewPoolTick(200)
	tick.Initialized = true
	tick.UpdateLiquidity(big.NewInt(1000), true)
	if tick.LiquidityGross.Cmp(big.NewInt(1000)) != 0 || tick.LiquidityNet.Cmp(big.NewInt(-1000)) != 0 || !tick.IsActive() {
		t.Fatal("upper tick addition mismatch")
	}
	tick.UpdateLiquidity(big.NewInt(-500), true)
	if tick.LiquidityGross.Cmp(big.NewInt(500)) != 0 || tick.LiquidityNet.Cmp(big.NewInt(-500)) != 0 || !tick.IsActive() {
		t.Fatal("upper tick removal mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick.rs:225
//	test: test_get_max_tick
func TestPoolTickGetMaxTick(t *testing.T) {
	for _, test := range []struct{ spacing, want int32 }{
		{1, 887272}, {10, 887270}, {60, 887220}, {200, 887200},
	} {
		got := GetMaxTick(test.spacing)
		if got != test.want || got%test.spacing != 0 || got > MaxTick {
			t.Errorf("max tick %d = %d", test.spacing, got)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick.rs:252
//	test: test_get_min_tick
func TestPoolTickGetMinTick(t *testing.T) {
	for _, test := range []struct{ spacing, want int32 }{
		{1, -887272}, {10, -887270}, {60, -887220}, {200, -887200},
	} {
		got := GetMinTick(test.spacing)
		if got != test.want || got%test.spacing != 0 || got < MinTick {
			t.Errorf("min tick %d = %d", test.spacing, got)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick.rs:279
//	test: test_tick_spacing_symmetry
func TestPoolTickSpacingSymmetry(t *testing.T) {
	for _, spacing := range []int32{1, 10, 60, 200} {
		maximum, minimum := GetMaxTick(spacing), GetMinTick(spacing)
		if maximum != -minimum || maximum%spacing != 0 || minimum%spacing != 0 ||
			maximum > MaxTick || minimum < MinTick {
			t.Errorf("spacing %d bounds = %d,%d", spacing, minimum, maximum)
		}
	}
}
