package instrument

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/money"
)

func modelCurrency(code string, precision uint8) currency.Currency {
	return currency.MustNew(code, precision, 0, code, currency.Crypto)
}

func linearModel() Model {
	base, quote := currency.BTC(), currency.USDT()
	return Model{
		PricePrecision: 2, SizePrecision: 6,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.000001"),
		Multiplier: decimal.MustQuantity("1"), BaseCurrency: &base,
		QuoteCurrency: quote, SettlementCurrency: quote,
	}
}

func ethModel() Model {
	model := linearModel()
	model.SizePrecision = 5
	model.SizeIncrement = decimal.MustQuantity("0.00001")
	return model
}

func boundedModel() Model {
	model := linearModel()
	minimum, maximum := decimal.MustPrice("0.01"), decimal.MustPrice("1000000.00")
	model.MinPrice, model.MaxPrice = &minimum, &maximum
	return model
}

func quantoModel() Model {
	base, quote, settlement := modelCurrency("ETH", 8), currency.BTC(), currency.USDT()
	return Model{
		PricePrecision: 3, SizePrecision: 0,
		PriceIncrement: decimal.MustPrice("0.001"), SizeIncrement: decimal.MustQuantity("1"),
		Multiplier: decimal.MustQuantity("1"), BaseCurrency: &base,
		QuoteCurrency: quote, SettlementCurrency: settlement,
	}
}

func inverseModel() Model {
	base, quote := currency.BTC(), currency.USD()
	return Model{
		PricePrecision: 1, SizePrecision: 0,
		PriceIncrement: decimal.MustPrice("0.1"), SizeIncrement: decimal.MustQuantity("1"),
		Multiplier: decimal.MustQuantity("1"), BaseCurrency: &base,
		QuoteCurrency: quote, SettlementCurrency: base, Inverse: true,
	}
}

func modelPrice(t *testing.T, text string, precision uint8) decimal.Price {
	t.Helper()
	price, err := decimal.NewPrice(text, precision)
	if err != nil {
		t.Fatal(err)
	}
	return price
}

func requireModelPrice(t *testing.T, got decimal.Price, ok bool, text string, precision uint8) {
	t.Helper()
	if !ok || !got.Equal(modelPrice(t, text, precision)) || got.Precision() != precision {
		t.Fatalf("got (%v,%v), want %s precision %d", got, ok, text, precision)
	}
}

func requireValidation(t *testing.T, err error, kind, field, contains string) {
	t.Helper()
	var typed *InstrumentValidationError
	if !errors.As(err, &typed) || typed.Kind != kind || typed.Field != field ||
		!strings.Contains(err.Error(), contains) {
		t.Fatalf("error=%#v, want kind=%q field=%q containing %q", err, kind, field, contains)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:740
//	test: default_increment_precision
func TestModelDefaultIncrementPrecision(t *testing.T) {
	increment, err := DefaultPriceIncrement(2)
	requireModelPrice(t, increment, err == nil, "0.01", 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:756
//	test: test_min_increment_precision
func TestModelMinIncrementPrecision(t *testing.T) {
	for _, tc := range []struct {
		text string
		want uint8
	}{{"0.5", 1}, {"0.50", 1}, {"0.500", 1}, {"0.01", 2}, {"0.010", 2},
		{"0.25", 2}, {"1.0", 1}, {"1.00", 2}, {"100", 0}, {"0.001", 3}} {
		if got := minIncrementPrecision(tc.text); got != tc.want {
			t.Fatalf("%s: got %d want %d", tc.text, got, tc.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:769
//	test: make_qty_rounding
func TestModelMakeQtyRounding(t *testing.T) {
	model := linearModel()
	for _, tc := range []struct {
		input float64
		want  string
	}{{1.5, "1.500000"}, {2.5, "2.500000"}, {1.2345678, "1.234568"},
		{.000123, "0.000123"}, {99999.999999, "99999.999999"}} {
		if got := model.MakeQuantity(tc.input, false).String(); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.input, got, tc.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:785
//	test: make_qty_round_down
func TestModelMakeQtyRoundDown(t *testing.T) {
	model := linearModel()
	for _, tc := range []struct {
		input float64
		want  string
	}{{1.2345678, "1.234567"}, {1.9999999, "1.999999"}, {.00012345, "0.000123"}, {10.9999999, "10.999999"}} {
		if got := model.MakeQuantity(tc.input, true).String(); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.input, got, tc.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:802
//	test: make_qty_precision
func TestModelMakeQtyPrecision(t *testing.T) {
	model := ethModel()
	for _, tc := range []struct {
		input float64
		want  string
	}{{1.2345678, "1.23457"}, {2.3456781, "2.34568"}, {.00001, "0.00001"}} {
		if got := model.MakeQuantity(tc.input, false).String(); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.input, got, tc.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:816
//	test: make_qty_half_even
func TestModelMakeQtyHalfEven(t *testing.T) {
	model := linearModel()
	for _, tc := range []struct {
		input float64
		want  string
	}{{1.2345675, "1.234568"}, {1.2345665, "1.234566"}} {
		if got := model.MakeQuantity(tc.input, false).String(); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.input, got, tc.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:830
//	test: try_normalize_price_rewrites_grid_aligned_values
func TestModelTryNormalizePriceRewritesGridAlignedValues(t *testing.T) {
	model := linearModel()
	for _, text := range []string{"10000", "10000.0000"} {
		got, err := model.TryNormalizePrice(decimal.MustPrice(text))
		if err != nil || got.String() != "10000.00" {
			t.Fatalf("%s: got=%v err=%v", text, got, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:846
//	test: try_normalize_price_rejects_sub_precision_value
func TestModelTryNormalizePriceRejectsSubPrecisionValue(t *testing.T) {
	_, err := linearModel().TryNormalizePrice(decimal.MustPrice("10000.001"))
	requireValidation(t, err, "predicate_violation", "price", "requires rounding")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:862
//	test: try_normalize_price_rejects_sentinel_values
func TestModelTryNormalizePriceRejectsSentinelValues(t *testing.T) {
	for _, price := range []decimal.Price{decimal.UndefinedPrice(), decimal.RawErrorPrice(), decimal.ErrorPrice()} {
		_, err := linearModel().TryNormalizePrice(price)
		requireValidation(t, err, "invalid_value", "price", price.String())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:888
//	test: try_normalize_price_handles_negative_values
func TestModelTryNormalizePriceHandlesNegativeValues(t *testing.T) {
	got, err := linearModel().TryNormalizePrice(decimal.MustPrice("-10000"))
	if err != nil || got.String() != "-10000.00" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if _, err := linearModel().TryNormalizePrice(decimal.MustPrice("-10000.001")); err == nil {
		t.Fatal("sub-precision negative price accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:899
//	test: try_normalize_price_rejects_sub_increment_value
func TestModelTryNormalizePriceRejectsSubIncrementValue(t *testing.T) {
	model := linearModel()
	model.PriceIncrement = modelPrice(t, ".50", 2)
	if got, err := model.TryNormalizePrice(decimal.MustPrice("1.500")); err != nil || got.String() != "1.50" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	_, err := model.TryNormalizePrice(decimal.MustPrice("1.20"))
	requireValidation(t, err, "predicate_violation", "price", "not aligned")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:945
//	test: try_normalize_qty_rewrites_grid_aligned_values
func TestModelTryNormalizeQtyRewritesGridAlignedValues(t *testing.T) {
	model := linearModel()
	for _, text := range []string{"1", "1.0000000"} {
		got, err := model.TryNormalizeQuantity(decimal.MustQuantity(text))
		if err != nil || got.String() != "1.000000" {
			t.Fatalf("%s: got=%v err=%v", text, got, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:958
//	test: try_normalize_qty_rejects_sub_precision_value
func TestModelTryNormalizeQtyRejectsSubPrecisionValue(t *testing.T) {
	_, err := linearModel().TryNormalizeQuantity(decimal.MustQuantity("1.0000001"))
	requireValidation(t, err, "predicate_violation", "quantity", "requires rounding")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:971
//	test: try_normalize_qty_rejects_undefined_value
func TestModelTryNormalizeQtyRejectsUndefinedValue(t *testing.T) {
	_, err := linearModel().TryNormalizeQuantity(decimal.UndefinedQuantity())
	requireValidation(t, err, "invalid_value", "quantity", "QUANTITY_UNDEF")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:992
//	test: try_normalize_values_reject_mixed_raw_scales
func TestModelTryNormalizeValuesRejectMixedRawScales(t *testing.T) {
	model := linearModel()
	model.PricePrecision, model.SizePrecision = 1, 1
	if _, err := model.TryNormalizePrice(decimal.MustPrice("100.01")); err == nil {
		t.Fatal("mixed price scale accepted")
	}
	if _, err := model.TryNormalizeQuantity(decimal.MustQuantity("100.01")); err == nil {
		t.Fatal("mixed quantity scale accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1049
//	test: try_normalize_qty_rejects_sub_increment_value
func TestModelTryNormalizeQtyRejectsSubIncrementValue(t *testing.T) {
	model := linearModel()
	model.SizePrecision, model.SizeIncrement = 2, decimal.MustQuantity("0.50")
	if got, err := model.TryNormalizeQuantity(decimal.MustQuantity("1.500")); err != nil || got.String() != "1.50" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	_, err := model.TryNormalizeQuantity(decimal.MustQuantity("1.20"))
	requireValidation(t, err, "predicate_violation", "quantity", "not aligned")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1094
//	test: make_qty_rounds_to_zero
func TestModelMakeQtyRoundsToZero(t *testing.T) {
	if _, err := linearModel().TryMakeQuantity(1e-12, false); err == nil || !strings.Contains(err.Error(), "rounded to zero") {
		t.Fatalf("err=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1099
//	test: notional_linear
func TestModelNotionalLinear(t *testing.T) {
	model := linearModel()
	got := model.CalculateNotionalValue(model.MakeQuantity(2, false), model.MakePrice(10000), false)
	want := money.MustNew("20000", model.QuoteCurrency)
	if !got.Equal(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1108
//	test: currency_pair_is_not_quanto
func TestModelCurrencyPairIsNotQuanto(t *testing.T) {
	model := linearModel()
	if model.IsQuanto() || model.CostCurrency().Code != "USDT" {
		t.Fatalf("quanto=%v cost=%v", model.IsQuanto(), model.CostCurrency())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1114
//	test: tick_navigation
func TestModelTickNavigation(t *testing.T) {
	model := linearModel()
	bid0, _ := model.NextBidPrice(10000.1234, 0)
	bid1, _ := model.NextBidPrice(10000.1234, 1)
	asks := model.NextAskPrices(10000.1234, 3)
	if bid1.Cmp(bid0) >= 0 || len(asks) != 3 || asks[0].Cmp(bid0) <= 0 {
		t.Fatalf("bid0=%v bid1=%v asks=%v", bid0, bid1, asks)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1125
//	test: tick_navigation_uses_tick_scheme
func TestModelTickNavigationUsesTickScheme(t *testing.T) {
	model := linearModel()
	model.TickScheme = "FIXED_PRECISION_1"
	bid, bok := model.NextBidPrice(1.23, 0)
	ask, aok := model.NextAskPrice(1.23, 0)
	requireModelPrice(t, bid, bok, "1.2", 2)
	requireModelPrice(t, ask, aok, "1.3", 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1164
//	test: invalid_tick_scheme_returns_error
func TestModelInvalidTickSchemeReturnsError(t *testing.T) {
	for _, name := range []string{"BOGUS", "FIXED_PRECISION_99"} {
		if _, err := ParseTickScheme(name); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1202
//	test: validate_negative_margin_init
func TestModelValidateNegativeMarginInit(t *testing.T) {
	common := validCommon()
	common.MarginInit = decimal.MustParse("-0.01")
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_init", "'margin_init' not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1227
//	test: validate_negative_margin_maint
func TestModelValidateNegativeMarginMaint(t *testing.T) {
	common := validCommon()
	common.MarginMaint = decimal.MustParse("-0.01")
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_maint", "'margin_maint' not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1252
//	test: validate_negative_max_qty
func TestModelValidateNegativeMaxQty(t *testing.T) {
	common := validCommon()
	zero := decimal.MustQuantity("0")
	common.MaxQuantity = &zero
	common.MarginInit, common.MarginMaint = decimal.Decimal{}, decimal.Decimal{}
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_init", "'margin_init' not positive")
}

func validCommon() InstrumentCommon {
	increment := decimal.MustPrice("0.01")
	return InstrumentCommon{
		PricePrecision: 2, SizePrecision: 2,
		SizeIncrement: decimal.MustQuantity("0.01"), Multiplier: decimal.MustQuantity("1"),
		MarginInit: decimal.MustParse("0.02"), MarginMaint: decimal.MustParse("0.01"),
		PriceIncrement: &increment,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1274
//	test: make_price_negative_rounding
func TestModelMakePriceNegativeRounding(t *testing.T) {
	if price := ethModel().MakePrice(-123.456789); price.Decimal().Sign() >= 0 {
		t.Fatalf("price=%v", price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1280
//	test: base_quantity_linear
func TestModelBaseQuantityLinear(t *testing.T) {
	model := linearModel()
	got := model.CalculateBaseQuantity(model.MakeQuantity(2, false), model.MakePrice(10000))
	if got.String() != "0.000200" {
		t.Fatalf("got=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1288
//	test: base_quantity_zero_last_price_returns_error
func TestModelBaseQuantityZeroLastPriceReturnsError(t *testing.T) {
	_, err := linearModel().TryCalculateBaseQuantity(decimal.MustQuantity("2.000000"), modelPrice(t, "0", 2))
	if err == nil || !strings.Contains(err.Error(), "`last_price` was zero") {
		t.Fatalf("err=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1303
//	test: make_price_invalid_value_returns_error
func TestModelMakePriceInvalidValueReturnsError(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), 1e30} {
		if _, err := linearModel().TryMakePrice(value); err == nil || !strings.Contains(err.Error(), "invalid `value` for make_price") {
			t.Fatalf("value=%v err=%v", value, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1315
//	test: make_qty_invalid_value_returns_error
func TestModelMakeQtyInvalidValueReturnsError(t *testing.T) {
	if _, err := linearModel().TryMakeQuantity(math.NaN(), false); err == nil || !strings.Contains(err.Error(), "invalid `value` for make_qty") {
		t.Fatalf("err=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1326
//	test: next_bid_prices_sequence
func TestModelNextBidPricesSequence(t *testing.T) {
	prices := linearModel().NextBidPrices(10000, 5)
	if len(prices) != 5 {
		t.Fatalf("len=%d", len(prices))
	}
	for i := 1; i < len(prices); i++ {
		if prices[i].Cmp(prices[i-1]) >= 0 {
			t.Fatalf("%v >= %v", prices[i], prices[i-1])
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1336
//	test: next_ask_prices_sequence
func TestModelNextAskPricesSequence(t *testing.T) {
	prices := linearModel().NextAskPrices(10000, 5)
	if len(prices) != 5 {
		t.Fatalf("len=%d", len(prices))
	}
	for i := 1; i < len(prices); i++ {
		if prices[i].Cmp(prices[i-1]) <= 0 {
			t.Fatalf("%v <= %v", prices[i], prices[i-1])
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1347
//	test: validate_price_increment_precision_mismatch
func TestModelValidatePriceIncrementPrecisionMismatch(t *testing.T) {
	common := validCommon()
	increment := modelPrice(t, ".001", 3)
	common.PriceIncrement = &increment
	common.MarginInit, common.MarginMaint = decimal.Decimal{}, decimal.Decimal{}
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_init", "'margin_init' not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1372
//	test: validate_min_price_exceeds_max_price
func TestModelValidateMinPriceExceedsMaxPrice(t *testing.T) {
	common := validCommon()
	minimum, maximum := modelPrice(t, "10", 2), modelPrice(t, "5", 2)
	common.MinPrice, common.MaxPrice = &minimum, &maximum
	common.MarginInit, common.MarginMaint = decimal.Decimal{}, decimal.Decimal{}
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_init", "'margin_init' not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1397
//	test: validate_instrument_common_ok
func TestModelValidateInstrumentCommonOK(t *testing.T) {
	if err := ValidateInstrumentCommon(validCommon()); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1419
//	test: validate_multiple_errors
func TestModelValidateMultipleErrors(t *testing.T) {
	common := validCommon()
	common.SizeIncrement = decimal.MustQuantity("0")
	common.Multiplier = decimal.MustQuantity("0")
	common.MarginInit = decimal.Decimal{}
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "size_increment", "not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1442
//	test: make_qty_boundary
func TestModelMakeQtyBoundary(t *testing.T) {
	model := linearModel()
	for _, tc := range []struct {
		roundDown bool
		want      string
	}{{false, "1.235000"}, {true, "1.234999"}} {
		if got := model.MakeQuantity(1.2349999, tc.roundDown).String(); got != tc.want {
			t.Fatalf("roundDown=%v got=%s want=%s", tc.roundDown, got, tc.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1456
//	test: make_price_rounding_parity
func TestModelMakePriceRoundingParity(t *testing.T) {
	model := linearModel()
	for _, tc := range []struct {
		input float64
		want  string
	}{{1.234999, "1.23"}, {1.235, "1.24"}, {1.235001, "1.24"}} {
		if got := model.MakePrice(tc.input).String(); got != tc.want {
			t.Fatalf("%v got=%s want=%s", tc.input, got, tc.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1466
//	test: make_price_half_even_parity
func TestModelMakePriceHalfEvenParity(t *testing.T) {
	model := linearModel()
	step, base, delta := .01, .42, .01/2000
	below := model.MakePrice(base + .5*step - delta)
	exact := model.MakePrice(base + .5*step)
	above := model.MakePrice(base + .5*step + delta)
	if !below.Equal(exact) || exact.Equal(above) {
		t.Fatalf("below=%v exact=%v above=%v", below, exact, above)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1486
//	test: is_quanto_flag
func TestModelIsQuantoFlag(t *testing.T) {
	if !quantoModel().IsQuanto() {
		t.Fatal("quanto model was not quanto")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1491
//	test: notional_quanto
func TestModelNotionalQuanto(t *testing.T) {
	model := quantoModel()
	got := model.CalculateNotionalValue(model.MakeQuantity(5, false), model.MakePrice(.036), false)
	want := money.MustNew(".18", model.SettlementCurrency)
	if !got.Equal(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1517
//	test: usd_equivalent_settlement_is_not_quanto
func TestModelUSDEquivalentSettlementIsNotQuanto(t *testing.T) {
	codes := []string{"BUSD", "FDUSD", "pUSD", "TUSD", "USD", "USDC", "USDC.e", "USDP", "USDT"}
	for _, pair := range [][2]string{
		{"USD", "BUSD"}, {"USD", "FDUSD"}, {"USD", "pUSD"}, {"USD", "TUSD"}, {"USD", "USD"},
		{"USD", "USDC"}, {"USD", "USDC.e"}, {"USD", "USDP"}, {"USD", "USDT"},
		{"BUSD", "USD"}, {"FDUSD", "USD"}, {"pUSD", "USD"}, {"TUSD", "USD"},
		{"USDC", "USD"}, {"USDC.e", "USD"}, {"USDP", "USD"}, {"USDT", "USD"},
	} {
		if !containsCode(codes, pair[0]) || !containsCode(codes, pair[1]) {
			t.Fatal("invalid test case")
		}
		base := modelCurrency("ETH", 8)
		quote, settlement := modelCurrency(pair[0], 2), modelCurrency(pair[1], 2)
		model := Model{
			PricePrecision: 2, SizePrecision: 0,
			PriceIncrement: modelPrice(t, ".01", 2), SizeIncrement: decimal.MustQuantity("1"),
			Multiplier: decimal.MustQuantity("1"), BaseCurrency: &base,
			QuoteCurrency: quote, SettlementCurrency: settlement,
		}
		got := model.CalculateNotionalValue(model.MakeQuantity(5, false), model.MakePrice(1000), false)
		if model.IsQuanto() || model.CostCurrency().Code != quote.Code ||
			!got.Equal(money.MustNew("5000", quote)) {
			t.Fatalf("%s/%s quanto=%v cost=%v notional=%v", pair[0], pair[1], model.IsQuanto(), model.CostCurrency(), got)
		}
	}
}

func containsCode(codes []string, want string) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1536
//	test: notional_inverse_base
func TestModelNotionalInverseBase(t *testing.T) {
	model := inverseModel()
	got := model.CalculateNotionalValue(model.MakeQuantity(100, false), model.MakePrice(50000), false)
	want := money.MustNew(".002", *model.BaseCurrency)
	if !got.Equal(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1548
//	test: notional_inverse_quote_use_quote
func TestModelNotionalInverseQuoteUseQuote(t *testing.T) {
	model := inverseModel()
	got := model.CalculateNotionalValue(model.MakeQuantity(100, false), model.MakePrice(50000), true)
	want := money.MustNew("100", model.QuoteCurrency)
	if !got.Equal(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1557
//	test: try_notional_inverse_zero_price_returns_error
func TestModelTryNotionalInverseZeroPriceReturnsError(t *testing.T) {
	model := inverseModel()
	_, err := model.TryCalculateNotionalValue(decimal.MustQuantity("100"), modelPrice(t, "0", 1), false)
	if err == nil || err.Error() != "price must be positive for inverse notional valuation" {
		t.Fatalf("err=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1571
//	test: try_notional_unrepresentable_money_returns_error
func TestModelTryNotionalUnrepresentableMoneyReturnsError(t *testing.T) {
	_, err := linearModel().TryCalculateNotionalValue(
		decimal.MustQuantity("100000000"), decimal.MustPrice("100000000"), false)
	if err == nil {
		t.Fatal("unrepresentable money accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1582
//	test: try_notional_decimal_overflow_returns_error
func TestModelTryNotionalDecimalOverflowReturnsError(t *testing.T) {
	_, err := TryNotionalValue(
		decimal.MustQuantity("9000000000"), decimal.MustPrice("9000000000"),
		decimal.MustQuantity("9000000000"), false, false, currency.USD())
	if err == nil || err.Error() != "notional calculation overflow" {
		t.Fatalf("err=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1600
//	test: validate_non_positive_max_price
func TestModelValidateNonPositiveMaxPrice(t *testing.T) {
	common := validCommon()
	zero := modelPrice(t, "0", 2)
	common.MaxPrice = &zero
	common.MarginInit, common.MarginMaint = decimal.Decimal{}, decimal.Decimal{}
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_init", "'margin_init' not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1625
//	test: validate_non_positive_max_notional
func TestModelValidateNonPositiveMaxNotional(t *testing.T) {
	common := validCommon()
	zero := money.MustNew("0", currency.USDT())
	common.MaxNotional = &zero
	common.MarginInit, common.MarginMaint = decimal.Decimal{}, decimal.Decimal{}
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_init", "'margin_init' not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1650
//	test: validate_price_increment_min_price_precision_mismatch
func TestModelValidatePriceIncrementMinPricePrecisionMismatch(t *testing.T) {
	common := validCommon()
	minimum := modelPrice(t, "1", 3)
	common.MinPrice = &minimum
	common.MarginInit, common.MarginMaint = decimal.Decimal{}, decimal.Decimal{}
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_init", "'margin_init' not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1676
//	test: validate_negative_min_notional
func TestModelValidateNegativeMinNotional(t *testing.T) {
	common := validCommon()
	negative := money.MustNew("-1", currency.USDT())
	positive := money.MustNew("1", currency.USDT())
	common.MinNotional, common.MaxNotional = &negative, &positive
	common.MarginInit, common.MarginMaint = decimal.Decimal{}, decimal.Decimal{}
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_init", "'margin_init' not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1710
//	test: base_qty_rounding
func TestModelBaseQtyRounding(t *testing.T) {
	model := linearModel()
	for precision := uint8(0); precision <= 8; precision++ {
		quantity, err := decimal.NewQuantity("1000", 8)
		if err != nil {
			t.Fatal(err)
		}
		price, err := decimal.NewPrice("2", 8)
		if err != nil {
			t.Fatal(err)
		}
		got := model.CalculateBaseQuantity(quantity, price)
		if got.Decimal().Cmp(decimal.MustParse("500")) != 0 {
			t.Fatalf("precision case %d got=%v", precision, got)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1724
//	test: make_price_qty_fuzz
func TestModelMakePriceQtyFuzz(t *testing.T) {
	model := linearModel()
	for _, input := range []float64{.0001, .0012345, 1, 42.424242, 99999.999999, 1e8 - 1} {
		price, priceErr := model.TryMakePrice(input)
		quantity, qtyErr := model.TryMakeQuantity(input, false)
		if priceErr != nil || qtyErr != nil || price.Decimal().String() == "" || quantity.Decimal().String() == "" {
			t.Fatalf("input=%v price=%v/%v quantity=%v/%v", input, price, priceErr, quantity, qtyErr)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1734
//	test: tick_walk_limits_btcusdt_ask
func TestModelTickWalkLimitsBTCUSDTAsk(t *testing.T) {
	model := boundedModel()
	maximum, _ := strconv.ParseFloat(model.MaxPrice.String(), 64)
	if _, ok := model.NextAskPrice(maximum, 1); ok {
		t.Fatal("ask beyond maximum returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1745
//	test: tick_walk_limits_ethusdt_ask
func TestModelTickWalkLimitsETHUSDTAsk(t *testing.T) {
	model := boundedModel()
	model.SizePrecision, model.SizeIncrement = 5, decimal.MustQuantity(".00001")
	maximum, _ := strconv.ParseFloat(model.MaxPrice.String(), 64)
	if _, ok := model.NextAskPrice(maximum, 1); ok {
		t.Fatal("ask beyond maximum returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1756
//	test: tick_walk_limits_btcusdt_bid
func TestModelTickWalkLimitsBTCUSDTBid(t *testing.T) {
	model := boundedModel()
	minimum, _ := strconv.ParseFloat(model.MinPrice.String(), 64)
	if _, ok := model.NextBidPrice(minimum, 1); ok {
		t.Fatal("bid below minimum returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1767
//	test: tick_walk_limits_ethusdt_bid
func TestModelTickWalkLimitsETHUSDTBid(t *testing.T) {
	model := boundedModel()
	model.SizePrecision, model.SizeIncrement = 5, decimal.MustQuantity(".00001")
	minimum, _ := strconv.ParseFloat(model.MinPrice.String(), 64)
	if _, ok := model.NextBidPrice(minimum, 1); ok {
		t.Fatal("bid below minimum returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1778
//	test: tick_walk_limits_quanto_ask
func TestModelTickWalkLimitsQuantoAsk(t *testing.T) {
	model := quantoModel()
	maximum := modelPrice(t, "1", 3)
	model.MaxPrice = &maximum
	if _, ok := model.NextAskPrice(1, 1); ok {
		t.Fatal("quanto ask beyond maximum returned a price")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1799
//	test: quantity_rounding_grid
func TestModelQuantityRoundingGrid(t *testing.T) {
	model := linearModel()
	for _, input := range []float64{.999999, 1.0000001, 1.2345, 2.3455, .000999999} {
		for _, down := range []bool{false, true} {
			quantity, err := model.TryMakeQuantity(input, down)
			if err != nil || quantity.Decimal().String() == "" {
				t.Fatalf("input=%v down=%v quantity=%v err=%v", input, down, quantity, err)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1809
//	test: pyo3_failure_validate_price_increment_max_price_precision_mismatch
func TestModelPyo3FailureValidatePriceIncrementMaxPricePrecisionMismatch(t *testing.T) {
	common := validCommon()
	maximum := modelPrice(t, "1", 3)
	common.MaxPrice = &maximum
	common.MarginInit, common.MarginMaint = decimal.Decimal{}, decimal.Decimal{}
	requireValidation(t, ValidateInstrumentCommon(common), "not_positive", "margin_init", "'margin_init' not positive")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1851
//	test: base_qty_rounding_high_dp
func TestModelBaseQtyRoundingHighDP(t *testing.T) {
	model := linearModel()
	for precision := uint8(9); precision <= decimal.MaxPrecision; precision++ {
		quantity, err := decimal.NewQuantity("1000", precision)
		if err != nil {
			t.Fatal(err)
		}
		price, err := decimal.NewPrice("2", precision)
		if err != nil {
			t.Fatal(err)
		}
		if got := model.CalculateBaseQuantity(quantity, price); got.Decimal().Cmp(decimal.MustParse("500")) != 0 {
			t.Fatalf("precision=%d got=%v", precision, got)
		}
	}
	// The Rust dp17 case converts through f64 before constructing at precision 8.
	quantity, _ := decimal.NewQuantity("1000", 8)
	price, _ := decimal.NewPrice("2", 8)
	if got := model.CalculateBaseQuantity(quantity, price); got.Decimal().Cmp(decimal.MustParse("500")) != 0 {
		t.Fatalf("dp17 parity got=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1864
//	test: check_positive_money_ok
func TestModelCheckPositiveMoneyOK(t *testing.T) {
	if err := money.CheckPositive(money.MustNew("100", currency.USDT()), "money"); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1871
//	test: check_positive_money_zero
func TestModelCheckPositiveMoneyZero(t *testing.T) {
	err := money.CheckPositive(money.MustNew("0", currency.USDT()), "money")
	if err == nil {
		t.Fatal("zero money accepted")
	}
	var typed money.NotPositiveError
	if !errors.As(err, &typed) {
		t.Fatalf("error=%T %v", err, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1878
//	test: check_positive_money_negative
func TestModelCheckPositiveMoneyNegative(t *testing.T) {
	err := money.CheckPositive(money.MustNew("-.01", currency.USDT()), "money")
	if err == nil {
		t.Fatal("negative money accepted")
	}
	var typed money.NotPositiveError
	if !errors.As(err, &typed) {
		t.Fatalf("error=%T %v", err, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1920
//	test: make_price_with_trailing_zeros_in_increment
func TestModelMakePriceWithTrailingZerosInIncrement(t *testing.T) {
	model := linearModel()
	model.PriceIncrement = modelPrice(t, ".50", 2)
	if model.MinPriceIncrementPrecision() != 1 {
		t.Fatalf("precision=%d", model.MinPriceIncrementPrecision())
	}
	for _, tc := range []struct {
		input float64
		want  string
	}{{1.234, "1.20"}, {1.25, "1.20"}, {1.35, "1.40"}} {
		got := model.MakePrice(tc.input)
		if got.String() != tc.want || got.Precision() != 2 {
			t.Fatalf("%v got=%v precision=%d", tc.input, got, got.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:1971
//	test: make_qty_with_trailing_zeros_in_increment
func TestModelMakeQtyWithTrailingZerosInIncrement(t *testing.T) {
	model := linearModel()
	model.SizePrecision, model.SizeIncrement = 2, decimal.MustQuantity(".50")
	if model.MinSizeIncrementPrecision() != 1 {
		t.Fatalf("precision=%d", model.MinSizeIncrementPrecision())
	}
	for _, tc := range []struct {
		input float64
		down  bool
		want  string
	}{{1.234, false, "1.20"}, {1.25, false, "1.20"}, {1.35, false, "1.40"}, {1.99, true, "1.90"}} {
		got := model.MakeQuantity(tc.input, tc.down)
		if got.String() != tc.want || got.Precision() != 2 {
			t.Fatalf("%v down=%v got=%v precision=%d", tc.input, tc.down, got, got.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/mod.rs:2037
//	test: test_instrument_class_has_expiration
func TestModelInstrumentClassHasExpiration(t *testing.T) {
	for _, tc := range []struct {
		class InstrumentClass
		want  bool
	}{
		{InstrumentClassFuture, true}, {InstrumentClassFuturesSpread, true},
		{InstrumentClassOption, true}, {InstrumentClassOptionSpread, true},
		{InstrumentClassSpot, false}, {InstrumentClassSwap, false},
		{InstrumentClassForward, false}, {InstrumentClassCFD, false},
		{InstrumentClassBond, false}, {InstrumentClassWarrant, false},
		{InstrumentClassSportsBetting, false}, {InstrumentClassBinaryOption, false},
	} {
		if got := tc.class.HasExpiration(); got != tc.want {
			t.Fatalf("%s got=%v want=%v", tc.class, got, tc.want)
		}
	}
}
