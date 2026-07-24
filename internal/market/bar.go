package market

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// BarAggregation identifies how samples are grouped into a bar.
type BarAggregation uint8

const (
	BarAggregationMillisecond BarAggregation = iota
	BarAggregationSecond
	BarAggregationMinute
	BarAggregationHour
	BarAggregationDay
	BarAggregationWeek
	BarAggregationMonth
	BarAggregationYear
	BarAggregationTick
	BarAggregationTickImbalance
	BarAggregationTickRuns
	BarAggregationVolume
	BarAggregationVolumeImbalance
	BarAggregationVolumeRuns
	BarAggregationValue
	BarAggregationValueImbalance
	BarAggregationValueRuns
	BarAggregationRenko
)

var barAggregationNames = [...]string{
	"MILLISECOND", "SECOND", "MINUTE", "HOUR", "DAY", "WEEK", "MONTH", "YEAR",
	"TICK", "TICK_IMBALANCE", "TICK_RUNS", "VOLUME", "VOLUME_IMBALANCE",
	"VOLUME_RUNS", "VALUE", "VALUE_IMBALANCE", "VALUE_RUNS", "RENKO",
}

func (a BarAggregation) String() string {
	if int(a) >= len(barAggregationNames) {
		return fmt.Sprintf("BarAggregation(%d)", a)
	}
	return barAggregationNames[a]
}

func parseBarAggregation(text string) (BarAggregation, error) {
	for value, name := range barAggregationNames {
		if text == name {
			return BarAggregation(value), nil
		}
	}
	return 0, fmt.Errorf("invalid bar aggregation %q", text)
}

// AggregationSource identifies whether a bar was aggregated internally or externally.
type AggregationSource uint8

const (
	AggregationSourceInternal AggregationSource = iota
	AggregationSourceExternal
)

func (s AggregationSource) String() string {
	switch s {
	case AggregationSourceInternal:
		return "INTERNAL"
	case AggregationSourceExternal:
		return "EXTERNAL"
	default:
		return fmt.Sprintf("AggregationSource(%d)", s)
	}
}

func parseAggregationSource(text string) (AggregationSource, error) {
	switch text {
	case "INTERNAL":
		return AggregationSourceInternal, nil
	case "EXTERNAL":
		return AggregationSourceExternal, nil
	default:
		return 0, fmt.Errorf("invalid aggregation source %q", text)
	}
}

func parseBarPriceType(text string) (PriceType, error) {
	switch text {
	case "BID":
		return PriceTypeBid, nil
	case "ASK":
		return PriceTypeAsk, nil
	case "MID":
		return PriceTypeMid, nil
	case "LAST":
		return PriceTypeLast, nil
	default:
		return 0, fmt.Errorf("invalid price type %q", text)
	}
}

// BarSpecification describes the samples used to aggregate a bar.
type BarSpecification struct {
	Step        uint64         `json:"step"`
	Aggregation BarAggregation `json:"aggregation"`
	PriceType   PriceType      `json:"price_type"`
}

func NewBarSpecification(step uint64, aggregation BarAggregation, priceType PriceType) (BarSpecification, error) {
	if step == 0 {
		return BarSpecification{}, errors.New("Invalid step: 0 (must be non-zero)")
	}
	if subunits, allowEqual, periodic := periodicSubunits(aggregation); periodic {
		if subunits%step != 0 {
			return BarSpecification{}, fmt.Errorf(
				"Invalid step in bar_type.spec.step: %d for aggregation=%s. step must evenly divide %d (so it is periodic).",
				step, aggregation, subunits,
			)
		}
		if !allowEqual && step == subunits {
			return BarSpecification{}, fmt.Errorf(
				"Invalid step in bar_type.spec.step: %d for aggregation=%s. step must not be %d. Use higher aggregation unit instead.",
				step, aggregation, subunits,
			)
		}
	}
	return BarSpecification{Step: step, Aggregation: aggregation, PriceType: priceType}, nil
}

func MustBarSpecification(step uint64, aggregation BarAggregation, priceType PriceType) BarSpecification {
	spec, err := NewBarSpecification(step, aggregation, priceType)
	if err != nil {
		panic(err)
	}
	return spec
}

func periodicSubunits(aggregation BarAggregation) (uint64, bool, bool) {
	switch aggregation {
	case BarAggregationMillisecond:
		return 1000, false, true
	case BarAggregationSecond, BarAggregationMinute:
		return 60, false, true
	case BarAggregationHour:
		return 24, false, true
	case BarAggregationMonth:
		return 12, true, true
	default:
		return 0, false, false
	}
}

func (s BarSpecification) String() string {
	return fmt.Sprintf("%d-%s-%s", s.Step, s.Aggregation, s.PriceType)
}

func (s BarSpecification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Step        uint64 `json:"step"`
		Aggregation string `json:"aggregation"`
		PriceType   string `json:"price_type"`
	}{s.Step, s.Aggregation.String(), s.PriceType.String()})
}

func (s *BarSpecification) UnmarshalJSON(data []byte) error {
	var wire struct {
		Step        uint64 `json:"step"`
		Aggregation string `json:"aggregation"`
		PriceType   string `json:"price_type"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	aggregation, err := parseBarAggregation(wire.Aggregation)
	if err != nil {
		return err
	}
	priceType, err := parseBarPriceType(wire.PriceType)
	if err != nil {
		return err
	}
	*s, err = NewBarSpecification(wire.Step, aggregation, priceType)
	return err
}

func (s BarSpecification) IsTimeAggregated() bool {
	return s.Aggregation <= BarAggregationYear
}

func (s BarSpecification) IsThresholdAggregated() bool {
	switch s.Aggregation {
	case BarAggregationTick, BarAggregationTickImbalance, BarAggregationVolume,
		BarAggregationVolumeImbalance, BarAggregationValue, BarAggregationValueImbalance:
		return true
	default:
		return false
	}
}

func (s BarSpecification) IsInformationAggregated() bool {
	switch s.Aggregation {
	case BarAggregationTickRuns, BarAggregationVolumeRuns, BarAggregationValueRuns:
		return true
	default:
		return false
	}
}

// BarType combines an instrument, aggregation specification, and source.
type BarType struct {
	InstrumentID               InstrumentID
	Spec                       BarSpecification
	AggregationSource          AggregationSource
	Composite                  bool
	CompositeStep              uint64
	CompositeAggregation       BarAggregation
	CompositeAggregationSource AggregationSource
}

func NewBarType(instrumentID InstrumentID, spec BarSpecification, source AggregationSource) BarType {
	return BarType{InstrumentID: instrumentID, Spec: spec, AggregationSource: source}
}

func NewCompositeBarType(
	instrumentID InstrumentID,
	spec BarSpecification,
	source AggregationSource,
	compositeStep uint64,
	compositeAggregation BarAggregation,
	compositeSource AggregationSource,
) (BarType, error) {
	if _, err := NewBarSpecification(compositeStep, compositeAggregation, spec.PriceType); err != nil {
		return BarType{}, err
	}
	return BarType{
		InstrumentID: instrumentID, Spec: spec, AggregationSource: source, Composite: true,
		CompositeStep: compositeStep, CompositeAggregation: compositeAggregation,
		CompositeAggregationSource: compositeSource,
	}, nil
}

func MustCompositeBarType(
	instrumentID InstrumentID,
	spec BarSpecification,
	source AggregationSource,
	compositeStep uint64,
	compositeAggregation BarAggregation,
	compositeSource AggregationSource,
) BarType {
	barType, err := NewCompositeBarType(instrumentID, spec, source, compositeStep, compositeAggregation, compositeSource)
	if err != nil {
		panic(err)
	}
	return barType
}

func (b BarType) IsStandard() bool  { return !b.Composite }
func (b BarType) IsComposite() bool { return b.Composite }
func (b BarType) IsExternallyAggregated() bool {
	return b.AggregationSource == AggregationSourceExternal
}
func (b BarType) IsInternallyAggregated() bool {
	return b.AggregationSource == AggregationSourceInternal
}
func (b BarType) Standard() BarType {
	return NewBarType(b.InstrumentID, b.Spec, b.AggregationSource)
}
func (b BarType) CompositeType() BarType {
	if !b.Composite {
		return b
	}
	return NewBarType(
		b.InstrumentID,
		MustBarSpecification(b.CompositeStep, b.CompositeAggregation, b.Spec.PriceType),
		b.CompositeAggregationSource,
	)
}

type BarTypeIDSpecKey struct {
	InstrumentID InstrumentID
	Spec         BarSpecification
}

func (b BarType) IDSpecKey() BarTypeIDSpecKey {
	return BarTypeIDSpecKey{InstrumentID: b.InstrumentID, Spec: b.Spec}
}

func (b BarType) String() string {
	standard := fmt.Sprintf("%s-%s-%s", b.InstrumentID, b.Spec, b.AggregationSource)
	if !b.Composite {
		return standard
	}
	return fmt.Sprintf("%s@%d-%s-%s", standard, b.CompositeStep, b.CompositeAggregation, b.CompositeAggregationSource)
}

func (b BarType) Compare(other BarType) int {
	return strings.Compare(b.String(), other.String())
}

func (b BarType) MarshalJSON() ([]byte, error) { return json.Marshal(b.String()) }

func (b *BarType) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := ParseBarType(text)
	if err != nil {
		return err
	}
	*b = parsed
	return nil
}

type BarTypeParseError struct {
	Input    string
	Token    string
	Position int
}

func (e *BarTypeParseError) Error() string {
	return fmt.Sprintf("Error parsing `BarType` from '%s', invalid token: '%s' at position %d", e.Input, e.Token, e.Position)
}

func ParseBarType(input string) (BarType, error) {
	segments := strings.Split(input, "@")
	if len(segments) > 2 {
		return BarType{}, barTypeParseError(input, segments[2], 5)
	}
	standardTokens, ok := splitFromRight(segments[0], 5)
	if !ok {
		token := segments[0]
		if index := strings.LastIndex(token, "-"); index >= 0 {
			token = token[:index]
		}
		return BarType{}, barTypeParseError(input, token, 0)
	}
	instrumentID := InstrumentID(standardTokens[0])
	if !validBarInstrumentID(string(instrumentID)) {
		return BarType{}, barTypeParseError(input, standardTokens[0], 0)
	}
	step, err := strconv.ParseUint(standardTokens[1], 10, 64)
	if err != nil {
		return BarType{}, barTypeParseError(input, standardTokens[1], 1)
	}
	aggregation, err := parseBarAggregation(standardTokens[2])
	if err != nil {
		return BarType{}, barTypeParseError(input, standardTokens[2], 2)
	}
	priceType, err := parseBarPriceType(standardTokens[3])
	if err != nil {
		return BarType{}, barTypeParseError(input, standardTokens[3], 3)
	}
	source, err := parseAggregationSource(standardTokens[4])
	if err != nil {
		return BarType{}, barTypeParseError(input, standardTokens[4], 4)
	}
	spec, err := NewBarSpecification(step, aggregation, priceType)
	if err != nil {
		return BarType{}, barTypeParseError(input, standardTokens[1], 1)
	}
	standard := NewBarType(instrumentID, spec, source)
	if len(segments) == 1 {
		return standard, nil
	}
	compositeTokens, ok := splitFromRight(segments[1], 3)
	if !ok {
		return BarType{}, barTypeParseError(input, "", 5)
	}
	compositeStep, err := strconv.ParseUint(compositeTokens[0], 10, 64)
	if err != nil {
		return BarType{}, barTypeParseError(input, compositeTokens[0], 5)
	}
	compositeAggregation, err := parseBarAggregation(compositeTokens[1])
	if err != nil {
		return BarType{}, barTypeParseError(input, compositeTokens[1], 6)
	}
	compositeSource, err := parseAggregationSource(compositeTokens[2])
	if err != nil {
		return BarType{}, barTypeParseError(input, compositeTokens[2], 7)
	}
	if _, err := NewBarSpecification(compositeStep, compositeAggregation, priceType); err != nil {
		return BarType{}, barTypeParseError(input, compositeTokens[0], 5)
	}
	return NewCompositeBarType(instrumentID, spec, source, compositeStep, compositeAggregation, compositeSource)
}

func MustBarType(input string) BarType {
	barType, err := ParseBarType(input)
	if err != nil {
		panic(err)
	}
	return barType
}

func splitFromRight(input string, count int) ([]string, bool) {
	result := make([]string, count)
	remaining := input
	for index := count - 1; index > 0; index-- {
		position := strings.LastIndex(remaining, "-")
		if position < 0 {
			return nil, false
		}
		result[index] = remaining[position+1:]
		remaining = remaining[:position]
	}
	result[0] = remaining
	return result, true
}

func validBarInstrumentID(input string) bool {
	index := strings.LastIndex(input, ".")
	return index > 0 && index < len(input)-1
}

func barTypeParseError(input, token string, position int) error {
	return &BarTypeParseError{Input: input, Token: token, Position: position}
}

// GetBarInterval returns the fixed or proxy duration for a time bar.
func GetBarInterval(barType BarType) time.Duration {
	step := barType.Spec.Step
	if step > math.MaxInt64 {
		panic("`step` exceeds i64 range")
	}
	var unit time.Duration
	switch barType.Spec.Aggregation {
	case BarAggregationMillisecond:
		unit = time.Millisecond
	case BarAggregationSecond:
		unit = time.Second
	case BarAggregationMinute:
		unit = time.Minute
	case BarAggregationHour:
		unit = time.Hour
	case BarAggregationDay:
		unit = 24 * time.Hour
	case BarAggregationWeek:
		unit = 7 * 24 * time.Hour
	case BarAggregationMonth:
		unit = 30 * 24 * time.Hour
	case BarAggregationYear:
		unit = 365 * 24 * time.Hour
	default:
		panic("Aggregation not time based")
	}
	if step > uint64(math.MaxInt64/int64(unit)) {
		panic("`step` overflows i64 days")
	}
	return time.Duration(step) * unit
}

func GetBarIntervalNanos(barType BarType) UnixNanos {
	return UnixNanos(GetBarInterval(barType).Nanoseconds())
}

// GetTimeBarStart returns the most recent aligned bar boundary in UTC.
func GetTimeBarStart(now time.Time, barType BarType, origin time.Duration) time.Time {
	now = now.UTC()
	step := barType.Spec.Step
	switch barType.Spec.Aggregation {
	case BarAggregationMonth:
		if step > math.MaxUint32 {
			panic("`step` exceeds u32 range for month arithmetic")
		}
		start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC).Add(origin)
		for start.After(now) {
			start = start.AddDate(0, -12, 0)
		}
		for next := start.AddDate(0, int(step), 0); !next.After(now); next = start.AddDate(0, int(step), 0) {
			start = next
		}
		return start
	case BarAggregationYear:
		if step > math.MaxInt32 {
			panic("`step` exceeds i32 range for year arithmetic")
		}
		year := now.Year()
		start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Add(origin)
		if start.After(now) {
			year -= int(step)
		}
		for {
			next := time.Date(year+int(step), 1, 1, 0, 0, 0, 0, time.UTC).Add(origin)
			if next.After(now) {
				break
			}
			year += int(step)
		}
		return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Add(origin)
	case BarAggregationWeek:
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		daysFromMonday := (int(dayStart.Weekday()) + 6) % 7
		start := dayStart.AddDate(0, 0, -daysFromMonday).Add(origin)
		if start.After(now) {
			start = start.Add(-time.Duration(step) * 7 * 24 * time.Hour)
		}
		return start
	default:
		interval := GetBarInterval(barType)
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		base := dayStart.Add(origin)
		delta := now.Sub(base)
		periods := floorDiv(int64(delta), int64(interval))
		return base.Add(time.Duration(periods) * interval)
	}
}

func floorDiv(value, divisor int64) int64 {
	quotient := value / divisor
	if value%divisor < 0 {
		quotient--
	}
	return quotient
}

// Bar represents one validated OHLCV aggregate.
type Bar struct {
	BarType BarType          `json:"bar_type"`
	Open    decimal.Price    `json:"open"`
	High    decimal.Price    `json:"high"`
	Low     decimal.Price    `json:"low"`
	Close   decimal.Price    `json:"close"`
	Volume  decimal.Quantity `json:"volume"`
	TsEvent UnixNanos        `json:"ts_event"`
	TsInit  UnixNanos        `json:"ts_init"`
}

func NewBar(
	barType BarType,
	open, high, low, close decimal.Price,
	volume decimal.Quantity,
	tsEvent, tsInit UnixNanos,
) (Bar, error) {
	for _, condition := range []struct {
		ok   bool
		name string
	}{
		{high.Cmp(open) >= 0, "high >= open"},
		{high.Cmp(low) >= 0, "high >= low"},
		{high.Cmp(close) >= 0, "high >= close"},
		{low.Cmp(close) <= 0, "low <= close"},
		{low.Cmp(open) <= 0, "low <= open"},
	} {
		if !condition.ok {
			return Bar{}, fmt.Errorf("Condition failed: %s", condition.name)
		}
	}
	return Bar{barType, open, high, low, close, volume, tsEvent, tsInit}, nil
}

func MustBar(
	barType BarType,
	open, high, low, close decimal.Price,
	volume decimal.Quantity,
	tsEvent, tsInit UnixNanos,
) Bar {
	bar, err := NewBar(barType, open, high, low, close, volume, tsEvent, tsInit)
	if err != nil {
		panic(err)
	}
	return bar
}

func (b Bar) Equal(other Bar) bool {
	return b.BarType == other.BarType &&
		b.Open.Equal(other.Open) && b.High.Equal(other.High) &&
		b.Low.Equal(other.Low) && b.Close.Equal(other.Close) &&
		b.Volume.Equal(other.Volume) && b.TsEvent == other.TsEvent && b.TsInit == other.TsInit
}

func (b Bar) String() string {
	return fmt.Sprintf("%s,%s,%s,%s,%s,%s,%d", b.BarType, b.Open, b.High, b.Low, b.Close, b.Volume, b.TsEvent)
}

func (b Bar) MarshalJSON() ([]byte, error) {
	type wire Bar
	return json.Marshal(struct {
		Type string `json:"type"`
		wire
	}{Type: "Bar", wire: wire(b)})
}

func (b *Bar) UnmarshalJSON(data []byte) error {
	type wire Bar
	var decoded struct {
		Type string `json:"type"`
		wire
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Type != "" && decoded.Type != "Bar" {
		return fmt.Errorf("invalid market data type %q", decoded.Type)
	}
	validated, err := NewBar(
		decoded.BarType, decoded.Open, decoded.High, decoded.Low, decoded.Close,
		decoded.Volume, decoded.TsEvent, decoded.TsInit,
	)
	if err != nil {
		return err
	}
	*b = validated
	return nil
}

func (b Bar) MarshalBinary() ([]byte, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(data); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (b *Bar) UnmarshalBinary(data []byte) error {
	var jsonData []byte
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&jsonData); err != nil {
		return err
	}
	return json.Unmarshal(jsonData, b)
}

func DefaultBar() Bar {
	return MustBar(
		MustBarType("AUDUSD.SIM-1-MINUTE-LAST-INTERNAL"),
		decimal.MustPrice("1.00010"), decimal.MustPrice("1.00020"),
		decimal.MustPrice("1.00000"), decimal.MustPrice("1.00010"),
		decimal.MustQuantity("100000"), 0, 0,
	)
}
