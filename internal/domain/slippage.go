package domain

import (
	"errors"
	"fmt"
	"math/big"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

var ErrInvalidReferencePrice = errors.New("invalid reference price")

// PriceWithinAdverseBasisPoints reports whether candidate is no worse than the
// exact adverse-slippage boundary around reference. Favorable prices always
// pass through unchanged.
func PriceWithinAdverseBasisPoints(
	reference Price,
	candidate Price,
	buy bool,
	basisPoints uint32,
) (bool, error) {
	if !reference.instrument.Equal(candidate.instrument) {
		return false, fmt.Errorf(
			"%w: slippage prices use different instrument revisions",
			ErrUnitMismatch,
		)
	}
	if reference.value.Sign() <= 0 || candidate.value.Sign() <= 0 {
		return false, ErrInvalidReferencePrice
	}

	var adverse decimal.Decimal
	var err error
	if buy {
		adverse, err = candidate.value.Sub(reference.value)
	} else {
		adverse, err = reference.value.Sub(candidate.value)
	}
	if err != nil {
		return false, err
	}
	if adverse.Sign() <= 0 {
		return true, nil
	}

	rate, err := decimal.NewScaled(big.NewInt(int64(basisPoints)), 4)
	if err != nil {
		return false, err
	}
	maximumAdverse, err := reference.value.Mul(rate)
	if err != nil {
		return false, err
	}
	return adverse.Cmp(maximumAdverse) <= 0, nil
}
