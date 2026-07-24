package decimal

import "math/big"

var fixedScalar = powerOfTen(uint32(MaxPrecision))

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
	if lhs.Sign() < 0 || rhs.Sign() < 0 {
		return nil, false
	}
	result := new(big.Int).Mul(lhs, rhs)
	result.Quo(result, fixedScalar)
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
