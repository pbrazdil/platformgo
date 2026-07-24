package decimal

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var (
	maxPrice = MustParse("17014118346046")
	minPrice = MustParse("-17014118346046")
)

// Price is an exact immutable market price. Negative values are valid for
// spreads, basis trades, and derivatives.
type Price struct {
	value Decimal
	kind  priceKind
}

type priceKind uint8

const (
	priceNormal priceKind = iota
	priceUndefined
	priceError
	priceRawError
)

// PriceValidationError classifies a stable price-construction failure.
type PriceValidationError struct {
	Kind    string
	Message string
}

func (e *PriceValidationError) Error() string { return e.Message }

// ParsePrice parses an exact price and infers precision from its decimal
// representation, including trailing zeros.
func ParsePrice(text string) (Price, error) {
	value, err := Parse(text)
	if err != nil {
		return Price{}, fmt.Errorf("parse price: %w", err)
	}
	return priceFromDecimal(value)
}

// NewPrice parses text and rounds it to precision using round-half-to-even.
func NewPrice(text string, precision uint8) (Price, error) {
	if precision > MaxPrecision {
		return Price{}, fmt.Errorf("price precision %d exceeds maximum %d", precision, MaxPrecision)
	}
	value, err := Parse(text)
	if err != nil {
		return Price{}, fmt.Errorf("parse price: %w", err)
	}
	return priceFromDecimal(value.Quantize(precision, RoundHalfEven))
}

// MustPrice is ParsePrice for source-derived constants.
func MustPrice(text string) Price {
	price, err := ParsePrice(text)
	if err != nil {
		panic(err)
	}
	return price
}

// ZeroPrice returns zero at precision.
func ZeroPrice(precision uint8) (Price, error) {
	if precision > MaxPrecision {
		return Price{}, fmt.Errorf("price precision %d exceeds maximum %d", precision, MaxPrecision)
	}
	return Price{value: newDecimal(new(big.Int), precision)}, nil
}

// MaxPrice returns the maximum representable price at precision.
func MaxPrice(precision uint8) (Price, error) {
	if precision > MaxPrecision {
		return Price{}, fmt.Errorf("price precision %d exceeds maximum %d", precision, MaxPrecision)
	}
	return Price{value: maxPrice.Quantize(precision, RoundHalfEven)}, nil
}

// MinPrice returns the minimum representable price at precision.
func MinPrice(precision uint8) (Price, error) {
	if precision > MaxPrecision {
		return Price{}, fmt.Errorf("price precision %d exceeds maximum %d", precision, MaxPrecision)
	}
	return Price{value: minPrice.Quantize(precision, RoundHalfEven)}, nil
}

// PriceFromMantissaExponent constructs mantissa*10^exponent at precision.
func PriceFromMantissaExponent(mantissa int64, exponent int, precision uint8) (Price, error) {
	if precision > MaxPrecision {
		return Price{}, fmt.Errorf("price precision %d exceeds maximum %d", precision, MaxPrecision)
	}
	if mantissa == 0 {
		return ZeroPrice(precision)
	}

	coefficient := big.NewInt(mantissa)
	scaleExponent := exponent + int(precision)
	if scaleExponent >= 0 {
		if scaleExponent > 38 {
			return Price{}, fmt.Errorf("Price::from_mantissa_exponent exceeds i128 range for exponent %d", exponent)
		}
		coefficient.Mul(coefficient, powerOfTen(uint32(scaleExponent)))
	} else {
		coefficient = BankersRound(coefficient, uint32(-scaleExponent))
	}
	return priceFromDecimal(newDecimal(coefficient, precision))
}

// MustPriceFromMantissaExponent is PriceFromMantissaExponent for statically
// valid source-derived values.
func MustPriceFromMantissaExponent(mantissa int64, exponent int, precision uint8) Price {
	price, err := PriceFromMantissaExponent(mantissa, exponent, precision)
	if err != nil {
		panic(fmt.Errorf("Price::from_mantissa_exponent: %w", err))
	}
	return price
}

// Precision returns the number of fractional decimal digits.
func (p Price) Precision() uint8 {
	if p.kind == priceError {
		return 255
	}
	return p.value.Scale()
}

// Decimal returns the exact numeric value.
func (p Price) Decimal() Decimal {
	return newDecimal(p.value.coefficientCopy(), p.value.scale)
}

// IsZero reports whether this is a normal zero price.
func (p Price) IsZero() bool {
	return p.kind == priceNormal && p.value.IsZero()
}

// IsUndefined reports whether this is the undefined-price sentinel.
func (p Price) IsUndefined() bool {
	return p.kind == priceUndefined
}

// IsPositive reports whether the price is greater than zero.
func (p Price) IsPositive() bool {
	return p.kind == priceNormal && p.value.Sign() > 0
}

// RequirePositive returns a stable validation error for zero or negative
// prices.
func (p Price) RequirePositive(name string) error {
	if p.IsUndefined() {
		return fmt.Errorf("invalid `Price` for '%s', was PRICE_UNDEF", name)
	}
	if p.IsPositive() {
		return nil
	}
	return fmt.Errorf("invalid Price for %q: not positive, was %s", name, p)
}

// Add returns the exact sum with the greater operand precision. The boolean is
// false when the result is outside the representable range.
func (p Price) Add(other Price) (Price, bool) {
	if p.kind != priceNormal || other.kind != priceNormal {
		return Price{}, false
	}
	result, err := priceFromDecimal(p.value.Add(other.value))
	return result, err == nil
}

// Sub returns the exact difference with the greater operand precision. The
// boolean is false when the result is outside the representable range.
func (p Price) Sub(other Price) (Price, bool) {
	if p.kind != priceNormal || other.kind != priceNormal {
		return Price{}, false
	}
	result, err := priceFromDecimal(p.value.Sub(other.value))
	return result, err == nil
}

// Neg returns the additive inverse.
func (p Price) Neg() Price {
	if p.kind != priceNormal {
		return p
	}
	return Price{value: p.value.Neg()}
}

// Cmp compares prices numerically.
func (p Price) Cmp(other Price) int {
	return p.value.Cmp(other.value)
}

// Equal compares prices numerically. Precision is formatting metadata and does
// not affect equality, matching the pinned model's raw-value semantics.
func (p Price) Equal(other Price) bool {
	if p.kind != priceNormal || other.kind != priceNormal {
		return p.kind == other.kind
	}
	return p.value.Equal(other.value)
}

// AddDecimal returns the exact decimal sum.
func (p Price) AddDecimal(other Decimal) Decimal {
	return p.value.Add(other)
}

// SubDecimal returns the exact decimal difference.
func (p Price) SubDecimal(other Decimal) Decimal {
	return p.value.Sub(other)
}

// MulDecimal returns the exact decimal product.
func (p Price) MulDecimal(other Decimal) Decimal {
	return p.value.Mul(other)
}

// QuoDecimal divides by other, retaining the price precision.
func (p Price) QuoDecimal(other Decimal) (Decimal, error) {
	return p.value.Quo(other, p.Precision(), RoundHalfEven)
}

func (p Price) String() string {
	switch p.kind {
	case priceUndefined:
		return "PRICE_UNDEF"
	case priceError:
		return "ERROR_PRICE"
	case priceRawError:
		return "PRICE_ERROR"
	}
	return p.value.String()
}

// GoString provides the diagnostic representation used by %#v.
func (p Price) GoString() string {
	return "Price(" + p.String() + ")"
}

// FormattedString groups the integer portion with underscores.
func (p Price) FormattedString() string {
	text := p.String()
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign = "-"
		text = strings.TrimPrefix(text, "-")
	}
	parts := strings.SplitN(text, ".", 2)
	integer := parts[0]
	for index := len(integer) - 3; index > 0; index -= 3 {
		integer = integer[:index] + "_" + integer[index:]
	}
	if len(parts) == 2 {
		return sign + integer + "." + parts[1]
	}
	return sign + integer
}

// MarshalJSON encodes prices as strings so precision and trailing zeros
// survive round trips.
func (p Price) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

// UnmarshalJSON accepts the string representation emitted by MarshalJSON.
func (p *Price) UnmarshalJSON(data []byte) error {
	if p == nil {
		return errors.New("cannot unmarshal Price into nil receiver")
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("price must be a JSON string: %w", err)
	}
	price, err := ParsePrice(text)
	if err != nil {
		return err
	}
	*p = price
	return nil
}

func priceFromDecimal(value Decimal) (Price, error) {
	if value.Scale() > MaxPrecision {
		return Price{}, fmt.Errorf("price precision %d exceeds maximum %d", value.Scale(), MaxPrecision)
	}
	if value.Cmp(minPrice) < 0 || value.Cmp(maxPrice) > 0 {
		return Price{}, &PriceValidationError{
			Kind:    "out_of_range",
			Message: fmt.Sprintf("price %s outside valid range [%s, %s]", value, minPrice, maxPrice),
		}
	}
	return Price{value: newDecimal(value.coefficientCopy(), value.scale)}, nil
}

// UndefinedPrice returns the sentinel used for a missing price.
func UndefinedPrice() Price {
	return Price{kind: priceUndefined}
}

// UndefinedPriceChecked constructs the undefined sentinel, whose precision
// must be zero.
func UndefinedPriceChecked(precision uint8) (Price, error) {
	if precision != 0 {
		return Price{}, errors.New("`precision` must be 0 when `raw` is PRICE_UNDEF")
	}
	return UndefinedPrice(), nil
}

// ErrorPrice returns the error sentinel with its distinct precision marker.
func ErrorPrice() Price {
	return Price{kind: priceError}
}

// RawErrorPrice returns the raw error sentinel.
func RawErrorPrice() Price {
	return Price{kind: priceRawError}
}

// Hash64 returns a stable hash consistent with Equal.
func (p Price) Hash64() uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	text := p.String()
	if p.kind == priceNormal {
		text = p.value.Normalize().String()
	}
	for index := 0; index < len(text); index++ {
		hash ^= uint64(text[index])
		hash *= prime
	}
	return hash
}

// DecodeRawPriceI64 converts a legacy 1e9-scaled raw price without using
// floating point.
func DecodeRawPriceI64(value int64, precision uint8) (Price, error) {
	raw := newDecimal(big.NewInt(value), 9)
	return priceFromDecimal(raw.Quantize(precision, RoundHalfEven))
}
