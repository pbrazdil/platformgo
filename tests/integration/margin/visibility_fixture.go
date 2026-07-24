package margin

import (
	"fmt"
	"math/big"
	"strings"
)

type visibilityDecimal string

func visibilityDecimalFrom(value string) visibilityDecimal {
	if _, ok := new(big.Rat).SetString(value); !ok {
		panic("invalid decimal fixture value: " + value)
	}
	return visibilityDecimal(value)
}

func (d visibilityDecimal) rat() *big.Rat {
	value, ok := new(big.Rat).SetString(string(d))
	if !ok {
		panic("invalid stored decimal fixture value: " + string(d))
	}
	return value
}

func visibilityDecimalFromRat(value *big.Rat) visibilityDecimal {
	if value.Sign() == 0 {
		return "0"
	}

	denominator := new(big.Int).Set(value.Denom())
	twos := 0
	fives := 0
	two := big.NewInt(2)
	five := big.NewInt(5)
	zero := big.NewInt(0)
	remainder := new(big.Int)
	for {
		remainder.Mod(denominator, two)
		if remainder.Cmp(zero) != 0 {
			break
		}
		denominator.Div(denominator, two)
		twos++
	}
	for {
		remainder.Mod(denominator, five)
		if remainder.Cmp(zero) != 0 {
			break
		}
		denominator.Div(denominator, five)
		fives++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return visibilityDecimal(value.RatString())
	}

	scale := max(twos, fives)
	text := value.FloatString(scale)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" {
		text = "0"
	}
	return visibilityDecimal(text)
}

func (d visibilityDecimal) add(other visibilityDecimal) visibilityDecimal {
	return visibilityDecimalFromRat(new(big.Rat).Add(d.rat(), other.rat()))
}

func (d visibilityDecimal) subtract(other visibilityDecimal) visibilityDecimal {
	return visibilityDecimalFromRat(new(big.Rat).Sub(d.rat(), other.rat()))
}

func (d visibilityDecimal) multiply(other visibilityDecimal) visibilityDecimal {
	return visibilityDecimalFromRat(new(big.Rat).Mul(d.rat(), other.rat()))
}

func (d visibilityDecimal) divide(other visibilityDecimal) visibilityDecimal {
	return visibilityDecimalFromRat(new(big.Rat).Quo(d.rat(), other.rat()))
}

func (d visibilityDecimal) compare(other visibilityDecimal) int {
	return d.rat().Cmp(other.rat())
}

type visibilityManualClock struct {
	tick uint64
}

func (c *visibilityManualClock) next() uint64 {
	c.tick++
	return c.tick
}

type visibilityPosition struct {
	quantity   visibilityDecimal
	entryPrice visibilityDecimal
	markPrice  visibilityDecimal
	openedAt   uint64
}

func (p visibilityPosition) unrealizedPnL() visibilityDecimal {
	return p.quantity.multiply(p.markPrice.subtract(p.entryPrice))
}

type visibilityBalance struct {
	Total  visibilityDecimal
	Locked visibilityDecimal
	Free   visibilityDecimal
	Equity visibilityDecimal
}

type visibilityDenialReason string

const visibilityInsufficientMargin visibilityDenialReason = "insufficient_margin"

type visibilityErrorEnvelope struct {
	Reason visibilityDenialReason
}

type visibilityOrderResult struct {
	Status   int
	Accepted bool
	Error    *visibilityErrorEnvelope
}

type visibilityEngine struct {
	clock      visibilityManualClock
	total      visibilityDecimal
	marginInit visibilityDecimal
	leverage   visibilityDecimal
	position   *visibilityPosition
	locked     visibilityDecimal
}

func newVisibilityEngine(total, marginInit, leverage string) *visibilityEngine {
	return &visibilityEngine{
		total:      visibilityDecimalFrom(total),
		marginInit: visibilityDecimalFrom(marginInit),
		leverage:   visibilityDecimalFrom(leverage),
		locked:     visibilityDecimalFrom("0"),
	}
}

func (e *visibilityEngine) requiredMargin(quantity, entryPrice visibilityDecimal) visibilityDecimal {
	return quantity.multiply(entryPrice).multiply(e.marginInit).divide(e.leverage)
}

func (e *visibilityEngine) submitOrder(quantity, entryPrice visibilityDecimal) visibilityOrderResult {
	required := e.requiredMargin(quantity, entryPrice)
	if required.compare(e.balance().Free) > 0 {
		return visibilityOrderResult{
			Status: 400,
			Error:  &visibilityErrorEnvelope{Reason: visibilityInsufficientMargin},
		}
	}

	e.position = &visibilityPosition{
		quantity:   quantity,
		entryPrice: entryPrice,
		markPrice:  entryPrice,
		openedAt:   e.clock.next(),
	}
	e.locked = required
	return visibilityOrderResult{Status: 202, Accepted: true}
}

func (e *visibilityEngine) injectMark(mark visibilityDecimal) error {
	if e.position == nil {
		return fmt.Errorf("mark requires an open position")
	}
	e.position.markPrice = mark
	e.clock.next()
	return nil
}

func (e *visibilityEngine) balance() visibilityBalance {
	equity := e.total
	if e.position != nil {
		equity = equity.add(e.position.unrealizedPnL())
	}
	return visibilityBalance{
		Total:  e.total,
		Locked: e.locked,
		Free:   equity.subtract(e.locked),
		Equity: equity,
	}
}

func (e *visibilityEngine) closePosition() {
	e.position = nil
	e.locked = visibilityDecimalFrom("0")
	e.clock.next()
}

func (e *visibilityEngine) setLeverage(leverage visibilityDecimal) error {
	if e.position != nil {
		return fmt.Errorf("cannot change leverage while a position is open")
	}
	if leverage.compare(visibilityDecimalFrom("0")) <= 0 {
		return fmt.Errorf("leverage must be positive")
	}
	e.leverage = leverage
	e.clock.next()
	return nil
}
