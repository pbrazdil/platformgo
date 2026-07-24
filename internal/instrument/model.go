package instrument

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/money"
)

const (
	InstrumentClassFuture        InstrumentClass = "FUTURE"
	InstrumentClassFuturesSpread InstrumentClass = "FUTURES_SPREAD"
	InstrumentClassOption        InstrumentClass = "OPTION"
	InstrumentClassOptionSpread  InstrumentClass = "OPTION_SPREAD"
	InstrumentClassSpot          InstrumentClass = "SPOT"
	InstrumentClassForward       InstrumentClass = "FORWARD"
	InstrumentClassCFD           InstrumentClass = "CFD"
	InstrumentClassBond          InstrumentClass = "BOND"
	InstrumentClassWarrant       InstrumentClass = "WARRANT"
	InstrumentClassSportsBetting InstrumentClass = "SPORTS_BETTING"
	InstrumentClassBinaryOption  InstrumentClass = "BINARY_OPTION"
)

func (class InstrumentClass) HasExpiration() bool {
	switch class {
	case InstrumentClassFuture, InstrumentClassFuturesSpread,
		InstrumentClassOption, InstrumentClassOptionSpread:
		return true
	default:
		return false
	}
}

// InstrumentValidationError classifies a common instrument invariant failure.
type InstrumentValidationError struct {
	Kind    string
	Field   string
	Message string
}

func (e *InstrumentValidationError) Error() string { return e.Message }

// InstrumentCommon contains the shared constraints checked by instrument
// constructors.
type InstrumentCommon struct {
	PricePrecision uint8
	SizePrecision  uint8
	SizeIncrement  decimal.Quantity
	Multiplier     decimal.Quantity
	MarginInit     decimal.Decimal
	MarginMaint    decimal.Decimal
	PriceIncrement *decimal.Price
	LotSize        *decimal.Quantity
	MaxQuantity    *decimal.Quantity
	MinQuantity    *decimal.Quantity
	MaxNotional    *money.Money
	MinNotional    *money.Money
	MaxPrice       *decimal.Price
	MinPrice       *decimal.Price
}

func ValidateInstrumentCommon(value InstrumentCommon) error {
	if err := positiveQuantity(value.SizeIncrement, "size_increment"); err != nil {
		return err
	}
	if value.SizeIncrement.Precision() != value.SizePrecision {
		return validation("precision_mismatch", "size_increment",
			fmt.Sprintf("size_increment.precision %d was not equal to size_precision %d", value.SizeIncrement.Precision(), value.SizePrecision))
	}
	if err := positiveQuantity(value.Multiplier, "multiplier"); err != nil {
		return err
	}
	if value.MarginInit.Sign() <= 0 {
		return validation("not_positive", "margin_init", "'margin_init' not positive")
	}
	if value.MarginMaint.Sign() <= 0 {
		return validation("not_positive", "margin_maint", "'margin_maint' not positive")
	}
	if value.PriceIncrement != nil {
		if err := positivePrice(*value.PriceIncrement, "price_increment"); err != nil {
			return err
		}
		if value.PriceIncrement.Precision() != value.PricePrecision {
			return validation("precision_mismatch", "price_increment",
				fmt.Sprintf("price_increment.precision %d was not equal to price_precision %d", value.PriceIncrement.Precision(), value.PricePrecision))
		}
	}
	for _, item := range []struct {
		name  string
		value *decimal.Quantity
	}{{"lot_size", value.LotSize}, {"max_quantity", value.MaxQuantity}, {"min_quantity", value.MinQuantity}} {
		if item.value != nil {
			if err := positiveQuantity(*item.value, item.name); err != nil {
				return err
			}
		}
	}
	for _, item := range []struct {
		name  string
		value *money.Money
	}{{"max_notional", value.MaxNotional}, {"min_notional", value.MinNotional}} {
		if item.value != nil {
			if err := money.CheckPositive(*item.value, item.name); err != nil {
				return validation("not_positive", item.name, err.Error())
			}
		}
	}
	for _, item := range []struct {
		name  string
		value *decimal.Price
	}{{"max_price", value.MaxPrice}, {"min_price", value.MinPrice}} {
		if item.value == nil {
			continue
		}
		if err := positivePrice(*item.value, item.name); err != nil {
			return err
		}
		if item.value.Precision() != value.PricePrecision {
			return validation("precision_mismatch", item.name,
				fmt.Sprintf("%s.precision %d was not equal to price_precision %d", item.name, item.value.Precision(), value.PricePrecision))
		}
	}
	if value.MinPrice != nil && value.MaxPrice != nil && value.MinPrice.Cmp(*value.MaxPrice) > 0 {
		return validation("predicate_violation", "min_price", "min_price exceeds max_price")
	}
	return nil
}

func validation(kind, field, message string) error {
	return &InstrumentValidationError{Kind: kind, Field: field, Message: message}
}

func positiveQuantity(value decimal.Quantity, field string) error {
	if !value.IsPositive() {
		return validation("not_positive", field, fmt.Sprintf("'%s' not positive", field))
	}
	return nil
}

func positivePrice(value decimal.Price, field string) error {
	if !value.IsPositive() {
		return validation("not_positive", field, fmt.Sprintf("'%s' not positive", field))
	}
	return nil
}

// Model is the shared behavior exercised by concrete instrument types.
type Model struct {
	PricePrecision     uint8
	SizePrecision      uint8
	PriceIncrement     decimal.Price
	SizeIncrement      decimal.Quantity
	Multiplier         decimal.Quantity
	BaseCurrency       *currency.Currency
	QuoteCurrency      currency.Currency
	SettlementCurrency currency.Currency
	Inverse            bool
	TickScheme         string
	MaxPrice           *decimal.Price
	MinPrice           *decimal.Price
}

func DefaultPriceIncrement(precision uint8) (decimal.Price, error) {
	return decimal.NewPrice(fmt.Sprintf("1e-%d", precision), precision)
}

func (m Model) MinPriceIncrementPrecision() uint8 {
	return minIncrementPrecision(m.PriceIncrement.String())
}

func (m Model) MinSizeIncrementPrecision() uint8 {
	return minIncrementPrecision(m.SizeIncrement.String())
}

func minIncrementPrecision(text string) uint8 {
	parts := strings.SplitN(text, ".", 2)
	if len(parts) == 1 {
		return 0
	}
	trimmed := strings.TrimRight(parts[1], "0")
	if trimmed == "" {
		return uint8(len(parts[1]))
	}
	return uint8(len(trimmed))
}

func (m Model) TryMakePrice(value float64) (decimal.Price, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || !finitePrice(value) {
		return decimal.Price{}, fmt.Errorf("invalid `value` for make_price, was %v", value)
	}
	input, err := decimal.Parse(strconv.FormatFloat(value, 'g', -1, 64))
	if err != nil {
		return decimal.Price{}, fmt.Errorf("invalid `value` for make_price, was %v", value)
	}
	rounded := input.Quantize(m.MinPriceIncrementPrecision(), decimal.RoundHalfEven)
	price, err := decimal.NewPrice(rounded.String(), m.PricePrecision)
	if err != nil {
		return decimal.Price{}, fmt.Errorf("invalid `value` for make_price, was %v: %w", value, err)
	}
	return price, nil
}

func (m Model) MakePrice(value float64) decimal.Price {
	price, err := m.TryMakePrice(value)
	if err != nil {
		panic(err)
	}
	return price
}

func (m Model) TryMakeQuantity(value float64, roundDown bool) (decimal.Quantity, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return decimal.Quantity{}, fmt.Errorf("invalid `value` for make_qty, was %v", value)
	}
	input, err := decimal.Parse(strconv.FormatFloat(value, 'g', -1, 64))
	if err != nil {
		return decimal.Quantity{}, fmt.Errorf("invalid `value` for make_qty, was %v", value)
	}
	mode := decimal.RoundHalfEven
	if roundDown {
		mode = decimal.RoundTowardZero
	}
	rounded := input.Quantize(m.MinSizeIncrementPrecision(), mode)
	if input.Sign() > 0 && rounded.IsZero() {
		return decimal.Quantity{}, fmt.Errorf("value rounded to zero for quantity")
	}
	quantity, err := decimal.NewQuantity(rounded.String(), m.SizePrecision)
	if err != nil {
		return decimal.Quantity{}, err
	}
	return quantity, nil
}

func (m Model) MakeQuantity(value float64, roundDown bool) decimal.Quantity {
	quantity, err := m.TryMakeQuantity(value, roundDown)
	if err != nil {
		panic(err)
	}
	return quantity
}

func (m Model) TryNormalizePrice(price decimal.Price) (decimal.Price, error) {
	switch price.String() {
	case "ERROR_PRICE", "PRICE_ERROR", "PRICE_UNDEF":
		return decimal.Price{}, validation("invalid_value", "price",
			fmt.Sprintf("invalid `Price` for 'price', was %s", price))
	}
	quantized := price.Decimal().Quantize(m.PricePrecision, decimal.RoundHalfEven)
	if quantized.Cmp(price.Decimal()) != 0 {
		return decimal.Price{}, validation("predicate_violation", "price",
			fmt.Sprintf("`price` requires rounding to instrument price precision %d, was %s", m.PricePrecision, price))
	}
	if !multipleOf(price.Decimal(), m.PriceIncrement.Decimal()) {
		return decimal.Price{}, validation("predicate_violation", "price",
			fmt.Sprintf("`price` is not aligned to price increment %s, was %s", m.PriceIncrement, price))
	}
	return decimal.NewPrice(price.Decimal().String(), m.PricePrecision)
}

func (m Model) TryNormalizeQuantity(quantity decimal.Quantity) (decimal.Quantity, error) {
	if quantity.IsUndefined() {
		return decimal.Quantity{}, validation("invalid_value", "quantity",
			"invalid `Quantity` for 'quantity', was QUANTITY_UNDEF")
	}
	quantized := quantity.Decimal().Quantize(m.SizePrecision, decimal.RoundHalfEven)
	if quantized.Cmp(quantity.Decimal()) != 0 {
		return decimal.Quantity{}, validation("predicate_violation", "quantity",
			fmt.Sprintf("`quantity` requires rounding to instrument size precision %d, was %s", m.SizePrecision, quantity))
	}
	if !multipleOf(quantity.Decimal(), m.SizeIncrement.Decimal()) {
		return decimal.Quantity{}, validation("predicate_violation", "quantity",
			fmt.Sprintf("`quantity` is not aligned to size increment %s, was %s", m.SizeIncrement, quantity))
	}
	return decimal.NewQuantity(quantity.Decimal().String(), m.SizePrecision)
}

func multipleOf(value, increment decimal.Decimal) bool {
	if increment.IsZero() {
		return true
	}
	left, ok := new(big.Rat).SetString(value.String())
	if !ok {
		return false
	}
	right, ok := new(big.Rat).SetString(increment.String())
	if !ok {
		return false
	}
	return new(big.Rat).Quo(left, right).IsInt()
}

func (m Model) TryCalculateBaseQuantity(quantity decimal.Quantity, lastPrice decimal.Price) (decimal.Quantity, error) {
	if lastPrice.IsZero() {
		return decimal.Quantity{}, fmt.Errorf("`last_price` was zero when calculating base quantity")
	}
	value, err := quantity.Decimal().Quo(lastPrice.Decimal(), m.MinSizeIncrementPrecision(), decimal.RoundHalfEven)
	if err != nil {
		return decimal.Quantity{}, err
	}
	return decimal.NewQuantity(value.String(), m.SizePrecision)
}

func (m Model) CalculateBaseQuantity(quantity decimal.Quantity, lastPrice decimal.Price) decimal.Quantity {
	result, err := m.TryCalculateBaseQuantity(quantity, lastPrice)
	if err != nil {
		panic(err)
	}
	return result
}

func (m Model) IsQuanto() bool {
	if m.BaseCurrency == nil {
		return false
	}
	return m.SettlementCurrency.Code != m.BaseCurrency.Code &&
		!currenciesEquivalentForQuanto(m.SettlementCurrency, m.QuoteCurrency)
}

func currenciesEquivalentForQuanto(left, right currency.Currency) bool {
	if left.Code == right.Code {
		return true
	}
	return isUSDEquivalent(left.Code) && isUSDEquivalent(right.Code)
}

func isUSDEquivalent(code string) bool {
	switch code {
	case "BUSD", "FDUSD", "pUSD", "TUSD", "USD", "USDC", "USDC.e", "USDP", "USDT":
		return true
	default:
		return false
	}
}

func (m Model) CostCurrency() currency.Currency {
	if m.Inverse && m.BaseCurrency != nil {
		return *m.BaseCurrency
	}
	if m.IsQuanto() {
		return m.SettlementCurrency
	}
	return m.QuoteCurrency
}

func (m Model) TryCalculateNotionalValue(quantity decimal.Quantity, price decimal.Price, useQuoteForInverse bool) (money.Money, error) {
	denomination := m.QuoteCurrency
	if m.Inverse && !useQuoteForInverse {
		if m.BaseCurrency == nil {
			return money.Money{}, fmt.Errorf("inverse instrument has no base currency")
		}
		denomination = *m.BaseCurrency
	} else if m.IsQuanto() {
		denomination = m.SettlementCurrency
	}
	return TryNotionalValue(quantity, price, m.Multiplier, m.Inverse, useQuoteForInverse, denomination)
}

func (m Model) CalculateNotionalValue(quantity decimal.Quantity, price decimal.Price, useQuoteForInverse bool) money.Money {
	result, err := m.TryCalculateNotionalValue(quantity, price, useQuoteForInverse)
	if err != nil {
		panic(err)
	}
	return result
}

func TryNotionalValue(quantity decimal.Quantity, price decimal.Price, multiplier decimal.Quantity,
	inverse, useQuoteForInverse bool, denomination currency.Currency,
) (money.Money, error) {
	var amount decimal.Decimal
	if inverse && !useQuoteForInverse {
		if !price.IsPositive() {
			return money.Money{}, fmt.Errorf("price must be positive for inverse notional valuation")
		}
		product := quantity.Decimal().Mul(multiplier.Decimal())
		var err error
		amount, err = product.Quo(price.Decimal(), decimal.MaxPrecision, decimal.RoundHalfEven)
		if err != nil {
			return money.Money{}, fmt.Errorf("inverse notional calculation overflow")
		}
	} else if inverse {
		amount = quantity.Decimal()
	} else {
		amount = quantity.Decimal().Mul(multiplier.Decimal()).Mul(price.Decimal())
	}
	result, err := money.FromDecimal(amount, denomination)
	if err != nil {
		return money.Money{}, fmt.Errorf("notional calculation overflow")
	}
	return result, nil
}

func (m Model) tickRule() (TickSchemeRule, bool) {
	if m.TickScheme == "" {
		return nil, false
	}
	return TickSchemeRuleFromName(m.TickScheme)
}

func (m Model) NextBidPrice(value float64, n int32) (decimal.Price, bool) {
	return m.nextPrice(value, n, false)
}

func (m Model) NextAskPrice(value float64, n int32) (decimal.Price, bool) {
	return m.nextPrice(value, n, true)
}

func (m Model) nextPrice(value float64, n int32, ask bool) (decimal.Price, bool) {
	if n < 0 {
		return decimal.Price{}, false
	}
	var price decimal.Price
	var ok bool
	if rule, exists := m.tickRule(); exists {
		if ask {
			price, ok = rule.NextAskPrice(value, n, m.PricePrecision)
		} else {
			price, ok = rule.NextBidPrice(value, n, m.PricePrecision)
		}
	} else {
		if !finitePrice(value) {
			return decimal.Price{}, false
		}
		input, increment := floatRat(value), floatRatFromDecimal(m.PriceIncrement.Decimal())
		index := floorRat(new(big.Rat).Quo(input, increment))
		if ask && new(big.Rat).Mul(new(big.Rat).SetInt(index), increment).Cmp(input) < 0 {
			index.Add(index, big.NewInt(1))
		}
		if ask {
			index.Add(index, big.NewInt(int64(n)))
		} else {
			index.Sub(index, big.NewInt(int64(n)))
		}
		result := new(big.Rat).Mul(new(big.Rat).SetInt(index), increment)
		var err error
		price, err = decimal.NewPrice(ratString(result, m.PricePrecision), m.PricePrecision)
		if err != nil {
			return decimal.Price{}, false
		}
		ok = true
	}
	if !ok || m.MinPrice != nil && price.Cmp(*m.MinPrice) < 0 ||
		m.MaxPrice != nil && price.Cmp(*m.MaxPrice) > 0 {
		return decimal.Price{}, false
	}
	return price, true
}

func floatRatFromDecimal(value decimal.Decimal) *big.Rat {
	rat, ok := new(big.Rat).SetString(value.String())
	if !ok {
		panic("invalid decimal")
	}
	return rat
}

func (m Model) NextBidPrices(value float64, count int) []decimal.Price {
	return m.nextPrices(value, count, false)
}

func (m Model) NextAskPrices(value float64, count int) []decimal.Price {
	return m.nextPrices(value, count, true)
}

func (m Model) nextPrices(value float64, count int, ask bool) []decimal.Price {
	prices := make([]decimal.Price, 0, count)
	for index := 0; index < count && index <= math.MaxInt32; index++ {
		var price decimal.Price
		var ok bool
		if ask {
			price, ok = m.NextAskPrice(value, int32(index))
		} else {
			price, ok = m.NextBidPrice(value, int32(index))
		}
		if !ok {
			break
		}
		prices = append(prices, price)
	}
	return prices
}
