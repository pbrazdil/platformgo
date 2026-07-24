package instrument

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func wantPrice(t *testing.T, text string, precision uint8) decimal.Price {
	t.Helper()
	price, err := decimal.NewPrice(text, precision)
	if err != nil {
		t.Fatal(err)
	}
	return price
}

func requirePrice(t *testing.T, got decimal.Price, ok bool, text string, precision uint8) {
	t.Helper()
	if !ok || !got.Equal(wantPrice(t, text, precision)) || got.Precision() != precision {
		t.Fatalf("got (%v, %v), want %s at precision %d", got, ok, text, precision)
	}
}

func requireTickError(t *testing.T, err error, kind, display string) *TickSchemeError {
	t.Helper()
	var typed *TickSchemeError
	if !errors.As(err, &typed) || typed.Kind != kind || err.Error() != display {
		t.Fatalf("got %#v (%v), want kind %q display %q", err, err, kind, display)
	}
	return typed
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:725
//	test: fixed_tick_scheme_prices
func TestFixedTickSchemePrices(t *testing.T) {
	s, _ := NewFixedTickScheme(.5)
	bid, bok := s.NextBidPrice(10.3, 0, 2)
	ask, aok := s.NextAskPrice(10.3, 0, 2)
	if !bok || !aok || bid.Cmp(ask) >= 0 {
		t.Fatalf("bid=%v ask=%v", bid, ask)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:733
//	test: fixed_tick_negative_returns_typed_error_with_display
func TestFixedTickNegativeReturnsTypedErrorWithDisplay(t *testing.T) {
	_, err := NewFixedTickScheme(-.01)
	typed := requireTickError(t, err, "tick_not_positive", "tick must be positive")
	if typed.Tick != -.01 {
		t.Fatalf("tick=%v", typed.Tick)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:741
//	test: fixed_tick_boundary
func TestFixedTickBoundary(t *testing.T) {
	s, _ := NewFixedTickScheme(.5)
	price, ok := s.NextBidPrice(10.5, 0, 2)
	requirePrice(t, price, ok, "10.5", 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:748
//	test: fixed_tick_scheme_preserves_decimal_boundaries
func TestFixedTickSchemePreservesDecimalBoundaries(t *testing.T) {
	tenth, _ := NewFixedTickScheme(.1)
	cent, _ := NewFixedTickScheme(.01)
	for _, tc := range []struct {
		scheme    FixedTickScheme
		value     float64
		precision uint8
		expected  string
	}{
		{tenth, .3, 1, ".3"}, {cent, .07, 2, ".07"},
	} {
		bid, bok := tc.scheme.NextBidPrice(tc.value, 0, tc.precision)
		ask, aok := tc.scheme.NextAskPrice(tc.value, 0, tc.precision)
		requirePrice(t, bid, bok, tc.expected, tc.precision)
		requirePrice(t, ask, aok, tc.expected, tc.precision)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:759
//	test: fixed_tick_multiple_steps
func TestFixedTickMultipleSteps(t *testing.T) {
	s, _ := NewFixedTickScheme(1)
	bid, bok := s.NextBidPrice(10, 2, 1)
	ask, aok := s.NextAskPrice(10, 3, 1)
	requirePrice(t, bid, bok, "8", 1)
	requirePrice(t, ask, aok, "13", 1)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:768
//	test: tick_scheme_round_trip
func TestTickSchemeRoundTrip(t *testing.T) {
	s, err := ParseTickScheme("CRYPTO_0_01")
	if err != nil || s.String() != "CRYPTO_0_01" {
		t.Fatalf("scheme=%v err=%v", s, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:774
//	test: tick_scheme_rule_from_fixed_precision_name
func TestTickSchemeRuleFromFixedPrecisionName(t *testing.T) {
	s, ok := TickSchemeRuleFromName("fixed_precision_1")
	if !ok {
		t.Fatal("scheme not found")
	}
	bid, bok := s.NextBidPrice(.3, 0, 1)
	ask, aok := s.NextAskPrice(.31, 0, 1)
	requirePrice(t, bid, bok, ".3", 1)
	requirePrice(t, ask, aok, ".4", 1)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:782
//	test: tick_scheme_unknown
func TestTickSchemeUnknown(t *testing.T) {
	_, err := ParseTickScheme("UNKNOWN")
	typed := requireTickError(t, err, "unknown_name", "unknown tick scheme UNKNOWN")
	if typed.Name != "UNKNOWN" {
		t.Fatalf("name=%q", typed.Name)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:795
//	test: tick_scheme_fixed_precision_above_max_returns_unknown_name
func TestTickSchemeFixedPrecisionAboveMaxReturnsUnknownName(t *testing.T) {
	name := "FIXED_PRECISION_17"
	_, err := ParseTickScheme(name)
	requireTickError(t, err, "unknown_name", "unknown tick scheme "+name)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:804
//	test: fixed_tick_zero
func TestFixedTickZero(t *testing.T) {
	_, err := NewFixedTickScheme(0)
	requireTickError(t, err, "tick_not_positive", "tick must be positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:814
//	test: fixed_tick_non_finite_returns_error
func TestFixedTickNonFiniteReturnsError(t *testing.T) {
	for _, tick := range []float64{math.Inf(1), math.NaN()} {
		_, err := NewFixedTickScheme(tick)
		typed := requireTickError(t, err, "tick_not_finite", "tick must be finite")
		if !(typed.Tick == tick || math.IsNaN(typed.Tick) && math.IsNaN(tick)) {
			t.Fatalf("tick=%v input=%v", typed.Tick, tick)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:832
//	test: fixed_tick_scheme_nan_value_returns_none
func TestFixedTickSchemeNaNValueReturnsNone(t *testing.T) {
	s, _ := NewFixedTickScheme(1)
	if _, ok := s.NextBidPrice(math.NaN(), 0, 2); ok {
		t.Fatal("NaN bid returned a price")
	}
	if _, ok := s.NextAskPrice(math.NaN(), 0, 2); ok {
		t.Fatal("NaN ask returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:839
//	test: fixed_tick_scheme_out_of_range_returns_none
func TestFixedTickSchemeOutOfRangeReturnsNone(t *testing.T) {
	s, _ := NewFixedTickScheme(priceMaximum)
	if _, ok := s.NextAskPrice(priceMaximum, 1, 2); ok {
		t.Fatal("out-of-range ask returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:846
//	test: tiered_tick_scheme_topix100_construction
func TestTieredTickSchemeTopix100Construction(t *testing.T) {
	s := Topix100TickScheme()
	if s.TickCount() == 0 || s.Precision() != 4 {
		t.Fatalf("count=%d precision=%d", s.TickCount(), s.Precision())
	}
	requirePrice(t, s.MinPrice(), true, ".1", 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:854
//	test: tiered_tick_scheme_betfair_construction
func TestTieredTickSchemeBetfairConstruction(t *testing.T) {
	s := BetfairTickScheme()
	if s.TickCount() != 350 || s.Precision() != 2 {
		t.Fatalf("count=%d precision=%d", s.TickCount(), s.Precision())
	}
	requirePrice(t, s.MinPrice(), true, "1.01", 2)
	requirePrice(t, s.MaxPrice(), true, "1000", 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:863
//	test: tiered_tick_scheme_ask_at_low_price
func TestTieredTickSchemeAskAtLowPrice(t *testing.T) {
	price, ok := Topix100TickScheme().NextAskPrice(500, 0, 4)
	requirePrice(t, price, ok, "500", 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:870
//	test: tiered_tick_scheme_bid_at_low_price
func TestTieredTickSchemeBidAtLowPrice(t *testing.T) {
	price, ok := Topix100TickScheme().NextBidPrice(500, 0, 4)
	requirePrice(t, price, ok, "500", 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:877
//	test: tiered_tick_scheme_ask_steps
func TestTieredTickSchemeAskSteps(t *testing.T) {
	s := Topix100TickScheme()
	zero, _ := s.NextAskPrice(500, 0, 4)
	one, ok := s.NextAskPrice(500, 1, 4)
	requirePrice(t, one, ok, "500.1", 4)
	if one.Cmp(zero) <= 0 {
		t.Fatalf("%v <= %v", one, zero)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:886
//	test: tiered_tick_scheme_bid_steps
func TestTieredTickSchemeBidSteps(t *testing.T) {
	s := Topix100TickScheme()
	zero, _ := s.NextBidPrice(500, 0, 4)
	one, ok := s.NextBidPrice(500, 1, 4)
	requirePrice(t, one, ok, "499.9", 4)
	if one.Cmp(zero) >= 0 {
		t.Fatalf("%v >= %v", one, zero)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:895
//	test: tiered_tick_scheme_tier_boundary_1000
func TestTieredTickSchemeTierBoundary1000(t *testing.T) {
	price, ok := Topix100TickScheme().NextAskPrice(1000, 1, 4)
	requirePrice(t, price, ok, "1000.5", 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:905
//	test: tiered_tick_scheme_betfair_ask_transition
func TestTieredTickSchemeBetfairAskTransition(t *testing.T) {
	s := BetfairTickScheme()
	for _, tc := range []struct {
		value float64
		n     int32
		want  string
	}{{3.90, 1, "3.95"}, {4, 1, "4.10"}} {
		price, ok := s.NextAskPrice(tc.value, tc.n, 2)
		requirePrice(t, price, ok, tc.want, 2)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:919
//	test: tiered_tick_scheme_betfair_bid_transition
func TestTieredTickSchemeBetfairBidTransition(t *testing.T) {
	s := BetfairTickScheme()
	for _, tc := range []struct {
		value float64
		n     int32
		want  string
	}{{1.499, 0, "1.49"}, {2.011, 0, "2.00"}, {2.027, 2, "1.99"}} {
		price, ok := s.NextBidPrice(tc.value, tc.n, 2)
		requirePrice(t, price, ok, tc.want, 2)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:930
//	test: tiered_tick_scheme_between_ticks
func TestTieredTickSchemeBetweenTicks(t *testing.T) {
	s := Topix100TickScheme()
	ask, aok := s.NextAskPrice(1000.3, 0, 4)
	bid, bok := s.NextBidPrice(1000.3, 0, 4)
	if !aok || !bok || ask.Decimal().Cmp(decimal.MustParse("1000.3")) < 0 || bid.Decimal().Cmp(decimal.MustParse("1000.3")) > 0 {
		t.Fatalf("bid=%v ask=%v", bid, ask)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:940
//	test: tiered_tick_scheme_off_grid_bid_below_tick
func TestTieredTickSchemeOffGridBidBelowTick(t *testing.T) {
	s, _ := NewTieredTickScheme([]PriceTier{{1, 2, .05}}, 2, 100)
	price, ok := s.NextBidPrice(1.049, 0, 2)
	requirePrice(t, price, ok, "1", 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:948
//	test: tiered_tick_scheme_off_grid_ask_above_tick
func TestTieredTickSchemeOffGridAskAboveTick(t *testing.T) {
	s, _ := NewTieredTickScheme([]PriceTier{{1, 2, .05}}, 2, 100)
	price, ok := s.NextAskPrice(1.051, 0, 2)
	requirePrice(t, price, ok, "1.10", 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:956
//	test: tiered_tick_scheme_bid_below_min_returns_none
func TestTieredTickSchemeBidBelowMinReturnsNone(t *testing.T) {
	if _, ok := Topix100TickScheme().NextBidPrice(.05, 0, 4); ok {
		t.Fatal("below-min bid returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:962
//	test: tiered_tick_scheme_ask_beyond_last_tick_returns_none
func TestTieredTickSchemeAskBeyondLastTickReturnsNone(t *testing.T) {
	s := Topix100TickScheme()
	last, _ := strconvFloat(s.MaxPrice().String())
	if _, ok := s.NextAskPrice(last, 1, 4); ok {
		t.Fatal("ask beyond last returned a price")
	}
}

func strconvFloat(text string) (float64, error) {
	return strconv.ParseFloat(text, 64)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:969
//	test: tiered_tick_scheme_bid_beyond_last_tick_returns_last
func TestTieredTickSchemeBidBeyondLastTickReturnsLast(t *testing.T) {
	s, _ := NewTieredTickScheme([]PriceTier{{1, 10, 1}}, 1, 100)
	price, ok := s.NextBidPrice(9.5, 0, 1)
	requirePrice(t, price, ok, "9", 1)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:977
//	test: tiered_tick_scheme_negative_n_returns_none
func TestTieredTickSchemeNegativeNReturnsNone(t *testing.T) {
	s := Topix100TickScheme()
	if _, ok := s.NextBidPrice(500, -1, 4); ok {
		t.Fatal("negative bid steps returned a price")
	}
	if _, ok := s.NextAskPrice(500, -1, 4); ok {
		t.Fatal("negative ask steps returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:984
//	test: tiered_tick_scheme_nan_value_returns_none
func TestTieredTickSchemeNaNValueReturnsNone(t *testing.T) {
	s := Topix100TickScheme()
	if _, ok := s.NextBidPrice(math.NaN(), 0, 4); ok {
		t.Fatal("NaN bid returned a price")
	}
	if _, ok := s.NextAskPrice(math.NaN(), 0, 4); ok {
		t.Fatal("NaN ask returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:991
//	test: tiered_tick_scheme_infinite_value_saturates
func TestTieredTickSchemeInfiniteValueSaturates(t *testing.T) {
	s := Topix100TickScheme()
	bid, ok := s.NextBidPrice(math.Inf(1), 0, 4)
	if !ok || !bid.Equal(s.MaxPrice()) {
		t.Fatalf("bid=%v ok=%v", bid, ok)
	}
	if _, ok := s.NextAskPrice(math.Inf(1), 0, 4); ok {
		t.Fatal("+Inf ask returned a price")
	}
	if _, ok := s.NextBidPrice(math.Inf(-1), 0, 4); ok {
		t.Fatal("-Inf bid returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1002
//	test: crypto_tick_scheme_out_of_range_returns_none
func TestCryptoTickSchemeOutOfRangeReturnsNone(t *testing.T) {
	s, _ := ParseTickScheme(Crypto001TickSchemeName)
	if _, ok := s.NextAskPrice(priceMaximum*2, 0, 2); ok {
		t.Fatal("out-of-range ask returned a price")
	}
	if _, ok := s.NextBidPrice(priceMinimum*2, 0, 2); ok {
		t.Fatal("out-of-range bid returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1010
//	test: tiered_tick_scheme_validation_empty_tiers
func TestTieredTickSchemeValidationEmptyTiers(t *testing.T) {
	_, err := NewTieredTickScheme(nil, 2, 100)
	requireTickError(t, err, "empty_tiers", "tiers must not be empty")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1081
//	test: tiered_tick_scheme_invalid_tiers_return_typed_errors
func TestTieredTickSchemeInvalidTiersReturnTypedErrors(t *testing.T) {
	cases := []struct {
		tiers   []PriceTier
		kind    string
		display string
	}{
		{[]PriceTier{{100, 50, 1}}, "tier_start_not_less_than_stop", "tier 0: start (100) must be less than stop (50)"},
		{[]PriceTier{{2, 2, .1}}, "tier_start_not_less_than_stop", "tier 0: start (2) must be less than stop (2)"},
		{[]PriceTier{{0, 100, -1}}, "tier_step_not_positive", "tier 0: step (-1) must be positive"},
		{[]PriceTier{{1, 2, 0}}, "tier_step_not_positive", "tier 0: step (0) must be positive"},
		{[]PriceTier{{0, 100, 200}}, "tier_step_not_less_than_range", "tier 0: step (200) must be less than range (100 - 0 = 100)"},
		{[]PriceTier{{10, 20, 1}, {1, 10, 1}}, "tier_overlaps_previous", "tier 1: start (1) overlaps previous tier stop (20)"},
		{[]PriceTier{{1, 10, 1}, {5, 15, 1}}, "tier_overlaps_previous", "tier 1: start (5) overlaps previous tier stop (10)"},
	}
	for _, tc := range cases {
		_, err := NewTieredTickScheme(tc.tiers, 2, 100)
		requireTickError(t, err, tc.kind, tc.display)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1096
//	test: tiered_tick_scheme_nan_tiers_return_typed_error
func TestTieredTickSchemeNaNTiersReturnTypedError(t *testing.T) {
	for _, tiers := range [][]PriceTier{
		{{math.NaN(), 10, 1}}, {{1, math.NaN(), 1}}, {{1, 10, math.NaN()}},
	} {
		_, err := NewTieredTickScheme(tiers, 2, 100)
		typed := requireTickError(t, err, "tier_values_nan", "tier 0: values must not be NaN")
		if typed.Index != 0 || !(math.IsNaN(typed.Start) || math.IsNaN(typed.Stop) || math.IsNaN(typed.Step)) {
			t.Fatalf("error=%#v", typed)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1118
//	test: tiered_tick_scheme_start_outside_price_range_returns_typed_error
func TestTieredTickSchemeStartOutsidePriceRangeReturnsTypedError(t *testing.T) {
	start, stop := priceMinimum-1, priceMinimum+1
	_, err := NewTieredTickScheme([]PriceTier{{start, stop, 1}}, 2, 100)
	typed := requireTickError(t, err, "tier_start_outside_price_range",
		"tier 0: start ("+strconv.FormatFloat(start, 'g', -1, 64)+") outside Price range")
	if typed.Start != start {
		t.Fatalf("start=%v", typed.Start)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1134
//	test: tiered_tick_scheme_stop_outside_price_range_returns_typed_error
func TestTieredTickSchemeStopOutsidePriceRangeReturnsTypedError(t *testing.T) {
	start, stop := priceMaximum-2, priceMaximum+1
	_, err := NewTieredTickScheme([]PriceTier{{start, stop, 1}}, 2, 100)
	typed := requireTickError(t, err, "tier_stop_outside_price_range",
		"tier 0: stop ("+strconv.FormatFloat(stop, 'g', -1, 64)+") outside Price range")
	if typed.Stop != stop {
		t.Fatalf("stop=%v", typed.Stop)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1150
//	test: tiered_tick_scheme_invalid_precision_wraps_source_error
func TestTieredTickSchemeInvalidPrecisionWrapsSourceError(t *testing.T) {
	_, source := decimal.ZeroPrice(decimal.MaxPrecision + 1)
	_, err := NewTieredTickScheme([]PriceTier{{1, 10, 1}}, decimal.MaxPrecision+1, 100)
	typed := requireTickError(t, err, "invalid_precision", source.Error())
	if typed.Source == nil || typed.Source.Error() != source.Error() {
		t.Fatalf("source=%v, want %v", typed.Source, source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1165
//	test: tiered_tick_scheme_empty_expansion_returns_typed_error
func TestTieredTickSchemeEmptyExpansionReturnsTypedError(t *testing.T) {
	_, err := NewTieredTickScheme([]PriceTier{{1, math.Inf(1), 1}}, 2, 0)
	requireTickError(t, err, "empty_tick_expansion", "tier expansion produced no ticks")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1173
//	test: tiered_tick_scheme_expanded_tick_outside_range_returns_typed_error
func TestTieredTickSchemeExpandedTickOutsideRangeReturnsTypedError(t *testing.T) {
	invalid := priceMaximum + 1
	_, err := NewTieredTickScheme([]PriceTier{{priceMaximum, math.Inf(1), 1}}, 2, 2)
	typed := requireTickError(t, err, "expanded_tick_outside_price_range",
		"expanded tick value "+strconv.FormatFloat(invalid, 'g', -1, 64)+" outside Price range")
	if typed.Value != invalid {
		t.Fatalf("value=%v want=%v", typed.Value, invalid)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1190
//	test: tiered_tick_scheme_finite_tier_includes_all_ticks
func TestTieredTickSchemeFiniteTierIncludesAllTicks(t *testing.T) {
	s, err := NewTieredTickScheme([]PriceTier{{0, .3, .1}}, 1, 100)
	if err != nil || s.TickCount() != 3 {
		t.Fatalf("count=%d err=%v", s.TickCount(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1197
//	test: tiered_tick_scheme_simple_two_tiers
func TestTieredTickSchemeSimpleTwoTiers(t *testing.T) {
	s, err := NewTieredTickScheme([]PriceTier{{1, 10, 1}, {10, 100, 5}}, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	ticks := s.Ticks()
	for _, tc := range []struct {
		index int
		want  string
	}{{0, "1"}, {8, "9"}, {9, "10"}, {10, "15"}} {
		requirePrice(t, ticks[tc.index], true, tc.want, 2)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1210
//	test: tiered_tick_scheme_infinity_tier
func TestTieredTickSchemeInfinityTier(t *testing.T) {
	s, err := NewTieredTickScheme([]PriceTier{{100, math.Inf(1), 10}}, 1, 5)
	if err != nil || s.TickCount() != 5 {
		t.Fatalf("count=%d err=%v", s.TickCount(), err)
	}
	requirePrice(t, s.Ticks()[0], true, "100", 1)
	requirePrice(t, s.Ticks()[4], true, "140", 1)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1219
//	test: tiered_tick_scheme_from_str_topix100
func TestTieredTickSchemeFromStrTopix100(t *testing.T) {
	s, err := ParseTickScheme("TOPIX100")
	if err != nil || s.String() != "TIERED" {
		t.Fatalf("scheme=%v err=%v", s, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1225
//	test: tiered_tick_scheme_from_str_betfair
func TestTieredTickSchemeFromStrBetfair(t *testing.T) {
	s, err := ParseTickScheme("BETFAIR")
	if err != nil || s.String() != BetfairTickSchemeName {
		t.Fatalf("scheme=%v err=%v", s, err)
	}
	price, ok := s.NextAskPrice(4, 1, 2)
	requirePrice(t, price, ok, "4.1", 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1235
//	test: tiered_tick_scheme_display
func TestTieredTickSchemeDisplay(t *testing.T) {
	s, _ := NewTieredTickScheme([]PriceTier{{1, 10, 1}}, 2, 100)
	if s.String() != "TIERED" {
		t.Fatalf("display=%q", s.String())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1241
//	test: tiered_tick_scheme_min_tick_bid
func TestTieredTickSchemeMinTickBid(t *testing.T) {
	price, ok := Topix100TickScheme().NextBidPrice(.1, 0, 4)
	requirePrice(t, price, ok, ".1", 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1248
//	test: tiered_tick_scheme_min_tick_bid_n1_returns_none
func TestTieredTickSchemeMinTickBidN1ReturnsNone(t *testing.T) {
	if _, ok := Topix100TickScheme().NextBidPrice(.1, 1, 4); ok {
		t.Fatal("bid before minimum returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1254
//	test: tiered_tick_scheme_boundary_tick_equality
func TestTieredTickSchemeBoundaryTickEquality(t *testing.T) {
	s := Topix100TickScheme()
	bid, bok := s.NextBidPrice(1000, 0, 4)
	ask, aok := s.NextAskPrice(1000, 0, 4)
	requirePrice(t, bid, bok, "1000", 4)
	requirePrice(t, ask, aok, "1000", 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1263
//	test: tiered_tick_scheme_tier_transition_ask_from_999_9
func TestTieredTickSchemeTierTransitionAskFrom9999(t *testing.T) {
	price, ok := Topix100TickScheme().NextAskPrice(999.9, 0, 4)
	requirePrice(t, price, ok, "999.9", 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1270
//	test: tiered_tick_scheme_tier_transition_bid_from_1000_5
func TestTieredTickSchemeTierTransitionBidFrom10005(t *testing.T) {
	price, ok := Topix100TickScheme().NextBidPrice(1000.5, 1, 4)
	requirePrice(t, price, ok, "1000", 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1277
//	test: tiered_tick_scheme_large_n_beyond_bounds_ask
func TestTieredTickSchemeLargeNBeyondBoundsAsk(t *testing.T) {
	s := Topix100TickScheme()
	maximum, _ := strconvFloat(s.MaxPrice().String())
	if _, ok := s.NextAskPrice(maximum-1000, 100000, 4); ok {
		t.Fatal("large ask step returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1284
//	test: tiered_tick_scheme_large_n_beyond_bounds_bid
func TestTieredTickSchemeLargeNBeyondBoundsBid(t *testing.T) {
	s := Topix100TickScheme()
	minimum, _ := strconvFloat(s.MinPrice().String())
	if _, ok := s.NextBidPrice(minimum+1000, 100000, 4); ok {
		t.Fatal("large bid step returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1291
//	test: tiered_tick_scheme_out_of_bounds_ask_far_above
func TestTieredTickSchemeOutOfBoundsAskFarAbove(t *testing.T) {
	if _, ok := Topix100TickScheme().NextAskPrice(999999999, 0, 4); ok {
		t.Fatal("far-above ask returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1297
//	test: tiered_tick_scheme_idempotent_on_tick
func TestTieredTickSchemeIdempotentOnTick(t *testing.T) {
	s := Topix100TickScheme()
	for _, ask := range []bool{false, true} {
		var first decimal.Price
		var ok bool
		if ask {
			first, ok = s.NextAskPrice(500, 0, 4)
		} else {
			first, ok = s.NextBidPrice(500, 0, 4)
		}
		value, _ := strconvFloat(first.String())
		var second decimal.Price
		if ask {
			second, ok = s.NextAskPrice(value, 0, 4)
		} else {
			second, ok = s.NextBidPrice(value, 0, 4)
		}
		if !ok || !first.Equal(second) {
			t.Fatalf("first=%v second=%v", first, second)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1309
//	test: tiered_tick_scheme_consistency_forward_backward
func TestTieredTickSchemeConsistencyForwardBackward(t *testing.T) {
	s := Topix100TickScheme()
	forward, ok := s.NextAskPrice(5000, 10, 4)
	if !ok {
		t.Fatal("forward missing")
	}
	value, _ := strconvFloat(forward.String())
	back, ok := s.NextBidPrice(value, 10, 4)
	if !ok || back.Decimal().Cmp(decimal.MustParse("5000")) > 0 {
		t.Fatalf("back=%v", back)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1318
//	test: tiered_tick_scheme_cumulative_equals_direct
func TestTieredTickSchemeCumulativeEqualsDirect(t *testing.T) {
	s := Topix100TickScheme()
	value := 1000.0
	for range 5 {
		price, ok := s.NextAskPrice(value, 1, 4)
		if !ok {
			t.Fatal("cumulative ask missing")
		}
		value, _ = strconvFloat(price.String())
	}
	direct, ok := s.NextAskPrice(1000, 5, 4)
	requirePrice(t, direct, ok, strconv.FormatFloat(value, 'f', -1, 64), 4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1337
//	test: tiered_tick_scheme_topix100_ask_parametrized
func TestTieredTickSchemeTopix100AskParametrized(t *testing.T) {
	s := Topix100TickScheme()
	for _, tc := range []struct {
		value float64
		n     int32
		want  string
	}{{1000, 0, "1000"}, {1000.25, 0, "1000.5"}, {10001, 0, "10005"}, {10000001, 0, "10005000"}, {9999, 2, "10005"}} {
		price, ok := s.NextAskPrice(tc.value, tc.n, 4)
		requirePrice(t, price, ok, tc.want, 4)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1352
//	test: tiered_tick_scheme_topix100_bid_parametrized
func TestTieredTickSchemeTopix100BidParametrized(t *testing.T) {
	s := Topix100TickScheme()
	for _, tc := range []struct {
		value float64
		n     int32
		want  string
	}{{1000.75, 0, "1000.5"}, {10007, 0, "10005"}, {10000001, 0, "10000000"}, {10006, 2, "9999"}} {
		price, ok := s.NextBidPrice(tc.value, tc.n, 4)
		requirePrice(t, price, ok, tc.want, 4)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1365
//	test: prop_tiered_bid_at_or_below_value
func TestPropTieredBidAtOrBelowValue(t *testing.T) {
	s := Topix100TickScheme()
	for _, value := range []float64{.1, .11, 1.234, 999.99, 1000.25, 50000.7, 99999.9} {
		if bid, ok := s.NextBidPrice(value, 0, 4); ok && bid.Decimal().Cmp(decimal.MustParse(strconv.FormatFloat(value, 'g', -1, 64))) > 0 {
			t.Fatalf("bid %v exceeds %v", bid, value)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1376
//	test: prop_tiered_ask_at_or_above_value
func TestPropTieredAskAtOrAboveValue(t *testing.T) {
	s := Topix100TickScheme()
	for _, value := range []float64{.1, .11, 1.234, 999.99, 1000.25, 50000.7, 99999.9} {
		if ask, ok := s.NextAskPrice(value, 0, 4); ok && ask.Decimal().Cmp(decimal.MustParse(strconv.FormatFloat(value, 'g', -1, 64))) < 0 {
			t.Fatalf("ask %v below %v", ask, value)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1386
//	test: prop_tiered_bid_ask_match_adjacent_ticks
func TestPropTieredBidAskMatchAdjacentTicks(t *testing.T) {
	s := Topix100TickScheme()
	ticks := s.Ticks()
	for _, index := range []int{0, 1, 9998, 10000, len(ticks) - 2} {
		lower, _ := strconvFloat(ticks[index].String())
		upper, _ := strconvFloat(ticks[index+1].String())
		value := (lower + upper) / 2
		for _, offset := range []int32{0, 1, 4} {
			bid, bok := s.NextBidPrice(value, offset, 4)
			ask, aok := s.NextAskPrice(value, offset, 4)
			bidIndex, askIndex := index-int(offset), index+int(offset)+1
			if (bidIndex >= 0) != bok || bok && !bid.Equal(ticks[bidIndex]) {
				t.Fatalf("index=%d offset=%d bid=%v ok=%v", index, offset, bid, bok)
			}
			if (askIndex < len(ticks)) != aok || aok && !ask.Equal(ticks[askIndex]) {
				t.Fatalf("index=%d offset=%d ask=%v ok=%v", index, offset, ask, aok)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1414
//	test: prop_tiered_ask_monotonic_in_n
func TestPropTieredAskMonotonicInN(t *testing.T) {
	s := Topix100TickScheme()
	for _, value := range []float64{1, 123.456, 9999.9} {
		var previous decimal.Price
		for n := int32(0); n < 5; n++ {
			ask, ok := s.NextAskPrice(value, n, 4)
			if !ok {
				continue
			}
			if n > 0 && ask.Cmp(previous) < 0 {
				t.Fatalf("value=%v n=%d ask=%v previous=%v", value, n, ask, previous)
			}
			previous = ask
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tick_scheme.rs:1432
//	test: prop_tiered_ticks_sorted
func TestPropTieredTicksSorted(t *testing.T) {
	for _, tier := range []PriceTier{{1, 1.1, .01}, {12.34, 22.34, 1}, {99, 198, 9.9}} {
		s, err := NewTieredTickScheme([]PriceTier{tier}, 2, 100)
		if err != nil {
			continue
		}
		ticks := s.Ticks()
		for i := 1; i < len(ticks); i++ {
			if ticks[i].Cmp(ticks[i-1]) <= 0 {
				t.Fatalf("ticks[%d]=%v <= %v", i, ticks[i], ticks[i-1])
			}
		}
	}
}
