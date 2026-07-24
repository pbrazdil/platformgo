package defi

import (
	"math/big"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/market"
)

func swapQuoteTokens(t *testing.T) (Token, Token) {
	t.Helper()
	chain, ok := ChainFromName("Arbitrum")
	if !ok {
		t.Fatal("Arbitrum chain fixture is unavailable")
	}
	rain := NewToken(chain, "0x25118290e6A5f4139381D072181157035864099d", "RAIN", "RAIN", 18)
	weth := NewToken(chain, "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1", "Wrapped Ether", "WETH", 18)
	return rain, weth
}

func swapQuoteBig(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid swap quote test integer")
	}
	return result
}

func swapQuoteAssertDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	expected := decimal.MustParse(want)
	if got.Cmp(expected) != 0 {
		t.Fatalf("got %s, want %s", got, expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/quote.rs:322
//	test: test_swap_quote_sell
func TestSwapQuoteSell(t *testing.T) {
	rain, weth := swapQuoteTokens(t)
	amount0 := swapQuoteBig("287175356684998201516914")
	amount1 := swapQuoteBig("-270157537808188649")
	quote := NewSwapQuote(
		ids.InstrumentID{},
		amount0,
		amount1,
		swapQuoteBig("76951769738874829996307631"),
		swapQuoteBig("76812046714213096298497129"),
		-138_746,
		-138_782,
		swapQuoteBig("292285495328044734302670"),
		new(big.Int),
		new(big.Int),
		new(big.Int),
		nil,
	)
	if err := quote.CalculateTradeInfo(rain, weth); err != nil {
		t.Fatalf("calculate trade info: %v", err)
	}
	if quote.TradeInfo == nil {
		t.Fatal("trade info is nil")
	}
	if quote.TradeInfo.OrderSide != market.OrderSideSell {
		t.Fatalf("side = %s, want SELL", quote.TradeInfo.OrderSide)
	}
	if quote.GetInputAmount().Cmp(new(big.Int).Abs(amount0)) != 0 {
		t.Fatalf("input amount = %s", quote.GetInputAmount())
	}
	if quote.GetOutputAmount().Cmp(new(big.Int).Abs(amount1)) != 0 {
		t.Fatalf("output amount = %s", quote.GetOutputAmount())
	}
	swapQuoteAssertDecimal(t, quote.TradeInfo.QuantityBase.Decimal(), "287175.356684998201516914")
	swapQuoteAssertDecimal(t, quote.TradeInfo.QuantityQuote.Decimal(), "0.270157537808188649")
	swapQuoteAssertDecimal(t, quote.TradeInfo.SpotPrice.Decimal(), "0.0000009399386483")
	priceImpact, err := quote.TradeInfo.GetPriceImpactBPS()
	if err != nil || priceImpact != 36 {
		t.Fatalf("price impact = %d, err = %v", priceImpact, err)
	}
	slippage, err := quote.TradeInfo.GetSlippageBPS()
	if err != nil || slippage != 28 {
		t.Fatalf("slippage = %d, err = %v", slippage, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/quote.rs:371
//	test: test_swap_quote_buy
func TestSwapQuoteBuy(t *testing.T) {
	rain, weth := swapQuoteTokens(t)
	amount0 := swapQuoteBig("-117180628248242869089291")
	amount1 := swapQuoteBig("110241020399788696")
	quote := NewSwapQuote(
		ids.InstrumentID{},
		amount0,
		amount1,
		swapQuoteBig("76827576486429933391429745"),
		swapQuoteBig("76857455902960072891859299"),
		-138_778,
		-138_770,
		swapQuoteBig("292285495328044734302670"),
		new(big.Int),
		new(big.Int),
		new(big.Int),
		nil,
	)
	if err := quote.CalculateTradeInfo(rain, weth); err != nil {
		t.Fatalf("calculate trade info: %v", err)
	}
	if quote.TradeInfo == nil {
		t.Fatal("trade info is nil")
	}
	if quote.TradeInfo.OrderSide != market.OrderSideBuy {
		t.Fatalf("side = %s, want BUY", quote.TradeInfo.OrderSide)
	}
	if quote.GetInputAmount().Cmp(new(big.Int).Abs(amount1)) != 0 {
		t.Fatalf("input amount = %s", quote.GetInputAmount())
	}
	if quote.GetOutputAmount().Cmp(new(big.Int).Abs(amount0)) != 0 {
		t.Fatalf("output amount = %s", quote.GetOutputAmount())
	}
	swapQuoteAssertDecimal(t, quote.TradeInfo.QuantityBase.Decimal(), "117180.628248242869089291")
	swapQuoteAssertDecimal(t, quote.TradeInfo.QuantityQuote.Decimal(), "0.110241020399788696")
	swapQuoteAssertDecimal(t, quote.TradeInfo.SpotPrice.Decimal(), "0.000000941050309")
	swapQuoteAssertDecimal(t, quote.TradeInfo.ExecutionPrice.Decimal(), "0.0000009407785403")
	priceImpact, err := quote.TradeInfo.GetPriceImpactBPS()
	if err != nil || priceImpact != 8 {
		t.Fatalf("price impact = %d, err = %v", priceImpact, err)
	}
	slippage, err := quote.TradeInfo.GetSlippageBPS()
	if err != nil || slippage != 5 {
		t.Fatalf("slippage = %d, err = %v", slippage, err)
	}
}
