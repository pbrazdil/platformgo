package defi

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/defi/tickmap"
	"github.com/upcomers-org/platformgo/internal/market"
)

type RawSwapData struct {
	Amount0      *big.Int
	Amount1      *big.Int
	SqrtPriceX96 *big.Int
}

func NewRawSwapData(amount0, amount1, sqrtPriceX96 *big.Int) RawSwapData {
	return RawSwapData{
		Amount0:      new(big.Int).Set(amount0),
		Amount1:      new(big.Int).Set(amount1),
		SqrtPriceX96: new(big.Int).Set(sqrtPriceX96),
	}
}

type SwapTradeInfo struct {
	OrderSide       market.OrderSide
	QuantityBase    decimal.Quantity
	QuantityQuote   decimal.Quantity
	SpotPrice       decimal.Price
	ExecutionPrice  decimal.Price
	IsInverted      bool
	SpotPriceBefore *decimal.Price
}

func (s *SwapTradeInfo) SetSpotPriceBefore(price decimal.Price) {
	copy := price
	s.SpotPriceBefore = &copy
}

func (s SwapTradeInfo) GetPriceImpactBPS() (uint32, error) {
	if s.SpotPriceBefore == nil {
		return 0, errors.New("Cannot calculate price impact, the spot price before is not set")
	}
	return swapTradeBasisPoints(s.SpotPrice, *s.SpotPriceBefore)
}

func (s SwapTradeInfo) GetSlippageBPS() (uint32, error) {
	if s.SpotPriceBefore == nil {
		return 0, errors.New("Cannot calculate slippage, the spot price before is not set")
	}
	return swapTradeBasisPoints(s.ExecutionPrice, *s.SpotPriceBefore)
}

type SwapTradeInfoCalculator struct {
	token0      Token
	token1      Token
	rawSwapData RawSwapData
	IsInverted  bool
}

func NewSwapTradeInfoCalculator(token0, token1 Token, rawSwapData RawSwapData) SwapTradeInfoCalculator {
	return SwapTradeInfoCalculator{
		token0: token0,
		token1: token1,
		rawSwapData: NewRawSwapData(
			rawSwapData.Amount0,
			rawSwapData.Amount1,
			rawSwapData.SqrtPriceX96,
		),
		IsInverted: token0.Priority() < token1.Priority(),
	}
}

func (c SwapTradeInfoCalculator) ZeroForOne() bool {
	return c.rawSwapData.Amount0.Sign() > 0
}

func (c SwapTradeInfoCalculator) OrderSide() market.OrderSide {
	if c.IsInverted == c.ZeroForOne() {
		return market.OrderSideBuy
	}
	return market.OrderSideSell
}

func (c SwapTradeInfoCalculator) Compute(sqrtPriceX96Before *big.Int) (SwapTradeInfo, error) {
	quantityBase, err := c.QuantityBase()
	if err != nil {
		return SwapTradeInfo{}, err
	}
	quantityQuote, err := c.QuantityQuote()
	if err != nil {
		return SwapTradeInfo{}, err
	}
	spotPrice, err := c.SpotPrice()
	if err != nil {
		return SwapTradeInfo{}, err
	}
	executionPrice, err := c.ExecutionPrice()
	if err != nil {
		return SwapTradeInfo{}, err
	}

	result := SwapTradeInfo{
		OrderSide:      c.OrderSide(),
		QuantityBase:   quantityBase,
		QuantityQuote:  quantityQuote,
		SpotPrice:      spotPrice,
		ExecutionPrice: executionPrice,
		IsInverted:     c.IsInverted,
	}
	if sqrtPriceX96Before != nil {
		before, decodeErr := tickmap.DecodeSqrtPriceX96ToPriceTokensAdjusted(
			sqrtPriceX96Before,
			c.token0.Decimals,
			c.token1.Decimals,
			c.IsInverted,
		)
		if decodeErr != nil {
			return SwapTradeInfo{}, decodeErr
		}
		result.SetSpotPriceBefore(before)
	}
	return result, nil
}

func (c SwapTradeInfoCalculator) QuantityBase() (decimal.Quantity, error) {
	if c.IsInverted {
		return decimal.QuantityFromU256(swapTradeAbs(c.rawSwapData.Amount1), c.token1.Decimals)
	}
	return decimal.QuantityFromU256(swapTradeAbs(c.rawSwapData.Amount0), c.token0.Decimals)
}

func (c SwapTradeInfoCalculator) QuantityQuote() (decimal.Quantity, error) {
	if c.IsInverted {
		return decimal.QuantityFromU256(swapTradeAbs(c.rawSwapData.Amount0), c.token0.Decimals)
	}
	return decimal.QuantityFromU256(swapTradeAbs(c.rawSwapData.Amount1), c.token1.Decimals)
}

func (c SwapTradeInfoCalculator) SpotPrice() (decimal.Price, error) {
	return tickmap.DecodeSqrtPriceX96ToPriceTokensAdjusted(
		c.rawSwapData.SqrtPriceX96,
		c.token0.Decimals,
		c.token1.Decimals,
		c.IsInverted,
	)
}

func (c SwapTradeInfoCalculator) ExecutionPrice() (decimal.Price, error) {
	amount0 := swapTradeAbs(c.rawSwapData.Amount0)
	amount1 := swapTradeAbs(c.rawSwapData.Amount1)
	if amount0.Sign() == 0 || amount1.Sign() == 0 {
		return decimal.Price{}, errors.New("Cannot calculate execution price with zero amounts")
	}

	var quoteAmount, baseAmount *big.Int
	var quoteDecimals, baseDecimals uint8
	if c.IsInverted {
		quoteAmount, baseAmount = amount0, amount1
		quoteDecimals, baseDecimals = c.token0.Decimals, c.token1.Decimals
	} else {
		quoteAmount, baseAmount = amount1, amount0
		quoteDecimals, baseDecimals = c.token1.Decimals, c.token0.Decimals
	}

	numerator, err := tickmap.MulDiv(quoteAmount, swapTradePower10(baseDecimals), big.NewInt(1))
	if err != nil {
		return decimal.Price{}, err
	}
	numerator, err = tickmap.MulDiv(numerator, swapTradePower10(decimal.MaxPrecision), big.NewInt(1))
	if err != nil {
		return decimal.Price{}, err
	}
	denominator, err := tickmap.MulDiv(baseAmount, swapTradePower10(quoteDecimals), big.NewInt(1))
	if err != nil {
		return decimal.Price{}, err
	}
	raw, err := tickmap.MulDiv(numerator, big.NewInt(1), denominator)
	if err != nil {
		return decimal.Price{}, err
	}
	return swapTradePriceFromRaw(raw, decimal.MaxPrecision)
}

func swapTradeBasisPoints(after, before decimal.Price) (uint32, error) {
	change := after.Decimal().Sub(before.Decimal())
	ratio, err := change.Quo(before.Decimal(), 28, decimal.RoundHalfEven)
	if err != nil {
		return 0, err
	}
	if ratio.Sign() < 0 {
		ratio = ratio.Neg()
	}
	basisPoints := ratio.Mul(decimal.MustParse("10000")).Quantize(0, decimal.RoundHalfEven)
	value, err := strconv.ParseUint(basisPoints.String(), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(value), nil
}

func swapTradePriceFromRaw(raw *big.Int, precision uint8) (decimal.Price, error) {
	if raw.Sign() < 0 {
		return decimal.Price{}, errors.New("negative execution price")
	}
	digits := raw.String()
	scale := int(precision)
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	point := len(digits) - scale
	price, err := decimal.ParsePrice(digits[:point] + "." + digits[point:])
	if err != nil {
		return decimal.Price{}, fmt.Errorf("execution price: %w", err)
	}
	return price, nil
}

func swapTradeAbs(value *big.Int) *big.Int {
	return new(big.Int).Abs(new(big.Int).Set(value))
}

func swapTradePower10(exponent uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(uint64(exponent)), nil)
}
