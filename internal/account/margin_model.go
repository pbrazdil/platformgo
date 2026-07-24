package account

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/money"
)

// MarginModel calculates order and position margin for a leveraged instrument.
type MarginModel interface {
	CalculateInitialMargin(
		instrument MarginInstrument,
		quantity, price, leverage decimal.Decimal,
		useQuoteForInverse bool,
	) (money.Money, error)
	CalculateMaintenanceMargin(
		instrument MarginInstrument,
		quantity, price, leverage decimal.Decimal,
		useQuoteForInverse bool,
	) (money.Money, error)
}

// StandardMarginModel applies fixed margin rates without leverage division.
type StandardMarginModel struct{}

func (StandardMarginModel) CalculateInitialMargin(
	instrument MarginInstrument,
	quantity, price, _ decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	return calculateStandardMargin(
		instrument,
		quantity,
		price,
		instrument.InitialMarginRate,
		useQuoteForInverse,
		"initial",
	)
}

func (StandardMarginModel) CalculateMaintenanceMargin(
	instrument MarginInstrument,
	quantity, price, _ decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	return calculateStandardMargin(
		instrument,
		quantity,
		price,
		instrument.MaintenanceRate,
		useQuoteForInverse,
		"maintenance",
	)
}

// LeveragedMarginModel divides notional by leverage before applying rates.
type LeveragedMarginModel struct{}

func (LeveragedMarginModel) CalculateInitialMargin(
	instrument MarginInstrument,
	quantity, price, leverage decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	return calculateLeveragedMargin(
		instrument,
		quantity,
		price,
		leverage,
		instrument.InitialMarginRate,
		useQuoteForInverse,
		"initial",
	)
}

func (LeveragedMarginModel) CalculateMaintenanceMargin(
	instrument MarginInstrument,
	quantity, price, leverage decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	return calculateLeveragedMargin(
		instrument,
		quantity,
		price,
		leverage,
		instrument.MaintenanceRate,
		useQuoteForInverse,
		"maintenance",
	)
}

// MarginModelAny provides runtime dispatch between the supported models.
type MarginModelAny struct {
	model MarginModel
}

func NewMarginModelAny(model MarginModel) MarginModelAny {
	return MarginModelAny{model: model}
}

func DefaultMarginModelAny() MarginModelAny {
	return NewMarginModelAny(LeveragedMarginModel{})
}

func (m MarginModelAny) IsLeveraged() bool {
	_, ok := m.model.(LeveragedMarginModel)
	return ok
}

func (m MarginModelAny) CalculateInitialMargin(
	instrument MarginInstrument,
	quantity, price, leverage decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	if m.model == nil {
		return money.Money{}, fmt.Errorf("margin model is not configured")
	}
	return m.model.CalculateInitialMargin(
		instrument,
		quantity,
		price,
		leverage,
		useQuoteForInverse,
	)
}

func (m MarginModelAny) CalculateMaintenanceMargin(
	instrument MarginInstrument,
	quantity, price, leverage decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	if m.model == nil {
		return money.Money{}, fmt.Errorf("margin model is not configured")
	}
	return m.model.CalculateMaintenanceMargin(
		instrument,
		quantity,
		price,
		leverage,
		useQuoteForInverse,
	)
}

func calculateStandardMargin(
	instrument MarginInstrument,
	quantity, price, rate decimal.Decimal,
	useQuoteForInverse bool,
	kind string,
) (money.Money, error) {
	notional, err := marginNotional(instrument, quantity, price, useQuoteForInverse)
	if err != nil {
		return money.Money{}, err
	}
	margin := notional.Decimal().Mul(rate)
	if exceedsRustDecimalRange(margin) {
		return money.Money{}, fmt.Errorf("%s margin calculation overflow", kind)
	}
	return money.FromDecimal(margin, notional.Currency())
}

func calculateLeveragedMargin(
	instrument MarginInstrument,
	quantity, price, leverage, rate decimal.Decimal,
	useQuoteForInverse bool,
	kind string,
) (money.Money, error) {
	if leverage.Sign() <= 0 {
		return money.Money{}, fmt.Errorf("Invalid leverage %s for %s", leverage, instrument.ID)
	}
	notional, err := marginNotional(instrument, quantity, price, useQuoteForInverse)
	if err != nil {
		return money.Money{}, err
	}
	adjusted, err := notional.Decimal().Quo(
		leverage,
		decimal.MaxPrecision,
		decimal.RoundHalfEven,
	)
	if err != nil || exceedsRustDecimalRange(adjusted) {
		return money.Money{}, fmt.Errorf("%s margin calculation overflow", kind)
	}
	margin := adjusted.Mul(rate)
	if exceedsRustDecimalRange(margin) {
		return money.Money{}, fmt.Errorf("%s margin calculation overflow", kind)
	}
	return money.FromDecimal(margin, notional.Currency())
}

func exceedsRustDecimalRange(value decimal.Decimal) bool {
	maximum := decimal.MustParse("79228162514264337593543950335")
	return value.Cmp(maximum) > 0 || value.Cmp(maximum.Neg()) < 0
}
