package defi

import (
	"math/big"
	"testing"
)

const liquidityOwner = "1234567890123456789012345678901234567890"

func bigValue(value int64) *big.Int { return big.NewInt(value) }
func requireBig(t *testing.T, got *big.Int, want int64) {
	t.Helper()
	if got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("got %s, want %d", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/position.rs:178
//	test: test_new_position
func TestLiquidityPositionNewPosition(t *testing.T) {
	position := NewLiquidityPosition(liquidityOwner, -100, 100, bigValue(1000))
	if position.Owner != "0x"+liquidityOwner || position.TickLower != -100 || position.TickUpper != 100 {
		t.Fatalf("identity fields differ: %+v", position)
	}
	requireBig(t, position.Liquidity, 1000)
	requireBig(t, position.FeeGrowthInside0Last, 0)
	requireBig(t, position.FeeGrowthInside1Last, 0)
	requireBig(t, position.TokensOwed0, 0)
	requireBig(t, position.TokensOwed1, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/position.rs:197
//	test: test_get_position_key
func TestLiquidityPositionGetPositionKey(t *testing.T) {
	key := LiquidityPositionKey(liquidityOwner, -100, 100)
	if want := "0x" + liquidityOwner + ":-100:100"; key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/position.rs:208
//	test: test_update_liquidity_positive
func TestLiquidityPositionUpdateLiquidityPositive(t *testing.T) {
	position := NewLiquidityPosition(liquidityOwner, -100, 100, bigValue(1000))
	position.UpdateLiquidity(bigValue(500))
	requireBig(t, position.Liquidity, 1500)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/position.rs:217
//	test: test_update_liquidity_negative
func TestLiquidityPositionUpdateLiquidityNegative(t *testing.T) {
	position := NewLiquidityPosition(liquidityOwner, -100, 100, bigValue(1000))
	position.UpdateLiquidity(bigValue(-300))
	requireBig(t, position.Liquidity, 700)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/position.rs:226
//	test: test_update_liquidity_negative_saturating
func TestLiquidityPositionUpdateLiquidityNegativeSaturating(t *testing.T) {
	position := NewLiquidityPosition(liquidityOwner, -100, 100, bigValue(1000))
	position.UpdateLiquidity(bigValue(-2000))
	requireBig(t, position.Liquidity, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/position.rs:235
//	test: test_update_fees
func TestLiquidityPositionUpdateFees(t *testing.T) {
	position := NewLiquidityPosition(liquidityOwner, -100, 100, bigValue(1000))
	position.UpdateFees(bigValue(100), bigValue(200))
	requireBig(t, position.FeeGrowthInside0Last, 100)
	requireBig(t, position.FeeGrowthInside1Last, 200)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/position.rs:251
//	test: test_collect_fees
func TestLiquidityPositionCollectFees(t *testing.T) {
	position := NewLiquidityPosition(liquidityOwner, -100, 100, bigValue(1000))
	position.TokensOwed0.SetInt64(100)
	position.TokensOwed1.SetInt64(200)
	position.CollectFees(bigValue(50), bigValue(150))
	requireBig(t, position.TotalAmount0Collected, 50)
	requireBig(t, position.TotalAmount1Collected, 150)
	requireBig(t, position.TokensOwed0, 50)
	requireBig(t, position.TokensOwed1, 50)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/position.rs:269
//	test: test_collect_fees_more_than_owed
func TestLiquidityPositionCollectFeesMoreThanOwed(t *testing.T) {
	position := NewLiquidityPosition(liquidityOwner, -100, 100, bigValue(1000))
	position.TokensOwed0.SetInt64(100)
	position.TokensOwed1.SetInt64(200)
	position.CollectFees(bigValue(150), bigValue(300))
	requireBig(t, position.TotalAmount0Collected, 100)
	requireBig(t, position.TotalAmount1Collected, 200)
	requireBig(t, position.TokensOwed0, 0)
	requireBig(t, position.TokensOwed1, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/position.rs:286
//	test: test_is_empty
func TestLiquidityPositionIsEmpty(t *testing.T) {
	position := NewLiquidityPosition(liquidityOwner, -100, 100, bigValue(0))
	if !position.IsEmpty() {
		t.Fatal("new zero position was not empty")
	}
	position.Liquidity.SetInt64(100)
	if position.IsEmpty() {
		t.Fatal("position with liquidity was empty")
	}
	position.Liquidity.SetInt64(0)
	position.TokensOwed0.SetInt64(50)
	if position.IsEmpty() {
		t.Fatal("position with token0 owed was empty")
	}
	position.TokensOwed0.SetInt64(0)
	position.TokensOwed1.SetInt64(25)
	if position.IsEmpty() {
		t.Fatal("position with token1 owed was empty")
	}
	position.TokensOwed1.SetInt64(0)
	if !position.IsEmpty() {
		t.Fatal("cleared position was not empty")
	}
}
