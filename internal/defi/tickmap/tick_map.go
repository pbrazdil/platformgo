package tickmap

import "math/big"

type TickMap struct {
	ticks               map[int32]*PoolTick
	bitmap              *TickBitmap
	Liquidity           *big.Int
	MaxLiquidityPerTick *big.Int
}

func NewTickMap(tickSpacing uint32) *TickMap {
	if tickSpacing == 0 {
		panic("Tick spacing must be greater than zero")
	}
	return &TickMap{
		ticks:               make(map[int32]*PoolTick),
		bitmap:              NewTickBitmap(tickSpacing),
		Liquidity:           new(big.Int),
		MaxLiquidityPerTick: TickSpacingToMaxLiquidityPerTick(int32(tickSpacing)),
	}
}

func (m *TickMap) GetTick(value int32) *PoolTick { return m.ticks[value] }

func (m *TickMap) getTickOrInit(value int32) *PoolTick {
	tick := m.ticks[value]
	if tick == nil {
		tick = NewPoolTick(value)
		m.ticks[value] = tick
	}
	return tick
}

func (m *TickMap) GetFeeGrowthInside(
	lowerValue, upperValue, currentValue int32,
	global0, global1 *big.Int,
) (*big.Int, *big.Int) {
	lower := m.getTickOrInit(lowerValue)
	upper := m.getTickOrInit(upperValue)

	below0, below1 := lower.FeeGrowthOutside0, lower.FeeGrowthOutside1
	if currentValue < lower.Value {
		below0 = u256Sub(global0, lower.FeeGrowthOutside0)
		below1 = u256Sub(global1, lower.FeeGrowthOutside1)
	}
	above0, above1 := upper.FeeGrowthOutside0, upper.FeeGrowthOutside1
	if currentValue >= upper.Value {
		above0 = u256Sub(global0, upper.FeeGrowthOutside0)
		above1 = u256Sub(global1, upper.FeeGrowthOutside1)
	}
	inside0 := u256Sub(u256Sub(global0, below0), above0)
	inside1 := u256Sub(u256Sub(global1, below1), above1)
	return inside0, inside1
}

func (m *TickMap) Update(
	value, currentValue int32,
	liquidityDelta *big.Int,
	upper bool,
	global0, global1 *big.Int,
) bool {
	tick := m.getTickOrInit(value)
	before := tick.UpdateLiquidity(liquidityDelta, upper)
	after := tick.LiquidityGross
	if after.Cmp(m.MaxLiquidityPerTick) > 0 {
		panic("Liquidity exceeds maximum per tick")
	}
	if before.Sign() == 0 {
		if tick.Value <= currentValue {
			tick.FeeGrowthOutside0.Set(global0)
			tick.FeeGrowthOutside1.Set(global1)
		}
		tick.Initialized = true
	}
	flipped := (after.Sign() == 0) != (before.Sign() == 0)
	if flipped {
		m.bitmap.FlipTick(value)
	}
	return flipped
}

func (m *TickMap) CrossTick(value int32, global0, global1 *big.Int) *big.Int {
	tick := m.getTickOrInit(value)
	tick.UpdateFeeGrowth(global0, global1)
	return new(big.Int).Set(tick.LiquidityNet)
}

func (m *TickMap) ActiveTickCount() int {
	count := 0
	for value := range m.ticks {
		if m.bitmap.IsInitialized(value) {
			count++
		}
	}
	return count
}

func (m *TickMap) SetTick(tick *PoolTick) { m.ticks[tick.Value] = tick }
func (m *TickMap) Clear(value int32)      { delete(m.ticks, value) }
func (m *TickMap) IsTickInitialized(value int32) bool {
	return m.bitmap.IsInitialized(value)
}

func (m *TickMap) NextInitializedTick(value int32, lessThanOrEqual bool) (int32, bool) {
	return m.bitmap.NextInitializedTickWithinOneWord(value, lessThanOrEqual)
}

func u256Sub(left, right *big.Int) *big.Int {
	result := new(big.Int).Sub(left, right)
	if result.Sign() < 0 {
		result.Add(result, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	return result
}
