package domain

import (
	"errors"
	"fmt"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

var ErrInvalidFeeRate = errors.New("invalid trading fee rate")

// TradingFee calculates price times quantity times a signed fee rate, rounding
// once at the settlement-currency boundary using half-even. Negative maker
// rates represent rebates.
func TradingFee(
	price Price,
	quantity Quantity,
	rate Rate,
	settlementCurrency Currency,
) (Money, error) {
	if !price.instrument.Equal(quantity.instrument) {
		return Money{}, fmt.Errorf(
			"%w: fee values use different instrument revisions",
			ErrUnitMismatch,
		)
	}
	one, err := decimal.Parse("1")
	if err != nil {
		return Money{}, err
	}
	negativeOne, err := decimal.Parse("-1")
	if err != nil {
		return Money{}, err
	}
	if rate.value.Cmp(one) > 0 || rate.value.Cmp(negativeOne) < 0 {
		return Money{}, ErrInvalidFeeRate
	}
	notional, err := price.value.Mul(quantity.value)
	if err != nil {
		return Money{}, err
	}
	amount, err := decimal.MulQuantized(
		notional,
		rate.value,
		settlementCurrency.Scale(),
		decimal.RoundHalfEven,
		"trading fee",
	)
	if err != nil {
		return Money{}, err
	}
	return NewMoney(amount.String(), settlementCurrency)
}
