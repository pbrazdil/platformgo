package domain

import (
	"fmt"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

// RealizedPnL calculates the settlement-currency result for one closing
// quantity and rounds exactly once, at the currency boundary, using half-even.
func RealizedPnL(
	entry Price,
	exit Price,
	quantity Quantity,
	long bool,
	settlementCurrency Currency,
) (Money, error) {
	if !entry.instrument.Equal(exit.instrument) ||
		!entry.instrument.Equal(quantity.instrument) {
		return Money{}, fmt.Errorf(
			"%w: realized PnL values use different instrument revisions",
			ErrUnitMismatch,
		)
	}
	var priceChange Price
	var err error
	if long {
		priceChange, err = exit.Sub(entry)
	} else {
		priceChange, err = entry.Sub(exit)
	}
	if err != nil {
		return Money{}, err
	}
	amount, err := decimal.MulQuantized(
		priceChange.Decimal(),
		quantity.Decimal(),
		settlementCurrency.Scale(),
		decimal.RoundHalfEven,
		"position realized PnL",
	)
	if err != nil {
		return Money{}, err
	}
	return NewMoney(amount.String(), settlementCurrency)
}
