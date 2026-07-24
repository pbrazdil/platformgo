package decimal

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var maxQuantity = MustParse("34028236692093")

// Quantity is an exact immutable non-negative trade or position size.
type Quantity struct {
	value Decimal
}

// ParseQuantity parses an exact quantity and infers precision, including
// trailing zeros.
func ParseQuantity(text string) (Quantity, error) {
	value, err := Parse(text)
	if err != nil {
		return Quantity{}, fmt.Errorf("parse quantity: %w", err)
	}
	return quantityFromDecimal(value)
}

// NewQuantity parses text and rounds it to precision using round-half-to-even.
func NewQuantity(text string, precision uint8) (Quantity, error) {
	if precision > MaxPrecision {
		return Quantity{}, fmt.Errorf("quantity precision %d exceeds maximum %d", precision, MaxPrecision)
	}
	value, err := Parse(text)
	if err != nil {
		return Quantity{}, fmt.Errorf("parse quantity: %w", err)
	}
	return quantityFromDecimal(value.Quantize(precision, RoundHalfEven))
}

// NonZeroQuantity is NewQuantity with a post-rounding non-zero invariant.
func NonZeroQuantity(text string, precision uint8) (Quantity, error) {
	quantity, err := NewQuantity(text, precision)
	if err != nil {
		return Quantity{}, err
	}
	if quantity.IsZero() {
		return Quantity{}, fmt.Errorf("quantity %s is zero after rounding to precision %d", text, precision)
	}
	return quantity, nil
}

// MustQuantity is ParseQuantity for source-derived constants.
func MustQuantity(text string) Quantity {
	quantity, err := ParseQuantity(text)
	if err != nil {
		panic(err)
	}
	return quantity
}

// ZeroQuantity returns zero at precision.
func ZeroQuantity(precision uint8) (Quantity, error) {
	if precision > MaxPrecision {
		return Quantity{}, fmt.Errorf("quantity precision %d exceeds maximum %d", precision, MaxPrecision)
	}
	return Quantity{value: newDecimal(new(big.Int), precision)}, nil
}

// MaxQuantity returns the maximum representable quantity at precision.
func MaxQuantity(precision uint8) (Quantity, error) {
	if precision > MaxPrecision {
		return Quantity{}, fmt.Errorf("quantity precision %d exceeds maximum %d", precision, MaxPrecision)
	}
	return Quantity{value: maxQuantity.Quantize(precision, RoundHalfEven)}, nil
}

// QuantityFromMantissaExponent constructs mantissa*10^exponent at precision.
func QuantityFromMantissaExponent(mantissa uint64, exponent int, precision uint8) (Quantity, error) {
	if precision > MaxPrecision {
		return Quantity{}, fmt.Errorf("quantity precision %d exceeds maximum %d", precision, MaxPrecision)
	}
	if mantissa == 0 {
		return ZeroQuantity(precision)
	}

	coefficient := new(big.Int).SetUint64(mantissa)
	scaleExponent := exponent + int(precision)
	if scaleExponent >= 0 {
		if scaleExponent > 38 {
			return Quantity{}, fmt.Errorf("QuantityFromMantissaExponent: exponent %d exceeds integer range", exponent)
		}
		coefficient.Mul(coefficient, powerOfTen(uint32(scaleExponent)))
	} else {
		coefficient = BankersRound(coefficient, uint32(-scaleExponent))
	}
	return quantityFromDecimal(newDecimal(coefficient, precision))
}

// Precision returns the number of fractional decimal digits.
func (q Quantity) Precision() uint8 {
	return q.value.Scale()
}

// Decimal returns the exact numeric value.
func (q Quantity) Decimal() Decimal {
	return newDecimal(q.value.coefficientCopy(), q.value.scale)
}

// IsZero reports whether the quantity is zero.
func (q Quantity) IsZero() bool {
	return q.value.IsZero()
}

// IsPositive reports whether the quantity is greater than zero.
func (q Quantity) IsPositive() bool {
	return q.value.Sign() > 0
}

// RequirePositive validates the positive-quantity invariant.
func (q Quantity) RequirePositive(name string) error {
	if q.IsPositive() {
		return nil
	}
	return fmt.Errorf("invalid Quantity for %q: not positive, was %s", name, q)
}

// Add returns the exact sum at the greater operand precision. The boolean is
// false when the result exceeds the representable range.
func (q Quantity) Add(other Quantity) (Quantity, bool) {
	result, err := quantityFromDecimal(q.value.Add(other.value))
	return result, err == nil
}

// Sub returns the exact difference at the greater operand precision. The
// boolean is false when the result would be negative.
func (q Quantity) Sub(other Quantity) (Quantity, bool) {
	result, err := quantityFromDecimal(q.value.Sub(other.value))
	return result, err == nil
}

// SaturatingSub clamps a negative result to zero at the greater precision.
func (q Quantity) SaturatingSub(other Quantity) Quantity {
	if result, ok := q.Sub(other); ok {
		return result
	}
	zero, err := ZeroQuantity(max(q.Precision(), other.Precision()))
	if err != nil {
		panic(err)
	}
	return zero
}

// Mul returns the scaled product at the greater operand precision. The
// boolean is false when the final result exceeds the representable range.
func (q Quantity) Mul(other Quantity) (Quantity, bool) {
	precision := max(q.Precision(), other.Precision())
	product := q.value.Mul(other.value).Quantize(precision, RoundHalfEven)
	result, err := quantityFromDecimal(product)
	return result, err == nil
}

// Cmp compares quantities numerically.
func (q Quantity) Cmp(other Quantity) int {
	return q.value.Cmp(other.value)
}

// Equal compares quantities numerically; precision is formatting metadata.
func (q Quantity) Equal(other Quantity) bool {
	return q.value.Equal(other.value)
}

// AddDecimal returns the exact decimal sum.
func (q Quantity) AddDecimal(other Decimal) Decimal {
	return q.value.Add(other)
}

// SubDecimal returns the exact decimal difference.
func (q Quantity) SubDecimal(other Decimal) Decimal {
	return q.value.Sub(other)
}

// MulDecimal returns the exact decimal product.
func (q Quantity) MulDecimal(other Decimal) Decimal {
	return q.value.Mul(other)
}

// QuoDecimal divides by other, retaining quantity precision.
func (q Quantity) QuoDecimal(other Decimal) (Decimal, error) {
	return q.value.Quo(other, q.Precision(), RoundHalfEven)
}

func (q Quantity) String() string {
	return q.value.String()
}

// GoString provides the diagnostic representation used by %#v.
func (q Quantity) GoString() string {
	return "Quantity(" + q.String() + ")"
}

// FormattedString groups the integer portion with underscores.
func (q Quantity) FormattedString() string {
	text := q.String()
	parts := strings.SplitN(text, ".", 2)
	integer := parts[0]
	for index := len(integer) - 3; index > 0; index -= 3 {
		integer = integer[:index] + "_" + integer[index:]
	}
	if len(parts) == 2 {
		return integer + "." + parts[1]
	}
	return integer
}

// MarshalJSON encodes quantities as strings so precision survives round trips.
func (q Quantity) MarshalJSON() ([]byte, error) {
	return json.Marshal(q.String())
}

// UnmarshalJSON accepts the string representation emitted by MarshalJSON.
func (q *Quantity) UnmarshalJSON(data []byte) error {
	if q == nil {
		return errors.New("cannot unmarshal Quantity into nil receiver")
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("quantity must be a JSON string: %w", err)
	}
	quantity, err := ParseQuantity(text)
	if err != nil {
		return err
	}
	*q = quantity
	return nil
}

func quantityFromDecimal(value Decimal) (Quantity, error) {
	if value.Scale() > MaxPrecision {
		return Quantity{}, fmt.Errorf("quantity precision %d exceeds maximum %d", value.Scale(), MaxPrecision)
	}
	if value.Sign() < 0 {
		return Quantity{}, fmt.Errorf("decimal value %q is negative, Quantity must be non-negative", value)
	}
	if value.Cmp(maxQuantity) > 0 {
		return Quantity{}, fmt.Errorf("quantity %s outside valid range [0, %s]", value, maxQuantity)
	}
	return Quantity{value: newDecimal(value.coefficientCopy(), value.scale)}, nil
}
