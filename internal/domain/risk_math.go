package domain

import (
	"errors"
	"fmt"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

var (
	ErrInvalidMarginRate = errors.New("invalid margin rate")
	ErrInvalidLeverage   = errors.New("invalid leverage")
)

// PositionNotional calculates absolute price times quantity and rounds once at
// the settlement-currency boundary using half-even.
func PositionNotional(
	price Price,
	quantity Quantity,
	settlementCurrency Currency,
) (Money, error) {
	if !price.instrument.Equal(quantity.instrument) {
		return Money{}, fmt.Errorf(
			"%w: notional values use different instrument revisions",
			ErrUnitMismatch,
		)
	}
	amount, err := decimal.MulQuantized(
		price.Decimal(),
		quantity.Decimal(),
		settlementCurrency.Scale(),
		decimal.RoundHalfEven,
		"position notional",
	)
	if err != nil {
		return Money{}, err
	}
	return NewMoney(amount.String(), settlementCurrency)
}

// PositionMargin calculates quantity times price times initial-margin rate,
// divided by leverage, with one half-even settlement-currency rounding point.
func PositionMargin(
	price Price,
	quantity Quantity,
	marginRate Rate,
	leverage Ratio,
	settlementCurrency Currency,
) (Money, error) {
	if !price.instrument.Equal(quantity.instrument) {
		return Money{}, fmt.Errorf(
			"%w: margin values use different instrument revisions",
			ErrUnitMismatch,
		)
	}
	if marginRate.value.Sign() < 0 {
		return Money{}, ErrInvalidMarginRate
	}
	if leverage.value.Sign() <= 0 {
		return Money{}, ErrInvalidLeverage
	}
	notional, err := price.value.Mul(quantity.value)
	if err != nil {
		return Money{}, err
	}
	numerator, err := notional.Mul(marginRate.value)
	if err != nil {
		return Money{}, err
	}
	amount, err := decimal.QuoQuantized(
		numerator,
		leverage.value,
		settlementCurrency.Scale(),
		decimal.RoundHalfEven,
		"position used margin",
	)
	if err != nil {
		return Money{}, err
	}
	return NewMoney(amount.String(), settlementCurrency)
}

// FundingPayment returns the account balance delta for one position. Positive
// rates debit longs and credit shorts; negative rates reverse that direction.
func FundingPayment(
	oraclePrice Price,
	quantity Quantity,
	rate Rate,
	long bool,
	settlementCurrency Currency,
) (Money, error) {
	if !oraclePrice.instrument.Equal(quantity.instrument) {
		return Money{}, fmt.Errorf(
			"%w: funding values use different instrument revisions",
			ErrUnitMismatch,
		)
	}
	notional, err := oraclePrice.value.Mul(quantity.value)
	if err != nil {
		return Money{}, err
	}
	amount, err := decimal.MulQuantized(
		notional,
		rate.value,
		settlementCurrency.Scale(),
		decimal.RoundHalfEven,
		"funding payment",
	)
	if err != nil {
		return Money{}, err
	}
	if long {
		var zero decimal.Decimal
		amount, err = zero.Sub(amount)
		if err != nil {
			return Money{}, err
		}
	}
	return NewMoney(amount.String(), settlementCurrency)
}
