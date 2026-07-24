package tickmap

import (
	"fmt"
	"math/big"
)

var maxU128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))

func MaxU128() *big.Int { return new(big.Int).Set(maxU128) }

type LiquidityOverflowError struct {
	Current *big.Int
	Delta   *big.Int
}

func (e *LiquidityOverflowError) Error() string {
	return fmt.Sprintf("liquidity addition overflow: current=%s delta=%s", e.Current, e.Delta)
}

type LiquidityUnderflowError struct {
	Current *big.Int
	Delta   *big.Int
}

func (e *LiquidityUnderflowError) Error() string {
	return fmt.Sprintf("liquidity subtraction underflow: current=%s delta=%s", e.Current, e.Delta)
}

func TryLiquidityMathAdd(current, signedDelta *big.Int) (*big.Int, error) {
	if current == nil || current.Sign() < 0 || current.Cmp(maxU128) > 0 {
		return nil, fmt.Errorf("current liquidity is outside u128: %v", current)
	}
	if signedDelta == nil {
		signedDelta = new(big.Int)
	}
	if signedDelta.Sign() < 0 {
		delta := new(big.Int).Abs(new(big.Int).Set(signedDelta))
		if delta.Cmp(current) > 0 {
			return nil, &LiquidityUnderflowError{
				Current: new(big.Int).Set(current),
				Delta:   delta,
			}
		}
		return new(big.Int).Sub(current, delta), nil
	}
	result := new(big.Int).Add(current, signedDelta)
	if result.Cmp(maxU128) > 0 {
		return nil, &LiquidityOverflowError{
			Current: new(big.Int).Set(current),
			Delta:   new(big.Int).Set(signedDelta),
		}
	}
	return result, nil
}

func LiquidityMathAdd(current, signedDelta *big.Int) *big.Int {
	result, err := TryLiquidityMathAdd(current, signedDelta)
	if err == nil {
		return result
	}
	switch err.(type) {
	case *LiquidityOverflowError:
		panic(fmt.Sprintf("Liquidity addition overflow: x=%s, y=%s", current, signedDelta))
	case *LiquidityUnderflowError:
		panic(fmt.Sprintf("Liquidity subtraction underflow: x=%s, y=%s", current, signedDelta))
	default:
		panic(err)
	}
}

func TickSpacingToMaxLiquidityPerTick(tickSpacing int32) *big.Int {
	if tickSpacing == 0 {
		panic("Tick spacing must be non-zero")
	}
	const minTick int32 = -887272
	const maxTick int32 = 887272
	minAligned := (minTick / tickSpacing) * tickSpacing
	maxAligned := (maxTick / tickSpacing) * tickSpacing
	numTicks := (int64(maxAligned)-int64(minAligned))/int64(tickSpacing) + 1
	return new(big.Int).Quo(MaxU128(), big.NewInt(numTicks))
}
