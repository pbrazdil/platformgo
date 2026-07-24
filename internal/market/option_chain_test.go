package market

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func makeOptionQuote(instrumentID InstrumentID) QuoteTick {
	return MustQuoteTick(
		instrumentID,
		decimal.MustPrice("100.00"),
		decimal.MustPrice("101.00"),
		decimal.MustQuantity("1.0"),
		decimal.MustQuantity("1.0"),
		1,
		1,
	)
}

func makeOptionSeriesID() ids.OptionSeriesID {
	return ids.NewOptionSeriesID(
		ids.MustVenue("DERIBIT"),
		ids.MustSymbol("BTC"),
		ids.MustSymbol("BTC"),
		1_700_000_000_000_000_000,
	)
}

func optionFloat(value float64) *float64 {
	return &value
}

func optionPrice(value string) *decimal.Price {
	price := decimal.MustPrice(value)
	return &price
}

func optionQuantity(value string) *decimal.Quantity {
	quantity := decimal.MustQuantity(value)
	return &quantity
}

func optionRequireStrikes(t *testing.T, got []decimal.Price, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("strike count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Equal(decimal.MustPrice(want[i])) {
			t.Fatalf("strike[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:419
//	test: test_strike_range_fixed
func TestStrikeRangeFixed(t *testing.T) {
	strikeRange := FixedStrikeRange(decimal.MustPrice("50000"), decimal.MustPrice("55000"))
	want := FixedStrikeRange(decimal.MustPrice("50000"), decimal.MustPrice("55000"))
	if !strikeRange.Equal(want) {
		t.Fatalf("fixed range = %+v, want %+v", strikeRange, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:428
//	test: test_strike_range_atm_relative
func TestStrikeRangeATMRelative(t *testing.T) {
	strikeRange := ATMRelativeStrikeRange(5, 5)
	if strikeRange.Kind != StrikeRangeATMRelative ||
		strikeRange.StrikesAbove != 5 || strikeRange.StrikesBelow != 5 {
		t.Fatalf("unexpected ATM-relative range: %+v", strikeRange)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:447
//	test: test_strike_range_atm_percent
func TestStrikeRangeATMPercent(t *testing.T) {
	strikeRange := ATMPercentStrikeRange(0.1)
	if strikeRange.Kind != StrikeRangeATMPercent ||
		math.Abs(strikeRange.Percent-0.1) >= math.Nextafter(1, 2)-1 {
		t.Fatalf("unexpected ATM-percent range: %+v", strikeRange)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:457
//	test: test_option_greeks_default_fields
func TestOptionGreeksDefaultFields(t *testing.T) {
	greeks := OptionGreeks{
		InstrumentID: "BTC-20240101-50000-C.DERIBIT",
		Convention:   GreeksConventionBlackScholes,
		Greeks:       OptionGreekValues{},
	}
	if greeks.Greeks.Delta != 0 || greeks.Greeks.Gamma != 0 ||
		greeks.Greeks.Vega != 0 || greeks.Greeks.Theta != 0 ||
		greeks.MarkIV != nil || greeks.Convention != GreeksConventionBlackScholes {
		t.Fatalf("unexpected default fields: %+v", greeks)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:479
//	test: test_option_greeks_default_is_black_scholes
func TestOptionGreeksDefaultIsBlackScholes(t *testing.T) {
	greeks := DefaultOptionGreeks()
	if greeks.Convention != GreeksConventionBlackScholes {
		t.Fatalf("default convention = %s", greeks.Convention)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:485
//	test: test_option_greeks_display
func TestOptionGreeksDisplay(t *testing.T) {
	greeks := OptionGreeks{
		InstrumentID: "BTC-20240101-50000-C.DERIBIT",
		Convention:   GreeksConventionPriceAdjusted,
		Greeks: OptionGreekValues{
			Delta: 0.55,
			Gamma: 0.001,
			Vega:  10,
			Theta: -5,
		},
		MarkIV: optionFloat(0.65),
	}
	display := greeks.String()
	for _, want := range []string{"OptionGreeks", "PRICE_ADJUSTED", "0.55"} {
		if !strings.Contains(display, want) {
			t.Fatalf("%q does not contain %q", display, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:511
//	test: test_option_greeks_data_serde_round_trip
//
// Adaptation: the tagged OptionGreeks value is serialized directly because Go
// does not require the source enum wrapper for dynamic dispatch.
func TestOptionGreeksDataSerdeRoundTrip(t *testing.T) {
	greeks := OptionGreeks{
		InstrumentID: "BTC-20240101-50000-C.DERIBIT",
		Convention:   GreeksConventionPriceAdjusted,
		Greeks: OptionGreekValues{
			Delta: 0.55,
			Gamma: 0.001,
			Vega:  10,
			Theta: -5,
			Rho:   0.2,
		},
		MarkIV:          optionFloat(0.65),
		AskIV:           optionFloat(0.66),
		UnderlyingPrice: optionPrice("50000.0"),
		OpenInterest:    nil,
		TsEvent:         1,
		TsInit:          2,
	}
	data, err := json.Marshal(greeks)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundtripped OptionGreeks
	if err := json.Unmarshal(data, &roundtripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !roundtripped.Equal(greeks) {
		t.Fatalf("round-trip differs: %+v != %+v", roundtripped, greeks)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:539
//	test: test_option_chain_slice_empty
func TestOptionChainSliceEmpty(t *testing.T) {
	slice := OptionChainSlice{
		SeriesID: makeOptionSeriesID(),
		Calls:    make(OptionStrikeMap),
		Puts:     make(OptionStrikeMap),
		TsEvent:  1,
		TsInit:   1,
	}
	if !slice.IsEmpty() || slice.StrikeCount() != 0 || len(slice.Strikes()) != 0 {
		t.Fatalf("unexpected non-empty slice: %+v", slice)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:555
//	test: test_option_chain_slice_with_data
func TestOptionChainSliceWithData(t *testing.T) {
	callID := InstrumentID("BTC-20240101-50000-C.DERIBIT")
	putID := InstrumentID("BTC-20240101-50000-P.DERIBIT")
	strike := decimal.MustPrice("50000")
	calls := make(OptionStrikeMap)
	calls.Set(strike, OptionStrikeData{
		Quote: makeOptionQuote(callID),
		Greeks: &OptionGreeks{
			InstrumentID: callID,
			Convention:   GreeksConventionBlackScholes,
			Greeks:       OptionGreekValues{Delta: 0.55},
		},
	})
	puts := make(OptionStrikeMap)
	puts.Set(strike, OptionStrikeData{Quote: makeOptionQuote(putID)})
	slice := OptionChainSlice{
		SeriesID:  makeOptionSeriesID(),
		ATMStrike: &strike,
		Calls:     calls,
		Puts:      puts,
		TsEvent:   1,
		TsInit:    1,
	}
	if slice.IsEmpty() || slice.StrikeCount() != 1 {
		t.Fatalf("unexpected slice counts: %+v", slice)
	}
	optionRequireStrikes(t, slice.Strikes(), "50000")
	if _, ok := slice.GetCall(strike); !ok {
		t.Fatal("call missing")
	}
	if _, ok := slice.GetPut(strike); !ok {
		t.Fatal("put missing")
	}
	callGreeks, ok := slice.GetCallGreeks(strike)
	if !ok || callGreeks == nil {
		t.Fatal("call Greeks missing")
	}
	if _, ok := slice.GetPutGreeks(strike); ok {
		t.Fatal("unexpected put Greeks")
	}
	if callGreeks.Greeks.Delta != 0.55 {
		t.Fatalf("call delta = %v", callGreeks.Greeks.Delta)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:605
//	test: test_option_chain_slice_display
func TestOptionChainSliceDisplay(t *testing.T) {
	slice := OptionChainSlice{
		SeriesID: makeOptionSeriesID(),
		Calls:    make(OptionStrikeMap),
		Puts:     make(OptionStrikeMap),
		TsEvent:  1,
		TsInit:   1,
	}
	display := slice.String()
	for _, want := range []string{"OptionChainSlice", "DERIBIT"} {
		if !strings.Contains(display, want) {
			t.Fatalf("%q does not contain %q", display, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:621
//	test: test_option_chain_slice_ts_init
func TestOptionChainSliceTsInit(t *testing.T) {
	slice := OptionChainSlice{
		SeriesID: makeOptionSeriesID(),
		Calls:    make(OptionStrikeMap),
		Puts:     make(OptionStrikeMap),
		TsEvent:  1,
		TsInit:   42,
	}
	if slice.TsInit != 42 {
		t.Fatalf("ts_init = %d", slice.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:637
//	test: test_strike_range_resolve_fixed
func TestStrikeRangeResolveFixed(t *testing.T) {
	strikeRange := FixedStrikeRange(decimal.MustPrice("50000"), decimal.MustPrice("55000"))
	result := strikeRange.Resolve(nil, nil)
	optionRequireStrikes(t, result, "50000", "55000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:644
//	test: test_strike_range_resolve_atm_relative
func TestStrikeRangeResolveATMRelative(t *testing.T) {
	strikeRange := ATMRelativeStrikeRange(2, 2)
	strikes := []decimal.Price{
		decimal.MustPrice("45000"),
		decimal.MustPrice("47000"),
		decimal.MustPrice("50000"),
		decimal.MustPrice("53000"),
		decimal.MustPrice("55000"),
		decimal.MustPrice("57000"),
	}
	atm := decimal.MustPrice("50000")
	result := strikeRange.Resolve(&atm, strikes)
	if len(result) != 5 {
		t.Fatalf("strike count = %d", len(result))
	}
	if !result[0].Equal(decimal.MustPrice("45000")) ||
		!result[4].Equal(decimal.MustPrice("55000")) {
		t.Fatalf("unexpected strike window: %v", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:662
//	test: test_strike_range_resolve_atm_relative_saturates_extreme_window
func TestStrikeRangeResolveATMRelativeSaturatesExtremeWindow(t *testing.T) {
	strikeRange := ATMRelativeStrikeRange(math.MaxUint, math.MaxUint)
	strikes := []decimal.Price{
		decimal.MustPrice("45000"),
		decimal.MustPrice("50000"),
		decimal.MustPrice("55000"),
	}
	atm := decimal.MustPrice("50000")
	result := strikeRange.Resolve(&atm, strikes)
	optionRequireStrikes(t, result, "45000", "50000", "55000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:680
//	test: test_strike_range_resolve_atm_relative_no_atm
func TestStrikeRangeResolveATMRelativeNoATM(t *testing.T) {
	strikeRange := ATMRelativeStrikeRange(2, 2)
	strikes := []decimal.Price{decimal.MustPrice("50000"), decimal.MustPrice("55000")}
	if result := strikeRange.Resolve(nil, strikes); len(result) != 0 {
		t.Fatalf("result = %v, want empty", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:692
//	test: test_strike_range_resolve_atm_percent
func TestStrikeRangeResolveATMPercent(t *testing.T) {
	strikeRange := ATMPercentStrikeRange(0.1)
	strikes := []decimal.Price{
		decimal.MustPrice("45000"),
		decimal.MustPrice("48000"),
		decimal.MustPrice("50000"),
		decimal.MustPrice("52000"),
		decimal.MustPrice("55000"),
		decimal.MustPrice("60000"),
	}
	atm := decimal.MustPrice("50000")
	result := strikeRange.Resolve(&atm, strikes)
	if len(result) != 5 {
		t.Fatalf("strike count = %d", len(result))
	}
	for _, want := range []string{"45000", "48000", "50000", "52000", "55000"} {
		if !containsStrike(result, decimal.MustPrice(want)) {
			t.Fatalf("result %v does not contain %s", result, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:710
//	test: test_option_chain_slice_new_empty
func TestOptionChainSliceNewEmpty(t *testing.T) {
	slice := NewOptionChainSlice(makeOptionSeriesID())
	if !slice.IsEmpty() || slice.CallCount() != 0 || slice.PutCount() != 0 ||
		slice.ATMStrike != nil {
		t.Fatalf("unexpected new slice: %+v", slice)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:719
//	test: test_strike_range_resolve_delta_falls_back_to_atm_relative
func TestStrikeRangeResolveDeltaFallsBackToATMRelative(t *testing.T) {
	strikes := make([]decimal.Price, 0, 21)
	for i := 0; i <= 20; i++ {
		strikes = append(strikes, decimal.MustPrice(fmt.Sprintf("%d", 40000+i*1000)))
	}
	atm := decimal.MustPrice("50000")
	deltaRange := DeltaStrikeRange(0.25, 0.05)
	expected := ATMRelativeStrikeRange(
		DefaultDeltaFallbackStrikes,
		DefaultDeltaFallbackStrikes,
	).Resolve(&atm, strikes)
	result := deltaRange.Resolve(&atm, strikes)
	optionRequireStrikes(t, result, strikeStrings(expected)...)
	if len(result) != 2*DefaultDeltaFallbackStrikes+1 ||
		!containsStrike(result, decimal.MustPrice("50000")) ||
		containsStrike(result, decimal.MustPrice("40000")) ||
		containsStrike(result, decimal.MustPrice("60000")) {
		t.Fatalf("unexpected delta fallback: %v", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/option_chain.rs:745
//	test: test_strike_range_resolve_delta_empty_without_atm
func TestStrikeRangeResolveDeltaEmptyWithoutATM(t *testing.T) {
	deltaRange := DeltaStrikeRange(0.25, 0.05)
	strikes := []decimal.Price{decimal.MustPrice("50000"), decimal.MustPrice("55000")}
	if result := deltaRange.Resolve(nil, strikes); len(result) != 0 {
		t.Fatalf("result = %v, want empty", result)
	}
}

func strikeStrings(strikes []decimal.Price) []string {
	result := make([]string, len(strikes))
	for i, strike := range strikes {
		result[i] = strike.String()
	}
	return result
}
