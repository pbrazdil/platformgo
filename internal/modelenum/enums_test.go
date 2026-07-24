package modelenum

import (
	"encoding/json"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2073
//	test: test_aggressor_side_from_u8
func TestAggressorSideFromUint8(t *testing.T) {
	for _, test := range []struct {
		value uint8
		want  AggressorSide
		ok    bool
	}{{0, NoAggressor, true}, {1, Buyer, true}, {2, Seller, true}, {3, 0, false}, {255, 0, false}} {
		got, ok := AggressorSideFromUint8(test.value)
		if got != test.want || ok != test.ok {
			t.Errorf("%d = %v,%v", test.value, got, ok)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2080
//	test: test_greeks_convention_serde_roundtrip
func TestGreeksConventionJSONRoundTrip(t *testing.T) {
	for _, convention := range []GreeksConvention{BlackScholes, PriceAdjusted} {
		data, err := json.Marshal(convention)
		if err != nil {
			t.Fatal(err)
		}
		var restored GreeksConvention
		if err := json.Unmarshal(data, &restored); err != nil || restored != convention {
			t.Fatalf("%s -> %s -> %s (%v)", convention, data, restored, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2091
//	test: test_greeks_convention_default_is_black_scholes
func TestGreeksConventionDefaultIsBlackScholes(t *testing.T) {
	if DefaultGreeksConvention() != BlackScholes {
		t.Fatal("default Greeks convention is not Black-Scholes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2100
//	test: test_continuous_future_adjustment_type_predicates
func TestContinuousFutureAdjustmentTypePredicates(t *testing.T) {
	for _, test := range []struct {
		mode            ContinuousFutureAdjustmentType
		ratio, backward bool
	}{{BackwardSpread, false, true}, {ForwardSpread, false, false},
		{BackwardRatio, true, true}, {ForwardRatio, true, false}} {
		if test.mode.IsRatio() != test.ratio || test.mode.IsBackward() != test.backward {
			t.Errorf("%s predicates = %v,%v", test.mode, test.mode.IsRatio(), test.mode.IsBackward())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2114
//	test: test_continuous_future_adjustment_type_serde_roundtrip
func TestContinuousFutureAdjustmentTypeJSONRoundTrip(t *testing.T) {
	for _, mode := range []ContinuousFutureAdjustmentType{
		BackwardSpread, ForwardSpread, BackwardRatio, ForwardRatio,
	} {
		data, err := json.Marshal(mode)
		if err != nil {
			t.Fatal(err)
		}
		var restored ContinuousFutureAdjustmentType
		if err := json.Unmarshal(data, &restored); err != nil || restored != mode {
			t.Fatalf("%s -> %s -> %s (%v)", mode, data, restored, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2125
//	test: test_continuous_future_adjustment_type_default_is_backward_spread
func TestContinuousFutureAdjustmentTypeDefaultIsBackwardSpread(t *testing.T) {
	if DefaultContinuousFutureAdjustmentType() != BackwardSpread {
		t.Fatal("default adjustment is not backward spread")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2145
//	test: test_instrument_class_allows_negative_price
func TestInstrumentClassAllowsNegativePrice(t *testing.T) {
	for _, test := range []struct {
		class InstrumentClass
		want  bool
	}{{Option, true}, {FuturesSpread, true}, {OptionSpread, true}, {Spot, false},
		{Swap, false}, {Future, false}, {Forward, false}, {CFD, false}, {Bond, false},
		{Warrant, false}, {SportsBetting, false}, {BinaryOption, false}} {
		if test.class.AllowsNegativePrice() != test.want {
			t.Errorf("%s allows negative = %v", test.class, test.class.AllowsNegativePrice())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2164
//	test: test_instrument_class_try_from_parent_suffix
func TestInstrumentClassTryFromParentSuffix(t *testing.T) {
	for _, test := range []struct {
		suffix string
		want   InstrumentClass
		ok     bool
	}{{"FUT", Future, true}, {"FUTURE", Future, true}, {"OPT", Option, true},
		{"OPTION", Option, true}, {"fut", "", false}, {"Fut", "", false},
		{"option", "", false}, {"Option", "", false}, {"SPREAD", "", false},
		{"UNKNOWN", "", false}, {"", "", false}} {
		got, ok := InstrumentClassFromParentSuffix(test.suffix)
		if got != test.want || ok != test.ok {
			t.Errorf("%q = %q,%v", test.suffix, got, ok)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2184
//	test: test_instrument_class_parent_suffix
func TestInstrumentClassParentSuffix(t *testing.T) {
	for _, test := range []struct {
		class InstrumentClass
		want  string
		ok    bool
	}{{Future, "FUT", true}, {Option, "OPT", true}, {Spot, "", false}, {Swap, "", false},
		{FuturesSpread, "", false}, {Forward, "", false}, {CFD, "", false}, {Bond, "", false},
		{OptionSpread, "", false}, {Warrant, "", false}, {SportsBetting, "", false},
		{BinaryOption, "", false}} {
		got, ok := test.class.ParentSuffix()
		if got != test.want || ok != test.ok {
			t.Errorf("%s suffix = %q,%v", test.class, got, ok)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/enums.rs:2194
//	test: test_instrument_class_parent_suffix_roundtrip
func TestInstrumentClassParentSuffixRoundTrip(t *testing.T) {
	for _, class := range []InstrumentClass{Future, Option} {
		suffix, ok := class.ParentSuffix()
		if !ok {
			t.Fatalf("%s has no suffix", class)
		}
		restored, ok := InstrumentClassFromParentSuffix(suffix)
		if !ok || restored != class {
			t.Fatalf("%s -> %s -> %s", class, suffix, restored)
		}
	}
}
