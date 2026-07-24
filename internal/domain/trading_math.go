package domain

import (
	"errors"
	"fmt"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

var ErrEmptyPriceQuantity = errors.New("price/quantity set has zero total quantity")

// PriceQuantity is one exact execution price and quantity pair.
type PriceQuantity struct {
	Price    Price
	Quantity Quantity
}

// WeightedAveragePrice calculates an exact quantity-weighted mean and rounds
// once to the instrument price scale using half-even policy.
func WeightedAveragePrice(values []PriceQuantity) (Price, error) {
	if len(values) == 0 {
		return Price{}, ErrEmptyPriceQuantity
	}
	instrument := values[0].Price.instrument
	var totalNotional decimal.Decimal
	var totalQuantity decimal.Decimal
	for _, value := range values {
		if !value.Price.instrument.Equal(instrument) ||
			!value.Quantity.instrument.Equal(instrument) {
			return Price{}, fmt.Errorf(
				"%w: weighted average contains multiple instrument revisions",
				ErrUnitMismatch,
			)
		}
		notional, err := value.Price.value.Mul(value.Quantity.value)
		if err != nil {
			return Price{}, err
		}
		totalNotional, err = totalNotional.Add(notional)
		if err != nil {
			return Price{}, err
		}
		totalQuantity, err = totalQuantity.Add(value.Quantity.value)
		if err != nil {
			return Price{}, err
		}
	}
	if totalQuantity.IsZero() {
		return Price{}, ErrEmptyPriceQuantity
	}
	average, err := decimal.QuoQuantized(
		totalNotional,
		totalQuantity,
		instrument.priceScale,
		decimal.RoundHalfEven,
		"weighted average fill price",
	)
	if err != nil {
		return Price{}, err
	}
	return priceFromDecimal(average, instrument)
}
