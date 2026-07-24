package decimal

import (
	"fmt"
	"math"
	"math/big"
)

var fixedScalar = powerOfTen(uint32(MaxPrecision))

// FixedError identifies a stable fixed-point validation failure.
type FixedError struct {
	Kind    string
	Message string
}

func (e *FixedError) Error() string { return e.Message }

// CheckFixedPrecision validates precision against the Go model's pinned
// high-precision fixed scale.
func CheckFixedPrecision(precision uint8) error {
	return checkFixedPrecisionLimit(precision, MaxPrecision, "FIXED_PRECISION")
}

func checkFixedPrecisionLimit(precision, maximum uint8, name string) error {
	if precision <= maximum {
		return nil
	}
	return &FixedError{
		Kind: "predicate_violation",
		Message: fmt.Sprintf(
			"`precision` exceeded maximum `%s` (%d), was %d",
			name,
			maximum,
			precision,
		),
	}
}

// CheckFixedRaw validates that raw contains no digits beyond precision.
func CheckFixedRaw(raw *big.Int, precision uint8) error {
	return checkFixedRawAt(raw, precision, MaxPrecision)
}

func checkFixedRawAt(raw *big.Int, precision, fixedPrecision uint8) error {
	if precision >= fixedPrecision {
		return nil
	}
	scale := powerOfTen(uint32(fixedPrecision - precision))
	remainder := new(big.Int).Rem(new(big.Int).Set(raw), scale)
	if remainder.Sign() == 0 {
		return nil
	}
	return fmt.Errorf(
		"Invalid fixed-point raw value %s for precision %d: remainder %s when divided by scale %s. Raw value should be a multiple of %s. This indicates data corruption or incorrect precision/scaling upstream",
		raw,
		precision,
		remainder,
		scale,
		scale,
	)
}

// Float64ToFixedInt64 converts value into a signed 64-bit raw value at the
// pinned fixed scale.
func Float64ToFixedInt64(value float64, precision uint8) int64 {
	raw := float64ToFixedBig(value, precision, MaxPrecision, true, 64, "i64")
	return raw.Int64()
}

// Float64ToFixedInt128 converts value into a signed 128-bit raw value.
func Float64ToFixedInt128(value float64, precision uint8) *big.Int {
	return float64ToFixedBig(value, precision, MaxPrecision, true, 128, "i128")
}

// Float64ToFixedUint64 converts value into an unsigned 64-bit raw value.
func Float64ToFixedUint64(value float64, precision uint8) uint64 {
	raw := float64ToFixedBig(value, precision, MaxPrecision, false, 64, "u64")
	return raw.Uint64()
}

// Float64ToFixedUint128 converts value into an unsigned 128-bit raw value.
func Float64ToFixedUint128(value float64, precision uint8) *big.Int {
	return float64ToFixedBig(value, precision, MaxPrecision, false, 128, "u128")
}

func float64ToFixedBig(
	value float64,
	precision, fixedPrecision uint8,
	signed bool,
	bits uint,
	typeName string,
) *big.Int {
	if err := checkFixedPrecisionLimit(precision, fixedPrecision, "FIXED_PRECISION"); err != nil {
		panic("Condition failed: " + err.Error())
	}
	rounded := math.Round(value * math.Pow10(int(precision)))
	if math.IsNaN(rounded) || math.IsInf(rounded, 0) {
		panic("Overflow when scaling f64 to fixed-point " + typeName)
	}
	integer, _ := new(big.Float).SetFloat64(rounded).Int(nil)
	if integer == nil {
		panic("Overflow when scaling f64 to fixed-point " + typeName)
	}
	integer.Mul(integer, powerOfTen(uint32(fixedPrecision-precision)))

	var minimum, maximum *big.Int
	if signed {
		minimum, maximum = signedBounds(bits)
	} else {
		minimum, maximum = unsignedBounds(bits)
	}
	if integer.Cmp(minimum) < 0 || integer.Cmp(maximum) > 0 {
		panic("Overflow when scaling f64 to fixed-point " + typeName)
	}
	return integer
}

func fixedBigToFloat64(value *big.Int, fixedPrecision uint8) float64 {
	numerator, _ := new(big.Float).SetInt(value).Float64()
	return numerator / math.Pow10(int(fixedPrecision))
}

func FixedInt64ToFloat64(value int64) float64 {
	return fixedBigToFloat64(big.NewInt(value), MaxPrecision)
}

func FixedInt128ToFloat64(value *big.Int) float64 {
	return fixedBigToFloat64(value, MaxPrecision)
}

func FixedUint64ToFloat64(value uint64) float64 {
	return fixedBigToFloat64(new(big.Int).SetUint64(value), MaxPrecision)
}

func FixedUint128ToFloat64(value *big.Int) float64 {
	return fixedBigToFloat64(value, MaxPrecision)
}

// BankersRound removes excess base-10 digits using round-half-to-even.
//
// The pinned source accepts an i128 mantissa. Any excess of 39 or more must
// therefore produce zero without constructing an overflowing 10^excess.
func BankersRound(mantissa *big.Int, excess uint32) *big.Int {
	return bankersRound(mantissa, excess, true)
}

func bankersRound(mantissa *big.Int, excess uint32, i128Bounded bool) *big.Int {
	if excess == 0 {
		return new(big.Int).Set(mantissa)
	}
	if i128Bounded && excess >= 39 {
		return new(big.Int)
	}

	divisor := powerOfTen(excess)
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(mantissa, divisor, remainder)

	absoluteRemainder := new(big.Int).Abs(new(big.Int).Set(remainder))
	half := new(big.Int).Quo(divisor, big.NewInt(2))
	comparison := absoluteRemainder.Cmp(half)
	quotientOdd := new(big.Int).Abs(new(big.Int).Set(quotient)).Bit(0) == 1
	if comparison > 0 || (comparison == 0 && quotientOdd) {
		if mantissa.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	return quotient
}

// correctRaw rounds a bounded integer to a valid multiple for precision. It
// uses half-away-from-zero and falls back toward zero on bounded overflow.
func correctRaw(raw *big.Int, precision uint8, minimum, maximum *big.Int) *big.Int {
	if precision >= MaxPrecision {
		return new(big.Int).Set(raw)
	}

	scale := powerOfTen(uint32(MaxPrecision - precision))
	remainder := new(big.Int).Rem(new(big.Int).Set(raw), scale)
	if remainder.Sign() == 0 {
		return new(big.Int).Set(raw)
	}

	towardZero := new(big.Int).Sub(new(big.Int).Set(raw), remainder)
	absoluteRemainder := new(big.Int).Abs(new(big.Int).Set(remainder))
	half := new(big.Int).Quo(new(big.Int).Set(scale), big.NewInt(2))
	if absoluteRemainder.Cmp(half) < 0 {
		return towardZero
	}

	away := new(big.Int).Set(towardZero)
	if raw.Sign() < 0 {
		away.Sub(away, scale)
	} else {
		away.Add(away, scale)
	}
	if away.Cmp(minimum) < 0 || away.Cmp(maximum) > 0 {
		return towardZero
	}
	return away
}

// checkedMulDivFixed returns lhs*rhs/10^MaxPrecision, truncated toward zero,
// when the unsigned result fits maximum.
func checkedMulDivFixed(lhs, rhs, maximum *big.Int) (*big.Int, bool) {
	return checkedMulDivScaled(lhs, rhs, fixedScalar, maximum)
}

func checkedMulDivScaled(lhs, rhs, scalar, maximum *big.Int) (*big.Int, bool) {
	if lhs.Sign() < 0 || rhs.Sign() < 0 {
		return nil, false
	}
	result := new(big.Int).Mul(lhs, rhs)
	result.Quo(result, scalar)
	if result.Cmp(maximum) > 0 {
		return nil, false
	}
	return result, true
}

func powerOfTen(exponent uint32) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(uint64(exponent)), nil)
}

func signedBounds(bits uint) (*big.Int, *big.Int) {
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), bits-1), big.NewInt(1))
	minimum := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), bits-1))
	return minimum, maximum
}

func unsignedBounds(bits uint) (*big.Int, *big.Int) {
	minimum := new(big.Int)
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), bits), big.NewInt(1))
	return minimum, maximum
}
