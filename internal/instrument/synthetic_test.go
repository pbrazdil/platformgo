package instrument

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func syntheticInputs(values ...string) []decimal.Decimal {
	result := make([]decimal.Decimal, len(values))
	for index, value := range values {
		result[index] = decimal.MustParse(value)
	}
	return result
}

func requireSyntheticError(t *testing.T, err error, kind SyntheticErrorKind) *SyntheticError {
	t.Helper()
	var syntheticErr *SyntheticError
	if !errors.As(err, &syntheticErr) {
		t.Fatalf("error type = %T, want *SyntheticError", err)
	}
	if syntheticErr.Kind != kind {
		t.Fatalf("error kind = %q, want %q", syntheticErr.Kind, kind)
	}
	return syntheticErr
}

func manySynthetic(t *testing.T) (*Synthetic, []ids.InstrumentID) {
	t.Helper()
	components := make([]ids.InstrumentID, maxInlineComponents+2)
	terms := make([]string, len(components))
	for index := range components {
		components[index] = ids.MustInstrumentID(fmt.Sprintf("C%d.VENUE", index))
		terms[index] = components[index].String()
	}
	synthetic, err := NewSynthetic("BIG", 2, components, strings.Join(terms, " + "), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return synthetic, components
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:376
//	test: test_calculate_from_map
//
// Adaptations:
//   - Economic inputs use exact decimal strings instead of f64.
func TestSyntheticCalculateFromMap(t *testing.T) {
	synthetic := DefaultSynthetic()
	price, err := synthetic.CalculateFromMap(map[string]decimal.Decimal{
		"BTC.BINANCE": decimal.MustParse("100"),
		"LTC.BINANCE": decimal.MustParse("200"),
	})
	if err != nil || !price.Equal(decimal.MustPrice("150.0")) {
		t.Fatalf("price = %s, %v", price, err)
	}
	if synthetic.Formula != "(BTC.BINANCE + LTC.BINANCE) / 2.0" {
		t.Fatalf("formula = %q", synthetic.Formula)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:391
//	test: test_calculate
//
// Adaptations:
//   - Economic inputs use exact decimal strings instead of f64.
func TestSyntheticCalculate(t *testing.T) {
	price, err := DefaultSynthetic().Calculate(syntheticInputs("100", "200"))
	if err != nil || !price.Equal(decimal.MustPrice("150.0")) {
		t.Fatalf("price = %s, %v", price, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:399
//	test: test_change_formula
func TestSyntheticChangeFormula(t *testing.T) {
	synthetic := DefaultSynthetic()
	const formula = "(BTC.BINANCE + LTC.BINANCE) / 4"
	if err := synthetic.ChangeFormula(formula); err != nil {
		t.Fatal(err)
	}
	price, err := synthetic.Calculate(syntheticInputs("100", "200"))
	if err != nil || !price.Equal(decimal.MustPrice("75.0")) || synthetic.Formula != formula {
		t.Fatalf("price/formula = %s/%q, %v", price, synthetic.Formula, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:414
//	test: test_hyphenated_instrument_ids_preserve_raw_formula
func TestSyntheticHyphenatedInstrumentIDsPreserveRawFormula(t *testing.T) {
	components := []ids.InstrumentID{
		ids.MustInstrumentID("ETHUSDC-PERP.BINANCE_FUTURES"),
		ids.MustInstrumentID("ETH_USDC-PERP.HYPERLIQUID"),
	}
	formula := fmt.Sprintf("(%s + %s) / 2.0", components[0], components[1])
	synthetic, err := NewSynthetic("ETH-USDC", 2, components, formula, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	price, err := synthetic.Calculate(syntheticInputs("100", "200"))
	if err != nil || !price.Equal(decimal.MustPrice("150.0")) || synthetic.Formula != formula {
		t.Fatalf("price/formula = %s/%q, %v", price, synthetic.Formula, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:429
//	test: test_hyphenated_instrument_ids_support_legacy_sanitized_formula
func TestSyntheticHyphenatedInstrumentIDsSupportLegacySanitizedFormula(t *testing.T) {
	components := []ids.InstrumentID{
		ids.MustInstrumentID("ETH-USDT-SWAP.OKX"),
		ids.MustInstrumentID("ETH-USDC-PERP.HYPERLIQUID"),
	}
	formula := fmt.Sprintf("(%s + %s) / 2.0",
		strings.ReplaceAll(components[0].String(), "-", "_"),
		strings.ReplaceAll(components[1].String(), "-", "_"))
	synthetic, err := NewSynthetic("ETH-USD", 2, components, formula, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	price, err := synthetic.CalculateFromMap(map[string]decimal.Decimal{
		components[0].String(): decimal.MustParse("100"),
		components[1].String(): decimal.MustParse("200"),
	})
	if err != nil || !price.Equal(decimal.MustPrice("150.0")) || synthetic.Formula != formula {
		t.Fatalf("price/formula = %s/%q, %v", price, synthetic.Formula, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:457
//	test: test_slashed_instrument_ids_calculate_from_map
func TestSyntheticSlashedInstrumentIDsCalculateFromMap(t *testing.T) {
	components := []ids.InstrumentID{
		ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustInstrumentID("NZD/USD.SIM"),
	}
	formula := fmt.Sprintf("(%s + %s) / 2.0", components[0], components[1])
	synthetic, err := NewSynthetic("FX-BASKET", 5, components, formula, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	price, err := synthetic.CalculateFromMap(map[string]decimal.Decimal{
		components[0].String(): decimal.MustParse("0.65001"),
		components[1].String(): decimal.MustParse("0.59001"),
	})
	if err != nil || !price.Equal(decimal.MustPrice("0.62001")) || synthetic.Formula != formula {
		t.Fatalf("price/formula = %s/%q, %v", price, synthetic.Formula, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:482
//	test: test_new_checked_rejects_unknown_formula_symbol_with_expression_error
func TestSyntheticNewCheckedRejectsUnknownFormulaSymbol(t *testing.T) {
	_, err := NewSynthetic("BTC-LTC", 2, []ids.InstrumentID{
		ids.MustInstrumentID("BTC.BINANCE"), ids.MustInstrumentID("LTC.BINANCE"),
	}, "BTC.BINANCE + missing", 0, 0)
	syntheticErr := requireSyntheticError(t, err, SyntheticExpression)
	if syntheticErr.Error() != "Unknown symbol `missing`" {
		t.Fatalf("error = %q", syntheticErr)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:506
//	test: test_new_checked_rejects_invalid_precision_with_validation_error
func TestSyntheticNewCheckedRejectsInvalidPrecision(t *testing.T) {
	_, err := NewSynthetic("BTC-LTC", decimal.MaxPrecision+1, []ids.InstrumentID{
		ids.MustInstrumentID("BTC.BINANCE"), ids.MustInstrumentID("LTC.BINANCE"),
	}, "BTC.BINANCE + LTC.BINANCE", 0, 0)
	syntheticErr := requireSyntheticError(t, err, SyntheticValidation)
	if !strings.Contains(syntheticErr.Error(), "precision") {
		t.Fatalf("error = %q", syntheticErr)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:533
//	test: test_deserialize_rejects_unknown_formula_symbol
func TestSyntheticDeserializeRejectsUnknownFormulaSymbol(t *testing.T) {
	payload, err := json.Marshal(DefaultSynthetic())
	if err != nil {
		t.Fatal(err)
	}
	payload = []byte(strings.Replace(string(payload),
		"(BTC.BINANCE + LTC.BINANCE) / 2.0", "BTC.BINANCE + missing", 1))
	var decoded Synthetic
	err = json.Unmarshal(payload, &decoded)
	if err == nil || !strings.Contains(err.Error(), "Unknown symbol `missing`") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:549
//	test: test_calculate_rejects_wrong_input_count
func TestSyntheticCalculateRejectsWrongInputCount(t *testing.T) {
	_, err := DefaultSynthetic().Calculate(syntheticInputs("100"))
	syntheticErr := requireSyntheticError(t, err, SyntheticInputCountMismatch)
	if syntheticErr.Expected != 2 || syntheticErr.Actual != 1 ||
		syntheticErr.Error() != "Expected 2 input values, received 1" {
		t.Fatalf("error = %#v", syntheticErr)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:563
//	test: test_change_formula_rejects_invalid_formula_without_mutation
func TestSyntheticChangeFormulaRejectsInvalidFormulaWithoutMutation(t *testing.T) {
	synthetic := DefaultSynthetic()
	originalFormula := synthetic.Formula
	originalPrice, _ := synthetic.Calculate(syntheticInputs("100", "200"))
	err := synthetic.ChangeFormula("BTC.BINANCE + missing")
	requireSyntheticError(t, err, SyntheticExpression)
	currentPrice, currentErr := synthetic.Calculate(syntheticInputs("100", "200"))
	if currentErr != nil || synthetic.Formula != originalFormula || !currentPrice.Equal(originalPrice) {
		t.Fatalf("state mutated: formula=%q price=%s err=%v", synthetic.Formula, currentPrice, currentErr)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:581
//	test: test_calculate_from_map_rejects_missing_component
func TestSyntheticCalculateFromMapRejectsMissingComponent(t *testing.T) {
	_, err := DefaultSynthetic().CalculateFromMap(map[string]decimal.Decimal{
		"BTC.BINANCE": decimal.MustParse("100"),
	})
	syntheticErr := requireSyntheticError(t, err, SyntheticMissingInput)
	if syntheticErr.Component != "LTC.BINANCE" ||
		syntheticErr.Error() != "Missing price for component: LTC.BINANCE" {
		t.Fatalf("error = %#v", syntheticErr)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:601
//	test: test_calculate_from_map_fallback_rejects_missing_component
func TestSyntheticCalculateFromMapFallbackRejectsMissingComponent(t *testing.T) {
	synthetic, components := manySynthetic(t)
	inputs := make(map[string]decimal.Decimal)
	for _, component := range components[:len(components)-1] {
		inputs[component.String()] = decimal.MustParse("10")
	}
	_, err := synthetic.CalculateFromMap(inputs)
	syntheticErr := requireSyntheticError(t, err, SyntheticMissingInput)
	want := components[len(components)-1].String()
	if syntheticErr.Component != want || syntheticErr.Error() != "Missing price for component: "+want {
		t.Fatalf("error = %#v", syntheticErr)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:639
//	test: test_calculate_rejects_invalid_price_result
func TestSyntheticCalculateRejectsInvalidPriceResult(t *testing.T) {
	synthetic := DefaultSynthetic()
	if err := synthetic.ChangeFormula("BTC.BINANCE / (LTC.BINANCE - LTC.BINANCE)"); err != nil {
		t.Fatal(err)
	}
	_, err := synthetic.Calculate(syntheticInputs("100", "100"))
	syntheticErr := requireSyntheticError(t, err, SyntheticInvalidPrice)
	if !strings.Contains(syntheticErr.Error(), "Formula result produced invalid price") {
		t.Fatalf("error = %q", syntheticErr)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:662
//	test: test_is_valid_formula
func TestSyntheticIsValidFormula(t *testing.T) {
	synthetic := DefaultSynthetic()
	if !synthetic.IsValidFormula("(BTC.BINANCE + LTC.BINANCE) / 3") {
		t.Fatal("valid formula rejected")
	}
	if synthetic.IsValidFormula("UNKNOWN.VENUE + 1") || synthetic.IsValidFormula("") {
		t.Fatal("invalid formula accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:674
//	test: test_calculate_rejects_non_finite_inputs
//
// Adaptations:
//   - Exact decimal inputs cannot represent non-finite values; rejection occurs at parsing.
func TestSyntheticCalculateRejectsNonFiniteInputs(t *testing.T) {
	for _, value := range []string{"NaN", "Inf", "-Inf"} {
		if _, err := decimal.Parse(value); err == nil {
			t.Errorf("non-finite input %q parsed", value)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:692
//	test: test_components_with_colliding_legacy_aliases_coexist
func TestSyntheticComponentsWithCollidingLegacyAliasesCoexist(t *testing.T) {
	components := []ids.InstrumentID{
		ids.MustInstrumentID("FOO-BAR.VENUE"),
		ids.MustInstrumentID("FOO_BAR.VENUE"),
	}
	formula := fmt.Sprintf("%s + %s", components[0], components[1])
	synthetic, err := NewSynthetic("TEST", 2, components, formula, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	price, err := synthetic.Calculate(syntheticInputs("100", "200"))
	if err != nil || !price.Equal(decimal.MustPrice("300.0")) {
		t.Fatalf("price = %s, %v", price, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/synthetic.rs:710
//	test: test_calculate_from_map_fallback_for_many_components
func TestSyntheticCalculateFromMapFallbackForManyComponents(t *testing.T) {
	synthetic, components := manySynthetic(t)
	inputs := make(map[string]decimal.Decimal)
	for _, component := range components {
		inputs[component.String()] = decimal.MustParse("10")
	}
	price, err := synthetic.CalculateFromMap(inputs)
	if err != nil || !price.Equal(decimal.MustPrice("100.0")) {
		t.Fatalf("price = %s, %v", price, err)
	}
}
