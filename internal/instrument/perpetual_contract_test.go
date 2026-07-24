package instrument

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func euroCurrency() currency.Currency {
	return currency.MustNew("EUR", 2, 978, "Euro", currency.Fiat)
}

func eurusdPerpetual(t *testing.T) PerpetualContract {
	t.Helper()
	base := euroCurrency()
	contract, err := NewCheckedPerpetualContract(PerpetualContractConfig{
		InstrumentID: ids.MustInstrumentID("EURUSD-PERP.AX"), RawSymbol: ids.MustSymbol("EURUSD-PERP"),
		Underlying: "EURUSD", AssetClass: AssetClassFX, BaseCurrency: &base,
		QuoteCurrency: currency.USD(), SettlementCurrency: currency.USD(),
		PricePrecision: 5, SizePrecision: 0,
		PriceIncrement: decimal.MustPrice("0.00001"), SizeIncrement: decimal.MustQuantity("1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func validPerpetualContractConfig() PerpetualContractConfig {
	base := euroCurrency()
	return PerpetualContractConfig{
		InstrumentID: ids.MustInstrumentID("TEST.EXCHANGE"), RawSymbol: ids.MustSymbol("TEST"),
		Underlying: "TEST", AssetClass: AssetClassFX, BaseCurrency: &base,
		QuoteCurrency: currency.USD(), SettlementCurrency: currency.USD(),
		PricePrecision: 5, SizePrecision: 0,
		PriceIncrement: decimal.MustPrice("0.00001"), SizeIncrement: decimal.MustQuantity("1"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/perpetual_contract.rs:529
//	test: test_trait_accessors
func TestPerpetualContractTraitAccessors(t *testing.T) {
	contract := eurusdPerpetual(t)
	if contract.InstrumentID != ids.MustInstrumentID("EURUSD-PERP.AX") ||
		contract.AssetClass != AssetClassFX || contract.InstrumentClass() != InstrumentClassSwap {
		t.Fatal("identity or class differs")
	}
	if contract.BaseCurrency == nil || !contract.BaseCurrency.Equal(euroCurrency()) ||
		!contract.QuoteCurrency.Equal(currency.USD()) || !contract.SettlementCurrency.Equal(currency.USD()) ||
		contract.Inverse || contract.PricePrecision != 5 || contract.SizePrecision != 0 {
		t.Fatal("currency, inverse, or precision differs")
	}
	if !contract.PriceIncrement.Equal(decimal.MustPrice("0.00001")) ||
		!contract.SizeIncrement.Equal(decimal.MustQuantity("1")) ||
		contract.Underlying != "EURUSD" {
		t.Fatal("increment or underlying differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/perpetual_contract.rs:566
//	test: test_new_checked_inverse_without_base_currency
func TestPerpetualContractNewCheckedInverseWithoutBaseCurrency(t *testing.T) {
	config := validPerpetualContractConfig()
	config.BaseCurrency = nil
	config.Inverse = true
	_, err := NewCheckedPerpetualContract(config)
	if err == nil || !strings.Contains(err.Error(), "base_currency") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/perpetual_contract.rs:602
//	test: test_new_checked_price_precision_mismatch
func TestPerpetualContractNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validPerpetualContractConfig()
	config.PricePrecision = 3
	if _, err := NewCheckedPerpetualContract(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/perpetual_contract.rs:639
//	test: test_new_checked_rejects_non_positive_sizing
func TestPerpetualContractNewCheckedRejectsNonPositiveSizing(t *testing.T) {
	zero := decimal.MustQuantity("0")
	for _, test := range []struct {
		name string
		set  func(*PerpetualContractConfig)
	}{
		{"zero multiplier", func(config *PerpetualContractConfig) { config.Multiplier = &zero }},
		{"zero lot size", func(config *PerpetualContractConfig) { config.LotSize = &zero }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validPerpetualContractConfig()
			test.set(&config)
			_, err := NewCheckedPerpetualContract(config)
			if err == nil || !strings.Contains(err.Error(), "not positive") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/perpetual_contract.rs:678
//	test: test_serialization_roundtrip
func TestPerpetualContractSerializationRoundtrip(t *testing.T) {
	original := eurusdPerpetual(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored PerpetualContract
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restoredData, _ := json.Marshal(restored)
	if !original.Equal(restored) || !bytes.Equal(data, restoredData) {
		t.Fatalf("round-trip changed contract:\n%s\n%s", data, restoredData)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/perpetual_contract.rs:685
//	test: test_builder_matches_new_checked
func TestPerpetualContractBuilderMatchesNewChecked(t *testing.T) {
	base := euroCurrency()
	multiplier, lot := decimal.MustQuantity("10"), decimal.MustQuantity("5")
	maxQty, minQty := decimal.MustQuantity("100"), decimal.MustQuantity("1")
	maxNotional, minNotional := money.MustNew("1000.0", currency.USD()), money.MustNew("10.0", currency.USD())
	maxPrice, minPrice := decimal.MustPrice("9.99999"), decimal.MustPrice("0.00002")
	marginInit, marginMaint := decimal.MustParse("0.01"), decimal.MustParse("0.02")
	makerFee, takerFee := decimal.MustParse("0.0002"), decimal.MustParse("0.0004")
	positional, err := NewCheckedPerpetualContract(PerpetualContractConfig{
		InstrumentID: ids.MustInstrumentID("EURUSD-PERP.AX"), RawSymbol: ids.MustSymbol("EURUSD-PERP"),
		Underlying: "EURUSD", AssetClass: AssetClassFX, BaseCurrency: &base,
		QuoteCurrency: currency.USD(), SettlementCurrency: currency.BTC(),
		PricePrecision: 5, SizePrecision: 0, PriceIncrement: decimal.MustPrice("0.00001"),
		SizeIncrement: decimal.MustQuantity("1"), Multiplier: &multiplier, LotSize: &lot,
		MaxQuantity: &maxQty, MinQuantity: &minQty, MaxNotional: &maxNotional, MinNotional: &minNotional,
		MaxPrice: &maxPrice, MinPrice: &minPrice, MarginInit: &marginInit, MarginMaint: &marginMaint,
		MakerFee: &makerFee, TakerFee: &takerFee, TsEvent: 1, TsInit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewPerpetualContractBuilder().
		Instrument(ids.MustInstrumentID("EURUSD-PERP.AX")).Symbol(ids.MustSymbol("EURUSD-PERP")).
		ForUnderlying("EURUSD").Class(AssetClassFX).Base(euroCurrency()).
		Quote(currency.USD()).Settlement(currency.BTC()).IsInverse(false).
		Precisions(5, 0).Increments(decimal.MustPrice("0.00001"), decimal.MustQuantity("1")).
		Sizing(decimal.MustQuantity("10"), decimal.MustQuantity("5")).
		QuantityLimits(decimal.MustQuantity("100"), decimal.MustQuantity("1")).
		NotionalLimits(money.MustNew("1000.0", currency.USD()), money.MustNew("10.0", currency.USD())).
		PriceLimits(decimal.MustPrice("9.99999"), decimal.MustPrice("0.00002")).
		Margins(decimal.MustParse("0.01"), decimal.MustParse("0.02")).
		Fees(decimal.MustParse("0.0002"), decimal.MustParse("0.0004")).Timestamps(1, 2).Build()
	if err != nil {
		t.Fatal(err)
	}
	positionalJSON, _ := json.Marshal(positional)
	builtJSON, _ := json.Marshal(built)
	if !bytes.Equal(positionalJSON, builtJSON) {
		t.Fatalf("builder differs:\n%s\n%s", positionalJSON, builtJSON)
	}
}
