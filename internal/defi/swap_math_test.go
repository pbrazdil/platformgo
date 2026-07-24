package defi

import (
	"math/big"
	"testing"

	"github.com/upcomers-org/platformgo/internal/defi/tickmap"
)

func swapBig(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid swap math integer")
	}
	return result
}

func swapAssertBig(t *testing.T, got *big.Int, want string) {
	t.Helper()
	expected := swapBig(want)
	if got.Cmp(expected) != 0 {
		t.Fatalf("got %s, want %s", got, expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/swap_math.rs:202
//	test: test_exact_amount_in_that_gets_capped_at_price_target_in_one_for_zero
func TestExactAmountInThatGetsCappedAtPriceTargetInOneForZero(t *testing.T) {
	price := tickmap.EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	priceTarget := tickmap.EncodeSqrtRatioX96(big.NewInt(101), big.NewInt(100))
	liquidity := tickmap.ExpandTo18Decimals(2)
	amount := tickmap.ExpandTo18Decimals(1)

	result, err := ComputeSwapStep(price, priceTarget, liquidity, amount, 600)
	if err != nil {
		t.Fatalf("compute swap step: %v", err)
	}
	swapAssertBig(t, result.AmountIn, "9975124224178055")
	swapAssertBig(t, result.AmountOut, "9925619580021728")
	swapAssertBig(t, result.FeeAmount, "5988667735148")
	if result.SqrtRatioNextX96.Cmp(priceTarget) != 0 {
		t.Fatalf("next ratio = %s, want %s", result.SqrtRatioNextX96, priceTarget)
	}
	if new(big.Int).Add(result.AmountIn, result.FeeAmount).Cmp(amount) >= 0 {
		t.Fatal("entire amount was unexpectedly used")
	}

	priceAfterWholeInput := tickmap.GetNextSqrtPriceFromInput(
		price,
		liquidity,
		amount,
		false,
	)
	if result.SqrtRatioNextX96.Cmp(priceTarget) != 0 {
		t.Fatalf("next ratio = %s, want target %s", result.SqrtRatioNextX96, priceTarget)
	}
	if result.SqrtRatioNextX96.Cmp(priceAfterWholeInput) >= 0 {
		t.Fatalf("next ratio %s should be below whole-input ratio %s", result.SqrtRatioNextX96, priceAfterWholeInput)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/swap_math.rs:246
//	test: test_exact_amount_in_that_is_fully_spent_in_one_for_zero
func TestExactAmountInThatIsFullySpentInOneForZero(t *testing.T) {
	price := tickmap.EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	priceTarget := tickmap.EncodeSqrtRatioX96(big.NewInt(1000), big.NewInt(100))
	liquidity := tickmap.ExpandTo18Decimals(2)
	amount := tickmap.ExpandTo18Decimals(1)

	result, err := ComputeSwapStep(price, priceTarget, liquidity, amount, 600)
	if err != nil {
		t.Fatalf("compute swap step: %v", err)
	}
	swapAssertBig(t, result.AmountIn, "999400000000000000")
	swapAssertBig(t, result.FeeAmount, "600000000000000")
	swapAssertBig(t, result.AmountOut, "666399946655997866")
	if new(big.Int).Add(result.AmountIn, result.FeeAmount).Cmp(amount) != 0 {
		t.Fatalf("used amount = %s, want %s", new(big.Int).Add(result.AmountIn, result.FeeAmount), amount)
	}

	inputLessFee := new(big.Int).Sub(amount, result.FeeAmount)
	priceAfterWholeInputLessFee := tickmap.GetNextSqrtPriceFromInput(
		price,
		liquidity,
		inputLessFee,
		false,
	)
	if result.SqrtRatioNextX96.Cmp(priceTarget) >= 0 {
		t.Fatalf("next ratio %s should be below target %s", result.SqrtRatioNextX96, priceTarget)
	}
	if result.SqrtRatioNextX96.Cmp(priceAfterWholeInputLessFee) != 0 {
		t.Fatalf("next ratio = %s, want %s", result.SqrtRatioNextX96, priceAfterWholeInputLessFee)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/swap_math.rs:295
//	test: test_exact_amount_out_that_is_fully_received_in_one_for_zero
func TestExactAmountOutThatIsFullyReceivedInOneForZero(t *testing.T) {
	price := tickmap.EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	priceTarget := tickmap.EncodeSqrtRatioX96(big.NewInt(10_000), big.NewInt(100))
	liquidity := tickmap.ExpandTo18Decimals(2)
	amount := tickmap.ExpandTo18Decimals(1)
	negativeAmount := new(big.Int).Neg(new(big.Int).Set(amount))

	result, err := ComputeSwapStep(price, priceTarget, liquidity, negativeAmount, 600)
	if err != nil {
		t.Fatalf("compute swap step: %v", err)
	}
	swapAssertBig(t, result.AmountIn, "2000000000000000000")
	swapAssertBig(t, result.FeeAmount, "1200720432259356")
	if result.AmountOut.Cmp(amount) != 0 {
		t.Fatalf("amount out = %s, want %s", result.AmountOut, amount)
	}

	priceAfterWholeOutput := tickmap.GetNextSqrtPriceFromOutput(
		price,
		liquidity,
		amount,
		false,
	)
	if result.SqrtRatioNextX96.Cmp(priceTarget) >= 0 {
		t.Fatalf("next ratio %s should be below target %s", result.SqrtRatioNextX96, priceTarget)
	}
	if result.SqrtRatioNextX96.Cmp(priceAfterWholeOutput) != 0 {
		t.Fatalf("next ratio = %s, want %s", result.SqrtRatioNextX96, priceAfterWholeOutput)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/swap_math.rs:332
//	test: test_amount_out_is_capped_at_the_desired_amount_out
func TestAmountOutIsCappedAtTheDesiredAmountOut(t *testing.T) {
	result, err := ComputeSwapStep(
		swapBig("417332158212080721273783715441582"),
		swapBig("1452870262520218020823638996"),
		swapBig("159344665391607089467575320103"),
		big.NewInt(-1),
		1,
	)
	if err != nil {
		t.Fatalf("compute swap step: %v", err)
	}
	swapAssertBig(t, result.AmountIn, "1")
	swapAssertBig(t, result.FeeAmount, "1")
	swapAssertBig(t, result.AmountOut, "1")
	swapAssertBig(t, result.SqrtRatioNextX96, "417332158212080721273783715441581")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/swap_math.rs:352
//	test: test_entire_input_amount_taken_as_fee
func TestEntireInputAmountTakenAsFee(t *testing.T) {
	result, err := ComputeSwapStep(
		big.NewInt(2413),
		swapBig("79887613182836312"),
		swapBig("1985041575832132834610021537970"),
		big.NewInt(10),
		1872,
	)
	if err != nil {
		t.Fatalf("compute swap step: %v", err)
	}
	swapAssertBig(t, result.AmountIn, "0")
	swapAssertBig(t, result.FeeAmount, "10")
	swapAssertBig(t, result.AmountOut, "0")
	swapAssertBig(t, result.SqrtRatioNextX96, "2413")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/swap_math.rs:369
//	test: test_handles_intermediate_insufficient_liquidity_in_zero_for_one_exact_output_case
func TestHandlesIntermediateInsufficientLiquidityInZeroForOneExactOutputCase(t *testing.T) {
	sqrtPrice := swapBig("20282409603651670423947251286016")
	sqrtPriceTarget := new(big.Int).Quo(
		new(big.Int).Mul(sqrtPrice, big.NewInt(11)),
		big.NewInt(10),
	)
	result, err := ComputeSwapStep(
		sqrtPrice,
		sqrtPriceTarget,
		big.NewInt(1024),
		big.NewInt(-4),
		3000,
	)
	if err != nil {
		t.Fatalf("compute swap step: %v", err)
	}
	swapAssertBig(t, result.AmountOut, "0")
	if result.SqrtRatioNextX96.Cmp(sqrtPriceTarget) != 0 {
		t.Fatalf("next ratio = %s, want %s", result.SqrtRatioNextX96, sqrtPriceTarget)
	}
	swapAssertBig(t, result.AmountIn, "26215")
	swapAssertBig(t, result.FeeAmount, "79")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/swap_math.rs:389
//	test: test_handles_intermediate_insufficient_liquidity_in_one_for_zero_exact_output_case
func TestHandlesIntermediateInsufficientLiquidityInOneForZeroExactOutputCase(t *testing.T) {
	sqrtPrice := swapBig("20282409603651670423947251286016")
	sqrtPriceTarget := new(big.Int).Quo(
		new(big.Int).Mul(sqrtPrice, big.NewInt(9)),
		big.NewInt(10),
	)
	result, err := ComputeSwapStep(
		sqrtPrice,
		sqrtPriceTarget,
		big.NewInt(1024),
		big.NewInt(-263_000),
		3000,
	)
	if err != nil {
		t.Fatalf("compute swap step: %v", err)
	}
	swapAssertBig(t, result.AmountOut, "26214")
	if result.SqrtRatioNextX96.Cmp(sqrtPriceTarget) != 0 {
		t.Fatalf("next ratio = %s, want %s", result.SqrtRatioNextX96, sqrtPriceTarget)
	}
	swapAssertBig(t, result.AmountIn, "1")
	swapAssertBig(t, result.FeeAmount, "1")
}
