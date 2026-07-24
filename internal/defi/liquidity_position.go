package defi

import (
	"fmt"
	"math/big"
	"strings"
)

// LiquidityPosition tracks one owner's concentrated-liquidity tick range.
type LiquidityPosition struct {
	Owner                    string
	TickLower, TickUpper     int32
	Liquidity                *big.Int
	FeeGrowthInside0Last     *big.Int
	FeeGrowthInside1Last     *big.Int
	TokensOwed0, TokensOwed1 *big.Int
	TotalAmount0Deposited    *big.Int
	TotalAmount1Deposited    *big.Int
	TotalAmount0Collected    *big.Int
	TotalAmount1Collected    *big.Int
}

func NewLiquidityPosition(owner string, lower, upper int32, liquidity *big.Int) LiquidityPosition {
	normalized := strings.ToLower(owner)
	if !strings.HasPrefix(normalized, "0x") {
		normalized = "0x" + normalized
	}
	absolute := new(big.Int)
	if liquidity != nil {
		absolute.Abs(liquidity)
	}
	return LiquidityPosition{
		Owner: normalized, TickLower: lower, TickUpper: upper, Liquidity: absolute,
		FeeGrowthInside0Last: new(big.Int), FeeGrowthInside1Last: new(big.Int),
		TokensOwed0: new(big.Int), TokensOwed1: new(big.Int),
		TotalAmount0Deposited: new(big.Int), TotalAmount1Deposited: new(big.Int),
		TotalAmount0Collected: new(big.Int), TotalAmount1Collected: new(big.Int),
	}
}

func LiquidityPositionKey(owner string, lower, upper int32) string {
	normalized := strings.ToLower(owner)
	if !strings.HasPrefix(normalized, "0x") {
		normalized = "0x" + normalized
	}
	return fmt.Sprintf("%s:%d:%d", normalized, lower, upper)
}

func (position *LiquidityPosition) UpdateLiquidity(delta *big.Int) {
	if delta == nil {
		return
	}
	next := new(big.Int).Add(position.Liquidity, delta)
	if next.Sign() < 0 {
		next.SetInt64(0)
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	if next.Cmp(max) > 0 {
		next.Set(max)
	}
	position.Liquidity = next
}

func (position *LiquidityPosition) UpdateFees(growth0, growth1 *big.Int) {
	growth0, growth1 = nonNilBig(growth0), nonNilBig(growth1)
	if position.Liquidity.Sign() > 0 {
		q128 := new(big.Int).Lsh(big.NewInt(1), 128)
		delta0 := saturatingSub(growth0, position.FeeGrowthInside0Last)
		delta1 := saturatingSub(growth1, position.FeeGrowthInside1Last)
		earned0 := new(big.Int).Quo(new(big.Int).Mul(delta0, position.Liquidity), q128)
		earned1 := new(big.Int).Quo(new(big.Int).Mul(delta1, position.Liquidity), q128)
		position.TokensOwed0.Add(position.TokensOwed0, truncateU128(earned0))
		position.TokensOwed1.Add(position.TokensOwed1, truncateU128(earned1))
	}
	position.FeeGrowthInside0Last = new(big.Int).Set(growth0)
	position.FeeGrowthInside1Last = new(big.Int).Set(growth1)
}

func (position *LiquidityPosition) CollectFees(amount0, amount1 *big.Int) {
	collected0 := minBig(nonNilBig(amount0), position.TokensOwed0)
	collected1 := minBig(nonNilBig(amount1), position.TokensOwed1)
	position.TokensOwed0.Sub(position.TokensOwed0, collected0)
	position.TokensOwed1.Sub(position.TokensOwed1, collected1)
	position.TotalAmount0Collected.Add(position.TotalAmount0Collected, collected0)
	position.TotalAmount1Collected.Add(position.TotalAmount1Collected, collected1)
}

func (position LiquidityPosition) IsEmpty() bool {
	return position.Liquidity.Sign() == 0 &&
		position.TokensOwed0.Sign() == 0 &&
		position.TokensOwed1.Sign() == 0
}

func nonNilBig(value *big.Int) *big.Int {
	if value == nil {
		return new(big.Int)
	}
	return value
}

func saturatingSub(left, right *big.Int) *big.Int {
	result := new(big.Int).Sub(left, right)
	if result.Sign() < 0 {
		result.SetInt64(0)
	}
	return result
}

func truncateU128(value *big.Int) *big.Int {
	modulus := new(big.Int).Lsh(big.NewInt(1), 128)
	return new(big.Int).Mod(value, modulus)
}

func minBig(left, right *big.Int) *big.Int {
	if left.Cmp(right) <= 0 {
		return new(big.Int).Set(left)
	}
	return new(big.Int).Set(right)
}
