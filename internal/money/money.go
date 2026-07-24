// Package money provides exact monetary values denominated in a currency.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strings"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
)

const fixedPrecision = decimal.MaxPrecision

// Money is an immutable fixed-point monetary amount. Raw values use the
// rewrite's fixed precision (16 digits), except currencies above that precision
// which retain their native scale for source compatibility.
type Money struct {
	raw      *big.Int
	currency currency.Currency
}

// OutOfRangeError reports an amount outside the supported Money bounds.
type OutOfRangeError struct {
	Value string
}

func (e OutOfRangeError) Error() string {
	return fmt.Sprintf(
		"invalid decimal for 'amount' not in range [%s, %s], was %s",
		minAmount(),
		maxAmount(),
		e.Value,
	)
}

// PredicateViolationError reports a raw or precision invariant violation.
type PredicateViolationError struct {
	Message string
}

func (e PredicateViolationError) Error() string {
	return e.Message
}

// NotPositiveError reports a zero or negative monetary value.
type NotPositiveError struct {
	Param string
	Value string
}

func (e NotPositiveError) Error() string {
	return fmt.Sprintf("invalid `Money` for '%s' not positive, was %s", e.Param, e.Value)
}

// New parses an exact amount and rounds it to the currency precision using
// round-half-to-even.
func New(amount string, denomination currency.Currency) (Money, error) {
	value, err := decimal.Parse(amount)
	if err != nil {
		return Money{}, fmt.Errorf("parse money amount: %w", err)
	}
	return FromDecimal(value, denomination)
}

// MustNew is New for source-derived values and fixtures.
func MustNew(amount string, denomination currency.Currency) Money {
	value, err := New(amount, denomination)
	if err != nil {
		panic(err)
	}
	return value
}

// FromDecimal constructs Money from an exact decimal.
func FromDecimal(value decimal.Decimal, denomination currency.Currency) (Money, error) {
	if denomination.Precision > fixedPrecision {
		return Money{}, PredicateViolationError{Message: fmt.Sprintf(
			"`precision` exceeded maximum `FIXED_PRECISION` (%d), was %d",
			fixedPrecision,
			denomination.Precision,
		)}
	}
	rounded := value.Quantize(denomination.Precision, decimal.RoundHalfEven)
	if rounded.Cmp(minAmount()) < 0 || rounded.Cmp(maxAmount()) > 0 {
		return Money{}, OutOfRangeError{Value: rounded.String()}
	}
	raw, err := rawFromDecimal(rounded, denomination.Precision)
	if err != nil {
		return Money{}, err
	}
	return Money{raw: raw, currency: denomination}, nil
}

// FromRawChecked constructs Money from a fixed-point raw integer.
func FromRawChecked(raw *big.Int, denomination currency.Currency) (Money, error) {
	if raw == nil {
		raw = new(big.Int)
	}
	if denomination.Precision > 18 {
		return Money{}, PredicateViolationError{Message: fmt.Sprintf(
			"`precision` exceeded maximum supported precision (18), was %d",
			denomination.Precision,
		)}
	}
	if raw.Cmp(MaxRaw()) > 0 || raw.Cmp(MinRaw()) < 0 {
		return Money{}, PredicateViolationError{Message: fmt.Sprintf(
			"`raw` value %s exceeded bounds [%s, %s] for Money",
			raw,
			MinRaw(),
			MaxRaw(),
		)}
	}
	return Money{raw: new(big.Int).Set(raw), currency: denomination}, nil
}

// MustFromRaw is FromRawChecked for source-derived values.
func MustFromRaw(raw *big.Int, denomination currency.Currency) Money {
	value, err := FromRawChecked(raw, denomination)
	if err != nil {
		panic(err)
	}
	return value
}

// FromMantissaExponent constructs mantissa*10^exponent exactly.
func FromMantissaExponent(mantissa int64, exponent int, denomination currency.Currency) Money {
	if mantissa == 0 {
		return Zero(denomination)
	}
	if exponent > 38 {
		panic("value exceeds i128 range in Money::from_mantissa_exponent")
	}
	value, err := decimal.Parse(fmt.Sprintf("%de%d", mantissa, exponent))
	if err != nil {
		panic(fmt.Sprintf("Money::from_mantissa_exponent: %v", err))
	}
	result, err := FromDecimal(value, denomination)
	if err != nil {
		panic(fmt.Sprintf("Money::from_mantissa_exponent: %v", err))
	}
	return result
}

// Zero returns zero in denomination.
func Zero(denomination currency.Currency) Money {
	if denomination.Precision > fixedPrecision {
		panic(PredicateViolationError{Message: fmt.Sprintf(
			"`precision` exceeded maximum `FIXED_PRECISION` (%d), was %d",
			fixedPrecision,
			denomination.Precision,
		)})
	}
	return Money{currency: denomination}
}

// MaxRaw returns a copy of the maximum fixed-point raw value.
func MaxRaw() *big.Int {
	value, _ := new(big.Int).SetString("170141183460460000000000000000", 10)
	return value
}

// MinRaw returns a copy of the minimum fixed-point raw value.
func MinRaw() *big.Int {
	return new(big.Int).Neg(MaxRaw())
}

func maxAmount() decimal.Decimal {
	return decimal.MustParse("17014118346046")
}

func minAmount() decimal.Decimal {
	return decimal.MustParse("-17014118346046")
}

// Raw returns a copy of the fixed-point raw integer.
func (m Money) Raw() *big.Int {
	if m.raw == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(m.raw)
}

// Currency returns the denomination metadata.
func (m Money) Currency() currency.Currency {
	return m.currency
}

// Decimal returns the amount at the currency's display precision.
func (m Money) Decimal() decimal.Decimal {
	scale := m.currency.Precision
	rawScale := scale
	if rawScale <= fixedPrecision {
		rawScale = fixedPrecision
	}
	text := decimalText(m.Raw(), rawScale)
	value := decimal.MustParse(text)
	return value.Quantize(scale, decimal.RoundTowardZero)
}

// Equal compares raw amount and currency code.
func (m Money) Equal(other Money) bool {
	return m.Raw().Cmp(other.Raw()) == 0 && m.currency.Equal(other.currency)
}

func (m Money) IsZero() bool     { return m.Raw().Sign() == 0 }
func (m Money) IsPositive() bool { return m.Raw().Sign() > 0 }

// Cmp compares like-denominated amounts and panics on a currency mismatch.
func (m Money) Cmp(other Money) int {
	requireSameCurrency(m, other, "compare")
	return m.Raw().Cmp(other.Raw())
}

// Add returns the exact sum and panics on mismatch or overflow.
func (m Money) Add(other Money) Money {
	requireSameCurrency(m, other, "add")
	result, ok := m.CheckedAdd(other)
	if !ok {
		panic("Overflow occurred when adding `Money`")
	}
	return result
}

// Sub returns the exact difference and panics on mismatch or underflow.
func (m Money) Sub(other Money) Money {
	requireSameCurrency(m, other, "subtract")
	result, ok := m.CheckedSub(other)
	if !ok {
		panic("Underflow occurred when subtracting `Money`")
	}
	return result
}

// CheckedAdd returns false when the result is outside Money bounds.
func (m Money) CheckedAdd(other Money) (Money, bool) {
	requireSameCurrency(m, other, "add")
	raw := new(big.Int).Add(m.Raw(), other.Raw())
	if raw.Cmp(MaxRaw()) > 0 || raw.Cmp(MinRaw()) < 0 {
		return Money{}, false
	}
	return Money{raw: raw, currency: m.currency}, true
}

// CheckedSub returns false when the result is outside Money bounds.
func (m Money) CheckedSub(other Money) (Money, bool) {
	requireSameCurrency(m, other, "subtract")
	raw := new(big.Int).Sub(m.Raw(), other.Raw())
	if raw.Cmp(MaxRaw()) > 0 || raw.Cmp(MinRaw()) < 0 {
		return Money{}, false
	}
	return Money{raw: raw, currency: m.currency}, true
}

func (m Money) Neg() Money {
	return Money{raw: new(big.Int).Neg(m.Raw()), currency: m.currency}
}

func (m Money) AddDecimal(value decimal.Decimal) decimal.Decimal {
	return m.Decimal().Add(value)
}

func (m Money) SubDecimal(value decimal.Decimal) decimal.Decimal {
	return m.Decimal().Sub(value)
}

func (m Money) MulDecimal(value decimal.Decimal) decimal.Decimal {
	return m.Decimal().Mul(value)
}

func (m Money) DivDecimal(value decimal.Decimal) (decimal.Decimal, error) {
	return m.Decimal().Quo(value, m.currency.Precision, decimal.RoundHalfEven)
}

func (m Money) String() string {
	if m.currency.Precision > fixedPrecision {
		return m.Raw().String() + " " + m.currency.Code
	}
	return m.Decimal().String() + " " + m.currency.Code
}

func (m Money) DebugString() string {
	if m.currency.Precision > fixedPrecision {
		return fmt.Sprintf("Money(%s, %s)", m.Raw(), m.currency.Code)
	}
	return fmt.Sprintf("Money(%s, %s)", m.Decimal(), m.currency.Code)
}

// FormattedString inserts underscores into the integer portion.
func (m Money) FormattedString() string {
	parts := strings.SplitN(m.String(), " ", 2)
	amount := parts[0]
	sign := ""
	if strings.HasPrefix(amount, "-") {
		sign, amount = "-", amount[1:]
	}
	numberParts := strings.SplitN(amount, ".", 2)
	whole := numberParts[0]
	for index := len(whole) - 3; index > 0; index -= 3 {
		whole = whole[:index] + "_" + whole[index:]
	}
	formatted := sign + whole
	if len(numberParts) == 2 {
		formatted += "." + numberParts[1]
	}
	return formatted + " " + parts[1]
}

// WriteHash writes the identity fields to h.
func (m Money) WriteHash(h hash.Hash) {
	_, _ = h.Write([]byte(m.Raw().String()))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(m.currency.Code))
}

func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// Parse resolves a textual amount through registry.
func Parse(text string, registry *currency.Registry) (Money, error) {
	parts := strings.Fields(text)
	if len(parts) != 2 {
		return Money{}, fmt.Errorf(
			"Error invalid input format '%s'. Expected '<amount> <currency>'",
			text,
		)
	}
	denomination, err := registry.Lookup(parts[1])
	if err != nil {
		return Money{}, err
	}
	return New(strings.ReplaceAll(parts[0], "_", ""), denomination)
}

func MustParse(text string, registry *currency.Registry) Money {
	value, err := Parse(text, registry)
	if err != nil {
		panic("Condition failed: " + err.Error())
	}
	return value
}

func FromJSON(data []byte, registry *currency.Registry) (Money, error) {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return Money{}, err
	}
	return Parse(text, registry)
}

// CheckPositive returns a typed error for zero and negative values.
func CheckPositive(value Money, param string) error {
	if value.IsPositive() {
		return nil
	}
	return NotPositiveError{Param: param, Value: value.String()}
}

func requireSameCurrency(left, right Money, operation string) {
	if !left.currency.Equal(right.currency) {
		panic(fmt.Sprintf(
			"Currency mismatch: cannot %s %s and %s",
			operation,
			left.currency.Code,
			right.currency.Code,
		))
	}
}

func rawFromDecimal(value decimal.Decimal, precision uint8) (*big.Int, error) {
	scale := precision
	if scale <= fixedPrecision {
		scale = fixedPrecision
	}
	quantized := value.Quantize(scale, decimal.RoundHalfEven)
	text := strings.ReplaceAll(quantized.String(), ".", "")
	raw, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, errors.New("invalid fixed-point amount")
	}
	return raw, nil
}

func decimalText(coefficient *big.Int, scale uint8) string {
	negative := coefficient.Sign() < 0
	digits := new(big.Int).Abs(new(big.Int).Set(coefficient)).String()
	if scale == 0 {
		if negative {
			return "-" + digits
		}
		return digits
	}
	if len(digits) <= int(scale) {
		digits = strings.Repeat("0", int(scale)-len(digits)+1) + digits
	}
	point := len(digits) - int(scale)
	result := digits[:point] + "." + digits[point:]
	if negative {
		return "-" + result
	}
	return result
}
