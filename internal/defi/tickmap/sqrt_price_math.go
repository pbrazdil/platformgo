package tickmap

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

var (
	q96     = new(big.Int).Lsh(big.NewInt(1), 96)
	q192    = new(big.Int).Lsh(big.NewInt(1), 192)
	maxU160 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 160), big.NewInt(1))
)

func Q96() *big.Int     { return new(big.Int).Set(q96) }
func MaxU160() *big.Int { return new(big.Int).Set(maxU160) }

func EncodeSqrtRatioX96(amount0, amount1 *big.Int) *big.Int {
	requireUnsigned(amount0, 128, "amount0")
	requireUnsigned(amount1, 128, "amount1")
	if amount1.Sign() == 0 {
		panic("Division by zero")
	}
	if amount0.Sign() == 0 {
		return new(big.Int)
	}

	maxStandardAmount := new(big.Int).Quo(MaxU256(), q192)
	var result *big.Int
	if amount0.Cmp(maxStandardAmount) > 0 {
		sqrtAmount0 := new(big.Int).Sqrt(amount0)
		sqrtAmount1 := new(big.Int).Sqrt(amount1)
		if sqrtAmount1.Sign() == 0 {
			panic("Division by zero in sqrt")
		}
		var err error
		result, err = MulDiv(sqrtAmount0, q96, sqrtAmount1)
		if err != nil {
			panic("mul_div overflow")
		}
	} else {
		ratio, err := MulDiv(amount0, q192, amount1)
		if err != nil {
			panic("mul_div overflow")
		}
		result = new(big.Int).Sqrt(ratio)
	}
	if result.Cmp(maxU160) > 0 {
		return MaxU160()
	}
	return result
}

func GetNextSqrtPriceFromInput(
	sqrtPriceX96, liquidity, amountIn *big.Int,
	zeroForOne bool,
) *big.Int {
	requireU160(sqrtPriceX96)
	requireUnsigned(liquidity, 128, "liquidity")
	requireU256(amountIn, "amount_in")
	if sqrtPriceX96.Sign() == 0 {
		panic("sqrt_price_x96 must be greater than zero")
	}
	if liquidity.Sign() == 0 {
		panic("Liquidity must be greater than zero")
	}
	if zeroForOne {
		return nextSqrtPriceAmount0RoundingUp(sqrtPriceX96, liquidity, amountIn, true)
	}
	return nextSqrtPriceAmount1RoundingDown(sqrtPriceX96, liquidity, amountIn, true)
}

func GetNextSqrtPriceFromOutput(
	sqrtPriceX96, liquidity, amountOut *big.Int,
	zeroForOne bool,
) *big.Int {
	requireU160(sqrtPriceX96)
	requireUnsigned(liquidity, 128, "liquidity")
	requireU256(amountOut, "amount_out")
	if sqrtPriceX96.Sign() == 0 {
		panic("sqrt_price_x96 must be greater than zero")
	}
	if liquidity.Sign() == 0 {
		panic("Liquidity must be greater than zero")
	}
	if zeroForOne {
		return nextSqrtPriceAmount1RoundingDown(sqrtPriceX96, liquidity, amountOut, false)
	}
	return nextSqrtPriceAmount0RoundingUp(sqrtPriceX96, liquidity, amountOut, false)
}

func nextSqrtPriceAmount0RoundingUp(
	sqrtPriceX96, liquidity, amount *big.Int,
	add bool,
) *big.Int {
	if amount.Sign() == 0 {
		return new(big.Int).Set(sqrtPriceX96)
	}
	numerator := new(big.Int).Lsh(new(big.Int).Set(liquidity), 96)
	product := u256Mul(amount, sqrtPriceX96)
	if add {
		if new(big.Int).Quo(new(big.Int).Set(product), amount).Cmp(sqrtPriceX96) == 0 {
			denominator := u256Add(numerator, product)
			if denominator.Cmp(numerator) >= 0 {
				result, err := MulDivRoundingUp(numerator, sqrtPriceX96, denominator)
				if err != nil {
					panic("mul_div_rounding_up failed")
				}
				return requireResultU160(result)
			}
		}
		fallbackDenominator := new(big.Int).Add(
			new(big.Int).Quo(new(big.Int).Set(numerator), sqrtPriceX96),
			amount,
		)
		result := ceilDiv(numerator, fallbackDenominator)
		return requireResultU160(result)
	}

	if new(big.Int).Quo(new(big.Int).Set(product), amount).Cmp(sqrtPriceX96) != 0 ||
		numerator.Cmp(product) <= 0 {
		panic("Invalid conditions for amount0 removal: overflow or underflow detected")
	}
	denominator := new(big.Int).Sub(numerator, product)
	result, err := MulDivRoundingUp(numerator, sqrtPriceX96, denominator)
	if err != nil {
		panic("mul_div_rounding_up failed")
	}
	return requireResultU160(result)
}

func nextSqrtPriceAmount1RoundingDown(
	sqrtPriceX96, liquidity, amount *big.Int,
	add bool,
) *big.Int {
	var quotient *big.Int
	if amount.Cmp(maxU160) <= 0 {
		scaled := new(big.Int).Lsh(new(big.Int).Set(amount), 96)
		if add {
			quotient = new(big.Int).Quo(scaled, liquidity)
		} else {
			quotient = ceilDiv(scaled, liquidity)
		}
	} else {
		var err error
		if add {
			quotient, err = MulDiv(amount, q96, liquidity)
		} else {
			quotient, err = MulDivRoundingUp(amount, q96, liquidity)
		}
		if err != nil {
			quotient = new(big.Int)
		}
	}
	if add {
		return requireResultU160(u256Add(sqrtPriceX96, quotient))
	}
	if sqrtPriceX96.Cmp(quotient) <= 0 {
		panic("sqrt_price_x96 must be greater than quotient")
	}
	return new(big.Int).Sub(sqrtPriceX96, quotient)
}

func GetAmount0Delta(
	sqrtRatioAX96, sqrtRatioBX96, liquidity *big.Int,
	roundUp bool,
) *big.Int {
	requireU160(sqrtRatioAX96)
	requireU160(sqrtRatioBX96)
	requireUnsigned(liquidity, 128, "liquidity")
	a, b := orderedPrices(sqrtRatioAX96, sqrtRatioBX96)
	numerator1 := new(big.Int).Lsh(new(big.Int).Set(liquidity), 96)
	numerator2 := new(big.Int).Sub(b, a)
	if roundUp {
		result, err := MulDivRoundingUp(numerator1, numerator2, b)
		if err != nil {
			return new(big.Int)
		}
		if a.Sign() == 0 {
			return new(big.Int)
		}
		return ceilDiv(result, a)
	}
	result, err := MulDiv(numerator1, numerator2, b)
	if err != nil {
		result = new(big.Int)
	}
	return new(big.Int).Quo(result, a)
}

func GetAmount1Delta(
	sqrtRatioAX96, sqrtRatioBX96, liquidity *big.Int,
	roundUp bool,
) *big.Int {
	requireU160(sqrtRatioAX96)
	requireU160(sqrtRatioBX96)
	requireUnsigned(liquidity, 128, "liquidity")
	a, b := orderedPrices(sqrtRatioAX96, sqrtRatioBX96)
	difference := new(big.Int).Sub(b, a)
	if liquidity.Sign() == 0 || difference.Sign() == 0 {
		return new(big.Int)
	}
	var (
		result *big.Int
		err    error
	)
	if roundUp {
		result, err = MulDivRoundingUp(liquidity, difference, q96)
	} else {
		result, err = MulDiv(liquidity, difference, q96)
	}
	if err != nil {
		return new(big.Int)
	}
	return result
}

func ExpandTo18Decimals(amount uint64) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(amount), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
}

func DecodeSqrtPriceX96ToPrice(sqrtPriceX96 *big.Int) (decimal.Price, error) {
	requireU160(sqrtPriceX96)
	priceX192 := u256Mul(sqrtPriceX96, sqrtPriceX96)
	raw, err := MulDiv(priceX192, pow10(decimal.MaxPrecision), q192)
	if err != nil {
		return decimal.Price{}, err
	}
	return priceFromRawInteger(raw, decimal.MaxPrecision)
}

func DecodeSqrtPriceX96ToPriceTokensAdjusted(
	sqrtPriceX96 *big.Int,
	token0Decimals, token1Decimals uint8,
	invert bool,
) (decimal.Price, error) {
	requireU160(sqrtPriceX96)
	priceX192 := u256Mul(sqrtPriceX96, sqrtPriceX96)
	decimalDifference := int(token0Decimals) - int(token1Decimals)
	fixedScalar := pow10(decimal.MaxPrecision)
	divisorBase := new(big.Int).Set(q192)
	adjustment := pow10(uint8(absInt(decimalDifference)))

	var numerator *big.Int
	var err error
	if invert {
		if decimalDifference >= 0 {
			denominator, multiplyErr := MulDiv(priceX192, adjustment, big.NewInt(1))
			if multiplyErr != nil {
				return decimal.Price{}, multiplyErr
			}
			numerator, err = MulDiv(divisorBase, fixedScalar, denominator)
		} else {
			adjusted, multiplyErr := MulDiv(divisorBase, adjustment, big.NewInt(1))
			if multiplyErr != nil {
				return decimal.Price{}, multiplyErr
			}
			numerator, err = MulDiv(adjusted, fixedScalar, priceX192)
		}
	} else if decimalDifference >= 0 {
		temporary, multiplyErr := MulDiv(priceX192, adjustment, big.NewInt(1))
		if multiplyErr != nil {
			return decimal.Price{}, multiplyErr
		}
		numerator, err = MulDiv(temporary, fixedScalar, divisorBase)
	} else {
		adjustedDivisor := u256Mul(divisorBase, adjustment)
		numerator, err = MulDiv(priceX192, fixedScalar, adjustedDivisor)
	}
	if err != nil {
		return decimal.Price{}, err
	}
	return priceFromRawInteger(numerator, decimal.MaxPrecision)
}

func orderedPrices(left, right *big.Int) (*big.Int, *big.Int) {
	if left.Cmp(right) <= 0 {
		return new(big.Int).Set(left), new(big.Int).Set(right)
	}
	return new(big.Int).Set(right), new(big.Int).Set(left)
}

func ceilDiv(numerator, denominator *big.Int) *big.Int {
	if denominator.Sign() == 0 {
		panic("division by zero")
	}
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

func u256Mul(left, right *big.Int) *big.Int {
	return new(big.Int).And(new(big.Int).Mul(left, right), maxU256)
}

func u256Add(left, right *big.Int) *big.Int {
	return new(big.Int).And(new(big.Int).Add(left, right), maxU256)
}

func requireResultU160(value *big.Int) *big.Int {
	if value.Sign() < 0 || value.Cmp(maxU160) > 0 {
		panic("Uint conversion error: Value is too large for Uint<160>")
	}
	return value
}

func requireU160(value *big.Int) {
	requireUnsigned(value, 160, "sqrt_price_x96")
}

func requireU256(value *big.Int, name string) {
	requireUnsigned(value, 256, name)
}

func requireUnsigned(value *big.Int, bits int, name string) {
	if value == nil || value.Sign() < 0 || value.BitLen() > bits {
		panic(fmt.Sprintf("%s is outside uint%d", name, bits))
	}
}

func pow10(exponent uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(uint64(exponent)), nil)
}

func priceFromRawInteger(raw *big.Int, precision uint8) (decimal.Price, error) {
	if raw.Sign() < 0 {
		return decimal.Price{}, errors.New("negative decoded price")
	}
	digits := raw.String()
	scale := int(precision)
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	point := len(digits) - scale
	text := digits[:point] + "." + digits[point:]
	return decimal.ParsePrice(text)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
