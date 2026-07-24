package market

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

const greeksSourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

func testGreeksData() GreeksData {
	return NewGreeksData(
		1_000_000_000, 1_500_000_000,
		InstrumentID("SPY240315C00500000.OPRA"), true,
		decimal.MustPrice("500"), 20240315, 91, 0.25,
		decimal.MustParse("100"), decimal.MustQuantity("1"),
		decimal.MustPrice("520"), 0.05, 0.05, 0.20,
		decimal.MustParse("250"), decimal.MustParse("25.50"),
		OptionGreekValues{Delta: 0.65, Gamma: 0.003, Vega: 15.2, Theta: -0.08},
		0.75,
	)
}

func testPortfolioGreeks() PortfolioGreeks {
	return NewPortfolioGreeks(
		1_000_000_000, 1_500_000_000,
		decimal.MustParse("1500"), decimal.MustParse("125.5"),
		OptionGreekValues{Delta: 2.15, Gamma: 0.008, Vega: 42.7, Theta: -2.3},
	)
}

func testYieldCurveData() YieldCurveData {
	return NewYieldCurveData(
		1_000_000_000, 1_500_000_000, "USD",
		[]float64{0.25, 0.5, 1, 2, 5},
		[]float64{0.025, 0.03, 0.035, 0.04, 0.045},
	)
}

func requireDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	if got.Cmp(decimal.MustParse(want)) != 0 {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func requirePrice(t *testing.T, got decimal.Price, want string) {
	t.Helper()
	if got.Decimal().Cmp(decimal.MustParse(want)) != 0 {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func requireQuantity(t *testing.T, got decimal.Quantity, want string) {
	t.Helper()
	if got.Decimal().Cmp(decimal.MustParse(want)) != 0 {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func requireClose(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("got %.12g, want %.12g (tolerance %.3g)", got, want, tolerance)
	}
}

func requireFinite(t *testing.T, values ...float64) {
	t.Helper()
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("expected finite value, got %v", value)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:758
//	test: test_black_scholes_greeks_result_creation
func TestBlackScholesGreeksResultCreation(t *testing.T) {
	result := BlackScholesGreeksResult{
		Price: decimal.MustParse("25.5"), Delta: 0.65, Gamma: 0.003,
		Vega: 15.2, Theta: -0.08, ITMProb: 0.75,
	}
	requireDecimal(t, result.Price, "25.5")
	requireClose(t, result.Delta, 0.65, 0)
	requireClose(t, result.Gamma, 0.003, 0)
	requireClose(t, result.Vega, 15.2, 0)
	requireClose(t, result.Theta, -0.08, 0)
	requireClose(t, result.ITMProb, 0.75, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:778
//	test: test_black_scholes_greeks_result_clone_and_copy
func TestBlackScholesGreeksResultCloneAndCopy(t *testing.T) {
	original := BlackScholesGreeksResult{
		Price: decimal.MustParse("25.5"), Delta: 0.65, Gamma: 0.003,
		Vega: 15.2, Theta: -0.08, ITMProb: 0.75,
	}
	copied := original
	requireDecimal(t, copied.Price, "25.5")
	if copied != original {
		t.Fatalf("copied result differs: %#v != %#v", copied, original)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:796
//	test: test_black_scholes_greeks_result_debug
func TestBlackScholesGreeksResultDebug(t *testing.T) {
	result := BlackScholesGreeksResult{Price: decimal.MustParse("25.5"), Delta: 0.65}
	debug := fmt.Sprintf("%T%+v", result, result)
	for _, text := range []string{"BlackScholesGreeksResult", "25.5", "0.65"} {
		if !strings.Contains(debug, text) {
			t.Fatalf("debug output %q lacks %q", debug, text)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:814
//	test: test_imply_vol_and_greeks_result_creation
func TestImplyVolAndGreeksResultCreation(t *testing.T) {
	result := ImpliedVolAndGreeksResult{
		Vol: 0.2, Price: decimal.MustParse("25.5"),
		Delta: 0.65, Gamma: 0.003, Vega: 15.2, Theta: -0.08,
	}
	requireClose(t, result.Vol, 0.2, 0)
	requireDecimal(t, result.Price, "25.5")
	requireClose(t, result.Delta, 0.65, 0)
	requireClose(t, result.Gamma, 0.003, 0)
	requireClose(t, result.Vega, 15.2, 0)
	requireClose(t, result.Theta, -0.08, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:834
//	test: test_black_scholes_greeks_basic_call
func TestBlackScholesGreeksBasicCall(t *testing.T) {
	result := BlackScholesGreeks(100, 0.05, 0.05, 0.2, true, 100, 1)
	if decimalFloat64(result.Price) <= 0 || result.Delta <= 0 || result.Delta >= 1 ||
		result.Gamma <= 0 || result.Vega <= 0 || result.Theta >= 0 {
		t.Fatalf("unexpected call greeks: %+v", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:853
//	test: test_black_scholes_greeks_basic_put
func TestBlackScholesGreeksBasicPut(t *testing.T) {
	result := BlackScholesGreeks(100, 0.05, 0.05, 0.2, false, 100, 1)
	if decimalFloat64(result.Price) <= 0 || result.Delta <= -1 || result.Delta >= 0 ||
		result.Gamma <= 0 || result.Vega <= 0 || result.Theta >= 0 {
		t.Fatalf("unexpected put greeks: %+v", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:876
//	test: test_black_scholes_greeks_deep_itm_call
func TestBlackScholesGreeksDeepITMCall(t *testing.T) {
	result := BlackScholesGreeks(150, 0.05, 0.05, 0.2, true, 100, 1)
	if result.Delta <= 0.9 || result.Gamma <= 0 || result.Gamma >= 0.01 {
		t.Fatalf("unexpected deep ITM greeks: %+v", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:892
//	test: test_black_scholes_greeks_deep_otm_call
func TestBlackScholesGreeksDeepOTMCall(t *testing.T) {
	result := BlackScholesGreeks(50, 0.05, 0.05, 0.2, true, 100, 1)
	if result.Delta >= 0.1 || result.Gamma <= 0 || result.Gamma >= 0.01 {
		t.Fatalf("unexpected deep OTM greeks: %+v", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:908
//	test: test_black_scholes_greeks_zero_time
func TestBlackScholesGreeksZeroTime(t *testing.T) {
	result := BlackScholesGreeks(100, 0.05, 0.05, 0.2, true, 100, 0.0001)
	if decimalFloat64(result.Price) < 0 || math.IsNaN(result.Theta) || math.IsInf(result.Theta, 0) {
		t.Fatalf("unexpected near-expiry greeks: %+v", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:924
//	test: test_imply_vol_basic
func TestImplyVolBasic(t *testing.T) {
	target := BlackScholesGreeks(100, 0.05, 0.05, 0.2, true, 100, 1).Price
	requireClose(t, ImpliedVolatility(100, 0.05, 0.05, true, 100, 1, target), 0.2, 1e-4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:951
//	test: test_greeks_data_new
func TestGreeksDataNew(t *testing.T) {
	data := testGreeksData()
	if data.TsInit != 1_000_000_000 || data.TsEvent != 1_500_000_000 ||
		data.InstrumentID != "SPY240315C00500000.OPRA" || !data.IsCall ||
		data.Expiry != 20240315 || data.ExpiryInDays != 91 {
		t.Fatalf("unexpected identity fields: %+v", data)
	}
	requirePrice(t, data.Strike, "500")
	requireDecimal(t, data.Multiplier, "100")
	requireQuantity(t, data.Quantity, "1")
	requirePrice(t, data.UnderlyingPrice, "520")
	requireDecimal(t, data.PnL, "250")
	requireDecimal(t, data.Price, "25.5")
	requireClose(t, data.ExpiryInYears, 0.25, 0)
	requireClose(t, data.InterestRate, 0.05, 0)
	requireClose(t, data.CostOfCarry, 0.05, 0)
	requireClose(t, data.Vol, 0.2, 0)
	requireClose(t, data.Greeks.Delta, 0.65, 0)
	requireClose(t, data.ITMProb, 0.75, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:980
//	test: test_greeks_data_from_delta
func TestGreeksDataFromDelta(t *testing.T) {
	data := GreeksDataFromDelta(1_500_000_000, "SPY.OPRA", 0.65, decimal.MustParse("100"))
	if data.TsInit != data.TsEvent || data.TsEvent != 1_500_000_000 ||
		data.InstrumentID != "SPY.OPRA" || !data.IsCall {
		t.Fatalf("unexpected identity fields: %+v", data)
	}
	requirePrice(t, data.Strike, "0")
	requireDecimal(t, data.Multiplier, "100")
	requireQuantity(t, data.Quantity, "1")
	requirePrice(t, data.UnderlyingPrice, "0")
	requireDecimal(t, data.PnL, "0")
	requireDecimal(t, data.Price, "0")
	requireClose(t, data.Greeks.Delta, 0.65, 0)
	requireClose(t, data.Greeks.Gamma, 0, 0)
	requireClose(t, data.Greeks.Vega, 0, 0)
	requireClose(t, data.Greeks.Theta, 0, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1006
//	test: test_greeks_data_default
func TestGreeksDataDefault(t *testing.T) {
	data := DefaultGreeksData()
	if data.TsInit != 0 || data.TsEvent != 0 || data.InstrumentID != "ES.GLBX" || !data.IsCall {
		t.Fatalf("unexpected default identity fields: %+v", data)
	}
	requirePrice(t, data.Strike, "0")
	requireDecimal(t, data.Multiplier, "0")
	requireQuantity(t, data.Quantity, "0")
	requirePrice(t, data.UnderlyingPrice, "0")
	requireDecimal(t, data.PnL, "0")
	requireDecimal(t, data.Price, "0")
	requireClose(t, data.ExpiryInYears, 0, 0)
	requireClose(t, data.InterestRate, 0, 0)
	requireClose(t, data.CostOfCarry, 0, 0)
	requireClose(t, data.Vol, 0, 0)
	requireClose(t, data.ITMProb, 0, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1024
//	test: test_greeks_data_display
func TestGreeksDataDisplay(t *testing.T) {
	text := testGreeksData().String()
	for _, want := range []string{"GreeksData", "CALL", "SPY240315C00500000.OPRA", "20240315", "75.00%", "20.00%", "250", "25.50", "0.65"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q lacks %q", text, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1039
//	test: test_greeks_data_multiplication
func TestGreeksDataMultiplication(t *testing.T) {
	original := testGreeksData()
	scaled := original.Scale(decimal.MustParse("5"))
	if scaled.TsInit != original.TsInit || scaled.TsEvent != original.TsEvent ||
		scaled.InstrumentID != original.InstrumentID || scaled.IsCall != original.IsCall ||
		scaled.Expiry != original.Expiry || scaled.Quantity.String() != original.Quantity.String() {
		t.Fatalf("scaling changed metadata: %+v", scaled)
	}
	requireDecimal(t, scaled.PnL, "1250")
	requireDecimal(t, scaled.Price, "127.5")
	requireClose(t, scaled.Greeks.Delta, 3.25, 0)
	requireClose(t, scaled.Greeks.Gamma, 0.015, 1e-15)
	requireClose(t, scaled.Greeks.Vega, 76, 0)
	requireClose(t, scaled.Greeks.Theta, -0.4, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1065
//	test: test_greeks_data_has_ts_init
func TestGreeksDataHasTsInit(t *testing.T) {
	if testGreeksData().TsInit != 1_000_000_000 {
		t.Fatal("incorrect initialization timestamp")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1071
//	test: test_greeks_data_clone
func TestGreeksDataClone(t *testing.T) {
	original := testGreeksData()
	cloned := original
	if cloned.TsInit != original.TsInit || cloned.InstrumentID != original.InstrumentID ||
		cloned.Greeks != original.Greeks || cloned.PnL.Cmp(original.PnL) != 0 {
		t.Fatalf("clone differs: %+v != %+v", cloned, original)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1082
//	test: test_portfolio_greeks_new
func TestPortfolioGreeksNew(t *testing.T) {
	data := testPortfolioGreeks()
	if data.TsInit != 1_000_000_000 || data.TsEvent != 1_500_000_000 {
		t.Fatalf("unexpected timestamps: %+v", data)
	}
	requireDecimal(t, data.PnL, "1500")
	requireDecimal(t, data.Price, "125.5")
	requireClose(t, data.Greeks.Delta, 2.15, 0)
	requireClose(t, data.Greeks.Gamma, 0.008, 0)
	requireClose(t, data.Greeks.Vega, 42.7, 0)
	requireClose(t, data.Greeks.Theta, -2.3, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1096
//	test: test_portfolio_greeks_default
func TestPortfolioGreeksDefault(t *testing.T) {
	data := DefaultPortfolioGreeks()
	if data.TsInit != 0 || data.TsEvent != 0 || data.Greeks != (OptionGreekValues{}) {
		t.Fatalf("unexpected default: %+v", data)
	}
	requireDecimal(t, data.PnL, "0")
	requireDecimal(t, data.Price, "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1110
//	test: test_portfolio_greeks_display
func TestPortfolioGreeksDisplay(t *testing.T) {
	text := testPortfolioGreeks().String()
	for _, want := range []string{"PortfolioGreeks", "1970-01-01T00:00:01Z", "1500", "125.5", "2.15", "0.0080", "42.70", "-2.30"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q lacks %q", text, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1124
//	test: test_portfolio_greeks_addition
func TestPortfolioGreeksAddition(t *testing.T) {
	left := testPortfolioGreeks()
	right := NewPortfolioGreeks(
		2, 3, decimal.MustParse("500"), decimal.MustParse("24.5"),
		OptionGreekValues{Delta: 0.85, Gamma: 0.002, Vega: 7.3, Theta: -0.7},
	)
	sum := left.Add(right)
	if sum.TsInit != left.TsInit || sum.TsEvent != left.TsEvent {
		t.Fatalf("addition did not preserve left timestamps: %+v", sum)
	}
	requireDecimal(t, sum.PnL, "2000")
	requireDecimal(t, sum.Price, "150")
	requireClose(t, sum.Greeks.Delta, 3, 1e-15)
	requireClose(t, sum.Greeks.Gamma, 0.01, 1e-15)
	requireClose(t, sum.Greeks.Vega, 50, 1e-15)
	requireClose(t, sum.Greeks.Theta, -3, 1e-15)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1159
//	test: test_portfolio_greeks_from_greeks_data
func TestPortfolioGreeksFromGreeksData(t *testing.T) {
	source := testGreeksData()
	result := PortfolioGreeksFromGreeksData(source)
	if result.TsInit != source.TsInit || result.TsEvent != source.TsEvent || result.Greeks != source.Greeks {
		t.Fatalf("conversion differs: %+v", result)
	}
	requireDecimal(t, result.PnL, "250")
	requireDecimal(t, result.Price, "25.5")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1174
//	test: test_portfolio_greeks_has_ts_init
func TestPortfolioGreeksHasTsInit(t *testing.T) {
	if testPortfolioGreeks().TsInit != 1_000_000_000 {
		t.Fatal("incorrect initialization timestamp")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1180
//	test: test_yield_curve_data_new
func TestYieldCurveDataNew(t *testing.T) {
	data := testYieldCurveData()
	if data.TsInit != 1_000_000_000 || data.TsEvent != 1_500_000_000 ||
		data.CurveName != "USD" || len(data.Tenors) != 5 || len(data.InterestRates) != 5 {
		t.Fatalf("unexpected yield curve: %+v", data)
	}
	requireClose(t, data.Tenors[0], 0.25, 0)
	requireClose(t, data.InterestRates[4], 0.045, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1191
//	test: test_yield_curve_data_default
func TestYieldCurveDataDefault(t *testing.T) {
	data := DefaultYieldCurveData()
	if data.TsInit != 0 || data.TsEvent != 0 || data.CurveName != "USD" ||
		len(data.Tenors) != 5 || len(data.InterestRates) != 5 {
		t.Fatalf("unexpected default yield curve: %+v", data)
	}
	for i, want := range []float64{0.5, 1, 1.5, 2, 2.5} {
		requireClose(t, data.Tenors[i], want, 0)
		requireClose(t, data.InterestRates[i], 0.04, 0)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1202
//	test: test_yield_curve_data_get_rate_single_point
func TestYieldCurveDataGetRateSinglePoint(t *testing.T) {
	curve := NewYieldCurveData(0, 0, "USD", []float64{1}, []float64{0.05})
	for _, expiry := range []float64{0, 0.5, 1, 2, 10} {
		requireClose(t, curve.GetRate(expiry), 0.05, 0)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1217
//	test: test_yield_curve_data_get_rate_interpolation
func TestYieldCurveDataGetRateInterpolation(t *testing.T) {
	curve := testYieldCurveData()
	requireClose(t, curve.GetRate(0.25), 0.025, 0)
	requireClose(t, curve.GetRate(1), 0.035, 0)
	requireClose(t, curve.GetRate(5), 0.045, 0)
	interpolated := curve.GetRate(0.75)
	if interpolated <= 0.025 || interpolated >= 0.045 {
		t.Fatalf("interpolated rate out of bounds: %v", interpolated)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1231
//	test: test_yield_curve_data_display
func TestYieldCurveDataDisplay(t *testing.T) {
	text := testYieldCurveData().String()
	for _, want := range []string{"YieldCurveData", "USD"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q lacks %q", text, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1240
//	test: test_yield_curve_data_has_ts_init
func TestYieldCurveDataHasTsInit(t *testing.T) {
	if testYieldCurveData().TsInit != 1_000_000_000 {
		t.Fatal("incorrect initialization timestamp")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1246
//	test: test_yield_curve_data_clone
func TestYieldCurveDataClone(t *testing.T) {
	original := testYieldCurveData()
	cloned := original.Clone()
	cloned.Tenors[0] = 99
	cloned.InterestRates[0] = 99
	requireClose(t, original.Tenors[0], 0.25, 0)
	requireClose(t, original.InterestRates[0], 0.025, 0)
	if cloned.CurveName != original.CurveName || cloned.TsInit != original.TsInit {
		t.Fatalf("clone metadata differs: %+v", cloned)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1256
//	test: test_black_scholes_greeks_extreme_values
func TestBlackScholesGreeksExtremeValues(t *testing.T) {
	result := BlackScholesGreeks(1000, 0.1, 0.1, 5, true, 10, 0.1)
	price := decimalFloat64(result.Price)
	requireFinite(t, price, result.Delta, result.Gamma, result.Vega, result.Theta, result.ITMProb)
	if price <= 0 || result.Delta <= 0.99 {
		t.Fatalf("unexpected extreme greeks: %+v", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1277
//	test: test_black_scholes_greeks_high_volatility
func TestBlackScholesGreeksHighVolatility(t *testing.T) {
	result := BlackScholesGreeks(100, 0.05, 0.05, 2, true, 100, 1)
	price := decimalFloat64(result.Price)
	requireFinite(t, price, result.Delta, result.Gamma, result.Vega, result.Theta, result.ITMProb)
	if price <= 0 || result.Gamma <= 0 || result.Vega <= 0 {
		t.Fatalf("unexpected high-volatility greeks: %+v", result)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1297
//	test: test_greeks_data_put_option
func TestGreeksDataPutOption(t *testing.T) {
	data := testGreeksData()
	data.IsCall = false
	data.PnL = decimal.MustParse("-150")
	data.Greeks.Delta = -0.35
	if data.IsCall || data.Greeks.Delta >= 0 {
		t.Fatalf("unexpected put data: %+v", data)
	}
	requireDecimal(t, data.PnL, "-150")
}

func finiteDifferenceGreeks(s, r, b, vol float64, isCall bool, k, expiry, epsilon float64) (float64, float64, float64, float64) {
	price := func(spot, sigma, t float64) float64 {
		return decimalFloat64(BlackScholesGreeksExact(spot, r, b, sigma, isCall, k, t).Price)
	}
	base := price(s, vol, expiry)
	up := price(s+epsilon, vol, expiry)
	down := price(s-epsilon, vol, expiry)
	deltaValue := (up - down) / (2 * epsilon)
	gammaValue := (up - 2*base + down) / (epsilon * epsilon)
	vegaValue := (price(s, vol+epsilon, expiry) - price(s, vol-epsilon, expiry)) / (2 * epsilon) * 0.01
	thetaValue := (price(s, vol, expiry-1.0/365.25) - base)
	return deltaValue, gammaValue, vegaValue, thetaValue
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1332
//	test: test_greeks_accuracy_call
func TestGreeksAccuracyCall(t *testing.T) {
	result := BlackScholesGreeks(100, 0.01, 0.005, 0.2, true, 100.1, 1)
	deltaValue, gammaValue, vegaValue, thetaValue := finiteDifferenceGreeks(100, 0.01, 0.005, 0.2, true, 100.1, 1, 1e-3)
	requireClose(t, result.Delta, deltaValue, 0.005)
	requireClose(t, result.Gamma, gammaValue, 0.1)
	requireClose(t, result.Vega, vegaValue, 0.005)
	requireClose(t, result.Theta, thetaValue, 0.005)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1391
//	test: test_greeks_accuracy_put
func TestGreeksAccuracyPut(t *testing.T) {
	result := BlackScholesGreeks(100, 0.01, 0.005, 0.2, false, 100.1, 1)
	deltaValue, gammaValue, vegaValue, thetaValue := finiteDifferenceGreeks(100, 0.01, 0.005, 0.2, false, 100.1, 1, 1e-3)
	requireClose(t, result.Delta, deltaValue, 0.005)
	requireClose(t, result.Gamma, gammaValue, 0.1)
	requireClose(t, result.Vega, vegaValue, 0.005)
	requireClose(t, result.Theta, thetaValue, 0.005)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1450
//	test: test_imply_vol_and_greeks_accuracy_call
func TestImplyVolAndGreeksAccuracyCall(t *testing.T) {
	base := BlackScholesGreeks(100, 0.01, 0.005, 0.2, true, 100.1, 1)
	result := ImpliedVolAndGreeks(100, 0.01, 0.005, true, 100.1, 1, base.Price)
	requireClose(t, result.Vol, 0.2, 2e-4)
	requireClose(t, decimalFloat64(result.Price), decimalFloat64(base.Price), 2e-4)
	requireClose(t, result.Delta, base.Delta, 2e-4)
	requireClose(t, result.Gamma, base.Gamma, 2e-4)
	requireClose(t, result.Vega, base.Vega, 2e-4)
	requireClose(t, result.Theta, base.Theta, 2e-4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1505
//	test: test_black_scholes_greeks_target_price_refinement
func TestBlackScholesGreeksTargetPriceRefinement(t *testing.T) {
	target := BlackScholesGreeks(100, 0.05, 0.05, 0.2, true, 100, 1).Price
	result := RefineVolAndGreeks(100, 0.05, 0.05, true, 100, 1, target, 0.22)
	requireClose(t, decimalFloat64(result.Price), decimalFloat64(target), 0.01)
	if result.Vol < 0.18 || result.Vol > 0.22 {
		t.Fatalf("refined volatility out of bounds: %v", result.Vol)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1544
//	test: test_black_scholes_greeks_target_price_refinement_put
func TestBlackScholesGreeksTargetPriceRefinementPut(t *testing.T) {
	target := BlackScholesGreeks(100, 0.05, 0.05, 0.25, false, 105, 0.5).Price
	result := RefineVolAndGreeks(100, 0.05, 0.05, false, 105, 0.5, target, 0.2)
	requireClose(t, decimalFloat64(result.Price), decimalFloat64(target), 0.01)
	if result.Vol < 0.2 || result.Vol > 0.275 {
		t.Fatalf("refined volatility out of bounds: %v", result.Vol)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1583
//	test: test_imply_vol_and_greeks_accuracy_put
func TestImplyVolAndGreeksAccuracyPut(t *testing.T) {
	base := BlackScholesGreeks(100, 0.01, 0.005, 0.2, false, 100.1, 1)
	result := ImpliedVolAndGreeks(100, 0.01, 0.005, false, 100.1, 1, base.Price)
	requireClose(t, result.Vol, 0.2, 2e-4)
	requireClose(t, decimalFloat64(result.Price), decimalFloat64(base.Price), 2e-4)
	requireClose(t, result.Delta, base.Delta, 2e-4)
	requireClose(t, result.Gamma, base.Gamma, 2e-4)
	requireClose(t, result.Vega, base.Vega, 2e-4)
	requireClose(t, result.Theta, base.Theta, 2e-4)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1640
//	test: test_black_scholes_greeks_vs_exact
func TestBlackScholesGreeksVsExact(t *testing.T) {
	for _, spot := range []float64{90, 100, 110} {
		for _, isCall := range []bool{true, false} {
			for _, vol := range []float64{0.15, 0.25, 0.5} {
				for _, expiry := range []float64{0.01, 0.25, 2} {
					got := BlackScholesGreeks(spot, 0.05, 0.05, vol, isCall, 100, expiry)
					want := BlackScholesGreeksExact(spot, 0.05, 0.05, vol, isCall, 100, expiry)
					requireClose(t, decimalFloat64(got.Price), decimalFloat64(want.Price), 1e-12)
					requireClose(t, got.Delta, want.Delta, 1e-12)
					requireClose(t, got.Gamma, want.Gamma, 1e-12)
					requireClose(t, got.Vega, want.Vega, 1e-12)
					requireClose(t, got.Theta, want.Theta, 1e-12)
					requireClose(t, got.ITMProb, want.ITMProb, 1e-12)
				}
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/greeks.rs:1704
//	test: test_refine_vol_and_greeks_vs_imply_vol_and_greeks
func TestRefineVolAndGreeksVsImplyVolAndGreeks(t *testing.T) {
	for _, spot := range []float64{90, 100, 110} {
		for _, isCall := range []bool{true, false} {
			for _, targetVol := range []float64{0.15, 0.25, 0.5} {
				for _, expiry := range []float64{0.01, 0.25, 2} {
					price := BlackScholesGreeks(spot, 0.05, 0.05, targetVol, isCall, 100, expiry).Price
					refined := RefineVolAndGreeks(spot, 0.05, 0.05, isCall, 100, expiry, price, targetVol-0.01)
					implied := ImpliedVolAndGreeks(spot, 0.05, 0.05, isCall, 100, expiry, price)

					deepEdge := expiry < 0.1 && math.Abs((spot-100)/100) > 0.05
					volTolerance := 0.001
					switch {
					case deepEdge:
						volTolerance = 2
					case expiry < 0.1:
						volTolerance = 0.10
					case expiry > 1.5 && targetVol <= 0.15:
						volTolerance = 0.05
					case expiry > 1.5:
						volTolerance = 0.01
					case targetVol <= 0.15:
						volTolerance = 0.05
					}
					if math.Abs(refined.Vol-targetVol)/targetVol >= volTolerance {
						t.Fatalf("refined vol mismatch: spot=%v call=%v target=%v expiry=%v got=%v", spot, isCall, targetVol, expiry, refined.Vol)
					}
					if math.Abs(implied.Vol-targetVol)/targetVol >= volTolerance {
						t.Fatalf("implied vol mismatch: spot=%v call=%v target=%v expiry=%v got=%v", spot, isCall, targetVol, expiry, implied.Vol)
					}
					for name, pair := range map[string][2]float64{
						"price": {decimalFloat64(refined.Price), decimalFloat64(implied.Price)},
						"delta": {refined.Delta, implied.Delta},
						"gamma": {refined.Gamma, implied.Gamma},
						"vega":  {refined.Vega, implied.Vega},
						"theta": {refined.Theta, implied.Theta},
					} {
						if pair[0] != pair[1] {
							t.Fatalf("%s differs: refined=%v implied=%v", name, pair[0], pair[1])
						}
					}
				}
			}
		}
	}
}
