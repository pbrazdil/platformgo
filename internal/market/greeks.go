package market

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// OptionGreekValues contains dimensionless analytical sensitivities. These
// values remain float64 because they are outputs of transcendental models, not
// ledger values. Prices, money, multipliers, and quantities below use exact
// decimal representations.
type OptionGreekValues struct {
	Delta float64
	Gamma float64
	Vega  float64
	Theta float64
	Rho   float64
}

func (g OptionGreekValues) Add(other OptionGreekValues) OptionGreekValues {
	return OptionGreekValues{
		Delta: g.Delta + other.Delta,
		Gamma: g.Gamma + other.Gamma,
		Vega:  g.Vega + other.Vega,
		Theta: g.Theta + other.Theta,
		Rho:   g.Rho + other.Rho,
	}
}

func (g OptionGreekValues) Scale(scalar float64) OptionGreekValues {
	return OptionGreekValues{
		Delta: g.Delta * scalar,
		Gamma: g.Gamma * scalar,
		Vega:  g.Vega * scalar,
		Theta: g.Theta * scalar,
		Rho:   g.Rho * scalar,
	}
}

func (g OptionGreekValues) String() string {
	return fmt.Sprintf(
		"OptionGreekValues(delta=%.4f, gamma=%.4f, vega=%.4f, theta=%.4f, rho=%.4f)",
		g.Delta,
		g.Gamma,
		g.Vega,
		g.Theta,
		g.Rho,
	)
}

// BlackScholesGreeksResult stores analytical prices as exact decimal snapshots.
// Conversion to float64 is confined to the model boundary in decimalFloat64.
type BlackScholesGreeksResult struct {
	Price   decimal.Decimal
	Vol     float64
	Delta   float64
	Gamma   float64
	Vega    float64
	Theta   float64
	ITMProb float64
}

// ImpliedVolAndGreeksResult is the source model's implied-volatility result.
type ImpliedVolAndGreeksResult struct {
	Vol   float64
	Price decimal.Decimal
	Delta float64
	Gamma float64
	Vega  float64
	Theta float64
}

// BlackScholesGreeks evaluates the generalized Black-Scholes model.
func BlackScholesGreeks(
	s, r, b, vol float64,
	isCall bool,
	k, t float64,
) BlackScholesGreeksResult {
	return blackScholesGreeks(s, r, b, vol, isCall, k, t)
}

// BlackScholesGreeksExact is the full-precision reference implementation.
func BlackScholesGreeksExact(
	s, r, b, vol float64,
	isCall bool,
	k, t float64,
) BlackScholesGreeksResult {
	return blackScholesGreeks(s, r, b, vol, isCall, k, t)
}

func blackScholesGreeks(
	s, r, b, vol float64,
	isCall bool,
	k, t float64,
) BlackScholesGreeksResult {
	phi := -1.0
	if isCall {
		phi = 1
	}

	sqrtT := math.Sqrt(t)
	d1 := (math.Log(s/k) + (b+vol*vol/2)*t) / (vol * sqrtT)
	d2 := d1 - vol*sqrtT
	dfB := math.Exp((b - r) * t)
	dfR := math.Exp(-r * t)
	cdfPhiD1 := normalCDF(phi * d1)
	cdfPhiD2 := normalCDF(phi * d2)
	pdfD1 := normalPDF(d1)

	price := phi * (s*dfB*cdfPhiD1 - k*dfR*cdfPhiD2)
	delta := phi * dfB * cdfPhiD1
	gamma := dfB * pdfD1 / (s * vol * sqrtT)
	vega := s * dfB * sqrtT * pdfD1 * 0.01
	thetaV := -(s * dfB * pdfD1 * vol) / (2 * sqrtT)
	thetaB := -(b - r) * s * dfB * phi * cdfPhiD1
	thetaR := -r * k * dfR * phi * cdfPhiD2

	return BlackScholesGreeksResult{
		Price:   decimalFromFloat64(price),
		Vol:     vol,
		Delta:   delta,
		Gamma:   gamma,
		Vega:    vega,
		Theta:   (thetaV + thetaB + thetaR) / 365.25,
		ITMProb: cdfPhiD2,
	}
}

// ImpliedVolatility recovers volatility by monotonic bisection. targetPrice is
// exact outside this analytical boundary.
func ImpliedVolatility(
	s, r, b float64,
	isCall bool,
	k, t float64,
	targetPrice decimal.Decimal,
) float64 {
	target := decimalFloat64(targetPrice)
	low, high := 1e-8, 5.0
	for decimalFloat64(BlackScholesGreeks(s, r, b, high, isCall, k, t).Price) < target && high < 100 {
		high *= 2
	}
	for range 160 {
		mid := (low + high) / 2
		price := decimalFloat64(BlackScholesGreeks(s, r, b, mid, isCall, k, t).Price)
		if price < target {
			low = mid
		} else {
			high = mid
		}
	}
	return (low + high) / 2
}

func ImpliedVolAndGreeks(
	s, r, b float64,
	isCall bool,
	k, t float64,
	targetPrice decimal.Decimal,
) ImpliedVolAndGreeksResult {
	vol := ImpliedVolatility(s, r, b, isCall, k, t, targetPrice)
	if vol < 1e-8 {
		vol = 1e-8
	}
	return impliedResult(BlackScholesGreeks(s, r, b, vol, isCall, k, t))
}

func RefineVolAndGreeks(
	s, r, b float64,
	isCall bool,
	k, t float64,
	targetPrice decimal.Decimal,
	_ float64,
) ImpliedVolAndGreeksResult {
	return ImpliedVolAndGreeks(s, r, b, isCall, k, t, targetPrice)
}

func impliedResult(result BlackScholesGreeksResult) ImpliedVolAndGreeksResult {
	return ImpliedVolAndGreeksResult{
		Vol:   result.Vol,
		Price: result.Price,
		Delta: result.Delta,
		Gamma: result.Gamma,
		Vega:  result.Vega,
		Theta: result.Theta,
	}
}

type GreeksData struct {
	TsInit          UnixNanos
	TsEvent         UnixNanos
	InstrumentID    InstrumentID
	IsCall          bool
	Strike          decimal.Price
	Expiry          int32
	ExpiryInDays    int32
	ExpiryInYears   float64
	Multiplier      decimal.Decimal
	Quantity        decimal.Quantity
	UnderlyingPrice decimal.Price
	InterestRate    float64
	CostOfCarry     float64
	Vol             float64
	PnL             decimal.Decimal
	Price           decimal.Decimal
	Greeks          OptionGreekValues
	ITMProb         float64
}

func NewGreeksData(
	tsInit, tsEvent UnixNanos,
	instrumentID InstrumentID,
	isCall bool,
	strike decimal.Price,
	expiry, expiryInDays int32,
	expiryInYears float64,
	multiplier decimal.Decimal,
	quantity decimal.Quantity,
	underlyingPrice decimal.Price,
	interestRate, costOfCarry, vol float64,
	pnl, price decimal.Decimal,
	greeks OptionGreekValues,
	itmProb float64,
) GreeksData {
	return GreeksData{
		TsInit: tsInit, TsEvent: tsEvent, InstrumentID: instrumentID, IsCall: isCall,
		Strike: strike, Expiry: expiry, ExpiryInDays: expiryInDays,
		ExpiryInYears: expiryInYears, Multiplier: multiplier, Quantity: quantity,
		UnderlyingPrice: underlyingPrice, InterestRate: interestRate,
		CostOfCarry: costOfCarry, Vol: vol, PnL: pnl, Price: price,
		Greeks: greeks, ITMProb: itmProb,
	}
}

func GreeksDataFromDelta(
	tsEvent UnixNanos,
	instrumentID InstrumentID,
	delta float64,
	multiplier decimal.Decimal,
) GreeksData {
	return GreeksData{
		TsInit: tsEvent, TsEvent: tsEvent, InstrumentID: instrumentID, IsCall: true,
		Strike: decimal.MustPrice("0"), Multiplier: multiplier,
		Quantity: decimal.MustQuantity("1"), UnderlyingPrice: decimal.MustPrice("0"),
		Greeks: OptionGreekValues{Delta: delta},
	}
}

func DefaultGreeksData() GreeksData {
	return GreeksData{
		InstrumentID:    InstrumentID("ES.GLBX"),
		IsCall:          true,
		Strike:          decimal.MustPrice("0"),
		Quantity:        decimal.MustQuantity("0"),
		UnderlyingPrice: decimal.MustPrice("0"),
	}
}

func (g GreeksData) Scale(scalar decimal.Decimal) GreeksData {
	scaled := g
	scaled.PnL = g.PnL.Mul(scalar)
	scaled.Price = g.Price.Mul(scalar)
	scaled.Greeks = g.Greeks.Scale(decimalFloat64(scalar))
	return scaled
}

func (g GreeksData) String() string {
	optionType := "PUT"
	if g.IsCall {
		optionType = "CALL"
	}
	return fmt.Sprintf(
		"GreeksData(type=%s, instrument_id=%s, expiry=%d, itm_prob=%.2f%%, vol=%.2f%%, pnl=%s, price=%s, delta=%.2f)",
		optionType, g.InstrumentID, g.Expiry, g.ITMProb*100, g.Vol*100,
		g.PnL.String(), g.Price.String(), g.Greeks.Delta,
	)
}

type PortfolioGreeks struct {
	TsInit  UnixNanos
	TsEvent UnixNanos
	PnL     decimal.Decimal
	Price   decimal.Decimal
	Greeks  OptionGreekValues
}

func NewPortfolioGreeks(
	tsInit, tsEvent UnixNanos,
	pnl, price decimal.Decimal,
	greeks OptionGreekValues,
) PortfolioGreeks {
	return PortfolioGreeks{TsInit: tsInit, TsEvent: tsEvent, PnL: pnl, Price: price, Greeks: greeks}
}

func DefaultPortfolioGreeks() PortfolioGreeks {
	return PortfolioGreeks{}
}

func PortfolioGreeksFromGreeksData(data GreeksData) PortfolioGreeks {
	return PortfolioGreeks{
		TsInit: data.TsInit, TsEvent: data.TsEvent,
		PnL: data.PnL, Price: data.Price, Greeks: data.Greeks,
	}
}

func (g PortfolioGreeks) Add(other PortfolioGreeks) PortfolioGreeks {
	return PortfolioGreeks{
		TsInit: g.TsInit, TsEvent: g.TsEvent,
		PnL: g.PnL.Add(other.PnL), Price: g.Price.Add(other.Price),
		Greeks: g.Greeks.Add(other.Greeks),
	}
}

func (g PortfolioGreeks) String() string {
	return fmt.Sprintf(
		"PortfolioGreeks(ts_init=%s, ts_event=%s, pnl=%s, price=%s, delta=%.2f, gamma=%.4f, vega=%.2f, theta=%.2f)",
		unixNanosString(g.TsInit), unixNanosString(g.TsEvent), g.PnL.String(),
		g.Price.String(), g.Greeks.Delta, g.Greeks.Gamma, g.Greeks.Vega, g.Greeks.Theta,
	)
}

type YieldCurveData struct {
	TsInit        UnixNanos
	TsEvent       UnixNanos
	CurveName     string
	Tenors        []float64
	InterestRates []float64
}

func NewYieldCurveData(
	tsInit, tsEvent UnixNanos,
	curveName string,
	tenors, interestRates []float64,
) YieldCurveData {
	return YieldCurveData{
		TsInit: tsInit, TsEvent: tsEvent, CurveName: curveName,
		Tenors:        append([]float64(nil), tenors...),
		InterestRates: append([]float64(nil), interestRates...),
	}
}

func DefaultYieldCurveData() YieldCurveData {
	return NewYieldCurveData(
		0, 0, "USD",
		[]float64{0.5, 1, 1.5, 2, 2.5},
		[]float64{0.04, 0.04, 0.04, 0.04, 0.04},
	)
}

func (y YieldCurveData) Clone() YieldCurveData {
	return NewYieldCurveData(y.TsInit, y.TsEvent, y.CurveName, y.Tenors, y.InterestRates)
}

func (y YieldCurveData) GetRate(expiry float64) float64 {
	if len(y.InterestRates) == 0 {
		return 0
	}
	if len(y.InterestRates) == 1 || len(y.Tenors) < 2 {
		return y.InterestRates[0]
	}
	if expiry <= y.Tenors[0] {
		return y.InterestRates[0]
	}
	last := min(len(y.Tenors), len(y.InterestRates)) - 1
	if expiry >= y.Tenors[last] {
		return y.InterestRates[last]
	}
	for i := 1; i <= last; i++ {
		if expiry <= y.Tenors[i] {
			fraction := (expiry - y.Tenors[i-1]) / (y.Tenors[i] - y.Tenors[i-1])
			return y.InterestRates[i-1] + fraction*(y.InterestRates[i]-y.InterestRates[i-1])
		}
	}
	return y.InterestRates[last]
}

func (y YieldCurveData) String() string {
	return fmt.Sprintf(
		"YieldCurveData(ts_init=%s, ts_event=%s, curve_name=%s, tenors=%v, interest_rates=%v)",
		unixNanosString(y.TsInit), unixNanosString(y.TsEvent),
		y.CurveName, y.Tenors, y.InterestRates,
	)
}

func normalCDF(value float64) float64 {
	return 0.5 * (1 + math.Erf(value/math.Sqrt2))
}

func normalPDF(value float64) float64 {
	return math.Exp(-value*value/2) / math.Sqrt(2*math.Pi)
}

func decimalFromFloat64(value float64) decimal.Decimal {
	return decimal.MustParse(strconv.FormatFloat(value, 'g', -1, 64))
}

func decimalFloat64(value decimal.Decimal) float64 {
	result, err := strconv.ParseFloat(value.String(), 64)
	if err != nil {
		panic(fmt.Sprintf("convert exact decimal %q at analytical boundary: %v", value, err))
	}
	return result
}

func unixNanosString(value UnixNanos) string {
	return time.Unix(0, int64(value)).UTC().Format(time.RFC3339Nano)
}
