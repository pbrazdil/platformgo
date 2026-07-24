package tickmap

import (
	"fmt"
	"math/big"
)

var (
	minSqrtRatio = big.NewInt(4_295_128_739)
	maxSqrtRatio = tickMathBig("fffd8963efd1fc6a506488495d951d5263988d26", 16)

	tickMathRatios = [...]*big.Int{
		tickMathBig("fffcb933bd6fad37aa2d162d1a594001", 16),
		tickMathBig("fff97272373d413259a46990580e213a", 16),
		tickMathBig("fff2e50f5f656932ef12357cf3c7fdcc", 16),
		tickMathBig("ffe5caca7e10e4e61c3624eaa0941cd0", 16),
		tickMathBig("ffcb9843d60f6159c9db58835c926644", 16),
		tickMathBig("ff973b41fa98c081472e6896dfb254c0", 16),
		tickMathBig("ff2ea16466c96a3843ec78b326b52861", 16),
		tickMathBig("fe5dee046a99a2a811c461f1969c3053", 16),
		tickMathBig("fcbe86c7900a88aedcffc83b479aa3a4", 16),
		tickMathBig("f987a7253ac413176f2b074cf7815e54", 16),
		tickMathBig("f3392b0822b70005940c7a398e4b70f3", 16),
		tickMathBig("e7159475a2c29b7443b29c7fa6e889d9", 16),
		tickMathBig("d097f3bdfd2022b8845ad8f792aa5825", 16),
		tickMathBig("a9f746462d870fdf8a65dc1f90e061e5", 16),
		tickMathBig("70d869a156d2a1b890bb3df62baf32f7", 16),
		tickMathBig("31be135f97d08fd981231505542fcfa6", 16),
		tickMathBig("9aa508b5b7a84e1c677de54f3e99bc9", 16),
		tickMathBig("5d6af8dedb81196699c329225ee604", 16),
		tickMathBig("2216e584f5fa1ea926041bedfe98", 16),
		tickMathBig("48a170391f7dc42444e8fa2", 16),
	}
)

func MinSqrtRatio() *big.Int { return new(big.Int).Set(minSqrtRatio) }
func MaxSqrtRatio() *big.Int { return new(big.Int).Set(maxSqrtRatio) }

// GetSqrtRatioAtTick returns sqrt(1.0001^tick) as a Q64.96 integer.
func GetSqrtRatioAtTick(tick int32) *big.Int {
	if tick < MinTick || tick > MaxTick {
		panic(fmt.Sprintf("Tick %d out of bounds", tick))
	}

	absTick := int64(tick)
	if absTick < 0 {
		absTick = -absTick
	}

	ratio := new(big.Int).Lsh(big.NewInt(1), 128)
	if absTick&1 != 0 {
		ratio.Set(tickMathRatios[0])
	}
	for bit := 1; bit < len(tickMathRatios); bit++ {
		if absTick&(int64(1)<<bit) != 0 {
			ratio.Mul(ratio, tickMathRatios[bit])
			ratio.Rsh(ratio, 128)
		}
	}

	if tick > 0 {
		ratio.Quo(maxU256, ratio)
	}
	ratio.Add(ratio, new(big.Int).SetUint64(0xffff_ffff))
	ratio.Rsh(ratio, 32)
	return requireResultU160(ratio)
}

// GetTickAtSqrtRatio returns the greatest tick whose ratio does not exceed
// sqrtPriceX96.
func GetTickAtSqrtRatio(sqrtPriceX96 *big.Int) int32 {
	requireU160(sqrtPriceX96)
	if sqrtPriceX96.Cmp(minSqrtRatio) < 0 || sqrtPriceX96.Cmp(maxSqrtRatio) >= 0 {
		panic("Sqrt price out of bounds")
	}

	ratio := new(big.Int).Lsh(new(big.Int).Set(sqrtPriceX96), 32)
	msb := MostSignificantBit(ratio)

	var log2X64 *big.Int
	if msb >= 128 {
		log2X64 = new(big.Int).Lsh(big.NewInt(int64(msb-128)), 64)
	} else {
		difference := new(big.Int).Lsh(big.NewInt(int64(128-msb)), 64)
		log2X64 = new(big.Int).Sub(new(big.Int).Add(maxU256, big.NewInt(1)), difference)
	}

	var r *big.Int
	if msb >= 128 {
		r = new(big.Int).Rsh(ratio, uint(msb-127))
	} else {
		r = new(big.Int).Lsh(ratio, uint(127-msb))
	}

	decimals := new(big.Int)
	for bit := 63; bit >= 50; bit-- {
		r.Mul(r, r)
		r.Rsh(r, 127)
		if r.Bit(128) != 0 {
			decimals.SetBit(decimals, bit, 1)
			r.Rsh(r, 1)
		}
	}
	log2X64.Or(log2X64, decimals)

	logSqrt10001 := tickMathWrapU256(new(big.Int).Mul(
		log2X64,
		tickMathBig("255738958999603826347141", 10),
	))
	tickLowOffset := tickMathBig("3402992956809132418596140100660247210", 10)
	tickHighOffset := tickMathBig("291339464771989622907027621153398088495", 10)

	tickLowBits := tickMathWrapU256(new(big.Int).Sub(logSqrt10001, tickLowOffset))
	tickLowBits.Rsh(tickLowBits, 128)
	tickHighBits := tickMathWrapU256(new(big.Int).Add(logSqrt10001, tickHighOffset))
	tickHighBits.Rsh(tickHighBits, 128)

	tickLow := int32(uint32(tickLowBits.Uint64()))
	tickHigh := int32(uint32(tickHighBits.Uint64()))
	if tickLow == tickHigh {
		return tickLow
	}
	if GetSqrtRatioAtTick(tickHigh).Cmp(sqrtPriceX96) <= 0 {
		return tickHigh
	}
	return tickLow
}

func tickMathBig(value string, base int) *big.Int {
	result, ok := new(big.Int).SetString(value, base)
	if !ok {
		panic("invalid tick math integer")
	}
	return result
}

func tickMathWrapU256(value *big.Int) *big.Int {
	return new(big.Int).And(value, maxU256)
}
