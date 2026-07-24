package margin

import "github.com/upcomers-org/platformgo/internal/decimal"

const stopoutCoreMarkFreshForMillis int64 = 30_000

type stopoutCoreClock struct {
	nowMillis int64
}

func newStopoutCoreClock() *stopoutCoreClock {
	return &stopoutCoreClock{nowMillis: 1_000_000}
}

func (clock *stopoutCoreClock) advance(millis int64) {
	clock.nowMillis += millis
}

type stopoutCorePrice struct {
	value    decimal.Decimal
	atMillis int64
}

type stopoutCorePosition struct {
	quantity decimal.Decimal
	entry    decimal.Decimal
	open     bool
}

type stopoutCoreOrder struct {
	intentID string
	quantity decimal.Decimal
	side     string
	status   string
}

// stopoutCoreFixture is a synchronous substitute for the live quote injector,
// runtime, and SQL projections. Its manual clock and injected prices make every
// valuation transition explicit.
type stopoutCoreFixture struct {
	clock           *stopoutCoreClock
	maxLeverage     decimal.Decimal
	scalarUsed      decimal.Decimal
	balance         decimal.Decimal
	position        stopoutCorePosition
	mark            stopoutCorePrice
	quote           stopoutCorePrice
	stopoutOrders   []stopoutCoreOrder
	valuationSource string
	lastValuation   decimal.Decimal
}

func newStopoutCoreFixture() *stopoutCoreFixture {
	return &stopoutCoreFixture{
		clock:       newStopoutCoreClock(),
		maxLeverage: stopoutCoreDecimal("50"),
		scalarUsed:  stopoutCoreDecimal("0.12"),
		balance:     stopoutCoreDecimal("1000"),
	}
}

func stopoutCoreDecimal(text string) decimal.Decimal {
	return decimal.MustParse(text)
}

func stopoutCoreDivide(left, right decimal.Decimal) decimal.Decimal {
	value, err := left.Quo(right, decimal.MaxPrecision, decimal.RoundHalfEven)
	if err != nil {
		panic(err)
	}
	return value.Normalize()
}

func (fixture *stopoutCoreFixture) openLong(quantity, entry decimal.Decimal) {
	fixture.clock.advance(1)
	fixture.position = stopoutCorePosition{
		quantity: quantity,
		entry:    entry,
		open:     true,
	}
}

func (fixture *stopoutCoreFixture) injectMark(mark decimal.Decimal, ageMillis int64) {
	fixture.clock.advance(1)
	fixture.mark = stopoutCorePrice{
		value:    mark,
		atMillis: fixture.clock.nowMillis - ageMillis,
	}
	fixture.evaluate()
}

func (fixture *stopoutCoreFixture) injectQuote(quote decimal.Decimal) {
	fixture.clock.advance(1)
	fixture.quote = stopoutCorePrice{
		value:    quote,
		atMillis: fixture.clock.nowMillis,
	}
	fixture.evaluate()
}

func (fixture *stopoutCoreFixture) setBalance(balance decimal.Decimal) {
	fixture.clock.advance(1)
	fixture.balance = balance
	fixture.evaluate()
}

func (fixture *stopoutCoreFixture) maintenanceAt(mark decimal.Decimal) decimal.Decimal {
	notional := fixture.position.quantity.Mul(mark)
	return stopoutCoreDivide(
		notional,
		stopoutCoreDecimal("2").Mul(fixture.maxLeverage),
	)
}

func (fixture *stopoutCoreFixture) maintenanceCoefficient() decimal.Decimal {
	return stopoutCoreDivide(
		fixture.position.quantity,
		stopoutCoreDecimal("2").Mul(fixture.maxLeverage),
	)
}

func (fixture *stopoutCoreFixture) equityAt(mark decimal.Decimal) decimal.Decimal {
	unrealized := fixture.position.quantity.Mul(mark.Sub(fixture.position.entry))
	return fixture.balance.Add(unrealized)
}

func (fixture *stopoutCoreFixture) liquidationPrice(accountFloor decimal.Decimal) (decimal.Decimal, bool) {
	if !fixture.position.open || accountFloor.Sign() <= 0 {
		return decimal.Decimal{}, false
	}
	denominator := fixture.position.quantity.Sub(fixture.maintenanceCoefficient())
	if denominator.IsZero() {
		return decimal.Decimal{}, false
	}
	equityAboveFloor := fixture.balance.Sub(accountFloor)
	price := fixture.position.entry.Sub(stopoutCoreDivide(equityAboveFloor, denominator))
	return price, price.Sign() > 0
}

func (fixture *stopoutCoreFixture) valuationPrice() (decimal.Decimal, string, bool) {
	if !fixture.mark.value.IsZero() &&
		fixture.clock.nowMillis-fixture.mark.atMillis <= stopoutCoreMarkFreshForMillis {
		return fixture.mark.value, "mark", true
	}
	if !fixture.quote.value.IsZero() {
		return fixture.quote.value, "live_quote", true
	}
	return decimal.Decimal{}, "", false
}

func (fixture *stopoutCoreFixture) evaluate() {
	if !fixture.position.open {
		return
	}
	price, source, ok := fixture.valuationPrice()
	if !ok {
		return
	}
	fixture.valuationSource = source
	fixture.lastValuation = price
	if fixture.equityAt(price).Cmp(fixture.maintenanceAt(price)) <= 0 {
		fixture.liquidate()
	}
}

func (fixture *stopoutCoreFixture) liquidate() {
	if !fixture.position.open {
		return
	}
	fixture.stopoutOrders = append(fixture.stopoutOrders, stopoutCoreOrder{
		intentID: "stopout:1",
		quantity: fixture.position.quantity,
		side:     "SELL",
		status:   "filled",
	})
	fixture.position.open = false
}
