package market

import "math"

// BlackScholesFloat32Greeks contains the raw analytical outputs of the
// float32 Black-Scholes kernel. Vega and theta are annualized raw values,
// unlike BlackScholesGreeksResult's scaled reporting values.
type BlackScholesFloat32Greeks struct {
	Price   float32
	Vol     float32
	Delta   float32
	Gamma   float32
	Vega    float32
	Theta   float32
	ITMProb float32
}

// ComputeBlackScholesGreeks32 evaluates the generalized Black-Scholes model
// using float32 inputs and outputs.
func ComputeBlackScholesGreeks32(
	s, k, t, r, b, vol float32,
	isCall bool,
) BlackScholesFloat32Greeks {
	sqrtT := float32(math.Sqrt(float64(t)))
	scaledVol := vol * sqrtT
	invScaledVol := 1 / scaledVol
	dfR := float32(math.Exp(float64(-r * t)))
	dfB := float32(math.Exp(float64((b - r) * t)))
	d1 := (float32(math.Log(float64(s/k))) + (b+0.5*vol*vol)*t) * invScaledVol
	d2 := d1 - scaledVol
	sForward := s * dfB
	phi := float32(-1)
	if isCall {
		phi = 1
	}

	return blackScholesPricingKernel32(
		sForward, k, dfR, d1, d2, invScaledVol, vol, sqrtT, t, r, b, s, phi,
	)
}

// ComputeBlackScholesIVAndGreeks32 performs one Halley refinement from
// initialGuess, then recomputes the Greeks at the refined volatility.
func ComputeBlackScholesIVAndGreeks32(
	marketPrice, s, k, t, r, b float32,
	isCall bool,
	initialGuess float32,
) BlackScholesFloat32Greeks {
	sqrtT := float32(math.Sqrt(float64(t)))
	invSqrtT := 1 / sqrtT
	lnSKBT := float32(math.Log(float64(s))) - float32(math.Log(float64(k))) + b*t
	halfT := float32(0.5) * t
	dfR := float32(math.Exp(float64(-r * t)))
	sForward := s * float32(math.Exp(float64((b-r)*t)))
	phi := float32(-1)
	if isCall {
		phi = 1
	}

	vol := initialGuess
	invVol := 1 / vol
	invScaledVol := invVol * invSqrtT
	d1 := (lnSKBT + halfT*vol*vol) * invScaledVol
	d2 := d1 - vol*sqrtT
	price, rawVega := blackScholesPriceVega32(sForward, k, dfR, d1, d2, sqrtT, phi)

	diff := price - marketPrice
	vega := max32(abs32(rawVega), 1e-9)
	volga := vega * d1 * d2 * invVol
	numerator := 2 * diff * vega
	denominator := 2*vega*vega - diff*volga
	safeDenominator := sign32(denominator) * max32(abs32(denominator), 1e-9)
	vol -= numerator / safeDenominator
	vol = min32(max32(vol, 1e-6), 10)

	invVol = 1 / vol
	invScaledVol = invVol * invSqrtT
	scaledVol := vol * sqrtT
	d1 = (lnSKBT + halfT*vol*vol) * invScaledVol
	d2 = d1 - scaledVol

	return blackScholesPricingKernel32(
		sForward, k, dfR, d1, d2, invScaledVol, vol, sqrtT, t, r, b, s, phi,
	)
}

func blackScholesPricingKernel32(
	sForward, k, dfR, d1, d2, invScaledVol, vol, sqrtT, t, r, b, s, phi float32,
) BlackScholesFloat32Greeks {
	cdfPhiD1 := normalCDF32(phi * d1)
	cdfPhiD2 := normalCDF32(phi * d2)
	pdfD1 := normalPDF32(d1)
	dfB := float32(math.Exp(float64((b - r) * t)))

	price := phi * (sForward*cdfPhiD1 - k*dfR*cdfPhiD2)
	delta := phi * dfB * cdfPhiD1
	vega := sForward * sqrtT * pdfD1
	gamma := dfB * pdfD1 * invScaledVol / s
	thetaV := -(sForward * pdfD1 * vol) / (2 * sqrtT)
	thetaB := -phi * (b - r) * sForward * cdfPhiD1
	thetaR := -phi * r * k * dfR * cdfPhiD2

	return BlackScholesFloat32Greeks{
		Price:   price,
		Vol:     vol,
		Delta:   delta,
		Gamma:   gamma,
		Vega:    vega,
		Theta:   thetaV + thetaB + thetaR,
		ITMProb: cdfPhiD2,
	}
}

func blackScholesPriceVega32(
	sForward, k, dfR, d1, d2, sqrtT, phi float32,
) (float32, float32) {
	cdfPhiD1 := normalCDF32(phi * d1)
	cdfPhiD2 := normalCDF32(phi * d2)
	price := phi * (sForward*cdfPhiD1 - k*dfR*cdfPhiD2)
	vega := sForward * sqrtT * normalPDF32(d1)
	return price, vega
}

func normalCDF32(value float32) float32 {
	return float32(normalCDF(float64(value)))
}

func normalPDF32(value float32) float32 {
	return float32(normalPDF(float64(value)))
}

func abs32(value float32) float32 {
	if value < 0 {
		return -value
	}
	return value
}

func sign32(value float32) float32 {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	default:
		return 0
	}
}

func max32(left, right float32) float32 {
	if left > right {
		return left
	}
	return right
}

func min32(left, right float32) float32 {
	if left < right {
		return left
	}
	return right
}
