package decimal

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/big"
	"strconv"
	"strings"
)

var maxQuantity = MustParse("34028236692093")
var quantityRawMax = new(big.Int).Mul(
	maxQuantity.coefficientCopy(),
	powerOfTen(uint32(MaxPrecision)),
)
var quantityUndefinedRaw = func() *big.Int {
	value := new(big.Int).Lsh(big.NewInt(1), 128)
	return value.Sub(value, big.NewInt(1))
}()

const maxDeFiQuantityPrecision uint8 = 18

// QuantityError classifies stable quantity validation failures.
type QuantityError struct {
	Kind    string
	Message string
}

func (e *QuantityError) Error() string { return e.Message }

// Quantity is an exact immutable non-negative trade or position size.
type Quantity struct {
	value       Decimal
	rawOverride *big.Int
	undefined   bool
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
			return Quantity{}, fmt.Errorf(
				"Quantity::from_mantissa_exponent: exponent %d exceeds i128 range",
				exponent,
			)
		}
		coefficient.Mul(coefficient, powerOfTen(uint32(scaleExponent)))
	} else {
		coefficient = BankersRound(coefficient, uint32(-scaleExponent))
	}
	return quantityFromDecimal(newDecimal(coefficient, precision))
}

// MustQuantityFromMantissaExponent is the panicking form of
// QuantityFromMantissaExponent.
func MustQuantityFromMantissaExponent(mantissa uint64, exponent int, precision uint8) Quantity {
	quantity, err := QuantityFromMantissaExponent(mantissa, exponent, precision)
	if err != nil {
		panic("Quantity::from_mantissa_exponent: " + err.Error())
	}
	return quantity
}

// QuantityFromRawChecked constructs a quantity from a fixed-point raw integer.
// Raw values use MaxPrecision scaling regardless of display precision.
func QuantityFromRawChecked(raw *big.Int, precision uint8) (Quantity, error) {
	if raw == nil || raw.Sign() < 0 {
		return Quantity{}, &QuantityError{
			Kind:    "predicate_violation",
			Message: "raw quantity must be a non-negative integer",
		}
	}
	if raw.Cmp(quantityUndefinedRaw) == 0 {
		if precision != 0 {
			return Quantity{}, &QuantityError{
				Kind:    "predicate_violation",
				Message: "`precision` must be 0 when `raw` is QUANTITY_UNDEF",
			}
		}
		return Quantity{
			rawOverride: new(big.Int).Set(raw),
			undefined:   true,
		}, nil
	}
	if raw.Cmp(quantityRawMax) > 0 {
		return Quantity{}, &QuantityError{
			Kind: "predicate_violation",
			Message: fmt.Sprintf(
				"raw value %s exceeds QUANTITY_RAW_MAX=%s",
				raw,
				quantityRawMax,
			),
		}
	}
	if precision > MaxPrecision {
		return Quantity{}, fmt.Errorf(
			"quantity precision %d exceeds maximum %d",
			precision,
			MaxPrecision,
		)
	}

	coefficient := new(big.Int).Set(raw)
	coefficient.Quo(coefficient, powerOfTen(uint32(MaxPrecision-precision)))
	return Quantity{
		value:       newDecimal(coefficient, precision),
		rawOverride: new(big.Int).Set(raw),
	}, nil
}

// MustQuantityFromRaw is the panicking form of QuantityFromRawChecked.
func MustQuantityFromRaw(raw *big.Int, precision uint8) Quantity {
	quantity, err := QuantityFromRawChecked(raw, precision)
	if err != nil {
		panic("Condition failed: " + err.Error())
	}
	return quantity
}

// QuantityRawMax returns the greatest valid fixed-point raw quantity.
func QuantityRawMax() *big.Int { return new(big.Int).Set(quantityRawMax) }

// QuantityUndefinedRaw returns the raw sentinel for an undefined quantity.
func QuantityUndefinedRaw() *big.Int { return new(big.Int).Set(quantityUndefinedRaw) }

// UndefinedQuantity returns the unset quantity sentinel.
func UndefinedQuantity() Quantity {
	return MustQuantityFromRaw(QuantityUndefinedRaw(), 0)
}

// QuantityFromInt32 constructs an integer quantity.
func QuantityFromInt32(value int32) (Quantity, error) {
	return quantityFromSignedInt64(int64(value))
}

// QuantityFromInt64 constructs an integer quantity.
func QuantityFromInt64(value int64) (Quantity, error) {
	return quantityFromSignedInt64(value)
}

// QuantityFromUint32 constructs an integer quantity.
func QuantityFromUint32(value uint32) (Quantity, error) {
	return ParseQuantity(strconv.FormatUint(uint64(value), 10))
}

// QuantityFromUint64 constructs an integer quantity.
func QuantityFromUint64(value uint64) (Quantity, error) {
	return ParseQuantity(strconv.FormatUint(value, 10))
}

// QuantityFromU256 constructs a quantity from a non-negative 256-bit integer
// carrying precision decimal places.
func QuantityFromU256(amount *big.Int, precision uint8) (Quantity, error) {
	if amount == nil || amount.Sign() < 0 || amount.BitLen() > 256 {
		return Quantity{}, &QuantityError{
			Kind:    "predicate_violation",
			Message: "amount must be an unsigned 256-bit integer",
		}
	}
	if precision > maxDeFiQuantityPrecision {
		return Quantity{}, &QuantityError{
			Kind: "predicate_violation",
			Message: fmt.Sprintf(
				"`precision` exceeded maximum `WEI_PRECISION` (%d), was %d",
				maxDeFiQuantityPrecision,
				precision,
			),
		}
	}

	scaled := new(big.Int).Set(amount)
	if precision < MaxPrecision {
		scaled.Mul(scaled, powerOfTen(uint32(MaxPrecision-precision)))
		if scaled.BitLen() > 256 {
			return Quantity{}, &QuantityError{
				Kind: "predicate_violation",
				Message: fmt.Sprintf(
					"Amount overflow during scaling to fixed precision: %s * 10^%d",
					amount,
					MaxPrecision-precision,
				),
			}
		}
	}
	if scaled.Cmp(quantityRawMax) > 0 {
		return Quantity{}, &QuantityError{
			Kind: "predicate_violation",
			Message: fmt.Sprintf(
				"raw value %s exceeds QUANTITY_RAW_MAX=%s",
				scaled,
				quantityRawMax,
			),
		}
	}

	return quantityFromDecimalAtPrecision(
		newDecimal(new(big.Int).Set(amount), precision),
		maxDeFiQuantityPrecision,
	)
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
	return !q.undefined && q.rawValue().Sign() == 0
}

// IsPositive reports whether the quantity is greater than zero.
func (q Quantity) IsPositive() bool {
	return !q.undefined && q.rawValue().Sign() > 0
}

// IsUndefined reports whether the quantity is the unset sentinel.
func (q Quantity) IsUndefined() bool { return q.undefined }

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
	if q.undefined || other.undefined {
		return Quantity{}, false
	}
	if q.rawOverride != nil || other.rawOverride != nil {
		raw := new(big.Int).Add(q.rawValue(), other.rawValue())
		result, err := QuantityFromRawChecked(raw, max(q.Precision(), other.Precision()))
		return result, err == nil
	}
	result, err := quantityFromDecimal(q.value.Add(other.value))
	return result, err == nil
}

// Sub returns the exact difference at the greater operand precision. The
// boolean is false when the result would be negative.
func (q Quantity) Sub(other Quantity) (Quantity, bool) {
	if q.undefined || other.undefined {
		return Quantity{}, false
	}
	if q.rawOverride != nil || other.rawOverride != nil {
		raw := new(big.Int).Sub(q.rawValue(), other.rawValue())
		if raw.Sign() < 0 {
			return Quantity{}, false
		}
		result, err := QuantityFromRawChecked(raw, max(q.Precision(), other.Precision()))
		return result, err == nil
	}
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
	if q.undefined || other.undefined {
		return Quantity{}, false
	}
	precision := max(q.Precision(), other.Precision())
	product := q.value.Mul(other.value).Quantize(precision, RoundHalfEven)
	result, err := quantityFromDecimal(product)
	return result, err == nil
}

// Cmp compares quantities numerically.
func (q Quantity) Cmp(other Quantity) int {
	return q.rawValue().Cmp(other.rawValue())
}

// Equal compares quantities numerically; precision is formatting metadata.
func (q Quantity) Equal(other Quantity) bool {
	return q.undefined == other.undefined && q.rawValue().Cmp(other.rawValue()) == 0
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
	if q.undefined {
		return quantityUndefinedRaw.String()
	}
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

// Hash64 returns a deterministic hash of the fixed-point raw value.
func (q Quantity) Hash64() uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write(q.rawValue().Bytes())
	return hash.Sum64()
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
	return quantityFromDecimalAtPrecision(value, MaxPrecision)
}

func quantityFromDecimalAtPrecision(value Decimal, maxPrecision uint8) (Quantity, error) {
	if value.Scale() > maxPrecision {
		return Quantity{}, fmt.Errorf("quantity precision %d exceeds maximum %d", value.Scale(), MaxPrecision)
	}
	if value.Sign() < 0 {
		return Quantity{}, fmt.Errorf("decimal value %q is negative, Quantity must be non-negative", value)
	}
	if value.Cmp(maxQuantity) > 0 {
		return Quantity{}, &QuantityError{
			Kind: "out_of_range",
			Message: fmt.Sprintf(
				"quantity %s outside valid range [0, %s]",
				value,
				maxQuantity,
			),
		}
	}
	return Quantity{value: newDecimal(value.coefficientCopy(), value.scale)}, nil
}

func quantityFromSignedInt64(value int64) (Quantity, error) {
	if value < 0 {
		return Quantity{}, fmt.Errorf("cannot create Quantity from negative integer: %d", value)
	}
	return ParseQuantity(strconv.FormatInt(value, 10))
}

func (q Quantity) rawValue() *big.Int {
	if q.rawOverride != nil {
		return new(big.Int).Set(q.rawOverride)
	}
	raw := q.value.coefficientCopy()
	if q.value.scale <= MaxPrecision {
		raw.Mul(raw, powerOfTen(uint32(MaxPrecision-q.value.scale)))
	}
	return raw
}
