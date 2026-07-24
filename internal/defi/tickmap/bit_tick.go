package tickmap

import "math/big"

const (
	MinTick int32 = -887272
	MaxTick int32 = 887272
)

func MostSignificantBit(value *big.Int) int32 {
	if value == nil || value.Sign() == 0 {
		return 0
	}
	return int32(value.BitLen() - 1)
}

func LeastSignificantBit(value *big.Int) int32 {
	if value == nil || value.Sign() == 0 {
		return 0
	}
	for bit := 0; bit < value.BitLen(); bit++ {
		if value.Bit(bit) == 1 {
			return int32(bit)
		}
	}
	return 0
}

type PoolTick struct {
	Value             int32
	LiquidityGross    *big.Int
	LiquidityNet      *big.Int
	FeeGrowthOutside0 *big.Int
	FeeGrowthOutside1 *big.Int
	Initialized       bool
	LastUpdatedBlock  uint64
	UpdatesCount      uint64
}

func NewPoolTick(value int32) *PoolTick {
	return &PoolTick{
		Value:             value,
		LiquidityGross:    new(big.Int),
		LiquidityNet:      new(big.Int),
		FeeGrowthOutside0: new(big.Int),
		FeeGrowthOutside1: new(big.Int),
	}
}

func (t *PoolTick) UpdateLiquidity(delta *big.Int, upper bool) *big.Int {
	before := new(big.Int).Set(t.LiquidityGross)
	t.LiquidityGross = LiquidityMathAdd(t.LiquidityGross, delta)
	if upper {
		t.LiquidityNet.Sub(t.LiquidityNet, delta)
	} else {
		t.LiquidityNet.Add(t.LiquidityNet, delta)
	}
	t.UpdatesCount++
	return before
}

func (t *PoolTick) IsActive() bool {
	return t.Initialized && t.LiquidityGross.Sign() > 0
}

func (t *PoolTick) Clear() {
	t.LiquidityGross.SetInt64(0)
	t.LiquidityNet.SetInt64(0)
	t.FeeGrowthOutside0.SetInt64(0)
	t.FeeGrowthOutside1.SetInt64(0)
	t.Initialized = false
}

func (t *PoolTick) UpdateFeeGrowth(global0, global1 *big.Int) {
	t.FeeGrowthOutside0.Sub(global0, t.FeeGrowthOutside0)
	t.FeeGrowthOutside1.Sub(global1, t.FeeGrowthOutside1)
}

func GetMaxTick(tickSpacing int32) int32 { return (MaxTick / tickSpacing) * tickSpacing }
func GetMinTick(tickSpacing int32) int32 { return (MinTick / tickSpacing) * tickSpacing }
