package defi

import (
	"errors"
	"math/big"

	"github.com/upcomers-org/platformgo/internal/defi/tickmap"
)

const MaxSwapFee uint32 = 1_000_000

type SwapStepResult struct {
	SqrtRatioNextX96 *big.Int
	AmountIn         *big.Int
	AmountOut        *big.Int
	FeeAmount        *big.Int
}

// ComputeSwapStep computes one exact-input or exact-output concentrated
// liquidity swap step. A non-negative amountRemaining denotes exact input.
func ComputeSwapStep(
	sqrtRatioCurrentX96, sqrtRatioTargetX96, liquidity, amountRemaining *big.Int,
	feePips uint32,
) (SwapStepResult, error) {
	if amountRemaining == nil {
		return SwapStepResult{}, errors.New("amount remaining is nil")
	}

	maxFee := big.NewInt(int64(MaxSwapFee))
	fee := new(big.Int).SetUint64(uint64(feePips))
	feeComplement := new(big.Int).Sub(maxFee, fee)
	if feeComplement.Sign() < 0 {
		feeComplement.Add(feeComplement, new(big.Int).Lsh(big.NewInt(1), 256))
	}

	zeroForOne := sqrtRatioCurrentX96.Cmp(sqrtRatioTargetX96) >= 0
	exactIn := amountRemaining.Sign() >= 0
	absoluteRemaining := new(big.Int).Abs(new(big.Int).Set(amountRemaining))

	var (
		sqrtRatioNextX96 *big.Int
		amountIn         = new(big.Int)
		amountOut        = new(big.Int)
	)

	if exactIn {
		amountRemainingLessFee, err := tickmap.MulDiv(
			amountRemaining,
			feeComplement,
			maxFee,
		)
		if err != nil {
			return SwapStepResult{}, err
		}

		if zeroForOne {
			amountIn = tickmap.GetAmount0Delta(
				sqrtRatioTargetX96,
				sqrtRatioCurrentX96,
				liquidity,
				true,
			)
		} else {
			amountIn = tickmap.GetAmount1Delta(
				sqrtRatioCurrentX96,
				sqrtRatioTargetX96,
				liquidity,
				true,
			)
		}

		if amountRemainingLessFee.Cmp(amountIn) >= 0 {
			sqrtRatioNextX96 = new(big.Int).Set(sqrtRatioTargetX96)
		} else {
			sqrtRatioNextX96 = tickmap.GetNextSqrtPriceFromInput(
				sqrtRatioCurrentX96,
				liquidity,
				amountRemainingLessFee,
				zeroForOne,
			)
		}
	} else {
		if zeroForOne {
			amountOut = tickmap.GetAmount1Delta(
				sqrtRatioTargetX96,
				sqrtRatioCurrentX96,
				liquidity,
				false,
			)
		} else {
			amountOut = tickmap.GetAmount0Delta(
				sqrtRatioCurrentX96,
				sqrtRatioTargetX96,
				liquidity,
				false,
			)
		}

		if absoluteRemaining.Cmp(amountOut) >= 0 {
			sqrtRatioNextX96 = new(big.Int).Set(sqrtRatioTargetX96)
		} else {
			sqrtRatioNextX96 = tickmap.GetNextSqrtPriceFromOutput(
				sqrtRatioCurrentX96,
				liquidity,
				absoluteRemaining,
				zeroForOne,
			)
		}
	}

	reachedTarget := sqrtRatioNextX96.Cmp(sqrtRatioTargetX96) == 0
	if zeroForOne {
		if !reachedTarget || !exactIn {
			amountIn = tickmap.GetAmount0Delta(
				sqrtRatioNextX96,
				sqrtRatioCurrentX96,
				liquidity,
				true,
			)
		}
		if !reachedTarget || exactIn {
			amountOut = tickmap.GetAmount1Delta(
				sqrtRatioNextX96,
				sqrtRatioCurrentX96,
				liquidity,
				false,
			)
		}
	} else {
		if !reachedTarget || !exactIn {
			amountIn = tickmap.GetAmount1Delta(
				sqrtRatioCurrentX96,
				sqrtRatioNextX96,
				liquidity,
				true,
			)
		}
		if !reachedTarget || exactIn {
			amountOut = tickmap.GetAmount0Delta(
				sqrtRatioCurrentX96,
				sqrtRatioNextX96,
				liquidity,
				false,
			)
		}
	}

	if !exactIn && amountOut.Cmp(absoluteRemaining) > 0 {
		amountOut.Set(absoluteRemaining)
	}

	var feeAmount *big.Int
	if exactIn && !reachedTarget {
		feeAmount = new(big.Int).Sub(absoluteRemaining, amountIn)
	} else {
		var err error
		feeAmount, err = tickmap.MulDivRoundingUp(amountIn, fee, feeComplement)
		if err != nil {
			return SwapStepResult{}, err
		}
	}

	return SwapStepResult{
		SqrtRatioNextX96: new(big.Int).Set(sqrtRatioNextX96),
		AmountIn:         new(big.Int).Set(amountIn),
		AmountOut:        new(big.Int).Set(amountOut),
		FeeAmount:        new(big.Int).Set(feeAmount),
	}, nil
}
