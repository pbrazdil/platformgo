package tickmap

import (
	"math/big"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func sqrtBI(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid integer fixture")
	}
	return result
}

func sqrtAssertBig(t *testing.T, got *big.Int, want string) {
	t.Helper()
	expected := sqrtBI(want)
	if got.Cmp(expected) != 0 {
		t.Fatalf("got %s, want %s", got, expected)
	}
}

func sqrtRequirePanic(t *testing.T, want string, operation func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(recovered.(string), want) {
			t.Fatalf("panic = %v, want text %q", recovered, want)
		}
	}()
	operation()
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:428
//	test: test_if_get_next_sqrt_price_from_input_panic_if_price_zero
func TestIfGetNextSqrtPriceFromInputPanicIfPriceZero(t *testing.T) {
	sqrtRequirePanic(t, "sqrt_price_x96 must be greater than zero", func() {
		GetNextSqrtPriceFromInput(new(big.Int), big.NewInt(1), new(big.Int), true)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:434
//	test: test_if_get_next_sqrt_price_from_input_panic_if_liquidity_zero
func TestIfGetNextSqrtPriceFromInputPanicIfLiquidityZero(t *testing.T) {
	sqrtRequirePanic(t, "Liquidity must be greater than zero", func() {
		GetNextSqrtPriceFromInput(big.NewInt(1), new(big.Int), new(big.Int), true)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:440
//	test: test_if_get_next_sqrt_price_from_input_panics_from_big_price
func TestIfGetNextSqrtPriceFromInputPanicsFromBigPrice(t *testing.T) {
	price := new(big.Int).Sub(MaxU160(), big.NewInt(1))
	sqrtRequirePanic(t, "Uint conversion error: Value is too large for Uint<160>", func() {
		GetNextSqrtPriceFromInput(price, big.NewInt(1024), big.NewInt(1024), false)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:446
//	test: test_any_input_amount_cannot_underflow_the_price
func TestAnyInputAmountCannotUnderflowThePrice(t *testing.T) {
	amountIn := new(big.Int).Lsh(big.NewInt(1), 255)
	result := GetNextSqrtPriceFromInput(big.NewInt(1), big.NewInt(1), amountIn, true)
	sqrtAssertBig(t, result, "1")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:457
//	test: test_returns_input_price_if_amount_in_is_zero_and_zero_for_one_true
func TestReturnsInputPriceIfAmountInIsZeroAndZeroForOneTrue(t *testing.T) {
	price := EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	liquidity := new(big.Int).Quo(ExpandTo18Decimals(1), big.NewInt(10))
	result := GetNextSqrtPriceFromInput(price, liquidity, new(big.Int), true)
	if result.Cmp(price) != 0 {
		t.Fatalf("got %s, want %s", result, price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:465
//	test: test_returns_input_price_if_amount_in_is_zero_and_zero_for_one_false
func TestReturnsInputPriceIfAmountInIsZeroAndZeroForOneFalse(t *testing.T) {
	price := EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	liquidity := new(big.Int).Quo(ExpandTo18Decimals(1), big.NewInt(10))
	result := GetNextSqrtPriceFromInput(price, liquidity, new(big.Int), false)
	if result.Cmp(price) != 0 {
		t.Fatalf("got %s, want %s", result, price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:473
//	test: test_returns_the_minimum_price_for_max_inputs
func TestReturnsTheMinimumPriceForMaxInputs(t *testing.T) {
	sqrtPrice := MaxU160()
	liquidity := MaxU128()
	reserve := new(big.Int).Quo(
		new(big.Int).Lsh(new(big.Int).Set(liquidity), 96),
		sqrtPrice,
	)
	maxAmountNoOverflow := new(big.Int).Sub(MaxU256(), reserve)
	result := GetNextSqrtPriceFromInput(sqrtPrice, liquidity, maxAmountNoOverflow, true)
	sqrtAssertBig(t, result, "1")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:483
//	test: test_input_amount_of_0_1_token1
func TestInputAmountOfPointOneToken1(t *testing.T) {
	amount := new(big.Int).Quo(ExpandTo18Decimals(1), big.NewInt(10))
	result := GetNextSqrtPriceFromInput(
		EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1)),
		ExpandTo18Decimals(1),
		amount,
		false,
	)
	sqrtAssertBig(t, result, "87150978765690771352898345369")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:497
//	test: test_input_amount_of_0_1_token0
func TestInputAmountOfPointOneToken0(t *testing.T) {
	amount := new(big.Int).Quo(ExpandTo18Decimals(1), big.NewInt(10))
	result := GetNextSqrtPriceFromInput(
		EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1)),
		ExpandTo18Decimals(1),
		amount,
		true,
	)
	sqrtAssertBig(t, result, "72025602285694852357767227579")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:511
//	test: test_amount_in_greater_than_uint96_max_and_zero_for_one_true
func TestAmountInGreaterThanUint96MaxAndZeroForOneTrue(t *testing.T) {
	amount := new(big.Int).Lsh(big.NewInt(1), 100)
	result := GetNextSqrtPriceFromInput(
		EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1)),
		ExpandTo18Decimals(10),
		amount,
		true,
	)
	sqrtAssertBig(t, result, "624999999995069620")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:525
//	test: test_can_return_1_with_enough_amount_in_and_zero_for_one_true
func TestCanReturnOneWithEnoughAmountInAndZeroForOneTrue(t *testing.T) {
	amount := new(big.Int).Quo(MaxU256(), big.NewInt(2))
	result := GetNextSqrtPriceFromInput(
		EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1)),
		big.NewInt(1),
		amount,
		true,
	)
	sqrtAssertBig(t, result, "1")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:539
//	test: test_fails_if_output_amount_is_exactly_virtual_reserves_of_token0
func TestFailsIfOutputAmountIsExactlyVirtualReservesOfToken0(t *testing.T) {
	price := sqrtBI("20282409603651670423947251286016")
	sqrtRequirePanic(t, "Invalid conditions for amount0 removal: overflow or underflow detected", func() {
		GetNextSqrtPriceFromOutput(price, big.NewInt(1024), big.NewInt(4), false)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:550
//	test: test_fails_if_output_amount_is_greater_than_virtual_reserves_of_token0
func TestFailsIfOutputAmountIsGreaterThanVirtualReservesOfToken0(t *testing.T) {
	price := sqrtBI("20282409603651670423947251286016")
	sqrtRequirePanic(t, "Invalid conditions for amount0 removal: overflow or underflow detected", func() {
		GetNextSqrtPriceFromOutput(price, big.NewInt(1024), big.NewInt(5), false)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:559
//	test: test_fails_if_output_amount_is_greater_than_virtual_reserves_of_token1
func TestFailsIfOutputAmountIsGreaterThanVirtualReservesOfToken1(t *testing.T) {
	price := sqrtBI("20282409603651670423947251286016")
	sqrtRequirePanic(t, "sqrt_price_x96 must be greater than quotient", func() {
		GetNextSqrtPriceFromOutput(price, big.NewInt(1024), big.NewInt(262145), true)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:568
//	test: test_fails_if_output_amount_is_exactly_virtual_reserves_of_token1
func TestFailsIfOutputAmountIsExactlyVirtualReservesOfToken1(t *testing.T) {
	price := sqrtBI("20282409603651670423947251286016")
	sqrtRequirePanic(t, "sqrt_price_x96 must be greater than quotient", func() {
		GetNextSqrtPriceFromOutput(price, big.NewInt(1024), big.NewInt(262144), true)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:576
//	test: test_succeeds_if_output_amount_is_just_less_than_virtual_reserves_of_token1
func TestSucceedsIfOutputAmountIsJustLessThanVirtualReservesOfToken1(t *testing.T) {
	price := sqrtBI("20282409603651670423947251286016")
	result := GetNextSqrtPriceFromOutput(price, big.NewInt(1024), big.NewInt(262143), true)
	sqrtAssertBig(t, result, "77371252455336267181195264")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:588
//	test: test_returns_input_price_if_amount_out_is_zero_and_zero_for_one_true
func TestReturnsInputPriceIfAmountOutIsZeroAndZeroForOneTrue(t *testing.T) {
	price := EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	liquidity := new(big.Int).Quo(ExpandTo18Decimals(1), big.NewInt(10))
	result := GetNextSqrtPriceFromOutput(price, liquidity, new(big.Int), true)
	if result.Cmp(price) != 0 {
		t.Fatalf("got %s, want %s", result, price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:596
//	test: test_returns_input_price_if_amount_out_is_zero_and_zero_for_one_false
func TestReturnsInputPriceIfAmountOutIsZeroAndZeroForOneFalse(t *testing.T) {
	price := EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	liquidity := new(big.Int).Quo(ExpandTo18Decimals(1), big.NewInt(10))
	result := GetNextSqrtPriceFromOutput(price, liquidity, new(big.Int), false)
	if result.Cmp(price) != 0 {
		t.Fatalf("got %s, want %s", result, price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:604
//	test: test_output_amount_of_0_1_token1_zero_for_one_false
func TestOutputAmountOfPointOneToken1ZeroForOneFalse(t *testing.T) {
	amount := new(big.Int).Quo(ExpandTo18Decimals(1), big.NewInt(10))
	result := GetNextSqrtPriceFromOutput(
		EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1)),
		ExpandTo18Decimals(1),
		amount,
		false,
	)
	sqrtAssertBig(t, result, "88031291682515930659493278152")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:618
//	test: test_output_amount_of_0_1_token1_zero_for_one_true
func TestOutputAmountOfPointOneToken1ZeroForOneTrue(t *testing.T) {
	amount := new(big.Int).Quo(ExpandTo18Decimals(1), big.NewInt(10))
	result := GetNextSqrtPriceFromOutput(
		EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1)),
		ExpandTo18Decimals(1),
		amount,
		true,
	)
	sqrtAssertBig(t, result, "71305346262837903834189555302")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:633
//	test: test_if_get_next_sqrt_price_from_output_panic_if_price_zero
func TestIfGetNextSqrtPriceFromOutputPanicIfPriceZero(t *testing.T) {
	sqrtRequirePanic(t, "sqrt_price_x96 must be greater than zero", func() {
		GetNextSqrtPriceFromOutput(new(big.Int), big.NewInt(1), new(big.Int), true)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:639
//	test: test_if_get_next_sqrt_price_from_output_panic_if_liquidity_zero
func TestIfGetNextSqrtPriceFromOutputPanicIfLiquidityZero(t *testing.T) {
	sqrtRequirePanic(t, "Liquidity must be greater than zero", func() {
		GetNextSqrtPriceFromOutput(big.NewInt(1), new(big.Int), new(big.Int), true)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:644
//	test: test_encode_sqrt_ratio_x98_some_values
func TestEncodeSqrtRatioX98SomeValues(t *testing.T) {
	cases := []struct {
		amount0 string
		amount1 string
		want    string
	}{
		{"1", "1", "79228162514264337593543950336"},
		{"100", "1", "792281625142643375935439503360"},
		{"1", "100", "7922816251426433759354395033"},
		{"111", "333", "45742400955009932534161870629"},
		{"333", "111", "137227202865029797602485611888"},
	}
	for _, test := range cases {
		result := EncodeSqrtRatioX96(sqrtBI(test.amount0), sqrtBI(test.amount1))
		sqrtAssertBig(t, result, test.want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:665
//	test: test_get_amount0_delta_returns_0_if_liquidity_is_0
func TestGetAmount0DeltaReturnsZeroIfLiquidityIsZero(t *testing.T) {
	result := GetAmount0Delta(
		EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1)),
		EncodeSqrtRatioX96(big.NewInt(2), big.NewInt(1)),
		new(big.Int),
		true,
	)
	sqrtAssertBig(t, result, "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:676
//	test: test_get_amount0_delta_returns_0_if_prices_are_equal
func TestGetAmount0DeltaReturnsZeroIfPricesAreEqual(t *testing.T) {
	price := EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	result := GetAmount0Delta(price, price, new(big.Int), true)
	sqrtAssertBig(t, result, "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:687
//	test: test_get_amount0_delta_returns_0_1_amount1_for_price_of_1_to_1_21
func TestGetAmount0DeltaReturnsPointOneAmount1ForPriceOfOneToOnePointTwentyOne(t *testing.T) {
	priceA := EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	priceB := EncodeSqrtRatioX96(big.NewInt(121), big.NewInt(100))
	amountUp := GetAmount0Delta(priceA, priceB, ExpandTo18Decimals(1), true)
	sqrtAssertBig(t, amountUp, "90909090909090910")
	amountDown := GetAmount0Delta(priceA, priceB, ExpandTo18Decimals(1), false)
	if amountDown.Cmp(new(big.Int).Sub(amountUp, big.NewInt(1))) != 0 {
		t.Fatalf("rounded down = %s, rounded up = %s", amountDown, amountUp)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:710
//	test: test_get_amount0_delta_works_for_prices_that_overflow
func TestGetAmount0DeltaWorksForPricesThatOverflow(t *testing.T) {
	priceLow := EncodeSqrtRatioX96(new(big.Int).Lsh(big.NewInt(1), 90), big.NewInt(1))
	priceHigh := EncodeSqrtRatioX96(new(big.Int).Lsh(big.NewInt(1), 96), big.NewInt(1))
	amountUp := GetAmount0Delta(priceLow, priceHigh, ExpandTo18Decimals(1), true)
	amountDown := GetAmount0Delta(priceLow, priceHigh, ExpandTo18Decimals(1), false)
	if amountUp.Cmp(new(big.Int).Add(amountDown, big.NewInt(1))) != 0 {
		t.Fatalf("rounded up = %s, rounded down = %s", amountUp, amountDown)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:726
//	test: test_get_amount1_delta_returns_0_if_liquidity_is_0
func TestGetAmount1DeltaReturnsZeroIfLiquidityIsZero(t *testing.T) {
	result := GetAmount1Delta(
		EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1)),
		EncodeSqrtRatioX96(big.NewInt(2), big.NewInt(1)),
		new(big.Int),
		true,
	)
	sqrtAssertBig(t, result, "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:737
//	test: test_get_amount1_delta_returns_0_if_prices_are_equal
func TestGetAmount1DeltaReturnsZeroIfPricesAreEqual(t *testing.T) {
	price := EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	result := GetAmount1Delta(price, price, new(big.Int), true)
	sqrtAssertBig(t, result, "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:748
//	test: test_get_amount1_delta_returns_0_1_amount1_for_price_of_1_to_1_21
func TestGetAmount1DeltaReturnsPointOneAmount1ForPriceOfOneToOnePointTwentyOne(t *testing.T) {
	priceA := EncodeSqrtRatioX96(big.NewInt(1), big.NewInt(1))
	priceB := EncodeSqrtRatioX96(big.NewInt(121), big.NewInt(100))
	amountUp := GetAmount1Delta(priceA, priceB, ExpandTo18Decimals(1), true)
	sqrtAssertBig(t, amountUp, "100000000000000000")
	amountDown := GetAmount1Delta(priceA, priceB, ExpandTo18Decimals(1), false)
	if amountDown.Cmp(new(big.Int).Sub(amountUp, big.NewInt(1))) != 0 {
		t.Fatalf("rounded down = %s, rounded up = %s", amountDown, amountUp)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/sqrt_price_math.rs:771
//	test: test_decode_sqrt_price_x96_to_price_and_decimal_adjustments
func TestDecodeSqrtPriceX96ToPriceAndDecimalAdjustments(t *testing.T) {
	sqrtPrice := sqrtBI("2018382873588440326581633304624437")
	rawPrice, err := DecodeSqrtPriceX96ToPrice(sqrtPrice)
	if err != nil {
		t.Fatalf("decode raw price: %v", err)
	}
	if rawPrice.Decimal().Cmp(decimal.MustParse("649004842.7013700766389061")) != 0 {
		t.Fatalf("raw price = %s", rawPrice)
	}
	adjustedPrice, err := DecodeSqrtPriceX96ToPriceTokensAdjusted(sqrtPrice, 6, 18, true)
	if err != nil {
		t.Fatalf("decode adjusted price: %v", err)
	}
	if adjustedPrice.Decimal().Cmp(decimal.MustParse("1540.8205520280456880")) != 0 {
		t.Fatalf("adjusted price = %s", adjustedPrice)
	}
}
