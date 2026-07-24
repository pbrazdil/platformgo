package defi

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// WeiQuantity stores unsigned quantities at their native decimal scale. Wei
// quantities use precision 18 and therefore preserve every integer wei.
type WeiQuantity struct {
	raw       *big.Int
	precision uint8
}

var maxUnsigned128 = func() *big.Int {
	value := new(big.Int).Lsh(big.NewInt(1), 128)
	return value.Sub(value, big.NewInt(1))
}()

func QuantityFromWei(raw *big.Int) (WeiQuantity, error) {
	if raw == nil {
		raw = new(big.Int)
	}
	if raw.Sign() < 0 || raw.Cmp(maxUnsigned128) > 0 {
		return WeiQuantity{}, errors.New("raw wei value exceeds unsigned 128-bit range")
	}
	return WeiQuantity{raw: new(big.Int).Set(raw), precision: 18}, nil
}

func MustQuantityFromWei(raw *big.Int) WeiQuantity {
	quantity, err := QuantityFromWei(raw)
	if err != nil {
		panic(err)
	}
	return quantity
}

func QuantityFromRaw(raw *big.Int, precision uint8) WeiQuantity {
	if raw == nil {
		raw = new(big.Int)
	}
	if raw.Sign() < 0 || raw.Cmp(maxUnsigned128) > 0 {
		panic("quantity raw value exceeds unsigned 128-bit range")
	}
	return WeiQuantity{raw: new(big.Int).Set(raw), precision: precision}
}

func StandardDeFiQuantity(value string, precision uint8) (WeiQuantity, error) {
	if precision > decimal.MaxPrecision {
		return WeiQuantity{}, fmt.Errorf("quantity precision %d exceeds maximum %d", precision, decimal.MaxPrecision)
	}
	parsed, err := decimal.Parse(value)
	if err != nil {
		return WeiQuantity{}, err
	}
	if parsed.Sign() < 0 {
		return WeiQuantity{}, errors.New("quantity cannot be negative")
	}
	quantized := parsed.Quantize(precision, decimal.RoundHalfEven)
	rawText := strings.ReplaceAll(quantized.String(), ".", "")
	raw, ok := new(big.Int).SetString(rawText, 10)
	if !ok {
		return WeiQuantity{}, errors.New("invalid quantity")
	}
	if raw.Cmp(maxUnsigned128) > 0 {
		return WeiQuantity{}, errors.New("quantity raw value exceeds unsigned 128-bit range")
	}
	return WeiQuantity{raw: raw, precision: precision}, nil
}

func (q WeiQuantity) Precision() uint8 { return q.precision }

func (q WeiQuantity) Wei() *big.Int {
	if q.precision != 18 {
		panic(fmt.Sprintf("Failed to convert quantity with precision %d to wei (requires precision 18)", q.precision))
	}
	return q.rawCopy()
}

func (q WeiQuantity) Decimal() decimal.Decimal {
	raw := q.rawCopy()
	digits := raw.String()
	if q.precision == 0 {
		return decimal.MustParse(digits)
	}
	if len(digits) <= int(q.precision) {
		digits = strings.Repeat("0", int(q.precision)-len(digits)+1) + digits
	}
	point := len(digits) - int(q.precision)
	return decimal.MustParse(digits[:point] + "." + digits[point:])
}

func (q WeiQuantity) Add(other WeiQuantity) (WeiQuantity, bool) {
	if q.precision != other.precision {
		return WeiQuantity{}, false
	}
	raw := new(big.Int).Add(q.rawCopy(), other.rawCopy())
	if raw.Cmp(maxUnsigned128) > 0 {
		return WeiQuantity{}, false
	}
	return QuantityFromRaw(raw, q.precision), true
}

func (q WeiQuantity) Sub(other WeiQuantity) (WeiQuantity, bool) {
	if q.precision != other.precision || q.rawCopy().Cmp(other.rawCopy()) < 0 {
		return WeiQuantity{}, false
	}
	return QuantityFromRaw(new(big.Int).Sub(q.rawCopy(), other.rawCopy()), q.precision), true
}

func (q WeiQuantity) Cmp(other WeiQuantity) int {
	if q.precision == other.precision {
		return q.rawCopy().Cmp(other.rawCopy())
	}
	return q.Decimal().Cmp(other.Decimal())
}

func (q WeiQuantity) Equal(other WeiQuantity) bool { return q.Cmp(other) == 0 }

func (q WeiQuantity) rawCopy() *big.Int {
	if q.raw == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(q.raw)
}
