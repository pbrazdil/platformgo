package instrument

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

const (
	BetfairTickSchemeName       = "BETFAIR"
	Topix100TickSchemeName      = "TOPIX100"
	Crypto001TickSchemeName     = "CRYPTO_0_01"
	Forex3DecimalTickSchemeName = "FOREX_3DECIMAL"
	Forex5DecimalTickSchemeName = "FOREX_5DECIMAL"
	FixedTickSchemeName         = "FIXED"
	FixedPrecisionPrefix        = "FIXED_PRECISION_"

	priceMaximum = 17014118346046.0
	priceMinimum = -priceMaximum
)

// TickSchemeRule navigates the valid prices of a tick scheme.
type TickSchemeRule interface {
	NextBidPrice(value float64, n int32, precision uint8) (decimal.Price, bool)
	NextAskPrice(value float64, n int32, precision uint8) (decimal.Price, bool)
	fmt.Stringer
}

// TickSchemeError is a typed construction or lookup failure.
type TickSchemeError struct {
	Kind         string
	Index        int
	Tick         float64
	Start        float64
	Stop         float64
	Step         float64
	Range        float64
	PreviousStop float64
	Value        float64
	Name         string
	Source       error
}

func (e *TickSchemeError) Error() string {
	switch e.Kind {
	case "tick_not_finite":
		return "tick must be finite"
	case "tick_not_positive":
		return "tick must be positive"
	case "empty_tiers":
		return "tiers must not be empty"
	case "tier_values_nan":
		return fmt.Sprintf("tier %d: values must not be NaN", e.Index)
	case "tier_start_not_less_than_stop":
		return fmt.Sprintf("tier %d: start (%v) must be less than stop (%v)", e.Index, e.Start, e.Stop)
	case "tier_step_not_positive":
		return fmt.Sprintf("tier %d: step (%v) must be positive", e.Index, e.Step)
	case "tier_step_not_less_than_range":
		return fmt.Sprintf("tier %d: step (%v) must be less than range (%v - %v = %v)", e.Index, e.Step, e.Stop, e.Start, e.Range)
	case "tier_overlaps_previous":
		return fmt.Sprintf("tier %d: start (%v) overlaps previous tier stop (%v)", e.Index, e.Start, e.PreviousStop)
	case "tier_start_outside_price_range":
		return fmt.Sprintf("tier %d: start (%v) outside Price range", e.Index, e.Start)
	case "tier_stop_outside_price_range":
		return fmt.Sprintf("tier %d: stop (%v) outside Price range", e.Index, e.Stop)
	case "invalid_precision":
		return e.Source.Error()
	case "empty_tick_expansion":
		return "tier expansion produced no ticks"
	case "expanded_tick_outside_price_range":
		return fmt.Sprintf("expanded tick value %v outside Price range", e.Value)
	case "unknown_name":
		return "unknown tick scheme " + e.Name
	default:
		return "tick scheme error"
	}
}

func (e *TickSchemeError) Unwrap() error { return e.Source }

// FixedTickScheme uses one tick size at every price.
type FixedTickScheme struct {
	tick float64
}

func NewFixedTickScheme(tick float64) (FixedTickScheme, error) {
	if math.IsNaN(tick) || math.IsInf(tick, 0) {
		return FixedTickScheme{}, &TickSchemeError{Kind: "tick_not_finite", Tick: tick}
	}
	if tick <= 0 {
		return FixedTickScheme{}, &TickSchemeError{Kind: "tick_not_positive", Tick: tick}
	}
	return FixedTickScheme{tick: tick}, nil
}

func (FixedTickScheme) String() string { return FixedTickSchemeName }

func (s FixedTickScheme) NextBidPrice(value float64, n int32, precision uint8) (decimal.Price, bool) {
	return s.next(value, n, precision, false)
}

func (s FixedTickScheme) NextAskPrice(value float64, n int32, precision uint8) (decimal.Price, bool) {
	return s.next(value, n, precision, true)
}

func (s FixedTickScheme) next(value float64, n int32, precision uint8, ask bool) (decimal.Price, bool) {
	if n < 0 || precision > decimal.MaxPrecision || !finitePrice(value) {
		return decimal.Price{}, false
	}
	tick := floatRat(s.tick)
	tick = quantizeRat(tick, precision)
	if tick.Sign() <= 0 {
		return decimal.Price{}, false
	}
	valueRat := floatRat(value)
	quotient := floorRat(new(big.Rat).Quo(valueRat, tick))
	if ask && new(big.Rat).Mul(new(big.Rat).SetInt(quotient), tick).Cmp(valueRat) < 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if ask {
		quotient.Add(quotient, big.NewInt(int64(n)))
	} else {
		quotient.Sub(quotient, big.NewInt(int64(n)))
	}
	result := new(big.Rat).Mul(new(big.Rat).SetInt(quotient), tick)
	if !ratInPriceRange(result) {
		return decimal.Price{}, false
	}
	price, err := decimal.NewPrice(ratString(result, precision), precision)
	return price, err == nil
}

// PriceTier is a half-open (start, stop, step) tick interval. An infinite stop
// expands at most maxTicksPerTier entries.
type PriceTier struct {
	Start float64
	Stop  float64
	Step  float64
}

// TieredTickScheme stores its expanded ticks for deterministic navigation.
type TieredTickScheme struct {
	ticks     []decimal.Price
	precision uint8
}

func NewTieredTickScheme(tiers []PriceTier, precision uint8, maxTicksPerTier int) (TieredTickScheme, error) {
	if len(tiers) == 0 {
		return TieredTickScheme{}, &TickSchemeError{Kind: "empty_tiers"}
	}
	for index, tier := range tiers {
		if math.IsNaN(tier.Start) || math.IsNaN(tier.Stop) || math.IsNaN(tier.Step) {
			return TieredTickScheme{}, &TickSchemeError{Kind: "tier_values_nan", Index: index, Start: tier.Start, Stop: tier.Stop, Step: tier.Step}
		}
		if tier.Start >= tier.Stop {
			return TieredTickScheme{}, &TickSchemeError{Kind: "tier_start_not_less_than_stop", Index: index, Start: tier.Start, Stop: tier.Stop}
		}
		if tier.Step <= 0 {
			return TieredTickScheme{}, &TickSchemeError{Kind: "tier_step_not_positive", Index: index, Step: tier.Step}
		}
		if !math.IsInf(tier.Stop, 0) && tier.Step >= tier.Stop-tier.Start {
			return TieredTickScheme{}, &TickSchemeError{Kind: "tier_step_not_less_than_range", Index: index, Start: tier.Start, Stop: tier.Stop, Step: tier.Step, Range: tier.Stop - tier.Start}
		}
		if index > 0 && tier.Start < tiers[index-1].Stop {
			return TieredTickScheme{}, &TickSchemeError{Kind: "tier_overlaps_previous", Index: index, Start: tier.Start, PreviousStop: tiers[index-1].Stop}
		}
		if !finitePrice(tier.Start) {
			return TieredTickScheme{}, &TickSchemeError{Kind: "tier_start_outside_price_range", Index: index, Start: tier.Start}
		}
		if !math.IsInf(tier.Stop, 0) && !finitePrice(tier.Stop) {
			return TieredTickScheme{}, &TickSchemeError{Kind: "tier_stop_outside_price_range", Index: index, Stop: tier.Stop}
		}
	}
	if _, err := decimal.ZeroPrice(precision); err != nil {
		return TieredTickScheme{}, &TickSchemeError{Kind: "invalid_precision", Source: err}
	}

	ticks := make([]decimal.Price, 0)
	for _, tier := range tiers {
		start, step := floatRat(tier.Start), floatRat(tier.Step)
		var stop *big.Rat
		if !math.IsInf(tier.Stop, 0) {
			stop = floatRat(tier.Stop)
		}
		for i := 0; i < maxTicksPerTier; i++ {
			value := new(big.Rat).Add(start, new(big.Rat).Mul(new(big.Rat).SetInt64(int64(i)), step))
			if stop != nil && value.Cmp(stop) >= 0 {
				break
			}
			if !ratInPriceRange(value) {
				invalid, _ := strconv.ParseFloat(value.FloatString(0), 64)
				return TieredTickScheme{}, &TickSchemeError{Kind: "expanded_tick_outside_price_range", Value: invalid}
			}
			price, err := decimal.NewPrice(ratString(value, precision), precision)
			if err != nil {
				invalid, _ := strconv.ParseFloat(value.FloatString(int(precision)), 64)
				return TieredTickScheme{}, &TickSchemeError{Kind: "expanded_tick_outside_price_range", Value: invalid}
			}
			if len(ticks) == 0 || !ticks[len(ticks)-1].Equal(price) {
				ticks = append(ticks, price)
			}
		}
	}
	if len(ticks) == 0 {
		return TieredTickScheme{}, &TickSchemeError{Kind: "empty_tick_expansion"}
	}
	return TieredTickScheme{ticks: ticks, precision: precision}, nil
}

func (TieredTickScheme) String() string            { return "TIERED" }
func (s TieredTickScheme) Precision() uint8        { return s.precision }
func (s TieredTickScheme) TickCount() int          { return len(s.ticks) }
func (s TieredTickScheme) MinPrice() decimal.Price { return s.ticks[0] }
func (s TieredTickScheme) MaxPrice() decimal.Price { return s.ticks[len(s.ticks)-1] }
func (s TieredTickScheme) Ticks() []decimal.Price {
	return append([]decimal.Price(nil), s.ticks...)
}

func (s TieredTickScheme) NextBidPrice(value float64, n int32, _ uint8) (decimal.Price, bool) {
	if n < 0 || math.IsNaN(value) || math.IsInf(value, -1) {
		return decimal.Price{}, false
	}
	if math.IsInf(value, 1) {
		index := len(s.ticks) - 1 - int(n)
		if index < 0 {
			return decimal.Price{}, false
		}
		return s.ticks[index], true
	}
	valueDecimal := decimal.MustParse(strconv.FormatFloat(value, 'g', -1, 64))
	index := sort.Search(len(s.ticks), func(i int) bool {
		return s.ticks[i].Decimal().Cmp(valueDecimal) >= 0
	})
	if index < len(s.ticks) && s.ticks[index].Decimal().Cmp(valueDecimal) == 0 {
		index -= int(n)
	} else {
		index = index - 1 - int(n)
	}
	if index < 0 || index >= len(s.ticks) {
		return decimal.Price{}, false
	}
	return s.ticks[index], true
}

func (s TieredTickScheme) NextAskPrice(value float64, n int32, _ uint8) (decimal.Price, bool) {
	if n < 0 || math.IsNaN(value) || math.IsInf(value, 1) {
		return decimal.Price{}, false
	}
	if math.IsInf(value, -1) {
		if int(n) >= len(s.ticks) {
			return decimal.Price{}, false
		}
		return s.ticks[n], true
	}
	valueDecimal := decimal.MustParse(strconv.FormatFloat(value, 'g', -1, 64))
	index := sort.Search(len(s.ticks), func(i int) bool {
		return s.ticks[i].Decimal().Cmp(valueDecimal) >= 0
	}) + int(n)
	if index < 0 || index >= len(s.ticks) {
		return decimal.Price{}, false
	}
	return s.ticks[index], true
}

var (
	betfairTiers = []PriceTier{
		{1.01, 2, .01}, {2, 3, .02}, {3, 4, .05}, {4, 6, .1}, {6, 10, .2},
		{10, 20, .5}, {20, 30, 1}, {30, 50, 2}, {50, 100, 5}, {100, 1010, 10},
	}
	topix100Tiers = []PriceTier{
		{.1, 1000, .1}, {1000, 3000, .5}, {3000, 10000, 1}, {10000, 30000, 5},
		{30000, 100000, 10}, {100000, 300000, 50}, {300000, 1000000, 100},
		{1000000, 3000000, 500}, {3000000, 10000000, 1000},
		{10000000, 30000000, 5000}, {30000000, math.Inf(1), 10000},
	}
	topix100Scheme = mustTieredTickScheme(topix100Tiers, 4, 10000)
	betfairScheme  = mustTieredTickScheme(betfairTiers, 2, 100)
)

func Topix100TickScheme() TieredTickScheme {
	return topix100Scheme
}

func BetfairTickScheme() TieredTickScheme {
	return betfairScheme
}

func mustTieredTickScheme(tiers []PriceTier, precision uint8, maxTicksPerTier int) TieredTickScheme {
	scheme, err := NewTieredTickScheme(tiers, precision, maxTicksPerTier)
	if err != nil {
		panic(err)
	}
	return scheme
}

type namedTickScheme struct {
	name string
	rule TickSchemeRule
}

func (s namedTickScheme) String() string { return s.name }
func (s namedTickScheme) NextBidPrice(value float64, n int32, precision uint8) (decimal.Price, bool) {
	return s.rule.NextBidPrice(value, n, precision)
}
func (s namedTickScheme) NextAskPrice(value float64, n int32, precision uint8) (decimal.Price, bool) {
	return s.rule.NextAskPrice(value, n, precision)
}

func ParseTickScheme(name string) (TickSchemeRule, error) {
	trimmed := strings.TrimSpace(name)
	upper := strings.ToUpper(trimmed)
	switch upper {
	case FixedTickSchemeName:
		s, _ := NewFixedTickScheme(1)
		return s, nil
	case Forex3DecimalTickSchemeName:
		s, _ := NewFixedTickScheme(.001)
		return s, nil
	case Forex5DecimalTickSchemeName:
		s, _ := NewFixedTickScheme(.00001)
		return s, nil
	case Topix100TickSchemeName:
		return Topix100TickScheme(), nil
	case BetfairTickSchemeName:
		return namedTickScheme{BetfairTickSchemeName, BetfairTickScheme()}, nil
	case Crypto001TickSchemeName:
		s, _ := NewFixedTickScheme(.01)
		return namedTickScheme{Crypto001TickSchemeName, s}, nil
	}
	if strings.HasPrefix(upper, FixedPrecisionPrefix) {
		precision, err := strconv.ParseUint(strings.TrimPrefix(upper, FixedPrecisionPrefix), 10, 8)
		if err == nil && precision <= uint64(decimal.MaxPrecision) {
			s, _ := NewFixedTickScheme(math.Pow10(-int(precision)))
			return s, nil
		}
	}
	return nil, &TickSchemeError{Kind: "unknown_name", Name: name}
}

func TickSchemeRuleFromName(name string) (TickSchemeRule, bool) {
	scheme, err := ParseTickScheme(name)
	return scheme, err == nil
}

func finitePrice(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= priceMinimum && value <= priceMaximum
}

func floatRat(value float64) *big.Rat {
	rat, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'g', -1, 64))
	if !ok {
		panic("unrepresentable finite float")
	}
	return rat
}

func floorRat(value *big.Rat) *big.Int {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value.Num(), value.Denom(), remainder)
	if remainder.Sign() < 0 {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient
}

func quantizeRat(value *big.Rat, precision uint8) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	scaled := new(big.Rat).Mul(value, new(big.Rat).SetInt(scale))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(scaled.Num(), scaled.Denom(), remainder)
	twice := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
	if comparison := twice.Cmp(scaled.Denom()); comparison > 0 || comparison == 0 && quotient.Bit(0) == 1 {
		if scaled.Sign() >= 0 {
			quotient.Add(quotient, big.NewInt(1))
		} else {
			quotient.Sub(quotient, big.NewInt(1))
		}
	}
	return new(big.Rat).SetFrac(quotient, scale)
}

func ratString(value *big.Rat, precision uint8) string {
	return value.FloatString(int(precision))
}

func ratInPriceRange(value *big.Rat) bool {
	return value.Cmp(floatRat(priceMinimum)) >= 0 && value.Cmp(floatRat(priceMaximum)) <= 0
}
