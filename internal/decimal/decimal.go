// Package decimal provides exact base-10 values for prices, quantities, money,
// margin, fees, and PnL. It never converts through floating point.
package decimal

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// MaxPrecision is the maximum economic precision used by the pinned
// high-precision Nautilus model.
const MaxPrecision uint8 = 16

// RoundingMode controls how discarded decimal digits are rounded.
type RoundingMode uint8

const (
	// RoundHalfEven rounds a midpoint toward the nearest even last digit.
	RoundHalfEven RoundingMode = iota
	// RoundTowardZero discards excess digits.
	RoundTowardZero
)

// Decimal is an immutable exact base-10 value. coefficient is scaled by
// 10^-scale. A nil coefficient represents zero, making the zero value useful.
type Decimal struct {
	coefficient *big.Int
	scale       uint8
}

// Parse returns the exact value represented by text. Underscores and
// scientific notation are accepted, and trailing fractional zeros are
// preserved in Scale and String.
func Parse(text string) (Decimal, error) {
	input := strings.ReplaceAll(strings.TrimSpace(text), "_", "")
	if input == "" {
		return Decimal{}, errors.New("decimal is empty")
	}

	sign := 1
	switch input[0] {
	case '-':
		sign = -1
		input = input[1:]
	case '+':
		input = input[1:]
	}
	if input == "" {
		return Decimal{}, fmt.Errorf("invalid decimal %q", text)
	}
	if !strings.ContainsAny(input, "0123456789") {
		return Decimal{}, fmt.Errorf("invalid decimal %q", text)
	}

	exponent := int64(0)
	if index := strings.IndexAny(input, "eE"); index >= 0 {
		if strings.IndexAny(input[index+1:], "eE") >= 0 {
			return Decimal{}, fmt.Errorf("invalid decimal %q", text)
		}
		exp, ok := new(big.Int).SetString(input[index+1:], 10)
		if !ok || !exp.IsInt64() {
			return Decimal{}, fmt.Errorf("invalid decimal exponent in %q", text)
		}
		exponent = exp.Int64()
		input = input[:index]
	}

	whole := input
	fraction := ""
	if index := strings.IndexByte(input, '.'); index >= 0 {
		if strings.IndexByte(input[index+1:], '.') >= 0 {
			return Decimal{}, fmt.Errorf("invalid decimal %q", text)
		}
		whole = input[:index]
		fraction = input[index+1:]
	}
	if whole == "" {
		whole = "0"
	}
	if !allDigits(whole) || !allDigits(fraction) {
		return Decimal{}, fmt.Errorf("invalid decimal %q", text)
	}

	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		digits = "0"
	}
	coefficient, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("invalid decimal %q", text)
	}
	if sign < 0 {
		coefficient.Neg(coefficient)
	}

	scale := int64(len(fraction)) - exponent
	if scale < 0 {
		coefficient.Mul(coefficient, powerOfTen(uint32(-scale)))
		scale = 0
	}
	if scale > 255 {
		return Decimal{}, fmt.Errorf("decimal scale %d exceeds 255", scale)
	}

	return newDecimal(coefficient, uint8(scale)), nil
}

// MustParse is Parse for source-derived constants.
func MustParse(text string) Decimal {
	value, err := Parse(text)
	if err != nil {
		panic(err)
	}
	return value
}

// Scale returns the number of fractional decimal digits.
func (d Decimal) Scale() uint8 {
	return d.scale
}

// IsZero reports whether the value is zero.
func (d Decimal) IsZero() bool {
	return d.coefficient == nil || d.coefficient.Sign() == 0
}

// Sign returns -1, 0, or 1 according to the value's sign.
func (d Decimal) Sign() int {
	return d.coefficientCopy().Sign()
}

// Add returns the exact sum at the greater operand scale.
func (d Decimal) Add(other Decimal) Decimal {
	left, right := aligned(d, other)
	scale := max(d.scale, other.scale)
	return newDecimal(new(big.Int).Add(left, right), scale)
}

// Sub returns the exact difference at the greater operand scale.
func (d Decimal) Sub(other Decimal) Decimal {
	left, right := aligned(d, other)
	scale := max(d.scale, other.scale)
	return newDecimal(new(big.Int).Sub(left, right), scale)
}

// Neg returns the additive inverse.
func (d Decimal) Neg() Decimal {
	return newDecimal(new(big.Int).Neg(d.coefficientCopy()), d.scale)
}

// Mul returns the exact product.
func (d Decimal) Mul(other Decimal) Decimal {
	scale := uint16(d.scale) + uint16(other.scale)
	if scale > 255 {
		panic("decimal multiplication scale exceeds 255")
	}
	return newDecimal(
		new(big.Int).Mul(d.coefficientCopy(), other.coefficientCopy()),
		uint8(scale),
	)
}

// Quo divides by other and returns targetScale digits using mode.
func (d Decimal) Quo(other Decimal, targetScale uint8, mode RoundingMode) (Decimal, error) {
	if other.IsZero() {
		return Decimal{}, errors.New("decimal division by zero")
	}

	numerator := d.coefficientCopy()
	denominator := other.coefficientCopy()
	exponent := int(targetScale) + int(other.scale) - int(d.scale)
	if exponent >= 0 {
		numerator.Mul(numerator, powerOfTen(uint32(exponent)))
	} else {
		denominator.Mul(denominator, powerOfTen(uint32(-exponent)))
	}

	coefficient, remainder := new(big.Int), new(big.Int)
	coefficient.QuoRem(numerator, denominator, remainder)
	switch mode {
	case RoundHalfEven:
		if remainder.Sign() != 0 {
			twiceRemainder := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
			absoluteDenominator := new(big.Int).Abs(new(big.Int).Set(denominator))
			comparison := twiceRemainder.Cmp(absoluteDenominator)
			coefficientOdd := new(big.Int).Abs(new(big.Int).Set(coefficient)).Bit(0) == 1
			if comparison > 0 || (comparison == 0 && coefficientOdd) {
				if numerator.Sign() == denominator.Sign() {
					coefficient.Add(coefficient, big.NewInt(1))
				} else {
					coefficient.Sub(coefficient, big.NewInt(1))
				}
			}
		}
	case RoundTowardZero:
	default:
		return Decimal{}, fmt.Errorf("unsupported rounding mode %d", mode)
	}
	return newDecimal(coefficient, targetScale), nil
}

// Cmp compares d and other numerically.
func (d Decimal) Cmp(other Decimal) int {
	left, right := aligned(d, other)
	return left.Cmp(right)
}

// Normalize removes trailing fractional zeros.
func (d Decimal) Normalize() Decimal {
	coefficient := d.coefficientCopy()
	scale := d.scale
	ten := big.NewInt(10)
	remainder := new(big.Int)
	for scale > 0 {
		remainder.Rem(coefficient, ten)
		if remainder.Sign() != 0 {
			break
		}
		coefficient.Quo(coefficient, ten)
		scale--
	}
	return newDecimal(coefficient, scale)
}

// Quantize returns the same value at targetScale using mode when digits must
// be discarded.
func (d Decimal) Quantize(targetScale uint8, mode RoundingMode) Decimal {
	coefficient := d.coefficientCopy()
	if targetScale == d.scale {
		return newDecimal(coefficient, targetScale)
	}
	if targetScale > d.scale {
		coefficient.Mul(coefficient, powerOfTen(uint32(targetScale-d.scale)))
		return newDecimal(coefficient, targetScale)
	}

	excess := uint32(d.scale - targetScale)
	switch mode {
	case RoundHalfEven:
		coefficient = bankersRound(coefficient, excess, false)
	case RoundTowardZero:
		coefficient.Quo(coefficient, powerOfTen(excess))
	default:
		panic(fmt.Sprintf("unsupported rounding mode %d", mode))
	}
	return newDecimal(coefficient, targetScale)
}

// Equal compares numeric value while ignoring representational scale.
func (d Decimal) Equal(other Decimal) bool {
	left, right := aligned(d, other)
	return left.Cmp(right) == 0
}

func (d Decimal) String() string {
	coefficient := d.coefficientCopy()
	negative := coefficient.Sign() < 0
	coefficient.Abs(coefficient)
	digits := coefficient.String()

	if d.scale == 0 {
		if negative {
			return "-" + digits
		}
		return digits
	}

	scale := int(d.scale)
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	point := len(digits) - scale
	result := digits[:point] + "." + digits[point:]
	if negative {
		return "-" + result
	}
	return result
}

func newDecimal(coefficient *big.Int, scale uint8) Decimal {
	if coefficient.Sign() == 0 {
		return Decimal{scale: scale}
	}
	return Decimal{coefficient: new(big.Int).Set(coefficient), scale: scale}
}

func (d Decimal) coefficientCopy() *big.Int {
	if d.coefficient == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(d.coefficient)
}

func aligned(left, right Decimal) (*big.Int, *big.Int) {
	leftCoefficient := left.coefficientCopy()
	rightCoefficient := right.coefficientCopy()
	switch {
	case left.scale < right.scale:
		leftCoefficient.Mul(leftCoefficient, powerOfTen(uint32(right.scale-left.scale)))
	case right.scale < left.scale:
		rightCoefficient.Mul(rightCoefficient, powerOfTen(uint32(left.scale-right.scale)))
	}
	return leftCoefficient, rightCoefficient
}

func allDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
