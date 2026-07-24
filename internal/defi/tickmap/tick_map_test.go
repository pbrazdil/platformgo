package tickmap

import (
	"math/big"
	"strings"
	"testing"
)

func poolTick(value, gross, net int64, fee0, fee1 *big.Int, initialized bool) *PoolTick {
	return &PoolTick{
		Value: value32(value), LiquidityGross: big.NewInt(gross), LiquidityNet: big.NewInt(net),
		FeeGrowthOutside0: new(big.Int).Set(fee0), FeeGrowthOutside1: new(big.Int).Set(fee1),
		Initialized: initialized,
	}
}

func value32(value int64) int32 { return int32(value) }

func assertPair(t *testing.T, got0, got1 *big.Int, want0, want1 int64) {
	t.Helper()
	if got0.Cmp(big.NewInt(want0)) != 0 || got1.Cmp(big.NewInt(want1)) != 0 {
		t.Fatalf("pair = %s,%s want %d,%d", got0, got1, want0, want1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:285
//	test: test_new_tick_maps
func TestNewTickMaps(t *testing.T) {
	tickMap := NewTickMap(1)
	if tickMap.ActiveTickCount() != 0 || tickMap.Liquidity.Sign() != 0 {
		t.Fatalf("new map = active %d liquidity %s", tickMap.ActiveTickCount(), tickMap.Liquidity)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:291
//	test: test_get_fee_growth_inside_uninitialized_ticks
func TestGetFeeGrowthInsideUninitializedTicks(t *testing.T) {
	tickMap := NewTickMap(1)
	for _, test := range []struct {
		current      int32
		want0, want1 int64
	}{{0, 15, 15}, {4, 0, 0}, {-4, 0, 0}} {
		got0, got1 := tickMap.GetFeeGrowthInside(-2, 2, test.current, big.NewInt(15), big.NewInt(15))
		assertPair(t, got0, got1, test.want0, test.want1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:315
//	test: test_get_fee_growth_inside_if_upper_tick_is_below
func TestGetFeeGrowthInsideIfUpperTickIsBelow(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.SetTick(poolTick(2, 0, 0, big.NewInt(2), big.NewInt(3), true))
	got0, got1 := tickMap.GetFeeGrowthInside(-2, 2, 0, big.NewInt(15), big.NewInt(15))
	assertPair(t, got0, got1, 13, 12)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:334
//	test: test_get_fee_growth_inside_if_lower_tick_is_above
func TestGetFeeGrowthInsideIfLowerTickIsAbove(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.SetTick(poolTick(-2, 0, 0, big.NewInt(2), big.NewInt(3), true))
	got0, got1 := tickMap.GetFeeGrowthInside(-2, 2, 0, big.NewInt(15), big.NewInt(15))
	assertPair(t, got0, got1, 13, 12)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:353
//	test: test_get_fee_growth_inside_if_upper_and_lower_tick_are_initialized
func TestGetFeeGrowthInsideIfUpperAndLowerTickAreInitialized(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.SetTick(poolTick(-2, 0, 0, big.NewInt(2), big.NewInt(3), true))
	tickMap.SetTick(poolTick(2, 0, 0, big.NewInt(4), big.NewInt(1), true))
	got0, got1 := tickMap.GetFeeGrowthInside(-2, 2, 0, big.NewInt(15), big.NewInt(15))
	assertPair(t, got0, got1, 9, 11)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:381
//	test: test_get_fee_growth_inside_with_overflow
func TestGetFeeGrowthInsideWithOverflow(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.SetTick(poolTick(-2, 0, 0,
		new(big.Int).Sub(MaxU256(), big.NewInt(3)),
		new(big.Int).Sub(MaxU256(), big.NewInt(2)), true))
	tickMap.SetTick(poolTick(2, 0, 0, big.NewInt(3), big.NewInt(5), true))
	got0, got1 := tickMap.GetFeeGrowthInside(-2, 2, 0, big.NewInt(15), big.NewInt(15))
	assertPair(t, got0, got1, 16, 13)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:409
//	test: test_update_flips_from_zero_to_nonzero
func TestTickMapUpdateFlipsFromZeroToNonzero(t *testing.T) {
	tickMap := NewTickMap(1)
	if tickMap.IsTickInitialized(0) || !tickMap.Update(0, 0, big.NewInt(1), false, new(big.Int), new(big.Int)) ||
		!tickMap.IsTickInitialized(0) {
		t.Fatal("zero-to-nonzero flip mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:421
//	test: test_update_does_not_flip_from_nonzero_to_greater_nonzero
func TestTickMapUpdateDoesNotFlipFromNonzeroToGreaterNonzero(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.Update(0, 0, big.NewInt(1), false, new(big.Int), new(big.Int))
	if tickMap.Update(0, 0, big.NewInt(1), false, new(big.Int), new(big.Int)) ||
		!tickMap.IsTickInitialized(0) {
		t.Fatal("nonzero increase flipped")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:435
//	test: test_update_flips_from_nonzero_to_zero
func TestTickMapUpdateFlipsFromNonzeroToZero(t *testing.T) {
	tickMap := NewTickMap(1)
	if !tickMap.Update(0, 0, big.NewInt(1), false, new(big.Int), new(big.Int)) ||
		!tickMap.Update(0, 0, big.NewInt(-1), false, new(big.Int), new(big.Int)) ||
		tickMap.IsTickInitialized(0) {
		t.Fatal("nonzero-to-zero flip mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:450
//	test: test_update_does_not_flip_from_nonzero_to_lesser_nonzero
func TestTickMapUpdateDoesNotFlipFromNonzeroToLesserNonzero(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.Update(0, 0, big.NewInt(2), false, new(big.Int), new(big.Int))
	if tickMap.Update(0, 0, big.NewInt(-1), false, new(big.Int), new(big.Int)) ||
		!tickMap.IsTickInitialized(0) {
		t.Fatal("partial removal flipped")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:465
//	test: test_update_reverts_if_total_liquidity_gross_exceeds_max
func TestTickMapUpdateRevertsIfTotalLiquidityGrossExceedsMax(t *testing.T) {
	tickMap := NewTickMap(200)
	half := new(big.Int).Quo(tickMap.MaxLiquidityPerTick, big.NewInt(2))
	tickMap.Update(0, 0, half, false, new(big.Int), new(big.Int))
	tickMap.Update(0, 0, half, true, new(big.Int), new(big.Int))
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(recovered.(string), "Liquidity exceeds maximum per tick") {
			t.Fatalf("panic = %v", recovered)
		}
	}()
	tickMap.Update(0, 0, big.NewInt(1), false, new(big.Int), new(big.Int))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:492
//	test: test_update_nets_liquidity_based_on_upper_flag
func TestTickMapUpdateNetsLiquidityBasedOnUpperFlag(t *testing.T) {
	tickMap := NewTickMap(1)
	for _, update := range []struct {
		delta int64
		upper bool
	}{{2, false}, {1, true}, {3, true}, {1, false}} {
		tickMap.Update(0, 0, big.NewInt(update.delta), update.upper, new(big.Int), new(big.Int))
	}
	tick := tickMap.GetTick(0)
	if tick.LiquidityGross.Cmp(big.NewInt(7)) != 0 || tick.LiquidityNet.Cmp(big.NewInt(-1)) != 0 {
		t.Fatalf("tick = gross %s net %s", tick.LiquidityGross, tick.LiquidityNet)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:512
//	test: test_update_assumes_all_growth_happens_below_ticks_lte_current_tick
func TestTickMapUpdateAssumesGrowthBelowTicksLTECurrent(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.Update(1, 1, big.NewInt(1), false, big.NewInt(15), big.NewInt(2))
	tick := tickMap.GetTick(1)
	if tick.FeeGrowthOutside0.Cmp(big.NewInt(15)) != 0 ||
		tick.FeeGrowthOutside1.Cmp(big.NewInt(2)) != 0 || !tick.Initialized ||
		tick.LiquidityGross.Cmp(big.NewInt(1)) != 0 || tick.LiquidityNet.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("tick = %#v", tick)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:530
//	test: test_update_does_not_set_growth_fields_if_tick_already_initialized
func TestTickMapUpdateDoesNotSetGrowthIfAlreadyInitialized(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.Update(1, 1, big.NewInt(1), false, big.NewInt(1), big.NewInt(2))
	tickMap.Update(1, 1, big.NewInt(1), false, big.NewInt(6), big.NewInt(7))
	tick := tickMap.GetTick(1)
	if tick.FeeGrowthOutside0.Cmp(big.NewInt(1)) != 0 ||
		tick.FeeGrowthOutside1.Cmp(big.NewInt(2)) != 0 ||
		tick.LiquidityGross.Cmp(big.NewInt(2)) != 0 || tick.LiquidityNet.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("tick = %#v", tick)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:553
//	test: test_update_does_not_set_growth_fields_for_ticks_gt_current_tick
func TestTickMapUpdateDoesNotSetGrowthForTicksGTCurrent(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.Update(2, 1, big.NewInt(1), false, big.NewInt(1), big.NewInt(2))
	tick := tickMap.GetTick(2)
	if tick.FeeGrowthOutside0.Sign() != 0 || tick.FeeGrowthOutside1.Sign() != 0 ||
		!tick.Initialized || tick.LiquidityGross.Cmp(big.NewInt(1)) != 0 ||
		tick.LiquidityNet.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("tick = %#v", tick)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:571
//	test: test_clear_deletes_all_data_in_tick
func TestTickMapClearDeletesAllDataInTick(t *testing.T) {
	tickMap := NewTickMap(1)
	tickMap.SetTick(poolTick(2, 3, 4, big.NewInt(1), big.NewInt(2), true))
	before := tickMap.GetTick(2)
	if before == nil || before.LiquidityGross.Cmp(big.NewInt(3)) != 0 ||
		before.LiquidityNet.Cmp(big.NewInt(4)) != 0 || !before.Initialized {
		t.Fatalf("before = %#v", before)
	}
	tickMap.Clear(2)
	if tickMap.GetTick(2) != nil {
		t.Fatal("tick remained after clear")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/mod.rs:599
//	test: test_cross_tick_flips_growth_variables
func TestTickMapCrossTickFlipsGrowthVariables(t *testing.T) {
	tickMap := NewTickMap(1)
	tick := poolTick(2, 3, 4, big.NewInt(1), big.NewInt(2), true)
	tick.LastUpdatedBlock = 7
	tickMap.SetTick(tick)
	net := tickMap.CrossTick(2, big.NewInt(7), big.NewInt(9))
	got := tickMap.GetTick(2)
	if got.FeeGrowthOutside0.Cmp(big.NewInt(6)) != 0 ||
		got.FeeGrowthOutside1.Cmp(big.NewInt(7)) != 0 || net.Cmp(big.NewInt(4)) != 0 ||
		got.LiquidityGross.Cmp(big.NewInt(3)) != 0 || got.LiquidityNet.Cmp(big.NewInt(4)) != 0 ||
		!got.Initialized {
		t.Fatalf("crossed tick = %#v net %s", got, net)
	}
}
