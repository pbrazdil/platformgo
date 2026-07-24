package market

import (
	"math"
	"testing"
)

const blackScholesSourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

func requireBlackScholesClose(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) >= tolerance {
		t.Fatalf("got %.12g, want %.12g (tolerance %.3g)", got, want, tolerance)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/black_scholes.rs:378
//	test: test_accuracy_1e7
func TestBlackScholesAccuracy1e7(t *testing.T) {
	const s, k, expiry, rate, vol = float32(100), float32(100), float32(1), float32(0.05), float32(0.2)
	greeks := ComputeBlackScholesGreeks32(s, k, expiry, rate, rate, vol, true)
	requireBlackScholesClose(t, float64(greeks.Price), 10.45058, 1e-5)

	solved := ComputeBlackScholesIVAndGreeks32(greeks.Price, s, k, expiry, rate, rate, true, vol)
	requireBlackScholesClose(t, float64(solved.Vol), float64(vol), 1e-6)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/black_scholes.rs:391
//	test: test_compute_greeks_accuracy_vs_exact
func TestComputeBlackScholesGreeks32AccuracyVsExact(t *testing.T) {
	const s, k, expiry, rate, carry, vol = 100.0, 100.0, 1.0, 0.05, 0.05, 0.2
	fast := ComputeBlackScholesGreeks32(s, k, expiry, rate, carry, vol, true)
	exact := BlackScholesGreeksExact(s, rate, carry, vol, true, k, expiry)

	requireBlackScholesClose(t, float64(fast.Price), decimalFloat64(exact.Price), 1e-4)
	requireBlackScholesClose(t, float64(fast.Delta), exact.Delta, 1e-3)
	requireBlackScholesClose(t, float64(fast.Gamma), exact.Gamma, 1e-3)
	requireBlackScholesClose(t, float64(fast.Vega), exact.Vega/0.01, 1e-3)
	requireBlackScholesClose(t, float64(fast.Theta), exact.Theta/0.002_737_850_787_132_101_3, 1e-3)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/black_scholes.rs:452
//	test: test_put_theta_with_cost_of_carry_not_equal_to_rate
func TestBlackScholesPutThetaWithCarryDifferentFromRate(t *testing.T) {
	const s, k, expiry, rate, carry, vol = 100.0, 100.0, 1.0, 0.05, 0.0, 0.2
	fast := ComputeBlackScholesGreeks32(s, k, expiry, rate, carry, vol, false)
	exact := BlackScholesGreeksExact(s, rate, carry, vol, false, k, expiry)
	requireBlackScholesClose(t, float64(fast.Theta), exact.Theta/0.002_737_850_787_132_101_3, 1e-3)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/black_scholes.rs:478
//	test: test_compute_iv_and_greeks_halley_accuracy
func TestComputeBlackScholesIVAndGreeks32HalleyAccuracy(t *testing.T) {
	const s, k, expiry, rate, carry, trueVol, initialGuess = 100.0, 100.0, 1.0, 0.05, 0.05, 0.2, 0.25
	exact := BlackScholesGreeksExact(s, rate, carry, trueVol, true, k, expiry)
	halley := ComputeBlackScholesIVAndGreeks32(
		float32(decimalFloat64(exact.Price)), s, k, expiry, rate, carry, true, initialGuess,
	)

	requireBlackScholesClose(t, float64(halley.Vol), trueVol, 0.01)
	requireBlackScholesClose(t, float64(halley.Price), decimalFloat64(exact.Price), 5e-3)
	requireBlackScholesClose(t, float64(halley.Delta), exact.Delta, 5e-3)
	requireBlackScholesClose(t, float64(halley.Gamma), exact.Gamma, 5e-3)
	requireBlackScholesClose(t, float64(halley.Vega), exact.Vega/0.01, 5e-3)
	requireBlackScholesClose(t, float64(halley.Theta), exact.Theta/0.002_737_850_787_132_101_3, 5e-3)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/black_scholes.rs:560
//	test: test_print_halley_iv
func TestPrintBlackScholesHalleyIV(t *testing.T) {
	const s, k, expiry, rate, carry, trueVol = 100.0, 100.0, 1.0, 0.05, 0.05, 0.2
	exact := BlackScholesGreeksExact(s, rate, carry, trueVol, true, k, expiry)
	halley := ComputeBlackScholesIVAndGreeks32(
		float32(decimalFloat64(exact.Price)), s, k, expiry, rate, carry, true, trueVol,
	)
	absoluteError := math.Abs(float64(halley.Vol) - trueVol)
	t.Logf(
		"true volatility: %.8f; market price: %.8f; computed volatility: %.8f; absolute error: %.8f; relative error: %.4f%%",
		trueVol,
		decimalFloat64(exact.Price),
		halley.Vol,
		absoluteError,
		absoluteError/trueVol*100,
	)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/black_scholes.rs:601
//	test: test_compute_iv_and_greeks_deep_itm_otm
func TestComputeBlackScholesIVAndGreeks32DeepITMOTM(t *testing.T) {
	const expiry, rate, carry, trueVol = 1.0, 0.05, 0.05, 0.2

	itmExact := BlackScholesGreeksExact(150, rate, carry, trueVol, true, 100, expiry)
	itm := ComputeBlackScholesIVAndGreeks32(
		float32(decimalFloat64(itmExact.Price)), 150, 100, expiry, rate, carry, true, trueVol,
	)
	otmExact := BlackScholesGreeksExact(50, rate, carry, trueVol, true, 100, expiry)
	otm := ComputeBlackScholesIVAndGreeks32(
		float32(decimalFloat64(otmExact.Price)), 50, 100, expiry, rate, carry, false, trueVol,
	)

	for name, result := range map[string]BlackScholesFloat32Greeks{"deep ITM": itm, "deep OTM": otm} {
		if math.IsNaN(float64(result.Vol)) || math.IsInf(float64(result.Vol), 0) ||
			result.Vol <= 0 || result.Vol >= 2 {
			t.Fatalf("%s volatility recovery returned invalid result %v", name, result.Vol)
		}
	}

	itmRelativeError := math.Abs(float64(itm.Vol)-trueVol) / trueVol * 100
	otmRelativeError := math.Abs(float64(otm.Vol)-trueVol) / trueVol * 100
	if itmRelativeError >= 50 {
		t.Fatalf("deep ITM volatility error %.4f%% exceeds 50%%", itmRelativeError)
	}
	if otmRelativeError >= 150 {
		t.Fatalf("deep OTM volatility error %.4f%% exceeds 150%%", otmRelativeError)
	}
	t.Logf("deep ITM error %.2f%%; deep OTM error %.2f%%", itmRelativeError, otmRelativeError)
}
