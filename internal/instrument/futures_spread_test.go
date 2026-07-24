package instrument

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func esFuturesSpread(t *testing.T) FuturesSpread {
	t.Helper()
	exchange := "XCME"
	spread, err := NewCheckedFuturesSpread(FuturesSpreadConfig{
		InstrumentID: ids.MustInstrumentID("ESM4-ESU4.GLBX"),
		RawSymbol:    ids.MustSymbol("ESM4-ESU4"), AssetClass: AssetClassIndex,
		Exchange: &exchange, Underlying: "ES", StrategyType: "EQ",
		Activation: 1_655_818_200_000_000_000, Expiration: 1_718_976_600_000_000_000,
		Currency: currency.USD(), PricePrecision: 2, PriceIncrement: decimal.MustPrice("0.01"),
		Multiplier: decimal.MustQuantity("1"), LotSize: decimal.MustQuantity("1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return spread
}

func validFuturesSpreadConfig() FuturesSpreadConfig {
	exchange := "XCME"
	return FuturesSpreadConfig{
		InstrumentID: ids.MustInstrumentID("TEST.GLBX"), RawSymbol: ids.MustSymbol("TEST"),
		AssetClass: AssetClassIndex, Exchange: &exchange, Underlying: "ES", StrategyType: "EQ",
		Currency: currency.USD(), PricePrecision: 2, PriceIncrement: decimal.MustPrice("0.01"),
		Multiplier: decimal.MustQuantity("1"), LotSize: decimal.MustQuantity("1"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/futures_spread.rs:495
//	test: test_trait_accessors
func TestFuturesSpreadTraitAccessors(t *testing.T) {
	spread := esFuturesSpread(t)
	if spread.InstrumentID != ids.MustInstrumentID("ESM4-ESU4.GLBX") ||
		spread.AssetClass != AssetClassIndex ||
		spread.InstrumentClass() != InstrumentClassFuturesSpread ||
		!spread.QuoteCurrency().Equal(currency.USD()) || spread.IsInverse() {
		t.Fatal("identity, class, or currency accessor differs")
	}
	if spread.Exchange == nil || *spread.Exchange != "XCME" ||
		spread.SizePrecision != 0 || !spread.SizeIncrement.Equal(decimal.MustQuantity("1")) ||
		spread.MinQuantity == nil || !spread.MinQuantity.Equal(decimal.MustQuantity("1")) ||
		spread.ActivationNanosValue() == nil || spread.ExpirationNanosValue() == nil {
		t.Fatal("spread metadata or size defaults differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/futures_spread.rs:513
//	test: test_new_checked_price_precision_mismatch
func TestFuturesSpreadNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validFuturesSpreadConfig()
	config.PricePrecision = 4
	if _, err := NewCheckedFuturesSpread(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/futures_spread.rs:545
//	test: test_new_checked_zero_multiplier
func TestFuturesSpreadNewCheckedZeroMultiplier(t *testing.T) {
	config := validFuturesSpreadConfig()
	config.Multiplier = decimal.MustQuantity("0")
	if _, err := NewCheckedFuturesSpread(config); err == nil {
		t.Fatal("expected zero multiplier error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/futures_spread.rs:577
//	test: test_serialization_roundtrip
func TestFuturesSpreadSerializationRoundtrip(t *testing.T) {
	original := esFuturesSpread(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored FuturesSpread
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restoredData, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !original.Equal(restored) || !bytes.Equal(data, restoredData) {
		t.Fatalf("round-trip changed spread:\n%s\n%s", data, restoredData)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/futures_spread.rs:584
//	test: test_builder_matches_new_checked
func TestFuturesSpreadBuilderMatchesNewChecked(t *testing.T) {
	exchange := "XCME"
	maxQuantity := decimal.MustQuantity("10000")
	minQuantity := decimal.MustQuantity("5")
	maxPrice := decimal.MustPrice("9999.99")
	minPrice := decimal.MustPrice("0.01")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")
	positional, err := NewCheckedFuturesSpread(FuturesSpreadConfig{
		InstrumentID: ids.MustInstrumentID("ESM4-ESU4.GLBX"),
		RawSymbol:    ids.MustSymbol("ESM4-ESU4"), AssetClass: AssetClassIndex,
		Exchange: &exchange, Underlying: "ES", StrategyType: "EQ",
		Activation: 1_000, Expiration: 2_000, Currency: currency.USD(),
		PricePrecision: 2, PriceIncrement: decimal.MustPrice("0.01"),
		Multiplier: decimal.MustQuantity("50"), LotSize: decimal.MustQuantity("10"),
		MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &marginInit, MarginMaint: &marginMaint,
		MakerFee: &makerFee, TakerFee: &takerFee, TsEvent: 1, TsInit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewFuturesSpreadBuilder().
		Instrument(ids.MustInstrumentID("ESM4-ESU4.GLBX")).
		Symbol(ids.MustSymbol("ESM4-ESU4")).
		Class(AssetClassIndex).
		OnExchange("XCME").
		ForUnderlying("ES").
		WithStrategy("EQ").
		ActiveBetween(1_000, 2_000).
		DenominatedIn(currency.USD()).
		PriceDigits(2).
		TickSize(decimal.MustPrice("0.01")).
		WithMultiplier(decimal.MustQuantity("50")).
		WithLotSize(decimal.MustQuantity("10")).
		WithMaxQuantity(decimal.MustQuantity("10000")).
		WithMinQuantity(decimal.MustQuantity("5")).
		WithMaxPrice(decimal.MustPrice("9999.99")).
		WithMinPrice(decimal.MustPrice("0.01")).
		Margins(decimal.MustParse("0.01"), decimal.MustParse("0.02")).
		Fees(decimal.MustParse("0.0002"), decimal.MustParse("0.0004")).
		Timestamps(1, 2).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	positionalJSON, _ := json.Marshal(positional)
	builtJSON, _ := json.Marshal(built)
	if !bytes.Equal(positionalJSON, builtJSON) {
		t.Fatalf("builder differs:\n%s\n%s", positionalJSON, builtJSON)
	}
}
