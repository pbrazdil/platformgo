// Package market defines deterministic, infrastructure-free market data values.
package market

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/big"
	"strconv"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

const fixedSizeBinary = "FixedSizeBinary(16)"

// InstrumentID and TradeID are intentionally small value types. They keep
// market data strongly typed without coupling this model package to an ID registry.
type InstrumentID string
type TradeID string

type UnixNanos uint64

// PriceType identifies the market price or size selected from a data event.
type PriceType uint8

const (
	PriceTypeBid PriceType = iota
	PriceTypeAsk
	PriceTypeMid
	PriceTypeLast
)

func (p PriceType) String() string {
	switch p {
	case PriceTypeBid:
		return "BID"
	case PriceTypeAsk:
		return "ASK"
	case PriceTypeMid:
		return "MID"
	case PriceTypeLast:
		return "LAST"
	default:
		return fmt.Sprintf("PriceType(%d)", p)
	}
}

// AggressorSide is the side which initiated a trade.
type AggressorSide uint8

const (
	NoAggressor AggressorSide = iota
	Buyer
	Seller
)

func (a AggressorSide) String() string {
	switch a {
	case NoAggressor:
		return "NO_AGGRESSOR"
	case Buyer:
		return "BUYER"
	case Seller:
		return "SELLER"
	default:
		return fmt.Sprintf("AggressorSide(%d)", a)
	}
}

func (a AggressorSide) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

func (a *AggressorSide) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	switch text {
	case "NO_AGGRESSOR":
		*a = NoAggressor
	case "BUYER":
		*a = Buyer
	case "SELLER":
		*a = Seller
	default:
		return fmt.Errorf("invalid aggressor side %q", text)
	}
	return nil
}

// Field describes one ordered Arrow-compatible serialization field.
type Field struct {
	Name string
	Type string
}

func metadata(instrumentID InstrumentID, pricePrecision, sizePrecision uint8) map[string]string {
	return map[string]string{
		"instrument_id":   string(instrumentID),
		"price_precision": strconv.Itoa(int(pricePrecision)),
		"size_precision":  strconv.Itoa(int(sizePrecision)),
	}
}

func midpointPrice(left, right decimal.Price) decimal.Price {
	return decimal.MustPrice(midpoint(left.String(), right.String(), left.Precision()))
}

func midpointQuantity(left, right decimal.Quantity) decimal.Quantity {
	return decimal.MustQuantity(midpoint(left.String(), right.String(), left.Precision()))
}

// midpoint mirrors the pinned fixed-point raw midpoint: it increases displayed
// precision by one when possible and floors an odd raw sum at max precision.
func midpoint(left, right string, precision uint8) string {
	leftRaw := decimalCoefficient(left, precision)
	rightRaw := decimalCoefficient(right, precision)
	sum := new(big.Int).Add(leftRaw, rightRaw)
	resultPrecision := precision
	if resultPrecision < decimal.MaxPrecision {
		resultPrecision++
		sum.Mul(sum, big.NewInt(10))
	}
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(sum, big.NewInt(2), remainder)
	if remainder.Sign() < 0 {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return formatCoefficient(quotient, resultPrecision)
}

func decimalCoefficient(text string, precision uint8) *big.Int {
	sign := 1
	if strings.HasPrefix(text, "-") {
		sign = -1
		text = strings.TrimPrefix(text, "-")
	}
	parts := strings.SplitN(text, ".", 2)
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	fraction += strings.Repeat("0", int(precision)-len(fraction))
	value, ok := new(big.Int).SetString(parts[0]+fraction, 10)
	if !ok {
		panic("invalid exact decimal")
	}
	if sign < 0 {
		value.Neg(value)
	}
	return value
}

func formatCoefficient(value *big.Int, precision uint8) string {
	sign := ""
	if value.Sign() < 0 {
		sign = "-"
		value = new(big.Int).Abs(value)
	}
	digits := value.String()
	if precision == 0 {
		return sign + digits
	}
	if len(digits) <= int(precision) {
		digits = strings.Repeat("0", int(precision)-len(digits)+1) + digits
	}
	split := len(digits) - int(precision)
	return sign + digits[:split] + "." + digits[split:]
}

func hashStrings(values ...string) uint64 {
	hash := fnv.New64a()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hash.Sum64()
}
