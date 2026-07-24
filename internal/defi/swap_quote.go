package defi

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/upcomers-org/platformgo/internal/defi/tickmap"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type SwapCrossedTick struct {
	Tick       int32
	ZeroForOne bool
	FeeGrowth0 *big.Int
	FeeGrowth1 *big.Int
}

type SwapQuote struct {
	InstrumentID         ids.InstrumentID
	Amount0              *big.Int
	Amount1              *big.Int
	SqrtPriceBeforeX96   *big.Int
	SqrtPriceAfterX96    *big.Int
	TickBefore           int32
	TickAfter            int32
	LiquidityAfter       *big.Int
	FeeGrowthGlobalAfter *big.Int
	LPFee                *big.Int
	ProtocolFee          *big.Int
	CrossedTicks         []SwapCrossedTick
	TradeInfo            *SwapTradeInfo
}

func NewSwapQuote(
	instrumentID ids.InstrumentID,
	amount0, amount1, sqrtPriceBeforeX96, sqrtPriceAfterX96 *big.Int,
	tickBefore, tickAfter int32,
	liquidityAfter, feeGrowthGlobalAfter, lpFee, protocolFee *big.Int,
	crossedTicks []SwapCrossedTick,
) SwapQuote {
	return SwapQuote{
		InstrumentID:         instrumentID,
		Amount0:              new(big.Int).Set(amount0),
		Amount1:              new(big.Int).Set(amount1),
		SqrtPriceBeforeX96:   new(big.Int).Set(sqrtPriceBeforeX96),
		SqrtPriceAfterX96:    new(big.Int).Set(sqrtPriceAfterX96),
		TickBefore:           tickBefore,
		TickAfter:            tickAfter,
		LiquidityAfter:       new(big.Int).Set(liquidityAfter),
		FeeGrowthGlobalAfter: new(big.Int).Set(feeGrowthGlobalAfter),
		LPFee:                new(big.Int).Set(lpFee),
		ProtocolFee:          new(big.Int).Set(protocolFee),
		CrossedTicks:         append([]SwapCrossedTick(nil), crossedTicks...),
	}
}

func (q *SwapQuote) CalculateTradeInfo(token0, token1 Token) error {
	calculator := NewSwapTradeInfoCalculator(
		token0,
		token1,
		NewRawSwapData(q.Amount0, q.Amount1, q.SqrtPriceAfterX96),
	)
	tradeInfo, err := calculator.Compute(q.SqrtPriceBeforeX96)
	if err != nil {
		return err
	}
	q.TradeInfo = &tradeInfo
	return nil
}

func (q SwapQuote) ZeroForOne() bool {
	return q.Amount0.Sign() > 0
}

func (q SwapQuote) TotalFee() *big.Int {
	total := new(big.Int).Add(q.LPFee, q.ProtocolFee)
	return new(big.Int).And(total, tickmap.MaxU256())
}

func (q SwapQuote) GetEffectiveFeeBPS() uint32 {
	input := q.GetInputAmount()
	if input.Sign() == 0 {
		return 0
	}
	fee, err := tickmap.MulDiv(q.TotalFee(), big.NewInt(10_000), input)
	if err != nil {
		return 0
	}
	return uint32(fee.Uint64())
}

func (q SwapQuote) TotalCrossedTicks() uint32 {
	return uint32(len(q.CrossedTicks))
}

func (q SwapQuote) GetOutputAmount() *big.Int {
	if q.ZeroForOne() {
		return swapTradeAbs(q.Amount1)
	}
	return swapTradeAbs(q.Amount0)
}

func (q SwapQuote) GetInputAmount() *big.Int {
	if q.ZeroForOne() {
		return swapTradeAbs(q.Amount0)
	}
	return swapTradeAbs(q.Amount1)
}

func (q SwapQuote) GetPriceImpactBPS() (uint32, error) {
	if q.TradeInfo == nil {
		return 0, errors.New("Failed to calculate price impact: Trade info is not initialized. Please call calculate_trade_info() first.")
	}
	return q.TradeInfo.GetPriceImpactBPS()
}

func (q SwapQuote) GetSlippageBPS() (uint32, error) {
	if q.TradeInfo == nil {
		return 0, errors.New("Failed to calculate slippage: Trade info is not initialized. Please call calculate_trade_info() first.")
	}
	return q.TradeInfo.GetSlippageBPS()
}

func (q SwapQuote) ValidateSlippageTolerance(maxSlippageBPS uint32) error {
	actual, err := q.GetSlippageBPS()
	if err != nil {
		return err
	}
	if actual > maxSlippageBPS {
		return fmt.Errorf("Slippage %d bps exceeds tolerance %d bps", actual, maxSlippageBPS)
	}
	return nil
}

func (q SwapQuote) ValidateExactOutput(requested *big.Int) error {
	actual := q.GetOutputAmount()
	if actual.Cmp(requested) < 0 {
		return fmt.Errorf("Insufficient liquidity: requested %s, available %s", requested, actual)
	}
	return nil
}
