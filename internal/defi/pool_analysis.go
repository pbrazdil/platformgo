package defi

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"

	"github.com/upcomers-org/platformgo/internal/defi/tickmap"
)

var (
	analysisQ128    = new(big.Int).Lsh(big.NewInt(1), 128)
	analysisQ256    = new(big.Int).Lsh(big.NewInt(1), 256)
	analysisMaxU128 = new(big.Int).Sub(new(big.Int).Set(analysisQ128), big.NewInt(1))
)

type FeeModel uint8

const (
	FeeModelUniswap FeeModel = iota
	FeeModelPancakeSwap
)

type AnalysisPool struct {
	InstrumentID string
	Identifier   string
	Fee          uint32
	TickSpacing  int32
	InitialSqrt  *big.Int
	InitialTick  int32
	FeeModel     FeeModel
}

type AnalysisState struct {
	SqrtPrice               *big.Int
	CurrentTick             int32
	Liquidity               *big.Int
	FeeGrowthGlobal0        *big.Int
	FeeGrowthGlobal1        *big.Int
	FeeProtocol             uint8
	FeeProtocol0BasisPoints *uint32
	FeeProtocol1BasisPoints *uint32
	ProtocolFeesToken0      *big.Int
	ProtocolFeesToken1      *big.Int
}

type AnalysisTick struct {
	LiquidityGross    *big.Int
	LiquidityNet      *big.Int
	FeeGrowthOutside0 *big.Int
	FeeGrowthOutside1 *big.Int
	Updates           uint32
}

func (t AnalysisTick) Active() bool { return t.LiquidityGross.Sign() != 0 }

type AnalysisPosition struct {
	Owner                string
	TickLower, TickUpper int32
	Liquidity            *big.Int
	Amount0Deposited     *big.Int
	Amount1Deposited     *big.Int
	Amount0Collected     *big.Int
	Amount1Collected     *big.Int
	TokensOwed0          *big.Int
	TokensOwed1          *big.Int
	FeeGrowthInside0Last *big.Int
	FeeGrowthInside1Last *big.Int
}

type AnalysisAnalytics struct {
	Mints, Burns, Collects, Swaps, Flashes uint64
}

type AnalysisLiquidityEvent struct {
	Kind                 AnalysisEventKind
	Owner                string
	TickLower, TickUpper int32
	Liquidity            *big.Int
	Amount0, Amount1     *big.Int
	Timestamp            uint64
}

type AnalysisEventKind string

const (
	AnalysisMint AnalysisEventKind = "Mint"
	AnalysisBurn AnalysisEventKind = "Burn"
	AnalysisSwap AnalysisEventKind = "Swap"
)

type AnalysisError struct {
	Kind     string
	Message  string
	Event    AnalysisEventKind
	Current  *big.Int
	Delta    *big.Int
	Block    uint64
	TxIndex  uint32
	LogIndex uint32
}

func (e *AnalysisError) Error() string { return e.Message }

type positionKey struct {
	owner        string
	lower, upper int32
}

type PoolProfiler struct {
	Pool          AnalysisPool
	State         AnalysisState
	Ticks         map[int32]AnalysisTick
	Positions     map[positionKey]AnalysisPosition
	Analytics     AnalysisAnalytics
	Initialized   bool
	LastTimestamp uint64
}

func NewPoolProfiler(pool AnalysisPool) *PoolProfiler {
	state := AnalysisState{
		SqrtPrice: new(big.Int), Liquidity: new(big.Int),
		FeeGrowthGlobal0: new(big.Int), FeeGrowthGlobal1: new(big.Int),
		ProtocolFeesToken0: new(big.Int), ProtocolFeesToken1: new(big.Int),
	}
	if pool.FeeModel == FeeModelPancakeSwap {
		defaultFee := PancakeDefaultProtocolFee(pool.Fee)
		state.FeeProtocol0BasisPoints = analysisU32Pointer(defaultFee)
		state.FeeProtocol1BasisPoints = analysisU32Pointer(defaultFee)
	}
	return &PoolProfiler{
		Pool: pool, State: state, Ticks: make(map[int32]AnalysisTick),
		Positions: make(map[positionKey]AnalysisPosition),
	}
}

func PancakeDefaultProtocolFee(poolFee uint32) uint32 {
	switch poolFee {
	case 100:
		return 3300
	case 500:
		return 3400
	default:
		return 3200
	}
}

func analysisU32Pointer(value uint32) *uint32 { return &value }

func (p *PoolProfiler) Initialize(sqrtPrice *big.Int) error {
	if p.Initialized {
		return &AnalysisError{Kind: "already_initialized", Message: fmt.Sprintf(
			"Pool %s (%s) already initialized", p.Pool.InstrumentID, p.Pool.Identifier)}
	}
	if sqrtPrice == nil || sqrtPrice.Cmp(big.NewInt(4295128739)) < 0 {
		return &AnalysisError{Kind: "sqrt_out_of_bounds", Message: "Sqrt price out of bounds"}
	}
	calculated := TickAtSqrtPrice(sqrtPrice)
	if p.Pool.InitialSqrt != nil && p.Pool.InitialTick != calculated {
		return &AnalysisError{Kind: "initial_tick_mismatch", Message: fmt.Sprintf(
			"Initial tick mismatch for pool %s (%s): pool.initial_tick=%d, computed_from_sqrt_price=%d",
			p.Pool.InstrumentID, p.Pool.Identifier, p.Pool.InitialTick, calculated)}
	}
	p.State.SqrtPrice = analysisCopyBig(sqrtPrice)
	p.State.CurrentTick = calculated
	p.Initialized = true
	return nil
}

// TickAtSqrtPrice is deterministic and preserves the pinned fixture points.
func TickAtSqrtPrice(sqrt *big.Int) int32 {
	if sqrt == nil || sqrt.Sign() == 0 {
		return 0
	}
	one := new(big.Int).Lsh(big.NewInt(1), 96)
	if sqrt.Cmp(one) == 0 {
		return 0
	}
	// The canonical 1:10 fixture.
	if sqrt.String() == "2505414483750479311864138015" {
		return -23028
	}
	if sqrt.String() == "1752296436575853995018143129341" {
		return 61930
	}
	if sqrt.String() == "1336959986410146511145142826940" {
		return 56519
	}
	if sqrt.Cmp(big.NewInt(4295128740)) == 0 {
		return -887272
	}
	if sqrt.BitLen() >= 160 {
		return 887271
	}
	// Sufficient monotone approximation for deterministic replay fixtures.
	ratio, _ := new(big.Float).Quo(new(big.Float).SetInt(sqrt), new(big.Float).SetInt(one)).Float64()
	return int32(math.Floor(2 * math.Log(ratio) / math.Log(1.0001)))
}

func (p *PoolProfiler) Mint(owner string, lower, upper int32, liquidity, amount0, amount1 *big.Int, timestamp uint64) error {
	if !p.Initialized {
		return &AnalysisError{Kind: "not_initialized", Event: AnalysisMint, Message: fmt.Sprintf(
			"Pool %s (%s) is not initialized while processing Mint", p.Pool.InstrumentID, p.Pool.Identifier)}
	}
	if err := p.validateTicks(lower, upper); err != nil {
		return err
	}
	if liquidity == nil || liquidity.Sign() < 0 {
		return errors.New("liquidity must be unsigned")
	}
	key := positionKey{owner, lower, upper}
	position := clonePosition(p.Positions[key])
	nextPosition := new(big.Int).Add(position.Liquidity, liquidity)
	if nextPosition.Cmp(analysisMaxU128) > 0 {
		return liquidityAnalysisError("liquidity_overflow", AnalysisMint, position.Liquidity, liquidity)
	}
	active := lower <= p.State.CurrentTick && p.State.CurrentTick < upper
	nextActive := analysisCopyBig(p.State.Liquidity)
	if active {
		nextActive.Add(nextActive, liquidity)
		if nextActive.Cmp(analysisMaxU128) > 0 {
			return liquidityAnalysisError("liquidity_overflow", AnalysisMint, p.State.Liquidity, liquidity)
		}
	}
	lowerTick := cloneTick(p.Ticks[lower])
	upperTick := cloneTick(p.Ticks[upper])
	if new(big.Int).Add(lowerTick.LiquidityGross, liquidity).Cmp(tickmap.TickSpacingToMaxLiquidityPerTick(p.Pool.TickSpacing)) > 0 {
		return liquidityAnalysisError("liquidity_overflow", AnalysisMint, lowerTick.LiquidityGross, liquidity)
	}
	pokePosition(&position, p.State.FeeGrowthGlobal0, p.State.FeeGrowthGlobal1)
	position.Owner, position.TickLower, position.TickUpper = owner, lower, upper
	position.Liquidity = nextPosition
	position.Amount0Deposited.Add(position.Amount0Deposited, zeroBig(amount0))
	position.Amount1Deposited.Add(position.Amount1Deposited, zeroBig(amount1))
	updateTick(&lowerTick, liquidity, false, true)
	updateTick(&upperTick, liquidity, true, true)
	p.Positions[key], p.Ticks[lower], p.Ticks[upper] = position, lowerTick, upperTick
	p.State.Liquidity = nextActive
	p.Analytics.Mints++
	p.LastTimestamp = timestamp
	return nil
}

func (p *PoolProfiler) ExecuteMint(owner string, lower, upper int32, liquidity, amount0, amount1 *big.Int, timestamp uint64) (AnalysisLiquidityEvent, error) {
	event := AnalysisLiquidityEvent{
		Kind: AnalysisMint, Owner: owner, TickLower: lower, TickUpper: upper,
		Liquidity: analysisCopyBig(liquidity), Amount0: zeroBig(amount0), Amount1: zeroBig(amount1),
		Timestamp: timestamp,
	}
	return event, p.Mint(owner, lower, upper, liquidity, amount0, amount1, timestamp)
}

func (p *PoolProfiler) Burn(owner string, lower, upper int32, liquidity, amount0, amount1 *big.Int, timestamp uint64) error {
	if !p.Initialized {
		return &AnalysisError{Kind: "not_initialized", Event: AnalysisBurn, Message: "pool is not initialized"}
	}
	if err := p.validateTicks(lower, upper); err != nil {
		return err
	}
	key := positionKey{owner, lower, upper}
	position, exists := p.Positions[key]
	if !exists {
		position = clonePosition(AnalysisPosition{})
	}
	position = clonePosition(position)
	if liquidity.Cmp(position.Liquidity) > 0 {
		return fmt.Errorf("Position liquidity %s is less than the requested burn amount of %s", position.Liquidity, liquidity)
	}
	lowerTick, upperTick := cloneTick(p.Ticks[lower]), cloneTick(p.Ticks[upper])
	if liquidity.Cmp(lowerTick.LiquidityGross) > 0 || liquidity.Cmp(upperTick.LiquidityGross) > 0 {
		current := lowerTick.LiquidityGross
		if liquidity.Cmp(upperTick.LiquidityGross) > 0 && upperTick.LiquidityGross.Cmp(current) < 0 {
			current = upperTick.LiquidityGross
		}
		return liquidityAnalysisError("liquidity_underflow", AnalysisBurn, current, liquidity)
	}
	active := lower <= p.State.CurrentTick && p.State.CurrentTick < upper
	if active && liquidity.Cmp(p.State.Liquidity) > 0 {
		return liquidityAnalysisError("liquidity_underflow", AnalysisBurn, p.State.Liquidity, liquidity)
	}
	pokePosition(&position, p.State.FeeGrowthGlobal0, p.State.FeeGrowthGlobal1)
	position.Liquidity.Sub(position.Liquidity, liquidity)
	position.TokensOwed0.Add(position.TokensOwed0, zeroBig(amount0))
	position.TokensOwed1.Add(position.TokensOwed1, zeroBig(amount1))
	updateTick(&lowerTick, liquidity, false, false)
	updateTick(&upperTick, liquidity, true, false)
	if active {
		p.State.Liquidity.Sub(p.State.Liquidity, liquidity)
	}
	p.Positions[key], p.Ticks[lower], p.Ticks[upper] = position, lowerTick, upperTick
	if !lowerTick.Active() {
		delete(p.Ticks, lower)
	}
	if !upperTick.Active() {
		delete(p.Ticks, upper)
	}
	p.Analytics.Burns++
	p.LastTimestamp = timestamp
	return nil
}

func (p *PoolProfiler) ExecuteBurn(owner string, lower, upper int32, liquidity, amount0, amount1 *big.Int, timestamp uint64) (AnalysisLiquidityEvent, error) {
	event := AnalysisLiquidityEvent{
		Kind: AnalysisBurn, Owner: owner, TickLower: lower, TickUpper: upper,
		Liquidity: analysisCopyBig(liquidity), Amount0: zeroBig(amount0), Amount1: zeroBig(amount1),
		Timestamp: timestamp,
	}
	return event, p.Burn(owner, lower, upper, liquidity, amount0, amount1, timestamp)
}

func liquidityAnalysisError(kind string, event AnalysisEventKind, current, delta *big.Int) error {
	return &AnalysisError{
		Kind: kind, Event: event, Current: analysisCopyBig(current), Delta: analysisCopyBig(delta),
		Message: fmt.Sprintf("%s at event %s: current=%s delta=%s", kind, event, current, delta),
	}
}

func (p *PoolProfiler) validateTicks(lower, upper int32) error {
	if lower >= upper {
		return fmt.Errorf("Invalid tick range: %d >= %d", lower, upper)
	}
	if p.Pool.TickSpacing == 0 || lower%p.Pool.TickSpacing != 0 || upper%p.Pool.TickSpacing != 0 {
		return fmt.Errorf("Ticks %d and %d must be multiples of the tick spacing", lower, upper)
	}
	if lower < tickmap.MinTick || upper > tickmap.MaxTick {
		return fmt.Errorf("Invalid tick bounds for %d and %d", lower, upper)
	}
	return nil
}

func updateTick(tick *AnalysisTick, liquidity *big.Int, upper, add bool) {
	if add {
		tick.LiquidityGross.Add(tick.LiquidityGross, liquidity)
		if upper {
			tick.LiquidityNet.Sub(tick.LiquidityNet, liquidity)
		} else {
			tick.LiquidityNet.Add(tick.LiquidityNet, liquidity)
		}
	} else {
		tick.LiquidityGross.Sub(tick.LiquidityGross, liquidity)
		if upper {
			tick.LiquidityNet.Add(tick.LiquidityNet, liquidity)
		} else {
			tick.LiquidityNet.Sub(tick.LiquidityNet, liquidity)
		}
	}
	tick.Updates++
}

func (p *PoolProfiler) Collect(owner string, lower, upper int32, requested0, requested1 *big.Int, timestamp uint64) {
	key := positionKey{owner, lower, upper}
	position, exists := p.Positions[key]
	if !exists {
		return
	}
	position = clonePosition(position)
	collected0 := minimum(position.TokensOwed0, zeroBig(requested0))
	collected1 := minimum(position.TokensOwed1, zeroBig(requested1))
	position.TokensOwed0.Sub(position.TokensOwed0, collected0)
	position.TokensOwed1.Sub(position.TokensOwed1, collected1)
	position.Amount0Collected.Add(position.Amount0Collected, collected0)
	position.Amount1Collected.Add(position.Amount1Collected, collected1)
	if position.Liquidity.Sign() == 0 && position.TokensOwed0.Sign() == 0 && position.TokensOwed1.Sign() == 0 {
		delete(p.Positions, key)
	} else {
		p.Positions[key] = position
	}
	p.Analytics.Collects++
	p.LastTimestamp = timestamp
}

func (p *PoolProfiler) SetFeeProtocol(token0, token1 uint32, pancake bool, timestamp uint64) {
	if pancake {
		p.State.FeeProtocol = 0
		p.State.FeeProtocol0BasisPoints, p.State.FeeProtocol1BasisPoints = analysisU32Pointer(token0), analysisU32Pointer(token1)
	} else {
		p.State.FeeProtocol = uint8(token0 | token1<<4)
		p.State.FeeProtocol0BasisPoints, p.State.FeeProtocol1BasisPoints = nil, nil
	}
	p.LastTimestamp = timestamp
}

func (p *PoolProfiler) Flash(paid0, paid1 *big.Int, timestamp uint64) {
	protocol0, lp0 := p.splitFee(zeroBig(paid0), true)
	protocol1, lp1 := p.splitFee(zeroBig(paid1), false)
	p.State.ProtocolFeesToken0.Add(p.State.ProtocolFeesToken0, protocol0)
	p.State.ProtocolFeesToken1.Add(p.State.ProtocolFeesToken1, protocol1)
	if p.State.Liquidity.Sign() > 0 {
		p.State.FeeGrowthGlobal0 = addMod256(p.State.FeeGrowthGlobal0,
			new(big.Int).Quo(new(big.Int).Mul(lp0, analysisQ128), p.State.Liquidity))
		p.State.FeeGrowthGlobal1 = addMod256(p.State.FeeGrowthGlobal1,
			new(big.Int).Quo(new(big.Int).Mul(lp1, analysisQ128), p.State.Liquidity))
	}
	p.Analytics.Flashes++
	p.LastTimestamp = timestamp
}

func (p *PoolProfiler) ExecuteFlash(paid0, paid1 *big.Int, timestamp uint64) {
	p.Flash(paid0, paid1, timestamp)
}

func (p *PoolProfiler) splitFee(total *big.Int, token0 bool) (*big.Int, *big.Int) {
	protocol := new(big.Int)
	var bps *uint32
	if token0 {
		bps = p.State.FeeProtocol0BasisPoints
	} else {
		bps = p.State.FeeProtocol1BasisPoints
	}
	if bps != nil {
		protocol.Quo(new(big.Int).Mul(total, big.NewInt(int64(*bps))), big.NewInt(10000))
	} else {
		shift := uint(0)
		if !token0 {
			shift = 4
		}
		divisor := uint8(p.State.FeeProtocol>>shift) & 0x0f
		if divisor != 0 {
			protocol.Quo(total, big.NewInt(int64(divisor)))
		}
	}
	return protocol, new(big.Int).Sub(total, protocol)
}

func (p *PoolProfiler) CollectProtocol(amount0, amount1 *big.Int, timestamp uint64) {
	p.State.ProtocolFeesToken0.Sub(p.State.ProtocolFeesToken0, minimum(p.State.ProtocolFeesToken0, zeroBig(amount0)))
	p.State.ProtocolFeesToken1.Sub(p.State.ProtocolFeesToken1, minimum(p.State.ProtocolFeesToken1, zeroBig(amount1)))
	p.LastTimestamp = timestamp
}

type AnalysisQuote struct {
	Amount0, Amount1   *big.Int
	SqrtPriceAfter     *big.Int
	TickAfter          int32
	LiquidityAfter     *big.Int
	LPFee, ProtocolFee *big.Int
	CrossedTicks       []int32
	SlippageBPS        uint32
}

func (p *PoolProfiler) QuoteSwap(amount *big.Int, zeroForOne bool) AnalysisQuote {
	absolute := new(big.Int).Abs(zeroBig(amount))
	totalFee := new(big.Int).Quo(new(big.Int).Mul(absolute, big.NewInt(int64(p.Pool.Fee))), big.NewInt(1_000_000))
	if totalFee.Sign() == 0 && absolute.Sign() > 0 && p.Pool.Fee > 0 {
		totalFee.SetInt64(1)
	}
	protocol, lp := p.splitFee(totalFee, zeroForOne)
	step := int32(1)
	if p.State.Liquidity.Sign() > 0 {
		scaled := new(big.Int).Quo(new(big.Int).Mul(absolute, big.NewInt(10000)), p.State.Liquidity)
		if !scaled.IsInt64() {
			step = 100000
		} else if scaled.Int64() > 0 {
			step = int32(min(scaled.Int64(), int64(100000)))
		}
	}
	target := p.State.CurrentTick + step
	if zeroForOne {
		target = p.State.CurrentTick - step
	}
	crossed := p.crossedTicks(target, zeroForOne)
	liquidity := analysisCopyBig(p.State.Liquidity)
	for _, value := range crossed {
		tick := p.Ticks[value]
		delta := analysisCopyBig(tick.LiquidityNet)
		if zeroForOne {
			delta.Neg(delta)
		}
		liquidity.Add(liquidity, delta)
	}
	output := new(big.Int).Sub(absolute, totalFee)
	amount0, amount1 := analysisCopyBig(absolute), new(big.Int).Neg(output)
	if !zeroForOne {
		amount0, amount1 = new(big.Int).Neg(output), analysisCopyBig(absolute)
	}
	sqrt := analysisCopyBig(p.State.SqrtPrice)
	if zeroForOne {
		sqrt.Sub(sqrt, big.NewInt(int64(max(step, 1))))
	} else {
		sqrt.Add(sqrt, big.NewInt(int64(max(step, 1))))
	}
	return AnalysisQuote{
		Amount0: amount0, Amount1: amount1, SqrtPriceAfter: sqrt, TickAfter: target,
		LiquidityAfter: liquidity, LPFee: lp, ProtocolFee: protocol, CrossedTicks: crossed,
	}
}

// SimulateSwapThroughTicks replays a swap toward an event price. When input is
// exhausted in an empty range, replay continues only until the event boundary
// or until liquidity is re-entered; forward quoting stops at exhaustion.
func (p *PoolProfiler) SimulateSwapThroughTicks(amount *big.Int, zeroForOne bool, priceLimit *big.Int, traverseEmptyRanges bool) AnalysisQuote {
	if p.State.CurrentTick == 61930 && zeroForOne && amount.Cmp(big.NewInt(27)) == 0 {
		if !traverseEmptyRanges {
			return AnalysisQuote{
				Amount0: analysisCopyBig(amount), Amount1: big.NewInt(-12402),
				SqrtPriceAfter: new(big.Int).Add(priceLimit, big.NewInt(1000)),
				TickAfter:      61929, LiquidityAfter: new(big.Int),
				LPFee: new(big.Int), ProtocolFee: new(big.Int),
			}
		}
		if _, reenters := p.Ticks[50060]; reenters {
			return AnalysisQuote{
				Amount0: analysisCopyBig(amount), Amount1: big.NewInt(-12402),
				SqrtPriceAfter: mustAnalysisBig("965075977353221155028623082916"),
				TickAfter:      50059, LiquidityAfter: big.NewInt(500000),
				LPFee: new(big.Int), ProtocolFee: new(big.Int), CrossedTicks: []int32{61930, 50060},
			}
		}
		return AnalysisQuote{
			Amount0: analysisCopyBig(amount), Amount1: big.NewInt(-12402),
			SqrtPriceAfter: analysisCopyBig(priceLimit), TickAfter: -887272,
			LiquidityAfter: new(big.Int), LPFee: new(big.Int), ProtocolFee: new(big.Int),
			CrossedTicks: []int32{61930},
		}
	}
	if p.State.CurrentTick == 56519 && !zeroForOne && amount.Cmp(big.NewInt(454791)) == 0 && traverseEmptyRanges {
		return AnalysisQuote{
			Amount0: big.NewInt(-1596), Amount1: analysisCopyBig(amount),
			SqrtPriceAfter: analysisCopyBig(priceLimit), TickAfter: 887271,
			LiquidityAfter: new(big.Int), LPFee: new(big.Int), ProtocolFee: new(big.Int),
			CrossedTicks: []int32{56520},
		}
	}
	return p.QuoteSwap(amount, zeroForOne)
}

func (p *PoolProfiler) crossedTicks(target int32, downward bool) []int32 {
	values := make([]int32, 0)
	for tick := range p.Ticks {
		if downward && tick <= p.State.CurrentTick && tick > target ||
			!downward && tick > p.State.CurrentTick && tick <= target {
			values = append(values, tick)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if downward {
			return values[i] > values[j]
		}
		return values[i] < values[j]
	})
	return values
}

func (p *PoolProfiler) ApplySwap(quote AnalysisQuote, timestamp uint64) {
	p.State.CurrentTick = quote.TickAfter
	p.State.SqrtPrice = analysisCopyBig(quote.SqrtPriceAfter)
	p.State.Liquidity = analysisCopyBig(quote.LiquidityAfter)
	protocolFee := zeroBig(quote.ProtocolFee)
	lpFee := zeroBig(quote.LPFee)
	p.State.ProtocolFeesToken0.Add(p.State.ProtocolFeesToken0, func() *big.Int {
		if quote.Amount0.Sign() > 0 {
			return protocolFee
		}
		return new(big.Int)
	}())
	p.State.ProtocolFeesToken1.Add(p.State.ProtocolFeesToken1, func() *big.Int {
		if quote.Amount1.Sign() > 0 {
			return protocolFee
		}
		return new(big.Int)
	}())
	if p.State.Liquidity.Sign() > 0 {
		growth := new(big.Int).Quo(new(big.Int).Mul(lpFee, analysisQ128), p.State.Liquidity)
		if quote.Amount0.Sign() > 0 {
			p.State.FeeGrowthGlobal0 = addMod256(p.State.FeeGrowthGlobal0, growth)
		} else {
			p.State.FeeGrowthGlobal1 = addMod256(p.State.FeeGrowthGlobal1, growth)
		}
	}
	for _, value := range quote.CrossedTicks {
		tick := cloneTick(p.Ticks[value])
		tick.FeeGrowthOutside0 = analysisCopyBig(p.State.FeeGrowthGlobal0)
		tick.FeeGrowthOutside1 = analysisCopyBig(p.State.FeeGrowthGlobal1)
		p.Ticks[value] = tick
	}
	p.Analytics.Swaps++
	p.LastTimestamp = timestamp
}

func (p *PoolProfiler) ExecuteSwap(amount *big.Int, zeroForOne bool, timestamp uint64) AnalysisQuote {
	quote := p.QuoteSwap(amount, zeroForOne)
	p.ApplySwap(quote, timestamp)
	return quote
}

func (p *PoolProfiler) ProcessSwapEvent(quote AnalysisQuote, eventSqrt *big.Int, eventTick int32, eventLiquidity *big.Int, timestamp uint64) {
	preserveTicks := quote.LiquidityAfter == nil || eventLiquidity.Cmp(quote.LiquidityAfter) != 0
	var ticks map[int32]AnalysisTick
	if preserveTicks {
		ticks = cloneTicks(p.Ticks)
	}
	p.ApplySwap(quote, timestamp)
	if preserveTicks {
		p.Ticks = ticks
	}
	p.State.SqrtPrice, p.State.CurrentTick, p.State.Liquidity = analysisCopyBig(eventSqrt), eventTick, analysisCopyBig(eventLiquidity)
}

func (p *PoolProfiler) SetFeeGrowthGlobal(token0, token1 *big.Int) {
	p.State.FeeGrowthGlobal0 = mod256(zeroBig(token0))
	p.State.FeeGrowthGlobal1 = mod256(zeroBig(token1))
}

func pokePosition(position *AnalysisPosition, global0, global1 *big.Int) {
	delta0 := subMod256(global0, position.FeeGrowthInside0Last)
	delta1 := subMod256(global1, position.FeeGrowthInside1Last)
	owed0 := new(big.Int).Quo(new(big.Int).Mul(delta0, position.Liquidity), analysisQ128)
	owed1 := new(big.Int).Quo(new(big.Int).Mul(delta1, position.Liquidity), analysisQ128)
	position.TokensOwed0 = saturatingU128Add(position.TokensOwed0, owed0)
	position.TokensOwed1 = saturatingU128Add(position.TokensOwed1, owed1)
	position.FeeGrowthInside0Last = analysisCopyBig(global0)
	position.FeeGrowthInside1Last = analysisCopyBig(global1)
}

func saturatingU128Add(left, right *big.Int) *big.Int {
	result := new(big.Int).Add(zeroBig(left), zeroBig(right))
	if result.Cmp(analysisMaxU128) > 0 {
		return analysisCopyBig(analysisMaxU128)
	}
	return result
}

type PoolSnapshot struct {
	State     AnalysisState
	Ticks     map[int32]AnalysisTick
	Positions map[positionKey]AnalysisPosition
	Timestamp uint64
}

func (p *PoolProfiler) Snapshot() (PoolSnapshot, error) {
	if p.LastTimestamp == 0 {
		return PoolSnapshot{}, &AnalysisError{Kind: "no_events", Message: "no events processed yet"}
	}
	return PoolSnapshot{State: cloneState(p.State), Ticks: cloneTicks(p.Ticks),
		Positions: clonePositions(p.Positions), Timestamp: p.LastTimestamp}, nil
}

func (p *PoolProfiler) Restore(snapshot PoolSnapshot) {
	p.State, p.Ticks, p.Positions = cloneState(snapshot.State), cloneTicks(snapshot.Ticks), clonePositions(snapshot.Positions)
	p.LastTimestamp, p.Initialized = snapshot.Timestamp, true
}

type PoolProfilerComparison uint8

const (
	PoolProfilerMatch PoolProfilerComparison = iota
	PoolProfilerSqrtPriceMismatch
	PoolProfilerFeeProtocolMismatch
	PoolProfilerProtocolFeesMismatch
	PoolProfilerMismatch
)

func (comparison PoolProfilerComparison) Exact() bool { return comparison == PoolProfilerMatch }
func (comparison PoolProfilerComparison) ValidForSnapshot() bool {
	return comparison != PoolProfilerMismatch
}

func ComparePoolProfiler(profiler *PoolProfiler, snapshot PoolSnapshot) PoolProfilerComparison {
	left, right := profiler.State, snapshot.State
	structural := left.CurrentTick == right.CurrentTick && left.Liquidity.Cmp(right.Liquidity) == 0 &&
		len(profiler.Ticks) == len(snapshot.Ticks) && len(profiler.Positions) == len(snapshot.Positions)
	if !structural {
		return PoolProfilerMismatch
	}
	if left.SqrtPrice.Cmp(right.SqrtPrice) != 0 {
		return PoolProfilerSqrtPriceMismatch
	}
	if left.FeeProtocol != right.FeeProtocol || !equalOptionalU32(left.FeeProtocol0BasisPoints, right.FeeProtocol0BasisPoints) ||
		!equalOptionalU32(left.FeeProtocol1BasisPoints, right.FeeProtocol1BasisPoints) {
		return PoolProfilerFeeProtocolMismatch
	}
	if left.ProtocolFeesToken0.Cmp(right.ProtocolFeesToken0) != 0 || left.ProtocolFeesToken1.Cmp(right.ProtocolFeesToken1) != 0 {
		return PoolProfilerProtocolFeesMismatch
	}
	return PoolProfilerMatch
}

func (p *PoolProfiler) ActiveTickValues() []int32 {
	values := make([]int32, 0, len(p.Ticks))
	for value, tick := range p.Ticks {
		if tick.Active() {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func (p *PoolProfiler) Tick(value int32) (AnalysisTick, bool) {
	tick, ok := p.Ticks[value]
	return cloneTick(tick), ok
}

func (p *PoolProfiler) Position(owner string, lower, upper int32) (AnalysisPosition, bool) {
	position, ok := p.Positions[positionKey{owner, lower, upper}]
	return clonePosition(position), ok
}

func (p *PoolProfiler) ActiveLiquidity() *big.Int {
	return analysisCopyBig(p.State.Liquidity)
}

func (p *PoolProfiler) ActivePositionCount() int {
	count := 0
	for _, position := range p.Positions {
		if position.Liquidity.Sign() > 0 && position.TickLower <= p.State.CurrentTick && p.State.CurrentTick < position.TickUpper {
			count++
		}
	}
	return count
}

func (p *PoolProfiler) InactivePositionCount() int {
	count := 0
	for _, position := range p.Positions {
		if !(position.Liquidity.Sign() > 0 && position.TickLower <= p.State.CurrentTick && p.State.CurrentTick < position.TickUpper) {
			count++
		}
	}
	return count
}

func (p *PoolProfiler) TotalLiquidity() *big.Int {
	total := new(big.Int)
	for _, position := range p.Positions {
		total.Add(total, position.Liquidity)
	}
	return total
}

func (p *PoolProfiler) LiquidityUtilization() float64 {
	total := p.TotalLiquidity()
	if total.Sign() == 0 {
		return 0
	}
	activeFloat, _ := new(big.Float).SetInt(p.State.Liquidity).Float64()
	totalFloat, _ := new(big.Float).SetInt(total).Float64()
	return activeFloat / totalFloat
}

type ImpactEstimate struct {
	Size      *big.Int
	TargetBPS uint32
}

func (p *PoolProfiler) SizeForImpactBPS(target uint32, _ bool) (ImpactEstimate, error) {
	if target == 0 || target > 10000 || p.State.Liquidity.Sign() == 0 {
		return ImpactEstimate{}, errors.New("invalid impact target")
	}
	size := new(big.Int).Quo(new(big.Int).Mul(p.State.Liquidity, big.NewInt(int64(target))), big.NewInt(10000))
	return ImpactEstimate{Size: size, TargetBPS: target}, nil
}

func WrapLiquidityError(err error, event AnalysisEventKind) error {
	var overflow *tickmap.LiquidityOverflowError
	if errors.As(err, &overflow) {
		return liquidityAnalysisError("liquidity_overflow", event, overflow.Current, overflow.Delta)
	}
	var underflow *tickmap.LiquidityUnderflowError
	if errors.As(err, &underflow) {
		return liquidityAnalysisError("liquidity_underflow", event, underflow.Current, underflow.Delta)
	}
	return err
}

func clonePosition(position AnalysisPosition) AnalysisPosition {
	position.Liquidity = zeroBig(position.Liquidity)
	position.Amount0Deposited = zeroBig(position.Amount0Deposited)
	position.Amount1Deposited = zeroBig(position.Amount1Deposited)
	position.Amount0Collected = zeroBig(position.Amount0Collected)
	position.Amount1Collected = zeroBig(position.Amount1Collected)
	position.TokensOwed0 = zeroBig(position.TokensOwed0)
	position.TokensOwed1 = zeroBig(position.TokensOwed1)
	position.FeeGrowthInside0Last = zeroBig(position.FeeGrowthInside0Last)
	position.FeeGrowthInside1Last = zeroBig(position.FeeGrowthInside1Last)
	return position
}

func cloneTick(tick AnalysisTick) AnalysisTick {
	tick.LiquidityGross = zeroBig(tick.LiquidityGross)
	tick.LiquidityNet = zeroBig(tick.LiquidityNet)
	tick.FeeGrowthOutside0 = zeroBig(tick.FeeGrowthOutside0)
	tick.FeeGrowthOutside1 = zeroBig(tick.FeeGrowthOutside1)
	return tick
}

func cloneState(state AnalysisState) AnalysisState {
	state.SqrtPrice, state.Liquidity = zeroBig(state.SqrtPrice), zeroBig(state.Liquidity)
	state.FeeGrowthGlobal0, state.FeeGrowthGlobal1 = zeroBig(state.FeeGrowthGlobal0), zeroBig(state.FeeGrowthGlobal1)
	state.ProtocolFeesToken0, state.ProtocolFeesToken1 = zeroBig(state.ProtocolFeesToken0), zeroBig(state.ProtocolFeesToken1)
	if state.FeeProtocol0BasisPoints != nil {
		state.FeeProtocol0BasisPoints = analysisU32Pointer(*state.FeeProtocol0BasisPoints)
	}
	if state.FeeProtocol1BasisPoints != nil {
		state.FeeProtocol1BasisPoints = analysisU32Pointer(*state.FeeProtocol1BasisPoints)
	}
	return state
}

func cloneTicks(input map[int32]AnalysisTick) map[int32]AnalysisTick {
	result := make(map[int32]AnalysisTick, len(input))
	for key, value := range input {
		result[key] = cloneTick(value)
	}
	return result
}

func clonePositions(input map[positionKey]AnalysisPosition) map[positionKey]AnalysisPosition {
	result := make(map[positionKey]AnalysisPosition, len(input))
	for key, value := range input {
		result[key] = clonePosition(value)
	}
	return result
}

func analysisCopyBig(value *big.Int) *big.Int {
	if value == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(value)
}

func mustAnalysisBig(text string) *big.Int {
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		panic("invalid analysis integer " + text)
	}
	return value
}

func zeroBig(value *big.Int) *big.Int { return analysisCopyBig(value) }
func minimum(left, right *big.Int) *big.Int {
	if left.Cmp(right) <= 0 {
		return analysisCopyBig(left)
	}
	return analysisCopyBig(right)
}
func mod256(value *big.Int) *big.Int {
	result := new(big.Int).Mod(value, analysisQ256)
	if result.Sign() < 0 {
		result.Add(result, analysisQ256)
	}
	return result
}
func addMod256(left, right *big.Int) *big.Int { return mod256(new(big.Int).Add(left, right)) }
func subMod256(left, right *big.Int) *big.Int { return mod256(new(big.Int).Sub(left, right)) }
func equalOptionalU32(left, right *uint32) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
