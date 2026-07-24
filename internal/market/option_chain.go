package market

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

const DefaultDeltaFallbackStrikes = 5

type StrikeRangeKind uint8

const (
	StrikeRangeFixed StrikeRangeKind = iota
	StrikeRangeATMRelative
	StrikeRangeATMPercent
	StrikeRangeDelta
)

// StrikeRange is a typed subscription selector. Strike prices remain exact;
// percentage and delta parameters remain floating point because they are
// dimensionless analytical inputs.
type StrikeRange struct {
	Kind         StrikeRangeKind
	Fixed        []decimal.Price
	StrikesAbove uint
	StrikesBelow uint
	Percent      float64
	Target       float64
	Tolerance    float64
}

func FixedStrikeRange(strikes ...decimal.Price) StrikeRange {
	return StrikeRange{Kind: StrikeRangeFixed, Fixed: append([]decimal.Price(nil), strikes...)}
}

func ATMRelativeStrikeRange(strikesAbove, strikesBelow uint) StrikeRange {
	return StrikeRange{
		Kind: StrikeRangeATMRelative, StrikesAbove: strikesAbove, StrikesBelow: strikesBelow,
	}
}

func ATMPercentStrikeRange(percent float64) StrikeRange {
	return StrikeRange{Kind: StrikeRangeATMPercent, Percent: percent}
}

func DeltaStrikeRange(target, tolerance float64) StrikeRange {
	return StrikeRange{Kind: StrikeRangeDelta, Target: target, Tolerance: tolerance}
}

func (r StrikeRange) Equal(other StrikeRange) bool {
	if r.Kind != other.Kind || r.StrikesAbove != other.StrikesAbove ||
		r.StrikesBelow != other.StrikesBelow || r.Percent != other.Percent ||
		r.Target != other.Target || r.Tolerance != other.Tolerance ||
		len(r.Fixed) != len(other.Fixed) {
		return false
	}
	for i := range r.Fixed {
		if !r.Fixed[i].Equal(other.Fixed[i]) {
			return false
		}
	}
	return true
}

func (r StrikeRange) Resolve(atmPrice *decimal.Price, allStrikes []decimal.Price) []decimal.Price {
	switch r.Kind {
	case StrikeRangeFixed:
		if len(allStrikes) == 0 {
			return append([]decimal.Price(nil), r.Fixed...)
		}
		result := make([]decimal.Price, 0, len(r.Fixed))
		for _, fixed := range r.Fixed {
			if containsStrike(allStrikes, fixed) {
				result = append(result, fixed)
			}
		}
		return result
	case StrikeRangeATMRelative:
		if atmPrice == nil || len(allStrikes) == 0 {
			return nil
		}
		atmIndex := closestStrikeIndex(*atmPrice, allStrikes)
		below := saturatingUintToInt(r.StrikesBelow)
		above := saturatingUintToInt(r.StrikesAbove)
		start := max(0, atmIndex-below)
		end := min(len(allStrikes), saturatingAddInt(atmIndex, saturatingAddInt(above, 1)))
		return append([]decimal.Price(nil), allStrikes[start:end]...)
	case StrikeRangeATMPercent:
		if atmPrice == nil {
			return nil
		}
		if atmPrice.IsZero() {
			return append([]decimal.Price(nil), allStrikes...)
		}
		one := decimal.MustParse("1")
		percent := decimalFromFloat64(r.Percent)
		lower := atmPrice.Decimal().Mul(one.Sub(percent))
		upper := atmPrice.Decimal().Mul(one.Add(percent))
		result := make([]decimal.Price, 0, len(allStrikes))
		for _, strike := range allStrikes {
			value := strike.Decimal()
			if value.Cmp(lower) >= 0 && value.Cmp(upper) <= 0 {
				result = append(result, strike)
			}
		}
		return result
	case StrikeRangeDelta:
		return ATMRelativeStrikeRange(
			DefaultDeltaFallbackStrikes,
			DefaultDeltaFallbackStrikes,
		).Resolve(atmPrice, allStrikes)
	default:
		return nil
	}
}

type GreeksConvention uint8

const (
	GreeksConventionBlackScholes GreeksConvention = iota
	GreeksConventionPriceAdjusted
)

func (c GreeksConvention) String() string {
	switch c {
	case GreeksConventionBlackScholes:
		return "BLACK_SCHOLES"
	case GreeksConventionPriceAdjusted:
		return "PRICE_ADJUSTED"
	default:
		return fmt.Sprintf("GreeksConvention(%d)", c)
	}
}

// OptionGreeks carries exchange-provided sensitivities. IVs and sensitivities
// are dimensionless floats; underlying price and open interest use exact
// decimal market types.
type OptionGreeks struct {
	InstrumentID    InstrumentID      `json:"instrument_id"`
	Convention      GreeksConvention  `json:"convention"`
	Greeks          OptionGreekValues `json:"greeks"`
	MarkIV          *float64          `json:"mark_iv"`
	BidIV           *float64          `json:"bid_iv"`
	AskIV           *float64          `json:"ask_iv"`
	UnderlyingPrice *decimal.Price    `json:"underlying_price"`
	OpenInterest    *decimal.Quantity `json:"open_interest"`
	TsEvent         UnixNanos         `json:"ts_event"`
	TsInit          UnixNanos         `json:"ts_init"`
}

func DefaultOptionGreeks() OptionGreeks {
	return OptionGreeks{
		InstrumentID: "NULL.NULL",
		Convention:   GreeksConventionBlackScholes,
	}
}

func (g OptionGreeks) Equal(other OptionGreeks) bool {
	return g.InstrumentID == other.InstrumentID &&
		g.Convention == other.Convention &&
		g.Greeks == other.Greeks &&
		optionalFloatEqual(g.MarkIV, other.MarkIV) &&
		optionalFloatEqual(g.BidIV, other.BidIV) &&
		optionalFloatEqual(g.AskIV, other.AskIV) &&
		optionalPriceEqual(g.UnderlyingPrice, other.UnderlyingPrice) &&
		optionalQuantityEqual(g.OpenInterest, other.OpenInterest) &&
		g.TsEvent == other.TsEvent &&
		g.TsInit == other.TsInit
}

func (g OptionGreeks) String() string {
	markIV := "None"
	if g.MarkIV != nil {
		markIV = fmt.Sprintf("Some(%v)", *g.MarkIV)
	}
	return fmt.Sprintf(
		"OptionGreeks(%s, %s, delta=%.4f, gamma=%.4f, vega=%.4f, theta=%.4f, mark_iv=%s)",
		g.InstrumentID,
		g.Convention,
		g.Greeks.Delta,
		g.Greeks.Gamma,
		g.Greeks.Vega,
		g.Greeks.Theta,
		markIV,
	)
}

func (g OptionGreeks) MarshalJSON() ([]byte, error) {
	type wire OptionGreeks
	return json.Marshal(struct {
		Type string `json:"type"`
		wire
	}{Type: "OptionGreeks", wire: wire(g)})
}

func (g *OptionGreeks) UnmarshalJSON(data []byte) error {
	type wire OptionGreeks
	var decoded struct {
		Type string `json:"type"`
		wire
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Type != "" && decoded.Type != "OptionGreeks" {
		return fmt.Errorf("invalid market data type %q", decoded.Type)
	}
	*g = OptionGreeks(decoded.wire)
	return nil
}

type OptionStrikeData struct {
	Quote  QuoteTick
	Greeks *OptionGreeks
}

// OptionStrikeMap is keyed by the exact textual strike representation.
type OptionStrikeMap map[string]OptionStrikeData

func (m OptionStrikeMap) Set(strike decimal.Price, data OptionStrikeData) {
	m[strikeKey(strike)] = data
}

func (m OptionStrikeMap) Get(strike decimal.Price) (OptionStrikeData, bool) {
	value, ok := m[strikeKey(strike)]
	return value, ok
}

type OptionChainSlice struct {
	SeriesID  ids.OptionSeriesID
	ATMStrike *decimal.Price
	Calls     OptionStrikeMap
	Puts      OptionStrikeMap
	TsEvent   UnixNanos
	TsInit    UnixNanos
}

func NewOptionChainSlice(seriesID ids.OptionSeriesID) OptionChainSlice {
	return OptionChainSlice{
		SeriesID: seriesID,
		Calls:    make(OptionStrikeMap),
		Puts:     make(OptionStrikeMap),
	}
}

func (s OptionChainSlice) CallCount() int { return len(s.Calls) }
func (s OptionChainSlice) PutCount() int  { return len(s.Puts) }
func (s OptionChainSlice) IsEmpty() bool  { return len(s.Calls) == 0 && len(s.Puts) == 0 }

func (s OptionChainSlice) GetCall(strike decimal.Price) (OptionStrikeData, bool) {
	return s.Calls.Get(strike)
}

func (s OptionChainSlice) GetPut(strike decimal.Price) (OptionStrikeData, bool) {
	return s.Puts.Get(strike)
}

func (s OptionChainSlice) GetCallQuote(strike decimal.Price) (*QuoteTick, bool) {
	data, ok := s.GetCall(strike)
	if !ok {
		return nil, false
	}
	return &data.Quote, true
}

func (s OptionChainSlice) GetCallGreeks(strike decimal.Price) (*OptionGreeks, bool) {
	data, ok := s.GetCall(strike)
	return data.Greeks, ok && data.Greeks != nil
}

func (s OptionChainSlice) GetPutQuote(strike decimal.Price) (*QuoteTick, bool) {
	data, ok := s.GetPut(strike)
	if !ok {
		return nil, false
	}
	return &data.Quote, true
}

func (s OptionChainSlice) GetPutGreeks(strike decimal.Price) (*OptionGreeks, bool) {
	data, ok := s.GetPut(strike)
	return data.Greeks, ok && data.Greeks != nil
}

func (s OptionChainSlice) Strikes() []decimal.Price {
	byKey := make(map[string]decimal.Price, len(s.Calls)+len(s.Puts))
	for key := range s.Calls {
		byKey[key] = decimal.MustPrice(key)
	}
	for key := range s.Puts {
		byKey[key] = decimal.MustPrice(key)
	}
	result := make([]decimal.Price, 0, len(byKey))
	for _, strike := range byKey {
		result = append(result, strike)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Decimal().Cmp(result[j].Decimal()) < 0
	})
	return result
}

func (s OptionChainSlice) StrikeCount() int { return len(s.Strikes()) }

func (s OptionChainSlice) String() string {
	atm := "None"
	if s.ATMStrike != nil {
		atm = fmt.Sprintf("Some(%s)", *s.ATMStrike)
	}
	return fmt.Sprintf(
		"OptionChainSlice(%s, atm=%s, calls=%d, puts=%d)",
		s.SeriesID,
		atm,
		len(s.Calls),
		len(s.Puts),
	)
}

func strikeKey(strike decimal.Price) string {
	return strike.Decimal().Normalize().String()
}

func containsStrike(strikes []decimal.Price, target decimal.Price) bool {
	for _, strike := range strikes {
		if strike.Equal(target) {
			return true
		}
	}
	return false
}

func closestStrikeIndex(atm decimal.Price, strikes []decimal.Price) int {
	bestIndex := 0
	bestDifference := absoluteDecimal(strikes[0].Decimal().Sub(atm.Decimal()))
	for i := 1; i < len(strikes); i++ {
		difference := absoluteDecimal(strikes[i].Decimal().Sub(atm.Decimal()))
		if difference.Cmp(bestDifference) < 0 {
			bestIndex = i
			bestDifference = difference
		}
	}
	return bestIndex
}

func absoluteDecimal(value decimal.Decimal) decimal.Decimal {
	if value.Sign() < 0 {
		return value.Neg()
	}
	return value
}

func saturatingUintToInt(value uint) int {
	if uint64(value) > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value)
}

func saturatingAddInt(left, right int) int {
	if right > 0 && left > math.MaxInt-right {
		return math.MaxInt
	}
	return left + right
}

func optionalFloatEqual(left, right *float64) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && *left == *right
}

func optionalPriceEqual(left, right *decimal.Price) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && left.Equal(*right)
}

func optionalQuantityEqual(left, right *decimal.Quantity) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && left.Equal(*right)
}
