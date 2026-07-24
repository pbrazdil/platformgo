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

func aaplxCurrency() currency.Currency {
	return currency.MustNew("AAPLx", 8, 0, "AAPL Token", currency.Crypto)
}
func aaplxAsset(t *testing.T) TokenizedAsset {
	t.Helper()
	minQuantity := decimal.MustQuantity("0.0001")
	maker := decimal.MustParse("-0.0002")
	taker := decimal.MustParse("0.001")
	asset, err := NewCheckedTokenizedAsset(TokenizedAssetConfig{
		InstrumentID: ids.MustInstrumentID("AAPLx/USD.KRAKEN"), RawSymbol: ids.MustSymbol("AAPLxUSD"),
		AssetClass: AssetClassEquity, BaseCurrency: aaplxCurrency(), QuoteCurrency: currency.USD(),
		PricePrecision: 2, SizePrecision: 4, PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.0001"),
		MinQuantity: &minQuantity, MakerFee: &maker, TakerFee: &taker,
	})
	if err != nil {
		t.Fatal(err)
	}
	return asset
}
func validTokenizedConfig() TokenizedAssetConfig {
	return TokenizedAssetConfig{
		InstrumentID: ids.MustInstrumentID("TEST.KRAKEN"), RawSymbol: ids.MustSymbol("TEST"), AssetClass: AssetClassEquity,
		BaseCurrency: currency.BTC(), QuoteCurrency: currency.USD(), PricePrecision: 2, SizePrecision: 4,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.0001"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tokenized_asset.rs:510
//	test: test_trait_accessors
func TestTokenizedAssetTraitAccessors(t *testing.T) {
	asset := aaplxAsset(t)
	if asset.InstrumentID != ids.MustInstrumentID("AAPLx/USD.KRAKEN") || asset.AssetClass != AssetClassEquity ||
		asset.InstrumentClass() != InstrumentClassSpot || !asset.QuoteCurrency.Equal(currency.USD()) || asset.IsInverse() ||
		asset.PricePrecision != 2 || asset.SizePrecision != 4 {
		t.Fatal("accessors differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tokenized_asset.rs:527
//	test: test_new_checked_price_precision_mismatch
func TestTokenizedAssetNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validTokenizedConfig()
	config.PricePrecision = 4
	if _, err := NewCheckedTokenizedAsset(config); err == nil {
		t.Fatal("expected precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tokenized_asset.rs:560
//	test: test_new_checked_non_ascii_isin
func TestTokenizedAssetNewCheckedNonASCIIISIN(t *testing.T) {
	config := validTokenizedConfig()
	isin := "USé378331005"
	config.ISIN = &isin
	_, err := NewCheckedTokenizedAsset(config)
	if err == nil || !strings.Contains(err.Error(), "non-ASCII") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tokenized_asset.rs:596
//	test: test_new_checked_rejects_non_positive_sizing
func TestTokenizedAssetNewCheckedRejectsNonPositiveSizing(t *testing.T) {
	zero := decimal.MustQuantity("0")
	for _, test := range []struct {
		name string
		set  func(*TokenizedAssetConfig)
	}{
		{"zero multiplier", func(c *TokenizedAssetConfig) { c.Multiplier = &zero }},
		{"zero lot size", func(c *TokenizedAssetConfig) { c.LotSize = &zero }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validTokenizedConfig()
			test.set(&config)
			_, err := NewCheckedTokenizedAsset(config)
			if err == nil || !strings.Contains(err.Error(), "not positive") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tokenized_asset.rs:633
//	test: test_serialization_roundtrip
func TestTokenizedAssetSerializationRoundtrip(t *testing.T) {
	original := aaplxAsset(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored TokenizedAsset
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restoredData, _ := json.Marshal(restored)
	if !original.Equal(restored) || !bytes.Equal(data, restoredData) {
		t.Fatalf("round-trip differs:\n%s\n%s", data, restoredData)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/tokenized_asset.rs:640
//	test: test_builder_matches_new_checked
func TestTokenizedAssetBuilderMatchesNewChecked(t *testing.T) {
	isin := "US0378331005"
	multiplier, lot := decimal.MustQuantity("10"), decimal.MustQuantity("5")
	maxQty, minQty := decimal.MustQuantity("100"), decimal.MustQuantity("0.0001")
	maxNot, minNot := money.MustNew("1000.0", currency.USD()), money.MustNew("10.0", currency.USD())
	maxPrice, minPrice := decimal.MustPrice("999.99"), decimal.MustPrice("0.01")
	mi, mm, mf, tf := decimal.MustParse("0.01"), decimal.MustParse("0.02"), decimal.MustParse("0.0002"), decimal.MustParse("0.0004")
	positional, err := NewCheckedTokenizedAsset(TokenizedAssetConfig{
		InstrumentID: ids.MustInstrumentID("AAPLx/USD.KRAKEN"), RawSymbol: ids.MustSymbol("AAPLxUSD"), AssetClass: AssetClassEquity,
		BaseCurrency: currency.BTC(), QuoteCurrency: currency.USD(), ISIN: &isin, PricePrecision: 2, SizePrecision: 4,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.0001"), Multiplier: &multiplier, LotSize: &lot,
		MaxQuantity: &maxQty, MinQuantity: &minQty, MaxNotional: &maxNot, MinNotional: &minNot, MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &mi, MarginMaint: &mm, MakerFee: &mf, TakerFee: &tf, TsEvent: 1, TsInit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewTokenizedAssetBuilder().Instrument(ids.MustInstrumentID("AAPLx/USD.KRAKEN")).Symbol(ids.MustSymbol("AAPLxUSD")).
		Class(AssetClassEquity).Currencies(currency.BTC(), currency.USD()).WithISIN("US0378331005").Precisions(2, 4).
		Increments(decimal.MustPrice("0.01"), decimal.MustQuantity("0.0001")).Sizing(decimal.MustQuantity("10"), decimal.MustQuantity("5")).
		QuantityLimits(decimal.MustQuantity("100"), decimal.MustQuantity("0.0001")).
		NotionalLimits(money.MustNew("1000.0", currency.USD()), money.MustNew("10.0", currency.USD())).
		PriceLimits(decimal.MustPrice("999.99"), decimal.MustPrice("0.01")).Margins(mi, mm).Fees(mf, tf).Timestamps(1, 2).Build()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(positional)
	b, _ := json.Marshal(built)
	if !bytes.Equal(p, b) {
		t.Fatalf("builder differs:\n%s\n%s", p, b)
	}
}
