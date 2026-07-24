package decimal

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

const (
	// MaxSignificantDigits is the repository-wide economic precision budget.
	MaxSignificantDigits uint32 = 38
	// MaxScale matches the default NUMERIC(38,18) persistence boundary.
	MaxScale         uint8 = 18
	maxIntegerDigits       = MaxSignificantDigits - uint32(MaxScale)
	maxParseLength         = 128
)

var (
	ErrInvalidSyntax  = errors.New("invalid decimal syntax")
	ErrPrecision      = errors.New("decimal exceeds precision budget")
	ErrScale          = errors.New("decimal exceeds scale budget")
	ErrInexact        = errors.New("decimal operation is inexact")
	ErrOperation      = errors.New("decimal operation name is required")
	ErrRoundingMode   = errors.New("unsupported decimal rounding mode")
	ErrDivisionByZero = errors.New("decimal division by zero")
	ErrArithmetic     = errors.New("decimal arithmetic failed")
)

// RoundingMode names an explicit policy-boundary rounding rule.
type RoundingMode uint8

const (
	roundingModeInvalid RoundingMode = iota
	RoundHalfEven
	RoundTowardZero
)

// Decimal is an immutable exact economic value. Its zero value is numeric
// zero. The wrapped apd value is never exposed.
type Decimal struct {
	value apd.Decimal
}

// Parse accepts a plain base-10 economic value and returns its canonical
// representation. Scientific notation, leading plus, whitespace, underscores,
// NaN, infinity, and incomplete decimal forms are rejected.
func Parse(text string) (Decimal, error) {
	return ParseWithMaxScale(text, MaxScale)
}

// ParseWithMaxScale applies the strict economic grammar and rejects lexical
// scale above the field's declared limit before canonicalization.
func ParseWithMaxScale(text string, maxScale uint8) (Decimal, error) {
	if maxScale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %d > %d", ErrScale, maxScale, MaxScale)
	}
	if !validPlainDecimal(text) {
		return Decimal{}, fmt.Errorf("%w: %q", ErrInvalidSyntax, text)
	}
	if scale := lexicalScale(text); scale > int(maxScale) {
		return Decimal{}, fmt.Errorf("%w: %d > %d", ErrScale, scale, maxScale)
	}
	value, condition, err := apd.NewFromString(text)
	if err != nil || condition.Any() {
		return Decimal{}, fmt.Errorf("%w: %q", ErrInvalidSyntax, text)
	}
	return fromAPD(value)
}

func lexicalScale(text string) int {
	point := strings.IndexByte(text, '.')
	if point < 0 {
		return 0
	}
	return len(text) - point - 1
}

// NewScaled constructs an economic decimal from an exact integer coefficient
// and a number of fractional decimal places. The input integer is copied.
func NewScaled(coefficient *big.Int, scale uint8) (Decimal, error) {
	if coefficient == nil {
		return Decimal{}, fmt.Errorf("%w: nil coefficient", ErrInvalidSyntax)
	}
	if scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %d > %d", ErrScale, scale, MaxScale)
	}
	var apdCoefficient apd.BigInt
	apdCoefficient.SetMathBigInt(coefficient)
	value := apd.NewWithBigInt(&apdCoefficient, -int32(scale))
	return fromAPD(value)
}

func validPlainDecimal(text string) bool {
	if text == "" || len(text) > maxParseLength ||
		strings.TrimSpace(text) != text || text[0] == '+' {
		return false
	}
	index := 0
	if text[0] == '-' {
		index++
		if index == len(text) {
			return false
		}
	}
	wholeDigits := 0
	for index < len(text) && text[index] >= '0' && text[index] <= '9' {
		index++
		wholeDigits++
	}
	if wholeDigits == 0 {
		return false
	}
	if index == len(text) {
		return true
	}
	if text[index] != '.' {
		return false
	}
	index++
	fractionStart := index
	for index < len(text) && text[index] >= '0' && text[index] <= '9' {
		index++
	}
	return index == len(text) && index > fractionStart
}

func fromAPD(input *apd.Decimal) (Decimal, error) {
	if input == nil || input.Form != apd.Finite {
		return Decimal{}, fmt.Errorf("%w: non-finite value", ErrInvalidSyntax)
	}
	var reduced apd.Decimal
	reduced.Reduce(input)
	if reduced.IsZero() {
		reduced.SetInt64(0)
		return Decimal{value: reduced}, nil
	}
	if digits := reduced.NumDigits(); digits > int64(MaxSignificantDigits) {
		return Decimal{}, fmt.Errorf(
			"%w: %d significant digits > %d",
			ErrPrecision,
			digits,
			MaxSignificantDigits,
		)
	}
	if integerDigits := reduced.NumDigits() + int64(reduced.Exponent); integerDigits > int64(maxIntegerDigits) {
		return Decimal{}, fmt.Errorf(
			"%w: %d integer digits > %d",
			ErrPrecision,
			integerDigits,
			maxIntegerDigits,
		)
	}
	if reduced.Exponent < -int32(MaxScale) {
		return Decimal{}, fmt.Errorf(
			"%w: %d fractional digits > %d",
			ErrScale,
			-reduced.Exponent,
			MaxScale,
		)
	}
	var stored apd.Decimal
	stored.Set(&reduced)
	return Decimal{value: stored}, nil
}

func (d Decimal) valueCopy() *apd.Decimal {
	return new(apd.Decimal).Set(&d.value)
}

// String returns canonical plain decimal notation.
func (d Decimal) String() string {
	if d.IsZero() {
		return "0"
	}
	return d.value.Text('f')
}

// Scale returns the canonical number of fractional decimal places.
func (d Decimal) Scale() uint8 {
	if d.IsZero() || d.value.Exponent >= 0 {
		return 0
	}
	// fromAPD is the sole storage boundary and rejects exponents below -MaxScale.
	//nolint:gosec // The validated range is 1..MaxScale, which fits uint8.
	return uint8(-d.value.Exponent)
}

func (d Decimal) Sign() int {
	return d.value.Sign()
}

func (d Decimal) IsZero() bool {
	return d.value.IsZero()
}

func (d Decimal) Equal(other Decimal) bool {
	return d.value.Cmp(&other.value) == 0
}

func (d Decimal) Cmp(other Decimal) int {
	return d.value.Cmp(&other.value)
}

func (d Decimal) Add(other Decimal) (Decimal, error) {
	return exactBinary("add", d, other, (*apd.Context).Add)
}

func (d Decimal) Sub(other Decimal) (Decimal, error) {
	return exactBinary("subtract", d, other, (*apd.Context).Sub)
}

func (d Decimal) Mul(other Decimal) (Decimal, error) {
	return exactBinary("multiply", d, other, (*apd.Context).Mul)
}

type binaryOperation func(
	*apd.Context,
	*apd.Decimal,
	*apd.Decimal,
	*apd.Decimal,
) (apd.Condition, error)

func exactBinary(
	name string,
	left Decimal,
	right Decimal,
	operation binaryOperation,
) (Decimal, error) {
	context := exactContext()
	var result apd.Decimal
	condition, err := operation(
		&context,
		&result,
		left.valueCopy(),
		right.valueCopy(),
	)
	if err != nil || condition&(apd.Inexact|apd.Rounded) != 0 {
		return Decimal{}, classifyArithmeticError(name, condition, err)
	}
	return fromAPD(&result)
}

func exactContext() apd.Context {
	context := apd.BaseContext
	context.Precision = MaxSignificantDigits
	context.MaxExponent = int32(maxIntegerDigits - 1)
	context.MinExponent = -int32(MaxScale)
	context.Rounding = apd.RoundHalfEven
	context.Traps |= apd.Inexact | apd.Rounded | apd.Clamped
	return context
}

func roundingContext(mode RoundingMode) (apd.Context, error) {
	context := apd.BaseContext
	context.Precision = MaxSignificantDigits
	context.MaxExponent = int32(maxIntegerDigits - 1)
	context.MinExponent = -int32(MaxScale)
	switch mode {
	case RoundHalfEven:
		context.Rounding = apd.RoundHalfEven
	case RoundTowardZero:
		context.Rounding = apd.RoundDown
	case roundingModeInvalid:
		return apd.Context{}, fmt.Errorf("%w: omitted", ErrRoundingMode)
	default:
		return apd.Context{}, fmt.Errorf("%w: %d", ErrRoundingMode, mode)
	}
	return context, nil
}

// Quantize rounds d to scale at a named policy boundary.
func (d Decimal) Quantize(
	scale uint8,
	mode RoundingMode,
	operation string,
) (Decimal, error) {
	context, err := validateRoundingBoundary(scale, mode, operation)
	if err != nil {
		return Decimal{}, err
	}
	var result apd.Decimal
	condition, quantizeErr := context.Quantize(
		&result,
		d.valueCopy(),
		-int32(scale),
	)
	if quantizeErr != nil || condition&^(apd.Inexact|apd.Rounded) != 0 {
		return Decimal{}, classifyArithmeticError(operation, condition, quantizeErr)
	}
	return fromAPD(&result)
}

// MulQuantized multiplies exactly, then rounds once at the named boundary.
func MulQuantized(
	left Decimal,
	right Decimal,
	scale uint8,
	mode RoundingMode,
	operation string,
) (Decimal, error) {
	if _, err := validateRoundingBoundary(scale, mode, operation); err != nil {
		return Decimal{}, err
	}

	coefficient := left.value.Coeff.MathBigInt()
	coefficient.Mul(coefficient, right.value.Coeff.MathBigInt())
	if left.value.Negative != right.value.Negative {
		coefficient.Neg(coefficient)
	}
	exponent := int64(left.value.Exponent) +
		int64(right.value.Exponent) +
		int64(scale)
	rounded := quantizedCoefficient(coefficient, exponent, mode)
	result, err := NewScaled(rounded, scale)
	if err != nil {
		return Decimal{}, fmt.Errorf("%s: %w", operation, err)
	}
	return result, nil
}

// QuoQuantized divides exactly at the requested scale and rounds once using
// integer quotient/remainder arithmetic, avoiding double rounding.
func QuoQuantized(
	numerator Decimal,
	denominator Decimal,
	scale uint8,
	mode RoundingMode,
	operation string,
) (Decimal, error) {
	if _, err := validateRoundingBoundary(scale, mode, operation); err != nil {
		return Decimal{}, err
	}
	if denominator.IsZero() {
		return Decimal{}, ErrDivisionByZero
	}

	dividend := numerator.value.Coeff.MathBigInt()
	divisor := denominator.value.Coeff.MathBigInt()
	if numerator.value.Negative {
		dividend.Neg(dividend)
	}
	if denominator.value.Negative {
		divisor.Neg(divisor)
	}
	exponent := int64(numerator.value.Exponent) -
		int64(denominator.value.Exponent) +
		int64(scale)
	if exponent >= 0 {
		dividend.Mul(dividend, powerOfTen(exponent))
	} else {
		divisor.Mul(divisor, powerOfTen(-exponent))
	}

	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(dividend, divisor, remainder)
	roundQuotient(quotient, remainder, divisor, mode)
	result, err := NewScaled(quotient, scale)
	if err != nil {
		return Decimal{}, fmt.Errorf("%s: %w", operation, err)
	}
	return result, nil
}

func validateRoundingBoundary(
	scale uint8,
	mode RoundingMode,
	operation string,
) (apd.Context, error) {
	if operation == "" {
		return apd.Context{}, ErrOperation
	}
	if scale > MaxScale {
		return apd.Context{}, fmt.Errorf("%w: %d > %d", ErrScale, scale, MaxScale)
	}
	return roundingContext(mode)
}

func quantizedCoefficient(
	coefficient *big.Int,
	exponent int64,
	mode RoundingMode,
) *big.Int {
	if exponent >= 0 {
		return new(big.Int).Mul(coefficient, powerOfTen(exponent))
	}
	divisor := powerOfTen(-exponent)
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(coefficient, divisor, remainder)
	roundQuotient(quotient, remainder, divisor, mode)
	return quotient
}

func roundQuotient(
	quotient *big.Int,
	remainder *big.Int,
	divisor *big.Int,
	mode RoundingMode,
) {
	if mode == RoundTowardZero || remainder.Sign() == 0 {
		return
	}
	twiceRemainder := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
	absoluteDivisor := new(big.Int).Abs(new(big.Int).Set(divisor))
	comparison := twiceRemainder.Cmp(absoluteDivisor)
	odd := new(big.Int).Abs(new(big.Int).Set(quotient)).Bit(0) == 1
	if comparison < 0 || (comparison == 0 && !odd) {
		return
	}
	if remainder.Sign() == divisor.Sign() {
		quotient.Add(quotient, big.NewInt(1))
	} else {
		quotient.Sub(quotient, big.NewInt(1))
	}
}

func powerOfTen(exponent int64) *big.Int {
	return new(big.Int).Exp(
		big.NewInt(10),
		big.NewInt(exponent),
		nil,
	)
}

func classifyArithmeticError(
	operation string,
	condition apd.Condition,
	err error,
) error {
	switch {
	case condition.DivisionByZero():
		return fmt.Errorf("%s: %w", operation, ErrDivisionByZero)
	case condition.Inexact() || condition.Rounded():
		return fmt.Errorf(
			"%s: %w: %w: %s",
			operation,
			ErrPrecision,
			ErrInexact,
			condition,
		)
	case condition.Overflow() || condition.SystemOverflow() ||
		condition.Underflow() || condition.SystemUnderflow() ||
		condition.Subnormal() || condition.Clamped():
		return fmt.Errorf("%s: %w: %s", operation, ErrPrecision, condition)
	case err != nil:
		return fmt.Errorf("%s: %w: %w", operation, ErrArithmetic, err)
	default:
		return fmt.Errorf("%s: %w: %s", operation, ErrArithmetic, condition)
	}
}

func (d Decimal) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func (d *Decimal) UnmarshalText(data []byte) error {
	if d == nil {
		return fmt.Errorf("%w: nil decimal receiver", ErrInvalidSyntax)
	}
	value, err := Parse(string(data))
	if err != nil {
		return err
	}
	*d = value
	return nil
}

func (d Decimal) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Decimal) UnmarshalJSON(data []byte) error {
	if d == nil || len(data) == 0 || data[0] != '"' {
		return fmt.Errorf("%w: economic JSON decimals must be strings", ErrInvalidSyntax)
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSyntax, err)
	}
	return d.UnmarshalText([]byte(text))
}
