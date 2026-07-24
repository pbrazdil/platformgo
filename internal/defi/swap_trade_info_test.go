package defi

import (
	"math/big"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/defi/tickmap"
	"github.com/upcomers-org/platformgo/internal/market"
)

func swapTradeTokens(t *testing.T) (Token, Token) {
	t.Helper()
	chain, ok := ChainFromName("Arbitrum")
	if !ok {
		t.Fatal("Arbitrum chain fixture is unavailable")
	}
	weth := NewToken(chain, "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1", "Wrapped Ether", "WETH", 18)
	usdc := NewToken(chain, "0xFF970A61A04b1cA14834A43f5dE4533eBDDB5CC8", "USD Coin", "USDC", 6)
	return weth, usdc
}

func swapTradeTestBig(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid swap trade test integer")
	}
	return result
}

func swapTradeAssertDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	expected := decimal.MustParse(want)
	if got.Cmp(expected) != 0 {
		t.Fatalf("got %s, want %s", got, expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/swap_trade_info.rs:426
//	test: test_swap_trade_info_calculator_calculations_buy
func TestSwapTradeInfoCalculatorCalculationsBuy(t *testing.T) {
	weth, usdc := swapTradeTokens(t)
	rawData := NewRawSwapData(
		swapTradeTestBig("-466341596920355889"),
		swapTradeTestBig("1656236893"),
		swapTradeTestBig("4720799958938693700000000"),
	)
	calculator := NewSwapTradeInfoCalculator(weth, usdc, rawData)
	result, err := calculator.Compute(nil)
	if err != nil {
		t.Fatalf("compute trade info: %v", err)
	}
	if calculator.IsInverted {
		t.Fatal("WETH/USDC market should not be inverted")
	}
	if result.OrderSide != market.OrderSideBuy {
		t.Fatalf("side = %s, want BUY", result.OrderSide)
	}
	swapTradeAssertDecimal(t, result.QuantityBase.Decimal(), "0.466341596920355889")
	swapTradeAssertDecimal(t, result.QuantityQuote.Decimal(), "1656.236893")
	swapTradeAssertDecimal(t, result.SpotPrice.Decimal(), "3550.3570265047994091")
	swapTradeAssertDecimal(t, result.ExecutionPrice.Decimal(), "3551.5529902061477063")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/swap_trade_info.rs:453
//	test: test_swap_trade_info_calculator_calculations_sell
func TestSwapTradeInfoCalculatorCalculationsSell(t *testing.T) {
	weth, usdc := swapTradeTokens(t)
	rawData := NewRawSwapData(
		swapTradeTestBig("193450074461093702"),
		swapTradeTestBig("-691892530"),
		swapTradeTestBig("4739235524363817533004858"),
	)
	calculator := NewSwapTradeInfoCalculator(weth, usdc, rawData)
	result, err := calculator.Compute(nil)
	if err != nil {
		t.Fatalf("compute trade info: %v", err)
	}
	if result.OrderSide != market.OrderSideSell {
		t.Fatalf("side = %s, want SELL", result.OrderSide)
	}
	swapTradeAssertDecimal(t, result.QuantityBase.Decimal(), "0.193450074461093702")
	swapTradeAssertDecimal(t, result.QuantityQuote.Decimal(), "691.89253")
	swapTradeAssertDecimal(t, result.SpotPrice.Decimal(), "3578.1407251651610105")
	swapTradeAssertDecimal(t, result.ExecutionPrice.Decimal(), "3576.5947980503469024")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/swap_trade_info.rs:478
//	test: test_swap_trade_info_calculator_spot_price_overflow_is_recoverable
func TestSwapTradeInfoCalculatorSpotPriceOverflowIsRecoverable(t *testing.T) {
	weth, usdc := swapTradeTokens(t)
	rawData := NewRawSwapData(
		big.NewInt(1),
		big.NewInt(-1),
		new(big.Int).Sub(tickmap.MaxSqrtRatio(), big.NewInt(1)),
	)
	calculator := NewSwapTradeInfoCalculator(weth, usdc, rawData)
	if _, err := calculator.Compute(nil); err == nil {
		t.Fatal("expected recoverable spot-price overflow")
	}
}
