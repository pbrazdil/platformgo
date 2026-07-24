// Package tickmap provides exact integer arithmetic for concentrated-liquidity
// price and liquidity calculations.
package tickmap

import (
	"errors"
	"math/big"
)

var (
	q128    = new(big.Int).Lsh(big.NewInt(1), 128)
	maxU256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	mask128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
)

func Q128() *big.Int    { return new(big.Int).Set(q128) }
func MaxU256() *big.Int { return new(big.Int).Set(maxU256) }

// MulDiv returns floor(a*b/denominator), retaining the full intermediate
// product and rejecting values whose final result does not fit in U256.
func MulDiv(a, b, denominator *big.Int) (*big.Int, error) {
	if !isU256(a) || !isU256(b) || !isU256(denominator) {
		return nil, errors.New("input exceeds unsigned 256-bit range")
	}
	if denominator.Sign() == 0 {
		return nil, errors.New("Result would overflow 256 bits")
	}
	result := new(big.Int).Quo(new(big.Int).Mul(a, b), denominator)
	if result.Cmp(maxU256) > 0 {
		return nil, errors.New("Result would overflow 256 bits")
	}
	return result, nil
}

// MulDivRoundingUp returns ceil(a*b/denominator) with the same U256 bounds.
func MulDivRoundingUp(a, b, denominator *big.Int) (*big.Int, error) {
	result, err := MulDiv(a, b, denominator)
	if err != nil {
		return nil, err
	}
	product := new(big.Int).Mul(a, b)
	if new(big.Int).Rem(product, denominator).Sign() == 0 {
		return result, nil
	}
	if result.Cmp(maxU256) == 0 {
		return nil, errors.New("Result would overflow 256 bits")
	}
	return new(big.Int).Add(result, big.NewInt(1)), nil
}

// TruncateToU128 preserves the low 128 bits, matching a Solidity uint128 cast.
func TruncateToU128(value *big.Int) *big.Int {
	if value == nil {
		return new(big.Int)
	}
	return new(big.Int).And(value, mask128)
}

func isU256(value *big.Int) bool {
	return value != nil && value.Sign() >= 0 && value.Cmp(maxU256) <= 0
}
