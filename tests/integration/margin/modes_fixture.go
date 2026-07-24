package margin

import (
	"errors"
	"fmt"
	"time"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type marginMode string

const (
	marginModeCross    marginMode = "cross"
	marginModeIsolated marginMode = "isolated"
)

type omsMode string

const (
	omsModeNetting omsMode = "netting"
	omsModeHedging omsMode = "hedging"
)

type modePosition struct {
	Symbol         string
	SignedQuantity decimal.Decimal
	EntryPrice     decimal.Decimal
	MarginMode     marginMode
	IsolatedFunds  decimal.Decimal
}

type modeOrder struct {
	IntentID string
	Status   string
}

type marginModesFixture struct {
	now              time.Time
	balance          decimal.Decimal
	modeBySymbol     map[string]marginMode
	leverageBySymbol map[string]decimal.Decimal
	oms              omsMode
	positions        map[string]modePosition
	orders           []modeOrder
}

func newMarginModesFixture() *marginModesFixture {
	return &marginModesFixture{
		now:              time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		modeBySymbol:     make(map[string]marginMode),
		leverageBySymbol: make(map[string]decimal.Decimal),
		oms:              omsModeNetting,
		positions:        make(map[string]modePosition),
	}
}

func (fixture *marginModesFixture) setBalance(amount string) {
	fixture.balance = decimal.MustParse(amount)
}

func (fixture *marginModesFixture) deposit(amount string) {
	fixture.balance = fixture.balance.Add(decimal.MustParse(amount))
}

func (fixture *marginModesFixture) setOMS(mode omsMode) {
	fixture.oms = mode
}

func (fixture *marginModesFixture) effectiveMode(symbol string) marginMode {
	configured := fixture.modeBySymbol[symbol]
	if fixture.oms == omsModeHedging {
		return marginModeCross
	}
	if configured == marginModeIsolated {
		return marginModeIsolated
	}
	return marginModeCross
}

func (fixture *marginModesFixture) setMarginMode(symbol string, mode marginMode) error {
	if !fixture.positions[symbol].SignedQuantity.IsZero() {
		return errors.New("cannot change margin mode while a position is open")
	}
	fixture.modeBySymbol[symbol] = mode
	return nil
}

func (fixture *marginModesFixture) setLeverage(symbol, leverage string) error {
	if !fixture.positions[symbol].SignedQuantity.IsZero() {
		return errors.New("cannot change leverage while a position is open")
	}
	fixture.leverageBySymbol[symbol] = decimal.MustParse(leverage)
	return nil
}

func (fixture *marginModesFixture) marketBuy(symbol, intentID, quantity, price string) error {
	if fixture.balance.IsZero() {
		return fmt.Errorf("insufficient_funds: insufficient free margin for %s", symbol)
	}
	qty := decimal.MustParse(quantity)
	entry := decimal.MustParse(price)
	mode := fixture.effectiveMode(symbol)
	isolatedFunds := decimal.Decimal{}
	if mode == marginModeIsolated {
		leverage := fixture.leverageBySymbol[symbol]
		if leverage.IsZero() {
			leverage = decimal.MustParse("10")
		}
		notional := qty.Mul(entry)
		isolatedFunds, _ = notional.Quo(leverage, 8, decimal.RoundHalfEven)
	}
	fixture.positions[symbol] = modePosition{
		Symbol:         symbol,
		SignedQuantity: qty,
		EntryPrice:     entry,
		MarginMode:     mode,
		IsolatedFunds:  isolatedFunds,
	}
	fixture.orders = append(fixture.orders, modeOrder{IntentID: intentID, Status: "filled"})
	return nil
}

func (fixture *marginModesFixture) close(symbol, intentID string) {
	delete(fixture.positions, symbol)
	fixture.orders = append(fixture.orders, modeOrder{IntentID: intentID, Status: "filled"})
}

func (fixture *marginModesFixture) injectMark(symbol, mark string) {
	position, ok := fixture.positions[symbol]
	if !ok || position.MarginMode != marginModeIsolated {
		return
	}
	markPrice := decimal.MustParse(mark)
	loss := position.EntryPrice.Sub(markPrice).Mul(position.SignedQuantity)
	if loss.Cmp(position.IsolatedFunds) < 0 {
		return
	}
	fixture.balance = fixture.balance.Sub(position.IsolatedFunds)
	delete(fixture.positions, symbol)
	fixture.orders = append(fixture.orders, modeOrder{
		IntentID: "stopout:" + symbol + ":" + fixture.now.Format(time.RFC3339),
		Status:   "filled",
	})
}

func (fixture *marginModesFixture) openQuantity(symbol string) decimal.Decimal {
	return fixture.positions[symbol].SignedQuantity
}

func (fixture *marginModesFixture) stopoutCount() int {
	count := 0
	for _, order := range fixture.orders {
		if len(order.IntentID) >= len("stopout:") && order.IntentID[:len("stopout:")] == "stopout:" {
			count++
		}
	}
	return count
}
