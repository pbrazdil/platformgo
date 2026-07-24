package domain

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

var (
	ErrInvalidCurrency   = errors.New("invalid currency")
	ErrInvalidInstrument = errors.New("invalid instrument revision")
	ErrUnitMismatch      = errors.New("economic unit mismatch")
	ErrUnitScale         = errors.New("economic value exceeds unit scale")
	ErrNegativeQuantity  = errors.New("quantity must not be negative")
)

const maxInstrumentIDLength = 255

// Currency is the immutable unit of a monetary value.
type Currency struct {
	code  string
	scale uint8
}

func NewCurrency(code string, scale uint8) (Currency, error) {
	if !validCurrencyCode(code) {
		return Currency{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, code)
	}
	if scale > decimal.MaxScale {
		return Currency{}, fmt.Errorf(
			"%w: currency %s scale %d > %d",
			ErrUnitScale,
			code,
			scale,
			decimal.MaxScale,
		)
	}
	return Currency{code: code, scale: scale}, nil
}

func validCurrencyCode(code string) bool {
	if len(code) < 3 || len(code) > 12 || !utf8.ValidString(code) {
		return false
	}
	for _, character := range code {
		if (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func (c Currency) Code() string {
	return c.code
}

func (c Currency) Scale() uint8 {
	return c.scale
}

func (c Currency) Equal(other Currency) bool {
	return c == other
}

func (c Currency) String() string {
	return c.code
}

// InstrumentRevision binds economic values to one immutable instrument
// definition and its price/quantity scales.
type InstrumentRevision struct {
	id            string
	revision      uint64
	priceScale    uint8
	quantityScale uint8
}

func NewInstrumentRevision(
	id string,
	revision uint64,
	priceScale uint8,
	quantityScale uint8,
) (InstrumentRevision, error) {
	if !validInstrumentID(id) || revision == 0 {
		return InstrumentRevision{}, fmt.Errorf(
			"%w: id=%q revision=%d",
			ErrInvalidInstrument,
			id,
			revision,
		)
	}
	if priceScale > decimal.MaxScale || quantityScale > decimal.MaxScale {
		return InstrumentRevision{}, fmt.Errorf(
			"%w: price scale %d quantity scale %d",
			ErrUnitScale,
			priceScale,
			quantityScale,
		)
	}
	return InstrumentRevision{
		id:            id,
		revision:      revision,
		priceScale:    priceScale,
		quantityScale: quantityScale,
	}, nil
}

func validInstrumentID(id string) bool {
	if id == "" || len(id) > maxInstrumentIDLength ||
		!utf8.ValidString(id) || strings.TrimSpace(id) != id {
		return false
	}
	for _, character := range id {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func (i InstrumentRevision) ID() string {
	return i.id
}

func (i InstrumentRevision) Revision() uint64 {
	return i.revision
}

func (i InstrumentRevision) PriceScale() uint8 {
	return i.priceScale
}

func (i InstrumentRevision) QuantityScale() uint8 {
	return i.quantityScale
}

func (i InstrumentRevision) Equal(other InstrumentRevision) bool {
	return i == other
}

func (i InstrumentRevision) String() string {
	return fmt.Sprintf("%s@%d", i.id, i.revision)
}

// Money carries an exact amount and its currency.
type Money struct {
	amount   decimal.Decimal
	currency Currency
}

func NewMoney(text string, currency Currency) (Money, error) {
	if currency.code == "" {
		return Money{}, ErrInvalidCurrency
	}
	amount, err := decimal.ParseWithMaxScale(text, currency.scale)
	if err != nil {
		if errors.Is(err, decimal.ErrScale) {
			return Money{}, fmt.Errorf("%w: %w", ErrUnitScale, err)
		}
		return Money{}, err
	}
	return moneyFromDecimal(amount, currency)
}

func moneyFromDecimal(
	amount decimal.Decimal,
	currency Currency,
) (Money, error) {
	if currency.code == "" {
		return Money{}, ErrInvalidCurrency
	}
	if amount.Scale() > currency.scale {
		return Money{}, fmt.Errorf(
			"%w: amount scale %d > %s scale %d",
			ErrUnitScale,
			amount.Scale(),
			currency.code,
			currency.scale,
		)
	}
	return Money{amount: amount, currency: currency}, nil
}

func (m Money) Decimal() decimal.Decimal {
	return m.amount
}

func (m Money) Currency() Currency {
	return m.currency
}

func (m Money) Add(other Money) (Money, error) {
	if !m.currency.Equal(other.currency) {
		return Money{}, fmt.Errorf(
			"%w: %s and %s",
			ErrUnitMismatch,
			m.currency,
			other.currency,
		)
	}
	result, err := m.amount.Add(other.amount)
	if err != nil {
		return Money{}, err
	}
	return moneyFromDecimal(result, m.currency)
}

func (m Money) Sub(other Money) (Money, error) {
	if !m.currency.Equal(other.currency) {
		return Money{}, fmt.Errorf(
			"%w: %s and %s",
			ErrUnitMismatch,
			m.currency,
			other.currency,
		)
	}
	result, err := m.amount.Sub(other.amount)
	if err != nil {
		return Money{}, err
	}
	return moneyFromDecimal(result, m.currency)
}

func (m Money) String() string {
	return m.amount.String() + " " + m.currency.code
}

// Price carries an exact value and its instrument revision.
type Price struct {
	value      decimal.Decimal
	instrument InstrumentRevision
}

func NewPrice(text string, instrument InstrumentRevision) (Price, error) {
	if instrument.id == "" {
		return Price{}, ErrInvalidInstrument
	}
	value, err := decimal.ParseWithMaxScale(text, instrument.priceScale)
	if err != nil {
		if errors.Is(err, decimal.ErrScale) {
			return Price{}, fmt.Errorf("%w: %w", ErrUnitScale, err)
		}
		return Price{}, err
	}
	return priceFromDecimal(value, instrument)
}

// NewPriceScaled constructs a price from an exact integer coefficient and
// fractional scale. It does not round.
func NewPriceScaled(
	coefficient *big.Int,
	scale uint8,
	instrument InstrumentRevision,
) (Price, error) {
	if instrument.id == "" {
		return Price{}, ErrInvalidInstrument
	}
	value, err := decimal.NewScaled(coefficient, scale)
	if err != nil {
		return Price{}, err
	}
	return priceFromDecimal(value, instrument)
}

func priceFromDecimal(
	value decimal.Decimal,
	instrument InstrumentRevision,
) (Price, error) {
	if instrument.id == "" {
		return Price{}, ErrInvalidInstrument
	}
	if value.Scale() > instrument.priceScale {
		return Price{}, fmt.Errorf(
			"%w: price scale %d > instrument scale %d",
			ErrUnitScale,
			value.Scale(),
			instrument.priceScale,
		)
	}
	return Price{value: value, instrument: instrument}, nil
}

func (p Price) Decimal() decimal.Decimal {
	return p.value
}

func (p Price) Instrument() InstrumentRevision {
	return p.instrument
}

func (p Price) Scale() uint8 {
	return p.instrument.priceScale
}

func (p Price) Add(other Price) (Price, error) {
	if !p.instrument.Equal(other.instrument) {
		return Price{}, fmt.Errorf(
			"%w: %s and %s",
			ErrUnitMismatch,
			p.instrument,
			other.instrument,
		)
	}
	value, err := p.value.Add(other.value)
	if err != nil {
		return Price{}, err
	}
	return priceFromDecimal(value, p.instrument)
}

func (p Price) Sub(other Price) (Price, error) {
	if !p.instrument.Equal(other.instrument) {
		return Price{}, fmt.Errorf(
			"%w: %s and %s",
			ErrUnitMismatch,
			p.instrument,
			other.instrument,
		)
	}
	value, err := p.value.Sub(other.value)
	if err != nil {
		return Price{}, err
	}
	return priceFromDecimal(value, p.instrument)
}

// Quantity carries a non-negative exact value and its instrument revision.
type Quantity struct {
	value      decimal.Decimal
	instrument InstrumentRevision
}

func NewQuantity(
	text string,
	instrument InstrumentRevision,
) (Quantity, error) {
	if instrument.id == "" {
		return Quantity{}, ErrInvalidInstrument
	}
	value, err := decimal.ParseWithMaxScale(text, instrument.quantityScale)
	if err != nil {
		if errors.Is(err, decimal.ErrScale) {
			return Quantity{}, fmt.Errorf("%w: %w", ErrUnitScale, err)
		}
		return Quantity{}, err
	}
	return quantityFromDecimal(value, instrument)
}

func quantityFromDecimal(
	value decimal.Decimal,
	instrument InstrumentRevision,
) (Quantity, error) {
	if instrument.id == "" {
		return Quantity{}, ErrInvalidInstrument
	}
	if value.Sign() < 0 {
		return Quantity{}, ErrNegativeQuantity
	}
	if value.Scale() > instrument.quantityScale {
		return Quantity{}, fmt.Errorf(
			"%w: quantity scale %d > instrument scale %d",
			ErrUnitScale,
			value.Scale(),
			instrument.quantityScale,
		)
	}
	return Quantity{value: value, instrument: instrument}, nil
}

func (q Quantity) Decimal() decimal.Decimal {
	return q.value
}

func (q Quantity) Instrument() InstrumentRevision {
	return q.instrument
}

func (q Quantity) Scale() uint8 {
	return q.instrument.quantityScale
}

func (q Quantity) Add(other Quantity) (Quantity, error) {
	if !q.instrument.Equal(other.instrument) {
		return Quantity{}, fmt.Errorf(
			"%w: %s and %s",
			ErrUnitMismatch,
			q.instrument,
			other.instrument,
		)
	}
	value, err := q.value.Add(other.value)
	if err != nil {
		return Quantity{}, err
	}
	return quantityFromDecimal(value, q.instrument)
}

func (q Quantity) Sub(other Quantity) (Quantity, error) {
	if !q.instrument.Equal(other.instrument) {
		return Quantity{}, fmt.Errorf(
			"%w: %s and %s",
			ErrUnitMismatch,
			q.instrument,
			other.instrument,
		)
	}
	value, err := q.value.Sub(other.value)
	if err != nil {
		return Quantity{}, err
	}
	return quantityFromDecimal(value, q.instrument)
}

// Rate is an exact signed rate, distinct from money and ratios.
type Rate struct {
	value decimal.Decimal
}

func NewRate(text string) (Rate, error) {
	value, err := decimal.Parse(text)
	if err != nil {
		return Rate{}, err
	}
	return Rate{value: value}, nil
}

func (r Rate) Decimal() decimal.Decimal {
	return r.value
}

// Ratio is an exact dimensionless value distinct from Rate.
type Ratio struct {
	value decimal.Decimal
}

func NewRatio(text string) (Ratio, error) {
	value, err := decimal.Parse(text)
	if err != nil {
		return Ratio{}, err
	}
	return Ratio{value: value}, nil
}

func (r Ratio) Decimal() decimal.Decimal {
	return r.value
}

// BasisPoints is an exact integer count of 1/10,000 units.
type BasisPoints int32

func (b BasisPoints) Rate() (Rate, error) {
	value, err := decimal.NewScaled(big.NewInt(int64(b)), 4)
	if err != nil {
		return Rate{}, err
	}
	return Rate{value: value}, nil
}
