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
