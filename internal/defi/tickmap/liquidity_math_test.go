package tickmap

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/liquidity_math.rs:98
//	test: test_add
func TestLiquidityMathAdd(t *testing.T) {
	assertBigEqual(t, LiquidityMathAdd(big.NewInt(1), big.NewInt(0)), big.NewInt(1))
	assertBigEqual(t, LiquidityMathAdd(big.NewInt(1), big.NewInt(1)), big.NewInt(2))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/liquidity_math.rs:104
//	test: test_subtract_one
func TestLiquidityMathSubtractOne(t *testing.T) {
	assertBigEqual(t, LiquidityMathAdd(big.NewInt(1), big.NewInt(-1)), big.NewInt(0))
	assertBigEqual(t, LiquidityMathAdd(big.NewInt(3), big.NewInt(-2)), big.NewInt(1))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/liquidity_math.rs:111
//	test: test_addition_overflow
func TestLiquidityMathAdditionOverflow(t *testing.T) {
	x := new(big.Int).Sub(MaxU128(), big.NewInt(14))
	defer expectLiquidityPanic(t, "Liquidity addition overflow")
	_ = LiquidityMathAdd(x, big.NewInt(15))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/liquidity_math.rs:118
//	test: test_subtraction_underflow_zero
func TestLiquidityMathSubtractionUnderflowZero(t *testing.T) {
	defer expectLiquidityPanic(t, "Liquidity subtraction underflow")
	_ = LiquidityMathAdd(new(big.Int), big.NewInt(-1))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/liquidity_math.rs:124
//	test: test_subtraction_underflow
func TestLiquidityMathSubtractionUnderflow(t *testing.T) {
	defer expectLiquidityPanic(t, "Liquidity subtraction underflow")
	_ = LiquidityMathAdd(big.NewInt(3), big.NewInt(-4))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/liquidity_math.rs:129
//	test: test_try_add_returns_overflow_error
func TestTryLiquidityMathAddReturnsOverflowError(t *testing.T) {
	x := new(big.Int).Sub(MaxU128(), big.NewInt(14))
	_, err := TryLiquidityMathAdd(x, big.NewInt(15))
	var overflow *LiquidityOverflowError
	if !errors.As(err, &overflow) || overflow.Current.Cmp(x) != 0 || overflow.Delta.Cmp(big.NewInt(15)) != 0 {
		t.Fatalf("error = %#v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/liquidity_math.rs:142
//	test: test_try_add_returns_underflow_error
func TestTryLiquidityMathAddReturnsUnderflowError(t *testing.T) {
	_, err := TryLiquidityMathAdd(big.NewInt(3), big.NewInt(-4))
	var underflow *LiquidityUnderflowError
	if !errors.As(err, &underflow) || underflow.Current.Cmp(big.NewInt(3)) != 0 ||
		underflow.Delta.Cmp(big.NewInt(4)) != 0 {
		t.Fatalf("error = %#v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/liquidity_math.rs:154
//	test: test_tick_spacing_to_max_liquidity
func TestTickSpacingToMaxLiquidity(t *testing.T) {
	for _, test := range []struct {
		spacing int32
		want    string
	}{
		{1, "191757530477355301479181766273477"},
		{10, "1917569901783203986719870431555990"},
		{60, "11505743598341114571880798222544994"},
		{200, "38350317471085141830651933667504588"},
	} {
		assertBigEqual(t, TickSpacingToMaxLiquidityPerTick(test.spacing), bi(test.want))
	}
}

func expectLiquidityPanic(t *testing.T, want string) {
	t.Helper()
	if recovered := recover(); recovered == nil || !strings.Contains(recovered.(string), want) {
		t.Fatalf("panic = %v, want %q", recovered, want)
	}
}
