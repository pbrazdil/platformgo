package defi

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type WeiPrice struct {
	raw       *big.Int
	precision uint8
}

var maxSigned128 = func() *big.Int {
	value := new(big.Int).Lsh(big.NewInt(1), 127)
	return value.Sub(value, big.NewInt(1))
}()

func PriceFromWei(raw *big.Int) (WeiPrice, error) {
	if raw == nil {
		raw = new(big.Int)
	}
	if raw.Sign() < 0 || raw.Cmp(maxSigned128) > 0 {
		return WeiPrice{}, errors.New("raw wei value exceeds signed 128-bit range")
	}
	return WeiPrice{raw: new(big.Int).Set(raw), precision: 18}, nil
}

func MustPriceFromWei(raw *big.Int) WeiPrice {
	price, err := PriceFromWei(raw)
	if err != nil {
		panic(err)
	}
	return price
}

func PriceFromRaw(raw *big.Int, precision uint8) WeiPrice {
	if raw == nil {
		raw = new(big.Int)
	}
	return WeiPrice{raw: new(big.Int).Set(raw), precision: precision}
}

func StandardDeFiPrice(value string, precision uint8) (WeiPrice, error) {
	if precision > decimal.MaxPrecision {
		return WeiPrice{}, errors.New("use `Price::from_wei()` for wei values")
	}
	parsed, err := decimal.Parse(value)
	if err != nil {
		return WeiPrice{}, err
	}
	quantized := parsed.Quantize(precision, decimal.RoundHalfEven)
	rawText := strings.ReplaceAll(quantized.String(), ".", "")
	raw, ok := new(big.Int).SetString(rawText, 10)
	if !ok {
		return WeiPrice{}, errors.New("invalid price")
	}
	if quantized.Sign() < 0 && raw.Sign() > 0 {
		raw.Neg(raw)
	}
	return WeiPrice{raw: raw, precision: precision}, nil
}

func (p WeiPrice) Precision() uint8 { return p.precision }

func (p WeiPrice) Wei() *big.Int {
	if p.precision != 18 {
		panic(fmt.Sprintf("Failed to convert price with precision %d to wei (requires precision 18)", p.precision))
	}
	if p.raw.Sign() < 0 {
		panic("Failed to convert negative price to wei")
	}
	return new(big.Int).Set(p.raw)
}

func (p WeiPrice) Decimal() decimal.Decimal {
	sign := ""
	raw := new(big.Int).Set(p.raw)
	if raw.Sign() < 0 {
		sign = "-"
		raw.Abs(raw)
	}
	digits := raw.String()
	if p.precision == 0 {
		return decimal.MustParse(sign + digits)
	}
	if len(digits) <= int(p.precision) {
		digits = strings.Repeat("0", int(p.precision)-len(digits)+1) + digits
	}
	point := len(digits) - int(p.precision)
	return decimal.MustParse(sign + digits[:point] + "." + digits[point:])
}

func (p WeiPrice) Add(other WeiPrice) (WeiPrice, bool) {
	if p.precision != other.precision {
		return WeiPrice{}, false
	}
	raw := new(big.Int).Add(p.raw, other.raw)
	if raw.Cmp(maxSigned128) > 0 || raw.Cmp(new(big.Int).Neg(new(big.Int).Add(maxSigned128, big.NewInt(1)))) < 0 {
		return WeiPrice{}, false
	}
	return PriceFromRaw(raw, p.precision), true
}

func (p WeiPrice) Sub(other WeiPrice) (WeiPrice, bool) {
	if p.precision != other.precision {
		return WeiPrice{}, false
	}
	raw := new(big.Int).Sub(p.raw, other.raw)
	if raw.Cmp(maxSigned128) > 0 || raw.Cmp(new(big.Int).Neg(new(big.Int).Add(maxSigned128, big.NewInt(1)))) < 0 {
		return WeiPrice{}, false
	}
	return PriceFromRaw(raw, p.precision), true
}

func (p WeiPrice) Cmp(other WeiPrice) int {
	if p.precision == other.precision {
		return p.raw.Cmp(other.raw)
	}
	return p.Decimal().Cmp(other.Decimal())
}

func (p WeiPrice) Equal(other WeiPrice) bool { return p.Cmp(other) == 0 }
