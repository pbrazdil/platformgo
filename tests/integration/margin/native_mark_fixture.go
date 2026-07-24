package margin

import "fmt"

const nativeMarkScale int64 = 1_000_000

// nativeMarkDecimal is a fixed-point decimal used by the deterministic margin
// fixture. Six fractional digits are enough for the source contract's 0.001
// position while keeping every comparison exact.
type nativeMarkDecimal int64

func nativeMarkWhole(value int64) nativeMarkDecimal {
	return nativeMarkDecimal(value * nativeMarkScale)
}

func (value nativeMarkDecimal) mul(other nativeMarkDecimal) nativeMarkDecimal {
	return nativeMarkDecimal(int64(value) * int64(other) / nativeMarkScale)
}

func (value nativeMarkDecimal) div(other nativeMarkDecimal) nativeMarkDecimal {
	return nativeMarkDecimal(int64(value) * nativeMarkScale / int64(other))
}

func (value nativeMarkDecimal) String() string {
	whole := int64(value) / nativeMarkScale
	fraction := int64(value) % nativeMarkScale
	if fraction == 0 {
		return fmt.Sprintf("%d", whole)
	}
	if fraction < 0 {
		fraction = -fraction
	}
	return fmt.Sprintf("%d.%06d", whole, fraction)
}

type nativeMarkPosition struct {
	entry nativeMarkDecimal
	qty   nativeMarkDecimal
}

type nativeMarkObservation struct {
	price      nativeMarkDecimal
	observedAt int64
}

type nativeMarkMarginEngine struct {
	nowMillis          int64
	maxNativeAgeMillis int64
	maxLeverage        nativeMarkDecimal
	balance            nativeMarkDecimal
	position           *nativeMarkPosition
	nativeMark         *nativeMarkObservation
	liveQuote          nativeMarkDecimal
	stopoutCount       int
	valuationSource    string
}

func newNativeMarkMarginEngine() *nativeMarkMarginEngine {
	return &nativeMarkMarginEngine{
		nowMillis:          1_000_000,
		maxNativeAgeMillis: 30_000,
		maxLeverage:        nativeMarkWhole(50),
	}
}

func (engine *nativeMarkMarginEngine) openLongViaInjectedQuote() (nativeMarkDecimal, nativeMarkDecimal) {
	// A 20,000 limit crosses the injected 10,000 ask immediately.
	entry := nativeMarkWhole(10_000)
	qty := nativeMarkDecimal(1_000) // 0.001000
	engine.liveQuote = entry
	engine.position = &nativeMarkPosition{entry: entry, qty: qty}
	return entry, qty
}

func (engine *nativeMarkMarginEngine) injectNativeMark(price nativeMarkDecimal, ageMillis int64) {
	engine.nativeMark = &nativeMarkObservation{
		price:      price,
		observedAt: engine.nowMillis - ageMillis,
	}
}

func (engine *nativeMarkMarginEngine) injectLiveQuote(price nativeMarkDecimal) {
	engine.liveQuote = price
}

func (engine *nativeMarkMarginEngine) maintenanceFloor() nativeMarkDecimal {
	if engine.position == nil {
		return 0
	}
	// The deterministic Hyperliquid tier uses one quarter of the initial
	// margin at maximum leverage: 10 USDC / 50 / 4 = 0.05 USDC.
	notional := engine.position.qty.mul(engine.position.entry)
	return notional.div(engine.maxLeverage).div(nativeMarkWhole(4))
}

func (engine *nativeMarkMarginEngine) setBalance(balance nativeMarkDecimal) {
	engine.balance = balance
}

func (engine *nativeMarkMarginEngine) liquidationPrice() (nativeMarkDecimal, bool) {
	if engine.position == nil || engine.position.qty <= 0 {
		return 0, false
	}
	floor := engine.maintenanceFloor()
	buffer := engine.balance - floor
	price := engine.position.entry - buffer.div(engine.position.qty)
	return price, price > 0 && price < engine.position.entry
}

func (engine *nativeMarkMarginEngine) valuationMark() nativeMarkDecimal {
	if engine.nativeMark != nil &&
		engine.nowMillis-engine.nativeMark.observedAt <= engine.maxNativeAgeMillis {
		engine.valuationSource = "native"
		return engine.nativeMark.price
	}
	engine.valuationSource = "live_quote"
	return engine.liveQuote
}

func (engine *nativeMarkMarginEngine) evaluateStopout() bool {
	liq, reachable := engine.liquidationPrice()
	if !reachable || engine.valuationMark() > liq {
		return false
	}
	engine.position = nil
	engine.stopoutCount++
	return true
}

func (engine *nativeMarkMarginEngine) openPositions() int {
	if engine.position == nil {
		return 0
	}
	return 1
}
