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

const currencyPairSourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

func btcUSDTFixture(t *testing.T) CurrencyPair {
	t.Helper()
	maxQuantity := decimal.MustQuantity("9000")
	minQuantity := decimal.MustQuantity("0.000001")
	maxPrice := decimal.MustPrice("1000000")
	minPrice := decimal.MustPrice("0.01")
	margin := decimal.MustParse("0.001")
	pair, err := NewCheckedCurrencyPair(CurrencyPairConfig{
		InstrumentID: ids.MustInstrumentID("BTCUSDT.BINANCE"),
		RawSymbol:    ids.MustSymbol("BTCUSDT"),
		BaseCurrency: currency.BTC(), QuoteCurrency: currency.USDT(),
		PricePrecision: 2, SizePrecision: 6,
		PriceIncrement: decimal.MustPrice("0.01"),
		SizeIncrement:  decimal.MustQuantity("0.000001"),
		MaxQuantity:    &maxQuantity, MinQuantity: &minQuantity,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &margin, MarginMaint: &margin, MakerFee: &margin, TakerFee: &margin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func validCurrencyPairConfig() CurrencyPairConfig {
	return CurrencyPairConfig{
		InstrumentID: ids.MustInstrumentID("TEST.BINANCE"),
		RawSymbol:    ids.MustSymbol("TEST"),
		BaseCurrency: currency.BTC(), QuoteCurrency: currency.USDT(),
		PricePrecision: 2, SizePrecision: 6,
		PriceIncrement: decimal.MustPrice("0.01"),
		SizeIncrement:  decimal.MustQuantity("0.000001"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/currency_pair.rs:492
//	test: test_trait_accessors
func TestCurrencyPairTraitAccessors(t *testing.T) {
	pair := btcUSDTFixture(t)
	if pair.InstrumentID != ids.MustInstrumentID("BTCUSDT.BINANCE") ||
		pair.AssetClass() != AssetClassCryptocurrency ||
		pair.InstrumentClass() != InstrumentClassSpot {
		t.Fatal("identity or class accessor differs")
	}
	if !pair.BaseCurrency.Equal(currency.BTC()) ||
		!pair.QuoteCurrency.Equal(currency.USDT()) ||
		pair.IsInverse() {
		t.Fatal("currency or inverse accessor differs")
	}
	if pair.PricePrecision != 2 || pair.SizePrecision != 6 ||
		!pair.PriceIncrement.Equal(decimal.MustPrice("0.01")) ||
		!pair.SizeIncrement.Equal(decimal.MustQuantity("0.000001")) {
		t.Fatal("precision or increment accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/currency_pair.rs:518
//	test: test_new_checked_price_precision_mismatch
func TestCurrencyPairNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validCurrencyPairConfig()
	config.PricePrecision = 4
	if _, err := NewCheckedCurrencyPair(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/currency_pair.rs:551
//	test: test_new_checked_rejects_non_positive_sizing
func TestCurrencyPairNewCheckedRejectsNonPositiveSizing(t *testing.T) {
	zero := decimal.MustQuantity("0")
	for _, test := range []struct {
		name string
		set  func(*CurrencyPairConfig)
	}{
		{"zero multiplier", func(config *CurrencyPairConfig) { config.Multiplier = &zero }},
		{"zero lot size", func(config *CurrencyPairConfig) { config.LotSize = &zero }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validCurrencyPairConfig()
			test.set(&config)
			_, err := NewCheckedCurrencyPair(config)
			if err == nil || !strings.Contains(err.Error(), "not positive") {
				t.Fatalf("error = %v, want not-positive failure", err)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/currency_pair.rs:586
//	test: test_serialization_roundtrip
func TestCurrencyPairSerializationRoundtrip(t *testing.T) {
	pair := btcUSDTFixture(t)
	data, err := json.Marshal(pair)
	if err != nil {
		t.Fatal(err)
	}
	var deserialized CurrencyPair
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatal(err)
	}
	if !pair.Equal(deserialized) {
		t.Fatal("round-trip changed currency-pair identity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/currency_pair.rs:593
//	test: test_builder_matches_new_checked
func TestCurrencyPairBuilderMatchesNewChecked(t *testing.T) {
	multiplier := decimal.MustQuantity("10")
	lotSize := decimal.MustQuantity("5")
	maxQuantity := decimal.MustQuantity("9000.0")
	minQuantity := decimal.MustQuantity("0.000001")
	maxNotional := money.MustNew("1000000", currency.USDT())
	minNotional := money.MustNew("10", currency.USDT())
	maxPrice := decimal.MustPrice("1000000.00")
	minPrice := decimal.MustPrice("0.01")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")

	positional, err := NewCheckedCurrencyPair(CurrencyPairConfig{
		InstrumentID: ids.MustInstrumentID("BTCUSDT.BINANCE"),
		RawSymbol:    ids.MustSymbol("BTCUSDT"),
		BaseCurrency: currency.BTC(), QuoteCurrency: currency.USDT(),
		PricePrecision: 2, SizePrecision: 6,
		PriceIncrement: decimal.MustPrice("0.01"),
		SizeIncrement:  decimal.MustQuantity("0.000001"),
		Multiplier:     &multiplier, LotSize: &lotSize,
		MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
		MaxNotional: &maxNotional, MinNotional: &minNotional,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &marginInit, MarginMaint: &marginMaint,
		MakerFee: &makerFee, TakerFee: &takerFee,
		TsEvent: 1, TsInit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	built, err := NewCurrencyPairBuilder().
		Instrument(ids.MustInstrumentID("BTCUSDT.BINANCE")).
		Symbol(ids.MustSymbol("BTCUSDT")).
		Currencies(currency.BTC(), currency.USDT()).
		Precisions(2, 6).
		Increments(decimal.MustPrice("0.01"), decimal.MustQuantity("0.000001")).
		WithMultiplier(multiplier).
		WithLotSize(lotSize).
		QuantityLimits(maxQuantity, minQuantity).
		NotionalLimits(maxNotional, minNotional).
		PriceLimits(maxPrice, minPrice).
		Margins(marginInit, marginMaint).
		Fees(makerFee, takerFee).
		Timestamps(1, 2).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	positionalJSON, err := json.Marshal(positional)
	if err != nil {
		t.Fatal(err)
	}
	builtJSON, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(positionalJSON, builtJSON) {
		t.Fatalf("builder differs from checked constructor:\n%s\n%s", positionalJSON, builtJSON)
	}
}
